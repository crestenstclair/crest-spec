# crest-spec: Functional & Implementation Specification

## Overview

crest-spec is a **declarative, domain-driven code generation system**. You describe your software architecture as CUE specification files using a schema rooted in Domain-Driven Design (DDD) vocabulary, then generate implementation code by dispatching each declared resource to an LLM sub-agent with surgically scoped prompts. The system tracks all state in SQLite, diffs specs against prior state to build execution plans, and enforces architectural invariants at every stage.

The mental model is **Terraform for code generation**: you declare what your system *should* look like, the tool plans what needs to change, and then applies those changes — with dependency ordering, retry loops, and verification gates.

**Runtime:** A standalone Go binary (`crest-spec`) that acts as an MCP server. Its sub-agent execution engine is adapted from the proven `claude-mcp` server — the same agent wrapper (config isolation, process groups, concurrency semaphore), async job model (SQLite-persisted, PID-liveness-reconciled), and dual transports (stdio + Streamable HTTP) that are already battle-tested. The orchestrator dispatches sub-agents for code generation via `run_prompt`, and can invoke `code_review` and `bugbot` as verification steps in the constraint loop. On top of this engine, crest-spec adds CUE spec loading (via `cuelang.org/go`), a plan/apply lifecycle, prompt construction, wave-based execution, and the constraint loop.

- **Module:** `github.com/crestenstclair/crest-spec`
- **Go version:** 1.26.4
- **Server version reported over MCP:** `0.1.0`
- **MCP protocol version:** `2024-11-05`
- **Transport:** stdio (JSON-RPC over stdin/stdout) and Streamable HTTP (`POST /mcp`; SSE upgrade for progress streaming is planned but not yet implemented — currently plain JSON-RPC only)
- **Sub-agent invocation:** `runner.RunPrompt` (claude `--print` subprocess), with `--disallowedTools` for constrained code generation
- **Platform assumptions:** Unix-like (uses `syscall.Kill`, process groups, `ps`); developed on macOS/Darwin.

---

## 1. The CUE DSL

The spec language is [CUE](https://cuelang.org) — a data constraint language written in Go with a native Go API. CUE replaces the TypeScript builder-pattern DSL while preserving the same resource model, dependency semantics, and meta inheritance.

### 1.1 Why CUE

CUE's design maps directly to crest-spec's core needs:

- **Unification model -> Multi-file composition.** CUE files in the same package automatically merge. Split a large spec across `kernel.cue`, `synth.cue`, `shell.cue` — no explicit import wiring. CUE unifies them into a single value.

- **Constraints -> Invariants for free.** CUE's type system is a constraint system. An invariant like "must be 0-127" becomes `& >=0 & <=127` — validated at load time by the CUE evaluator, before any LLM is involved.

- **Go-native parsing.** The `cuelang.org/go` library parses and evaluates CUE directly in the Go process. No subprocess, no build step, no runtime dependency.

- **Declarative, not imperative.** CUE files are pure data with constraints — no side effects, no execution order. The resource graph is the spec, not a trace of builder calls.

- **Readable without tooling.** CUE is JSON-like with types and defaults. A spec file is self-documenting — you don't need to trace through builder methods to understand what's declared.

### 1.2 Design Philosophy

- **Spec files are CUE, not code.** You declare resources as structured data. CUE gives you types, constraints, defaults, and composition — but not loops, side effects, or runtime behavior. The spec is a static description.
- **DDD vocabulary is the schema.** Aggregates, value objects, entities, ports, adapters, domain services, repositories — each has a CUE definition with typed fields and constraints.
- **Metadata flows downward.** Project-level meta (language, style, avoid rules) merges into context-level meta, which merges into resource-level meta. CUE's unification handles the merge semantics naturally.
- **Dependencies are explicit.** References between resources (`uses`, `implements`, `of`) are string IDs resolved by the Go loader into the resource graph. This drives both prompt scoping and change cascading.
- **Multi-file specs are natural.** Place CUE files in the same package directory. CUE's unification merges them — same-name fields unify, new fields add. Split a large project across files however you like: by bounded context, by layer, by team ownership. No import chains needed.

### 1.3 Multi-File Composition via CUE Unification

CUE files in the same package automatically merge. This is how you organize a large spec:

```
spec/
  project.cue       <- project config, layers, invariants
  kernel.cue         <- Kernel context: shared types (NoteId, Velocity, MidiEvent, etc.)
  synth.cue          <- Synth context: Voice aggregate, VoiceAllocator, oscillator/filter/envelope
  realtime.cue       <- RealTime context: lock-free boundary, parameter bridge
  shell.cue          <- Shell context: ports (AudioOutput, MidiInput, GamepadInput)
  adapters.cue       <- Infrastructure: CpalAudioOutput, MidiAdapter, etc.
  assets.cue         <- Generated artifacts: Cargo.toml, lib.rs, mod.rs files
```

The Go loader loads all `.cue` files in the spec directory. CUE unifies them into a single value:

- Each file contributes its resources to the graph.
- Same-name fields unify — a resource partially declared in one file can be extended in another.
- Constraints accumulate across files.
- No explicit import or re-export needed. CUE handles the merge.

### 1.4 DSL Objects

All resources live under a top-level `project` struct. The CUE schema defines the shape; spec files provide concrete values.

#### `project` — The Root

```cue
project: {
    name: "crest-synth"
    layers: ["domain", "application", "infrastructure"]
    layerRules: {
        domain:         {dependsOn: []}
        application:    {dependsOn: ["domain"]}
        infrastructure: {dependsOn: ["domain", "application"]}
    }
    meta: {
        language: "rust"
        style:    "idiomatic Rust; lock-free audio thread; gamepad-driven UI"
        avoid: [
            "heap allocation on audio thread",
            "mutex locks on audio thread",
            "blocking I/O on audio thread",
        ]
    }
}
```

| Field | Purpose |
|-------|---------|
| `name` | Project identifier |
| `layers` | Architectural layer names (used for dependency enforcement) |
| `layerRules` | Layer dependency rules — which layers may depend on which |
| `meta` | Project-wide metadata: `language`, `style`, `avoid`, `rules`, `framework`, etc. |

**Key behavior:** The `meta.language` field drives the entire prompt system — it determines the system prompt role, file extension hints, folder structure conventions, and output format.

#### `contexts` — Bounded Contexts

A bounded context groups related domain concepts. Each context is a keyed entry under `project.contexts`.

```cue
project: contexts: Synth: {
    purpose: "polyphonic synthesis engine: voice management, oscillator, filter, envelope"
    ubiquitousLanguage: {
        Voice:         "a single sounding note with its own oscillator, filter, and envelope state"
        VoiceStealing: "reusing the oldest or quietest voice when polyphony limit is reached"
        EnvelopeStage: "current phase of an ADSR envelope: attack, decay, sustain, release, idle"
    }
}
```

| Field | Purpose |
|-------|---------|
| `purpose` | One-line description of this context's responsibility |
| `ubiquitousLanguage` | Domain vocabulary — term-to-definition map. Injected into prompts so the LLM uses the right language. |

**ID format:** `context.{name}`

#### `aggregates` — Aggregate Roots

The central concept. An aggregate is a cluster of objects treated as a unit for consistency, with commands, events, state fields, and invariants.

```cue
project: contexts: Synth: aggregates: Voice: {
    root:    true
    purpose: "a single sounding note: oscillator + filter + amp envelope"
    state: {
        noteId:        "NoteId"
        noteNumber:    "NoteNumber"
        velocity:      "Velocity"
        frequency:     "Frequency"
        oscillatorPhase: "f64"
        envelopeStage: "EnvelopeStage"
        envelopeLevel: "Amplitude"
        active:        "bool"
    }
    commands: {
        NoteOn: {noteId: "NoteId", noteNumber: "NoteNumber", velocity: "Velocity"}
        NoteOff: {noteId: "NoteId"}
    }
    events: {
        VoiceActivated: {noteId: "NoteId", noteNumber: "NoteNumber", frequency: "Frequency"}
        VoiceReleased:  {noteId: "NoteId"}
        VoiceFinished:  {noteId: "NoteId"}
        VoiceStolen:    {oldNoteId: "NoteId", newNoteId: "NoteId"}
    }
    invariants: [
        "frequency derived from noteNumber and any pitch modulation",
        "envelope progresses Idle -> Attack -> Decay -> Sustain -> Release -> Idle",
        "voice is reclaimable only when envelope reaches Idle",
    ]
}
```

| Field | Purpose |
|-------|---------|
| `root` | Whether this is an aggregate root |
| `purpose` | What this aggregate represents in the domain |
| `state` | Field declarations as `{name: type}` — types are domain types, not language types |
| `commands` | Named command descriptors — intents to change state. Each key is the command name, value is the payload. |
| `events` | Named event descriptors — facts that happened. Each key is the event name, value is the payload. |
| `invariants` | Array of plain-text business rules the generated code must enforce |
| `implements` | Optional port ID string if this aggregate implements a port contract |

**ID format:** `aggregate.{context}.{name}`

#### `entities` — Entities (within an Aggregate)

An object with identity that lives inside an aggregate.

```cue
project: contexts: Modulation: aggregates: ModMatrix: entities: ModRouting: {
    state: {
        source:      "ModSourceType"
        destination: "ModDestinationType"
        depth:       "f64"
    }
}
```

| Field | Purpose |
|-------|---------|
| `state` | Field declarations as `{name: type}` |

**ID format:** `entity.{context}.{aggregate}.{name}`

#### `valueObjects` — Value Objects

An immutable type defined entirely by its attributes — no identity. Three forms: wrapping a single primitive (`from`), composing multiple fields (`state`), or enums.

```cue
// Wrapper form
project: contexts: Kernel: valueObjects: NoteId: {
    from:        "u32"
    description: "unique identifier for a sounding note, enabling per-note expression"
}

// Composite form
project: contexts: Kernel: valueObjects: MidiEvent: {
    state: {
        group:      "MidiGroup"
        channel:    "MidiChannel"
        noteId:     "NoteId"
        kind:       "MidiEventKind"
        noteNumber: "NoteNumber"
        velocity:   "Velocity"
        value:      "f64"
    }
    description: "normalized internal event: (group, channel) addressed, high-res values, note-id tagged"
}

// Enum form
project: contexts: Synth: valueObjects: EnvelopeStage: {
    from:        "enum"
    description: "ADSR envelope phase: Idle, Attack, Decay, Sustain, Release"
}
```

| Field | Purpose |
|-------|---------|
| `from` | Single-value wrapper type (e.g., `"u32"`, `"f64"`, `"string"`, `"enum"`) |
| `state` | Multi-field composite (mutually exclusive with `from`) |
| `description` | What this value represents |
| `invariants` | Value constraints (e.g., `"must be 0-127"`) |

CUE bonus: numeric invariants can be expressed as CUE constraints directly and validated at load time:

```cue
project: contexts: Kernel: valueObjects: NoteNumber: {
    from:        "u8"
    description: "MIDI note number (0-127)"
    invariants: ["must be 0-127"]
    // CUE constraint — validated at spec load, before any LLM call
    _rangeCheck: uint8 & >=0 & <=127
}
```

**ID format:** `valueObject.{context}.{name}`

#### `ports` — Ports (Interface Contracts)

A port declares an interface that the domain needs but does not implement — the "hole" in the hexagonal architecture. Adapters fill it.

```cue
project: contexts: Shell: ports: AudioOutput: {
    contract: {
        openStream:  "SampleRate -> AudioStream"
        writeBuffer: "[AudioFrame] -> ()"
    }
}
```

| Field | Purpose |
|-------|---------|
| `contract` | Method signatures as `{name: signature}` — signatures are descriptive strings, not executable |

**ID format:** `port.{context}.{name}`

#### `adapters` — Adapters (Port Implementations)

An infrastructure-layer concrete implementation of a port.

```cue
project: adapters: CpalAudioOutput: {
    implements: "port.Shell.AudioOutput"
    layer:      "infrastructure"
    meta: notes: "cpal: cross-platform audio output (ALSA/PipeWire on Linux, WASAPI, CoreAudio)"
}
```

| Field | Purpose |
|-------|---------|
| `implements` | Port ID string this adapter fulfills |
| `layer` | Architectural layer (defaults to `"infrastructure"`) |

**ID format:** `adapter.{name}`

#### `repositories` — Repositories

Collection-like access to an aggregate by identity.

```cue
project: contexts: Patch: repositories: PatchRepository: {
    of: "aggregate.Patch.Patch"
    contract: {
        findById:      "PatchId -> Option<Patch>"
        findByChannel: "ChannelAddress -> Vec<Patch>"
        save:          "Patch -> ()"
        listAll:       "() -> Vec<Patch>"
    }
}
```

| Field | Purpose |
|-------|---------|
| `of` | Aggregate ID string this repository stores (creates an `of` dependency) |
| `contract` | Method signatures |

**ID format:** `repository.{context}.{name}`

#### `domainServices` — Domain Services

Domain logic that doesn't belong to a single aggregate.

```cue
project: contexts: Synth: domainServices: VoiceAllocator: {
    purpose: "assigns incoming notes to voices, stealing the oldest/quietest when the pool is full"
    uses: ["aggregate.Synth.Voice"]
}
```

| Field | Purpose |
|-------|---------|
| `purpose` | What this service does |
| `uses` | Array of resource ID strings this service depends on |

**ID format:** `domainService.{context}.{name}`

#### `applicationServices` — Application Services

Orchestrates domain objects to fulfill use cases.

```cue
project: contexts: SampleLibrary: applicationServices: SampleLoader: {
    purpose: "decodes SF2/WAV files into SampleSets"
    uses: ["aggregate.SampleLibrary.SampleSet"]
    operations: {
        LoadSf2: {input: {path: "PathBuf"}}
        LoadWav: {input: {path: "PathBuf", rootNote: "NoteNumber"}}
    }
}
```

| Field | Purpose |
|-------|---------|
| `purpose` | What this service orchestrates |
| `uses` | Resource ID strings |
| `operations` | Named operations with input signatures |

**ID format:** `applicationService.{context}.{name}`

#### `assetKinds` — Asset Kinds (Templates)

Defines a category of generated artifact — a template with shared generation rules.

```cue
project: assetKinds: "cargo-manifest": {
    description: "Rust Cargo.toml project manifest"
    filePattern: "Cargo.toml"
    prompts: [
        "Use edition 2021",
        "Only include dependencies actually needed by the generated code",
        "Include [lib] section with path = \"src/lib.rs\"",
    ]
}

project: assetKinds: "rust-module-declaration": {
    description: "Rust mod.rs or lib.rs module declaration file"
    prompts: [
        "Only output module declarations (pub mod) and re-exports",
        "Do not add any implementation code",
    ]
}
```

| Field | Purpose |
|-------|---------|
| `description` | What this kind of asset is |
| `filePattern` | Expected file pattern (e.g., `"Cargo.toml"`, `"src/main.rs"`) |
| `prompts` | Generation rules inherited by all assets of this kind |
| `references` | Reference URLs/paths |

**ID format:** `assetKind.{name}`

#### `assets` — Assets (Generated Artifacts)

A concrete file to be generated, linked to an asset kind and optionally to specific resources. Assets can be declared at the project level, context level, or aggregate level.

```cue
// Project-level asset
project: assets: ToneTestMain: {
    kind:        "rust-binary"
    description: "src/main.rs tone test: exercises AudioRenderer to prove MIDI-to-sound path"
    prompts: [
        "File path: src/main.rs",
        "Import kernel types and Audio::AudioRenderer from the crate lib",
        "Create an AudioRenderer at 44100 Hz",
        "Play a 3-second C4-E4-G4 arpeggio",
        "Write output to tone-test.wav using a pure-Rust WAV writer",
    ]
}

// Context-level asset
project: contexts: Kernel: assets: KernelMod: {
    kind:        "rust-module-declaration"
    description: "src/kernel/mod.rs module declarations for Kernel context"
    prompts: [
        "File path: src/kernel/mod.rs",
        "Declare modules for: MidiGroup, MidiChannel, NoteId, NoteNumber, Velocity, MidiEvent, SampleRate, AudioFrame",
    ]
}
```

| Field | Purpose |
|-------|---------|
| `kind` | Name of the `assetKind` this asset uses (creates `uses` dependency on the kind) |
| `description` | What this specific asset contains |
| `prompts` | Asset-specific generation instructions |
| `targets` | Optional resource ID strings linking to specific resources |

**ID format:** `asset.{name}`, `asset.{context}.{name}`, or `asset.{context}.{aggregate}.{name}`

**Key behavior:** An asset's prompt is built by combining the asset kind's prompts with the asset's own prompts, plus any target resource declarations. This controls exactly what the LLM generates per file.

#### `validations` — Resource-Specific Validations

Declares verification steps that run after a resource is generated. Goes beyond the generic type-check and test commands — validations let each resource define its own acceptance criteria.

```cue
project: assets: CpalAudioOutputAdapter: {
    kind:        "rust-adapter"
    description: "CPAL adapter implementing AudioOutput port"
    prompts: [...]
    validations: [
        {
            kind: "compiles"
            command: ["cargo", "build"]
        },
        {
            kind: "test"
            command: ["cargo", "test", "--lib", "cpal_audio_output"]
            description: "unit tests for the cpal adapter pass"
        },
        {
            kind: "integration"
            command: ["cargo", "run", "--", "--wav"]
            description: "arpeggio renders to WAV without errors"
            assertions: [
                {kind: "exit_code", expected: 0},
                {kind: "file_exists", path: "tone-test.wav"},
                {kind: "stdout_contains", pattern: "Wrote tone-test.wav"},
            ]
        },
        {
            kind: "custom"
            command: ["python3", "scripts/check-wav-frequencies.py", "tone-test.wav"]
            description: "WAV contains all 3 expected frequencies at correct time offsets"
        },
    ]
}
```

**Validation kinds:**

| Kind | What it checks |
|------|----------------|
| `compiles` | Build command exits 0 |
| `test` | Test command exits 0 |
| `integration` | Run command + structured assertions on output |
| `custom` | Arbitrary script — exit 0 = pass, nonzero = fail |

**Assertions (for `integration` kind):**

| Assertion | Fields |
|-----------|--------|
| `exit_code` | `expected: number` |
| `file_exists` | `path: string` |
| `file_not_empty` | `path: string` |
| `stdout_contains` | `pattern: string` |
| `stderr_empty` | (none) |
| `file_matches` | `path, regex: string` |

**How validations fit the lifecycle:** After the constraint loop produces accepted output, the resource validator checks for `validations` declared on the resource and runs them in order. A validation failure feeds back into the sub-agent state machine — the resource transitions from `completed` to `rejected` with the validation output attached, triggering a re-dispatch with the failure details in the fix prompt.

#### `invariants` — Architectural Invariants

Project-wide rules enforced during planning and generation.

```cue
project: invariants: [
    {
        text: "audio thread must never allocate heap memory"
        meta: rationale: "any allocation risks missing the audio buffer deadline and causing a dropout"
    },
    {
        text: "audio thread must never acquire a mutex or blocking lock"
        meta: rationale: "lock contention causes unbounded latency on the real-time thread"
    },
]
```

Invariants are checked both structurally (at plan time) and against generated code (during the constraint loop). The `rationale` is injected into prompts so the LLM understands *why*.

Since CUE unifies all files in the package, invariants declared in any file apply to the entire spec — no need to redeclare them.

#### `contextMap` — Context Relationships

Declares strategic DDD relationships between bounded contexts.

```cue
project: contextMap: [
    {from: "Synth", to: "Kernel", kind: "shared-kernel"},
    {from: "Patch", to: "Synth", kind: "customer-supplier", direction: "downstream"},
    {from: "Plugin", to: "Synth", kind: "anti-corruption"},
]
```

**Relationship kinds:** `"customer-supplier"`, `"anti-corruption"`, `"shared-kernel"`, `"published-language"`

### 1.5 The Meta Object

Every resource supports an extensible `meta` struct that flows through the prompt system:

```cue
meta: {
    rules?:      [...string]  // Additional generation rules
    prompts?:    [...string]  // Injected into LLM prompts
    references?: [...string]  // Reference URLs/paths
    examples?:   [...string]  // Example code/usage
    avoid?:      [...string]  // Anti-patterns to avoid
    style?:      string       // Code style guidance
    notes?:      string       // Free-form notes
    rationale?:  string       // Why this exists
    framework?:  string       // Framework/library to use (e.g., "nih-plug", "actix-web")
    reviewLevel?: "full" | "light" | "solid" | "skip"  // Constraint loop review depth (see section 8.2)
    ...                       // Extensible
}
```

**`reviewLevel` values:**

| Value | Behavior |
|-------|----------|
| `"full"` | Multi-model code review via `engine.CodeReview`. Heavyweight — fans out across opus/sonnet. Default for aggregates, domain services, adapters. |
| `"light"` | Single-model severity-ranked scan via `engine.Bugbot`. Default for value objects, entities, assets. |
| `"solid"` | Single-model SOLID/DI/interface review via `engine.Review`. Explicit opt-in for any resource. |
| `"skip"` | No LLM review. For generated boilerplate (mod.rs, Cargo.toml, manifests). |

The `framework` field is injected into prompts to guide framework-specific code generation (e.g., a `plugin` context with `framework: "nih-plug"` tells the LLM to generate nih-plug compatible code).

Meta merges hierarchically via CUE unification: project meta -> context meta -> resource meta. List fields are merged with deduplication (duplicate values are removed); scalar fields from more-specific levels override less-specific ones.

### 1.6 Resource IDs and Dependencies

Every resource gets a hierarchical ID:

```
project.crest-synth
context.Kernel
aggregate.Synth.Voice
entity.Modulation.ModMatrix.ModRouting
valueObject.Kernel.NoteId
port.Shell.AudioOutput
adapter.CpalAudioOutput
domainService.Synth.VoiceAllocator
repository.Patch.PatchRepository
applicationService.SampleLibrary.SampleLoader
asset.ToneTestMain
assetKind.cargo-manifest
```

Dependencies are tracked as typed edges in the resource graph:

| Kind | Meaning |
|------|---------|
| `implements` | Adapter implements a port |
| `uses` | Service/asset depends on an aggregate/kind |
| `of` | Repository or entity belongs to an aggregate |
| `targets` | Asset targets a specific resource (links generated artifact to its source resource) |
| `consumes` | Resource consumes events from another (used for planning/diffing and sub-agent context) |
| `publishes` | Resource publishes events (used for planning/diffing and sub-agent context) |

In CUE, dependencies are expressed as ID strings (e.g., `uses: ["aggregate.Synth.Voice"]`). The Go loader resolves these into the resource graph at load time, validating that all referenced IDs exist.

---

## 2. Architecture

### 2.1 Component Map

The codebase has two layers: the **engine** (adapted from `claude-mcp`) and the **spec system** (crest-spec's domain). The engine handles subprocess management, async jobs, transports, and concurrency. The spec system handles CUE loading, planning, prompt construction, and the constraint loop. The spec system dispatches sub-agents through the engine.

```
cmd/crest-spec/main.go               Entrypoint + flag/help handling
  +- config.New()                     envconfig (CREST_SPEC_ prefix)
  +- store.New(dbPath)                SQLite store (WAL mode) — all state in one DB
  +- agent.New(...)                   claude CLI wrapper (from claude-mcp)
  +- engine.New(agent, store, ...)    wraps run_prompt / code_review / bugbot execution
  +- spec.New(engine, store, ...)     plan/apply/verify lifecycle
  +- mcp.New(...)                     MCP server (tools, dispatch, metrics)
       +- stdio transport             reads stdin, writes stdout (Server handles directly)
       +- HTTP transport              net/http server, POST /mcp (Server handles directly)
       +- app.New(srv).Run(ctx)       lifecycle wrapper (Run until ctx cancelled)

internal/
  ## Engine layer (adapted from claude-mcp)
  agent/        Wraps the claude binary via os/exec; config isolation, process groups, concurrency
  config/       envconfig-based configuration + usage/help text
  engine/       Orchestration primitives: run_prompt, code_review, bugbot as callable functions
  mcp/          JSON-RPC server, tool definitions, dispatch, async jobs, metrics, recursion guard

  ## Spec layer (crest-spec domain)
  cue/          CUE loader: multi-file unification, resource parsing
  graph/        Resource dependency graph: topological sort, wave computation, hash propagation
  plan/         Planner: diff registry against state, compute effective hashes, produce PlannedAction[]
  prompt/       Prompt builder: system prompt, resource prompt, fix prompt, context injection
  spec/         Spec engine: plan/apply lifecycle, wave execution, constraint loop, sub-agent state machine; includes validation (validate.go)

  ## Shared
  db/           sqlc-generated query code (DO NOT EDIT)
  errors/       Const error sentinel type (`type New string`)
  store/        SQLite store: jobs, resources, files, applies, generations, sessions, notes, lock
migrations/     SQL schema, embedded via go:embed; applied at store startup
sql/queries/    sqlc query definitions (source for internal/db)
```

### 2.2 How the Layers Connect

The spec layer never touches `os/exec` or the `claude` binary directly. It calls the engine:

- **Code generation**: `engine.Generate(ctx, prompt, model, opts)` — calls `runner.RunPrompt` with `--disallowedTools` to get pure code output. This is a constrained `run_prompt` invocation: no tool access, no session persistence.
- **LLM verification**: `engine.Review(ctx, code, requirements, opts)` — calls `runner.RunPrompt` with a review prompt. Can also use `engine.CodeReview` or `engine.Bugbot` for multi-model verification passes in the constraint loop.
- **Concurrency**: all subprocess spawns flow through the engine's shared `MaxConcurrency` semaphore — wave-parallel code generation and verification passes share the same pool.
- **Async jobs**: long-running spec operations (`spec/apply`) use the same async job model as claude-mcp — job ID returned immediately, background goroutine, SQLite-persisted state, progress notifications.

### 2.3 Dependency Injection / Interfaces

The codebase uses **package-private interfaces at the consumer** for testability:

- `engine.runner` — the surface of `agent.Agent` the engine depends on (`RunPrompt`, `Models`, `About`, `Status`). Faked by `mocks.FakeRunner`.
- `spec.engine` — the surface of `engine.Engine` the spec layer depends on (`Generate`, `Review`, `CodeReview`, `Bugbot`). Faked by `mocks.FakeEngine`.
- `mcp.processTree` — abstraction over process-tree walking (`ParentProcess`, `SelfPID`). Real impl is `mcp.OSProcessTree`; faked by `mocks.FakeProcessTree`.
- `app.server` — anything with `Run(ctx) error`.

Mocks are generated with counterfeiter (`//go:generate` directives) and committed under `internal/mocks/`.

### 2.4 Startup Sequence (`main.go`)

1. **Subcommand check** — if `os.Args` is `check job <id>`, run `checkJob()` and exit (see section 13.3).
2. **Help** — `-h`/`--help` prints usage + env var table (`config.Help()`), exits 0.
3. **Config** — `config.New()`; on error, print help and panic.
4. **Store** — `store.New(dbPath())` where `dbPath()` is `.crest-spec/state.db` in the project directory. `defer store.Close()`.
5. **Orphan cleanup** — `store.CleanupOrphans()` marks jobs whose owner PID is dead as `failed` (logged, non-fatal on error).
6. **Signal context** — `signal.NotifyContext(ctx, SIGINT, SIGTERM)`.
7. **Agent** — `agent.New(...)` from config.
8. **Engine** — `engine.New(agent, store, cfg)` — wraps the agent with concurrency control and the run_prompt/code_review/bugbot execution paths.
9. **Spec** — `spec.New(engine, store, cfg)` — the plan/apply lifecycle engine.
10. **Transport setup** — stdio always starts; HTTP starts if `HTTPAddr` is set:
    - Build `mcp.New(spec, engine, store, OSProcessTree{}, os.Stdin, os.Stdout, log, cfg)`.
    - If `cfg.HTTPAddr != ""`, start the Streamable HTTP transport on that address.
    - `app.New(srv).Run(ctx)`.
    - On exit: if `ctx.Err() != nil` it was a graceful shutdown (log info); otherwise panic with the error.

---

## 3. Configuration (`internal/config`)

All env vars use the `CREST_SPEC_` prefix via `envconfig.Process("CREST_SPEC", &cfg)`.

#### Engine config (adapted from claude-mcp)

| Field | Env var | Type | Default | Purpose |
|-------|---------|------|---------|---------|
| `APIKey` | `CREST_SPEC_API_KEY` | string | (none) | Passed to subprocess as `ANTHROPIC_API_KEY`. If unset, the child uses the developer's OAuth/keychain session. |
| `AgentPath` | `CREST_SPEC_AGENT_PATH` | string | `claude` | Path/name of the claude binary. |
| `DefaultModel` | `CREST_SPEC_DEFAULT_MODEL` | string | `claude-sonnet-4-6` | Model used when a call omits one. Accepts aliases (`opus`/`sonnet`) or full IDs. |
| `PermissionMode` | `CREST_SPEC_PERMISSION_MODE` | string | `default` | Default permission mode (maps to `claude --permission-mode`). |
| `Timeout` | `CREST_SPEC_TIMEOUT` | `time.Duration` | `0s` | Default per-`RunPrompt` timeout; `0s` = no timeout. |
| `MaxConcurrency` | `CREST_SPEC_MAX_CONCURRENCY` | int | `5` | Maximum concurrent `claude` subprocess spawns server-wide. Shared across code generation, verification, and any direct tool calls. |
| `HTTPAddr` | `CREST_SPEC_HTTP_ADDR` | string | (none) | Listen address for Streamable HTTP transport (e.g., `:8080`). If unset, only stdio is active. |

#### Spec config

| Field | Env var | Type | Default | Purpose |
|-------|---------|------|---------|---------|
| `GenerateModel` | `CREST_SPEC_GENERATE_MODEL` | string | `claude-sonnet-4-6` | Model used for code generation sub-agents. Overrides `DefaultModel` for generation dispatches. |
| `VerifyModel` | `CREST_SPEC_VERIFY_MODEL` | string | `claude-sonnet-4-6` | Model used for the LLM verification pass in the constraint loop. |
| `MaxRetries` | `CREST_SPEC_MAX_RETRIES` | int | `3` | Default retry count per resource in the constraint loop. |
| `WaveMaxRetries` | `CREST_SPEC_WAVE_MAX_RETRIES` | int | `2` | Retry count for wave-level verification failures. |
| `SpecDir` | `CREST_SPEC_SPEC_DIR` | string | `./spec` | Directory containing CUE spec files. |
| `TypeCheckCommand` | `CREST_SPEC_TYPE_CHECK_CMD` | string | (none) | Build/type-check command (e.g., `cargo check`). |
| `TestCommand` | `CREST_SPEC_TEST_CMD` | string | (none) | Test command (e.g., `cargo test`). |

`config.Help()` renders an aligned usage table to stderr using `tabwriter` + `envconfig.Usagef`.

A separate env var `CLAUDE_CONFIG_DIR` (read directly in `agent.New`) overrides the Claude Code config directory (default `~/.claude`); it governs where the child process reads/writes `.claude.json`, credentials, and session state.

---

## 4. The Agent Wrapper & Engine Layer

### 4.1 Agent (`internal/agent`) — Adapted from claude-mcp

The agent wrapper is taken directly from the working claude-mcp implementation. It wraps the `claude` CLI binary via `os/exec` and handles all the subprocess management that makes concurrent invocations reliable.

**`Agent` struct** holds `path`, `apiKey`, `defaultModel`, `permissionMode`, `timeout`, and `configDir`. Constructed by `New(path, apiKey, defaultModel, permissionMode, timeout)`.

**Data types:**
- `RunOpts` — Prompt, Model, Mode, Effort, Cwd, RelevantPaths, AddDirs, Continue, Resume, SessionID, Force, AllowedTools, DisallowedTools, AppendSystemPrompt.
- `RunResult` — Output, Stderr, Model, SessionID, DurationMS, NumTurns, CostUSD, IsError, `*Usage`.
- `Usage` — InputTokens, OutputTokens, CacheReadTokens, CacheCreationTokens.

**`RunPrompt`** builds the `claude` argv:
- Always: `--print --output-format json`.
- `--model`, `--permission-mode`, `--effort`, `--add-dir`, `--continue`, `--resume`, `--session-id`, `--dangerously-skip-permissions`, `--allowedTools`, `--disallowedTools`, `--append-system-prompt`, `--no-session-persistence` — all supported, matching the claude-mcp implementation.
- Prompts <= 8 KB passed as positional arg; larger prompts piped via stdin.

**Subprocess robustness** (proven in claude-mcp):
- **Config isolation** — when an API key is configured (`CREST_SPEC_API_KEY`), each invocation gets a fresh temp config dir mirroring `~/.claude`. `.claude.json` is copied (writable per-process); credentials are hard-linked; subdirectories are symlinked. Prevents concurrent processes from racing. When no API key is set, config isolation is not activated — there is nothing to isolate since the child uses the developer's existing session.
- **Process groups** — `SysProcAttr{Setpgid: true}`; `cmd.Cancel` sends `SIGKILL` to the whole group (`syscall.Kill(-pid, SIGKILL)`); `cmd.WaitDelay = 5s`.
- **Partial results** — on error, parses whatever stdout was produced, attaches Stderr, returns `(partialResult, wrappedError)`.
- **`is_error` detection** — a JSON envelope with `is_error: true` is surfaced as an error even with exit code 0.

**Read-only commands:** `Models`, `About`, `Status`, `MCPServers`, `MCPTools` — same as claude-mcp.

### 4.2 Engine (`internal/engine`) — Sub-Agent Dispatch for crest-spec

The engine wraps the agent with higher-level operations that the spec layer calls. It owns the concurrency semaphore and provides the execution paths adapted from claude-mcp's async exec funcs.

**`Engine` struct** holds a `runner` (agent interface), the store, config, and the `chan struct{}` concurrency semaphore (size `MaxConcurrency`, default 5).

**Operations:**

- **`Generate(ctx, prompt, systemPrompt, model, GenerateOpts) (*RunResult, error)`** — the primary code generation path. Calls `runner.RunPrompt` with:
  - `--disallowedTools Bash Read Edit Write Glob Grep WebFetch WebSearch` — no tool access, pure code output
  - `--no-session-persistence` — stateless
  - `--append-system-prompt` with the crest-spec system prompt
  - Model defaults to `GenerateModel` config
  - Acquires a concurrency slot before spawning, releases on exit

- **`Review(ctx, code, requirements, model) (*RunResult, error)`** — LLM verification pass. Calls `runner.RunPrompt` with a generic review prompt. SOLID/DI/clean code checks are not hardcoded in the review template — they come from the user's meta rules (e.g., `meta.rules` in the CUE spec). Parses for `PASS`/`FAIL` verdict. Uses `VerifyModel` config.

- **`CodeReview(ctx, cwd, models, prompt, CodeReviewOpts) (string, error)`** — multi-model code review of generated files. Adapted from claude-mcp's `execCodeReview`: fans out across models (default `[opus, sonnet]`), each checking for architecture issues, nil derefs, bounds errors, performance, leaks. Results aggregated per model. Useful as a heavyweight verification step for critical resources.

- **`Bugbot(ctx, cwd, models, prompt, BugbotOpts) (string, error)`** — lightweight severity-ranked scan. Adapted from claude-mcp's `execBugbot`: defaults to `[sonnet]`, demands per-finding severity + remedy. Useful as a fast sanity check in the constraint loop.

All operations acquire a slot from the shared concurrency semaphore. The spec layer never needs to think about subprocess limits — the engine enforces them.

---

## 5. The Terraform-Inspired Lifecycle

### 5.1 Plan — `spec/plan`

**What it does:**
1. Loads all CUE spec files from the spec directory (CUE evaluator unifies them into a single value)
2. Parses the unified CUE value into the in-memory resource registry
3. Opens the SQLite state database
4. Computes "effective hashes" for every resource — a SHA256 that includes the resource's declaration, meta, invariants, all dependency hashes (transitively), and the model identifier
5. Compares each resource's effective hash against the stored hash in state
6. Returns a diff-style plan:

```
+ aggregate.Synth.Voice
  reason: new resource

~ valueObject.Kernel.NoteId
  reason: declaration changed

~ domainService.Synth.VoiceAllocator
  reason: dependency changed (aggregate.Synth.Voice)
  cascaded from: aggregate.Synth.Voice
  files: src/Synth/VoiceAllocator/VoiceAllocator.rs

- aggregate.Audio.SineVoice
  reason: removed from spec
  files: src/Audio/SineVoice/SineVoice.rs
```

**Key behavior:** Change cascading. If `aggregate.Synth.Voice` changes, every resource that `uses` it gets a cascading `modify` action. This is driven by the hash computation — a resource's effective hash includes its dependencies' hashes, so any upstream change ripples downstream.

**Hand-edits are not tracked.** The spec controls *what resources exist and how they are shaped* — once a file is generated, the disk is the source of truth for its *content*. Formatting, minor fixes, and manual patches to generated files are ignored by the planner (there is no content-drift detection; see `docs/drop-drift-detection.md`). Regeneration has exactly two triggers:

1. **Edit the spec** — a declaration or dependency change produces an `ActionModify` via the hash cascade.
2. **Delete the file(s)** — a missing generated file produces an `ActionModify` with reason `"generated file missing — regenerating"`:

```
~ adapter.CpalAudioOutput
  reason: generated file missing — regenerating
  files: src/Shell/CpalAudioOutput/CpalAudioOutput.rs
```

**Structural kinds** (`project`, `context`, `assetKind`) are skipped during planning — they are metadata containers, not generated resources.

### 5.2 Apply — `spec/apply`

**What it does:**
1. Runs the planner to build the execution plan
2. Acquires an exclusive lock in SQLite (prevents concurrent applies)
3. Executes the plan in two phases:

   **Phase A — Destroys:** Deletes files and state for removed resources.

   **Phase B — Generates:** For each `create` or `modify` action:
   - Builds a full prompt (system prompt + resource prompt + context layers)
   - Dispatches to a sub-agent via `engine.Generate`
   - Runs the Constraint Loop (parse -> verify -> retry)
   - Writes files to disk (per-file content hash check: files whose SHA256 matches what's already on disk are skipped — no write, no timestamp change, no file watcher trigger)
   - Records everything in SQLite

4. Releases the lock

**Two execution modes:**

| Mode | Behavior |
|------|----------|
| **Flat** (default) | Each resource goes through the constraint loop independently |
| **Wave-based** (incremental) | Resources are grouped into dependency waves; verification runs between waves |

### 5.3 Targeting and Forcing

**Target:** Filters the plan to only act on a specific resource and its cascading dependents. Useful for iterating on a single resource without re-generating the whole project.

**Force:** Bypasses the hash-based skip logic to force regeneration even when the spec hasn't changed.

---

## 6. Prompt Construction

The prompt system builds layered, surgically scoped prompts for each resource. There are two parts: the system prompt (shared across all resources in a project) and the resource prompt (unique per resource).

### 6.1 System Prompt

Built from project-level meta. Contains:

1. **Role definition**: `"You are a {language} code generator following strict SOLID principles."`
2. **Output format**: Fenced code blocks with path annotations (`// path: src/Context/Resource.ext`)
3. **Folder structure**: `src/{ContextName}/{ResourceName}/` — grouped by resource, not by architectural layer
4. **SOLID principles**: Mandatory rules for dependency injection, SRP, DIP, ISP, OCP
5. **Language-specific rules**: For Rust: module tree casing, `use` path conventions, avoid unstable APIs
6. **Code style**: From `meta.style`
7. **Avoid list**: From `meta.avoid` — anti-patterns the generated code must not exhibit
8. **Output requirement**: Both implementation files and unit tests

### 6.2 Resource Prompt

Built per resource. Structure depends on resource kind:

**For domain resources (aggregates, services, etc.):**
- Resource kind, name, ID, context, layer
- Full declaration as JSON (serialized from the CUE value)
- Commands with payloads
- Events with payloads
- Invariants
- Port contracts (if the resource implements a port)
- Used dependencies with their declarations

**For assets:**
- Asset kind definition (description, file pattern, kind-level prompts)
- Asset-specific description and prompts
- Target resource declarations (if the asset references specific resources)

### 6.3 Runtime Context Injection

At apply time, the prompt is augmented with runtime context:

1. **Module tree context**: Scans the existing `src/` directory tree and injects the full module structure so the LLM uses correct `use` paths and casing — crucial for Rust where module paths must match directory structure exactly.

2. **Existing files from dependencies**: If the resource depends on other resources that were already generated in this apply, their file contents are injected so the LLM can import from them correctly. Test files are excluded.

3. **Wave error context**: If the resource failed a wave-level verification (type check or test), the error output is appended with the instruction: *"The previous generation caused build errors. Fix these errors in your output."*

4. **Agent notes from dependencies**: When a sub-agent generates a resource, it can leave "notes" (design decisions, implementation choices). These notes are injected into downstream resources' prompts as `## Notes from dependencies`.

### 6.4 The Fix Prompt

When the constraint loop retries, it builds a "fix prompt" that includes:
1. The original resource prompt
2. The previous attempt's output (all generated files)
3. The specific error to fix

This gives the LLM full context on what it produced and exactly what went wrong.

---

## 7. SQLite as the Integration Layer

SQLite is the single source of truth for all system state. One database (`.crest-spec/state.db` in the project directory) holds everything: async job tracking, spec state, generation audit trail, and sub-agent coordination.

- SQLite via `modernc.org/sqlite` (pure-Go, CGO-free).
- PRAGMAs at open: `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON` (enables cascade behavior for foreign key constraints).
- Migrations: SQL files embedded via `migrations.FS` (go:embed), applied in filename order, tracked in a `schema_migrations(filename)` table; each applied transactionally.
- Queries are sqlc-generated into `internal/db/` (do not hand-edit); query source is `sql/queries/*.sql`.

### 7.1 Jobs (Async Tool Lifecycle)

Every long-running tool call (plan, apply, generate) returns a job ID immediately. The work proceeds in a background goroutine, and the result is collected later via `poll_result` or SSE streaming.

**`jobs` table:**

| Column | Type | Notes |
|--------|------|-------|
| `id` | TEXT PK | UUID |
| `tool` | TEXT | originating tool name |
| `status` | TEXT | `running` / `completed` / `failed` / `cancelled` / `deleted` (default `running`) |
| `result` | TEXT | populated on completion |
| `error` | TEXT | populated on failure |
| `pid` | INTEGER | owner process PID |
| `started_at` | TEXT | RFC3339Nano UTC |
| `done_at` | TEXT | RFC3339Nano UTC |

State transitions are guarded by `AND status = 'running'` clauses so terminal states are immutable.

**Orphan detection:** `processAlive(pid)` uses `syscall.Kill(pid, 0)`. `CleanupOrphans` (run at startup) finds distinct PIDs of `running` jobs and marks any whose owner process is dead as `failed`. `WaitForCompletion(ctx, id)` polls with exponential backoff (100ms initial, doubling, capped at 2s).

### 7.2 State Tracking (What exists)

**`resources` table:** Tracks every resource that has been successfully generated.
- `kind`: Resource kind (e.g., `aggregate`, `valueObject`, `asset`)
- `context_name`: Bounded context this resource belongs to (if applicable)
- `declaration_hash`: Hash of the resource's declaration (detects spec changes)
- `effective_hash`: Hash including all transitive dependencies (detects cascading changes)
- `model`: Which LLM model last generated this resource
- `settled_at`: When this resource was last successfully generated

**`generated_files` table:** Maps file paths to the resources that generated them.
- `content_hash`: SHA256 of file content
- `prompt_hash`: Hash of the prompt that produced this file
- `model`: Which LLM model generated it

**`dependencies` table:** Stores the resource dependency graph for change detection.

### 7.3 Audit Trail (What happened)

**`applies` table:** Records every plan execution with status, timestamps, and spec hash.

**`apply_actions` table:** What action was taken per resource per apply. The action column is free-text (the CHECK constraint limiting it to `create/modify/destroy` was removed in migration 006). Current values used in practice include `create`, `modify`, and `destroy`. (Historical rows may contain `drift` from before content-drift detection was removed.)

**`generations` table:** Full audit of every LLM invocation:
- Complete prompt text and output text
- Model ID, prompt hash
- Retry count and outcome (accepted/rejected)
- Rejection reason (if failed)
- Duration, token usage, cost

**`invariant_checks` table:** Records every invariant check result per apply.

### 7.4 Agent Communication (Who knows what)

**`agent_sessions` table:** Stores the active agent orchestration session:
- Serialized plan, wave groupings, and effective hashes
- Enables the orchestrator to resume, query, and advance through the plan

**`agent_notes` table:** Notes left by sub-agents during generation:
- Keyed by `(resource_id, apply_id)`
- Injected into downstream resources' prompts so later agents can learn from earlier ones' decisions

**`lock` table:** Single-row table (id=1) for exclusive apply lock:
- Prevents concurrent applies from corrupting state
- Records holder and acquisition time

### 7.5 Store API

Single `Store` struct exposes all operations:

**Job operations:** `CreateJob`, `CompleteJob`, `FailJob`, `CancelJob`, `GetJob`, `ListJobs`, `DeleteJob`, `CleanupOrphans`, `WaitForCompletion`.

**State operations:** `GetResource`, `SetResource`, `ListResources`, `DeleteResource`, `SetGeneratedFile`, `GetGeneratedFiles`, `SetDependency`.

**Apply operations:** `CreateApply`, `CompleteApply`, `RecordAction`, `RecordGeneration`, `RecordInvariantCheck`.

**Session operations:** `CreateSession`, `GetSession`, `UpdateSession`, `GetNote`, `SetNote`, `ListNotes`, `AcquireLock`, `ReleaseLock`, `GetLock`.

`Close` shuts down the database connection.

---

## 8. The Plan/Apply/Dispatch/Retry Loop

### 8.1 High-Level Flow

```
+-------------------------------------------------------------+
|                    Spec Files (.cue)                        |
|  project: contexts: Synth: aggregates: Voice: {...}        |
+-------------+-----------------------------------------------+
              | CUE load + unify
              v
+-------------------------------------------------------------+
|              Resource Registry (in-memory graph)            |
+-------------+-----------------------------------------------+
              | diff against
              v
+-------------------------------------------------------------+
|             SQLite State Database                           |
|  effective_hash comparison -> PlannedAction[]               |
+-------------+-----------------------------------------------+
              | plan
              v
+-------------------------------------------------------------+
|                    Plan                                     |
|  + aggregate.Synth.Voice (new)                             |
|  ~ domainService.Synth.Allocator (dependency changed)      |
|  - aggregate.Audio.SineVoice (removed)                     |
+-------------+-----------------------------------------------+
              | apply (flat or wave-based)
              v
+-------------------------------------------------------------+
|              Apply Engine                                   |
|  For each planned action:                                  |
|    1. Build prompt (system + resource + context)            |
|    2. Dispatch to sub-agent via engine.Generate             |
|    3. Run Constraint Loop                                  |
|    4. Write files / update state                           |
+-------------+-----------------------------------------------+
              |
              v
+-------------------------------------------------------------+
|            Constraint Loop (per resource)                   |
|                                                             |
|  +---> Generate (engine.Generate -> claude subprocess)     |
|  |      prompt from: assetKind.prompts + asset.prompts     |
|  |                   + resource declaration + dependencies  |
|  |        |                                                 |
|  |        v                                                 |
|  |    Parse: extract code blocks (assetKind.filePattern)    |
|  |        |                                                 |
|  |        v                                                 |
|  |    Resource Validations (from resource `validations`):   |
|  |      compiles    — build command                         |
|  |      test        — test command                          |
|  |      integration — run + structured assertions           |
|  |      custom      — arbitrary script                      |
|  |        |                                                 |
|  |        v                                                 |
|  |    Invariant Check (from project `invariants`)           |
|  |      each invariant.text checked against generated code  |
|  |      invariant.meta.rationale injected on failure        |
|  |        |                                                 |
|  |        v                                                 |
|  |    Code Review (engine.CodeReview / engine.Bugbot)       |
|  |      review level from assetKind or resource meta        |
|  |        |                                                 |
|  |        v                                                 |
|  |    Pass? --yes--> Return files                           |
|  |        |                                                 |
|  |       no                                                 |
|  |        |                                                 |
|  +--- Build fix prompt (original + previous output + error) |
|      Retry up to maxRetries (config, default 3)            |
+-------------------------------------------------------------+
```

### 8.2 The Constraint Loop in Detail

The constraint loop is the core verification engine. Each step is driven by DSL objects declared in the CUE spec — the loop reads the resource's declarations, its `assetKind`, its `validations`, and the project's `invariants` to decide what to verify.

For each resource, the loop executes these steps in order:

1. **Generate**: Dispatches via `engine.Generate`. The prompt is assembled from:
   - The resource's `assetKind.prompts` (generation rules inherited by all assets of this kind)
   - The resource's own `prompts` field (asset-specific instructions)
   - The resource declaration as JSON (state fields, commands, events, port contracts)
   - Dependency declarations (from `uses`, `implements`, `of` references)
   - Runtime context: module tree, existing dependency files, agent notes from upstream resources
   
   The sub-agent has no tool access — it returns pure code blocks.

2. **Parse**: Extracts fenced code blocks with `// path:` or `# path:` annotations. Expected file patterns come from `assetKind.filePattern` when available. If no parseable blocks -> retry.

3. **Resource Validations**: Runs the `validations` declared on the resource (see section 1.4), in order:
   - **`compiles`** — runs the declared build command (e.g., `["cargo", "build"]`). Fails -> retry with compiler errors.
   - **`test`** — runs the declared test command (e.g., `["cargo", "test", "--lib", "cpal_audio_output"]`). Fails -> retry with test output.
   - **`integration`** — runs a command and checks structured `assertions`: `exit_code`, `file_exists`, `file_not_empty`, `stdout_contains`, `stderr_empty`, `file_matches`. Any assertion failure -> retry with the specific assertion that failed and the actual output.
   - **`custom`** — runs an arbitrary script. Exit 0 = pass, nonzero = fail with stderr attached.

   If a resource declares no `validations`, this step falls back to the global `TypeCheckCommand` and `TestCommand` from config (if configured).

4. **Invariant Check**: Checks generated code against `project.invariants` from the CUE spec. Each invariant has a `text` (the rule) and `meta.rationale` (why it exists). On failure, both are injected into the retry prompt so the LLM understands not just *what* it violated but *why* the rule exists. Example: invariant `"audio thread must never allocate heap memory"` with rationale `"any allocation risks missing the audio buffer deadline"` — the LLM gets both.

5. **Code Review**: Runs a multi-model review via the engine. Which review to run is determined by the resource's `meta.reviewLevel` field, or defaults based on resource kind:

   | Review level | Engine call | Default for | What it checks |
   |-------------|-------------|-------------|----------------|
   | `"full"` | `engine.CodeReview` | aggregates, domain services, adapters | Fans out across models (opus, sonnet). Architecture, nil derefs, bounds, performance, leaks, concurrency. |
   | `"light"` | `engine.Bugbot` | value objects, entities, assets | Single model (sonnet). Severity-ranked findings: High/Medium/Low with remedies. |
   | `"solid"` | `engine.Review` | any (explicit opt-in) | Single model. Checks SOLID principles, DI, interfaces, folder structure against the resource's declared commands/events/invariants/contracts. |
   | `"skip"` | (none) | module declarations, manifests | No LLM review. Useful for generated boilerplate (mod.rs, Cargo.toml). |

   A `FAIL` verdict -> retry with the review findings (severity, description, suggested remedy) in the fix prompt.

**On retry**, the fix prompt includes:
- The original resource prompt (full requirements from the DSL)
- The previous attempt's output (all generated files)
- The specific failure: validation output, invariant violation with rationale, or code review findings with severity and remedy
- Any user guidance from `spec/resolve` (injected as `## User Guidance`)

The `maxRetries` config (default 3) controls how many automatic attempts the loop makes. After exhaustion, the resource transitions to `errored` in the sub-agent state machine (see section 8.5) and the user is prompted for resolution:

- **`spec/resolve`** — user provides guidance (e.g., "the test expects the module at `src/synth/voice.rs` not `src/Synth/Voice.rs`"), injected into the next attempt's prompt, and the resource is re-dispatched.
- **`spec/amend`** — user fixes the root cause in the CUE spec (e.g., adds a missing dependency, relaxes an invariant, corrects a validation command). The spec is re-loaded, hashes re-computed, and the resource re-dispatched with the updated declaration. This can also cascade: if the amendment changes a dependency, all downstream resources in the plan are re-planned.
- **`spec/skip`** — user skips the resource entirely. The wave proceeds without it.

A validation failure after the constraint loop passes (i.e., the resource reached `completed` but resource-specific `validations` then fail) follows the same resolution paths. The resource transitions to `rejected` and the user can resolve, amend the validation, or skip.

### 8.3 Wave-Based Execution (Incremental Mode)

Resources are organized into dependency waves:

```
Wave 0: [valueObject.Kernel.NoteId, valueObject.Kernel.Velocity, ...]
Wave 1: [aggregate.Synth.Voice, port.Shell.AudioOutput, ...]
Wave 2: [domainService.Synth.VoiceAllocator, adapter.CpalAudioOutput, ...]
Wave 3: [asset.ToneTestMain, ...]
```

**Algorithm:** Each resource is assigned to `wave = max(dependency_waves) + 1`. Resources with no dependencies go in wave 0.

**Execution:**
1. Generate all resources in wave 0 (with concurrency, bounded by `MaxConcurrency` semaphore)
2. Run verification (type check + tests) against the whole project
3. If verification fails:
   - Attribute errors to specific resources using file path matching
   - Re-dispatch only the failed resources with error context
   - Retry up to `waveMaxRetries` times
4. If all pass, proceed to wave 1
5. Repeat until all waves complete

**Error attribution:** The verifier parses compiler/test output, looks for file paths in error messages, and maps them back to resources using the file-to-resource map. It uses a sliding window of +/-10 lines to find file references near error lines.

### 8.4 Post-Wave Orchestrator Loop

After dispatching all sub-agents in a wave, the orchestrator enters a poll-based check loop instead of assuming everything succeeded:

```
dispatch wave N
  |
  +-- poll states from SQLite ----------------------+
  |                                                 |
  |   all completed/committed?  --> advance to N+1  |
  |   any blocked?              --> report to user  |
  |   any errored (retryable)?  --> re-dispatch     |
  |   any errored (exhausted)?  --> report to user  |
  |   any timed_out?            --> report to user  |
  |   still dispatched?         --> wait + re-poll  |
  |                                                 |
  +-------------------------------------------------+
```

The orchestrator **reports back to the user** for any non-happy-path state instead of silently skipping or failing at `finish`. The user gets a summary like:

```
Wave 2: 5 resources
  completed: VoiceAllocator, AudioRenderer, SynthEngine
  blocked:   CpalAudioOutput -- "need user decision: try_send vs send for backpressure"
  errored:   FilterState -- compile error (attempt 2/3, retrying)
```

The user can then:
- **`spec/resolve`** — answer the question or provide guidance, re-dispatches the resource
- **`spec/amend`** — fix the CUE spec (add missing fields, relax invariants, correct validations), re-loads and re-dispatches
- **`spec/skip`** — skip the resource and continue
- Abort the session via `spec/finish` (with `force: true`)

This loop runs per wave — wave N+1 does not begin until every resource in wave N has reached `committed`, `rejected` (with retries exhausted), or been explicitly skipped by the user.

### 8.5 Sub-Agent State Machine

Each resource in a wave is tracked through a state machine in SQLite.

**States:**

```
pending --> dispatched --> completed --> committed
                |                |
                +-> blocked      |
                +-> errored      +-> rejected
                +-> timed_out

Resolution (from any non-terminal failure state):
  blocked   --[spec/resolve]--> dispatched  (re-dispatch with user's answer)
  blocked   --[spec/amend]---> dispatched   (update spec, re-plan, re-dispatch)
  blocked   --[spec/skip]----> skipped
  errored   --[spec/resolve]--> dispatched  (re-dispatch with guidance)
  errored   --[spec/amend]---> dispatched   (fix spec, re-dispatch)
  errored   --[spec/skip]----> skipped
  timed_out --[spec/resolve]--> dispatched  (re-dispatch with longer timeout)
  timed_out --[spec/skip]----> skipped
  rejected  --[spec/resolve]--> dispatched  (re-dispatch with fix guidance)
  rejected  --[spec/amend]---> dispatched   (fix spec, re-dispatch)
  rejected  --[spec/skip]----> skipped
  skipped   (terminal — wave proceeds without this resource)
```

| State | Meaning |
|-------|---------|
| `pending` | In the plan, not yet dispatched to a sub-agent |
| `dispatched` | Sub-agent has been spawned (claude subprocess running) |
| `completed` | Sub-agent returned output; files written but not yet committed |
| `committed` | Files validated and recorded in state DB |
| `blocked` | Sub-agent needs a decision from the user or another resource |
| `errored` | Sub-agent failed (compile error, invariant violation, crash) |
| `timed_out` | Sub-agent didn't respond within deadline |
| `rejected` | Validation failed after completion; needs re-dispatch |
| `skipped` | User explicitly skipped this resource; wave proceeds without it |

#### Blocked Context

```cue
#BlockedContext: {
    resourceId: string
    reason:     string           // human-readable summary
    blockedOn?: string           // another resourceId, if dependency-blocked
    question?:  string           // question for the user, if decision-blocked
    options?: [...string]        // suggested choices (if applicable)
    retryable:  bool             // can the orchestrator retry automatically?
}
```

**Blocked reasons** (non-exhaustive):
- **Decision needed**: the sub-agent encountered an ambiguity in the spec that it can't resolve. Example: `"Should ParameterBridge use try_send (non-blocking, drops on full) or send (blocks until space)?"`.
- **Dependency blocked**: a resource this one depends on is itself blocked/errored/timed_out.
- **Conflicting invariants**: two invariants contradict each other for this resource's domain. Example: `"Invariant 'no heap allocation on audio thread' conflicts with 'use Vec<Voice> for dynamic polyphony'"`.
- **Missing context**: the spec doesn't declare enough information. Example: `"Voice.state includes 'filterCutoff: Frequency' but no filter parameters are declared on the aggregate"`.

#### Errored Context

```cue
#ErrorContext: {
    resourceId:  string
    errorKind:   "compile" | "invariant" | "runtime" | "parse" | "review" | "validation" | "unknown"
    message:     string
    files?: [...string]          // which generated files were involved
    retryCount:  int             // how many times we've retried
    maxRetries:  int             // give up after this many
    lastAttempt?: string         // the generated code from the last attempt
    suggestion?: string          // LLM's suggestion for what might fix it (from review findings)
}
```

#### State Transitions

The orchestrator polls resource states after dispatching a wave (see section 8.4). Automatic transitions:

- `completed` -> runs `validations` -> all pass -> `committed`
- `completed` -> runs `validations` -> any fail -> `rejected` (with validation output attached)
- `errored` with `retryCount < maxRetries` -> automatic re-dispatch with error in fix prompt
- `errored` with `retryCount >= maxRetries` -> stays `errored`, reported to user

All non-terminal failure states (`blocked`, `timed_out`, exhausted `errored`, `rejected`) are reported to the user for resolution.

### 8.6 User Resolution & Dynamic Spec Updates

When the orchestrator encounters a non-happy-path state, it surfaces the issue to the user (the orchestrating AI agent or the human behind it). The user has three resolution paths:

#### Path 1: Resolve — Provide Guidance (`spec/resolve`)

The user answers a question or provides guidance, and the resource is re-dispatched with the answer injected into the prompt.

```json
{
  "resourceId": "adapter.CpalAudioOutput",
  "answer": "Use try_send with a ring buffer. Drop samples on overflow and log a warning — backpressure on the audio thread is worse than a dropped frame.",
  "retryWithModel": "claude-opus-4-8"
}
```

The answer is appended to the resource prompt as a `## User Guidance` section and persisted in `agent_notes` so downstream resources can also see it. Optionally, a different/stronger model can be specified for the retry.

This works for:
- **Blocked (decision needed)**: user answers the question
- **Errored (retries exhausted)**: user provides a hint about what the LLM is getting wrong
- **Rejected (validation failed)**: user explains what the validation expects
- **Timed out**: user re-dispatches (possibly with a longer timeout)

#### Path 2: Amend — Update the Spec (`spec/amend`)

The user (or orchestrating agent) modifies the CUE spec to fix the root cause, and the resource is re-planned and re-dispatched with the updated declaration.

```json
{
  "resourceId": "aggregate.Synth.Voice",
  "amendments": [
    {
      "file": "spec/synth.cue",
      "description": "Add filter parameters to Voice aggregate state"
    }
  ]
}
```

**What `spec/amend` does:**
1. Waits for the user/agent to write the CUE file changes (it does not write them itself — the orchestrating agent uses its own file-editing tools).
2. Re-loads the CUE spec and validates it (catches syntax errors, constraint violations, missing dependency references).
3. Re-computes effective hashes for the amended resource and its dependents.
4. Updates the in-flight plan: the amended resource is re-planned (may change from `modify` to `create` or vice versa), and any cascading dependents are added to the plan if they weren't already.
5. Re-dispatches the resource with the updated prompt (built from the new declaration).
6. Records the amendment in the `applies` audit trail.

This is the key feedback loop: **a generation failure can fix the spec, not just the generated code**. Examples:
- Sub-agent can't generate a valid Voice aggregate because `filterCutoff` is in `state` but no filter parameters exist -> amend the spec to add filter parameters.
- Invariant `"no heap allocation"` causes repeated failures for a resource that genuinely needs a `Vec` -> amend the invariant to add an exception: `"no heap allocation on audio thread, except for voice pool initialization at startup"`.
- A `validation` command is wrong (e.g., tests a module name that doesn't match the generated structure) -> amend the validation.
- Missing `uses` dependency -> add it to the spec so the dependency's code is injected into the prompt.

#### Path 3: Skip — Move On (`spec/skip`)

The user explicitly skips the resource. It transitions to `skipped` (terminal) and the wave proceeds without it. Downstream resources that depend on it will either:
- Also be skipped (if the dependency is required)
- Proceed without it (if the dependency is optional / the code already exists from a prior apply)

```json
{
  "resourceId": "adapter.CpalAudioOutput",
  "reason": "Will implement manually after the apply completes"
}
```

The skip reason is recorded in the audit trail and in `agent_notes` so downstream resources are aware.

#### Resolution in Automated vs. Interactive Mode

| Mode | Behavior on non-happy-path |
|------|---------------------------|
| **`spec/apply` (automated)** | Automatic retries up to `maxRetries`. On exhaustion, the apply pauses and emits a progress notification with the blocked/errored resources and their context. The orchestrating agent can then call `spec/resolve`, `spec/amend`, or `spec/skip` to unblock, and the apply resumes. If no resolution comes, the apply eventually times out and reports a summary. |
| **`spec/begin` (interactive)** | The orchestrating agent sees the state directly via `spec/next` and chooses how to handle each resource. It has full control — it can read the error, inspect the generated files, edit the spec, and call the appropriate resolution tool. |

### 8.6 Concurrency Model

All engine operations (`Generate`, `Review`, `CodeReview`, `Bugbot`) acquire a slot from the engine's shared `chan struct{}` semaphore of size `MaxConcurrency` (default 5) before spawning any subprocess, and release it on exit. This single pool governs all `claude` subprocess spawns — code generation and verification share the same limit.

Within a wave, resources are dispatched concurrently (up to `MaxConcurrency`). Across waves, execution is sequential — wave N+1 does not begin until wave N is fully resolved.

---

## 8A. Amendments

Section 8.6 covers *in-flight* resolution: a resource fails mid-apply and the orchestrator unblocks it with `spec/resolve` / `spec/amend` / `spec/skip`. **Amendments** address a different problem — correcting a resource that already generated cleanly but whose committed output is wrong in some durable way (for example, a SOLID violation surfaced by a post-hoc `spec/deep_review`).

An amendment is a **resource-scoped, spec-resident correction**: a targeted change request written onto the resource's declaration so it becomes a durable, incrementally-applied modification that flows through the normal generation loop. Instead of an advisory review finding that evaporates, the finding is distilled into a small instruction that lives in the spec, re-applies deterministically, and can be verified and eventually retired.

### 8A.1 Where Amendments Live

Amendments are authored on the shared `meta.amendments` list, available on any resource (via the `Meta` struct). They are **not inherited by child resources** — an amendment on an aggregate does not flow down to its entities.

> **Authoring constraint — tool-managed, not hand-edited in base files.** Amendments are intended to be managed through `spec/apply_amendments` and `spec/graduate_amendment`, which write them into a per-resource **override file** (`override-<Name>.cue`). Do **not** also hand-author `meta.amendments` for the same resource in a base spec file. This is the same limitation as every other override field: at load time all `.cue` files in the spec dir are unified, and CUE unifies lists **positionally** — a base list and an override list of differing lengths conflict (`incompatible list lengths`) and the whole spec stops loading. Keeping a resource's amendments in exactly one file (the override the tools manage) avoids this. The `meta.amendments` shape shown below is the schema the tools emit; author by hand only when no tool-written override exists for that resource.

```cue
project: contexts: Synth: aggregates: Voice: meta: {
    amendments: [
        {
            name:   "validate-reference-pitch"          // stable kebab-case id, unique within the resource
            prompt: "Reject 0.0, NaN, and infinity for the reference pitch; return an error instead of producing a degenerate frequency."
            origin: "deep_review"                        // "deep_review" | "manual" | "bugbot" | ...
            finding: {                                   // optional provenance, carried from the review
                severity: "high"
                file:     "src/Synth/Voice/Voice.rs"
                text:     "reference pitch is not validated; 0.0 yields a NaN frequency"
            }
            validation: {                                // optional — added to the resource's validation set on commit
                kind:    "test"
                command: ["cargo", "test", "--lib", "voice::reference_pitch"]
            }
        },
    ]
}
```

| Field | Purpose |
|-------|---------|
| `name` | Stable kebab-case id, unique within the resource. Used to address the amendment for listing/graduation. |
| `prompt` | The targeted change instruction — pure data appended to the resource's regeneration prompt. |
| `origin` | Where the amendment came from (`deep_review`, `manual`, `bugbot`, …). |
| `finding` | Optional provenance from a review (`severity`, `file`, `line`, `text`). |
| `validation` | Optional validation (same shape as section 1.4) added to the resource's validation set on commit, so the amendment's intent is *checked*, not assumed. |

### 8A.2 Mechanism — Riding the Spec-Hash Path

Adding an amendment changes the resource's **declaration hash**, so the planner emits an `ActionModify` for that resource and it regenerates (see section 5.1). This rides the normal spec-hash cascade — hand-edits on disk are not tracked at all (content-drift detection was removed; see `docs/drop-drift-detection.md`).

Regeneration happens in **UPDATE mode**. Rather than regenerating from scratch, the generation sub-agent receives the existing committed files plus a flagged **"CHANGES TO MAKE"** block containing the pending amendment prompts, and produces a *minimal diff*. UPDATE mode is generic: any already-generated resource that needs re-rendering uses it — amendments are simply the most common trigger.

### 8A.3 `applied` Is Derived, Not Stored

There is no `applied` flag on an amendment. An amendment is **APPLIED iff the resource's committed output was generated from a spec snapshot that included it** — i.e. the stored declaration hash equals the current declaration hash. This makes "applied" a pure function of state, with no flag to drift out of sync.

A SQLite `amendments` table **materializes** this status for querying (`spec/list_amendments`). The table is reconciled from the spec on session begin and is **never an independent source of truth** — the spec plus the resource's stored hash is authoritative; the table is a derived index.

### 8A.4 Lifecycle

```
PENDING ──► APPLIED ──► VERIFIED ──► GRADUATED
                │
                └──► FAILED   (off-ramp)
```

| State | Meaning |
|-------|---------|
| `PENDING` | Written into the spec, not yet regenerated/committed. |
| `APPLIED` | The resource was committed from a spec hash that contains the amendment (derived — see 8A.3). Applied does **not** mean fixed. |
| `VERIFIED` | The amendment's `validation` passed (or a re-run `spec/deep_review` no longer reports the finding), confirming the intent was actually achieved. |
| `GRADUATED` | Human-gated: the amendment's intent has been folded into the resource's canonical `invariants` and the amendment removed, so the spec describes the system cleanly instead of accumulating a patch log. A forced clean regeneration must still pass afterward. |
| `FAILED` | Validation never passes. Surfaced for re-draft; a partial fix is **not** committed. |

**Why graduate?** Without graduation a long-lived resource accumulates an ever-growing list of patch prompts. Graduation distills the durable intent ("reject degenerate reference pitch") into a first-class `invariant` and drops the transient amendment — the spec stays a clean description of the system, not a changelog.

### 8A.5 Tools

All amendment tools that write are **human-gated**: the write path takes an explicit `apply` flag, and with `apply=false` the tool returns the CUE diff for review and writes nothing.

| Tool | Sync/Async | Purpose |
|------|-----------|---------|
| `spec/propose_amendments` | sync | Runs `spec/deep_review` over the target (a single `resource_id`, or the whole session) and asks the LLM to draft a `{name, prompt, finding}` per actionable finding. **Returns proposals only — writes nothing.** |
| `spec/apply_amendments` | sync | Human-gated write-back of approved `proposals` into the CUE spec as an override file. `apply=false` returns the CUE diff for review (writes nothing); `apply=true` writes it. After approval, the next `spec/plan` / `spec/begin` re-renders the resource in UPDATE mode. |
| `spec/list_amendments` | sync | Query the materialized `amendments` table, optionally filtered by `resource_id` and/or `state`. |
| `spec/graduate_amendment` | sync | Human-gated fold of a `VERIFIED` amendment (by `resource_id` + `name`) into the resource's canonical `invariants`, removing the amendment. `apply=false` previews the CUE diff; `apply=true` writes it. |

### 8A.6 Typical Flow

1. After a clean apply, run `spec/deep_review` (or `spec/propose_amendments`, which runs it) to surface durable findings.
2. `spec/propose_amendments` drafts `{name, prompt, finding}` proposals — no writes.
3. Review the proposals; pass the approved ones to `spec/apply_amendments` with `apply=false` to preview the CUE diff, then `apply=true` to write the override.
4. `spec/plan` / `spec/begin` now shows the resource as `~` (declaration changed) and re-renders it in **UPDATE mode** with the amendment prompts in the "CHANGES TO MAKE" block. The amendment becomes `APPLIED` once committed.
5. The amendment's `validation` runs on commit (or re-run `spec/deep_review`); on pass the amendment is `VERIFIED`, on persistent failure `FAILED` (re-draft, don't commit a partial fix).
6. Once `VERIFIED` and stable, `spec/graduate_amendment` folds the intent into canonical `invariants` and removes the amendment — a forced clean regeneration must still pass.

---

## 9. MCP Server Interface

The Go binary exposes the plan/apply lifecycle as MCP tools. An AI agent (Claude Code, or any MCP-capable client) connects to it and drives the lifecycle through tool calls.

### 9.1 Transports & Concurrency

The server supports two transports, both backed by a shared `dispatch(ctx, request) response` method.

#### Stdio Transport

Always active. `Server.Run(ctx)`:

- Reads stdin line-by-line with a `bufio.Scanner` whose buffer is grown to **10 MiB** to accommodate large requests.
- Each non-empty line is dispatched in its **own goroutine** — so many requests can be in flight concurrently. A `sync.WaitGroup` tracks reader goroutines.
- Empty/blank lines skipped. JSON parse failures emit a `-32700 Parse error`.
- Progress notifications are written to stdout as JSON-RPC notification lines.

**Output serialization:** `writeResponse` marshals and writes a single line to stdout under `outMu` (mutex), so concurrent responses don't interleave.

#### Streamable HTTP Transport

Active when `HTTPAddr` is configured. A `net/http` server (stdlib, no framework).

- **Single endpoint:** `POST /mcp` — clients send JSON-RPC requests in the POST body.
- **Sync tools:** the response is a plain JSON-RPC response.
- **Async tools:** the response is a plain JSON-RPC response with a job ID. SSE streaming for progress notifications is planned but not yet implemented.
- **Session management:** stateless — each request is independent. Job IDs are the correlation mechanism.
- **Shutdown:** `http.Server.Shutdown(ctx)` with a 30s drain timeout.

Both transports share the same `Server` instance — same stores, metrics, concurrency pool, and tool definitions.

### 9.2 Request Routing

| Method | Handler |
|--------|---------|
| `initialize` | `handleInitialize` |
| `notifications/initialized` | `handleInitialized` |
| `tools/list` | `handleToolsList` |
| `tools/call` | `handleToolCall` |
| `resources/list` | `handleResourcesList` |
| `resources/read` | `handleResourcesRead` |
| `prompts/list` | `handlePromptsList` |
| `prompts/get` | `handlePromptsGet` |
| other | `-32601 Method not found` |

`handleInitialize` returns protocolVersion `2024-11-05`, capabilities `{tools: {}, resources: {}, prompts: {}}`, serverInfo `{name: crest-spec, version: 0.1.0}`, and an `instructions` string describing the orchestrator protocol.

### 9.3 MCP Tools

The server exposes two groups of tools: **spec tools** (the plan/apply lifecycle) and **engine tools** (adapted from claude-mcp — the sub-agent execution primitives). The orchestrating AI agent uses both: spec tools to drive the lifecycle, engine tools to dispatch sub-agents and verify code.

#### Spec Lifecycle Tools

| Tool | Sync/Async | Purpose |
|------|-----------|---------|
| `spec/plan` | sync | Show what would change (dry run). Loads CUE, diffs against state, returns planned actions. |
| `spec/apply` | **async** | Execute the plan. Dispatches sub-agents via engine, runs constraint loops, writes files, updates state. Returns a job ID. |
| `spec/validate` | sync | Check structural invariants against the spec without generating code. |
| `spec/begin` | sync | Start an interactive agent session: compute plan, create waves, acquire lock. Returns plan and orchestrator instructions. |
| `spec/next` | sync | Get the next wave of uncommitted resources. Returns `done: true` when complete. |
| `spec/context` | sync | Get the scoped prompt for a specific resource. Returns `systemPrompt`, `prompt`, `dependencyNotes`, and `instructions`. |
| `spec/validate-resource` | sync | Run invariant checks and optional type check/tests against files on disk for a specific resource. |
| `spec/note` | sync | Save a design decision note for a resource. Notes are injected into downstream prompts. |
| `spec/commit` | sync | Record a resource as complete in state. |
| `spec/resolve` | sync | Provide guidance for a blocked/errored/rejected/timed_out resource. Answer is injected into the prompt; resource is re-dispatched. See section 8.6. |
| `spec/amend` | sync | Signal that the CUE spec has been updated for a resource. Re-loads spec, re-computes hashes, updates the in-flight plan, re-dispatches. See section 8.6. |
| `spec/skip` | sync | Skip a failed resource. Transitions to `skipped` (terminal); wave proceeds without it. See section 8.6. |
| `spec/deep_review` | **async** | Comprehensive SOLID/DI/clean-code/refactoring review of committed code (a `target` resource or all committed resources). The signal source for amendments. Returns a job ID. |
| `spec/propose_amendments` | sync | Runs `spec/deep_review` and drafts `{name, prompt, finding}` amendment proposals per actionable finding. Returns proposals only — writes nothing. See section 8A. |
| `spec/apply_amendments` | sync | Human-gated write-back of approved amendment proposals into the CUE spec. `apply=false` returns the CUE diff; `apply=true` writes it. See section 8A. |
| `spec/list_amendments` | sync | Query the materialized `amendments` table, optionally filtered by `resource_id` and/or `state`. See section 8A. |
| `spec/graduate_amendment` | sync | Human-gated fold of a `VERIFIED` amendment into the resource's canonical `invariants` (then removes it). `apply=false` previews the diff; `apply=true` writes it. See section 8A. |
| `spec/finish` | sync | Finalize the session: release lock, return summary. |
| `spec/status` | sync | Show current state — resources in state, active session, lock status. |
| `spec/log` | sync | List past applies with status. |
| `spec/history` | sync | Show generation history for a specific resource. |
| `spec/graph` | sync | Return the resource dependency graph. |
| `spec/diff` | sync | Reconstruct state delta between two applies. Shows what was created, modified, destroyed between `apply_a` and `apply_b`. |
| `spec/state` | sync | Inspect or modify state tracking. `list` returns all resources in state with hashes. `rm <resourceId>` removes a resource from state without deleting its code on disk (next plan will treat it as `create`). |
| `spec/vacuum` | sync | Compact history older than a given date. Deletes old generations, invariant checks, and apply records while preserving current state. Keeps the SQLite database bounded for long-lived projects. |
| `spec/sql` | sync | Open a read-only SQLite shell against the state database. For direct inspection and ad-hoc queries. |
| `spec/unlock` | sync | Force-clear a stale lock. |

#### Engine Tools (adapted from claude-mcp)

These are the sub-agent execution primitives. The spec layer uses them internally during `spec/apply`, and the orchestrating agent can also call them directly during interactive sessions (`spec/begin` -> `spec/finish`).

| Tool | Sync/Async | Purpose |
|------|-----------|---------|
| `run_prompt` | **async** | Run a single prompt via `claude`. The orchestrator uses this to dispatch constrained code generation sub-agents. Returns a job ID. |
| `code_review` | **async** | Multi-model code review. The constraint loop can use this as a heavyweight verification pass. Returns a job ID. |
| `bugbot` | **async** | Lightweight severity-ranked scan. The constraint loop can use this as a fast verification pass. Returns a job ID. |
| `poll_result` | sync | Check a job's status; optionally consume its result (`consume` flag). |
| `cancel_job` | sync | Cancel a running job and kill its subprocess group. |
| `list_jobs` | sync | List up to 50 recent non-deleted jobs. |
| `list_models` | sync | Curated static model list. |
| `about` | sync | `claude --version` + `claude auth status`. |
| `status` | sync | `claude auth status`. |
| `live_metrics` | sync | Self-monitoring snapshot: uptime, call counts, error rates, per-tool stats. |
| `bootstrap` | sync | Install `claude` and guide login. Only registered when startup `status` fails. |

All tools return structured JSON. The MCP protocol handles transport (stdio or SSE).

### 9.4 Async Job Model

The heart of the async design, adapted from claude-mcp:

1. Generate a UUID job ID; capture `os.Getpid()` as the owner PID.
2. Create a `jobCtx` derived from `bgCtx` with its own cancel; register the cancel in `s.cancels[id]` (guarded by `cancelsMu`).
3. `store.Create(id, tool, pid)` — persist as `running`. On failure, unwind and return an error.
4. `asyncWg.Add(1)`; spawn a goroutine that:
   - defers `wg.Done`, `jobCancel`, and removal from `s.cancels`.
   - runs the job func, times it, records metrics.
   - **Outcome dispatch:** `err == nil` -> `store.Complete(id, result)`; `jobCtx.Err() != nil` (cancelled) -> `store.Cancel(id)`; else -> `store.Fail(id, err)`.
5. Returns immediately a `textContent` with the job ID.

**Progress notifications:** Each async exec func receives a `progressFunc(phase, partialResult)` callback. The MCP dispatch layer wraps this to emit `notifications/progress` if the client provided a `_meta.progressToken`. For wave-based applies, progress phases track: session started, wave N started, resource dispatched, resource completed/failed, wave N complete, all waves done.

### 9.5 Orchestrator Protocol

The `spec/begin` response includes `orchestratorInstructions` — a block of text that tells the calling agent exactly how to behave:

- **You are a dispatcher, not a code generator.** Do not write code yourself.
- For each resource: call `spec/context` to get its prompt, then call `run_prompt` with that prompt (using `--disallowedTools` for constrained output), parse the output, write files, call `spec/note` with design decisions, call `spec/commit`.
- Use `poll_result` to collect `run_prompt` results (they're async).
- Optionally run `code_review` or `bugbot` against generated files before committing.
- Resources within the same wave can be dispatched in parallel (multiple `run_prompt` calls).
- Waves must be processed sequentially.

### 9.6 Sub-Agent Communication via Notes

When a sub-agent generates a resource, the orchestrator records notes about its design decisions via `spec/note`:

```json
{"resourceId": "aggregate.Synth.Voice", "content": "Used newtype wrappers for NoteId and Velocity; envelope as a state machine enum"}
```

When a downstream resource requests its context via `spec/context`, these notes are injected:

```
## Notes from dependencies

### aggregate.Synth.Voice
- Used newtype wrappers for NoteId and Velocity; envelope as a state machine enum
```

This is how information flows between independently spawned sub-agents — through SQLite via MCP tools, not through shared context.

### 9.7 MCP Resources

Resources are read-only snapshots exposed via `resources/list` and `resources/read`.

| URI | Name | MIME type | Content |
|-----|------|-----------|---------|
| `crest-spec://plan` | Current Plan | `application/json` | Latest planned actions (or empty if no changes) |
| `crest-spec://state` | Spec State | `application/json` | All resources in state with hashes and settle times |
| `crest-spec://graph` | Dependency Graph | `application/json` | Resource dependency graph with wave assignments |
| `crest-spec://session` | Active Session | `application/json` | Current orchestration session state (if any) |
| `crest-spec://metrics` | Server Metrics | `application/json` | Live uptime, call counts, error rates, per-tool stats |

### 9.8 MCP Prompts

Prompts are templates exposed via `prompts/list` and `prompts/get`.

| Name | Description | Arguments |
|------|-------------|-----------|
| `system_prompt` | The system prompt that would be sent to sub-agents for a given project | (none) |
| `resource_prompt` | The full resource prompt for a specific resource | `resource_id` (string, required) |
| `orchestrator_instructions` | The orchestrator protocol instructions | (none) |

### 9.9 Recursion Guard

`crest-spec` dispatches `claude` subprocesses which could themselves have `crest-spec` configured as an MCP server — risking infinite recursion and runaway API spend. `DetectRecursion` (adapted from claude-mcp) walks up the process tree from `SelfPID()`:

- For each ancestor, lower-cases the basename of the command; counts processes whose name contains `claude` but **not** `crest-spec` and **not** `mcp`.
- If it finds **more than one** such `claude` process, returns `true`.
- Loop protection: a `visited` map and `pid > 1` guard prevent infinite loops on self-referential PIDs.

On detection, the server replaces all tools with a single placeholder and `dispatch` refuses real work.

**`--strict-mcp-config` flag:** When invoking `claude` subprocesses, the agent wrapper passes `--strict-mcp-config` to prevent the child process from loading MCP server configurations that could cause recursion. Combined with env var filtering (stripping `CREST_SPEC_*` and `MCP_*` variables from the child environment), this ensures sub-agents cannot accidentally invoke `crest-spec` as an MCP server.

### 9.10 Metrics

Lock-free per-tool counters using `atomic.Int64` for Calls, Errors, TotalNs, MinNs, MaxNs (min/max via CAS loops). `Metrics` holds a `map[string]*toolMetric` under an `RWMutex` and a start time. `snapshot()` reports uptime, total calls/errors, and per-tool `{calls, errors, avg_ms, min_ms, max_ms}`. Metric keys include model-scoped variants (`generate:<model>`, `verify:<model>`, etc.) for tracking sub-agent performance.

---

## 10. The crest-synth Reference Spec

The crest-synth synthesizer is the reference project for crest-spec. It demonstrates how a large DDD spec looks in CUE across multiple files.

### Spec Directory Structure

In production, the spec is organized by domain:

```
crest-synth/spec/
  project.cue         <- project config, layers, layer rules, invariants
  kernel.cue          <- Kernel context: shared value objects (NoteId, Velocity, MidiEvent, etc.)
  synth.cue           <- Synth context: Voice aggregate, VoiceAllocator, AudioRenderer
  realtime.cue        <- RealTime context: lock-free boundary, ParameterBridge, BoundaryMessage
  patch.cue           <- Patch context: per-patch voice pools, channel dispatch, MPE zones
  modulation.cue      <- Modulation context: mod matrix, LFOs, per-note expression
  sample-library.cue  <- SampleLibrary context: SF2/WAV loading, key/velocity zones
  effects.cue         <- Effects context: per-patch and global FX chains
  presets.cue         <- Presets context: save/load, preset banks, session snapshots
  shell.cue           <- Shell context: ports (AudioOutput, MidiInput, GamepadInput)
  adapters.cue        <- Infrastructure: CpalAudioOutput, MidiAdapter, GamepadAdapter
  assets.cue          <- Generated artifacts: Cargo.toml, lib.rs, mod.rs, main.rs
  plugin.cue          <- Plugin context: nih-plug CLAP/VST3 wrapper
```

All files are loaded and unified together — the full spec is the result.

### Test Fixture: Phase Files

For integration testing, the same spec is also expressed as numbered phase files (`phase-1.cue`, `phase-2.cue`, ...). This lets tests exercise incremental plan/apply by loading subsets: load only `base.cue + phase-1.cue` to test the minimal "make noise" path, then add `phase-2.cue` to test that the planner detects new resources, and so on. This is a **test fixture pattern**, not a production feature — the loader has no special knowledge of phases.

### Contexts Overview

| Context | Key Concepts |
|---------|--------------|
| Kernel | Shared value objects: NoteId, Velocity, MidiEvent, SampleRate, AudioFrame |
| Synth | Voice aggregate, oscillator/filter/envelope, voice stealing, VoiceAllocator |
| RealTime | Lock-free boundary (rtrb, triple_buffer, basedrop), ParameterBridge |
| Patch | Per-patch voice pools, channel dispatch, MPE zones, global mixer |
| Modulation | Mod matrix, LFOs, envelopes, per-note expression (MPE-ready) |
| SampleLibrary | SF2/WAV loading, key/velocity zones, interpolation |
| Effects | Per-patch and global FX chains (reverb, chorus, delay) |
| Presets | Save/load patches, preset banks, full session snapshots |
| Shell | Ports: AudioOutput, MidiInput, GamepadInput + all infrastructure adapters |
| Plugin | nih-plug shell for CLAP/VST3, parameter mapping |

---

## 11. Lifecycle & Robustness

- **app.New() + Run(ctx)** — minimal lifecycle wrapper (`internal/app`).
- **Graceful shutdown** — SIGINT/SIGTERM via `signal.NotifyContext`; HTTP server (if active) shuts down via `http.Server.Shutdown(ctx)`; server drains in-flight jobs up to 30s; `cmd.Cancel` + process groups + `WaitDelay` ensure subprocess trees are SIGKILLed rather than leaked.
- **Global concurrency** — a `chan struct{}` semaphore of size `MaxConcurrency` (default 5) ensures at most N `claude` subprocesses run simultaneously across all tools and transports.
- **Process groups** — every subprocess runs with `Setpgid: true` and is killed via `kill(-pid, SIGKILL)` so children die too.
- **Config isolation** — prevents concurrent `claude` processes from corrupting `~/.claude/.claude.json` or contending on the session store. Only activates when `CREST_SPEC_API_KEY` is set.
- **Logging** — zerolog to stderr with timestamps; `Panic` (not `Fatal`) per convention so deferred cleanup runs.
- **Crash recovery** — persistent SQLite state + PID liveness reconciliation means stale `running` jobs from a previous (dead) process are cleaned up on next start. The `lock` table similarly detects stale locks from dead processes.
- **Exclusive apply lock** — the `lock` table prevents concurrent applies from corrupting generation state. `spec/unlock` provides a manual escape hatch for stale locks.

---

## 12. Build, Tooling & Conventions

### 12.1 Make Targets

| Target | Action |
|--------|--------|
| `make` / `make all` | `fmt test lint` |
| `make build` | build to `bin/crest-spec` |
| `make install` | `go install ./cmd/crest-spec` |
| `make test` | `go test ./...` |
| `make fmt` | `go fmt` + goimports-reviser |
| `make mocks` | regenerate counterfeiter fakes |
| `make sqlc` | `sqlc generate` |
| `make lint` / `lint-fix` | golangci-lint |
| `make update` | `go get -u` + `go mod tidy` |

### 12.2 Conventions

- `app.New() + Run(ctx)` lifecycle.
- `envconfig` config with a service prefix (`CREST_SPEC_`).
- zerolog structured logging; `Panic` not `Fatal`.
- SIGINT + SIGTERM graceful shutdown.
- Package-private interfaces for DI; counterfeiter mocks committed.
- sqlc for type-safe SQLite; queries in `sql/queries/`, schema in `migrations/`.
- Const error sentinels via `type New string` (`internal/errors`).
- Table-driven tests (error cases first, success last); testify `require` for guards, `assert` for multi-field checks.
- golangci-lint with gci import ordering.

### 12.3 Key Dependencies

`cuelang.org/go` (CUE parser/evaluator), `github.com/google/uuid`, `github.com/kelseyhightower/envconfig`, `github.com/rs/zerolog`, `github.com/stretchr/testify`, `modernc.org/sqlite` (CGO-free SQLite).

---

## 13. Running Modes & Client Integration

The `crest-spec` binary has three running modes: **MCP server** (long-lived, serves tools to an orchestrating agent), **Dashboard** (monitoring interface with API endpoints), and **CLI** (short-lived subcommands the orchestrator invokes directly).

### 13.1 Mode 1: MCP Server

The primary mode. An orchestrating AI agent (Claude Code, Cursor, or any MCP client) connects and drives the plan/apply lifecycle through MCP tool calls.

| Invocation | Behavior |
|------------|----------|
| `crest-spec` | Run as a stdio MCP server (default). |
| `CREST_SPEC_HTTP_ADDR=:8080 crest-spec` | Run with both stdio and Streamable HTTP transports. |

**Stdio client config** (e.g. `~/.mcp.json`):

```json
{
  "mcpServers": {
    "crest-spec": { "command": "crest-spec", "args": [] }
  }
}
```

Auth: uses the developer's existing local Claude Code session (`~/.claude` OAuth/keychain); no API key required unless `CREST_SPEC_API_KEY` is set (passed through as `ANTHROPIC_API_KEY`).

### 13.2 Mode 2: Dashboard

A monitoring interface that exposes API endpoints for inspecting system state. The dashboard provides visibility into active sessions, job status, resource state, and generation history without going through the MCP protocol.

### 13.3 Mode 3: CLI Subcommands

The orchestrating agent runs these as background shell commands to interact with SQLite state and collect async job results without going through the MCP protocol.

| Invocation | Behavior |
|------------|----------|
| `crest-spec check job <id>` | Block until job `<id>` completes; print result to stdout (exit 0) or error to stderr (exit 1); consume the job. SIGINT/SIGTERM-aware. |
| `crest-spec state list` | Print all resources in state with hashes and settle times. |
| `crest-spec state rm <resourceId>` | Remove a resource from state without deleting code on disk. Next plan treats it as `create`. |
| `crest-spec diff <apply_a> <apply_b>` | Show what changed between two applies (created, modified, destroyed resources). |
| `crest-spec vacuum --before <date>` | Compact history older than date. Deletes old generations, invariant checks, and apply records. |
| `crest-spec sql` | Open a read-only SQLite shell against `.crest-spec/state.db`. |
| `crest-spec -h` / `--help` | Print usage + env-var table; exit 0. |

**`check job`** is the recommended async result collector (adapted from claude-mcp). The orchestrating agent fires an async tool call (`spec/apply`, `run_prompt`), gets a job ID, launches `crest-spec check job <id>` as a background Bash command, and is notified when it completes. This decouples long-running jobs from the synchronous MCP request/response loop.

Implementation:
1. Open the store (`.crest-spec/state.db`), run `CleanupOrphans`.
2. `WaitForCompletion(ctx, id)` — blocks with exponential backoff, SIGINT/SIGTERM-aware.
3. `completed` -> print `result` to stdout, soft-delete the job, exit `0`.
4. `failed`/`cancelled` -> print status+error to stderr, soft-delete, exit `1`.

### 13.4 Multi-Phase Agent Runner

`scripts/run-phased-agent.sh` is a shell script that drives crest-spec through all 10 crest-synth phases with state carry-over. It automates the full lifecycle: for each phase, it loads the corresponding CUE spec subset, runs `spec/plan` and `spec/apply`, and carries forward SQLite state between phases so that incremental planning works correctly across the full spec evolution.

### 13.5 Typical Orchestration Flows

**Automated (spec/apply):**
1. Client calls `spec/plan` -> sees planned changes.
2. Client calls `spec/apply` -> gets a job ID.
3. Client runs `crest-spec check job <id>` in background (or polls `poll_result`, or uses SSE streaming over HTTP).
4. On success: all resources generated, verified, and committed.
5. On failure: progress notification surfaces blocked/errored resources. Client calls `spec/resolve`, `spec/amend`, or `spec/skip` to unblock. Apply resumes automatically.

**Interactive (spec/begin -> spec/finish):**
1. Client calls `spec/begin` -> gets plan + orchestrator instructions.
2. Client calls `spec/next` -> gets wave 0 resources.
3. For each resource:
   - `spec/context` -> get the scoped prompt
   - `run_prompt` with prompt + `--disallowedTools` -> get job ID
   - `crest-spec check job <id>` or `poll_result` -> collect generated code
   - Write files to disk
   - Optionally `bugbot` or `code_review` on the generated files for verification
   - `spec/note` -> record design decisions
   - `spec/commit` -> record resource as complete
4. If a resource fails:
   - `spec/resolve` -> provide guidance and re-dispatch
   - `spec/amend` -> edit the CUE spec, re-load, re-dispatch with updated declaration
   - `spec/skip` -> skip and move on
5. `spec/next` -> wave 1 resources. Repeat.
6. `spec/next` returns `done: true` -> `spec/finish`.

The interactive flow gives the orchestrating agent full control — it can inspect prompts, modify files, edit the CUE spec to fix root causes, resolve blocked resources, run targeted reviews, and make decisions the automated flow can't. The `spec/amend` path is particularly powerful: a generation failure becomes a signal to improve the spec, not just retry harder.

---

## 14. Design Principles

### Specification over Implementation
The spec describes *what* the system should look like, not *how* to build it. The LLM handles the *how*.

### Change Detection via Content Hashing
Effective hashes include the full dependency chain and the model identifier. Changing the LLM model triggers regeneration of everything — because the output would differ.

### Cascading Changes
A change to a value object cascades to every aggregate that uses it, every service that uses those aggregates, every asset that references any of them. The dependency graph ensures nothing is stale.

### Respect Hand-Edits
Generated code is not sacred — and neither is the spec's claim on it. Once a file is generated, its content belongs to the user: format it, fix it, patch it freely; the planner ignores content changes. Regeneration is opt-in — edit the spec or delete the file. The system never holds your edits hostage behind a reconciliation step.

### Multi-File Composition via Unification
CUE's unification model makes multi-file specs a first-class language feature. No import chains, no re-exports, no builder pattern. Files in the same package merge automatically. Split your spec however makes sense — by context, by layer, by team.

### Verification at Every Level
Code is verified by the constraint loop (parse -> type check -> invariant check -> test -> LLM review), then again at the wave level (cross-resource type check and tests), with retries at each level.

### Audit Everything
Every LLM call, every prompt, every output, every retry, every failure reason is recorded in SQLite. You can replay, debug, and understand every decision the system made.

### The LLM is a Constrained Worker
Sub-agents have no tool access. They receive a prompt and produce code blocks with path annotations. The orchestrator handles all file I/O, state management, and verification. The LLM never touches the filesystem.

### Everything Long-Running is Async + Persisted
This sidesteps stdio timeouts and lets the orchestrator dispatch several sub-agents in parallel while staying responsive. Job state survives crashes via SQLite + PID liveness reconciliation.

### MCP-Native
The Go binary is an MCP server, not a CLI with subcommands. An AI agent drives the lifecycle through structured tool calls. This makes crest-spec composable with any MCP-capable orchestrator.
