import type { ResourceDescriptor } from "../../types.js";
import type { IResourceRegistry } from "../../registry/resource-registry.js";
import type { IInvariantRule, InvariantResult } from "../invariant-checker.js";

export class ContractCompliance implements IInvariantRule {
  name = "contract compliance";

  appliesTo(resource: ResourceDescriptor): boolean {
    return resource.dependencies.some((d) => d.kind === "implements");
  }

  checkGenerated(resource: ResourceDescriptor, fileContents: Map<string, string>, registry: IResourceRegistry): InvariantResult {
    const allContent = [...fileContents.values()].join("\n");

    for (const dep of resource.dependencies.filter((d) => d.kind === "implements")) {
      const port = registry.getById(dep.targetId);
      if (!port) continue;

      const contract = port.declaration.contract as Record<string, string> | undefined;
      if (!contract) continue;

      for (const methodName of Object.keys(contract)) {
        if (!allContent.includes(methodName)) {
          return {
            invariant: this.name,
            resourceId: resource.id,
            status: "violated",
            detail: `Missing method "${methodName}" from port ${port.name}`,
            rationale: null,
          };
        }
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
