import type { ResourceDescriptor } from "../../types.js";
import type { IResourceRegistry } from "../../registry/resource-registry.js";
import type { IInvariantRule, InvariantResult } from "../invariant-checker.js";

export class MutationsThroughServices implements IInvariantRule {
  name = "all mutations route through ApplicationServices";

  appliesTo(resource: ResourceDescriptor): boolean {
    return resource.kind === "aggregate" && resource.declaration.root === true;
  }

  checkStructural(resource: ResourceDescriptor, registry: IResourceRegistry): InvariantResult {
    const services = registry.getByKind("applicationService");
    const hasService = services.some((s) =>
      s.dependencies.some((d) => d.targetId === resource.id && d.kind === "uses"),
    );

    return {
      invariant: this.name,
      resourceId: resource.id,
      status: hasService ? "ok" : "violated",
      detail: hasService ? null : `No ApplicationService uses aggregate ${resource.name}`,
      rationale: "single audit point; enables event sourcing",
    };
  }
}
