import { createHash } from "crypto";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { ResourceDescriptor } from "../types.js";

export interface IHashComputer {
  computeAll(registry: IResourceRegistry): Map<string, string>;
}

export class HashComputer implements IHashComputer {
  constructor(private readonly modelIdentifier: string) {}

  computeAll(registry: IResourceRegistry): Map<string, string> {
    const hashes = new Map<string, string>();
    const ordered = registry.topologicalOrder();

    for (const resource of ordered) {
      const hash = this.computeOne(resource, hashes, registry);
      hashes.set(resource.id, hash);
    }

    return hashes;
  }

  private computeOne(
    resource: ResourceDescriptor,
    computedHashes: Map<string, string>,
    registry: IResourceRegistry,
  ): string {
    const hasher = createHash("sha256");

    hasher.update(stableStringify(resource.declaration));
    hasher.update(stableStringify(resource.meta));
    hasher.update(stableStringify(resource.invariants ?? []));

    for (const dep of resource.dependencies) {
      const depHash = computedHashes.get(dep.targetId);
      if (depHash) {
        hasher.update(depHash);
      }
    }

    hasher.update(this.modelIdentifier);

    return hasher.digest("hex");
  }
}

function stableStringify(obj: unknown): string {
  return JSON.stringify(sortKeys(obj));
}

function sortKeys(obj: unknown): unknown {
  if (obj === null || obj === undefined) return obj;
  if (Array.isArray(obj)) return obj.map(sortKeys);
  if (typeof obj === "object") {
    const sorted: Record<string, unknown> = {};
    for (const key of Object.keys(obj as Record<string, unknown>).sort()) {
      sorted[key] = sortKeys((obj as Record<string, unknown>)[key]);
    }
    return sorted;
  }
  return obj;
}
