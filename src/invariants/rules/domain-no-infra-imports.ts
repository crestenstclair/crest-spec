import type { ResourceDescriptor } from "../../types.js";
import type { IResourceRegistry } from "../../registry/resource-registry.js";
import type { IInvariantRule, InvariantResult } from "../invariant-checker.js";

export class DomainNoInfraImports implements IInvariantRule {
  name = "domain layer has no infrastructure imports";

  appliesTo(resource: ResourceDescriptor): boolean {
    return resource.layer === "domain";
  }

  checkGenerated(resource: ResourceDescriptor, fileContents: Map<string, string>, _registry: IResourceRegistry): InvariantResult {
    for (const [path, content] of fileContents) {
      const importLines = content.split("\n").filter((l) => l.match(/^\s*import\s/));
      for (const line of importLines) {
        if (line.includes("/infrastructure/") || line.includes("/infra/")) {
          return {
            invariant: this.name,
            resourceId: resource.id,
            status: "violated",
            detail: `${path} imports from infrastructure: ${line.trim()}`,
            rationale: "preserves Clean Architecture's Dependency Rule",
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
