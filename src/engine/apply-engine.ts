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
import type { IWaveComputer } from "./wave-computer.js";
import type { IWaveVerifier } from "./wave-verifier.js";
import type { PlannedAction } from "../planner/plan.js";
import type { ResourceDescriptor } from "../types.js";

export interface ApplyOptions {
  target?: string;
  force?: boolean;
  maxRetries?: number;
  outputDir?: string;
  concurrency?: number;
  waveVerifyCommand?: string[];
  waveTestCommand?: string[];
  waveMaxRetries?: number;
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

interface ActionContext {
  registry: IResourceRegistry;
  state: IStateDatabase;
  llmClient: ILlmClient;
  options: ApplyOptions;
  result: ApplyResult;
  applyRecord: { id: number };
  effectiveHashes: Map<string, string>;
  generates: PlannedAction[];
  appendErrorLog: (resourceId: string, error: string) => Promise<void>;
  fileToResource: Map<string, string>;
}

export class ApplyEngine implements IApplyEngine {
  constructor(
    private readonly planner: IPlanner,
    private readonly promptBuilder: IPromptBuilder,
    private readonly constraintLoop: IConstraintLoop,
    private readonly hashComputer: IHashComputer,
    private readonly waveComputer?: IWaveComputer,
    private readonly waveVerifier?: IWaveVerifier,
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

    const ctx: ActionContext = {
      registry,
      state,
      llmClient,
      options: { ...options, maxRetries, outputDir, concurrency },
      result,
      applyRecord,
      effectiveHashes,
      generates,
      appendErrorLog,
      fileToResource: new Map(),
    };

    if (this.waveComputer && options.waveVerifyCommand) {
      await this.applyInWaves(ctx);
    } else {
      await this.applyFlat(ctx);
    }

    state.finishApply(applyRecord.id, result.failed > 0 ? "failed" : "ok");
    if (result.failed > 0) result.status = "failed";

    return result;
  }

  private async applyFlat(ctx: ActionContext): Promise<void> {
    const { generates, options } = ctx;
    const concurrency = options.concurrency ?? 1;

    console.log(`\nPlan: ${generates.length} resources to generate (concurrency: ${concurrency})`);

    const pending: Promise<void>[] = [];
    for (let i = 0; i < generates.length; i++) {
      const p = this.processAction(generates[i], i, ctx);
      pending.push(p);
      if (pending.length >= concurrency) {
        await Promise.race(pending);
        for (let j = pending.length - 1; j >= 0; j--) {
          const settled = await Promise.race([pending[j].then(() => true), Promise.resolve(false)]);
          if (settled) pending.splice(j, 1);
        }
      }
    }
    await Promise.all(pending);
  }

  private async applyInWaves(ctx: ActionContext): Promise<void> {
    const { generates, options } = ctx;
    const concurrency = options.concurrency ?? 1;
    const waves = this.waveComputer!.compute(generates, ctx.registry);
    const waveMaxRetries = options.waveMaxRetries ?? 2;

    console.log(`\nPlan: ${generates.length} resources in ${waves.length} waves (concurrency: ${concurrency})`);

    let globalIndex = 0;
    for (let w = 0; w < waves.length; w++) {
      const wave = waves[w];
      console.log(`\n${"═".repeat(60)}`);
      console.log(`Wave ${w + 1}/${waves.length}: ${wave.length} resources`);
      console.log("═".repeat(60));

      const pending: Promise<void>[] = [];
      for (let i = 0; i < wave.length; i++) {
        const p = this.processAction(wave[i], globalIndex + i, ctx);
        pending.push(p);
        if (pending.length >= concurrency) {
          await Promise.race(pending);
          for (let j = pending.length - 1; j >= 0; j--) {
            const settled = await Promise.race([pending[j].then(() => true), Promise.resolve(false)]);
            if (settled) pending.splice(j, 1);
          }
        }
      }
      await Promise.all(pending);

      const verifySteps: { label: string; command: string[] }[] = [];
      if (options.waveVerifyCommand) {
        verifySteps.push({ label: "type check", command: options.waveVerifyCommand });
      }
      if (options.waveTestCommand) {
        verifySteps.push({ label: "tests", command: options.waveTestCommand });
      }

      if (verifySteps.length > 0 && this.waveVerifier) {
        const allFileMap = new Map<string, string>(ctx.fileToResource);

        let verified = false;
        for (let attempt = 0; attempt <= waveMaxRetries; attempt++) {
          let stepFailed = false;

          for (const step of verifySteps) {
            const tag = `Wave ${w + 1} ${step.label}`;
            console.log(`\n  ${tag}${attempt > 0 ? ` (retry ${attempt})` : ""}...`);
            const verifyResult = await this.waveVerifier.verify(
              allFileMap,
              step.command,
              options.outputDir ?? ".",
            );

            if (verifyResult.passed) {
              console.log(`  ${tag} passed`);
              continue;
            }

            console.log(`  ${tag} failed (${verifyResult.errors.length} errors)`);
            stepFailed = true;

            if (attempt >= waveMaxRetries) {
              console.log(`  Wave ${w + 1} exhausted ${waveMaxRetries} retries`);
              for (const err of verifyResult.errors) {
                if (err.resourceId !== "__unknown__") {
                  ctx.result.errors.push(`${err.resourceId}: ${err.errorText}`);
                }
              }
              ctx.result.failed += wave.length;
              ctx.result.status = "failed";
              return;
            }

            const waveResourceIds = new Set(wave.map((a) => a.resourceId));
            const failedIds = new Set(
              verifyResult.errors
                .map((e) => e.resourceId)
                .filter((id) => id !== "__unknown__" && waveResourceIds.has(id)),
            );

            if (failedIds.size === 0) {
              failedIds.add(wave[0].resourceId);
            }

            console.log(`  Retrying ${failedIds.size} resource(s): ${[...failedIds].join(", ")}`);

            for (const resourceId of failedIds) {
              const action = wave.find((a) => a.resourceId === resourceId);
              if (!action) continue;

              const resource = ctx.registry.getById(resourceId);
              if (!resource) continue;

              const waveErrors = verifyResult.errors
                .filter((e) => e.resourceId === resourceId || e.resourceId === "__unknown__")
                .map((e) => e.errorText)
                .join("\n");

              await this.processAction(
                action,
                globalIndex + wave.indexOf(action),
                ctx,
                waveErrors,
              );
            }
            break;
          }

          if (!stepFailed) {
            verified = true;
            break;
          }
        }

        if (!verified) {
          ctx.result.status = "failed";
          return;
        }
      }

      globalIndex += wave.length;
    }
  }

  private async processAction(
    action: PlannedAction,
    index: number,
    ctx: ActionContext,
    waveError?: string,
  ): Promise<void> {
    const resource = ctx.registry.getById(action.resourceId);
    if (!resource) return;

    const label = `[${index + 1}/${ctx.generates.length}]`;
    console.log(`\n  ${label} ${action.action} ${action.resourceId}${waveError ? " (wave retry)" : ""}`);

    let prompt = this.promptBuilder.build(resource, ctx.registry);
    const systemPrompt = this.promptBuilder.systemPrompt();

    if (waveError) {
      prompt = this.buildWaveFixPrompt(prompt, waveError, resource);
    }

    let loopResult;
    try {
      loopResult = await this.constraintLoop.run({
        resource,
        registry: ctx.registry,
        llmClient: ctx.llmClient,
        prompt,
        systemPrompt,
        maxRetries: ctx.options.maxRetries ?? 3,
      });
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      console.log(`  ${label} CRASHED: ${msg.slice(0, 120)}`);
      if (!waveError) {
        ctx.result.failed++;
        ctx.result.errors.push(`${action.resourceId}: ${msg}`);
        ctx.state.recordAction(ctx.applyRecord.id, action.resourceId, action.action, "failed");
      }
      await ctx.appendErrorLog(action.resourceId, `CRASH: ${msg}\n\nPrompt:\n${prompt}`);
      return;
    }

    if (!loopResult.success) {
      console.log(`  ${label} FAILED: ${loopResult.lastError?.slice(0, 120)}`);
      if (!waveError) {
        ctx.result.failed++;
        ctx.result.errors.push(`${action.resourceId}: ${loopResult.lastError}`);
        ctx.state.recordAction(ctx.applyRecord.id, action.resourceId, action.action, "failed");
      }
      ctx.state.recordGeneration({
        apply_id: ctx.applyRecord.id,
        resource_id: action.resourceId,
        model: ctx.llmClient.modelId,
        prompt_hash: createHash("sha256").update(prompt).digest("hex"),
        prompt_text: prompt,
        output_text: "",
        retries: loopResult.retries,
        outcome: "rejected",
        rejection_reason: loopResult.lastError,
        created_at: new Date().toISOString(),
      });
      await ctx.appendErrorLog(action.resourceId, `INVARIANT: ${loopResult.lastError}\n\nPrompt:\n${prompt}`);
      return;
    }

    const outputDir = ctx.options.outputDir ?? ".";

    ctx.state.upsertResource({
      id: resource.id,
      kind: resource.kind,
      context: resource.context,
      declaration_hash: createHash("sha256")
        .update(JSON.stringify(resource.declaration))
        .digest("hex"),
      effective_hash: ctx.effectiveHashes.get(resource.id)!,
      declaration_json: JSON.stringify(resource.declaration),
      layer: resource.layer,
      settled_at: new Date().toISOString(),
      last_apply_id: ctx.applyRecord.id,
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

      ctx.state.upsertGeneratedFile({
        path: filePath,
        resource_id: action.resourceId,
        content_hash: contentHash,
        generator: "llm",
        model: ctx.llmClient.modelId,
        prompt_hash: createHash("sha256").update(prompt).digest("hex"),
        generated_at: new Date().toISOString(),
      });

      ctx.fileToResource.set(filePath, action.resourceId);
    }

    if (!waveError) {
      ctx.state.recordAction(ctx.applyRecord.id, action.resourceId, action.action, "success");
    }
    ctx.state.recordGeneration({
      apply_id: ctx.applyRecord.id,
      resource_id: action.resourceId,
      model: ctx.llmClient.modelId,
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
    console.log(`  ${label} OK → ${fileList}`);
    if (!waveError) {
      if (action.action === "create") ctx.result.created++;
      else ctx.result.modified++;
    }
  }

  private buildWaveFixPrompt(
    originalPrompt: string,
    waveErrors: string,
    resource: ResourceDescriptor,
  ): string {
    return [
      originalPrompt,
      "\n## Build Errors From Previous Attempt\n",
      `The previous generation of ${resource.id} caused build errors when compiled with the rest of the project.`,
      "Fix these errors in your output:\n",
      waveErrors,
    ].join("\n");
  }
}
