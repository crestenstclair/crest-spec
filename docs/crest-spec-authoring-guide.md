# Crest-Spec Authoring Guide

You are writing a **crest-spec** file: a TypeScript DSL that declaratively describes a software architecture using Domain-Driven Design (DDD) and Clean Architecture concepts. The output is a `.ts` file that the crest-spec engine processes to generate code, track changes, and enforce invariants.

---

## File Structure

A crest-spec file follows this top-down order:

1. **Imports** from `crest-spec`
2. **Project** declaration (one per file)
3. **Contexts** with their resources (value objects, ports, aggregates, entities, repositories, services)
4. **Context map** (relationships between contexts)
5. **Global invariants**

```ts
import { project, command, event, operation, invariant, relationship, layer } from "crest-spec";

const app = project("my-app", { /* ... */ });

// contexts and resources...

app.contextMap([ /* ... */ ]);
app.invariants([ /* ... */ ]);
```

---

## Project

The root declaration. Defines layers, layer dependency rules, and project-wide metadata.

```ts
const app = project("project-name", {
  layers: ["domain", "application", "infrastructure", "interface"],
  rules: [
    layer("domain").dependsOn([]),
    layer("application").dependsOn(["domain"]),
    layer("infrastructure").dependsOn(["application", "domain"]),
    layer("interface").dependsOn(["application"]),
  ],
  meta: {
    language: "typescript",       // or "csharp", "go", etc.
    framework: "Godot 4",        // optional
    style: "description of code conventions",
    avoid: ["things to not do"],
    prompts: ["guidance for code generation"],
    references: ["./docs/architecture.md"],
  },
});
```

**Fields:**
- `layers` - ordered list of architectural layer names
- `rules` - dependency rules between layers. `layer("X").dependsOn(["Y"])` means layer X may import from layer Y. Empty array means no dependencies allowed.
- `meta` - project-wide metadata inherited by all contexts and resources (see Meta section)

---

## Context

A bounded context (DDD). Groups related domain concepts.

```ts
const catalog = app.context("Catalog", {
  purpose: "product catalog management",
  ubiquitousLanguage: {
    "Product": "a sellable item with a name, price, and active status",
    "SKU": "stock keeping unit; unique product identifier",
  },
  meta: { /* optional context-level meta */ },
});
```

**Fields:**
- `purpose` (required) - one-line description of what this context owns
- `ubiquitousLanguage` (optional) - glossary of domain terms. Keys are term names, values are definitions.
- `meta` (optional) - merged with project meta; arrays concatenate, scalars replace

### Naming

Context names are PascalCase singular nouns or noun phrases: `Composition`, `Playback`, `MIDIImport`, `AudioEffects`.

---

## Value Object

Immutable, identity-less domain primitive. Two forms:

**Wrapper type** (wraps a primitive):
```ts
context.valueObject("BPM", {
  from: "number",
  description: "beats per minute",
  invariants: ["between 20 and 999"],
});
```

**Composite type** (has multiple fields):
```ts
context.valueObject("PhraseStep", {
  state: {
    note: "Note | null",
    velocity: "Velocity",
    patchId: "PatchIndex | null",
    fx1: "FxValue | null",
  },
  invariants: ["velocity is 0..127"],
});
```

**Fields:**
- `from` - base type for wrapper value objects: `"number"`, `"string"`, etc.
- `state` - field map for composite value objects. Keys are field names, values are type strings.
- `description` (optional) - what this value object represents
- `format` (optional) - expected format (e.g., `"UUID v4"`)
- `invariants` (optional) - array of prose constraint strings
- `meta` (optional)

Use `from` OR `state`, not both.

---

## Port

An interface/contract (hexagonal architecture). Defines what operations a capability must support.

```ts
const audioProducer = context.port("AudioProducer", {
  contract: {
    noteOn: "(note: Note, velocity: Velocity, channel: number) => void",
    noteOff: "(note: Note, channel: number) => void",
    render: "(frames: number) => Float32Array",
  },
});
```

**Fields:**
- `contract` (required) - method signatures as strings. Keys are method names, values are type signatures.
- `meta` (optional)

Ports return a `PortRef` that aggregates can reference via `implements`.

---

## Aggregate

The core DDD building block. An aggregate is a consistency boundary with state, commands, events, and invariants.

```ts
const product = context.aggregate("Product", {
  root: true,
  purpose: "manages product lifecycle and pricing",
  state: {
    id: "ProductId",
    name: "string",
    price: "number",
    active: "boolean",
  },
  invariants: [
    "price must be positive",
    "name must not be empty",
  ],
  commands: [
    command("CreateProduct", { name: "string", price: "number" }),
    command("UpdatePrice", { newPrice: "number" }),
    command("Deactivate"),
  ],
  events: [
    event("ProductCreated", { id: "ProductId", name: "string", price: "number" }),
    event("PriceUpdated", { id: "ProductId", oldPrice: "number", newPrice: "number" }),
    event("ProductDeactivated", { id: "ProductId" }),
  ],
  meta: { /* optional */ },
});
```

**Fields:**
- `root` (optional) - `true` if this is an aggregate root
- `purpose` (optional) - what this aggregate manages
- `implements` (optional) - a `PortRef` returned by `context.port()`
- `state` - field map. Keys are field names, values are type strings.
- `invariants` (optional) - array of prose constraint strings
- `commands` (optional) - array of `command()` descriptors
- `events` (optional) - array of `event()` descriptors
- `meta` (optional)

Aggregates return an `AggregateRef` (with `.id` and `.name`) used by repositories and services.

### Implementing a Port

```ts
const port = context.port("AudioProducer", { contract: { /* ... */ } });

context.aggregate("MeltySynthEngine", {
  implements: port,
  // ...
});
```

---

## Entity

An object with identity that lives inside an aggregate. Declared on the aggregate builder:

```ts
const song = context.aggregate("Song", { /* ... */ });

song.entity("Track", {
  state: {
    index: "TrackIndex",
    volume: "number",
    pan: "number",
    muted: "boolean",
  },
});
```

Or directly on the context (without a parent aggregate):

```ts
context.entity("Binding", {
  state: { event: "string", action: "string", target: "string" },
});
```

**Fields:**
- `state` (required) - field map
- `meta` (optional)

---

## Command

An intent to change state. Imperative verb, PascalCase.

```ts
command("SetBpm", { bpm: "BPM" })
command("Deactivate")                    // no payload
command("AddPostEffect", { patch: "PatchIndex", effectType: "string", at: "number" })
```

**Naming conventions:**
- Imperative mood: `Set`, `Add`, `Remove`, `Create`, `Update`, `Toggle`
- PascalCase
- Payload keys are camelCase
- Payload values are type strings referencing value objects or primitives

---

## Event

Something that happened. Past tense, PascalCase.

```ts
event("BpmChanged", { from: "BPM", to: "BPM" })
event("ProductCreated", { id: "ProductId", name: "string", price: "number" })
event("PostEffectRemoved", { patch: "PatchIndex", at: "number" })
```

**Naming conventions:**
- Past tense: `Changed`, `Created`, `Set`, `Added`, `Removed`, `Toggled`
- PascalCase
- Change events typically carry `from`/`to` for the changed value
- Creation events carry the full initial state
- Payload keys are camelCase

---

## Repository

Persistence interface for an aggregate. Automatically depends on its aggregate.

```ts
context.repository("ProductRepository", { of: product });
```

With explicit contract:

```ts
context.repository("ProductRepository", {
  of: product,
  contract: {
    findById: "ProductId -> Product | null",
    save: "Product -> void",
  },
});
```

**Fields:**
- `of` (required) - an `AggregateRef` (the return value of `context.aggregate()`)
- `contract` (optional) - explicit method signatures
- `meta` (optional)

---

## Application Service

Orchestrates use cases. Lives in the application layer.

```ts
context.applicationService("CatalogService", {
  purpose: "orchestrates product commands",
  uses: [product, otherAggregate],
  operations: [
    operation("createProduct", { input: { name: "string", price: "number" } }),
    operation("updatePrice", { input: { productId: "string", newPrice: "number" } }),
  ],
  meta: { /* optional */ },
});
```

**Fields:**
- `purpose` (required) - what this service orchestrates
- `uses` (optional) - array of `AggregateRef`s this service depends on
- `operations` (optional) - array of `operation()` descriptors
- `meta` (optional)

### Operation

A use case exposed by an application service:

```ts
operation("createProduct", { input: { name: "string", price: "number" } })
```

Operation names are camelCase verbs.

---

## Domain Service

Domain logic that doesn't belong in an aggregate. Lives in the domain layer.

```ts
context.domainService("Quantizer", {
  purpose: "snaps MIDI note timing to the nearest grid position",
  uses: [someAggregate],
});
```

**Fields:**
- `purpose` (required)
- `uses` (optional) - array of `AggregateRef`s
- `meta` (optional)

---

## Adapter

Implementation of a port. Lives in the infrastructure layer by default.

```ts
app.adapter("WebAudioOutput", {
  implements: audioProducer,   // PortRef
  layer: "infrastructure",     // optional, defaults to "infrastructure"
});
```

Declared on the project, not on a context.

---

## Asset Kind and Asset

Templates and generated content (tests, documentation, etc.).

**Asset kind** (template):
```ts
app.assetKind("unit-test", {
  description: "unit test for a single resource",
  filePattern: "tests/{context}/{name}.test.ts",
  prompts: ["use bun:test", "one test file per aggregate"],
  references: ["./docs/testing-guide.md"],
});
```

**Asset** (instance):
```ts
context.asset("product-tests", {
  kind: "unit-test",
  description: "unit tests for Product aggregate",
  targets: [product],           // ResourceRefs
  prompts: ["test all invariants"],
});
```

Assets can also be declared on aggregates:
```ts
product.asset("product-tests", { kind: "unit-test", /* ... */ });
```

---

## Context Map

Declares relationships between bounded contexts.

```ts
app.contextMap([
  relationship("Playback", "Composition", { kind: "customer-supplier", direction: "downstream" }),
  relationship("Composition", "Kernel", { kind: "shared-kernel" }),
  relationship("MIDIImport", "Composition", { kind: "customer-supplier", direction: "upstream" }),
  relationship("Editor", "Composition", { kind: "customer-supplier", direction: "both" }),
]);
```

**Relationship kinds:**
- `"shared-kernel"` - two contexts share a common model (typically used for a Kernel/primitives context)
- `"customer-supplier"` - one context depends on another. Use `direction` to clarify:
  - `"downstream"` - the `from` context consumes from the `to` context
  - `"upstream"` - the `from` context writes to the `to` context
  - `"both"` - bidirectional dependency
- `"anti-corruption"` - the `from` context translates the `to` context's model into its own
- `"published-language"` - the `to` context exposes a stable, versioned API

---

## Global Invariants

Project-wide rules enforced across all contexts.

```ts
app.invariants([
  invariant("domain layer has no infrastructure imports", {
    meta: { rationale: "clean architecture dependency rule" },
  }),
  invariant("all mutations flow through a central dispatcher", {
    meta: { rationale: "enables undo/redo and audit logging" },
  }),
  invariant("contexts do not reach into each other's internals except via declared relationships"),
]);
```

Write invariants as plain-English rules. Include a `rationale` in meta to explain why the rule exists.

---

## Meta

Every resource accepts an optional `meta` object. Meta flows downward: project -> context -> aggregate -> commands/events.

```ts
meta: {
  rules: string[];        // hard constraints the generator must obey
  prompts: string[];      // soft guidance for code generation
  references: string[];   // file paths or URLs the generator should read
  examples: string[];     // code snippets to follow
  avoid: string[];        // patterns to avoid
  style: string;          // code style description
  notes: string;          // free-form prose
  language: string;       // target language
  framework: string;      // target framework
  rationale: string;      // why something exists (used on invariants)
  [key: string]: unknown; // custom keys are preserved
}
```

**Inheritance rules:**
- Arrays (rules, prompts, references, examples, avoid) **concatenate** down the hierarchy
- Scalars (style, language, framework, notes) **replace** (child overrides parent)

---

## Type Strings

State fields and payloads use type strings, not actual TypeScript types. These are passed through to the code generator.

| Pattern | Example |
|---------|---------|
| Primitive | `"string"`, `"number"`, `"boolean"` |
| Value object reference | `"BPM"`, `"Note"`, `"ProductId"` |
| Nullable | `"Note \| null"` |
| Array | `"Item[]"`, `"number[]"` |
| Fixed-size array | `"Track[16]"`, `"PhraseStep[16]"` |
| Record/map | `"Record<string, number>"` |
| Union | `"Groove \| null[16]"` |
| Object literal | `"object"` |

Reference value objects defined elsewhere in the spec by name. The engine resolves these references.

---

## Resource ID Conventions

Every resource gets an auto-generated ID following this pattern:

| Kind | ID Pattern | Example |
|------|-----------|---------|
| project | `project.{name}` | `project.my-app` |
| context | `context.{name}` | `context.Catalog` |
| aggregate | `aggregate.{context}.{name}` | `aggregate.Catalog.Product` |
| entity (on aggregate) | `entity.{context}.{aggregate}.{name}` | `entity.Composition.Song.Track` |
| entity (on context) | `entity.{context}.{name}` | `entity.Controls.Binding` |
| valueObject | `valueObject.{context}.{name}` | `valueObject.Kernel.BPM` |
| port | `port.{context}.{name}` | `port.Composition.PhraseRender` |
| repository | `repository.{context}.{name}` | `repository.Catalog.ProductRepository` |
| applicationService | `applicationService.{context}.{name}` | `applicationService.Catalog.CatalogService` |
| domainService | `domainService.{context}.{name}` | `domainService.MIDIImport.Quantizer` |
| adapter | `adapter.{name}` | `adapter.WebAudioOutput` |
| assetKind | `assetKind.{name}` | `assetKind.unit-test` |
| asset | `asset.{name}` or `asset.{context}.{name}` | `asset.Catalog.product-tests` |

---

## Complete Minimal Example

```ts
import { project, command, event, operation, invariant, relationship, layer } from "crest-spec";

const app = project("bookstore", {
  layers: ["domain", "application", "infrastructure"],
  rules: [
    layer("domain").dependsOn([]),
    layer("application").dependsOn(["domain"]),
    layer("infrastructure").dependsOn(["application", "domain"]),
  ],
  meta: {
    language: "typescript",
    style: "functional TypeScript; readonly types; type aliases for value objects",
  },
});

// -- Kernel --

const kernel = app.context("Kernel", { purpose: "shared primitives" });

kernel.valueObject("BookId", { from: "string", description: "UUID v4" });
kernel.valueObject("Money", {
  state: { amount: "number", currency: "string" },
  invariants: ["amount >= 0", "currency is ISO 4217"],
});

// -- Catalog --

const catalog = app.context("Catalog", {
  purpose: "book inventory and pricing",
  ubiquitousLanguage: {
    "Book": "a title available for sale with ISBN, price, and stock count",
  },
});

const book = catalog.aggregate("Book", {
  root: true,
  purpose: "manages book metadata, pricing, and stock",
  state: {
    id: "BookId",
    title: "string",
    isbn: "string",
    price: "Money",
    stock: "number",
  },
  invariants: ["stock >= 0", "isbn is valid ISBN-13"],
  commands: [
    command("AddBook", { title: "string", isbn: "string", price: "Money" }),
    command("UpdatePrice", { price: "Money" }),
    command("AdjustStock", { delta: "number" }),
  ],
  events: [
    event("BookAdded", { id: "BookId", title: "string", isbn: "string", price: "Money" }),
    event("PriceUpdated", { id: "BookId", from: "Money", to: "Money" }),
    event("StockAdjusted", { id: "BookId", from: "number", to: "number" }),
  ],
});

catalog.repository("BookRepository", { of: book });

catalog.applicationService("CatalogService", {
  purpose: "orchestrates book catalog operations",
  uses: [book],
  operations: [
    operation("addBook", { input: { title: "string", isbn: "string", price: "Money" } }),
    operation("updatePrice", { input: { bookId: "BookId", price: "Money" } }),
    operation("adjustStock", { input: { bookId: "BookId", delta: "number" } }),
  ],
});

// -- Context Map --

app.contextMap([
  relationship("Catalog", "Kernel", { kind: "shared-kernel" }),
]);

// -- Invariants --

app.invariants([
  invariant("domain layer has no infrastructure imports"),
]);
```

---

## Design Principles

When designing a crest-spec, follow these principles:

1. **One aggregate per consistency boundary.** If two things must be transactionally consistent, they belong in the same aggregate. If they can be eventually consistent, separate them.

2. **Aggregates own their invariants.** Every business rule should be expressed as an invariant on the aggregate that enforces it.

3. **Commands are imperative, events are past tense.** `SetBpm` / `BpmChanged`. `CreateProduct` / `ProductCreated`.

4. **Value objects for domain primitives.** Don't pass raw `number` or `string` when a named type (BPM, Email, Money) makes the model more expressive.

5. **Kernel context for shared primitives.** Types used across multiple contexts belong in a shared Kernel context with `shared-kernel` relationships.

6. **Ports for cross-cutting capabilities.** When multiple implementations exist (synth engines, renderers, input sources), define a port and have aggregates implement it.

7. **Application services orchestrate, domain services calculate.** Application services coordinate aggregates. Domain services contain logic that doesn't belong to any single aggregate.

8. **Repository per aggregate root.** Every aggregate root gets a repository. Non-root aggregates are accessed through their root's repository.

9. **Context map makes integration explicit.** Every dependency between contexts must appear in the context map. No implicit cross-context references.

10. **Invariants explain why, not just what.** Use `meta.rationale` to document the reasoning behind each constraint.
