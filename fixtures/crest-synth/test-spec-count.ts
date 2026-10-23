import { resolve } from "path";
const specFile = resolve("phases/crest-spec-phase-5.ts");
await import(specFile);
const { getActiveProject } = await import("../../../src/dsl/singleton.js");
const project = getActiveProject();
if (project) {
  const registry = project.getRegistry();
  const all = registry.getAll();
  console.log(`Total resources in registry: ${all.length}`);
  console.log("\nByKind breakdown:");
  const byKind: Record<string, number> = {};
  for (const r of all) {
    byKind[r.kind] = (byKind[r.kind] ?? 0) + 1;
  }
  for (const [kind, count] of Object.entries(byKind).sort()) {
    console.log(`  ${kind}: ${count}`);
  }
} else {
  console.log("No active project");
}
