import type {
  AggregateConfig,
  ValueObjectConfig,
  PortConfig,
  RepositoryConfig,
  ApplicationServiceConfig,
  DomainServiceConfig,
  AdapterConfig,
  EntityConfig,
  ResourceDescriptor,
  PortRef,
  AggregateRef,
  Meta,
} from "../types.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import { AggregateBuilder } from "./aggregate-builder.js";

export class ContextBuilder {
  readonly name: string;

  constructor(
    name: string,
    private readonly projectMeta: Meta,
    private readonly contextMeta: Meta,
    private readonly registry: IResourceRegistry,
  ) {
    this.name = name;
  }

  private mergeMeta(resourceMeta?: Meta): Meta {
    return mergeMetas(this.projectMeta, this.contextMeta, resourceMeta ?? {});
  }

  aggregate(name: string, config: AggregateConfig): AggregateBuilder & AggregateRef {
    const id = `aggregate.${this.name}.${name}`;
    const dependencies = config.implements
      ? [{ targetId: config.implements.id, kind: "implements" as const }]
      : [];

    const descriptor: ResourceDescriptor = {
      id,
      kind: "aggregate",
      name,
      context: this.name,
      layer: "domain",
      declaration: {
        root: config.root,
        purpose: config.purpose,
        state: config.state,
        implements: config.implements?.name,
      },
      meta: this.mergeMeta(config.meta),
      dependencies,
      commands: config.commands,
      events: config.events,
      invariants: config.invariants,
    };
    this.registry.register(descriptor);

    const builder = new AggregateBuilder(this.name, name, this.registry);
    return Object.assign(builder, { id, name } as AggregateRef);
  }

  valueObject(name: string, config: ValueObjectConfig): void {
    const id = `valueObject.${this.name}.${name}`;
    const descriptor: ResourceDescriptor = {
      id,
      kind: "valueObject",
      name,
      context: this.name,
      layer: "domain",
      declaration: {
        from: config.from,
        state: config.state,
        format: config.format,
        description: config.description,
        invariants: config.invariants,
      },
      meta: this.mergeMeta(config.meta),
      dependencies: [],
    };
    this.registry.register(descriptor);
  }

  port(name: string, config: PortConfig): PortRef {
    const id = `port.${this.name}.${name}`;
    const descriptor: ResourceDescriptor = {
      id,
      kind: "port",
      name,
      context: this.name,
      layer: "domain",
      declaration: { contract: config.contract },
      meta: this.mergeMeta(config.meta),
      dependencies: [],
    };
    this.registry.register(descriptor);
    return { id, name, contract: config.contract };
  }

  repository(name: string, config: RepositoryConfig): void {
    const id = `repository.${this.name}.${name}`;
    const descriptor: ResourceDescriptor = {
      id,
      kind: "repository",
      name,
      context: this.name,
      layer: "domain",
      declaration: { of: config.of.name, contract: config.contract },
      meta: this.mergeMeta(config.meta),
      dependencies: [{ targetId: config.of.id, kind: "of" }],
    };
    this.registry.register(descriptor);
  }

  applicationService(name: string, config: ApplicationServiceConfig): void {
    const id = `applicationService.${this.name}.${name}`;
    const dependencies = (config.uses ?? []).map((ref) => ({
      targetId: ref.id,
      kind: "uses" as const,
    }));
    const descriptor: ResourceDescriptor = {
      id,
      kind: "applicationService",
      name,
      context: this.name,
      layer: "application",
      declaration: {
        purpose: config.purpose,
        operations: config.operations,
      },
      meta: this.mergeMeta(config.meta),
      dependencies,
    };
    this.registry.register(descriptor);
  }

  domainService(name: string, config: DomainServiceConfig): void {
    const id = `domainService.${this.name}.${name}`;
    const dependencies = (config.uses ?? []).map((ref) => ({
      targetId: ref.id,
      kind: "uses" as const,
    }));
    const descriptor: ResourceDescriptor = {
      id,
      kind: "domainService",
      name,
      context: this.name,
      layer: "domain",
      declaration: { purpose: config.purpose },
      meta: this.mergeMeta(config.meta),
      dependencies,
    };
    this.registry.register(descriptor);
  }

  entity(name: string, config: EntityConfig): void {
    const id = `entity.${this.name}.${name}`;
    const descriptor: ResourceDescriptor = {
      id,
      kind: "entity",
      name,
      context: this.name,
      layer: "domain",
      declaration: { state: config.state },
      meta: this.mergeMeta(config.meta),
      dependencies: [],
    };
    this.registry.register(descriptor);
  }
}

export function mergeMetas(...metas: Meta[]): Meta {
  const result: Meta = {};
  for (const meta of metas) {
    for (const [key, value] of Object.entries(meta)) {
      if (value === undefined) continue;
      const existing = result[key];
      if (Array.isArray(existing) && Array.isArray(value)) {
        result[key] = [...existing, ...value];
      } else {
        result[key] = value;
      }
    }
  }
  return result;
}
