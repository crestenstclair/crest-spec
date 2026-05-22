import type { EntityConfig, ResourceDescriptor, Meta } from "../types.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";

export class AggregateBuilder {
  readonly id: string;
  readonly name: string;

  constructor(
    private readonly contextName: string,
    name: string,
    private readonly registry: IResourceRegistry,
  ) {
    this.name = name;
    this.id = `aggregate.${contextName}.${name}`;
  }

  entity(name: string, config: EntityConfig): void {
    const id = `entity.${this.contextName}.${this.name}.${name}`;
    const meta = config.meta ?? {};
    const descriptor: ResourceDescriptor = {
      id,
      kind: "entity",
      name,
      context: this.contextName,
      layer: null,
      declaration: { state: config.state },
      meta,
      dependencies: [{ targetId: this.id, kind: "of" }],
    };
    this.registry.register(descriptor);
  }
}
