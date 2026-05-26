import { createHash } from "crypto";
import { mkdir } from "fs/promises";
import { dirname, join } from "path";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { IStateDatabase } from "../state/state-database.js";
import type { IPlanner } from "../planner/planner.js";
import type { IHashComputer } from "../planner/hash-computer.js";
import type { IPromptBuilder } from "./prompt-builder.js";
import type { IConstraintLoop } from "./constraint-loop.js";
import type { ILlmClient } from "./llm-client.js";

export interface ApplyOptions {
  target?: string;
  force?: boolean;
  maxRetries?: number;
  outputDir?: string;
  concurrency?: number;
}

export interface ApplyResult {
  status: "ok" | "failed";
  created: number;
  modified: number;
  destroyed: number;
  failed: number;
  errors: string[];
}

export interface IApplyEngine {
  apply(
    registry: IResourceRegistry,
    state: IStateDatabase,
    llmClient: ILlmClient,
    options?: ApplyOptions,
  ): Promise<ApplyResult>;
}

export class ApplyEngine implements IApplyEngine {
  constructor(
    private readonly planner: IPlanner,
    private readonly promptBuilder: IPromptBuilder,
    private readonly constraintLoop: IConstraintLoop,
    private readonly hashComputer: IHashComputer,
  ) {}

  async apply(
    registry: IResourceRegistry,
    state: IStateDatabase,
    llmClient: ILlmClient,
    options: ApplyOptions = {},
  ): Promise<ApplyResult> {
    const maxRetries = options.maxRetries ?? 3;
    const outputDir = options.outputDir ?? ".";
    const concurrency = options.concurrency ?? 1;

    const plan = this.planner.plan(registry, state);
    const effectiveHashes = this.hashComputer.computeAll(registry);

    let actions = plan.actions;
    if (options.target) {
      actions = actions.filter(
        (a) => a.resourceId === options.target || a.cascadedFrom === options.target,
      );
    }

    const applyRecord = state.beginApply(
      createHash("sha256")
        .update(JSON.stringify(registry.getAll().map((r) => r.id)))
        .digest("hex"),
    );

    const result: ApplyResult = {
      status: "ok",
      created: 0,
      modified: 0,
      destroyed: 0,
      failed: 0,
      errors: [],
    };

    // Handle destroys first (fast, no LLM)
    const destroys = actions.filter((a) => a.action === "destroy");
    const generates = actions.filter((a) => a.action !== "destroy");

    for (const action of destroys) {
      console.log(`  [destroy] ${action.resourceId}`);
      const files = state.getFilesForResource(action.resourceId);
      for (const file of files) {
        state.deleteGeneratedFile(file.path);
      }
      state.deleteResource(action.resourceId);
      state.recordAction(applyRecord.id, action.resourceId, "destroy", "success");
      result.destroyed++;
    }

    const errorLogPath = join(outputDir, "apply-errors.log");
    await Bun.write(errorLogPath, `Apply started at ${new Date().toISOString()}\n\n`);

    const appendErrorLog = async (resourceId: string, error: string) => {
      const entry = `--- ${resourceId} ---\n${error}\n\n`;
      const existing = await Bun.file(errorLogPath).text();
      await Bun.write(errorLogPath, existing + entry);
    };

    console.log(`\nPlan: ${generates.length} resources to generate (concurrency: ${concurrency})`);
    console.log(`Error log: ${errorLogPath}`);
    let completed = 0;

    const processAction = async (action: (typeof generates)[0], index: number) => {
      const resource = registry.getById(action.resourceId);
      if (!resource) return;

      const label = `[${index + 1}/${generates.length}]`;
      console.log(`\n  ${label} ${action.action} ${action.resourceId}`);

      const prompt = this.promptBuilder.build(resource, registry);
      const systemPrompt = this.promptBuilder.systemPrompt();

      let loopResult;
      try {
        loopResult = await this.constraintLoop.run({
          resource,
          registry,
          llmClient,
          prompt,
          systemPrompt,
          maxRetries,
        });
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        console.log(`  ${label} CRASHED: ${msg.slice(0, 120)}`);
        result.failed++;
        result.errors.push(`${action.resourceId}: ${msg}`);
        state.recordAction(applyRecord.id, action.resourceId, action.action, "failed");
        await appendErrorLog(action.resourceId, `CRASH: ${msg}\n\nPrompt:\n${prompt}`);
        completed++;
        return;
      }

      if (!loopResult.success) {
        console.log(`  ${label} FAILED: ${loopResult.lastError?.slice(0, 120)}`);
        result.failed++;
        result.errors.push(`${action.resourceId}: ${loopResult.lastError}`);
        state.recordAction(applyRecord.id, action.resourceId, action.action, "failed");
        state.recordGeneration({
          apply_id: applyRecord.id,
          resource_id: action.resourceId,
          model: llmClient.modelId,
          prompt_hash: createHash("sha256").update(prompt).digest("hex"),
          prompt_text: prompt,
          output_text: "",
          retries: loopResult.retries,
          outcome: "rejected",
          rejection_reason: loopResult.lastError,
          created_at: new Date().toISOString(),
        });
        await appendErrorLog(action.resourceId, `INVARIANT: ${loopResult.lastError}\n\nPrompt:\n${prompt}`);
        completed++;
        return;
      }

      state.upsertResource({
        id: resource.id,
        kind: resource.kind,
        context: resource.context,
        declaration_hash: createHash("sha256")
          .update(JSON.stringify(resource.declaration))
          .digest("hex"),
        effective_hash: effectiveHashes.get(resource.id)!,
        declaration_json: JSON.stringify(resource.declaration),
        layer: resource.layer,
        settled_at: new Date().toISOString(),
        last_apply_id: applyRecord.id,
      });

      for (const [filePath, content] of loopResult.files!) {
        const fullPath = join(outputDir, filePath);
        await mkdir(dirname(fullPath), { recursive: true });

        const contentHash = createHash("sha256").update(content).digest("hex");
        let existingHash: string | null = null;
        try {
          const existing = await Bun.file(fullPath).text();
          existingHash = createHash("sha256").update(existing).digest("hex");
        } catch {}

        if (existingHash !== contentHash) {
          await Bun.write(fullPath, content);
        }

        state.upsertGeneratedFile({
          path: filePath,
          resource_id: action.resourceId,
          content_hash: contentHash,
          generator: "llm",
          model: llmClient.modelId,
          prompt_hash: createHash("sha256").update(prompt).digest("hex"),
          generated_at: new Date().toISOString(),
        });
      }

      state.recordAction(applyRecord.id, action.resourceId, action.action, "success");
      state.recordGeneration({
        apply_id: applyRecord.id,
        resource_id: action.resourceId,
        model: llmClient.modelId,
        prompt_hash: createHash("sha256").update(prompt).digest("hex"),
        prompt_text: prompt,
        output_text: [...loopResult.files!.entries()]
          .map(([p, c]) => `// path: ${p}\n${c}`)
          .join("\n---\n"),
        retries: loopResult.retries,
        outcome: "accepted",
        rejection_reason: null,
        created_at: new Date().toISOString(),
      });

      const fileList = [...loopResult.files!.keys()].join(", ");
      completed++;
      console.log(`  ${label} OK → ${fileList} (${completed}/${generates.length} done)`);
      if (action.action === "create") result.created++;
      else result.modified++;
    };

    // Run with concurrency limit
    const pending: Promise<void>[] = [];
    for (let i = 0; i < generates.length; i++) {
      const p = processAction(generates[i], i);
      pending.push(p);
      if (pending.length >= concurrency) {
        await Promise.race(pending);
        // Remove settled promises
        for (let j = pending.length - 1; j >= 0; j--) {
          const settled = await Promise.race([pending[j].then(() => true), Promise.resolve(false)]);
          if (settled) pending.splice(j, 1);
        }
      }
    }
    await Promise.all(pending);

    state.finishApply(applyRecord.id, result.failed > 0 ? "failed" : "ok");
    if (result.failed > 0) result.status = "failed";

    return result;
  }
}
