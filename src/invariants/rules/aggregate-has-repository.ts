import type { ResourceDescriptor } from "../../types.js";
import type { IResourceRegistry } from "../../registry/resource-registry.js";
import type { IInvariantRule, InvariantResult } from "../invariant-checker.js";

export class AggregateHasRepository implements IInvariantRule {
  name = "every aggregate root has a repository";

  appliesTo(resource: ResourceDescriptor): boolean {
    return resource.kind === "aggregate" && resource.declaration.root === true;
  }

  checkStructural(resource: ResourceDescriptor, registry: IResourceRegistry): InvariantResult {
    const repos = registry.getByKind("repository");
    const hasRepo = repos.some((r) =>
      r.dependencies.some((d) => d.targetId === resource.id && d.kind === "of"),
    );

    return {
      invariant: this.name,
      resourceId: resource.id,
      status: hasRepo ? "ok" : "violated",
      detail: hasRepo ? null : `No repository found for aggregate root ${resource.name}`,
      rationale: "every root is persistable",
    };
  }
}
