import type { ResourceDescriptor } from "../../types.js";
import type { IResourceRegistry } from "../../registry/resource-registry.js";
import type { IInvariantRule, InvariantResult } from "../invariant-checker.js";

export class ContextBoundaries implements IInvariantRule {
  name = "context boundaries respected";

  appliesTo(resource: ResourceDescriptor): boolean {
    return resource.context !== null && resource.dependencies.length > 0;
  }

  checkStructural(resource: ResourceDescriptor, registry: IResourceRegistry): InvariantResult {
    const contextMap = registry.getContextMap();

    for (const dep of resource.dependencies) {
      const target = registry.getById(dep.targetId);
      if (!target || !target.context) continue;
      if (target.context === resource.context) continue;

      const hasRelationship = contextMap.some(
        (r) =>
          (r.from === resource.context && r.to === target.context) ||
          (r.to === resource.context && r.from === target.context),
      );

      if (!hasRelationship) {
        return {
          invariant: this.name,
          resourceId: resource.id,
          status: "violated",
          detail: `${resource.id} depends on ${target.id} across context boundary (${resource.context} -> ${target.context}) without a declared relationship`,
          rationale: "context boundaries are enforced; integration is explicit",
        };
      }
    }

    return {
      invariant: this.name,
      resourceId: resource.id,
      status: "ok",
      detail: null,
      rationale: null,
    };
  }
}
