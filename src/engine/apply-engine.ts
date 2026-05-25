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

    for (const action of actions) {
      if (action.action === "destroy") {
        const files = state.getFilesForResource(action.resourceId);
        for (const file of files) {
          state.deleteGeneratedFile(file.path);
        }
        state.deleteResource(action.resourceId);
        state.recordAction(applyRecord.id, action.resourceId, "destroy", "success");
        result.destroyed++;
        continue;
      }

      const resource = registry.getById(action.resourceId);
      if (!resource) continue;

      const prompt = this.promptBuilder.build(resource, registry);
      const systemPrompt = this.promptBuilder.systemPrompt();

      const loopResult = await this.constraintLoop.run({
        resource,
        registry,
        llmClient,
        prompt,
        systemPrompt,
        maxRetries,
      });

      if (!loopResult.success) {
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
        continue;
      }

      // Upsert the resource first so generated_files FK constraint is satisfied
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
        } catch {
          // File does not exist yet
        }

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

      if (action.action === "create") result.created++;
      else result.modified++;
    }

    state.finishApply(applyRecord.id, result.failed > 0 ? "failed" : "ok");
    if (result.failed > 0) result.status = "failed";

    return result;
  }
}
