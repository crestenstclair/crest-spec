import { join } from "path";
import { Planner } from "../../planner/planner.js";
import { HashComputer } from "../../planner/hash-computer.js";
import { StateDatabase } from "../../state/state-database.js";
import { InvariantChecker } from "../../invariants/invariant-checker.js";
import { allRules } from "../../invariants/rules/index.js";
import { PromptBuilder } from "../../engine/prompt-builder.js";
import { ConstraintLoop } from "../../engine/constraint-loop.js";
import { ApplyEngine } from "../../engine/apply-engine.js";
import { AnthropicLlmClient } from "../../engine/llm-client.js";
import { getActiveProject } from "../../dsl/singleton.js";
import { Formatter } from "../formatter.js";

export async function applyCommand(
  projectDir: string,
  specFile: string,
  options: {
    modelId: string;
    target?: string;
    force?: boolean;
    maxRetries?: number;
  },
): Promise<number> {
  await import(join(projectDir, specFile));
  const project = getActiveProject();
  if (!project) {
    console.error("No project found. Does the spec file call project()?");
    return 1;
  }

  const registry = project.getRegistry();
  const state = new StateDatabase(join(projectDir, "crest-spec.db"));

  if (!state.acquireLock(`pid:${process.pid}`)) {
    const lock = state.getLock();
    console.error(Formatter.error(`Apply is locked by ${lock?.holder} since ${lock?.acquired_at}`));
    return 1;
  }

  try {
    const hashComputer = new HashComputer(options.modelId);
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder();
    const checker = new InvariantChecker(allRules());
    const constraintLoop = new ConstraintLoop(checker, {
      projectRoot: projectDir,
    });

    const llmClient = new AnthropicLlmClient(options.modelId);
    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer);

    const result = await engine.apply(registry, state, llmClient, {
      target: options.target,
      force: options.force,
      maxRetries: options.maxRetries,
      outputDir: projectDir,
    });

    console.log(`\nApply complete:`);
    console.log(`  Created:   ${result.created}`);
    console.log(`  Modified:  ${result.modified}`);
    console.log(`  Destroyed: ${result.destroyed}`);
    if (result.failed > 0) {
      console.log(`  ${Formatter.error(`Failed: ${result.failed}`)}`);
      for (const err of result.errors) {
        console.log(`    ${err}`);
      }
    }

    return result.status === "ok" ? 0 : 1;
  } finally {
    state.releaseLock();
  }
}
