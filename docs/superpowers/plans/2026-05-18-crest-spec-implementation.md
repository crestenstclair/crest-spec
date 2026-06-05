# crest-spec Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a declarative DSL and CLI tool that lets users declare DDD resources in TypeScript, diffs declarations against persisted state, and uses Claude to generate implementation code with full validation.

**Architecture:** Single Bun package with internal modules: DSL builders populate a resource registry, a planner diffs the registry against a SQLite state database, and an apply engine dispatches Claude to generate code validated through a constraint pipeline (tsc, contract matching, invariant checking, test execution). Classes with dependency injection throughout, interfaces for testability.

**Tech Stack:** Bun (runtime, SQLite, test runner), TypeScript, Anthropic SDK (`@anthropic-ai/sdk`), SHA-256 for content hashing.

---

## File Structure

```
src/
  types.ts                          — ResourceKind, Meta, ResourceDescriptor, configs, all shared types
  dsl/
    helpers.ts                      — command(), event(), operation(), invariant(), relationship(), layer()
    project-builder.ts              — ProjectBuilder class + project() factory
    context-builder.ts              — ContextBuilder class
    aggregate-builder.ts            — AggregateBuilder class
    singleton.ts                    — module-level project singleton for spec loading
    index.ts                        — re-exports
  registry/
    resource-registry.ts            — IResourceRegistry + ResourceRegistry
    index.ts
  state/
    schema.ts                       — SQL DDL strings
    types.ts                        — StoredResource, StoredFile, ApplyRecord, etc.
    state-database.ts               — IStateDatabase + StateDatabase
    index.ts
  planner/
    hash-computer.ts                — IHashComputer + HashComputer
    planner.ts                      — IPlanner + Planner
    plan.ts                         — Plan class with display()
    index.ts
  engine/
    response-parser.ts              — IResponseParser + ResponseParser
    prompt-builder.ts               — IPromptBuilder + PromptBuilder
    llm-client.ts                   — ILlmClient + AnthropicLlmClient
    constraint-loop.ts              — IConstraintLoop + ConstraintLoop
    apply-engine.ts                 — IApplyEngine + ApplyEngine
    index.ts
  invariants/
    invariant-checker.ts            — IInvariantChecker + InvariantChecker
    rules/
      aggregate-has-repository.ts
      context-boundaries.ts
      dependency-rule.ts
      mutations-through-services.ts
      domain-no-infra-imports.ts
      contract-compliance.ts
      index.ts
    index.ts
  cli/
    main.ts                         — entry point, arg parsing, command dispatch
    formatter.ts                    — ANSI output formatting
    commands/
      init.ts
      plan.ts
      apply.ts
      refresh.ts
      graph.ts
      contextmap.ts
      validate.ts
      log.ts
      history.ts
      diff.ts
      state-list.ts
      state-rm.ts
      unlock.ts
      vacuum.ts
      sql.ts
    index.ts
  index.ts                          — package public API
tests/
  helpers.ts                        — shared test fixtures (makeResource, makeRegistry, etc.)
  dsl/
    helpers.test.ts
    builders.test.ts
  registry/
    resource-registry.test.ts
  state/
    state-database.test.ts
  planner/
    hash-computer.test.ts
    planner.test.ts
  engine/
    response-parser.test.ts
    prompt-builder.test.ts
    constraint-loop.test.ts
    apply-engine.test.ts
  invariants/
    invariant-checker.test.ts
    rules.test.ts
  cli/
    commands.test.ts
```

---

## Phase 1: Foundation

### Task 1: Project Setup & Core Types

**Files:**
- Create: `package.json`
- Create: `tsconfig.json`
- Create: `src/types.ts`
- Create: `src/index.ts`

- [ ] **Step 1: Initialize the Bun project**

Run:
```bash
bun init -y
```

- [ ] **Step 2: Install dependencies**

Run:
```bash
bun add @anthropic-ai/sdk
bun add -d @types/bun typescript
```

- [ ] **Step 3: Configure tsconfig.json**

Write `tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ESNext",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "outDir": "dist",
    "rootDir": "src",
    "declaration": true,
    "sourceMap": true,
    "types": ["bun-types"]
  },
  "include": ["src/**/*.ts"],
  "exclude": ["node_modules", "dist", "tests"]
}
```

- [ ] **Step 4: Create directory structure**

Run:
```bash
mkdir -p src/{dsl,registry,state,planner,engine,invariants/rules,cli/commands}
mkdir -p tests/{dsl,registry,state,planner,engine,invariants,cli}
```

- [ ] **Step 5: Write core types**

Write `src/types.ts`:

```ts
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
  | "factory";

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
```

- [ ] **Step 6: Write package entry point**

Write `src/index.ts`:

```ts
export * from "./types.js";
export { project, command, event, operation, invariant, relationship, layer } from "./dsl/index.js";
```

- [ ] **Step 7: Commit**

```bash
git add package.json tsconfig.json bun.lock src/types.ts src/index.ts
git commit -m "feat: project setup and core types"
```

---

### Task 2: DSL Helper Functions

**Files:**
- Create: `src/dsl/helpers.ts`
- Create: `tests/dsl/helpers.test.ts`
- Create: `src/dsl/index.ts`

- [ ] **Step 1: Write failing tests for helper functions**

Write `tests/dsl/helpers.test.ts`:

```ts
import { describe, test, expect } from "bun:test";
import { command, event, operation, invariant, relationship, layer } from "../../src/dsl/helpers";

describe("command()", () => {
  test("creates a command descriptor with name and payload", () => {
    const cmd = command("RenameSong", { name: "string" });
    expect(cmd).toEqual({ name: "RenameSong", payload: { name: "string" } });
  });

  test("creates a command descriptor with empty payload", () => {
    const cmd = command("Play");
    expect(cmd).toEqual({ name: "Play", payload: {} });
  });
});

describe("event()", () => {
  test("creates an event descriptor with name and payload", () => {
    const evt = event("SongRenamed", { id: "SongId", name: "string" });
    expect(evt).toEqual({
      name: "SongRenamed",
      payload: { id: "SongId", name: "string" },
    });
  });
});

describe("operation()", () => {
  test("creates an operation descriptor", () => {
    const op = operation("renameSong", { input: { id: "SongId", name: "string" } });
    expect(op).toEqual({
      name: "renameSong",
      input: { id: "SongId", name: "string" },
    });
  });
});

describe("invariant()", () => {
  test("creates an invariant descriptor from a string", () => {
    const inv = invariant("tempo is between 20 and 999 BPM");
    expect(inv).toEqual({ text: "tempo is between 20 and 999 BPM" });
  });

  test("creates an invariant descriptor with meta", () => {
    const inv = invariant("all mutations go through ApplicationServices", {
      meta: { rationale: "single audit point" },
    });
    expect(inv).toEqual({
      text: "all mutations go through ApplicationServices",
      meta: { rationale: "single audit point" },
    });
  });
});

describe("relationship()", () => {
  test("creates a context relationship", () => {
    const rel = relationship("Playback", "Composition", {
      kind: "customer-supplier",
      direction: "downstream",
    });
    expect(rel).toEqual({
      from: "Playback",
      to: "Composition",
      kind: "customer-supplier",
      direction: "downstream",
    });
  });
});

describe("layer()", () => {
  test("creates a layer rule", () => {
    const rule = layer("domain").dependsOn([]);
    expect(rule).toEqual({ layer: "domain", dependsOn: [] });
  });

  test("creates a layer rule with dependencies", () => {
    const rule = layer("application").dependsOn(["domain"]);
    expect(rule).toEqual({ layer: "application", dependsOn: ["domain"] });
  });
});
```

- [ ] **Step 2: Run tests to verify failure**

Run: `bun test tests/dsl/helpers.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement helper functions**

Write `src/dsl/helpers.ts`:

```ts
import type {
  CommandDescriptor,
  EventDescriptor,
  OperationDescriptor,
  InvariantDescriptor,
  ContextRelationship,
  LayerRule,
  Meta,
} from "../types.js";

export function command(name: string, payload?: Record<string, string>): CommandDescriptor {
  return { name, payload: payload ?? {} };
}

export function event(name: string, payload?: Record<string, string>): EventDescriptor {
  return { name, payload: payload ?? {} };
}

export function operation(
  name: string,
  config: { input: Record<string, string> },
): OperationDescriptor {
  return { name, input: config.input };
}

export function invariant(
  text: string,
  config?: { meta?: Meta },
): InvariantDescriptor {
  const desc: InvariantDescriptor = { text };
  if (config?.meta) desc.meta = config.meta;
  return desc;
}

export function relationship(
  from: string,
  to: string,
  config: {
    kind: ContextRelationship["kind"];
    direction?: ContextRelationship["direction"];
  },
): ContextRelationship {
  const rel: ContextRelationship = { from, to, kind: config.kind };
  if (config.direction) rel.direction = config.direction;
  return rel;
}

export function layer(name: string): { dependsOn: (deps: string[]) => LayerRule } {
  return {
    dependsOn(deps: string[]): LayerRule {
      return { layer: name, dependsOn: deps };
    },
  };
}
```

Write `src/dsl/index.ts`:

```ts
export { command, event, operation, invariant, relationship, layer } from "./helpers.js";
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `bun test tests/dsl/helpers.test.ts`
Expected: PASS — all 8 tests pass

- [ ] **Step 5: Commit**

```bash
git add src/dsl/helpers.ts src/dsl/index.ts tests/dsl/helpers.test.ts
git commit -m "feat: DSL helper functions (command, event, operation, invariant, relationship, layer)"
```

---

### Task 3: DSL Builders

**Files:**
- Create: `src/dsl/singleton.ts`
- Create: `src/dsl/project-builder.ts`
- Create: `src/dsl/context-builder.ts`
- Create: `src/dsl/aggregate-builder.ts`
- Create: `tests/dsl/builders.test.ts`
- Create: `tests/helpers.ts`

- [ ] **Step 1: Write test fixtures**

Write `tests/helpers.ts`:

```ts
import type { ResourceDescriptor, Meta } from "../src/types";

export function makeResource(overrides: Partial<ResourceDescriptor> = {}): ResourceDescriptor {
  return {
    id: "aggregate.TestContext.TestResource",
    kind: "aggregate",
    name: "TestResource",
    context: "TestContext",
    layer: null,
    declaration: {},
    meta: {},
    dependencies: [],
    ...overrides,
  };
}
```

- [ ] **Step 2: Write failing tests for builders**

Write `tests/dsl/builders.test.ts`:

```ts
import { describe, test, expect, beforeEach } from "bun:test";
import { ProjectBuilder } from "../../src/dsl/project-builder";
import { command, event } from "../../src/dsl/helpers";
import { resetSingleton, getActiveProject } from "../../src/dsl/singleton";

beforeEach(() => {
  resetSingleton();
});

describe("ProjectBuilder", () => {
  test("project() creates a ProjectBuilder and sets it as active", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker", {
      layers: ["domain", "application", "infrastructure"],
    });
    expect(app).toBeInstanceOf(ProjectBuilder);
    expect(getActiveProject()).toBe(app);
  });

  test("stores project config in registry", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker", {
      layers: ["domain", "application"],
    });
    const registry = app.getRegistry();
    const projectResource = registry.getById("project.tracker");
    expect(projectResource).not.toBeNull();
    expect(projectResource!.kind).toBe("project");
    expect(projectResource!.name).toBe("tracker");
  });
});

describe("ContextBuilder", () => {
  test("context() creates a context resource", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", {
      purpose: "structural model of a song",
    });
    const registry = app.getRegistry();
    const ctx = registry.getById("context.Composition");
    expect(ctx).not.toBeNull();
    expect(ctx!.kind).toBe("context");
    expect(ctx!.declaration).toEqual({ purpose: "structural model of a song" });
  });
});

describe("ContextBuilder.aggregate()", () => {
  test("creates an aggregate resource with commands and events", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    comp.aggregate("Song", {
      root: true,
      state: { id: "SongId", name: "string" },
      commands: [command("RenameSong", { name: "string" })],
      events: [event("SongRenamed", { id: "SongId", name: "string" })],
    });

    const registry = app.getRegistry();
    const agg = registry.getById("aggregate.Composition.Song");
    expect(agg).not.toBeNull();
    expect(agg!.kind).toBe("aggregate");
    expect(agg!.context).toBe("Composition");
    expect(agg!.commands).toHaveLength(1);
    expect(agg!.commands![0].name).toBe("RenameSong");
    expect(agg!.events).toHaveLength(1);
  });
});

describe("ContextBuilder.valueObject()", () => {
  test("creates a value object resource", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    comp.valueObject("Ticks", { from: "number", description: "musical time" });

    const registry = app.getRegistry();
    const vo = registry.getById("valueObject.Composition.Ticks");
    expect(vo).not.toBeNull();
    expect(vo!.kind).toBe("valueObject");
  });
});

describe("ContextBuilder.port()", () => {
  test("creates a port resource and returns a PortRef", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    const portRef = comp.port("PhraseRender", {
      contract: { render: "(ctx: MusicalContext) => NoteEvent[]" },
    });

    expect(portRef.id).toBe("port.Composition.PhraseRender");
    expect(portRef.name).toBe("PhraseRender");
    expect(portRef.contract).toEqual({
      render: "(ctx: MusicalContext) => NoteEvent[]",
    });

    const registry = app.getRegistry();
    const port = registry.getById("port.Composition.PhraseRender");
    expect(port).not.toBeNull();
    expect(port!.kind).toBe("port");
  });
});

describe("ContextBuilder.repository()", () => {
  test("creates a repository with a dependency on its aggregate", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    const songAgg = comp.aggregate("Song", { root: true, state: { id: "SongId" } });
    comp.repository("SongRepository", { of: songAgg });

    const registry = app.getRegistry();
    const repo = registry.getById("repository.Composition.SongRepository");
    expect(repo).not.toBeNull();
    expect(repo!.dependencies).toEqual([
      { targetId: "aggregate.Composition.Song", kind: "of" },
    ]);
  });
});

describe("AggregateBuilder.entity()", () => {
  test("creates a child entity inside an aggregate", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    const agg = comp.aggregate("Chain", { root: true, state: { id: "ChainId" } });
    agg.entity("ChainSlot", { state: { at: "Index", phraseId: "PhraseId | null" } });

    const registry = app.getRegistry();
    const entity = registry.getById("entity.Composition.Chain.ChainSlot");
    expect(entity).not.toBeNull();
    expect(entity!.kind).toBe("entity");
  });
});

describe("aggregate with implements", () => {
  test("creates a dependency on the implemented port", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    const portRef = comp.port("PhraseRender", {
      contract: { render: "(ctx: MusicalContext) => NoteEvent[]" },
    });
    comp.aggregate("LinearPhrase", {
      root: true,
      implements: portRef,
      state: { id: "PhraseId" },
    });

    const registry = app.getRegistry();
    const agg = registry.getById("aggregate.Composition.LinearPhrase");
    expect(agg!.dependencies).toEqual([
      { targetId: "port.Composition.PhraseRender", kind: "implements" },
    ]);
  });
});

describe("meta inheritance", () => {
  test("context inherits project meta", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker", { meta: { style: "functional" } });
    const comp = app.context("Composition", { purpose: "test" });
    comp.aggregate("Song", { root: true, state: {} });

    const registry = app.getRegistry();
    const agg = registry.getById("aggregate.Composition.Song");
    expect(agg!.meta.style).toBe("functional");
  });

  test("resource meta overrides project meta for scalars", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker", { meta: { style: "functional" } });
    const comp = app.context("Composition", { purpose: "test" });
    comp.aggregate("Song", {
      root: true,
      state: {},
      meta: { style: "OOP" },
    });

    const registry = app.getRegistry();
    const agg = registry.getById("aggregate.Composition.Song");
    expect(agg!.meta.style).toBe("OOP");
  });

  test("resource meta concatenates project meta for arrays", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker", { meta: { avoid: ["any"] } });
    const comp = app.context("Composition", {
      purpose: "test",
      meta: { avoid: ["setInterval"] },
    });

    const registry = app.getRegistry();
    const ctx = registry.getById("context.Composition");
    expect(ctx!.meta.avoid).toEqual(["any", "setInterval"]);
  });
});

describe("contextMap()", () => {
  test("stores context relationships in the registry", () => {
    const { project } = require("../../src/dsl/project-builder");
    const { relationship } = require("../../src/dsl/helpers");
    const app = project("tracker");
    app.context("Playback", { purpose: "scheduling" });
    app.context("Composition", { purpose: "model" });
    app.contextMap([
      relationship("Playback", "Composition", {
        kind: "customer-supplier",
        direction: "downstream",
      }),
    ]);

    const registry = app.getRegistry();
    const map = registry.getContextMap();
    expect(map).toHaveLength(1);
    expect(map[0].from).toBe("Playback");
    expect(map[0].to).toBe("Composition");
  });
});

describe("applicationService()", () => {
  test("creates an application service with uses dependencies", () => {
    const { project } = require("../../src/dsl/project-builder");
    const { operation } = require("../../src/dsl/helpers");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    const songAgg = comp.aggregate("Song", { root: true, state: { id: "SongId" } });
    comp.applicationService("SongEditor", {
      purpose: "orchestrates Song commands",
      uses: [songAgg],
      operations: [operation("renameSong", { input: { id: "SongId", name: "string" } })],
    });

    const registry = app.getRegistry();
    const svc = registry.getById("applicationService.Composition.SongEditor");
    expect(svc).not.toBeNull();
    expect(svc!.kind).toBe("applicationService");
    expect(svc!.dependencies).toEqual([
      { targetId: "aggregate.Composition.Song", kind: "uses" },
    ]);
  });
});
```

- [ ] **Step 3: Run tests to verify failure**

Run: `bun test tests/dsl/builders.test.ts`
Expected: FAIL — modules not found

- [ ] **Step 4: Implement singleton**

Write `src/dsl/singleton.ts`:

```ts
import type { ProjectBuilder } from "./project-builder.js";

let activeProject: ProjectBuilder | null = null;

export function setActiveProject(project: ProjectBuilder): void {
  activeProject = project;
}

export function getActiveProject(): ProjectBuilder | null {
  return activeProject;
}

export function resetSingleton(): void {
  activeProject = null;
}
```

- [ ] **Step 5: Implement AggregateBuilder**

Write `src/dsl/aggregate-builder.ts`:

```ts
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
```

- [ ] **Step 6: Implement ContextBuilder**

Write `src/dsl/context-builder.ts`:

```ts
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
```

- [ ] **Step 7: Implement ProjectBuilder**

Write `src/dsl/project-builder.ts`:

```ts
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
```

Update `src/dsl/index.ts`:

```ts
export { command, event, operation, invariant, relationship, layer } from "./helpers.js";
export { project, ProjectBuilder } from "./project-builder.js";
export { ContextBuilder } from "./context-builder.js";
export { AggregateBuilder } from "./aggregate-builder.js";
export { getActiveProject, resetSingleton } from "./singleton.js";
```

- [ ] **Step 8: Stub ResourceRegistry to satisfy imports**

The builders depend on `IResourceRegistry`. We need a minimal stub before Task 4 fills it in fully.

Write `src/registry/resource-registry.ts`:

```ts
import type { ResourceDescriptor, ContextRelationship, InvariantDescriptor } from "../types.js";

export interface IResourceRegistry {
  register(descriptor: ResourceDescriptor): void;
  getById(id: string): ResourceDescriptor | null;
  getAll(): ResourceDescriptor[];
  getByKind(kind: string): ResourceDescriptor[];
  getByContext(contextId: string): ResourceDescriptor[];
  getDependents(id: string): ResourceDescriptor[];
  getDependencies(id: string): ResourceDescriptor[];
  topologicalOrder(): ResourceDescriptor[];
  getContextMap(): ContextRelationship[];
  getInvariants(): InvariantDescriptor[];
  setContextMap(relationships: ContextRelationship[]): void;
  setInvariants(invariants: InvariantDescriptor[]): void;
}

export class ResourceRegistry implements IResourceRegistry {
  private resources = new Map<string, ResourceDescriptor>();
  private contextRelationships: ContextRelationship[] = [];
  private projectInvariants: InvariantDescriptor[] = [];

  register(descriptor: ResourceDescriptor): void {
    if (this.resources.has(descriptor.id)) {
      throw new Error(`Duplicate resource ID: ${descriptor.id}`);
    }
    this.resources.set(descriptor.id, descriptor);
  }

  getById(id: string): ResourceDescriptor | null {
    return this.resources.get(id) ?? null;
  }

  getAll(): ResourceDescriptor[] {
    return [...this.resources.values()];
  }

  getByKind(kind: string): ResourceDescriptor[] {
    return this.getAll().filter((r) => r.kind === kind);
  }

  getByContext(contextId: string): ResourceDescriptor[] {
    return this.getAll().filter((r) => r.context === contextId);
  }

  getDependents(id: string): ResourceDescriptor[] {
    return this.getAll().filter((r) =>
      r.dependencies.some((d) => d.targetId === id),
    );
  }

  getDependencies(id: string): ResourceDescriptor[] {
    const resource = this.getById(id);
    if (!resource) return [];
    return resource.dependencies
      .map((d) => this.getById(d.targetId))
      .filter((r): r is ResourceDescriptor => r !== null);
  }

  topologicalOrder(): ResourceDescriptor[] {
    const visited = new Set<string>();
    const result: ResourceDescriptor[] = [];

    const visit = (id: string) => {
      if (visited.has(id)) return;
      visited.add(id);
      const resource = this.getById(id);
      if (!resource) return;
      for (const dep of resource.dependencies) {
        visit(dep.targetId);
      }
      result.push(resource);
    };

    for (const resource of this.resources.values()) {
      visit(resource.id);
    }
    return result;
  }

  getContextMap(): ContextRelationship[] {
    return this.contextRelationships;
  }

  getInvariants(): InvariantDescriptor[] {
    return this.projectInvariants;
  }

  setContextMap(relationships: ContextRelationship[]): void {
    this.contextRelationships = relationships;
  }

  setInvariants(invariants: InvariantDescriptor[]): void {
    this.projectInvariants = invariants;
  }
}
```

Write `src/registry/index.ts`:

```ts
export { ResourceRegistry, type IResourceRegistry } from "./resource-registry.js";
```

- [ ] **Step 9: Run tests to verify they pass**

Run: `bun test tests/dsl/builders.test.ts`
Expected: PASS — all tests pass

- [ ] **Step 10: Commit**

```bash
git add src/dsl/ src/registry/ tests/dsl/builders.test.ts tests/helpers.ts
git commit -m "feat: DSL builders (ProjectBuilder, ContextBuilder, AggregateBuilder) and ResourceRegistry stub"
```

---

### Task 4: Resource Registry

**Files:**
- Modify: `src/registry/resource-registry.ts` (already created in Task 3)
- Create: `tests/registry/resource-registry.test.ts`

- [ ] **Step 1: Write failing tests for registry queries and topological sort**

Write `tests/registry/resource-registry.test.ts`:

```ts
import { describe, test, expect, beforeEach } from "bun:test";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";

describe("ResourceRegistry", () => {
  let registry: ResourceRegistry;

  beforeEach(() => {
    registry = new ResourceRegistry();
  });

  test("register and getById", () => {
    const r = makeResource({ id: "aggregate.Comp.Song" });
    registry.register(r);
    expect(registry.getById("aggregate.Comp.Song")).toEqual(r);
  });

  test("register throws on duplicate ID", () => {
    const r = makeResource({ id: "aggregate.Comp.Song" });
    registry.register(r);
    expect(() => registry.register(r)).toThrow("Duplicate resource ID");
  });

  test("getById returns null for unknown ID", () => {
    expect(registry.getById("nonexistent")).toBeNull();
  });

  test("getAll returns all registered resources", () => {
    registry.register(makeResource({ id: "a.1" }));
    registry.register(makeResource({ id: "a.2" }));
    expect(registry.getAll()).toHaveLength(2);
  });

  test("getByKind filters by kind", () => {
    registry.register(makeResource({ id: "a.1", kind: "aggregate" }));
    registry.register(makeResource({ id: "p.1", kind: "port" }));
    expect(registry.getByKind("aggregate")).toHaveLength(1);
    expect(registry.getByKind("port")).toHaveLength(1);
  });

  test("getByContext filters by context", () => {
    registry.register(makeResource({ id: "a.1", context: "Comp" }));
    registry.register(makeResource({ id: "a.2", context: "Playback" }));
    expect(registry.getByContext("Comp")).toHaveLength(1);
  });

  test("getDependents returns resources that depend on the given ID", () => {
    registry.register(makeResource({ id: "port.Comp.Render", kind: "port" }));
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );
    const dependents = registry.getDependents("port.Comp.Render");
    expect(dependents).toHaveLength(1);
    expect(dependents[0].id).toBe("agg.Comp.Linear");
  });

  test("getDependencies returns resources this one depends on", () => {
    registry.register(makeResource({ id: "port.Comp.Render", kind: "port" }));
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );
    const deps = registry.getDependencies("agg.Comp.Linear");
    expect(deps).toHaveLength(1);
    expect(deps[0].id).toBe("port.Comp.Render");
  });

  test("topologicalOrder puts dependencies before dependents", () => {
    registry.register(makeResource({ id: "port.Render", kind: "port" }));
    registry.register(
      makeResource({
        id: "agg.Linear",
        dependencies: [{ targetId: "port.Render", kind: "implements" }],
      }),
    );
    registry.register(
      makeResource({
        id: "repo.LinearRepo",
        dependencies: [{ targetId: "agg.Linear", kind: "of" }],
      }),
    );

    const order = registry.topologicalOrder();
    const ids = order.map((r) => r.id);
    expect(ids.indexOf("port.Render")).toBeLessThan(ids.indexOf("agg.Linear"));
    expect(ids.indexOf("agg.Linear")).toBeLessThan(ids.indexOf("repo.LinearRepo"));
  });

  test("contextMap stores and retrieves relationships", () => {
    registry.setContextMap([
      { from: "Playback", to: "Composition", kind: "customer-supplier", direction: "downstream" },
    ]);
    const map = registry.getContextMap();
    expect(map).toHaveLength(1);
    expect(map[0].from).toBe("Playback");
  });

  test("invariants stores and retrieves project invariants", () => {
    registry.setInvariants([{ text: "no infra in domain" }]);
    expect(registry.getInvariants()).toHaveLength(1);
  });
});
```

- [ ] **Step 2: Run tests to verify they pass**

The registry was already implemented in Task 3. Run the tests to verify.

Run: `bun test tests/registry/resource-registry.test.ts`
Expected: PASS — all tests pass

- [ ] **Step 3: Commit**

```bash
git add tests/registry/resource-registry.test.ts
git commit -m "test: resource registry tests"
```

---

## Phase 2: Persistence & Planning

### Task 5: SQLite State Layer

**Files:**
- Create: `src/state/schema.ts`
- Create: `src/state/types.ts`
- Create: `src/state/state-database.ts`
- Create: `src/state/index.ts`
- Create: `tests/state/state-database.test.ts`

- [ ] **Step 1: Write state layer types**

Write `src/state/types.ts`:

```ts
export interface StoredResource {
  id: string;
  kind: string;
  context: string | null;
  declaration_hash: string;
  effective_hash: string;
  declaration_json: string;
  layer: string | null;
  settled_at: string | null;
  last_apply_id: number | null;
}

export interface StoredFile {
  path: string;
  resource_id: string;
  content_hash: string;
  generator: "deterministic" | "llm";
  model: string | null;
  prompt_hash: string | null;
  generated_at: string;
}

export interface StoredDependency {
  from_resource: string;
  to_resource: string;
  kind: string;
}

export interface StoredContextRelationship {
  from_context: string;
  to_context: string;
  kind: string;
  direction: string;
}

export interface ApplyRecord {
  id: number;
  started_at: string;
  finished_at: string | null;
  status: "running" | "ok" | "failed" | "aborted";
  spec_hash: string;
  notes: string | null;
}

export interface ActionRecord {
  apply_id: number;
  resource_id: string;
  action: "create" | "modify" | "destroy" | "noop";
  outcome: string;
}

export interface GenerationRecord {
  id?: number;
  apply_id: number;
  resource_id: string;
  model: string;
  prompt_hash: string;
  prompt_text: string;
  output_text: string;
  retries: number;
  outcome: "accepted" | "rejected";
  rejection_reason: string | null;
  created_at: string;
}

export interface InvariantCheckRecord {
  apply_id: number;
  invariant: string;
  resource_id: string | null;
  status: "ok" | "violated";
  detail: string | null;
}

export interface LockRecord {
  holder: string;
  acquired_at: string;
}
```

- [ ] **Step 2: Write schema DDL**

Write `src/state/schema.ts`:

```ts
export const SCHEMA_VERSION = 1;

export const SCHEMA_DDL = `
CREATE TABLE IF NOT EXISTS resources (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  context TEXT,
  declaration_hash TEXT NOT NULL,
  effective_hash TEXT NOT NULL,
  declaration_json TEXT NOT NULL,
  layer TEXT,
  settled_at TEXT,
  last_apply_id INTEGER
);

CREATE TABLE IF NOT EXISTS generated_files (
  path TEXT PRIMARY KEY,
  resource_id TEXT NOT NULL REFERENCES resources(id),
  content_hash TEXT NOT NULL,
  generator TEXT NOT NULL,
  model TEXT,
  prompt_hash TEXT,
  generated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS dependencies (
  from_resource TEXT NOT NULL REFERENCES resources(id),
  to_resource TEXT NOT NULL REFERENCES resources(id),
  kind TEXT NOT NULL,
  PRIMARY KEY (from_resource, to_resource, kind)
);

CREATE TABLE IF NOT EXISTS context_relationships (
  from_context TEXT NOT NULL,
  to_context TEXT NOT NULL,
  kind TEXT NOT NULL,
  direction TEXT NOT NULL,
  PRIMARY KEY (from_context, to_context, kind)
);

CREATE TABLE IF NOT EXISTS applies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  status TEXT NOT NULL,
  spec_hash TEXT NOT NULL,
  notes TEXT
);

CREATE TABLE IF NOT EXISTS apply_actions (
  apply_id INTEGER NOT NULL REFERENCES applies(id),
  resource_id TEXT NOT NULL,
  action TEXT NOT NULL,
  outcome TEXT NOT NULL,
  PRIMARY KEY (apply_id, resource_id)
);

CREATE TABLE IF NOT EXISTS generations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  apply_id INTEGER NOT NULL REFERENCES applies(id),
  resource_id TEXT NOT NULL,
  model TEXT NOT NULL,
  prompt_hash TEXT NOT NULL,
  prompt_text TEXT NOT NULL,
  output_text TEXT NOT NULL,
  retries INTEGER NOT NULL,
  outcome TEXT NOT NULL,
  rejection_reason TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS invariant_checks (
  apply_id INTEGER NOT NULL,
  invariant TEXT NOT NULL,
  resource_id TEXT,
  status TEXT NOT NULL,
  detail TEXT,
  PRIMARY KEY (apply_id, invariant, resource_id)
);

CREATE TABLE IF NOT EXISTS lock (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  holder TEXT NOT NULL,
  acquired_at TEXT NOT NULL
);
`;
```

- [ ] **Step 3: Write failing tests for StateDatabase**

Write `tests/state/state-database.test.ts`:

```ts
import { describe, test, expect, beforeEach } from "bun:test";
import { StateDatabase } from "../../src/state/state-database";
import type { StoredResource, StoredFile, StoredDependency } from "../../src/state/types";

describe("StateDatabase", () => {
  let db: StateDatabase;

  beforeEach(() => {
    db = new StateDatabase(":memory:");
  });

  describe("resources", () => {
    const resource: StoredResource = {
      id: "aggregate.Comp.Song",
      kind: "aggregate",
      context: "Comp",
      declaration_hash: "abc123",
      effective_hash: "def456",
      declaration_json: '{"state":{"id":"SongId"}}',
      layer: "domain",
      settled_at: null,
      last_apply_id: null,
    };

    test("upsertResource and getResource", () => {
      db.upsertResource(resource);
      const result = db.getResource("aggregate.Comp.Song");
      expect(result).not.toBeNull();
      expect(result!.id).toBe("aggregate.Comp.Song");
      expect(result!.effective_hash).toBe("def456");
    });

    test("getResource returns null for unknown ID", () => {
      expect(db.getResource("nonexistent")).toBeNull();
    });

    test("getAllResources returns all resources", () => {
      db.upsertResource(resource);
      db.upsertResource({ ...resource, id: "aggregate.Comp.Chain", declaration_hash: "x", effective_hash: "y" });
      expect(db.getAllResources()).toHaveLength(2);
    });

    test("upsertResource updates existing resource", () => {
      db.upsertResource(resource);
      db.upsertResource({ ...resource, effective_hash: "updated" });
      const result = db.getResource("aggregate.Comp.Song");
      expect(result!.effective_hash).toBe("updated");
    });

    test("deleteResource removes a resource", () => {
      db.upsertResource(resource);
      db.deleteResource("aggregate.Comp.Song");
      expect(db.getResource("aggregate.Comp.Song")).toBeNull();
    });
  });

  describe("generated files", () => {
    test("upsertGeneratedFile and getGeneratedFile", () => {
      const file: StoredFile = {
        path: "src/comp/song.ts",
        resource_id: "aggregate.Comp.Song",
        content_hash: "hash123",
        generator: "llm",
        model: "claude-sonnet-4-6",
        prompt_hash: "phash",
        generated_at: "2026-05-18T00:00:00Z",
      };
      db.upsertGeneratedFile(file);
      const result = db.getGeneratedFile("src/comp/song.ts");
      expect(result).not.toBeNull();
      expect(result!.content_hash).toBe("hash123");
    });

    test("getFilesForResource returns files for a resource", () => {
      const file: StoredFile = {
        path: "src/comp/song.ts",
        resource_id: "aggregate.Comp.Song",
        content_hash: "h1",
        generator: "llm",
        model: null,
        prompt_hash: null,
        generated_at: "2026-05-18T00:00:00Z",
      };
      db.upsertGeneratedFile(file);
      db.upsertGeneratedFile({ ...file, path: "src/comp/song.test.ts", content_hash: "h2" });
      expect(db.getFilesForResource("aggregate.Comp.Song")).toHaveLength(2);
    });
  });

  describe("dependencies", () => {
    test("setDependencies and getDependencies", () => {
      const deps: StoredDependency[] = [
        { from_resource: "agg.Linear", to_resource: "port.Render", kind: "implements" },
      ];
      db.setDependencies("agg.Linear", deps);
      const result = db.getDependencies("agg.Linear");
      expect(result).toHaveLength(1);
      expect(result[0].to_resource).toBe("port.Render");
    });

    test("getDependents returns reverse dependencies", () => {
      const deps: StoredDependency[] = [
        { from_resource: "agg.Linear", to_resource: "port.Render", kind: "implements" },
      ];
      db.setDependencies("agg.Linear", deps);
      const result = db.getDependents("port.Render");
      expect(result).toHaveLength(1);
      expect(result[0].from_resource).toBe("agg.Linear");
    });
  });

  describe("applies", () => {
    test("beginApply creates a running apply", () => {
      const apply = db.beginApply("spechash123");
      expect(apply.id).toBeGreaterThan(0);
      expect(apply.status).toBe("running");
    });

    test("finishApply updates status", () => {
      const apply = db.beginApply("spechash123");
      db.finishApply(apply.id, "ok");
      const applies = db.getApplies();
      expect(applies[0].status).toBe("ok");
    });

    test("recordAction stores an action", () => {
      const apply = db.beginApply("spechash123");
      db.recordAction(apply.id, "aggregate.Comp.Song", "create", "success");
      const actions = db.getApplyActions(apply.id);
      expect(actions).toHaveLength(1);
      expect(actions[0].action).toBe("create");
    });
  });

  describe("generations", () => {
    test("recordGeneration and getGenerationsForResource", () => {
      const apply = db.beginApply("hash");
      db.recordGeneration({
        apply_id: apply.id,
        resource_id: "aggregate.Comp.Song",
        model: "claude-sonnet-4-6",
        prompt_hash: "ph",
        prompt_text: "generate Song",
        output_text: "export type Song = {}",
        retries: 0,
        outcome: "accepted",
        rejection_reason: null,
        created_at: "2026-05-18T00:00:00Z",
      });
      const gens = db.getGenerationsForResource("aggregate.Comp.Song");
      expect(gens).toHaveLength(1);
      expect(gens[0].outcome).toBe("accepted");
    });
  });

  describe("coordination lock", () => {
    test("acquireLock succeeds when no lock exists", () => {
      expect(db.acquireLock("worker-1")).toBe(true);
    });

    test("acquireLock fails when lock is held", () => {
      db.acquireLock("worker-1");
      expect(db.acquireLock("worker-2")).toBe(false);
    });

    test("getLock returns current lock holder", () => {
      db.acquireLock("worker-1");
      const lock = db.getLock();
      expect(lock).not.toBeNull();
      expect(lock!.holder).toBe("worker-1");
    });

    test("releaseLock clears the lock", () => {
      db.acquireLock("worker-1");
      db.releaseLock();
      expect(db.getLock()).toBeNull();
    });

    test("forceClearLock clears even without holding", () => {
      db.acquireLock("worker-1");
      db.forceClearLock();
      expect(db.getLock()).toBeNull();
    });
  });

  describe("invariant checks", () => {
    test("recordInvariantCheck stores a check", () => {
      const apply = db.beginApply("hash");
      db.recordInvariantCheck({
        apply_id: apply.id,
        invariant: "domain layer has no infrastructure imports",
        resource_id: "aggregate.Comp.Song",
        status: "ok",
        detail: null,
      });
    });
  });
});
```

- [ ] **Step 4: Run tests to verify failure**

Run: `bun test tests/state/state-database.test.ts`
Expected: FAIL — module not found

- [ ] **Step 5: Implement StateDatabase**

Write `src/state/state-database.ts`:

```ts
import { Database } from "bun:sqlite";
import { SCHEMA_DDL, SCHEMA_VERSION } from "./schema.js";
import type {
  StoredResource,
  StoredFile,
  StoredDependency,
  StoredContextRelationship,
  ApplyRecord,
  ActionRecord,
  GenerationRecord,
  InvariantCheckRecord,
  LockRecord,
} from "./types.js";

export interface IStateDatabase {
  getResource(id: string): StoredResource | null;
  getAllResources(): StoredResource[];
  upsertResource(resource: StoredResource): void;
  deleteResource(id: string): void;

  getGeneratedFile(path: string): StoredFile | null;
  getFilesForResource(resourceId: string): StoredFile[];
  upsertGeneratedFile(file: StoredFile): void;
  deleteGeneratedFile(path: string): void;

  setDependencies(resourceId: string, deps: StoredDependency[]): void;
  getDependencies(resourceId: string): StoredDependency[];
  getDependents(resourceId: string): StoredDependency[];
  setContextRelationships(relationships: StoredContextRelationship[]): void;

  beginApply(specHash: string): ApplyRecord;
  recordAction(applyId: number, resourceId: string, action: string, outcome: string): void;
  finishApply(applyId: number, status: string): void;

  recordGeneration(gen: GenerationRecord): void;
  getGenerationsForResource(resourceId: string): GenerationRecord[];

  recordInvariantCheck(check: InvariantCheckRecord): void;

  acquireLock(holder: string): boolean;
  releaseLock(): void;
  getLock(): LockRecord | null;
  forceClearLock(): void;

  getApplies(limit?: number): ApplyRecord[];
  getApplyActions(applyId: number): ActionRecord[];
}

export class StateDatabase implements IStateDatabase {
  private db: Database;

  constructor(path: string) {
    this.db = new Database(path);
    this.db.run("PRAGMA journal_mode = WAL");
    this.db.run("PRAGMA foreign_keys = ON");
    this.migrate();
  }

  private migrate(): void {
    const version = this.db.query("PRAGMA user_version").get() as { user_version: number };
    if (version.user_version < SCHEMA_VERSION) {
      this.db.run(SCHEMA_DDL);
      this.db.run(`PRAGMA user_version = ${SCHEMA_VERSION}`);
    }
  }

  getResource(id: string): StoredResource | null {
    return this.db.query("SELECT * FROM resources WHERE id = ?").get(id) as StoredResource | null;
  }

  getAllResources(): StoredResource[] {
    return this.db.query("SELECT * FROM resources").all() as StoredResource[];
  }

  upsertResource(resource: StoredResource): void {
    this.db
      .query(
        `INSERT INTO resources (id, kind, context, declaration_hash, effective_hash, declaration_json, layer, settled_at, last_apply_id)
         VALUES ($id, $kind, $context, $declaration_hash, $effective_hash, $declaration_json, $layer, $settled_at, $last_apply_id)
         ON CONFLICT(id) DO UPDATE SET
           kind = $kind, context = $context, declaration_hash = $declaration_hash,
           effective_hash = $effective_hash, declaration_json = $declaration_json,
           layer = $layer, settled_at = $settled_at, last_apply_id = $last_apply_id`,
      )
      .run({
        $id: resource.id,
        $kind: resource.kind,
        $context: resource.context,
        $declaration_hash: resource.declaration_hash,
        $effective_hash: resource.effective_hash,
        $declaration_json: resource.declaration_json,
        $layer: resource.layer,
        $settled_at: resource.settled_at,
        $last_apply_id: resource.last_apply_id,
      });
  }

  deleteResource(id: string): void {
    this.db.query("DELETE FROM resources WHERE id = ?").run(id);
  }

  getGeneratedFile(path: string): StoredFile | null {
    return this.db.query("SELECT * FROM generated_files WHERE path = ?").get(path) as StoredFile | null;
  }

  getFilesForResource(resourceId: string): StoredFile[] {
    return this.db
      .query("SELECT * FROM generated_files WHERE resource_id = ?")
      .all(resourceId) as StoredFile[];
  }

  upsertGeneratedFile(file: StoredFile): void {
    this.db
      .query(
        `INSERT INTO generated_files (path, resource_id, content_hash, generator, model, prompt_hash, generated_at)
         VALUES ($path, $resource_id, $content_hash, $generator, $model, $prompt_hash, $generated_at)
         ON CONFLICT(path) DO UPDATE SET
           resource_id = $resource_id, content_hash = $content_hash,
           generator = $generator, model = $model, prompt_hash = $prompt_hash,
           generated_at = $generated_at`,
      )
      .run({
        $path: file.path,
        $resource_id: file.resource_id,
        $content_hash: file.content_hash,
        $generator: file.generator,
        $model: file.model,
        $prompt_hash: file.prompt_hash,
        $generated_at: file.generated_at,
      });
  }

  deleteGeneratedFile(path: string): void {
    this.db.query("DELETE FROM generated_files WHERE path = ?").run(path);
  }

  setDependencies(resourceId: string, deps: StoredDependency[]): void {
    this.db.query("DELETE FROM dependencies WHERE from_resource = ?").run(resourceId);
    const insert = this.db.query(
      "INSERT INTO dependencies (from_resource, to_resource, kind) VALUES ($from, $to, $kind)",
    );
    for (const dep of deps) {
      insert.run({ $from: dep.from_resource, $to: dep.to_resource, $kind: dep.kind });
    }
  }

  getDependencies(resourceId: string): StoredDependency[] {
    return this.db
      .query("SELECT * FROM dependencies WHERE from_resource = ?")
      .all(resourceId) as StoredDependency[];
  }

  getDependents(resourceId: string): StoredDependency[] {
    return this.db
      .query("SELECT * FROM dependencies WHERE to_resource = ?")
      .all(resourceId) as StoredDependency[];
  }

  setContextRelationships(relationships: StoredContextRelationship[]): void {
    this.db.run("DELETE FROM context_relationships");
    const insert = this.db.query(
      `INSERT INTO context_relationships (from_context, to_context, kind, direction)
       VALUES ($from, $to, $kind, $direction)`,
    );
    for (const rel of relationships) {
      insert.run({
        $from: rel.from_context,
        $to: rel.to_context,
        $kind: rel.kind,
        $direction: rel.direction,
      });
    }
  }

  beginApply(specHash: string): ApplyRecord {
    const now = new Date().toISOString();
    this.db
      .query(
        `INSERT INTO applies (started_at, status, spec_hash) VALUES ($started_at, 'running', $spec_hash)`,
      )
      .run({ $started_at: now, $spec_hash: specHash });
    const row = this.db.query("SELECT * FROM applies ORDER BY id DESC LIMIT 1").get() as ApplyRecord;
    return row;
  }

  recordAction(applyId: number, resourceId: string, action: string, outcome: string): void {
    this.db
      .query(
        `INSERT INTO apply_actions (apply_id, resource_id, action, outcome)
         VALUES ($apply_id, $resource_id, $action, $outcome)`,
      )
      .run({ $apply_id: applyId, $resource_id: resourceId, $action: action, $outcome: outcome });
  }

  finishApply(applyId: number, status: string): void {
    const now = new Date().toISOString();
    this.db
      .query("UPDATE applies SET finished_at = $finished_at, status = $status WHERE id = $id")
      .run({ $finished_at: now, $status: status, $id: applyId });
  }

  recordGeneration(gen: GenerationRecord): void {
    this.db
      .query(
        `INSERT INTO generations (apply_id, resource_id, model, prompt_hash, prompt_text, output_text, retries, outcome, rejection_reason, created_at)
         VALUES ($apply_id, $resource_id, $model, $prompt_hash, $prompt_text, $output_text, $retries, $outcome, $rejection_reason, $created_at)`,
      )
      .run({
        $apply_id: gen.apply_id,
        $resource_id: gen.resource_id,
        $model: gen.model,
        $prompt_hash: gen.prompt_hash,
        $prompt_text: gen.prompt_text,
        $output_text: gen.output_text,
        $retries: gen.retries,
        $outcome: gen.outcome,
        $rejection_reason: gen.rejection_reason,
        $created_at: gen.created_at,
      });
  }

  getGenerationsForResource(resourceId: string): GenerationRecord[] {
    return this.db
      .query("SELECT * FROM generations WHERE resource_id = ? ORDER BY created_at")
      .all(resourceId) as GenerationRecord[];
  }

  recordInvariantCheck(check: InvariantCheckRecord): void {
    this.db
      .query(
        `INSERT INTO invariant_checks (apply_id, invariant, resource_id, status, detail)
         VALUES ($apply_id, $invariant, $resource_id, $status, $detail)`,
      )
      .run({
        $apply_id: check.apply_id,
        $invariant: check.invariant,
        $resource_id: check.resource_id,
        $status: check.status,
        $detail: check.detail,
      });
  }

  acquireLock(holder: string): boolean {
    try {
      const now = new Date().toISOString();
      this.db
        .query("INSERT INTO lock (id, holder, acquired_at) VALUES (1, $holder, $acquired_at)")
        .run({ $holder: holder, $acquired_at: now });
      return true;
    } catch {
      return false;
    }
  }

  releaseLock(): void {
    this.db.run("DELETE FROM lock WHERE id = 1");
  }

  getLock(): LockRecord | null {
    return this.db.query("SELECT holder, acquired_at FROM lock WHERE id = 1").get() as LockRecord | null;
  }

  forceClearLock(): void {
    this.db.run("DELETE FROM lock WHERE id = 1");
  }

  getApplies(limit?: number): ApplyRecord[] {
    const sql = limit
      ? "SELECT * FROM applies ORDER BY id DESC LIMIT ?"
      : "SELECT * FROM applies ORDER BY id DESC";
    return (limit ? this.db.query(sql).all(limit) : this.db.query(sql).all()) as ApplyRecord[];
  }

  getApplyActions(applyId: number): ActionRecord[] {
    return this.db
      .query("SELECT * FROM apply_actions WHERE apply_id = ?")
      .all(applyId) as ActionRecord[];
  }
}
```

Write `src/state/index.ts`:

```ts
export { StateDatabase, type IStateDatabase } from "./state-database.js";
export * from "./types.js";
export { SCHEMA_VERSION } from "./schema.js";
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `bun test tests/state/state-database.test.ts`
Expected: PASS — all tests pass

- [ ] **Step 7: Commit**

```bash
git add src/state/ tests/state/
git commit -m "feat: SQLite state layer (schema, StateDatabase, CRUD, locking, history)"
```

---

### Task 6: Hash Computer

**Files:**
- Create: `src/planner/hash-computer.ts`
- Create: `tests/planner/hash-computer.test.ts`

- [ ] **Step 1: Write failing tests for hash computation**

Write `tests/planner/hash-computer.test.ts`:

```ts
import { describe, test, expect, beforeEach } from "bun:test";
import { HashComputer } from "../../src/planner/hash-computer";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";

describe("HashComputer", () => {
  let registry: ResourceRegistry;
  let hashComputer: HashComputer;

  beforeEach(() => {
    registry = new ResourceRegistry();
    hashComputer = new HashComputer("claude-sonnet-4-6");
  });

  test("computes a hash for a simple resource", () => {
    registry.register(makeResource({ id: "vo.Comp.Ticks", kind: "valueObject" }));
    const hashes = hashComputer.computeAll(registry);
    expect(hashes.get("vo.Comp.Ticks")).toBeDefined();
    expect(typeof hashes.get("vo.Comp.Ticks")).toBe("string");
    expect(hashes.get("vo.Comp.Ticks")!.length).toBe(64);
  });

  test("same declaration produces same hash (deterministic)", () => {
    registry.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        declaration: { from: "number" },
      }),
    );
    const hashes1 = hashComputer.computeAll(registry);

    const registry2 = new ResourceRegistry();
    registry2.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        declaration: { from: "number" },
      }),
    );
    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("vo.Comp.Ticks")).toBe(hashes2.get("vo.Comp.Ticks"));
  });

  test("different declarations produce different hashes", () => {
    registry.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        declaration: { from: "number" },
      }),
    );
    const hashes1 = hashComputer.computeAll(registry);

    const registry2 = new ResourceRegistry();
    registry2.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        declaration: { from: "string" },
      }),
    );
    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("vo.Comp.Ticks")).not.toBe(hashes2.get("vo.Comp.Ticks"));
  });

  test("changing meta changes the hash", () => {
    registry.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        meta: { style: "functional" },
      }),
    );
    const hashes1 = hashComputer.computeAll(registry);

    const registry2 = new ResourceRegistry();
    registry2.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        meta: { style: "OOP" },
      }),
    );
    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("vo.Comp.Ticks")).not.toBe(hashes2.get("vo.Comp.Ticks"));
  });

  test("dependency hash is folded into dependent hash", () => {
    registry.register(makeResource({ id: "port.Render", kind: "port", declaration: { contract: { render: "() => void" } } }));
    registry.register(
      makeResource({
        id: "agg.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Render", kind: "implements" }],
      }),
    );
    const hashes1 = hashComputer.computeAll(registry);

    const registry2 = new ResourceRegistry();
    registry2.register(makeResource({ id: "port.Render", kind: "port", declaration: { contract: { render: "() => NoteEvent[]" } } }));
    registry2.register(
      makeResource({
        id: "agg.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Render", kind: "implements" }],
      }),
    );
    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("agg.Linear")).not.toBe(hashes2.get("agg.Linear"));
  });

  test("changing model identifier changes all hashes", () => {
    registry.register(makeResource({ id: "vo.Ticks", kind: "valueObject" }));
    const hashes1 = hashComputer.computeAll(registry);

    const hashComputer2 = new HashComputer("claude-opus-4-6");
    const hashes2 = hashComputer2.computeAll(registry);

    expect(hashes1.get("vo.Ticks")).not.toBe(hashes2.get("vo.Ticks"));
  });
});
```

- [ ] **Step 2: Run tests to verify failure**

Run: `bun test tests/planner/hash-computer.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement HashComputer**

Write `src/planner/hash-computer.ts`:

```ts
import { createHash } from "crypto";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { ResourceDescriptor } from "../types.js";

export interface IHashComputer {
  computeAll(registry: IResourceRegistry): Map<string, string>;
}

export class HashComputer implements IHashComputer {
  constructor(private readonly modelIdentifier: string) {}

  computeAll(registry: IResourceRegistry): Map<string, string> {
    const hashes = new Map<string, string>();
    const ordered = registry.topologicalOrder();

    for (const resource of ordered) {
      const hash = this.computeOne(resource, hashes, registry);
      hashes.set(resource.id, hash);
    }

    return hashes;
  }

  private computeOne(
    resource: ResourceDescriptor,
    computedHashes: Map<string, string>,
    registry: IResourceRegistry,
  ): string {
    const hasher = createHash("sha256");

    hasher.update(stableStringify(resource.declaration));
    hasher.update(stableStringify(resource.meta));
    hasher.update(stableStringify(resource.invariants ?? []));

    for (const dep of resource.dependencies) {
      const depHash = computedHashes.get(dep.targetId);
      if (depHash) {
        hasher.update(depHash);
      }
    }

    hasher.update(this.modelIdentifier);

    return hasher.digest("hex");
  }
}

function stableStringify(obj: unknown): string {
  return JSON.stringify(sortKeys(obj));
}

function sortKeys(obj: unknown): unknown {
  if (obj === null || obj === undefined) return obj;
  if (Array.isArray(obj)) return obj.map(sortKeys);
  if (typeof obj === "object") {
    const sorted: Record<string, unknown> = {};
    for (const key of Object.keys(obj as Record<string, unknown>).sort()) {
      sorted[key] = sortKeys((obj as Record<string, unknown>)[key]);
    }
    return sorted;
  }
  return obj;
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `bun test tests/planner/hash-computer.test.ts`
Expected: PASS — all 6 tests pass

- [ ] **Step 5: Commit**

```bash
git add src/planner/hash-computer.ts tests/planner/hash-computer.test.ts
git commit -m "feat: HashComputer for effective_hash computation"
```

---

### Task 7: Planner

**Files:**
- Create: `src/planner/plan.ts`
- Create: `src/planner/planner.ts`
- Create: `src/planner/index.ts`
- Create: `tests/planner/planner.test.ts`

- [ ] **Step 1: Write Plan class**

Write `src/planner/plan.ts`:

```ts
export interface PlannedAction {
  resourceId: string;
  action: "create" | "modify" | "destroy" | "refresh";
  reason: string;
  affectedFiles: string[];
  cascadedFrom?: string;
}

export interface InvariantViolation {
  invariant: string;
  resourceId: string | null;
  detail: string;
}

export class Plan {
  constructor(
    readonly actions: PlannedAction[],
    readonly invariantViolations: InvariantViolation[],
  ) {}

  get isEmpty(): boolean {
    return this.actions.length === 0 && this.invariantViolations.length === 0;
  }

  display(): string {
    if (this.isEmpty) return "No changes detected.";

    const lines: string[] = [];

    const creates = this.actions.filter((a) => a.action === "create");
    const modifies = this.actions.filter((a) => a.action === "modify");
    const destroys = this.actions.filter((a) => a.action === "destroy");
    const refreshes = this.actions.filter((a) => a.action === "refresh");

    for (const action of [...creates, ...modifies, ...destroys, ...refreshes]) {
      const prefix =
        action.action === "create" ? "+" :
        action.action === "modify" ? "~" :
        action.action === "destroy" ? "-" : "?";

      lines.push(`${prefix} ${action.resourceId}`);
      lines.push(`  reason: ${action.reason}`);
      if (action.cascadedFrom) {
        lines.push(`  cascaded from: ${action.cascadedFrom}`);
      }
      if (action.affectedFiles.length > 0) {
        lines.push(`  files: ${action.affectedFiles.join(", ")}`);
      }
      lines.push("");
    }

    if (this.invariantViolations.length > 0) {
      lines.push("Invariant violations:");
      for (const v of this.invariantViolations) {
        lines.push(`  ! ${v.invariant}`);
        if (v.resourceId) lines.push(`    resource: ${v.resourceId}`);
        lines.push(`    detail: ${v.detail}`);
      }
    }

    return lines.join("\n");
  }
}
```

- [ ] **Step 2: Write failing tests for Planner**

Write `tests/planner/planner.test.ts`:

```ts
import { describe, test, expect, beforeEach } from "bun:test";
import { Planner } from "../../src/planner/planner";
import { HashComputer } from "../../src/planner/hash-computer";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { StateDatabase } from "../../src/state/state-database";
import { makeResource } from "../helpers";

describe("Planner", () => {
  let registry: ResourceRegistry;
  let state: StateDatabase;
  let planner: Planner;

  beforeEach(() => {
    registry = new ResourceRegistry();
    state = new StateDatabase(":memory:");
    const hashComputer = new HashComputer("claude-sonnet-4-6");
    planner = new Planner(hashComputer);
  });

  test("new resource produces a create action", () => {
    registry.register(makeResource({ id: "vo.Comp.Ticks", kind: "valueObject" }));
    const plan = planner.plan(registry, state);
    expect(plan.actions).toHaveLength(1);
    expect(plan.actions[0].action).toBe("create");
    expect(plan.actions[0].resourceId).toBe("vo.Comp.Ticks");
    expect(plan.actions[0].reason).toBe("new resource");
  });

  test("unchanged resource produces no action", () => {
    const resource = makeResource({ id: "vo.Comp.Ticks", kind: "valueObject" });
    registry.register(resource);

    const hashComputer = new HashComputer("claude-sonnet-4-6");
    const hashes = hashComputer.computeAll(registry);

    state.upsertResource({
      id: "vo.Comp.Ticks",
      kind: "valueObject",
      context: "Comp",
      declaration_hash: "x",
      effective_hash: hashes.get("vo.Comp.Ticks")!,
      declaration_json: "{}",
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });

    const plan = planner.plan(registry, state);
    expect(plan.actions).toHaveLength(0);
  });

  test("changed resource produces a modify action", () => {
    registry.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        declaration: { from: "number" },
      }),
    );

    state.upsertResource({
      id: "vo.Comp.Ticks",
      kind: "valueObject",
      context: "Comp",
      declaration_hash: "old",
      effective_hash: "old_effective",
      declaration_json: '{"from":"string"}',
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });

    const plan = planner.plan(registry, state);
    expect(plan.actions).toHaveLength(1);
    expect(plan.actions[0].action).toBe("modify");
    expect(plan.actions[0].reason).toContain("changed");
  });

  test("resource in state but not in registry produces a destroy action", () => {
    state.upsertResource({
      id: "vo.Comp.OldThing",
      kind: "valueObject",
      context: "Comp",
      declaration_hash: "x",
      effective_hash: "y",
      declaration_json: "{}",
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });

    const plan = planner.plan(registry, state);
    expect(plan.actions).toHaveLength(1);
    expect(plan.actions[0].action).toBe("destroy");
    expect(plan.actions[0].resourceId).toBe("vo.Comp.OldThing");
  });

  test("cascade: changing a port cascades to implementors", () => {
    registry.register(
      makeResource({
        id: "port.Render",
        kind: "port",
        declaration: { contract: { render: "() => NoteEvent[]" } },
      }),
    );
    registry.register(
      makeResource({
        id: "agg.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Render", kind: "implements" }],
      }),
    );

    state.upsertResource({
      id: "port.Render",
      kind: "port",
      context: null,
      declaration_hash: "old",
      effective_hash: "old_port_hash",
      declaration_json: '{"contract":{"render":"() => void"}}',
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });
    state.upsertResource({
      id: "agg.Linear",
      kind: "aggregate",
      context: null,
      declaration_hash: "old",
      effective_hash: "old_agg_hash",
      declaration_json: "{}",
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });

    const plan = planner.plan(registry, state);
    const actions = plan.actions.sort((a, b) => a.resourceId.localeCompare(b.resourceId));
    expect(actions).toHaveLength(2);
    expect(actions.find((a) => a.resourceId === "port.Render")!.action).toBe("modify");
    expect(actions.find((a) => a.resourceId === "agg.Linear")!.action).toBe("modify");
    expect(actions.find((a) => a.resourceId === "agg.Linear")!.cascadedFrom).toBe("port.Render");
  });

  test("plan display shows formatted output", () => {
    registry.register(makeResource({ id: "vo.Ticks", kind: "valueObject" }));
    const plan = planner.plan(registry, state);
    const output = plan.display();
    expect(output).toContain("+ vo.Ticks");
    expect(output).toContain("reason: new resource");
  });

  test("empty plan displays no changes", () => {
    const plan = planner.plan(registry, state);
    expect(plan.display()).toBe("No changes detected.");
  });
});
```

- [ ] **Step 3: Run tests to verify failure**

Run: `bun test tests/planner/planner.test.ts`
Expected: FAIL — module not found

- [ ] **Step 4: Implement Planner**

Write `src/planner/planner.ts`:

```ts
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { IStateDatabase } from "../state/state-database.js";
import type { IHashComputer } from "./hash-computer.js";
import { Plan, type PlannedAction } from "./plan.js";

export interface IPlanner {
  plan(registry: IResourceRegistry, state: IStateDatabase): Plan;
}

export class Planner implements IPlanner {
  constructor(private readonly hashComputer: IHashComputer) {}

  plan(registry: IResourceRegistry, state: IStateDatabase): Plan {
    const effectiveHashes = this.hashComputer.computeAll(registry);
    const actions: PlannedAction[] = [];
    const storedResources = state.getAllResources();
    const storedIds = new Set(storedResources.map((r) => r.id));
    const specIds = new Set(registry.getAll().map((r) => r.id));

    const ordered = registry.topologicalOrder();
    const cascadeReasons = new Map<string, string>();

    for (const resource of ordered) {
      const stored = state.getResource(resource.id);
      const newHash = effectiveHashes.get(resource.id)!;

      if (!stored) {
        actions.push({
          resourceId: resource.id,
          action: "create",
          reason: "new resource",
          affectedFiles: [],
        });
        continue;
      }

      if (stored.effective_hash !== newHash) {
        const cascadedFrom = cascadeReasons.get(resource.id);
        const reason = cascadedFrom
          ? `dependency changed (${cascadedFrom})`
          : "declaration changed";

        actions.push({
          resourceId: resource.id,
          action: "modify",
          reason,
          affectedFiles: state.getFilesForResource(resource.id).map((f) => f.path),
          cascadedFrom,
        });

        const dependents = registry.getDependents(resource.id);
        for (const dep of dependents) {
          if (!cascadeReasons.has(dep.id)) {
            cascadeReasons.set(dep.id, resource.id);
          }
        }
      }
    }

    for (const stored of storedResources) {
      if (!specIds.has(stored.id)) {
        actions.push({
          resourceId: stored.id,
          action: "destroy",
          reason: "removed from spec",
          affectedFiles: state.getFilesForResource(stored.id).map((f) => f.path),
        });
      }
    }

    return new Plan(actions, []);
  }
}
```

Write `src/planner/index.ts`:

```ts
export { HashComputer, type IHashComputer } from "./hash-computer.js";
export { Planner, type IPlanner } from "./planner.js";
export { Plan, type PlannedAction, type InvariantViolation } from "./plan.js";
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `bun test tests/planner/planner.test.ts`
Expected: PASS — all 6 tests pass

- [ ] **Step 6: Commit**

```bash
git add src/planner/ tests/planner/
git commit -m "feat: Planner with effective_hash diffing and cascade tracking"
```

---

## Phase 3: Generation Engine

### Task 8: LLM Response Parser

**Files:**
- Create: `src/engine/response-parser.ts`
- Create: `tests/engine/response-parser.test.ts`

- [ ] **Step 1: Write failing tests for response parsing**

Write `tests/engine/response-parser.test.ts`:

```ts
import { describe, test, expect } from "bun:test";
import { ResponseParser } from "../../src/engine/response-parser";

describe("ResponseParser", () => {
  const parser = new ResponseParser();

  test("parses a single fenced code block with path annotation", () => {
    const input = `Here is the implementation:

\`\`\`ts
// path: src/composition/domain/song.ts
export interface Song {
  id: string;
  name: string;
}
\`\`\``;

    const files = parser.parse(input);
    expect(files.size).toBe(1);
    expect(files.get("src/composition/domain/song.ts")).toContain("export interface Song");
  });

  test("parses multiple fenced code blocks", () => {
    const input = `
\`\`\`ts
// path: src/domain/song.ts
export interface Song { id: string; }
\`\`\`

\`\`\`ts
// path: src/domain/song.test.ts
import { Song } from "./song";
test("song exists", () => {});
\`\`\``;

    const files = parser.parse(input);
    expect(files.size).toBe(2);
    expect(files.has("src/domain/song.ts")).toBe(true);
    expect(files.has("src/domain/song.test.ts")).toBe(true);
  });

  test("strips the path comment from file content", () => {
    const input = `\`\`\`ts
// path: src/song.ts
export type Song = {};
\`\`\``;

    const files = parser.parse(input);
    const content = files.get("src/song.ts")!;
    expect(content).not.toContain("// path:");
    expect(content.trim()).toBe("export type Song = {};");
  });

  test("ignores code blocks without path annotations", () => {
    const input = `Here's an example:

\`\`\`ts
const x = 1;
\`\`\`

\`\`\`ts
// path: src/real.ts
export const y = 2;
\`\`\``;

    const files = parser.parse(input);
    expect(files.size).toBe(1);
    expect(files.has("src/real.ts")).toBe(true);
  });

  test("returns empty map for input with no code blocks", () => {
    const files = parser.parse("No code here.");
    expect(files.size).toBe(0);
  });
});
```

- [ ] **Step 2: Run tests to verify failure**

Run: `bun test tests/engine/response-parser.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement ResponseParser**

Write `src/engine/response-parser.ts`:

```ts
export interface IResponseParser {
  parse(response: string): Map<string, string>;
}

export class ResponseParser implements IResponseParser {
  private static readonly BLOCK_REGEX = /```[\w]*\n([\s\S]*?)```/g;
  private static readonly PATH_REGEX = /^\/\/\s*path:\s*(.+)$/m;

  parse(response: string): Map<string, string> {
    const files = new Map<string, string>();

    for (const match of response.matchAll(ResponseParser.BLOCK_REGEX)) {
      const blockContent = match[1];
      const pathMatch = blockContent.match(ResponseParser.PATH_REGEX);
      if (!pathMatch) continue;

      const filePath = pathMatch[1].trim();
      const content = blockContent.replace(ResponseParser.PATH_REGEX, "").trim() + "\n";
      files.set(filePath, content);
    }

    return files;
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `bun test tests/engine/response-parser.test.ts`
Expected: PASS — all 5 tests pass

- [ ] **Step 5: Commit**

```bash
git add src/engine/response-parser.ts tests/engine/response-parser.test.ts
git commit -m "feat: ResponseParser for extracting files from LLM output"
```

---

### Task 9: Prompt Builder

**Files:**
- Create: `src/engine/prompt-builder.ts`
- Create: `tests/engine/prompt-builder.test.ts`

- [ ] **Step 1: Write failing tests for prompt building**

Write `tests/engine/prompt-builder.test.ts`:

```ts
import { describe, test, expect, beforeEach } from "bun:test";
import { PromptBuilder } from "../../src/engine/prompt-builder";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";

describe("PromptBuilder", () => {
  let registry: ResourceRegistry;
  let builder: PromptBuilder;

  beforeEach(() => {
    registry = new ResourceRegistry();
    builder = new PromptBuilder();
  });

  test("includes resource declaration in prompt", () => {
    registry.register(
      makeResource({
        id: "agg.Comp.Song",
        kind: "aggregate",
        declaration: { state: { id: "SongId", name: "string" } },
      }),
    );
    const prompt = builder.build(registry.getById("agg.Comp.Song")!, registry);
    expect(prompt).toContain("SongId");
    expect(prompt).toContain("Declaration");
  });

  test("includes meta in prompt", () => {
    registry.register(
      makeResource({
        id: "agg.Comp.Song",
        kind: "aggregate",
        meta: { style: "functional", avoid: ["any"] },
      }),
    );
    const prompt = builder.build(registry.getById("agg.Comp.Song")!, registry);
    expect(prompt).toContain("functional");
    expect(prompt).toContain("any");
  });

  test("includes commands and events", () => {
    registry.register(
      makeResource({
        id: "agg.Comp.Song",
        kind: "aggregate",
        commands: [{ name: "RenameSong", payload: { name: "string" } }],
        events: [{ name: "SongRenamed", payload: { id: "SongId", name: "string" } }],
      }),
    );
    const prompt = builder.build(registry.getById("agg.Comp.Song")!, registry);
    expect(prompt).toContain("RenameSong");
    expect(prompt).toContain("SongRenamed");
  });

  test("includes invariants", () => {
    registry.register(
      makeResource({
        id: "agg.Comp.Song",
        kind: "aggregate",
        invariants: ["tempo is between 20 and 999"],
      }),
    );
    const prompt = builder.build(registry.getById("agg.Comp.Song")!, registry);
    expect(prompt).toContain("tempo is between 20 and 999");
  });

  test("includes contract from implemented port", () => {
    registry.register(
      makeResource({
        id: "port.Comp.Render",
        kind: "port",
        declaration: { contract: { render: "(ctx: MusicalContext) => NoteEvent[]" } },
      }),
    );
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );
    const prompt = builder.build(registry.getById("agg.Comp.Linear")!, registry);
    expect(prompt).toContain("render");
    expect(prompt).toContain("NoteEvent[]");
  });

  test("system prompt instructs structured output", () => {
    const systemPrompt = builder.systemPrompt();
    expect(systemPrompt).toContain("// path:");
    expect(systemPrompt).toContain("fenced code block");
  });
});
```

- [ ] **Step 2: Run tests to verify failure**

Run: `bun test tests/engine/prompt-builder.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement PromptBuilder**

Write `src/engine/prompt-builder.ts`:

```ts
import type { ResourceDescriptor } from "../types.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";

export interface IPromptBuilder {
  build(resource: ResourceDescriptor, registry: IResourceRegistry): string;
  systemPrompt(): string;
}

export class PromptBuilder implements IPromptBuilder {
  systemPrompt(): string {
    return [
      "You are a TypeScript code generator. You produce implementation files for declared software resources.",
      "For each file you generate, output it as a fenced code block with a path annotation on the first line:",
      "```ts",
      "// path: src/example/file.ts",
      "// ... file content ...",
      "```",
      "Generate both the implementation file and a test file for each resource.",
      "Use pure functions and immutable data structures unless the declaration specifies otherwise.",
      "All generated code must compile with strict TypeScript.",
      "Do not add any commentary outside of code blocks.",
    ].join("\n");
  }

  build(resource: ResourceDescriptor, registry: IResourceRegistry): string {
    const sections: string[] = [];

    sections.push(`## Resource: ${resource.kind} "${resource.name}" (${resource.id})`);
    sections.push(`Context: ${resource.context ?? "project-level"}`);
    sections.push(`Layer: ${resource.layer ?? "unspecified"}`);

    sections.push("\n## Declaration");
    sections.push("```json");
    sections.push(JSON.stringify(resource.declaration, null, 2));
    sections.push("```");

    if (resource.commands && resource.commands.length > 0) {
      sections.push("\n## Commands");
      for (const cmd of resource.commands) {
        sections.push(`- **${cmd.name}**: ${JSON.stringify(cmd.payload)}`);
      }
    }

    if (resource.events && resource.events.length > 0) {
      sections.push("\n## Events");
      for (const evt of resource.events) {
        sections.push(`- **${evt.name}**: ${JSON.stringify(evt.payload)}`);
      }
    }

    if (resource.invariants && resource.invariants.length > 0) {
      sections.push("\n## Invariants");
      for (const inv of resource.invariants) {
        sections.push(`- ${inv}`);
      }
    }

    if (Object.keys(resource.meta).length > 0) {
      sections.push("\n## Meta");
      sections.push("```json");
      sections.push(JSON.stringify(resource.meta, null, 2));
      sections.push("```");
    }

    const implementedPorts = resource.dependencies.filter((d) => d.kind === "implements");
    for (const dep of implementedPorts) {
      const port = registry.getById(dep.targetId);
      if (port) {
        sections.push("\n## Implements Port: " + port.name);
        sections.push("Contract:");
        sections.push("```json");
        sections.push(JSON.stringify(port.declaration.contract, null, 2));
        sections.push("```");
      }
    }

    const usedDeps = resource.dependencies.filter((d) => d.kind === "uses");
    if (usedDeps.length > 0) {
      sections.push("\n## Dependencies (uses)");
      for (const dep of usedDeps) {
        const target = registry.getById(dep.targetId);
        if (target) {
          sections.push(`- ${target.name} (${target.id}): ${JSON.stringify(target.declaration)}`);
        }
      }
    }

    return sections.join("\n");
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `bun test tests/engine/prompt-builder.test.ts`
Expected: PASS — all 6 tests pass

- [ ] **Step 5: Commit**

```bash
git add src/engine/prompt-builder.ts tests/engine/prompt-builder.test.ts
git commit -m "feat: PromptBuilder for assembling per-resource LLM prompts"
```

---

### Task 10: LLM Client

**Files:**
- Create: `src/engine/llm-client.ts`

- [ ] **Step 1: Implement LLM client interface and Anthropic wrapper**

Write `src/engine/llm-client.ts`:

```ts
import Anthropic from "@anthropic-ai/sdk";

export interface ILlmClient {
  generate(prompt: string, systemPrompt: string): Promise<string>;
  readonly modelId: string;
}

export class AnthropicLlmClient implements ILlmClient {
  private client: Anthropic;
  readonly modelId: string;

  constructor(modelId: string, apiKey?: string) {
    this.modelId = modelId;
    this.client = new Anthropic({ apiKey });
  }

  async generate(prompt: string, systemPrompt: string): Promise<string> {
    const response = await this.client.messages.create({
      model: this.modelId,
      max_tokens: 16384,
      system: systemPrompt,
      messages: [{ role: "user", content: prompt }],
    });

    const textBlocks = response.content.filter((b) => b.type === "text");
    return textBlocks.map((b) => b.text).join("\n");
  }
}
```

- [ ] **Step 2: Commit**

```bash
git add src/engine/llm-client.ts
git commit -m "feat: AnthropicLlmClient wrapping the Anthropic SDK"
```

---

### Task 11: Invariant Rules & Checker

**Files:**
- Create: `src/invariants/invariant-checker.ts`
- Create: `src/invariants/rules/aggregate-has-repository.ts`
- Create: `src/invariants/rules/context-boundaries.ts`
- Create: `src/invariants/rules/dependency-rule.ts`
- Create: `src/invariants/rules/mutations-through-services.ts`
- Create: `src/invariants/rules/domain-no-infra-imports.ts`
- Create: `src/invariants/rules/contract-compliance.ts`
- Create: `src/invariants/rules/index.ts`
- Create: `src/invariants/index.ts`
- Create: `tests/invariants/rules.test.ts`
- Create: `tests/invariants/invariant-checker.test.ts`

- [ ] **Step 1: Write the IInvariantRule interface and InvariantChecker**

Write `src/invariants/invariant-checker.ts`:

```ts
import type { ResourceDescriptor } from "../types.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";

export interface InvariantResult {
  invariant: string;
  resourceId: string | null;
  status: "ok" | "violated";
  detail: string | null;
  rationale: string | null;
}

export interface IInvariantRule {
  name: string;
  appliesTo(resource: ResourceDescriptor): boolean;
  checkStructural?(resource: ResourceDescriptor, registry: IResourceRegistry): InvariantResult;
  checkGenerated?(resource: ResourceDescriptor, fileContents: Map<string, string>, registry: IResourceRegistry): InvariantResult;
}

export interface IInvariantChecker {
  checkStructural(registry: IResourceRegistry): InvariantResult[];
  checkGenerated(resourceId: string, files: Map<string, string>, registry: IResourceRegistry): InvariantResult[];
}

export class InvariantChecker implements IInvariantChecker {
  constructor(private readonly rules: IInvariantRule[]) {}

  checkStructural(registry: IResourceRegistry): InvariantResult[] {
    const results: InvariantResult[] = [];
    for (const resource of registry.getAll()) {
      for (const rule of this.rules) {
        if (rule.checkStructural && rule.appliesTo(resource)) {
          results.push(rule.checkStructural(resource, registry));
        }
      }
    }
    return results;
  }

  checkGenerated(resourceId: string, files: Map<string, string>, registry: IResourceRegistry): InvariantResult[] {
    const resource = registry.getById(resourceId);
    if (!resource) return [];

    const results: InvariantResult[] = [];
    for (const rule of this.rules) {
      if (rule.checkGenerated && rule.appliesTo(resource)) {
        results.push(rule.checkGenerated(resource, files, registry));
      }
    }
    return results;
  }
}
```

- [ ] **Step 2: Write failing tests for invariant rules**

Write `tests/invariants/rules.test.ts`:

```ts
import { describe, test, expect, beforeEach } from "bun:test";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { AggregateHasRepository } from "../../src/invariants/rules/aggregate-has-repository";
import { ContextBoundaries } from "../../src/invariants/rules/context-boundaries";
import { DomainNoInfraImports } from "../../src/invariants/rules/domain-no-infra-imports";
import { ContractCompliance } from "../../src/invariants/rules/contract-compliance";
import { makeResource } from "../helpers";

describe("AggregateHasRepository", () => {
  const rule = new AggregateHasRepository();
  let registry: ResourceRegistry;

  beforeEach(() => {
    registry = new ResourceRegistry();
  });

  test("passes when aggregate root has a repository", () => {
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", declaration: { root: true } }));
    registry.register(
      makeResource({
        id: "repo.Comp.SongRepo",
        kind: "repository",
        dependencies: [{ targetId: "agg.Comp.Song", kind: "of" }],
      }),
    );

    const agg = registry.getById("agg.Comp.Song")!;
    const result = rule.checkStructural!(agg, registry);
    expect(result.status).toBe("ok");
  });

  test("fails when aggregate root has no repository", () => {
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", declaration: { root: true } }));

    const agg = registry.getById("agg.Comp.Song")!;
    const result = rule.checkStructural!(agg, registry);
    expect(result.status).toBe("violated");
  });

  test("does not apply to non-root aggregates", () => {
    const resource = makeResource({ id: "agg.Comp.Song", kind: "aggregate", declaration: { root: false } });
    expect(rule.appliesTo(resource)).toBe(false);
  });
});

describe("ContextBoundaries", () => {
  const rule = new ContextBoundaries();
  let registry: ResourceRegistry;

  beforeEach(() => {
    registry = new ResourceRegistry();
  });

  test("passes when dependency is within the same context", () => {
    registry.register(makeResource({ id: "port.Comp.Render", kind: "port", context: "Comp" }));
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        kind: "aggregate",
        context: "Comp",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );

    const agg = registry.getById("agg.Comp.Linear")!;
    const result = rule.checkStructural!(agg, registry);
    expect(result.status).toBe("ok");
  });

  test("passes when cross-context dependency has a declared relationship", () => {
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", context: "Comp" }));
    registry.register(
      makeResource({
        id: "svc.Play.Seq",
        kind: "applicationService",
        context: "Playback",
        dependencies: [{ targetId: "agg.Comp.Song", kind: "uses" }],
      }),
    );
    registry.setContextMap([
      { from: "Playback", to: "Comp", kind: "customer-supplier", direction: "downstream" },
    ]);

    const svc = registry.getById("svc.Play.Seq")!;
    const result = rule.checkStructural!(svc, registry);
    expect(result.status).toBe("ok");
  });

  test("fails when cross-context dependency has no declared relationship", () => {
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", context: "Comp" }));
    registry.register(
      makeResource({
        id: "svc.Play.Seq",
        kind: "applicationService",
        context: "Playback",
        dependencies: [{ targetId: "agg.Comp.Song", kind: "uses" }],
      }),
    );

    const svc = registry.getById("svc.Play.Seq")!;
    const result = rule.checkStructural!(svc, registry);
    expect(result.status).toBe("violated");
  });
});

describe("DomainNoInfraImports", () => {
  const rule = new DomainNoInfraImports();

  test("passes when domain file has no infra imports", () => {
    const resource = makeResource({ id: "agg.Comp.Song", layer: "domain" });
    const files = new Map([
      ["src/comp/domain/song.ts", 'import { Ticks } from "../kernel/ticks";\nexport interface Song {}'],
    ]);

    const registry = new ResourceRegistry();
    registry.register(resource);
    const result = rule.checkGenerated!(resource, files, registry);
    expect(result.status).toBe("ok");
  });

  test("fails when domain file imports from infrastructure", () => {
    const resource = makeResource({ id: "agg.Comp.Song", layer: "domain" });
    const files = new Map([
      ["src/comp/domain/song.ts", 'import { db } from "../infrastructure/database";\nexport interface Song {}'],
    ]);

    const registry = new ResourceRegistry();
    registry.register(resource);
    const result = rule.checkGenerated!(resource, files, registry);
    expect(result.status).toBe("violated");
  });
});

describe("ContractCompliance", () => {
  const rule = new ContractCompliance();
  let registry: ResourceRegistry;

  beforeEach(() => {
    registry = new ResourceRegistry();
  });

  test("passes when generated code contains contract methods", () => {
    registry.register(
      makeResource({
        id: "port.Comp.Render",
        kind: "port",
        declaration: { contract: { render: "(ctx: MusicalContext) => NoteEvent[]" } },
      }),
    );
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );

    const files = new Map([
      ["src/linear.ts", "export function render(ctx: MusicalContext): NoteEvent[] { return []; }"],
    ]);
    const resource = registry.getById("agg.Comp.Linear")!;
    const result = rule.checkGenerated!(resource, files, registry);
    expect(result.status).toBe("ok");
  });

  test("fails when generated code is missing contract methods", () => {
    registry.register(
      makeResource({
        id: "port.Comp.Render",
        kind: "port",
        declaration: { contract: { render: "(ctx: MusicalContext) => NoteEvent[]" } },
      }),
    );
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );

    const files = new Map([["src/linear.ts", "export function doSomething() {}"]]);
    const resource = registry.getById("agg.Comp.Linear")!;
    const result = rule.checkGenerated!(resource, files, registry);
    expect(result.status).toBe("violated");
  });
});
```

- [ ] **Step 3: Run tests to verify failure**

Run: `bun test tests/invariants/rules.test.ts`
Expected: FAIL — modules not found

- [ ] **Step 4: Implement invariant rules**

Write `src/invariants/rules/aggregate-has-repository.ts`:

```ts
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
```

Write `src/invariants/rules/context-boundaries.ts`:

```ts
import type { ResourceDescriptor } from "../../types.js";
import type { IResourceRegistry } from "../../registry/resource-registry.js";
import type { IInvariantRule, InvariantResult } from "../invariant-checker.js";

export class ContextBoundaries implements IInvariantRule {
  name = "context boundaries respected";

  appliesTo(resource: ResourceDescriptor): boolean {
    return resource.context !== null && resource.dependencies.length > 0;
  }

  checkStructural(resource: ResourceDescriptor, registry: IResourceRegistry): InvariantResult {
    const contextMap = registry.getContextMap();

    for (const dep of resource.dependencies) {
      const target = registry.getById(dep.targetId);
      if (!target || !target.context) continue;
      if (target.context === resource.context) continue;

      const hasRelationship = contextMap.some(
        (r) =>
          (r.from === resource.context && r.to === target.context) ||
          (r.to === resource.context && r.from === target.context),
      );

      if (!hasRelationship) {
        return {
          invariant: this.name,
          resourceId: resource.id,
          status: "violated",
          detail: `${resource.id} depends on ${target.id} across context boundary (${resource.context} -> ${target.context}) without a declared relationship`,
          rationale: "context boundaries are enforced; integration is explicit",
        };
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
```

Write `src/invariants/rules/dependency-rule.ts`:

```ts
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
```

Write `src/invariants/rules/mutations-through-services.ts`:

```ts
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
```

Write `src/invariants/rules/domain-no-infra-imports.ts`:

```ts
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
```

Write `src/invariants/rules/contract-compliance.ts`:

```ts
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
```

Write `src/invariants/rules/index.ts`:

```ts
export { AggregateHasRepository } from "./aggregate-has-repository.js";
export { ContextBoundaries } from "./context-boundaries.js";
export { DependencyRule } from "./dependency-rule.js";
export { MutationsThroughServices } from "./mutations-through-services.js";
export { DomainNoInfraImports } from "./domain-no-infra-imports.js";
export { ContractCompliance } from "./contract-compliance.js";

import { AggregateHasRepository } from "./aggregate-has-repository.js";
import { ContextBoundaries } from "./context-boundaries.js";
import { DependencyRule } from "./dependency-rule.js";
import { MutationsThroughServices } from "./mutations-through-services.js";
import { DomainNoInfraImports } from "./domain-no-infra-imports.js";
import { ContractCompliance } from "./contract-compliance.js";
import type { IInvariantRule } from "../invariant-checker.js";

export function allRules(): IInvariantRule[] {
  return [
    new AggregateHasRepository(),
    new ContextBoundaries(),
    new DependencyRule(),
    new MutationsThroughServices(),
    new DomainNoInfraImports(),
    new ContractCompliance(),
  ];
}
```

Write `src/invariants/index.ts`:

```ts
export { InvariantChecker, type IInvariantChecker, type IInvariantRule, type InvariantResult } from "./invariant-checker.js";
export { allRules } from "./rules/index.js";
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `bun test tests/invariants/rules.test.ts`
Expected: PASS — all tests pass

- [ ] **Step 6: Write and run InvariantChecker tests**

Write `tests/invariants/invariant-checker.test.ts`:

```ts
import { describe, test, expect } from "bun:test";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { AggregateHasRepository } from "../../src/invariants/rules/aggregate-has-repository";
import { DomainNoInfraImports } from "../../src/invariants/rules/domain-no-infra-imports";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";

describe("InvariantChecker", () => {
  test("checkStructural runs all applicable rules", () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", declaration: { root: true } }));

    const checker = new InvariantChecker([new AggregateHasRepository()]);
    const results = checker.checkStructural(registry);
    expect(results).toHaveLength(1);
    expect(results[0].status).toBe("violated");
  });

  test("checkGenerated runs code-level rules", () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", layer: "domain" }));

    const checker = new InvariantChecker([new DomainNoInfraImports()]);
    const files = new Map([["src/song.ts", 'import { x } from "../infrastructure/db";\n']]);
    const results = checker.checkGenerated("agg.Comp.Song", files, registry);
    expect(results).toHaveLength(1);
    expect(results[0].status).toBe("violated");
  });
});
```

Run: `bun test tests/invariants/invariant-checker.test.ts`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add src/invariants/ tests/invariants/
git commit -m "feat: invariant rules and InvariantChecker"
```

---

### Task 12: Constraint Loop

**Files:**
- Create: `src/engine/constraint-loop.ts`
- Create: `tests/engine/constraint-loop.test.ts`

- [ ] **Step 1: Write failing tests for constraint loop**

Write `tests/engine/constraint-loop.test.ts`:

```ts
import { describe, test, expect } from "bun:test";
import { ConstraintLoop } from "../../src/engine/constraint-loop";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { makeResource } from "../helpers";
import type { ILlmClient } from "../../src/engine/llm-client";

function mockLlmClient(responses: string[]): ILlmClient {
  let callIndex = 0;
  return {
    modelId: "test-model",
    async generate(_prompt: string, _system: string): Promise<string> {
      return responses[callIndex++] ?? "";
    },
  };
}

describe("ConstraintLoop", () => {
  test("returns files on first success with no invariant violations", async () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "vo.Ticks", kind: "valueObject" }));

    const checker = new InvariantChecker([]);
    const llm = mockLlmClient([
      '```ts\n// path: src/ticks.ts\nexport type Ticks = number;\n```',
    ]);

    const loop = new ConstraintLoop(checker, {
      skipTypeCheck: true,
      skipTests: true,
    });

    const result = await loop.run({
      resource: registry.getById("vo.Ticks")!,
      registry,
      llmClient: llm,
      prompt: "generate Ticks",
      systemPrompt: "you are a code generator",
      maxRetries: 3,
    });

    expect(result.success).toBe(true);
    expect(result.files!.size).toBe(1);
    expect(result.files!.get("src/ticks.ts")).toContain("Ticks");
  });

  test("retries when invariant check fails, succeeds on second attempt", async () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "agg.Song", kind: "aggregate", layer: "domain" }));

    const { DomainNoInfraImports } = require("../../src/invariants/rules/domain-no-infra-imports");
    const checker = new InvariantChecker([new DomainNoInfraImports()]);

    const llm = mockLlmClient([
      '```ts\n// path: src/song.ts\nimport { db } from "../infrastructure/db";\nexport interface Song {}\n```',
      '```ts\n// path: src/song.ts\nexport interface Song { id: string; }\n```',
    ]);

    const loop = new ConstraintLoop(checker, {
      skipTypeCheck: true,
      skipTests: true,
    });

    const result = await loop.run({
      resource: registry.getById("agg.Song")!,
      registry,
      llmClient: llm,
      prompt: "generate Song",
      systemPrompt: "you are a code generator",
      maxRetries: 3,
    });

    expect(result.success).toBe(true);
    expect(result.retries).toBe(1);
  });

  test("fails after exhausting all retries", async () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "agg.Song", kind: "aggregate", layer: "domain" }));

    const { DomainNoInfraImports } = require("../../src/invariants/rules/domain-no-infra-imports");
    const checker = new InvariantChecker([new DomainNoInfraImports()]);

    const badResponse = '```ts\n// path: src/song.ts\nimport { db } from "../infrastructure/db";\nexport interface Song {}\n```';
    const llm = mockLlmClient([badResponse, badResponse, badResponse, badResponse]);

    const loop = new ConstraintLoop(checker, {
      skipTypeCheck: true,
      skipTests: true,
    });

    const result = await loop.run({
      resource: registry.getById("agg.Song")!,
      registry,
      llmClient: llm,
      prompt: "generate Song",
      systemPrompt: "you are a code generator",
      maxRetries: 3,
    });

    expect(result.success).toBe(false);
    expect(result.lastError).toContain("infrastructure");
  });
});
```

- [ ] **Step 2: Run tests to verify failure**

Run: `bun test tests/engine/constraint-loop.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement ConstraintLoop**

Write `src/engine/constraint-loop.ts`:

```ts
import type { ResourceDescriptor } from "../types.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { IInvariantChecker } from "../invariants/invariant-checker.js";
import type { ILlmClient } from "./llm-client.js";
import { ResponseParser } from "./response-parser.js";

export interface ConstraintLoopOptions {
  skipTypeCheck?: boolean;
  skipTests?: boolean;
  projectRoot?: string;
}

export interface ConstraintLoopInput {
  resource: ResourceDescriptor;
  registry: IResourceRegistry;
  llmClient: ILlmClient;
  prompt: string;
  systemPrompt: string;
  maxRetries: number;
}

export interface ConstraintLoopResult {
  success: boolean;
  files: Map<string, string> | null;
  retries: number;
  lastError: string | null;
}

export interface IConstraintLoop {
  run(input: ConstraintLoopInput): Promise<ConstraintLoopResult>;
}

export class ConstraintLoop implements IConstraintLoop {
  private readonly parser = new ResponseParser();

  constructor(
    private readonly invariantChecker: IInvariantChecker,
    private readonly options: ConstraintLoopOptions = {},
  ) {}

  async run(input: ConstraintLoopInput): Promise<ConstraintLoopResult> {
    let lastError: string | null = null;
    let currentPrompt = input.prompt;

    for (let attempt = 0; attempt <= input.maxRetries; attempt++) {
      const response = await input.llmClient.generate(currentPrompt, input.systemPrompt);
      const files = this.parser.parse(response);

      if (files.size === 0) {
        lastError = "LLM returned no parseable code blocks";
        currentPrompt = this.appendFeedback(input.prompt, lastError);
        continue;
      }

      if (!this.options.skipTypeCheck) {
        const typeError = await this.typeCheck(files);
        if (typeError) {
          lastError = typeError;
          currentPrompt = this.appendFeedback(input.prompt, typeError);
          continue;
        }
      }

      const invariantResults = this.invariantChecker.checkGenerated(
        input.resource.id,
        files,
        input.registry,
      );
      const violations = invariantResults.filter((r) => r.status === "violated");
      if (violations.length > 0) {
        lastError = violations.map((v) => `${v.invariant}: ${v.detail}`).join("\n");
        currentPrompt = this.appendFeedback(input.prompt, lastError);
        continue;
      }

      if (!this.options.skipTests) {
        const testError = await this.runTests(files);
        if (testError) {
          lastError = testError;
          currentPrompt = this.appendFeedback(input.prompt, testError);
          continue;
        }
      }

      return { success: true, files, retries: attempt, lastError: null };
    }

    return { success: false, files: null, retries: input.maxRetries, lastError };
  }

  private appendFeedback(originalPrompt: string, error: string): string {
    return `${originalPrompt}\n\n## Previous attempt failed\n\nYour previous output had the following error. Fix it:\n\n${error}`;
  }

  private async typeCheck(_files: Map<string, string>): Promise<string | null> {
    if (!this.options.projectRoot) return null;

    try {
      const proc = Bun.spawn(["tsc", "--noEmit"], {
        cwd: this.options.projectRoot,
        stdout: "pipe",
        stderr: "pipe",
      });
      const exitCode = await proc.exited;
      if (exitCode !== 0) {
        const stderr = await new Response(proc.stderr).text();
        return `TypeScript compilation failed:\n${stderr}`;
      }
      return null;
    } catch (e) {
      return `Type check error: ${e}`;
    }
  }

  private async runTests(_files: Map<string, string>): Promise<string | null> {
    if (!this.options.projectRoot) return null;

    try {
      const proc = Bun.spawn(["bun", "test"], {
        cwd: this.options.projectRoot,
        stdout: "pipe",
        stderr: "pipe",
      });
      const exitCode = await proc.exited;
      if (exitCode !== 0) {
        const stderr = await new Response(proc.stderr).text();
        return `Tests failed:\n${stderr}`;
      }
      return null;
    } catch (e) {
      return `Test execution error: ${e}`;
    }
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `bun test tests/engine/constraint-loop.test.ts`
Expected: PASS — all 3 tests pass

- [ ] **Step 5: Commit**

```bash
git add src/engine/constraint-loop.ts tests/engine/constraint-loop.test.ts
git commit -m "feat: ConstraintLoop with retry, invariant checking, and validation pipeline"
```

---

### Task 13: Apply Engine

**Files:**
- Create: `src/engine/apply-engine.ts`
- Create: `src/engine/index.ts`
- Create: `tests/engine/apply-engine.test.ts`

- [ ] **Step 1: Write failing tests for ApplyEngine**

Write `tests/engine/apply-engine.test.ts`:

```ts
import { describe, test, expect, beforeEach } from "bun:test";
import { ApplyEngine } from "../../src/engine/apply-engine";
import { Planner } from "../../src/planner/planner";
import { HashComputer } from "../../src/planner/hash-computer";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { StateDatabase } from "../../src/state/state-database";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { PromptBuilder } from "../../src/engine/prompt-builder";
import { ConstraintLoop } from "../../src/engine/constraint-loop";
import { makeResource } from "../helpers";
import type { ILlmClient } from "../../src/engine/llm-client";
import { mkdtemp, rm } from "fs/promises";
import { join } from "path";
import { tmpdir } from "os";

function mockLlmClient(response: string): ILlmClient {
  return {
    modelId: "test-model",
    async generate(): Promise<string> {
      return response;
    },
  };
}

describe("ApplyEngine", () => {
  let registry: ResourceRegistry;
  let state: StateDatabase;
  let tempDir: string;

  beforeEach(async () => {
    registry = new ResourceRegistry();
    state = new StateDatabase(":memory:");
    tempDir = await mkdtemp(join(tmpdir(), "crest-test-"));
  });

  test("creates new resources and writes files to disk", async () => {
    registry.register(
      makeResource({ id: "vo.Comp.Ticks", kind: "valueObject", declaration: { from: "number" } }),
    );

    const llm = mockLlmClient(
      '```ts\n// path: src/ticks.ts\nexport type Ticks = number;\n```',
    );

    const hashComputer = new HashComputer("test-model");
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder();
    const checker = new InvariantChecker([]);
    const constraintLoop = new ConstraintLoop(checker, { skipTypeCheck: true, skipTests: true });

    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer);
    const result = await engine.apply(registry, state, llm, { outputDir: tempDir });

    expect(result.status).toBe("ok");
    expect(result.created).toBe(1);

    const fileContent = await Bun.file(join(tempDir, "src/ticks.ts")).text();
    expect(fileContent).toContain("Ticks");

    const storedResource = state.getResource("vo.Comp.Ticks");
    expect(storedResource).not.toBeNull();
  });

  test("skips resources with unchanged hashes", async () => {
    registry.register(
      makeResource({ id: "vo.Comp.Ticks", kind: "valueObject" }),
    );

    const hashComputer = new HashComputer("test-model");
    const hashes = hashComputer.computeAll(registry);

    state.upsertResource({
      id: "vo.Comp.Ticks",
      kind: "valueObject",
      context: "Comp",
      declaration_hash: "x",
      effective_hash: hashes.get("vo.Comp.Ticks")!,
      declaration_json: "{}",
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });

    const llm = mockLlmClient("");
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder();
    const checker = new InvariantChecker([]);
    const constraintLoop = new ConstraintLoop(checker, { skipTypeCheck: true, skipTests: true });

    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer);
    const result = await engine.apply(registry, state, llm, { outputDir: tempDir });

    expect(result.status).toBe("ok");
    expect(result.created).toBe(0);
  });

  test("records generation in state for audit trail", async () => {
    registry.register(
      makeResource({ id: "vo.Comp.Ticks", kind: "valueObject" }),
    );

    const llm = mockLlmClient(
      '```ts\n// path: src/ticks.ts\nexport type Ticks = number;\n```',
    );

    const hashComputer = new HashComputer("test-model");
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder();
    const checker = new InvariantChecker([]);
    const constraintLoop = new ConstraintLoop(checker, { skipTypeCheck: true, skipTests: true });

    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer);
    await engine.apply(registry, state, llm, { outputDir: tempDir });

    const gens = state.getGenerationsForResource("vo.Comp.Ticks");
    expect(gens).toHaveLength(1);
    expect(gens[0].outcome).toBe("accepted");
  });
});
```

- [ ] **Step 2: Run tests to verify failure**

Run: `bun test tests/engine/apply-engine.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement ApplyEngine**

Write `src/engine/apply-engine.ts`:

```ts
import { createHash } from "crypto";
import { mkdir } from "fs/promises";
import { dirname, join } from "path";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { IStateDatabase } from "../state/state-database.js";
import type { IPlanner } from "../planner/planner.js";
import type { IHashComputer } from "../planner/hash-computer.js";
import type { IPromptBuilder } from "./prompt-builder.js";
import type { IConstraintLoop } from "./constraint-loop.js";
import type { ILlmClient } from "./llm-client.js";

export interface ApplyOptions {
  target?: string;
  force?: boolean;
  maxRetries?: number;
  outputDir?: string;
}

export interface ApplyResult {
  status: "ok" | "failed";
  created: number;
  modified: number;
  destroyed: number;
  failed: number;
  errors: string[];
}

export interface IApplyEngine {
  apply(
    registry: IResourceRegistry,
    state: IStateDatabase,
    llmClient: ILlmClient,
    options?: ApplyOptions,
  ): Promise<ApplyResult>;
}

export class ApplyEngine implements IApplyEngine {
  constructor(
    private readonly planner: IPlanner,
    private readonly promptBuilder: IPromptBuilder,
    private readonly constraintLoop: IConstraintLoop,
    private readonly hashComputer: IHashComputer,
  ) {}

  async apply(
    registry: IResourceRegistry,
    state: IStateDatabase,
    llmClient: ILlmClient,
    options: ApplyOptions = {},
  ): Promise<ApplyResult> {
    const maxRetries = options.maxRetries ?? 3;
    const outputDir = options.outputDir ?? ".";

    const plan = this.planner.plan(registry, state);
    const effectiveHashes = this.hashComputer.computeAll(registry);

    let actions = plan.actions;
    if (options.target) {
      actions = actions.filter(
        (a) => a.resourceId === options.target || a.cascadedFrom === options.target,
      );
    }

    const applyRecord = state.beginApply(
      createHash("sha256")
        .update(JSON.stringify(registry.getAll().map((r) => r.id)))
        .digest("hex"),
    );

    const result: ApplyResult = {
      status: "ok",
      created: 0,
      modified: 0,
      destroyed: 0,
      failed: 0,
      errors: [],
    };

    for (const action of actions) {
      if (action.action === "destroy") {
        const files = state.getFilesForResource(action.resourceId);
        for (const file of files) {
          state.deleteGeneratedFile(file.path);
        }
        state.deleteResource(action.resourceId);
        state.recordAction(applyRecord.id, action.resourceId, "destroy", "success");
        result.destroyed++;
        continue;
      }

      const resource = registry.getById(action.resourceId);
      if (!resource) continue;

      const prompt = this.promptBuilder.build(resource, registry);
      const systemPrompt = this.promptBuilder.systemPrompt();

      const loopResult = await this.constraintLoop.run({
        resource,
        registry,
        llmClient,
        prompt,
        systemPrompt,
        maxRetries,
      });

      if (!loopResult.success) {
        result.failed++;
        result.errors.push(`${action.resourceId}: ${loopResult.lastError}`);
        state.recordAction(applyRecord.id, action.resourceId, action.action, "failed");
        state.recordGeneration({
          apply_id: applyRecord.id,
          resource_id: action.resourceId,
          model: llmClient.modelId,
          prompt_hash: createHash("sha256").update(prompt).digest("hex"),
          prompt_text: prompt,
          output_text: "",
          retries: loopResult.retries,
          outcome: "rejected",
          rejection_reason: loopResult.lastError,
          created_at: new Date().toISOString(),
        });
        continue;
      }

      for (const [filePath, content] of loopResult.files!) {
        const fullPath = join(outputDir, filePath);
        await mkdir(dirname(fullPath), { recursive: true });

        const contentHash = createHash("sha256").update(content).digest("hex");
        let existingHash: string | null = null;
        try {
          const existing = await Bun.file(fullPath).text();
          existingHash = createHash("sha256").update(existing).digest("hex");
        } catch {}

        if (existingHash !== contentHash) {
          await Bun.write(fullPath, content);
        }

        state.upsertGeneratedFile({
          path: filePath,
          resource_id: action.resourceId,
          content_hash: contentHash,
          generator: "llm",
          model: llmClient.modelId,
          prompt_hash: createHash("sha256").update(prompt).digest("hex"),
          generated_at: new Date().toISOString(),
        });
      }

      state.upsertResource({
        id: resource.id,
        kind: resource.kind,
        context: resource.context,
        declaration_hash: createHash("sha256")
          .update(JSON.stringify(resource.declaration))
          .digest("hex"),
        effective_hash: effectiveHashes.get(resource.id)!,
        declaration_json: JSON.stringify(resource.declaration),
        layer: resource.layer,
        settled_at: new Date().toISOString(),
        last_apply_id: applyRecord.id,
      });

      state.recordAction(applyRecord.id, action.resourceId, action.action, "success");
      state.recordGeneration({
        apply_id: applyRecord.id,
        resource_id: action.resourceId,
        model: llmClient.modelId,
        prompt_hash: createHash("sha256").update(prompt).digest("hex"),
        prompt_text: prompt,
        output_text: [...loopResult.files!.entries()]
          .map(([p, c]) => `// path: ${p}\n${c}`)
          .join("\n---\n"),
        retries: loopResult.retries,
        outcome: "accepted",
        rejection_reason: null,
        created_at: new Date().toISOString(),
      });

      if (action.action === "create") result.created++;
      else result.modified++;
    }

    state.finishApply(applyRecord.id, result.failed > 0 ? "failed" : "ok");
    if (result.failed > 0) result.status = "failed";

    return result;
  }
}
```

Write `src/engine/index.ts`:

```ts
export { ResponseParser, type IResponseParser } from "./response-parser.js";
export { PromptBuilder, type IPromptBuilder } from "./prompt-builder.js";
export { AnthropicLlmClient, type ILlmClient } from "./llm-client.js";
export { ConstraintLoop, type IConstraintLoop, type ConstraintLoopResult } from "./constraint-loop.js";
export { ApplyEngine, type IApplyEngine, type ApplyOptions, type ApplyResult } from "./apply-engine.js";
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `bun test tests/engine/apply-engine.test.ts`
Expected: PASS — all 3 tests pass

- [ ] **Step 5: Commit**

```bash
git add src/engine/ tests/engine/apply-engine.test.ts
git commit -m "feat: ApplyEngine orchestrating plan execution with LLM generation"
```

---

## Phase 4: CLI

### Task 14: CLI Entry Point & Core Commands

**Files:**
- Create: `src/cli/main.ts`
- Create: `src/cli/formatter.ts`
- Create: `src/cli/commands/init.ts`
- Create: `src/cli/commands/plan.ts`
- Create: `src/cli/commands/apply.ts`
- Create: `src/cli/commands/index.ts`
- Create: `src/cli/index.ts`
- Create: `tests/cli/commands.test.ts`

- [ ] **Step 1: Write the formatter**

Write `src/cli/formatter.ts`:

```ts
const RESET = "\x1b[0m";
const GREEN = "\x1b[32m";
const YELLOW = "\x1b[33m";
const RED = "\x1b[31m";
const CYAN = "\x1b[36m";
const DIM = "\x1b[2m";

export class Formatter {
  static create(text: string): string {
    return `${GREEN}+ ${text}${RESET}`;
  }

  static modify(text: string): string {
    return `${YELLOW}~ ${text}${RESET}`;
  }

  static destroy(text: string): string {
    return `${RED}- ${text}${RESET}`;
  }

  static refresh(text: string): string {
    return `${CYAN}? ${text}${RESET}`;
  }

  static dim(text: string): string {
    return `${DIM}${text}${RESET}`;
  }

  static error(text: string): string {
    return `${RED}Error: ${text}${RESET}`;
  }

  static success(text: string): string {
    return `${GREEN}${text}${RESET}`;
  }
}
```

- [ ] **Step 2: Write the init command**

Write `src/cli/commands/init.ts`:

```ts
import { existsSync } from "fs";
import { join } from "path";
import { Formatter } from "../formatter.js";

export async function initCommand(projectDir: string): Promise<number> {
  const specPath = join(projectDir, "crest-spec.ts");
  const dbPath = join(projectDir, "crest-spec.db");

  if (existsSync(specPath)) {
    console.log(Formatter.error(`${specPath} already exists`));
    return 1;
  }

  const scaffold = `import { project, command, event, layer } from "crest-spec";

const app = project("my-project", {
  layers: ["domain", "application", "infrastructure"],
  rules: [
    layer("domain").dependsOn([]),
    layer("application").dependsOn(["domain"]),
    layer("infrastructure").dependsOn(["application", "domain"]),
  ],
});

// const myContext = app.context("MyContext", {
//   purpose: "describe your bounded context here",
// });

export default app;
`;

  await Bun.write(specPath, scaffold);
  console.log(Formatter.success(`Created ${specPath}`));

  const { StateDatabase } = await import("../../state/state-database.js");
  new StateDatabase(dbPath);
  console.log(Formatter.success(`Created ${dbPath}`));

  return 0;
}
```

- [ ] **Step 3: Write the plan command**

Write `src/cli/commands/plan.ts`:

```ts
import { join } from "path";
import { Planner } from "../../planner/planner.js";
import { HashComputer } from "../../planner/hash-computer.js";
import { StateDatabase } from "../../state/state-database.js";
import { InvariantChecker } from "../../invariants/invariant-checker.js";
import { allRules } from "../../invariants/rules/index.js";
import { getActiveProject } from "../../dsl/singleton.js";

export async function planCommand(
  projectDir: string,
  specFile: string,
  modelId: string,
): Promise<number> {
  await import(join(projectDir, specFile));
  const project = getActiveProject();
  if (!project) {
    console.error("No project found. Does the spec file call project()?");
    return 1;
  }

  const registry = project.getRegistry();
  const state = new StateDatabase(join(projectDir, "crest-spec.db"));
  const hashComputer = new HashComputer(modelId);
  const planner = new Planner(hashComputer);

  const checker = new InvariantChecker(allRules());
  const structuralViolations = checker.checkStructural(registry);
  const violations = structuralViolations.filter((r) => r.status === "violated");

  const plan = planner.plan(registry, state);

  console.log(plan.display());

  if (violations.length > 0) {
    console.log("\nInvariant violations:");
    for (const v of violations) {
      console.log(`  ! ${v.invariant}`);
      if (v.resourceId) console.log(`    resource: ${v.resourceId}`);
      if (v.detail) console.log(`    detail: ${v.detail}`);
      if (v.rationale) console.log(`    rationale: ${v.rationale}`);
    }
  }

  return violations.length > 0 ? 1 : 0;
}
```

- [ ] **Step 4: Write the apply command**

Write `src/cli/commands/apply.ts`:

```ts
import { join } from "path";
import { Planner } from "../../planner/planner.js";
import { HashComputer } from "../../planner/hash-computer.js";
import { StateDatabase } from "../../state/state-database.js";
import { InvariantChecker } from "../../invariants/invariant-checker.js";
import { allRules } from "../../invariants/rules/index.js";
import { PromptBuilder } from "../../engine/prompt-builder.js";
import { ConstraintLoop } from "../../engine/constraint-loop.js";
import { ApplyEngine } from "../../engine/apply-engine.js";
import { AnthropicLlmClient } from "../../engine/llm-client.js";
import { getActiveProject } from "../../dsl/singleton.js";
import { Formatter } from "../formatter.js";

export async function applyCommand(
  projectDir: string,
  specFile: string,
  options: {
    modelId: string;
    target?: string;
    force?: boolean;
    maxRetries?: number;
  },
): Promise<number> {
  await import(join(projectDir, specFile));
  const project = getActiveProject();
  if (!project) {
    console.error("No project found. Does the spec file call project()?");
    return 1;
  }

  const registry = project.getRegistry();
  const state = new StateDatabase(join(projectDir, "crest-spec.db"));

  if (!state.acquireLock(`pid:${process.pid}`)) {
    const lock = state.getLock();
    console.error(Formatter.error(`Apply is locked by ${lock?.holder} since ${lock?.acquired_at}`));
    return 1;
  }

  try {
    const hashComputer = new HashComputer(options.modelId);
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder();
    const checker = new InvariantChecker(allRules());
    const constraintLoop = new ConstraintLoop(checker, {
      projectRoot: projectDir,
    });

    const llmClient = new AnthropicLlmClient(options.modelId);
    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer);

    const result = await engine.apply(registry, state, llmClient, {
      target: options.target,
      force: options.force,
      maxRetries: options.maxRetries,
      outputDir: projectDir,
    });

    console.log(`\nApply complete:`);
    console.log(`  Created:   ${result.created}`);
    console.log(`  Modified:  ${result.modified}`);
    console.log(`  Destroyed: ${result.destroyed}`);
    if (result.failed > 0) {
      console.log(`  ${Formatter.error(`Failed: ${result.failed}`)}`);
      for (const err of result.errors) {
        console.log(`    ${err}`);
      }
    }

    return result.status === "ok" ? 0 : 1;
  } finally {
    state.releaseLock();
  }
}
```

- [ ] **Step 5: Write the CLI entry point**

Write `src/cli/commands/index.ts`:

```ts
export { initCommand } from "./init.js";
export { planCommand } from "./plan.js";
export { applyCommand } from "./apply.js";
```

Write `src/cli/main.ts`:

```ts
#!/usr/bin/env bun
import { resolve } from "path";
import { initCommand } from "./commands/init.js";
import { planCommand } from "./commands/plan.js";
import { applyCommand } from "./commands/apply.js";
import { Formatter } from "./formatter.js";

const DEFAULT_SPEC = "crest-spec.ts";
const DEFAULT_MODEL = "claude-sonnet-4-6";

async function main(): Promise<number> {
  const args = process.argv.slice(2);
  const command = args[0];
  const projectDir = resolve(".");

  function getFlag(name: string): string | undefined {
    const idx = args.indexOf(`-${name}`);
    if (idx === -1) return undefined;
    return args[idx + 1];
  }

  function hasFlag(name: string): boolean {
    return args.includes(`--${name}`);
  }

  const specFile = getFlag("spec") ?? DEFAULT_SPEC;
  const modelId = getFlag("model") ?? DEFAULT_MODEL;

  switch (command) {
    case "init":
      return initCommand(projectDir);

    case "plan":
      return planCommand(projectDir, specFile, modelId);

    case "apply": {
      const target = getFlag("target");
      const force = hasFlag("force");
      const maxRetries = getFlag("retries") ? parseInt(getFlag("retries")!) : undefined;
      return applyCommand(projectDir, specFile, { modelId, target, force, maxRetries });
    }

    case "log": {
      const { StateDatabase } = await import("../state/state-database.js");
      const state = new StateDatabase(resolve(projectDir, "crest-spec.db"));
      const applies = state.getApplies(parseInt(getFlag("limit") ?? "20"));
      for (const a of applies) {
        console.log(`#${a.id}  ${a.status.padEnd(8)} ${a.started_at}  spec:${a.spec_hash.slice(0, 8)}`);
      }
      return 0;
    }

    case "history": {
      const resourceId = args[1];
      if (!resourceId) {
        console.error("Usage: crest-spec history <resource-id>");
        return 1;
      }
      const { StateDatabase } = await import("../state/state-database.js");
      const state = new StateDatabase(resolve(projectDir, "crest-spec.db"));
      const gens = state.getGenerationsForResource(resourceId);
      for (const g of gens) {
        console.log(`  apply #${g.apply_id}  ${g.outcome}  model:${g.model}  retries:${g.retries}  ${g.created_at}`);
      }
      return 0;
    }

    case "state": {
      const subcommand = args[1];
      const { StateDatabase } = await import("../state/state-database.js");
      const state = new StateDatabase(resolve(projectDir, "crest-spec.db"));
      if (subcommand === "list") {
        const resources = state.getAllResources();
        for (const r of resources) {
          console.log(`${r.kind.padEnd(20)} ${r.id}`);
        }
      } else if (subcommand === "rm") {
        const id = args[2];
        if (!id) { console.error("Usage: crest-spec state rm <id>"); return 1; }
        state.deleteResource(id);
        console.log(`Removed ${id} from state`);
      } else {
        console.error("Usage: crest-spec state [list|rm]");
        return 1;
      }
      return 0;
    }

    case "validate": {
      await import(resolve(projectDir, specFile));
      const { getActiveProject } = await import("../dsl/singleton.js");
      const project = getActiveProject();
      if (!project) { console.error("No project found."); return 1; }
      const { InvariantChecker } = await import("../invariants/invariant-checker.js");
      const { allRules } = await import("../invariants/rules/index.js");
      const checker = new InvariantChecker(allRules());
      const results = checker.checkStructural(project.getRegistry());
      const violations = results.filter((r) => r.status === "violated");
      if (violations.length === 0) {
        console.log(Formatter.success("All invariants pass."));
        return 0;
      }
      for (const v of violations) {
        console.log(`! ${v.invariant}`);
        if (v.detail) console.log(`  ${v.detail}`);
      }
      return 1;
    }

    case "unlock": {
      const { StateDatabase } = await import("../state/state-database.js");
      const state = new StateDatabase(resolve(projectDir, "crest-spec.db"));
      state.forceClearLock();
      console.log("Lock cleared.");
      return 0;
    }

    case "vacuum": {
      const before = getFlag("before");
      if (!before) { console.error("Usage: crest-spec vacuum --before DATE"); return 1; }
      console.log(`Vacuum before ${before} — not yet implemented.`);
      return 0;
    }

    case "sql": {
      const dbPath = resolve(projectDir, "crest-spec.db");
      const proc = Bun.spawn(["sqlite3", dbPath], {
        stdin: "inherit",
        stdout: "inherit",
        stderr: "inherit",
      });
      return await proc.exited;
    }

    case "graph": {
      await import(resolve(projectDir, specFile));
      const { getActiveProject } = await import("../dsl/singleton.js");
      const project = getActiveProject();
      if (!project) { console.error("No project found."); return 1; }
      const registry = project.getRegistry();
      console.log("digraph resources {");
      for (const r of registry.getAll()) {
        for (const dep of r.dependencies) {
          console.log(`  "${r.id}" -> "${dep.targetId}" [label="${dep.kind}"];`);
        }
      }
      console.log("}");
      return 0;
    }

    case "contextmap": {
      await import(resolve(projectDir, specFile));
      const { getActiveProject } = await import("../dsl/singleton.js");
      const project = getActiveProject();
      if (!project) { console.error("No project found."); return 1; }
      const map = project.getRegistry().getContextMap();
      console.log("digraph contextmap {");
      for (const r of map) {
        console.log(`  "${r.from}" -> "${r.to}" [label="${r.kind}"];`);
      }
      console.log("}");
      return 0;
    }

    default:
      console.log(`crest-spec — declarative DDD specification tool

Commands:
  init                          Create a new spec and state database
  plan                          Show what would change
  apply                         Execute the plan
  validate                      Check invariants
  log                           List past applies
  history <resource>            Show history for a resource
  state list                    List resources in state
  state rm <id>                 Remove a resource from state
  graph                         Render resource dependency graph (DOT)
  contextmap                    Render context map (DOT)
  unlock                        Clear a stale lock
  vacuum --before DATE          Prune old history
  sql                           Open sqlite3 shell

Options:
  -spec <file>                  Spec file (default: crest-spec.ts)
  -model <id>                   Model ID (default: claude-sonnet-4-6)
  -target <resource>            Target a specific resource
  --force                       Force re-render
  -retries <n>                  Max retries (default: 3)`);
      return command ? 1 : 0;
  }
}

main().then((code) => process.exit(code));
```

Write `src/cli/index.ts`:

```ts
export { Formatter } from "./formatter.js";
```

- [ ] **Step 6: Add bin entry to package.json**

Add to `package.json`:

```json
{
  "bin": {
    "crest-spec": "src/cli/main.ts"
  }
}
```

- [ ] **Step 7: Write basic CLI tests**

Write `tests/cli/commands.test.ts`:

```ts
import { describe, test, expect } from "bun:test";
import { mkdtemp, rm } from "fs/promises";
import { join } from "path";
import { tmpdir } from "os";
import { existsSync } from "fs";

describe("init command", () => {
  test("creates crest-spec.ts and crest-spec.db", async () => {
    const dir = await mkdtemp(join(tmpdir(), "crest-init-"));
    try {
      const { initCommand } = await import("../../src/cli/commands/init");
      const code = await initCommand(dir);
      expect(code).toBe(0);
      expect(existsSync(join(dir, "crest-spec.ts"))).toBe(true);
      expect(existsSync(join(dir, "crest-spec.db"))).toBe(true);
    } finally {
      await rm(dir, { recursive: true });
    }
  });

  test("refuses to overwrite existing spec", async () => {
    const dir = await mkdtemp(join(tmpdir(), "crest-init-"));
    try {
      await Bun.write(join(dir, "crest-spec.ts"), "existing");
      const { initCommand } = await import("../../src/cli/commands/init");
      const code = await initCommand(dir);
      expect(code).toBe(1);
    } finally {
      await rm(dir, { recursive: true });
    }
  });
});
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `bun test tests/cli/commands.test.ts`
Expected: PASS

- [ ] **Step 9: Run full test suite**

Run: `bun test`
Expected: All tests pass across all modules

- [ ] **Step 10: Commit**

```bash
git add src/cli/ tests/cli/ package.json
git commit -m "feat: CLI with init, plan, apply, and all query/utility commands"
```

---

### Task 15: Integration Test & Polish

**Files:**
- Modify: `src/index.ts`

- [ ] **Step 1: Update package public API**

Update `src/index.ts`:

```ts
export * from "./types.js";
export { project, command, event, operation, invariant, relationship, layer } from "./dsl/index.js";
export { ProjectBuilder } from "./dsl/project-builder.js";
export { ContextBuilder } from "./dsl/context-builder.js";
export { AggregateBuilder } from "./dsl/aggregate-builder.js";
export { getActiveProject, resetSingleton } from "./dsl/singleton.js";
export { ResourceRegistry, type IResourceRegistry } from "./registry/index.js";
export { StateDatabase, type IStateDatabase } from "./state/index.js";
export { Planner, type IPlanner, HashComputer, type IHashComputer, Plan } from "./planner/index.js";
export {
  ApplyEngine,
  type IApplyEngine,
  PromptBuilder,
  type IPromptBuilder,
  ConstraintLoop,
  type IConstraintLoop,
  AnthropicLlmClient,
  type ILlmClient,
  ResponseParser,
} from "./engine/index.js";
export { InvariantChecker, type IInvariantChecker, allRules } from "./invariants/index.js";
```

- [ ] **Step 2: Run full test suite**

Run: `bun test`
Expected: All tests pass

- [ ] **Step 3: Verify CLI help output**

Run: `bun src/cli/main.ts`
Expected: Displays help text with all commands listed

- [ ] **Step 4: Verify init command end-to-end**

Run:
```bash
cd /tmp && mkdir crest-test && cd crest-test
bun /path/to/krusty-spec/src/cli/main.ts init
ls -la crest-spec.*
```
Expected: `crest-spec.ts` and `crest-spec.db` are created

- [ ] **Step 5: Commit**

```bash
git add src/index.ts
git commit -m "feat: finalize package public API"
```
