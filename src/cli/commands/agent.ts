import { join } from "path";
import { readdir } from "fs/promises";
import { StateDatabase } from "../../state/state-database.js";
import { Planner } from "../../planner/planner.js";
import { HashComputer } from "../../planner/hash-computer.js";
import { WaveComputer } from "../../engine/wave-computer.js";
import { PromptBuilder } from "../../engine/prompt-builder.js";
import { InvariantChecker } from "../../invariants/invariant-checker.js";
import { allRules } from "../../invariants/rules/index.js";
import { ResourceValidator } from "../../engine/resource-validator.js";
import { AgentSession } from "../../engine/agent-session.js";
import { getActiveProject } from "../../dsl/singleton.js";
import type { IResourceRegistry } from "../../registry/resource-registry.js";

function jsonOut(data: unknown): void {
  process.stdout.write(JSON.stringify(data, null, 2) + "\n");
}

function jsonError(error: string): void {
  jsonOut({ error });
}

async function discoverFiles(
  outputDir: string,
  resourceId: string,
  registry: IResourceRegistry,
): Promise<Map<string, string>> {
  const resource = registry.getById(resourceId);
  if (!resource) return new Map();

  const contextName = resource.context;
  const resourceName = resource.name;
  const files = new Map<string, string>();

  const dirs = [
    join(outputDir, "src", contextName ?? "", resourceName),
    join(outputDir, "tests", contextName ?? "", resourceName),
  ];

  for (const dir of dirs) {
    try {
      const entries = await readdir(dir, { recursive: true, withFileTypes: true });
      for (const entry of entries) {
        if (entry.isFile()) {
          const fullPath = join(dir, entry.name);
          const relativePath = fullPath.replace(outputDir + "/", "");
          const content = await Bun.file(fullPath).text();
          files.set(relativePath, content);
        }
      }
    } catch {}
  }

  return files;
}

export async function agentCommand(
  projectDir: string,
  specFile: string,
  modelId: string,
  subcommand: string,
  args: string[],
  flags: { target?: string; force?: boolean; skipTypecheck?: boolean; skipTests?: boolean },
): Promise<number> {
  const state = new StateDatabase(join(projectDir, "crest-spec.db"));

  if (subcommand === "begin") {
    await import(join(projectDir, specFile));
    const project = getActiveProject();
    if (!project) {
      jsonError("No project found. Does the spec file call project()?");
      return 1;
    }
    const registry = project.getRegistry();
    const meta = project.getMeta();
    const hashComputer = new HashComputer(modelId);
    const planner = new Planner(hashComputer);
    const waveComputer = new WaveComputer();
    const promptBuilder = PromptBuilder.fromMeta(meta);
    const checker = new InvariantChecker(allRules());
    const validator = new ResourceValidator(checker, {
      projectRoot: projectDir,
      typeCheckCommand: meta.typeCheckCommand as string[] | undefined,
      testCommand: meta.testCommand as string[] | undefined,
    });
    const session = new AgentSession(state, planner, waveComputer, hashComputer, promptBuilder, validator);

    try {
      const result = session.begin(registry, { target: flags.target, force: flags.force });
      jsonOut(result);
      return 0;
    } catch (e) {
      jsonError(e instanceof Error ? e.message : String(e));
      return 1;
    }
  }

  if (subcommand === "next") {
    const session = buildSessionFromActive(state, modelId, projectDir);
    if (!session) return 1;

    try {
      const result = session.next();
      jsonOut(result);
      return 0;
    } catch (e) {
      jsonError(e instanceof Error ? e.message : String(e));
      return 1;
    }
  }

  if (subcommand === "context") {
    const resourceId = args[0];
    if (!resourceId) {
      jsonError("Usage: crest-spec agent context <resource-id>");
      return 1;
    }

    await import(join(projectDir, specFile));
    const project = getActiveProject();
    if (!project) {
      jsonError("No project found.");
      return 1;
    }
    const registry = project.getRegistry();
    const meta = project.getMeta();
    const session = buildSessionFromActive(state, modelId, projectDir, {
      promptBuilder: PromptBuilder.fromMeta(meta),
    });
    if (!session) return 1;

    try {
      const result = session.context(resourceId, registry);
      jsonOut(result);
      return 0;
    } catch (e) {
      jsonError(e instanceof Error ? e.message : String(e));
      return 1;
    }
  }

  if (subcommand === "validate") {
    const resourceId = args[0];
    if (!resourceId) {
      jsonError("Usage: crest-spec agent validate <resource-id>");
      return 1;
    }

    await import(join(projectDir, specFile));
    const project = getActiveProject();
    if (!project) {
      jsonError("No project found.");
      return 1;
    }
    const registry = project.getRegistry();
    const meta = project.getMeta();
    const files = await discoverFiles(projectDir, resourceId, registry);
    const session = buildSessionFromActive(state, modelId, projectDir, {
      promptBuilder: PromptBuilder.fromMeta(meta),
      skipTypeCheck: flags.skipTypecheck,
      skipTests: flags.skipTests,
      typeCheckCommand: meta.typeCheckCommand as string[] | undefined,
      testCommand: meta.testCommand as string[] | undefined,
    });
    if (!session) return 1;

    try {
      const result = await session.validateAsync(resourceId, files, registry);
      jsonOut({ resourceId, ...result });
      return result.passed ? 0 : 1;
    } catch (e) {
      jsonError(e instanceof Error ? e.message : String(e));
      return 1;
    }
  }

  if (subcommand === "note") {
    const resourceId = args[0];
    const content = args.slice(1).join(" ");
    if (!resourceId || !content) {
      jsonError("Usage: crest-spec agent note <resource-id> <text>");
      return 1;
    }

    const session = buildSessionFromActive(state, modelId, projectDir);
    if (!session) return 1;

    try {
      const result = session.note(resourceId, content);
      jsonOut(result);
      return 0;
    } catch (e) {
      jsonError(e instanceof Error ? e.message : String(e));
      return 1;
    }
  }

  if (subcommand === "commit") {
    const resourceId = args[0];
    if (!resourceId) {
      jsonError("Usage: crest-spec agent commit <resource-id>");
      return 1;
    }

    await import(join(projectDir, specFile));
    const project = getActiveProject();
    if (!project) {
      jsonError("No project found.");
      return 1;
    }
    const registry = project.getRegistry();
    const files = await discoverFiles(projectDir, resourceId, registry);
    const session = buildSessionFromActive(state, modelId, projectDir);
    if (!session) return 1;

    try {
      const result = session.commit(resourceId, registry, files, projectDir);
      jsonOut(result);
      return 0;
    } catch (e) {
      jsonError(e instanceof Error ? e.message : String(e));
      return 1;
    }
  }

  if (subcommand === "finish") {
    const session = buildSessionFromActive(state, modelId, projectDir);
    if (!session) return 1;

    try {
      const result = session.finish();
      jsonOut(result);
      return 0;
    } catch (e) {
      jsonError(e instanceof Error ? e.message : String(e));
      return 1;
    }
  }

  jsonError(`Unknown agent subcommand: ${subcommand}. Valid: begin, next, context, validate, note, commit, finish`);
  return 1;
}

function buildSessionFromActive(
  state: StateDatabase,
  modelId: string,
  projectDir: string,
  options?: {
    promptBuilder?: PromptBuilder;
    skipTypeCheck?: boolean;
    skipTests?: boolean;
    typeCheckCommand?: string[];
    testCommand?: string[];
  },
): AgentSession | null {
  const activeSession = state.getActiveAgentSession();
  if (!activeSession) {
    jsonError("No active agent session. Run 'crest-spec agent begin' first.");
    return null;
  }

  const hashComputer = new HashComputer(modelId);
  const planner = new Planner(hashComputer);
  const waveComputer = new WaveComputer();
  const promptBuilder = options?.promptBuilder ?? new PromptBuilder({ language: "typescript" });
  const checker = new InvariantChecker(allRules());
  const validator = new ResourceValidator(checker, {
    projectRoot: projectDir,
    skipTypeCheck: options?.skipTypeCheck,
    skipTests: options?.skipTests,
    typeCheckCommand: options?.typeCheckCommand,
    testCommand: options?.testCommand,
  });

  return new AgentSession(state, planner, waveComputer, hashComputer, promptBuilder, validator);
}
