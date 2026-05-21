# crest-spec — a declarative DSL for software architecture

## Premise

Programming languages have spent decades making code more terse and precise. LLM-based coding has rolled this back: we now write long, ambiguous prose prompts to produce code, and the architectural discipline of the author erodes with every chat turn.

crest-spec is the missing layer. You declare the artifacts you want — bounded contexts, aggregates, commands, events, ports — as typed resources in TypeScript using the vocabulary of Domain-Driven Design. The planner realizes them, using the LLM as a constrained subroutine to fill bodies where the declaration is intentionally underspecified. A SQLite state database keeps finished work settled so the spec becomes a living document you extend as your understanding grows.

The expert stays in control of architecture, which is the part LLMs are bad at. Boilerplate collapses into a terse, inspectable spec of what should exist.

## Goals

- **Declarative over imperative.** You describe what exists, not how to make it.
- **DDD vocabulary throughout.** The nouns and verbs are the ones working systems already use: BoundedContext, Aggregate, Entity, ValueObject, Command, DomainEvent, Repository, ApplicationService, Port, Adapter. No invented terminology.
- **Architecture is enforced, not advised.** Clean Architecture and DDD rules (dependency inversion, layer boundaries, aggregate consistency, context boundaries) are first-class declarations checked at plan time.
- **LLM in a cage.** The model fills bodies against typed contracts and declared invariants. It never decides shape, names, dependencies, or topology.
- **Monotonic progress.** A resource is regenerated only if its declaration, its `meta`, a file it references, an upstream dependency, or the generator/model has changed. Nothing else triggers a re-render. Settled work stays settled.
- **Diffable intent.** `plan` shows exactly what will change before anything hits disk.
- **REPL-first ergonomics.** A spec file is executable. The CLI is a thin wrapper for state, diff, and apply.

## Non-goals

- Not a framework. crest-spec does not own your runtime, your control flow, or your deployment.
- Not a chat interface. The model is invoked at well-defined points with structured input, not through conversation.
- Not model-agnostic on day one. Pick one provider, build operators around what it is actually good at, let abstraction emerge from real use.
- Not a visual editor. The spec is text. Visual tools are read-only lenses over the parsed spec and state.

## Mental model

Three things compose to make crest-spec work:

1. **The spec** — a TypeScript file declaring resources. Canonical, diffable, in git.
2. **The state database** — a SQLite file (`crest-spec.db`) recording settled resources, generated bodies, full apply history, every LLM call, invariant check results, and the coordination lock. Append-only by default, queryable by any tool that speaks SQL.
3. **The planner** — reads the spec, diffs against state, computes the work to do, enforces invariants, dispatches LLM calls for body generation, writes results back to both the codebase and the state database.

The lifecycle is `init → plan → apply`, with `apply` re-runnable to converge on the declared state.

## DDD vocabulary

crest-spec uses DDD nouns directly. A reader who knows DDD reads the spec and immediately knows what each resource means.

### Strategic

- **`BoundedContext`** — a self-contained model with its own ubiquitous language. The largest unit of architectural separation in a crest-spec project. A project has one or many.
- **`ContextMap`** — declared relationships between bounded contexts (customer/supplier, anti-corruption layer, shared kernel, published language). Defines how contexts integrate.

### Tactical (inside a bounded context)

- **`Entity`** — has identity that persists over time. Two entities with identical fields are still different if their IDs differ.
- **`ValueObject`** — defined entirely by its attributes; no identity. Immutable. `Ticks(480)` is `Ticks(480)`.
- **`Aggregate`** — a cluster of entities and value objects with one `AggregateRoot` and a consistency boundary. The unit of transaction. External code holds references only to roots.
- **`Command`** — an intent to change state, named in imperative (`RenamePhrase`, `Seek`). Validated against the aggregate's invariants; either accepted (producing events) or rejected.
- **`DomainEvent`** — a fact that has happened in the domain, named in past tense (`PhraseRenamed`, `Seeked`). Immutable. Emitted by aggregates on successful commands.
- **`Repository`** — collection-like access to aggregates by identity. Hides persistence. Declared as a port; implemented as an adapter.
- **`DomainService`** — domain logic that doesn't naturally belong to a single aggregate. Used sparingly.
- **`ApplicationService`** — orchestrates the domain to fulfill a request. Loads aggregates via repositories, dispatches commands, persists results, publishes events. The thin layer between the outside world and the domain.
- **`Factory`** — encapsulates complex aggregate construction. Optional.

### Infrastructure-side

- **`Port`** — an interface the domain or application layer needs from the outside world (Hexagonal terminology, folds cleanly into DDD).
- **`Adapter`** — a concrete implementation of a port.

### Layers

The default four-layer model, with arrows pointing inward (the Dependency Rule):

- **Domain** — entities, value objects, aggregates, domain events, domain services, repository *interfaces*. Depends on nothing.
- **Application** — application services, command handlers. Depends on Domain.
- **Infrastructure** — repository *implementations*, adapters, framework code. Depends on Application and Domain.
- **Interface** — HTTP, CLI, UI. Optional; often folded into Infrastructure. Depends on Application.

Layers are configurable per project. The Dependency Rule is enforced.

## Resource declaration

Examples throughout this section draw from the tracker worked example (see `tracker.md`). The DSL itself is general; the names (`Song`, `Composition`, `Playback`, `PhraseRender`) are tracker-specific.

### `project`

```ts
const app = project("tracker", {
  layers: ["domain", "application", "infrastructure", "interface"],
  rules: [
    layer("domain").dependsOn([]),
    layer("application").dependsOn(["domain"]),
    layer("infrastructure").dependsOn(["application", "domain"]),
    layer("interface").dependsOn(["application"]),
  ],
  meta: {
    style: "functional, no classes; prefer pure functions",
    avoid: ["any", "as unknown as"],
    references: ["./docs/architecture.md"],
  },
})
```

### `BoundedContext`

```ts
const composition = app.context("Composition", {
  purpose: "structural model of a song: chains, phrases, patches, tables, chords",
  ubiquitousLanguage: {
    Song: "a complete musical piece",
    Chain: "an ordered sequence of phrase references",
    Phrase: "a unit of musical content (linear, drum, euclidean, stochastic, generative)",
    Patch: "a signal chain: MIDIEffects → Engine → AudioEffects",
    Table: "a modulation sequence running alongside a phrase",
  },
  meta: {
    references: ["./docs/composition-model.md"],
  },
})
```

A context owns its aggregates, value objects, ports, repositories, application services, and events.

### `Aggregate`

```ts
const song = composition.aggregate("Song", {
  root: true,
  purpose: "the top-level musical piece; owns chains",
  state: {
    id: "SongId",
    name: "string",
    tempo: "BPM",
    chains: "ChainId[]",
  },
  invariants: [
    "tempo is between 20 and 999 BPM",
    "chain IDs are unique within the song",
  ],
  commands: [
    command("RenameSong", { name: "string" }),
    command("SetTempo", { bpm: "BPM" }),
    command("AddChain", { at: "Index" }),
    command("RemoveChain", { id: "ChainId" }),
  ],
  events: [
    event("SongRenamed", { id: "SongId", name: "string" }),
    event("SongTempoChanged", { id: "SongId", from: "BPM", to: "BPM" }),
    event("ChainAddedToSong", { id: "SongId", chainId: "ChainId", at: "Index" }),
    event("ChainRemovedFromSong", { id: "SongId", chainId: "ChainId" }),
  ],
})
```

Each command declares its payload type. Each event declares the fact it represents. The planner generates the command type, the event type, the dispatcher arm, and a stub command handler for each command. The handler body is filled by the LLM against the aggregate's invariants.

### `ValueObject`

```ts
composition.valueObject("Ticks", { from: "number", description: "musical time in 1/96-note ticks" })
composition.valueObject("BPM", { from: "number", invariants: ["between 20 and 999"] })
composition.valueObject("PhraseId", { from: "string", format: "uuid" })
```

Value objects are immutable. The planner generates the type, the constructor, and the equality function.

### `Entity`

Used for things with identity that live inside an aggregate but aren't aggregate roots:

```ts
linearPhrase.entity("Step", {
  state: { index: "number", note: "Note | null", velocity: "Velocity" },
})
```

### Polymorphic aggregates

A family of aggregates that share a contract. The contract is a port; each concrete aggregate provides the implementation.

```ts
const phraseRender = composition.port("PhraseRender", {
  contract: {
    render: "(context: MusicalContext, range: TickRange) => NoteEvent[]"
  },
})

const linearPhrase = composition.aggregate("LinearPhrase", {
  root: true,
  implements: phraseRender,
  state: { id: "PhraseId", length: "number", steps: "Step[]" },
  commands: [ /* ... */ ],
  events: [ /* ... */ ],
})

const euclideanPhrase = composition.aggregate("EuclideanPhrase", {
  root: true,
  implements: phraseRender,
  state: { id: "PhraseId", steps: "number", pulses: "number", rotation: "number" },
  commands: [ /* ... */ ],
  events: [ /* ... */ ],
})
```

The Sequencer in Playback depends on `PhraseRender` and asks any phrase to render itself. Adding a new phrase type is a new aggregate that implements `PhraseRender` and a new view in the Editor context — no changes to Playback.

### `Repository`

```ts
const songRepository = composition.repository("SongRepository", {
  of: song,
  contract: {
    findById: "(id: SongId) => Promise<Song | null>",
    save: "(song: Song) => Promise<void>",
    delete: "(id: SongId) => Promise<void>",
  },
})
```

The repository is a port. Adapters implement it in the infrastructure layer.

### `ApplicationService`

```ts
const songEditor = composition.applicationService("SongEditor", {
  purpose: "orchestrates command execution against Song aggregates",
  uses: [songRepository, eventBus],
  operations: [
    operation("renameSong", { input: { id: "SongId", name: "string" } }),
    operation("setTempo", { input: { id: "SongId", bpm: "BPM" } }),
  ],
})
```

The planner generates the service skeleton; the LLM fills each operation, following the pattern: load aggregate via repository → dispatch command → save → publish events.

### `Port` and `Adapter`

```ts
// in the AudioEngine context
const audioOutput = audioEngine.port("AudioOutput", {
  contract: { write: "(buffer: Float32Array) => void" },
})

// in the infrastructure layer
app.adapter("WebAudioOutput", { implements: audioOutput, layer: "infrastructure" })
app.adapter("NullAudioOutput", { implements: audioOutput, layer: "infrastructure" })
```

### `ContextMap`

```ts
app.contextMap([
  relationship(playback, composition, { kind: "customer-supplier", direction: "downstream" }),
  relationship(audioEngine, playback, { kind: "customer-supplier", direction: "downstream" }),
  relationship(editor, composition, { kind: "customer-supplier", direction: "both" }),
  relationship(editor, playback, { kind: "customer-supplier", direction: "downstream" }),
  relationship(controls, editor, { kind: "customer-supplier", direction: "downstream" }),
  relationship(controls, playback, { kind: "customer-supplier", direction: "downstream" }),
])
```

The map is enforced: a context cannot reference another context's internals except through the declared relationship.

A complete worked example using all of these resource types is in the companion document, `tracker.md`.

## The `meta` field

Every resource accepts an optional `meta` object: a place to attach additional prompts, rules, hints, references, and constraints that scope to that resource (or to the project, when set on `project`). The core declaration stays focused on *what should exist*; `meta` carries the softer, more contextual material that shapes *how* the LLM fills in underspecified parts.

```ts
const playback = app.context("Playback", {
  purpose: "scheduling and emission of timed note events",
  meta: {
    rules: [
      "tempo changes must be lock-free",
      "groove templates apply only to events from this context",
    ],
    prompts: [
      "all time arithmetic uses Ticks; never mix with seconds at this layer",
    ],
    references: ["./docs/timing-model.md"],
    avoid: ["setInterval", "Date.now()"],
    style: "single-threaded scheduler, no shared mutable state",
  },
})
```

### Well-known keys

- **`rules`** — hard constraints. Treated as local invariants; violations cause rejection in the constraint loop.
- **`prompts`** — softer guidance. Included in LLM context, not enforced.
- **`references`** — file paths or URLs to documents the LLM should read as context.
- **`examples`** — paths to code the LLM should treat as exemplars.
- **`avoid`** — APIs, patterns, or libraries to stay away from.
- **`style`** — short style descriptor.
- **`notes`** — free-form prose. Included verbatim.

Unknown keys are preserved and passed to LLM context as-is.

### Inheritance

`meta` on `project` is the baseline. Resource-level `meta` extends (lists concatenate) or overrides (scalars replace). Context `meta` inherits to its aggregates. Aggregate `meta` inherits to its commands and events.

### `meta` on invariants

Invariants accept `meta`, mostly for `rationale`:

```ts
invariant("all mutations go through ApplicationServices", {
  meta: {
    rationale: "single audit point; enables event sourcing later",
    references: ["./docs/event-sourcing.md"],
  },
})
```

When a violation is reported, the rationale is surfaced in the error message.

### `meta` is hashed

The merged `meta` is part of `declaration_hash`. Changing `meta` is a declaration change and shows up in `plan`.

## Monotonic regeneration

The single most important property of crest-spec: **a resource is regenerated only if it has changed, or if something it depends on has changed.** Nothing else triggers a re-render. Settled work stays settled.

### `effective_hash`

Every resource has an `effective_hash` combining:

1. **The declaration itself** — structural fields.
2. **The merged `meta`** — including inheritance.
3. **The contents of files referenced from `meta.references` and `meta.examples`** — hashed by content.
4. **The `effective_hash` of every resource this one depends on** — transitively.
5. **The project-level and context-level invariants** that apply.
6. **The generator version and model identifier** for LLM-generated parts.

The hash does *not* include unrelated resources, the contents of generated files, or any non-input signal.

### Cascade is bounded by the graph

The planner walks dependencies in topological order. Changing the `PhraseRender` port re-renders every aggregate that implements it and every consumer (Playback's Sequencer). Changing one aggregate doesn't touch unrelated aggregates.

### `plan` reports the reason

```
~ aggregate.LinearPhrase
  reason: port.PhraseRender contract changed
  re-render: src/composition/linear-phrase.ts, src/composition/linear-phrase.test.ts
  cascade: 0 dependents

~ aggregate.EuclideanPhrase
  reason: port.PhraseRender contract changed
  re-render: src/composition/euclidean-phrase.ts, ...

~ applicationService.Sequencer
  reason: port.PhraseRender contract changed
  re-render: src/playback/sequencer.ts
```

### Per-file granularity

A resource produces multiple generated files. Each is hashed individually. Files whose new generation hashes to stored content are skipped entirely. No-op writes never happen.

### Forcing a re-render

```
crest-spec apply -target aggregate.LinearPhrase --force
```

`--force` invalidates the stored hash for the target only. The cascade still respects the graph.

## The plan / apply lifecycle

### `crest-spec plan`

Reads the spec, loads state, computes the diff. Output:

- **Create**: resources in the spec, absent from state.
- **Modify**: resources whose `effective_hash` has changed.
- **Destroy**: resources in state, absent from the spec.
- **Refresh**: generated files whose disk hash has drifted from state (hand-edits).
- **Invariant violations**: rules the current spec would violate.

`plan` never touches disk or calls the LLM. Read-only.

### `crest-spec apply`

Executes the plan transactionally. For each resource:

1. Render the deterministic parts (types, command/event types, dispatcher arms, scaffolds).
2. For LLM-filled bodies, invoke the model with the resource declaration, merged `meta`, relevant invariants, port contracts, and referenced documents.
3. Validate the body against contracts, types, and invariants.
4. On failure, retry with the constraint violation as feedback. Up to N retries (default 3).
5. Write to disk and record in state.

`apply` is idempotent. Re-running with no spec changes is a no-op.

### `crest-spec refresh`

Detects drift between disk and state. Prompts to either re-import hand edits (accepting them as the new baseline) or revert to the last applied generation.

### Targeted apply

`crest-spec apply -target aggregate.Song` re-applies a single resource and its dependents.

## State: SQLite

State lives in a single SQLite file per project (`crest-spec.db`), in git alongside the spec. SQLite is a core part of the design — used for state, coordination, history, and audit.

### Why SQLite

- **Relational queries.** "Which aggregates implement port X?" "Which generated files were produced by model Y?" "Which resources have drifted?" One-line SQL.
- **Transactional applies.** All-or-nothing state updates across an apply.
- **Coordination.** `BEGIN IMMEDIATE` plus a lock table prevents concurrent applies from racing.
- **History.** Every apply, every generation, every check is a row, not an overwritten field.
- **External inspection.** Any SQL tool reads state. `sqlite3 crest-spec.db` is a first-class debugging interface.

### Schema (sketch)

```sql
CREATE TABLE resources (
  id TEXT PRIMARY KEY,              -- e.g. "aggregate.composition.Song"
  kind TEXT NOT NULL,               -- "context" | "aggregate" | "valueObject" | "port" | ...
  context TEXT,                     -- bounded context ID, NULL for project-level
  declaration_hash TEXT NOT NULL,   -- declaration + merged meta
  effective_hash TEXT NOT NULL,     -- folded with deps, refs, invariants, model
  declaration_json TEXT NOT NULL,
  layer TEXT,
  settled_at TIMESTAMP,
  last_apply_id INTEGER REFERENCES applies(id)
);

CREATE TABLE generated_files (
  path TEXT PRIMARY KEY,
  resource_id TEXT NOT NULL REFERENCES resources(id),
  content_hash TEXT NOT NULL,
  generator TEXT NOT NULL,          -- "deterministic" | "llm"
  model TEXT,
  prompt_hash TEXT,
  generated_at TIMESTAMP NOT NULL
);

CREATE TABLE dependencies (
  from_resource TEXT NOT NULL REFERENCES resources(id),
  to_resource TEXT NOT NULL REFERENCES resources(id),
  kind TEXT NOT NULL,               -- "implements" | "uses" | "consumes" | "publishes" | ...
  PRIMARY KEY (from_resource, to_resource, kind)
);

CREATE TABLE context_relationships (
  from_context TEXT NOT NULL,
  to_context TEXT NOT NULL,
  kind TEXT NOT NULL,               -- "customer-supplier" | "anti-corruption" | "shared-kernel" | "published-language"
  direction TEXT NOT NULL,          -- "upstream" | "downstream" | "both"
  PRIMARY KEY (from_context, to_context, kind)
);

CREATE TABLE applies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at TIMESTAMP NOT NULL,
  finished_at TIMESTAMP,
  status TEXT NOT NULL,             -- "running" | "ok" | "failed" | "aborted"
  spec_hash TEXT NOT NULL,
  notes TEXT
);

CREATE TABLE apply_actions (
  apply_id INTEGER NOT NULL REFERENCES applies(id),
  resource_id TEXT NOT NULL REFERENCES resources(id),
  action TEXT NOT NULL,             -- "create" | "modify" | "destroy" | "noop"
  outcome TEXT NOT NULL,
  PRIMARY KEY (apply_id, resource_id)
);

CREATE TABLE generations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  apply_id INTEGER NOT NULL REFERENCES applies(id),
  resource_id TEXT NOT NULL REFERENCES resources(id),
  model TEXT NOT NULL,
  prompt_hash TEXT NOT NULL,
  prompt_text TEXT NOT NULL,
  output_text TEXT NOT NULL,
  retries INTEGER NOT NULL,
  outcome TEXT NOT NULL,            -- "accepted" | "rejected"
  rejection_reason TEXT,
  created_at TIMESTAMP NOT NULL
);

CREATE TABLE invariant_checks (
  apply_id INTEGER NOT NULL REFERENCES applies(id),
  invariant TEXT NOT NULL,
  resource_id TEXT,
  status TEXT NOT NULL,             -- "ok" | "violated"
  detail TEXT,
  PRIMARY KEY (apply_id, invariant, resource_id)
);

CREATE TABLE lock (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  holder TEXT NOT NULL,
  acquired_at TIMESTAMP NOT NULL
);
```

### Coordination

`apply` opens a transaction with `BEGIN IMMEDIATE` and inserts into `lock`. Concurrent applies see the lock and either wait or abort with the holder's identity. `crest-spec unlock` clears a stale lock.

### History

`applies`, `apply_actions`, `generations`, and `invariant_checks` are append-only. `crest-spec log` walks `applies`. `crest-spec history aggregate.Song` shows every action and generation for one resource. `crest-spec diff <apply_a> <apply_b>` reconstructs the state delta.

### Generated bodies stored

The codebase has the canonical text on disk; the state DB has the hash and the full prompt+output for every LLM generation. Reverting a hand-edit is reading the last accepted output text out of `generations`.

## LLM integration

The LLM is a constrained subroutine.

### Where it's called

- Filling command-handler bodies against an aggregate's invariants.
- Filling application-service operation bodies against the orchestration pattern.
- Filling adapter implementations against port contracts.
- Generating test bodies against pre/postconditions.
- Implementing the `render` method on polymorphic aggregates (each phrase type, the Table).

### Where it is not called

- Choosing aggregate boundaries, command names, event names, repository contracts. All declared.
- Choosing dependencies. All declared.
- Deciding what should exist.

### Context per call

The LLM receives:

1. The resource's declaration.
2. The merged `meta` for the resource.
3. Project-level and context-level invariants relevant to this layer.
4. Any documents from `meta.references` and `meta.examples`.
5. The declared contracts of any ports this resource needs.
6. The state/commands/events of any aggregates it consumes.

Scoped, not chat-history-flavored.

### Constraint loop

```
generate → must_compile → must_match(contract) → must_satisfy(invariants) → must_pass(tests) → write
              ↑                 ↑                       ↑                       ↑
              └─────────── retry on failure with the violation as feedback (up to N) ───┘
```

A body that fails any gate is never written. After N retries, `apply` aborts and surfaces the last failure.

## CLI surface

```
crest-spec init                       # scaffold a new spec.ts and crest-spec.db
crest-spec plan                       # diff spec vs state, show invariant violations
crest-spec apply                      # execute the plan (acquires lock, transactional)
crest-spec apply -target X            # apply a single resource and its dependents
crest-spec apply -target X --force    # force re-render of a specific resource
crest-spec refresh                    # reconcile codebase drift with state
crest-spec graph                      # render the resource graph (read-only lens)
crest-spec contextmap                 # render the context map specifically
crest-spec validate                   # check invariants without diffing
crest-spec log                        # list past applies
crest-spec history <resource>         # full history of one resource
crest-spec diff <apply_a> <apply_b>   # state delta between two applies
crest-spec state list                 # inspect current resources
crest-spec state rm X                 # remove a resource from state (does not delete code)
crest-spec unlock                     # clear a stale coordination lock
crest-spec vacuum --before DATE       # compact history older than DATE
crest-spec sql                        # open a sqlite3 shell against crest-spec.db
```

## Editor and visual tooling

Text-first. The spec is TypeScript; existing LSP, autocomplete, and refactor tooling work out of the box.

Visual tooling is strictly read-only:

- `crest-spec graph` renders the full resource graph.
- `crest-spec contextmap` renders the context map (boundaries and relationships).
- A future VS Code extension provides a side panel that updates as the spec changes, highlights invariant violations, and supports click-to-jump.

Bidirectional graphical editing is explicitly out of scope. The spec file is canonical.

## Open questions

- **Schema declaration syntax.** TS types via a compile-time transformer vs. a runtime schema lib. Lean toward TS types with a transformer.
- **Multi-target output.** One spec emitting both TS client and Go server. v2 concern.
- **Per-resource model override.** Useful eventually; v1 picks one model and stays there.
- **Custom invariants.** User-supplied predicate functions over the resource graph. Worth building, but pre-canned set should cover most projects.
- **Test strategy.** How aggressive should LLM-generated assertions be? Default to conservative — fail-on-stub better than passing tests that test nothing.
- **Drift policy.** Default: flag in `plan`, require explicit `refresh`. Never silent overwrite.
- **Context boundary enforcement strength.** crest-spec enforces that contexts don't reach into each other's internals except through declared relationships. Should it also enforce *kind* (e.g. anti-corruption layer requires a translation adapter)? Probably yes, but starts complex.

## v1 scope

- TypeScript spec DSL with all DDD resource types listed above.
- `meta` support on all resources with well-known keys and inheritance.
- TS-based deterministic generation for command/event types, dispatcher arms, aggregate scaffolds, value object constructors, port interfaces.
- Single LLM provider, single model, constraint loop with retry.
- Pre-canned invariants for SRP, layer rules, aggregate consistency, context boundary respect, mutation routing through application services.
- SQLite state with full schema and coordination lock.
- `plan`, `apply`, `refresh`, `graph`, `contextmap`, `log`, `history`, `sql` commands.
- Tracker as the v1 dogfood and worked example; spec in `tracker.md`.

Out of v1: multi-target output, custom invariants, per-resource model selection, VS Code extension, remote/replicated SQLite backends.
