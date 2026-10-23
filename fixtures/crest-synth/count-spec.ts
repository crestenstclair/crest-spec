import { resolve } from "path";
const specFile = resolve("phases/crest-spec-phase-5.ts");
await import(specFile);
const { getActiveProject } = await import("../../../src/dsl/singleton.js");
const project = getActiveProject();
if (project) {
  const registry = project.getRegistry();
  const all = registry.getAll();
  const resourceIds = new Set(all.map(r => r.id));
  console.log(`Total unique resource IDs: ${resourceIds.size}`);
  
  // Breakdown by kind
  const byKind: Record<string, string[]> = {};
  for (const r of all) {
    if (!byKind[r.kind]) byKind[r.kind] = [];
    byKind[r.kind].push(r.id);
  }
  
  console.log("\nResources by kind:");
  for (const [kind, ids] of Object.entries(byKind).sort()) {
    console.log(`  ${kind}: ${ids.length}`);
    if (kind !== 'project' && kind !== 'context' && kind !== 'assetKind') {
      // Show count of non-structural
      console.log(`    IDs: ${ids.slice(0, 3).join(', ')}${ids.length > 3 ? '...' : ''}`);
    }
  }
} else {
  console.log("No active project");
}
