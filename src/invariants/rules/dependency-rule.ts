import type { ResourceDescriptor } from "../../types.js";
import type { IResourceRegistry } from "../../registry/resource-registry.js";
import type { IInvariantRule, InvariantResult } from "../invariant-checker.js";

export class DependencyRule implements IInvariantRule {
  name = "dependency rule (layer restrictions)";

  appliesTo(resource: ResourceDescriptor): boolean {
    return resource.layer !== null && resource.dependencies.length > 0;
  }

  checkStructural(resource: ResourceDescriptor, registry: IResourceRegistry): InvariantResult {
    const projectResource = registry.getByKind("project")[0];
    if (!projectResource) {
      return { invariant: this.name, resourceId: resource.id, status: "ok", detail: null, rationale: null };
    }

    const rules = (projectResource.declaration.rules as Array<{ layer: string; dependsOn: string[] }>) ?? [];
    const layerRule = rules.find((r) => r.layer === resource.layer);
    if (!layerRule) {
      return { invariant: this.name, resourceId: resource.id, status: "ok", detail: null, rationale: null };
    }

    for (const dep of resource.dependencies) {
      const target = registry.getById(dep.targetId);
      if (!target || !target.layer) continue;
      if (!layerRule.dependsOn.includes(target.layer) && target.layer !== resource.layer) {
        return {
          invariant: this.name,
          resourceId: resource.id,
          status: "violated",
          detail: `${resource.id} (layer: ${resource.layer}) depends on ${target.id} (layer: ${target.layer}), but ${resource.layer} only allows dependencies on [${layerRule.dependsOn.join(", ")}]`,
          rationale: "preserves Clean Architecture's Dependency Rule",
        };
      }
    }

    return { invariant: this.name, resourceId: resource.id, status: "ok", detail: null, rationale: null };
  }
}
