import type { PlannedAction } from "../planner/plan.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";

export interface IWaveComputer {
  compute(actions: PlannedAction[], registry: IResourceRegistry): PlannedAction[][];
}

export class WaveComputer implements IWaveComputer {
  compute(actions: PlannedAction[], registry: IResourceRegistry): PlannedAction[][] {
    if (actions.length === 0) return [];

    const actionIds = new Set(actions.map((a) => a.resourceId));

    const waveOf = new Map<string, number>();
    for (const action of actions) {
      const resource = registry.getById(action.resourceId);
      if (!resource) {
        waveOf.set(action.resourceId, 0);
        continue;
      }

      let maxDepWave = -1;
      for (const dep of resource.dependencies) {
        if (actionIds.has(dep.targetId) && waveOf.has(dep.targetId)) {
          maxDepWave = Math.max(maxDepWave, waveOf.get(dep.targetId)!);
        }
      }
      waveOf.set(action.resourceId, maxDepWave + 1);
    }

    const maxWave = Math.max(...waveOf.values());
    const waves: PlannedAction[][] = Array.from({ length: maxWave + 1 }, () => []);

    for (const action of actions) {
      waves[waveOf.get(action.resourceId)!].push(action);
    }

    return waves;
  }
}
