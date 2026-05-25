import type { ResourceDescriptor } from "../types.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";

export interface InvariantResult {
  invariant: string;
  resourceId: string | null;
  status: "ok" | "violated";
  detail: string | null;
  rationale: string | null;
}

export interface IInvariantRule {
  name: string;
  appliesTo(resource: ResourceDescriptor): boolean;
  checkStructural?(resource: ResourceDescriptor, registry: IResourceRegistry): InvariantResult;
  checkGenerated?(resource: ResourceDescriptor, fileContents: Map<string, string>, registry: IResourceRegistry): InvariantResult;
}

export interface IInvariantChecker {
  checkStructural(registry: IResourceRegistry): InvariantResult[];
  checkGenerated(resourceId: string, files: Map<string, string>, registry: IResourceRegistry): InvariantResult[];
}

export class InvariantChecker implements IInvariantChecker {
  constructor(private readonly rules: IInvariantRule[]) {}

  checkStructural(registry: IResourceRegistry): InvariantResult[] {
    const results: InvariantResult[] = [];
    for (const resource of registry.getAll()) {
      for (const rule of this.rules) {
        if (rule.checkStructural && rule.appliesTo(resource)) {
          results.push(rule.checkStructural(resource, registry));
        }
      }
    }
    return results;
  }

  checkGenerated(resourceId: string, files: Map<string, string>, registry: IResourceRegistry): InvariantResult[] {
    const resource = registry.getById(resourceId);
    if (!resource) return [];

    const results: InvariantResult[] = [];
    for (const rule of this.rules) {
      if (rule.checkGenerated && rule.appliesTo(resource)) {
        results.push(rule.checkGenerated(resource, files, registry));
      }
    }
    return results;
  }
}
