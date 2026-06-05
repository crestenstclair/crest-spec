import type { EntityConfig, AssetDeclaration, ResourceDescriptor, Meta } from "../types.js";
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

  asset(name: string, config: AssetDeclaration): void {
    const id = `asset.${this.contextName}.${this.name}.${name}`;
    const dependencies = [
      { targetId: `assetKind.${config.kind}`, kind: "uses" as const },
      { targetId: this.id, kind: "uses" as const },
      ...(config.targets ?? []).map((t) => ({ targetId: t.id, kind: "uses" as const })),
    ];
    const descriptor: ResourceDescriptor = {
      id,
      kind: "asset",
      name,
      context: this.contextName,
      layer: null,
      declaration: {
        assetKind: config.kind,
        description: config.description,
      },
      meta: {
        prompts: config.prompts,
        references: config.references,
        ...config.meta,
      },
      dependencies,
    };
    this.registry.register(descriptor);
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
