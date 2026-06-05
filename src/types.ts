export type ResourceKind =
  | "project"
  | "context"
  | "aggregate"
  | "entity"
  | "valueObject"
  | "command"
  | "event"
  | "port"
  | "adapter"
  | "repository"
  | "applicationService"
  | "domainService"
  | "factory"
  | "assetKind"
  | "asset";

export interface Meta {
  rules?: string[];
  prompts?: string[];
  references?: string[];
  examples?: string[];
  avoid?: string[];
  style?: string;
  notes?: string;
  rationale?: string;
  [key: string]: unknown;
}

export interface DependencyRef {
  targetId: string;
  kind: "implements" | "uses" | "consumes" | "publishes" | "of";
}

export interface CommandDescriptor {
  name: string;
  payload: Record<string, string>;
}

export interface EventDescriptor {
  name: string;
  payload: Record<string, string>;
}

export interface OperationDescriptor {
  name: string;
  input: Record<string, string>;
}

export interface InvariantDescriptor {
  text: string;
  meta?: Meta;
}

export interface LayerRule {
  layer: string;
  dependsOn: string[];
}

export interface ContextRelationship {
  from: string;
  to: string;
  kind: "customer-supplier" | "anti-corruption" | "shared-kernel" | "published-language";
  direction?: "upstream" | "downstream" | "both";
}

export interface ResourceDescriptor {
  id: string;
  kind: ResourceKind;
  name: string;
  context: string | null;
  layer: string | null;
  declaration: Record<string, unknown>;
  meta: Meta;
  dependencies: DependencyRef[];
  commands?: CommandDescriptor[];
  events?: EventDescriptor[];
  invariants?: string[];
}

export interface ProjectConfig {
  layers?: string[];
  rules?: LayerRule[];
  meta?: Meta;
}

export interface ContextConfig {
  purpose: string;
  ubiquitousLanguage?: Record<string, string>;
  meta?: Meta;
}

export interface AggregateConfig {
  root?: boolean;
  purpose?: string;
  implements?: PortRef;
  state?: Record<string, string>;
  invariants?: string[];
  commands?: CommandDescriptor[];
  events?: EventDescriptor[];
  meta?: Meta;
}

export interface PortRef {
  id: string;
  name: string;
  contract: Record<string, string>;
}

export interface ValueObjectConfig {
  from?: string;
  state?: Record<string, string>;
  format?: string;
  description?: string;
  invariants?: string[];
  meta?: Meta;
}

export interface EntityConfig {
  state: Record<string, string>;
  meta?: Meta;
}

export interface PortConfig {
  contract: Record<string, string>;
  meta?: Meta;
}

export interface RepositoryConfig {
  of: AggregateRef;
  contract?: Record<string, string>;
  meta?: Meta;
}

export interface AggregateRef {
  id: string;
  name: string;
}

export interface AdapterConfig {
  implements: PortRef;
  layer?: string;
  meta?: Meta;
}

export interface ApplicationServiceConfig {
  purpose: string;
  uses?: AggregateRef[];
  operations?: OperationDescriptor[];
  meta?: Meta;
}

export interface DomainServiceConfig {
  purpose: string;
  uses?: AggregateRef[];
  meta?: Meta;
}

export interface AssetKindConfig {
  description: string;
  prompts?: string[];
  references?: string[];
  filePattern?: string;
  meta?: Meta;
}

export interface ResourceRef {
  id: string;
}

export interface AssetDeclaration {
  kind: string;
  description?: string;
  targets?: ResourceRef[];
  prompts?: string[];
  references?: string[];
  meta?: Meta;
}
