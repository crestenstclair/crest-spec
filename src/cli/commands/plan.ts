import { join } from "path";
import { Planner } from "../../planner/planner.js";
import { HashComputer } from "../../planner/hash-computer.js";
import { StateDatabase } from "../../state/state-database.js";
import { InvariantChecker } from "../../invariants/invariant-checker.js";
import { allRules } from "../../invariants/rules/index.js";
import { getActiveProject } from "../../dsl/singleton.js";

export async function planCommand(
  projectDir: string,
  specFile: string,
  modelId: string,
): Promise<number> {
  await import(join(projectDir, specFile));
  const project = getActiveProject();
  if (!project) {
    console.error("No project found. Does the spec file call project()?");
    return 1;
  }

  const registry = project.getRegistry();
  const state = new StateDatabase(join(projectDir, "crest-spec.db"));
  const hashComputer = new HashComputer(modelId);
  const planner = new Planner(hashComputer);

  const checker = new InvariantChecker(allRules());
  const structuralViolations = checker.checkStructural(registry);
  const violations = structuralViolations.filter((r) => r.status === "violated");

  const plan = planner.plan(registry, state);

  console.log(plan.display());

  if (violations.length > 0) {
    console.log("\nInvariant violations:");
    for (const v of violations) {
      console.log(`  ! ${v.invariant}`);
      if (v.resourceId) console.log(`    resource: ${v.resourceId}`);
      if (v.detail) console.log(`    detail: ${v.detail}`);
      if (v.rationale) console.log(`    rationale: ${v.rationale}`);
    }
  }

  return violations.length > 0 ? 1 : 0;
}
