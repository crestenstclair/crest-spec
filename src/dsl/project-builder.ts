import type {
  ProjectConfig,
  ContextConfig,
  AdapterConfig,
  ContextRelationship,
  InvariantDescriptor,
  ResourceDescriptor,
  Meta,
} from "../types.js";
import { ResourceRegistry, type IResourceRegistry } from "../registry/resource-registry.js";
import { ContextBuilder } from "./context-builder.js";
import { setActiveProject } from "./singleton.js";

export class ProjectBuilder {
  private readonly registry: ResourceRegistry;
  private readonly projectMeta: Meta;
  private readonly name: string;

  constructor(name: string, config?: ProjectConfig) {
    this.name = name;
    this.projectMeta = config?.meta ?? {};
    this.registry = new ResourceRegistry();

    const descriptor: ResourceDescriptor = {
      id: `project.${name}`,
      kind: "project",
      name,
      context: null,
      layer: null,
      declaration: {
        layers: config?.layers,
        rules: config?.rules,
      },
      meta: this.projectMeta,
      dependencies: [],
    };
    this.registry.register(descriptor);
  }

  context(name: string, config: ContextConfig): ContextBuilder {
    const contextMeta = config.meta ?? {};
    const descriptor: ResourceDescriptor = {
      id: `context.${name}`,
      kind: "context",
      name,
      context: null,
      layer: null,
      declaration: {
        purpose: config.purpose,
        ubiquitousLanguage: config.ubiquitousLanguage,
      },
      meta: mergeMetas(this.projectMeta, contextMeta),
      dependencies: [],
    };
    this.registry.register(descriptor);
    return new ContextBuilder(name, this.projectMeta, contextMeta, this.registry);
  }

  adapter(name: string, config: AdapterConfig): void {
    const id = `adapter.${name}`;
    const descriptor: ResourceDescriptor = {
      id,
      kind: "adapter",
      name,
      context: null,
      layer: config.layer ?? "infrastructure",
      declaration: { implements: config.implements.name },
      meta: config.meta ?? {},
      dependencies: [{ targetId: config.implements.id, kind: "implements" }],
    };
    this.registry.register(descriptor);
  }

  contextMap(relationships: ContextRelationship[]): void {
    this.registry.setContextMap(relationships);
  }

  invariants(invariants: InvariantDescriptor[]): void {
    this.registry.setInvariants(invariants);
  }

  meta(meta: Meta): void {
    Object.assign(this.projectMeta, meta);
  }

  getRegistry(): IResourceRegistry & ResourceRegistry {
    return this.registry;
  }
}

function mergeMetas(...metas: Meta[]): Meta {
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

export function project(name: string, config?: ProjectConfig): ProjectBuilder {
  const builder = new ProjectBuilder(name, config);
  setActiveProject(builder);
  return builder;
}
