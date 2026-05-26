#!/usr/bin/env bun
import { resolve } from "path";
import { initCommand } from "./commands/init.js";
import { planCommand } from "./commands/plan.js";
import { applyCommand } from "./commands/apply.js";
import { Formatter } from "./formatter.js";

const DEFAULT_SPEC = "crest-spec.ts";
const DEFAULT_MODEL = "claude-sonnet-4-6";

async function main(): Promise<number> {
  const args = process.argv.slice(2);
  const command = args[0];
  const projectDir = resolve(".");

  function getFlag(name: string): string | undefined {
    let idx = args.indexOf(`--${name}`);
    if (idx === -1) idx = args.indexOf(`-${name}`);
    if (idx === -1) return undefined;
    return args[idx + 1];
  }

  function hasFlag(name: string): boolean {
    return args.includes(`--${name}`);
  }

  const specFile = getFlag("spec") ?? DEFAULT_SPEC;
  const modelId = getFlag("model") ?? DEFAULT_MODEL;

  switch (command) {
    case "init":
      return initCommand(projectDir);

    case "plan":
      return planCommand(projectDir, specFile, modelId);

    case "apply": {
      const target = getFlag("target");
      const force = hasFlag("force");
      const maxRetries = getFlag("retries") ? parseInt(getFlag("retries")!) : undefined;
      const concurrency = getFlag("concurrency") ? parseInt(getFlag("concurrency")!) : 5;
      return applyCommand(projectDir, specFile, { modelId, target, force, maxRetries, concurrency });
    }

    case "log": {
      const { StateDatabase } = await import("../state/state-database.js");
      const state = new StateDatabase(resolve(projectDir, "crest-spec.db"));
      const applies = state.getApplies(parseInt(getFlag("limit") ?? "20"));
      for (const a of applies) {
        console.log(`#${a.id}  ${a.status.padEnd(8)} ${a.started_at}  spec:${a.spec_hash.slice(0, 8)}`);
      }
      return 0;
    }

    case "history": {
      const resourceId = args[1];
      if (!resourceId) {
        console.error("Usage: crest-spec history <resource-id>");
        return 1;
      }
      const { StateDatabase } = await import("../state/state-database.js");
      const state = new StateDatabase(resolve(projectDir, "crest-spec.db"));
      const gens = state.getGenerationsForResource(resourceId);
      for (const g of gens) {
        console.log(`  apply #${g.apply_id}  ${g.outcome}  model:${g.model}  retries:${g.retries}  ${g.created_at}`);
      }
      return 0;
    }

    case "state": {
      const subcommand = args[1];
      const { StateDatabase } = await import("../state/state-database.js");
      const state = new StateDatabase(resolve(projectDir, "crest-spec.db"));
      if (subcommand === "list") {
        const resources = state.getAllResources();
        for (const r of resources) {
          console.log(`${r.kind.padEnd(20)} ${r.id}`);
        }
      } else if (subcommand === "rm") {
        const id = args[2];
        if (!id) { console.error("Usage: crest-spec state rm <id>"); return 1; }
        state.deleteResource(id);
        console.log(`Removed ${id} from state`);
      } else {
        console.error("Usage: crest-spec state [list|rm]");
        return 1;
      }
      return 0;
    }

    case "validate": {
      await import(resolve(projectDir, specFile));
      const { getActiveProject } = await import("../dsl/singleton.js");
      const project = getActiveProject();
      if (!project) { console.error("No project found."); return 1; }
      const { InvariantChecker } = await import("../invariants/invariant-checker.js");
      const { allRules } = await import("../invariants/rules/index.js");
      const checker = new InvariantChecker(allRules());
      const results = checker.checkStructural(project.getRegistry());
      const violations = results.filter((r) => r.status === "violated");
      if (violations.length === 0) {
        console.log(Formatter.success("All invariants pass."));
        return 0;
      }
      for (const v of violations) {
        console.log(`! ${v.invariant}`);
        if (v.detail) console.log(`  ${v.detail}`);
      }
      return 1;
    }

    case "unlock": {
      const { StateDatabase } = await import("../state/state-database.js");
      const state = new StateDatabase(resolve(projectDir, "crest-spec.db"));
      state.forceClearLock();
      console.log("Lock cleared.");
      return 0;
    }

    case "vacuum": {
      const before = getFlag("before");
      if (!before) { console.error("Usage: crest-spec vacuum --before DATE"); return 1; }
      console.log(`Vacuum before ${before} — not yet implemented.`);
      return 0;
    }

    case "sql": {
      const dbPath = resolve(projectDir, "crest-spec.db");
      const proc = Bun.spawn(["sqlite3", dbPath], {
        stdin: "inherit",
        stdout: "inherit",
        stderr: "inherit",
      });
      return await proc.exited;
    }

    case "graph": {
      await import(resolve(projectDir, specFile));
      const { getActiveProject } = await import("../dsl/singleton.js");
      const project = getActiveProject();
      if (!project) { console.error("No project found."); return 1; }
      const registry = project.getRegistry();
      console.log("digraph resources {");
      for (const r of registry.getAll()) {
        for (const dep of r.dependencies) {
          console.log(`  "${r.id}" -> "${dep.targetId}" [label="${dep.kind}"];`);
        }
      }
      console.log("}");
      return 0;
    }

    case "contextmap": {
      await import(resolve(projectDir, specFile));
      const { getActiveProject } = await import("../dsl/singleton.js");
      const project = getActiveProject();
      if (!project) { console.error("No project found."); return 1; }
      const map = project.getRegistry().getContextMap();
      console.log("digraph contextmap {");
      for (const r of map) {
        console.log(`  "${r.from}" -> "${r.to}" [label="${r.kind}"];`);
      }
      console.log("}");
      return 0;
    }

    default:
      console.log(`crest-spec — declarative DDD specification tool

Commands:
  init                          Create a new spec and state database
  plan                          Show what would change
  apply                         Execute the plan
  validate                      Check invariants
  log                           List past applies
  history <resource>            Show history for a resource
  state list                    List resources in state
  state rm <id>                 Remove a resource from state
  graph                         Render resource dependency graph (DOT)
  contextmap                    Render context map (DOT)
  unlock                        Clear a stale lock
  vacuum --before DATE          Prune old history
  sql                           Open sqlite3 shell

Options:
  -spec <file>                  Spec file (default: crest-spec.ts)
  -model <id>                   Model ID (default: claude-sonnet-4-6)
  -target <resource>            Target a specific resource
  --force                       Force re-render
  -retries <n>                  Max retries (default: 3)`);
      return command ? 1 : 0;
  }
}

main().then((code) => process.exit(code));
