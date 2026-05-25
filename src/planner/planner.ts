import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { IStateDatabase } from "../state/state-database.js";
import type { IHashComputer } from "./hash-computer.js";
import { Plan, type PlannedAction } from "./plan.js";

export interface IPlanner {
  plan(registry: IResourceRegistry, state: IStateDatabase): Plan;
}

export class Planner implements IPlanner {
  constructor(private readonly hashComputer: IHashComputer) {}

  plan(registry: IResourceRegistry, state: IStateDatabase): Plan {
    const effectiveHashes = this.hashComputer.computeAll(registry);
    const actions: PlannedAction[] = [];
    const storedResources = state.getAllResources();
    const specIds = new Set(registry.getAll().map((r) => r.id));

    const ordered = registry.topologicalOrder();
    const cascadeReasons = new Map<string, string>();

    for (const resource of ordered) {
      const stored = state.getResource(resource.id);
      const newHash = effectiveHashes.get(resource.id)!;

      if (!stored) {
        actions.push({
          resourceId: resource.id,
          action: "create",
          reason: "new resource",
          affectedFiles: [],
        });
        continue;
      }

      if (stored.effective_hash !== newHash) {
        const cascadedFrom = cascadeReasons.get(resource.id);
        const reason = cascadedFrom
          ? `dependency changed (${cascadedFrom})`
          : "declaration changed";

        actions.push({
          resourceId: resource.id,
          action: "modify",
          reason,
          affectedFiles: state.getFilesForResource(resource.id).map((f) => f.path),
          cascadedFrom,
        });

        const dependents = registry.getDependents(resource.id);
        for (const dep of dependents) {
          if (!cascadeReasons.has(dep.id)) {
            cascadeReasons.set(dep.id, resource.id);
          }
        }
      }
    }

    for (const stored of storedResources) {
      if (!specIds.has(stored.id)) {
        actions.push({
          resourceId: stored.id,
          action: "destroy",
          reason: "removed from spec",
          affectedFiles: state.getFilesForResource(stored.id).map((f) => f.path),
        });
      }
    }

    return new Plan(actions, []);
  }
}
