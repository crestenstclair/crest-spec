import { createHash } from "crypto";
import type { IStateDatabase } from "../state/state-database.js";
import type { IPlanner } from "../planner/planner.js";
import type { IHashComputer } from "../planner/hash-computer.js";
import type { IWaveComputer } from "./wave-computer.js";
import type { IPromptBuilder } from "./prompt-builder.js";
import type { IResourceValidator, ValidationResult } from "./resource-validator.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { PlannedAction } from "../planner/plan.js";

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

    return {
      applyId: applyRecord.id,
      plan: planSummary,
      waves: waveIds,
      totalResources: generates.length,
      orchestratorInstructions: AgentSession.orchestratorInstructions(),
    };
  }

  next(): NextResult {
    const session = this.state.getActiveAgentSession();
    if (!session) {
      throw new Error("No active agent session. Run 'agent begin' first.");
    }

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
    const session = this.state.getActiveAgentSession();
    if (!session) {
      throw new Error("No active agent session. Run 'agent begin' first.");
    }

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
    };
  }

  note(resourceId: string, content: string): NoteResult {
    const session = this.state.getActiveAgentSession();
    if (!session) {
      throw new Error("No active agent session. Run 'agent begin' first.");
    }

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
    const session = this.state.getActiveAgentSession();
    if (!session) {
      throw new Error("No active agent session. Run 'agent begin' first.");
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

    return { resourceId, committed: true, filesRecorded };
  }

  validate(
    resourceId: string,
    files: Map<string, string>,
    registry: IResourceRegistry,
  ): ValidationResult {
    return this.validator.validate(resourceId, files, registry);
  }

  async validateAsync(
    resourceId: string,
    files: Map<string, string>,
    registry: IResourceRegistry,
  ): Promise<ValidationResult> {
    return this.validator.validateAsync(resourceId, files, registry);
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
      "DO:",
      "  - Run `agent next` to get the current wave of resources",
      "  - Run `agent context <id>` to get the sub-agent prompt for each resource",
      "  - Spawn a sub-agent (Agent tool) for each resource, passing it the",
      "    systemPrompt and prompt from the context output",
      "  - Parse the sub-agent's output for fenced code blocks with `// path:` or",
      "    `# path:` annotations and write each file to disk",
      "  - Run `agent note <id>` with a summary of the sub-agent's decisions",
      "  - Run `agent commit <id>` after files are on disk",
      "  - Run `agent next` again; repeat until done",
      "  - Run `agent finish` to finalize the session",
      "",
      "DO NOT:",
      "  - Write implementation code directly — every file must come from a sub-agent",
      "  - Absorb the context prompts and generate code inline",
      "  - Skip the sub-agent step for any resource, even simple ones",
      "  - Modify the sub-agent's output unless it fails to compile",
      "",
      "DISPATCH PATTERN:",
      '  const ctx = `agent context <id>` // returns { systemPrompt, prompt }',
      "  sub-agent = Agent({",
      '    prompt: ctx.systemPrompt + "\\n\\n" + ctx.prompt,',
      '    description: "Generate <resource-id>"',
      "  })",
      "  // Parse code blocks from sub-agent output → write to disk",
      "",
      "Resources within the same wave are independent — dispatch them in parallel",
      "when possible. Resources in later waves depend on earlier ones, so waves",
      "must be processed sequentially.",
      "=" .repeat(78),
    ].join("\n");
  }

  static dispatchInstructions(resourceId: string): string {
    return [
      "=" .repeat(78),
      "  DISPATCH INSTRUCTIONS",
      "=" .repeat(78),
      "",
      "You MUST spawn a sub-agent to generate code for this resource.",
      "Do NOT write the code yourself.",
      "",
      "1. Combine systemPrompt + prompt into a single sub-agent prompt",
      "2. Spawn the sub-agent using your Agent tool:",
      `   Agent({ prompt: <combined>, description: "Generate ${resourceId}" })`,
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
      "4. Parse each code block, extract the path from the annotation, and write",
      "   the file to disk at that path (relative to the project root)",
      "5. After writing all files, run `agent note` and `agent commit`",
      "",
      "If the sub-agent's output fails to compile, you may re-dispatch with",
      "additional context (e.g., compiler errors), but do NOT hand-edit the code.",
      "=" .repeat(78),
    ].join("\n");
  }

  finish(): FinishResult {
    const session = this.state.getActiveAgentSession();
    if (!session) {
      throw new Error("No active agent session. Run 'agent begin' first.");
    }

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
