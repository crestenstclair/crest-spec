import { createHash } from "crypto";
import type { IStateDatabase } from "../state/state-database.js";
import type { IPlanner } from "../planner/planner.js";
import type { IHashComputer } from "../planner/hash-computer.js";
import type { IWaveComputer } from "./wave-computer.js";
import type { IPromptBuilder } from "./prompt-builder.js";
import type { IResourceValidator, ValidationResult } from "./resource-validator.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { PlannedAction } from "../planner/plan.js";
import type { SubAgentState, BlockedContext, ErrorContext } from "../state/types.js";

export interface BeginResult {
  applyId: number;
  plan: { resourceId: string; action: string; reason: string }[];
  waves: string[][];
  totalResources: number;
  orchestratorInstructions: string;
}

export interface NextResult {
  wave: number;
  resources: { resourceId: string; action: string; reason: string }[];
  done: boolean;
}

export interface ContextResult {
  resourceId: string;
  systemPrompt: string;
  prompt: string;
  dependencyNotes: Record<string, string[]>;
  dispatchInstructions: string;
  validations: import("../types.js").ValidationDeclaration[];
}

export interface NoteResult {
  resourceId: string;
  noteId: number;
  saved: boolean;
}

export interface CommitResult {
  resourceId: string;
  committed: boolean;
  filesRecorded: string[];
}

export interface FinishResult {
  applyId: number;
  status: string;
  created: number;
  modified: number;
  destroyed: number;
  failed: number;
}

export interface DispatchResult {
  resourceId: string;
  state: "dispatched";
}

export interface ResourceStatus {
  resourceId: string;
  state: SubAgentState;
  action: string;
  reason: string;
  context?: BlockedContext | ErrorContext;
}

export interface WaveStatusResult {
  wave: number;
  resources: ResourceStatus[];
  summary: Record<SubAgentState, number>;
  done: boolean;
  needsAttention: ResourceStatus[];
}

export class AgentSession {
  constructor(
    private readonly state: IStateDatabase,
    private readonly planner: IPlanner,
    private readonly waveComputer: IWaveComputer,
    private readonly hashComputer: IHashComputer,
    private readonly promptBuilder: IPromptBuilder,
    private readonly validator: IResourceValidator,
  ) {}

  begin(registry: IResourceRegistry, options?: { target?: string; force?: boolean }): BeginResult {
    if (!this.state.acquireLock(`agent:${process.pid}`)) {
      const lock = this.state.getLock();
      throw new Error(`Apply is locked by ${lock?.holder} since ${lock?.acquired_at}`);
    }

    const plan = this.planner.plan(registry, this.state);
    const effectiveHashes = this.hashComputer.computeAll(registry);

    let generates = plan.actions.filter((a) => a.action !== "destroy");
    if (options?.target) {
      generates = generates.filter(
        (a) => a.resourceId === options.target || a.cascadedFrom === options.target,
      );
    }

    const waves = this.waveComputer.compute(generates, registry);
    const waveIds = waves.map((w) => w.map((a) => a.resourceId));

    const specHash = createHash("sha256")
      .update(JSON.stringify(registry.getAll().map((r) => r.id)))
      .digest("hex");

    const applyRecord = this.state.beginApply(specHash);

    const planSummary = generates.map((a) => ({
      resourceId: a.resourceId,
      action: a.action,
      reason: a.reason,
    }));

    const hashObj: Record<string, string> = {};
    for (const [k, v] of effectiveHashes) {
      hashObj[k] = v;
    }

    this.state.createAgentSession({
      apply_id: applyRecord.id,
      plan_json: JSON.stringify(generates),
      waves_json: JSON.stringify(waveIds),
      hashes_json: JSON.stringify(hashObj),
      created_at: new Date().toISOString(),
    });

    for (const action of generates) {
      this.state.setResourceState(applyRecord.id, action.resourceId, "pending");
    }

    return {
      applyId: applyRecord.id,
      plan: planSummary,
      waves: waveIds,
      totalResources: generates.length,
      orchestratorInstructions: AgentSession.orchestratorInstructions(),
    };
  }

  next(): NextResult {
    const session = this.requireSession();

    const waves: string[][] = JSON.parse(session.waves_json);
    const committedActions = this.state.getApplyActions(session.apply_id);
    const committedIds = new Set(
      committedActions.filter((a) => a.outcome === "success").map((a) => a.resource_id),
    );

    const planActions: PlannedAction[] = JSON.parse(session.plan_json);
    const actionMap = new Map(planActions.map((a) => [a.resourceId, a]));

    for (let w = 0; w < waves.length; w++) {
      const uncommitted = waves[w].filter((id) => !committedIds.has(id));
      if (uncommitted.length > 0) {
        return {
          wave: w,
          resources: uncommitted.map((id) => {
            const action = actionMap.get(id);
            return {
              resourceId: id,
              action: action?.action ?? "create",
              reason: action?.reason ?? "unknown",
            };
          }),
          done: false,
        };
      }
    }

    return { wave: -1, resources: [], done: true };
  }

  context(resourceId: string, registry: IResourceRegistry): ContextResult {
    this.requireSession();

    const resource = registry.getById(resourceId);
    if (!resource) {
      throw new Error(`Resource ${resourceId} not found in registry`);
    }

    const systemPrompt = this.promptBuilder.systemPrompt();
    let prompt = this.promptBuilder.build(resource, registry);

    const dependencyNotes: Record<string, string[]> = {};
    for (const dep of resource.dependencies) {
      const notes = this.state.getLatestAgentNotes(dep.targetId);
      if (notes.length > 0) {
        dependencyNotes[dep.targetId] = notes.map((n) => n.content);
      }
    }

    if (Object.keys(dependencyNotes).length > 0) {
      const sections: string[] = ["\n\n## Notes from dependencies"];
      sections.push("Design decisions and context from agents that implemented upstream resources:\n");
      for (const [depId, noteTexts] of Object.entries(dependencyNotes)) {
        sections.push(`### ${depId}`);
        for (const note of noteTexts) {
          sections.push(`- ${note}`);
        }
        sections.push("");
      }
      prompt += sections.join("\n");
    }

    return {
      resourceId,
      systemPrompt,
      prompt,
      dependencyNotes,
      dispatchInstructions: AgentSession.dispatchInstructions(resourceId),
      validations: resource.validations ?? [],
    };
  }

  note(resourceId: string, content: string): NoteResult {
    const session = this.requireSession();

    const noteId = this.state.addAgentNote({
      resource_id: resourceId,
      apply_id: session.apply_id,
      content,
      created_at: new Date().toISOString(),
    });

    return { resourceId, noteId, saved: true };
  }

  commit(
    resourceId: string,
    registry: IResourceRegistry,
    filesOnDisk: Map<string, string>,
    outputDir: string,
  ): CommitResult {
    const session = this.requireSession();

    const resourceState = this.state.getResourceState(session.apply_id, resourceId);

    if (resourceState?.validation_passed === 0) {
      const errors = resourceState.validation_errors
        ? JSON.parse(resourceState.validation_errors)
        : ["validation failed"];
      throw new Error(
        `Cannot commit ${resourceId}: validation failed. ` +
        `Use 'agent block' or 'agent error' to report the failure, then re-dispatch after resolving.\n` +
        `Errors: ${JSON.stringify(errors)}`,
      );
    }

    if (resourceState?.validation_passed === null || resourceState?.validation_passed === undefined) {
      const resource = registry.getById(resourceId);
      const hasValidations = resource?.validations && resource.validations.length > 0;
      if (hasValidations) {
        throw new Error(
          `Cannot commit ${resourceId}: validation has not been run. ` +
          `Run 'agent validate ${resourceId}' first.`,
        );
      }
    }

    const planActions: PlannedAction[] = JSON.parse(session.plan_json);
    const action = planActions.find((a) => a.resourceId === resourceId);
    if (!action) {
      throw new Error(`Resource ${resourceId} is not in the current plan`);
    }

    const resource = registry.getById(resourceId);
    if (!resource) {
      throw new Error(`Resource ${resourceId} not found in registry`);
    }

    const hashes: Record<string, string> = JSON.parse(session.hashes_json);
    const effectiveHash = hashes[resourceId];

    this.state.upsertResource({
      id: resource.id,
      kind: resource.kind,
      context: resource.context,
      declaration_hash: createHash("sha256")
        .update(JSON.stringify(resource.declaration))
        .digest("hex"),
      effective_hash: effectiveHash ?? "",
      declaration_json: JSON.stringify(resource.declaration),
      layer: resource.layer,
      settled_at: new Date().toISOString(),
      last_apply_id: session.apply_id,
    });

    const filesRecorded: string[] = [];
    for (const [filePath, content] of filesOnDisk) {
      const contentHash = createHash("sha256").update(content).digest("hex");
      this.state.upsertGeneratedFile({
        path: filePath,
        resource_id: resourceId,
        content_hash: contentHash,
        generator: "llm",
        model: null,
        prompt_hash: null,
        generated_at: new Date().toISOString(),
      });
      filesRecorded.push(filePath);
    }

    this.state.recordAction(session.apply_id, resourceId, action.action, "success");
    this.state.setResourceState(session.apply_id, resourceId, "committed");

    return { resourceId, committed: true, filesRecorded };
  }

  validate(
    resourceId: string,
    files: Map<string, string>,
    registry: IResourceRegistry,
  ): ValidationResult {
    const session = this.requireSession();
    const result = this.validator.validate(resourceId, files, registry);
    this.state.setValidationResult(session.apply_id, resourceId, result.passed, result.errors);
    return result;
  }

  async validateAsync(
    resourceId: string,
    files: Map<string, string>,
    registry: IResourceRegistry,
  ): Promise<ValidationResult> {
    const session = this.requireSession();
    const result = await this.validator.validateAsync(resourceId, files, registry);
    this.state.setValidationResult(session.apply_id, resourceId, result.passed, result.errors);
    return result;
  }

  dispatch(resourceId: string): DispatchResult {
    const session = this.requireSession();
    this.state.setResourceState(session.apply_id, resourceId, "dispatched");
    return { resourceId, state: "dispatched" };
  }

  complete(resourceId: string): { resourceId: string; state: "completed" } {
    const session = this.requireSession();
    this.state.setResourceState(session.apply_id, resourceId, "completed");
    return { resourceId, state: "completed" };
  }

  block(resourceId: string, context: BlockedContext): { resourceId: string; state: "blocked" } {
    const session = this.requireSession();
    this.state.setResourceState(session.apply_id, resourceId, "blocked", JSON.stringify(context));
    return { resourceId, state: "blocked" };
  }

  error(resourceId: string, context: ErrorContext): { resourceId: string; state: "errored" } {
    const session = this.requireSession();
    this.state.setResourceState(session.apply_id, resourceId, "errored", JSON.stringify(context));
    return { resourceId, state: "errored" };
  }

  timeout(resourceId: string): { resourceId: string; state: "timed_out" } {
    const session = this.requireSession();
    this.state.setResourceState(session.apply_id, resourceId, "timed_out");
    return { resourceId, state: "timed_out" };
  }

  reject(resourceId: string, errors: string[]): { resourceId: string; state: "rejected" } {
    const session = this.requireSession();
    this.state.setResourceState(
      session.apply_id,
      resourceId,
      "rejected",
      JSON.stringify({ errors }),
    );
    return { resourceId, state: "rejected" };
  }

  status(): WaveStatusResult {
    const session = this.requireSession();
    const waves: string[][] = JSON.parse(session.waves_json);
    const planActions: PlannedAction[] = JSON.parse(session.plan_json);
    const actionMap = new Map(planActions.map((a) => [a.resourceId, a]));
    const allStates = this.state.getResourceStatesForApply(session.apply_id);
    const stateMap = new Map(allStates.map((s) => [s.resource_id, s]));

    for (let w = 0; w < waves.length; w++) {
      const waveIds = waves[w];
      const allCommitted = waveIds.every((id) => stateMap.get(id)?.state === "committed");
      if (allCommitted) continue;

      const resources: ResourceStatus[] = waveIds.map((id) => {
        const rec = stateMap.get(id);
        const action = actionMap.get(id);
        const status: ResourceStatus = {
          resourceId: id,
          state: rec?.state ?? "pending",
          action: action?.action ?? "create",
          reason: action?.reason ?? "unknown",
        };
        if (rec?.context_json) {
          try {
            status.context = JSON.parse(rec.context_json);
          } catch {}
        }
        return status;
      });

      const summary = {} as Record<SubAgentState, number>;
      const allStateNames: SubAgentState[] = [
        "pending", "dispatched", "completed", "committed",
        "blocked", "errored", "timed_out", "rejected",
      ];
      for (const s of allStateNames) summary[s] = 0;
      for (const r of resources) summary[r.state]++;

      const needsAttention = resources.filter(
        (r) => r.state === "blocked" || r.state === "errored" || r.state === "timed_out" || r.state === "rejected",
      );

      return { wave: w, resources, summary, done: false, needsAttention };
    }

    return {
      wave: -1,
      resources: [],
      summary: {
        pending: 0, dispatched: 0, completed: 0, committed: 0,
        blocked: 0, errored: 0, timed_out: 0, rejected: 0,
      },
      done: true,
      needsAttention: [],
    };
  }

  private requireSession() {
    const session = this.state.getActiveAgentSession();
    if (!session) {
      throw new Error("No active agent session. Run 'agent begin' first.");
    }
    return session;
  }

  static orchestratorInstructions(): string {
    return [
      "=" .repeat(78),
      "  CRITICAL: ORCHESTRATOR RULES — YOU ARE A DISPATCHER, NOT A CODE GENERATOR",
      "=" .repeat(78),
      "",
      "You are an orchestration agent. Your job is to drive the agent CLI and",
      "dispatch sub-agents. You MUST NOT write implementation code yourself.",
      "",
      "═══ WAVE LOOP ═══",
      "",
      "  1. Run `agent next` to get the current wave of resources",
      "  2. For EACH resource in the wave, run the DISPATCH SEQUENCE (see below)",
      "  3. After dispatching all resources, run `agent status`",
      "  4. Inspect the `needsAttention` array and handle each item:",
      "",
      "     blocked → Present the `context.question` to the user. When they answer,",
      "       run `agent dispatch <id>` again and re-spawn the sub-agent with the",
      "       answer appended to its prompt.",
      "",
      "     errored → Check `context.retryCount` vs `context.maxRetries`.",
      "       If retries remain: run `agent dispatch <id>` and re-spawn with the",
      "       error message appended to the prompt.",
      "       If exhausted: report the error to the user and ask how to proceed.",
      "",
      "     rejected → Re-dispatch the sub-agent with the validation errors",
      "       appended to its prompt so it can fix them.",
      "",
      "     timed_out → Report to the user.",
      "",
      "  5. Repeat step 3-4 until `status.done === true` or all resources in the",
      "     wave are committed, then run `agent next` for the next wave.",
      "  6. When `next.done === true`, run `agent finish`.",
      "",
      "═══ DISPATCH SEQUENCE (per resource) ═══",
      "",
      "  a. `agent context <id>` → returns { systemPrompt, prompt }",
      "  b. `agent dispatch <id>` → marks state as dispatched",
      "  c. Spawn sub-agent:",
      '     Agent({ prompt: systemPrompt + "\\n\\n" + prompt, description: "Generate <id>" })',
      "  d. If sub-agent succeeds (returns code blocks):",
      "       - Parse fenced code blocks (look for `// path:` or `# path:` annotations)",
      "       - Write each file to disk at that path (relative to project root)",
      "       - `agent complete <id>`",
      "       - `agent validate <id>` — if validation fails, run `agent reject <id>`",
      "         and re-dispatch; otherwise continue",
      "       - `agent note <id> <summary>` with a summary of the sub-agent's decisions",
      "       - `agent commit <id>`",
      "  e. If sub-agent fails (error, crash, bad output):",
      "       - `agent error <id> <json>` with:",
      '         {"errorKind":"compile|parse|runtime|unknown","message":"...","retryCount":1,"maxRetries":3}',
      "  f. If sub-agent reports it needs a decision:",
      "       - `agent block <id> <json>` with:",
      '         {"reason":"...","question":"...","retryable":true}',
      "",
      "═══ ALL COMMANDS ═══",
      "",
      "  agent begin                      — start session, get plan + waves",
      "  agent next                       — get current wave's uncommitted resources",
      "  agent context <id>               — get systemPrompt + prompt for a resource",
      "  agent dispatch <id>              — mark resource as dispatched (sub-agent spawned)",
      "  agent complete <id>              — mark resource as completed (files on disk)",
      "  agent validate <id>              — run invariant + type + declared validations",
      "  agent note <id> <text>           — record sub-agent design decisions",
      "  agent commit <id>                — record files to state DB, mark committed",
      "  agent block <id> <json>          — mark blocked (needs user decision)",
      "  agent error <id> <json>          — mark errored (sub-agent failed)",
      "  agent reject <id> [errors-json]  — mark rejected (validation failed)",
      "  agent timeout <id>               — mark timed out",
      "  agent status                     — get wave summary with per-resource states",
      "  agent finish                     — finalize session",
      "",
      "═══ RULES ═══",
      "",
      "  - NEVER write implementation code directly — every file comes from a sub-agent",
      "  - NEVER absorb context prompts and generate code inline",
      "  - NEVER skip the sub-agent step for any resource, even simple ones",
      "  - NEVER modify sub-agent output unless it fails to compile",
      "  - Resources within the same wave are independent — dispatch in parallel",
      "  - Waves must be processed sequentially (later waves depend on earlier ones)",
      "  - NEVER commit a resource that failed validation — use `agent block` to escalate",
      "  - `agent commit` will REFUSE if validation failed or was not run (hard-enforced)",
      "  - Always run `agent status` after dispatching a wave to catch failures",
      "=" .repeat(78),
    ].join("\n");
  }

  static dispatchInstructions(resourceId: string): string {
    return [
      "=" .repeat(78),
      "  DISPATCH INSTRUCTIONS FOR: " + resourceId,
      "=" .repeat(78),
      "",
      "You MUST spawn a sub-agent to generate code for this resource.",
      "Do NOT write the code yourself.",
      "",
      "1. Run `agent dispatch ${ID}` to mark the resource as dispatched",
      "",
      "2. Spawn the sub-agent:",
      `   Agent({ prompt: systemPrompt + "\\n\\n" + prompt, description: "Generate ${resourceId}" })`,
      "",
      "3. The sub-agent will return fenced code blocks with path annotations:",
      "   ```rust",
      "   // path: src/Context/Resource.rs",
      "   <code>",
      "   ```",
      "   or for non-Rust files:",
      "   ```toml",
      "   # path: Cargo.toml",
      "   <content>",
      "   ```",
      "",
      "4. Parse each code block, extract the path, write the file to disk",
      "",
      "5. Handle the outcome:",
      "",
      "   SUCCESS (code blocks parsed, files written):",
      `     agent complete ${resourceId}`,
      `     agent validate ${resourceId}`,
      "     If validate.passed:",
      `       agent note ${resourceId} <summary of sub-agent's design decisions>`,
      `       agent commit ${resourceId}`,
      "     If validate.errors:",
      `       agent error ${resourceId} {"errorKind":"invariant","message":"<errors>","retryCount":1,"maxRetries":3}`,
      "       Re-dispatch with the validation errors appended to the prompt",
      "",
      "   FAILURE (sub-agent crashed, returned unparseable output, compile error):",
      `     agent error ${resourceId} {"errorKind":"<compile|parse|unknown>","message":"<error details>","retryCount":1,"maxRetries":3}`,
      "     Re-dispatch with the error details appended to the prompt (up to maxRetries)",
      "",
      "   BLOCKED (sub-agent says it needs a user decision):",
      `     agent block ${resourceId} {"reason":"<why>","question":"<question for user>","retryable":true}`,
      "     Report the question to the user; on answer, re-dispatch with the answer",
      "",
      "   TIMEOUT (sub-agent did not respond in time):",
      `     agent timeout ${resourceId}`,
      "     Report to the user",
      "",
      "6. After the resource reaches committed state, move on to the next resource",
      "   or check `agent status` to see the full wave picture.",
      "=" .repeat(78),
    ].join("\n");
  }

  finish(): FinishResult {
    const session = this.requireSession();

    const planActions: PlannedAction[] = JSON.parse(session.plan_json);
    const committedActions = this.state.getApplyActions(session.apply_id);
    const committedIds = new Set(
      committedActions.filter((a) => a.outcome === "success").map((a) => a.resource_id),
    );

    let created = 0;
    let modified = 0;
    let failed = 0;

    for (const action of planActions) {
      if (committedIds.has(action.resourceId)) {
        if (action.action === "create") created++;
        else modified++;
      } else {
        failed++;
        this.state.recordAction(session.apply_id, action.resourceId, action.action, "skipped");
      }
    }

    const status = failed > 0 ? "failed" : "ok";
    this.state.finishApply(session.apply_id, status);
    this.state.deleteAgentSession(session.apply_id);
    this.state.releaseLock();

    return {
      applyId: session.apply_id,
      status,
      created,
      modified,
      destroyed: 0,
      failed,
    };
  }
}
