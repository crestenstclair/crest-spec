# crest-spec: Functional & Implementation Specification

## Overview

crest-spec is a **declarative, domain-driven code generation system**. You describe your software architecture as CUE specification files using a schema rooted in Domain-Driven Design (DDD) vocabulary, then generate implementation code one resource at a time with surgically scoped prompts. The system tracks all state in SQLite, diffs specs against prior state to build execution plans, and enforces architectural invariants at every stage.

The mental model is **Terraform for code generation**: you declare what your system *should* look like, the tool plans what needs to change, and then applies those changes — with dependency ordering, retry loops, and verification gates.

**The split of responsibilities is the central design point.** The `crest-spec` binary is a **pure spec state engine**: it plans (CUE → registry → graph → waves), tracks state in SQLite, runs **mechanical** validations (compile / test / custom commands) at commit time, enforces orchestrator-supplied invariant verdicts, and records history. **It never calls an LLM and never spawns subprocesses.** Generation is driven by **Claude Code natively** — the orchestrator runs the `.claude/skills/spec-generate` skill and the `.claude/workflows/spec-generate.js` workflow, which spawn one sonnet sub-agent per resource per wave. Each generator agent pulls a scoped prompt + invariants from `spec/context`, authors files, judges each invariant, and submits the result to `spec/commit`. The core validation loop survives at that commit boundary (see §4 and §8).

> **Architecture pivot (native-workflow).** Earlier versions of crest-spec shelled out to `claude` CLI subprocesses (an `internal/agent` wrapper + `internal/engine` dispatch layer) behind an async jobs system, and offered server-side orchestration (`spec/apply`, `spec/dispatch`, `spec/run_wave` and an in-server constraint loop). All of that was removed in the native-workflow pivot. The server no longer runs any LLM or subprocess; Claude Code is the orchestrator. Tombstones throughout this document mark the removed surfaces.

- **Module:** `github.com/crestenstclair/crest-spec`
- **Go version:** 1.26.4
- **Server version reported over MCP:** `0.1.0`
- **MCP protocol version:** `2024-11-05`
- **Transport:** stdio (JSON-RPC over stdin/stdout) and Streamable HTTP (`POST /mcp`; plain JSON-RPC only)
- **Code generation:** performed by the orchestrator's own sub-agents (default model: sonnet); the server only constructs prompts and validates results
- **Platform assumptions:** Unix-like; developed on macOS/Darwin.

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

**How validations fit the lifecycle:** Validations run at the `spec/commit` gate (§8.2). The server writes the resource's files, runs its declared `validations` in order, and any failure rejects the commit — the resource transitions from `completed` to `rejected` with the validation output attached, and that failure is injected into the next `spec/context` so the regenerated attempt sees it.

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

Invariants are checked structurally at plan time, and against generated code at commit time by the **orchestrator's** verdict (`spec/commit`'s `invariant_checks`), which the server enforces but does not itself judge (§4). The `rationale` is injected into the `spec/context` prompt so the sub-agent judges against *why* the rule exists, not just the rule.

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
    reviewLevel?: "full" | "light" | "solid" | "skip"  // Review-depth hint (see note below)
    ...                       // Extensible
}
```

**`reviewLevel` values:** The field is still parsed and propagated through the registry, but it no longer triggers a **server-side** LLM review — the server runs no LLM. It is now a hint the orchestrator may use to decide how hard to review a resource's committed code (e.g. before drafting amendments, §8A). The historical values:

| Value | Intended review depth |
|-------|----------------------|
| `"full"` | Heavyweight, multi-model architecture review. Suits aggregates, domain services, adapters. |
| `"light"` | Lightweight, severity-ranked scan. Suits value objects, entities, assets. |
| `"solid"` | SOLID/DI/interface review. Explicit opt-in for any resource. |
| `"skip"` | No review. For generated boilerplate (mod.rs, Cargo.toml, manifests). |

> The old server-dispatched review tools (`engine.CodeReview` / `engine.Bugbot` / `engine.Review`) were removed in the native-workflow pivot; reviewing is now the orchestrator's job.

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

The server is a single layer: the **spec state engine**. It handles CUE loading, planning, prompt construction, mechanical validation, and history — all in-process, with no subprocess management and no LLM calls. Orchestration (sub-agent dispatch, retries, parallelism) lives **outside the server**, in Claude Code's skills and workflows.

```
cmd/crest-spec/main.go               Entrypoint + flag/help handling
  +- config.New()                     envconfig (CREST_SPEC_ prefix)
  +- store.New(dbPath)                SQLite store (WAL mode) — all state in one DB
  +- spec.New(store, fs, cfg)         plan / commit / validate / history lifecycle
  +- mcp.New(spec, stdin, stdout, log, cfg)   MCP server (tools, dispatch, metrics)
       +- stdio transport             reads stdin, writes stdout (Server handles directly)
       +- HTTP transport              net/http server, POST /mcp (started if HTTPAddr set)

internal/
  config/       envconfig-based configuration + usage/help text
  mcp/          JSON-RPC server, tool definitions, dispatch, metrics
  cue/          CUE loader: multi-file unification, resource parsing
  graph/        Resource dependency graph: topological sort, wave computation, hash propagation
  plan/         Planner: diff registry against state, compute effective hashes, produce PlannedAction[]
  prompt/       Prompt builder: system prompt, resource prompt, fix/UPDATE-mode context, invariants
  spec/         Spec engine: plan/begin/context/commit/finish lifecycle, wave verification,
                commit-time validation gate, session state machine; includes validation (validate.go)

  ## Shared
  db/           sqlc-generated query code (DO NOT EDIT)
  errors/       Const error sentinel type (`type New string`)
  store/        SQLite store: resources, files, applies, generations, sessions, notes, lock
                (the jobs table is retained in the schema but unused — see §7.1)
migrations/     SQL schema, embedded via go:embed; applied at store startup
sql/queries/    sqlc query definitions (source for internal/db)

.claude/
  skills/spec-generate/SKILL.md       orchestrator entrypoint (the spec-generate skill)
  workflows/spec-generate.js          per-wave sub-agent fan-out + commit/retry/triage loop
```

> **Tombstone — engine/agent layers.** The `internal/agent` (claude CLI wrapper) and `internal/engine` (run_prompt / code_review / bugbot dispatch) packages were removed in the native-workflow pivot. The server no longer touches `os/exec`, the `claude` binary, or any concurrency semaphore. The work those packages did — dispatching sub-agents, retrying, parallelizing a wave — is now done by the Claude Code workflow in `.claude/workflows/spec-generate.js`.

### 2.2 How the Layers Connect

There is no server-internal generation path. The connection between the spec engine and the LLM is the **prompt-out / verdict-in** contract across the MCP tool boundary (see §4):

- **Prompt out**: `spec/context` returns the system prompt, the scoped resource prompt, and the project invariants (each `{text, rationale}`) for a resource. The orchestrator hands those to a sub-agent it spawns itself.
- **Files + verdict in**: `spec/commit` accepts the generated files, the model label, and the orchestrator's per-invariant verdicts (`invariant_checks`). The server writes the files, runs the resource's **mechanical** validations (compile / test / custom), and enforces the supplied invariant verdicts. Any failure rejects the commit and feeds the failure into the next `spec/context`.
- **Reflection out / learnings in**: `spec/evolve` (and `spec/finish`, per `CREST_SPEC_EVOLVE`) returns a reflection prompt built from the session's failure history; the orchestrator runs it and submits the raw output to `spec/record_learnings`.

### 2.3 Dependency Injection / Interfaces

The codebase uses **package-private interfaces at the consumer** for testability:

- `spec.FileSystem` — the filesystem surface the spec engine writes through (`MkdirAll`, `ReadFile`, `WriteFile`, …). Real impl is `spec.OSFileSystem{}`; faked in tests so commits can be validated without touching disk.
- `app.server` — anything with `Run(ctx) error`.

Mocks/fakes are committed under `internal/mocks/` (counterfeiter `//go:generate` directives) where still used.

### 2.4 Startup Sequence (`main.go`)

1. **Help** — `-h`/`--help` prints usage + env var table (`config.Help()`), exits 0.
2. **Subcommand check** — `crest-spec dashboard|state|diff|vacuum|sql` dispatch to their handlers and exit (see §13.3).
3. **Config** — `config.New()`; on error, print help and panic.
4. **Store** — `store.New(dbPath())` where `dbPath()` is `.crest-spec/state.db` in the project directory. `defer store.Close()`.
5. **Signal context** — `signal.NotifyContext(ctx, SIGINT, SIGTERM)`.
6. **Spec** — `sp := spec.New(store, OSFileSystem{}, cfg)` — the plan/commit/validate lifecycle engine.
7. **Server** — `srv := mcp.New(sp, os.Stdin, os.Stdout, log, cfg)`.
8. **Transport** — stdio is always served by `srv.Run(ctx)`; if `cfg.HTTPAddr != ""`, the Streamable HTTP transport (`POST /mcp`) also starts on that address. On exit, a graceful shutdown is logged.

---

## 3. Configuration (`internal/config`)

All env vars use the `CREST_SPEC_` prefix via `envconfig.Process("CREST_SPEC", &cfg)`. The `Config` struct is small — the server has no subprocess, model-client, or concurrency knobs to configure.

| Field | Env var | Type | Default | Purpose |
|-------|---------|------|---------|---------|
| `HTTPAddr` | `CREST_SPEC_HTTP_ADDR` | string | (none) | Listen address for the Streamable HTTP transport (e.g., `:8080`). If unset, only stdio is active. |
| `GenerateModel` | `CREST_SPEC_GENERATE_MODEL` | string | `claude-sonnet-4-6` | Model **label** recorded in effective hashes / state rows and used as the default commit label when `spec/commit` omits `model`. The server does not invoke this model — it is provenance metadata. |
| `MaxRetries` | `CREST_SPEC_MAX_RETRIES` | int | `3` | Per-resource retry budget surfaced to the orchestrator (the workflow uses it to bound its commit/retry loop). |
| `WaveMaxRetries` | `CREST_SPEC_WAVE_MAX_RETRIES` | int | `2` | Retry count for wave-level verification failures. |
| `SpecDir` | `CREST_SPEC_SPEC_DIR` | string | `./spec` | Directory containing CUE spec files. |
| `TypeCheckCommand` | `CREST_SPEC_TYPE_CHECK_CMD` | string | (none) | Build/type-check command (e.g., `cargo check`) used at the wave-verification gate. |
| `TestCommand` | `CREST_SPEC_TEST_CMD` | string | (none) | Test command (e.g., `cargo test`) used at the wave-verification gate. |
| `Mode` | `CREST_SPEC_MODE` | string | `default` | Mode label folded into hashes — different modes regenerate. |
| `Evolve` | `CREST_SPEC_EVOLVE` | string | `all` | Controls when reflection prompts are emitted. `finish`/`all` make `spec/finish` return a `reflection_prompt`. |

`config.Help()` renders an aligned usage table to stderr using `tabwriter` + `envconfig.Usagef`.

> **Tombstone — removed config.** The native-workflow pivot dropped every subprocess/model-client knob: `CREST_SPEC_API_KEY`, `CREST_SPEC_AGENT_PATH`, `CREST_SPEC_DEFAULT_MODEL`, `CREST_SPEC_TIMEOUT`, `CREST_SPEC_MAX_CONCURRENCY`, and `CREST_SPEC_VERIFY_MODEL`. The server spawns nothing, so there is nothing for them to govern; the orchestrator owns model choice and concurrency.

---

## 4. The Orchestration Boundary

> **Tombstone.** This section previously documented the `internal/agent` CLI wrapper and the `internal/engine` sub-agent dispatch layer (`Generate` / `Review` / `CodeReview` / `Bugbot`). Both packages were deleted in the native-workflow pivot. The server no longer generates code, runs reviews, or spawns processes. What follows is the contract that replaced them.

The server is a state engine; Claude Code is the orchestrator. The two communicate only through MCP tool calls, and the contract is **prompt-out / verdict-in**: the server emits prompts and ingests verdicts, but never judges generated content itself.

**The contract:**

- **`spec/context` (prompt out)** — given `{session_id, resource_id}`, returns `SystemPrompt`, `Prompt`, and `Invariants` (each `{text, rationale}`). The prompt carries the resource declaration, dependency declarations, dependency notes, existing files (in UPDATE mode), and any prior commit failure for this resource. The orchestrator hands these to a sub-agent it spawns itself.
- **`spec/commit` (files + verdicts in)** — given `{session_id, resource_id, files:[{path,content}], notes, model, invariant_checks:[{invariant, passed, summary}]}`, the server writes the changed files (per-file SHA256 skip), runs the resource's **mechanical** validations (`compiles` / `test` / `integration` / `custom`) plus any amendment validations, and enforces the supplied invariant verdicts. **Any failing validation or any `passed:false` invariant verdict rejects the commit** (`Committed:false`, state → rejected). The server records a `Generation` row labelled with `model` (or the configured `GenerateModel` if omitted) and the invariant verdicts as `invariant_checks` rows. It never re-judges an invariant — the orchestrator's verdict is authoritative.
- **`spec/evolve` (reflection out)** — given `{session_id}`, returns `{reflection_prompt}` built from the session's failure history (empty when there is nothing to learn). The orchestrator runs the prompt with a sub-agent.
- **`spec/record_learnings` (learnings in)** — given `{session_id, output}` (the raw reflection output), persists the distilled learnings and returns `{learnings_added}`.
- **`spec/finish`** — finalizes the session and, when `CREST_SPEC_EVOLVE` is `finish`/`all`, also returns a `reflection_prompt` so reflection can run without a separate `spec/evolve` call.

The orchestrator implementation lives in this repo under `.claude/`:

- **`.claude/workflows/spec-generate.js`** — the per-wave engine. It calls `spec/next` for a wave, spawns one sub-agent per resource via `parallel(...)`, and each sub-agent runs the `spec/context` → author → judge invariants → `spec/commit` → retry-on-rejection loop (bounded by `maxRetries`). Persistent failures are triaged (`spec/resolve` or `spec/skip`); a stall guard force-skips resources stuck after repeated wave passes (see §8).
- **`.claude/skills/spec-generate/SKILL.md`** — the operator-facing entrypoint that drives `spec/plan` → `spec/begin` → `spec/confirm_destroys` → the workflow → `spec/finish` (+ reflection).

The core validation loop — the thing that makes generation reliable — survives entirely at the `spec/commit` boundary. The server is still the quality gate; only the dispatch machinery moved out.

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

### 5.2 Apply — the workflow-driven loop

> **Tombstone — `spec/apply`.** The server no longer ships an unattended, server-side apply that dispatches sub-agents and runs an in-server constraint loop. `spec/apply` (and its async job) was removed in the native-workflow pivot. "Applying" a plan is now done by the orchestrator, not the server.

The apply phase is driven by Claude Code through the spec-generate skill/workflow (§4, §8):

1. **`spec/begin`** runs the planner, acquires the SQLite session lock, computes dependency waves, and returns `{session_id, plan, waves, PendingDestroys}`.
2. **`spec/confirm_destroys`** (when `PendingDestroys` is non-empty) deletes files and state for removed resources — only for the resource IDs the operator confirms.
3. For each wave, the workflow calls **`spec/next`**, then spawns one sub-agent per resource. Each sub-agent runs `spec/context` → author files → judge invariants → `spec/commit`. The server writes files (per-file SHA256 skip — files whose content already matches on disk are not rewritten), runs the resource's mechanical validations, enforces the invariant verdicts, and records everything in SQLite. On rejection the sub-agent re-pulls `spec/context` (now carrying the failure) and retries, up to `MaxRetries`.
4. Persistent failures are triaged with `spec/resolve` or `spec/skip`; the wave advances when every resource is committed, skipped, or terminally rejected.
5. **`spec/finish`** releases the lock and returns the session summary (and a `reflection_prompt` when `EVOLVE` is `finish`/`all`).

There is no "flat vs wave" mode toggle: generation is always wave-based, ordered by the dependency graph, with resources inside a wave run in parallel by the workflow (§8.6).

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

SQLite is the single source of truth for all system state. One database (`.crest-spec/state.db` in the project directory) holds everything: spec state, generation audit trail, and session coordination.

- SQLite via `modernc.org/sqlite` (pure-Go, CGO-free).
- PRAGMAs at open: `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON` (enables cascade behavior for foreign key constraints).
- Migrations: SQL files embedded via `migrations.FS` (go:embed), applied in filename order, tracked in a `schema_migrations(filename)` table; each applied transactionally.
- Queries are sqlc-generated into `internal/db/` (do not hand-edit); query source is `sql/queries/*.sql`.

### 7.1 Jobs (retired — async tool lifecycle removed)

> **Tombstone.** Every tool call is now synchronous: the server does no background work because it spawns no subprocesses. The async job lifecycle (`run_prompt`/`spec/apply` returning a job ID, a background goroutine, `poll_result`, `cancel_job`, `list_jobs`, orphan reconciliation via PID liveness) was removed in the native-workflow pivot.

The **`jobs` table is retained in the migrated schema** (so no migration churn was needed) but is **unused** — nothing writes to it and no tool reads it. It is kept only to avoid a destructive migration; treat it as dead schema. The original columns (`id`, `tool`, `status`, `result`, `error`, `pid`, `started_at`, `done_at`) and their semantics are no longer load-bearing.

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

**State operations:** `GetResource`, `SetResource`, `ListResources`, `DeleteResource`, `SetGeneratedFile`, `GetGeneratedFiles`, `SetDependency`.

**Apply operations:** `CreateApply`, `CompleteApply`, `RecordAction`, `RecordGeneration`, `RecordInvariantCheck`.

**Session operations:** `CreateSession`, `GetSession`, `UpdateSession`, `GetNote`, `SetNote`, `ListNotes`, `AcquireLock`, `ReleaseLock`, `GetLock`.

`Close` shuts down the database connection.

---

## 8. The Plan / Generate / Commit / Retry Loop

> **Tombstone — server-side dispatch.** The loop below used to run *inside* the server: an apply engine that dispatched `engine.Generate` and ran an in-server constraint loop (parse → validate → invariant → code-review → fix-prompt → retry). That machinery was removed. The loop now spans the MCP boundary: the **orchestrator** generates (via a sub-agent) and the **server** validates (at `spec/commit`). Code-review-as-LLM-gate is gone; the commit gate is purely mechanical validations plus orchestrator-supplied invariant verdicts.

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
              | spec/plan, spec/begin (waves + lock)
              v
+-------------------------------------------------------------+
|                    Plan                                     |
|  + aggregate.Synth.Voice (new)                             |
|  ~ domainService.Synth.Allocator (dependency changed)      |
|  - aggregate.Audio.SineVoice (removed)                     |
+-------------+-----------------------------------------------+
              | Claude Code workflow (.claude/workflows/spec-generate.js)
              v
+-------------------------------------------------------------+
|         Orchestrator (per wave, resources in parallel)      |
|  For each resource in the wave -> one sub-agent:            |
|    1. spec/context  -> SystemPrompt + Prompt + Invariants   |
|    2. author files (sub-agent, full file contents)         |
|    3. judge each invariant -> {invariant, passed, summary}  |
|    4. spec/commit  -> files + model + invariant_checks      |
+-------------+-----------------------------------------------+
              | spec/commit (server side)
              v
+-------------------------------------------------------------+
|         Commit Gate (server — mechanical + verdicts)        |
|    write changed files (per-file SHA256 skip)              |
|    run resource validations:                               |
|      compiles / test / integration / custom                |
|    run amendment validations                               |
|    enforce supplied invariant_checks verdicts              |
|        |                                                    |
|        +-- all pass -> Committed=true, record Generation    |
|        +-- any fail  -> Committed=false (rejected),         |
|                         failure stored for next context     |
+-------------+-----------------------------------------------+
              | on rejection
              v
   sub-agent re-pulls spec/context (now includes the failure),
   regenerates, re-commits — up to MaxRetries. Then triage:
   spec/resolve (guidance) or spec/skip.
```

### 8.2 The Commit Gate in Detail

The validation loop is the core of crest-spec's reliability, and it lives at `spec/commit`. The **orchestrator's sub-agent** assembles and generates; the **server** validates. Per resource:

1. **Generate (orchestrator sub-agent)**: `spec/context` returns the prompt assembled from:
   - The resource's `assetKind.prompts` (generation rules inherited by all assets of this kind)
   - The resource's own `prompts` field (asset-specific instructions)
   - The resource declaration as JSON (state fields, commands, events, port contracts)
   - Dependency declarations (from `uses`, `implements`, `of` references)
   - Runtime context: module tree, existing dependency files, notes from upstream resources, and — on a retry — the prior commit failure
   - The project `Invariants` (each `{text, rationale}`) the sub-agent must judge

   The sub-agent authors full file contents at the correct paths and produces a `{invariant, passed, summary}` verdict for each invariant.

2. **Write (server)**: `spec/commit` writes the changed files. Each file is content-hashed (SHA256); a file whose content already matches what is on disk is skipped — no write, no timestamp change, no file-watcher trigger.

3. **Resource Validations (server)**: Runs the `validations` declared on the resource (see §1.4), in order:
   - **`compiles`** — runs the declared build command (e.g., `["cargo", "build"]`).
   - **`test`** — runs the declared test command (e.g., `["cargo", "test", "--lib", "cpal_audio_output"]`).
   - **`integration`** — runs a command and checks structured `assertions`: `exit_code`, `file_exists`, `file_not_empty`, `stdout_contains`, `stderr_empty`, `file_matches`.
   - **`custom`** — runs an arbitrary script. Exit 0 = pass, nonzero = fail with stderr attached.

   If a resource declares no `validations`, this step falls back to the global `TypeCheckCommand` / `TestCommand` from config (if configured). Amendment validations (§8A) run here too.

4. **Invariant Verdicts (server enforces, does not judge)**: The server does **not** check invariants against the code — it has no LLM. It enforces the `invariant_checks` the orchestrator supplied: any verdict with `passed:false` rejects the commit. The verdicts are recorded as `invariant_checks` rows (prompt-out / verdict-in). Each invariant's `text` and `meta.rationale` were already in the `spec/context` prompt, so the sub-agent judged against both the rule and why it exists.

**Outcome.** If every validation passes and no invariant verdict is false, the commit succeeds (`Committed:true`), the resource is persisted, the `Generation` row is marked `success` (labelled with the supplied `model`), and amendment validations are marked `VERIFIED`. Otherwise the commit is rejected (`Committed:false`, state → `rejected`), the `Generation` row is marked `rejected` with the first failure message, amendment validations are marked `FAILED`, and the failure is stored so the next `spec/context` for this resource includes it.

**On retry**, the regenerated `spec/context` prompt carries:
- The original resource prompt (full requirements from the DSL)
- The existing committed files (UPDATE mode), so the sub-agent fixes rather than rewrites
- The specific failure: validation output or the failed invariant with its rationale
- Any user guidance from `spec/resolve` (injected as `## User Guidance`)

`MaxRetries` (config, default 3) bounds the workflow's retry loop. After exhaustion the resource stays `rejected` and is triaged (§8.5, §8.6):

- **`spec/resolve`** — provide guidance (e.g., "the test expects the module at `src/synth/voice.rs` not `src/Synth/Voice.rs`"); the resource resets to `pending` and the next wave pass regenerates it with the guidance injected.
- **`spec/amend`** — fix the root cause in the CUE spec (add a missing dependency, relax an invariant, correct a validation command). The spec is re-loaded, hashes re-computed, and the resource regenerates with the updated declaration — cascading to downstream resources if a dependency changed.
- **`spec/skip`** — skip the resource entirely; the wave proceeds without it.

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
1. The workflow generates all resources in wave 0 — one sub-agent per resource, run by the workflow's `parallel(...)` (concurrency is the orchestrator's, not the server's; see §8.6).
2. Each sub-agent's `spec/commit` already ran the resource's validations. After the wave, **wave verification** (`VerifyWave`) runs the project-level `TypeCheckCommand` / `TestCommand` and any project-level validations against the whole tree.
3. If wave verification fails:
   - Errors are attributed to specific resources using file-path matching
   - The failed resources are reset and regenerated on the next `spec/next` pass with error context
   - Retry up to `WaveMaxRetries` times
4. If all pass, proceed to wave 1
5. Repeat until all waves complete

**Error attribution:** The verifier parses compiler/test output, looks for file paths in error messages, and maps them back to resources using the file-to-resource map. It uses a sliding window of +/-10 lines to find file references near error lines. `VerifyWave` itself is unchanged by the pivot — only the dispatch around it moved to the workflow.

### 8.4 Post-Wave Orchestrator Loop

After a wave's sub-agents return, the workflow (`.claude/workflows/spec-generate.js`) does not assume success. It collects each sub-agent's structured outcome (`committed` / `rejected` / `skipped` / `error`) and triages the failures before advancing:

```
spec/next -> wave N (resources + attempts + last_error)
  |
  +-- parallel(): one generator sub-agent per resource ----+
  |     each runs spec/context -> author -> judge ->        |
  |     spec/commit, retrying on rejection up to MaxRetries |
  |                                                         |
  +-- collect outcomes -------------------------------------+
  |     all committed?        --> loop: spec/next (wave N+1)|
  |     any not committed?     --> one triage agent each    |
  |                                                         |
  +-- verify (if any committed): spec/verify_wave ----------+
  |     project type-check/test + project validations;      |
  |     attributed failures routed back via spec/resolve    |
  |                                                         |
  +-- triage (per failure): spec/resolve OR spec/skip ------+
  |     resolve -> resource resets to pending; next         |
  |               spec/next re-serves it in the same wave   |
  |     skip    -> terminal; wave proceeds without it       |
  +---------------------------------------------------------+
```

The workflow surfaces its `triaged` list to the operator when it finishes (the skill reports committed/skipped/errored counts and the triage decisions). A typical surfaced summary:

```
Wave 2: 5 resources
  committed: VoiceAllocator, AudioRenderer, SynthEngine
  resolved:  CpalAudioOutput -- guidance: "use try_send + ring buffer"
  skipped:   FilterState     -- "contradictory invariant, deferring"
```

Resolution paths the triage agent (or the operator) uses:
- **`spec/resolve`** — provide guidance; the resource resets to `pending` so the next `spec/next` pass regenerates it
- **`spec/amend`** — fix the CUE spec, re-load, regenerate with the updated declaration
- **`spec/skip`** — skip the resource and continue
- Abort the session via `spec/finish` (with `force: true`)

**Stall guard.** `rejected` is not a terminal state server-side, so if triage fails to actually call `spec/resolve`/`spec/skip`, `spec/next` would re-serve the same wave forever. The workflow tracks the last wave index; after `MAX_STALLS` (2) repeat passes of the same wave, it **force-skips** the stragglers (`spec/skip` with an `auto-skipped: unresolved after N triage passes` reason) so the session can always make progress. Wave N+1 does not begin until every resource in wave N is `committed`, `skipped`, or terminally accounted for.

### 8.5 Resource State Machine

Each resource in a wave is tracked through a state machine in SQLite. The server owns the states; the workflow drives transitions by calling commit/resolve/skip. (The server no longer "dispatches" anything — a sub-agent generates and the workflow re-serves the resource on the next `spec/next` pass.)

**States** (`ResourceState`; `IsTerminal` = `committed`/`skipped`; `IsFailure` = `blocked`/`errored`/`timed_out`/`rejected`):

```
pending --> (generate) --> completed --> committed
   ^                          |
   |                          +-> rejected   (commit gate failed)
   |
Resolution (from any failure state):
  rejected  --[spec/resolve]--> pending   (guidance injected; regenerate next pass)
  rejected  --[spec/amend]----> pending   (fix spec, re-plan, regenerate)
  rejected  --[spec/skip]-----> skipped
  errored   --[spec/resolve]--> pending
  errored   --[spec/skip]-----> skipped
  blocked   --[spec/resolve]--> pending
  blocked   --[spec/skip]-----> skipped
  skipped   (terminal — wave proceeds without this resource)
  committed (terminal)
```

| State | Meaning |
|-------|---------|
| `pending` | In the plan, awaiting (re)generation. `spec/next` serves pending resources; `spec/resolve`/`spec/amend` reset a failed resource back here. |
| `completed` | `spec/commit` wrote files; validation in progress (transient). |
| `committed` | Files passed the commit gate and were recorded in state DB (terminal). |
| `rejected` | The commit gate failed (validation or a `passed:false` invariant verdict). The failure is stored for the next `spec/context`. **Not terminal** — the workflow retries, then triages. |
| `errored` | Generation failed before a clean commit (e.g., the sub-agent could not produce parseable files). |
| `blocked` / `timed_out` | Legacy failure states retained in the enum; the native workflow folds these into the rejected/errored retry+triage path. |
| `skipped` | Explicitly skipped (operator, triage agent, or stall guard); wave proceeds without it (terminal). |

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

`spec/commit` drives the core transitions inside the server; the workflow drives retry/triage around them (see §8.4):

- `pending` -> (sub-agent generates, `spec/commit`) -> `completed`
- `completed` -> commit gate (validations + invariant verdicts) all pass -> `committed`
- `completed` -> commit gate any fail -> `rejected` (failure stored for the next `spec/context`)
- `rejected` with attempts `< MaxRetries` -> the workflow re-pulls `spec/context` (failure injected) and regenerates
- `rejected` with attempts `>= MaxRetries` -> stays `rejected`; the workflow triages with `spec/resolve` (→ `pending`) or `spec/skip` (→ `skipped`); the stall guard force-skips after repeated wave passes

### 8.6 User Resolution & Dynamic Spec Updates

When the orchestrator encounters a non-happy-path state, it surfaces the issue to the user (the orchestrating AI agent or the human behind it). The user has three resolution paths:

#### Path 1: Resolve — Provide Guidance (`spec/resolve`)

The user answers a question or provides guidance; the resource resets to `pending` and the next `spec/next` pass regenerates it with the answer injected into the prompt.

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
5. Resets the resource so the next wave pass regenerates it from the updated declaration.
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

#### Workflow-driven vs. hands-on resolution

There is one execution model — the orchestrator drives it — but resolution can be automatic or operator-directed:

| Path | Behavior on non-happy-path |
|------|---------------------------|
| **Workflow (default)** | The workflow's triage agent inspects each persistent failure and itself calls `spec/resolve` (with concrete guidance) or `spec/skip`. The stall guard force-skips anything still stuck after repeated wave passes. The operator only sees the surfaced `triaged` summary at the end. |
| **Hands-on** | The operator drives the tools directly via the skill: `spec/status`/`spec/wave_status` show where a session stopped; they can read the error, inspect generated files, edit the CUE spec, and call `spec/resolve` / `spec/amend` / `spec/skip` before re-invoking the workflow (which resumes — `spec/next` re-serves non-terminal resources). |

### 8.6 Concurrency Model

Concurrency lives in the orchestrator, not the server. The workflow runs a wave's resources with `parallel(resources.map(...))` — one sub-agent per resource, all in flight at once; the degree of parallelism is whatever the Claude Code workflow runtime allows. Across waves, execution is sequential: wave N+1 does not begin until every resource in wave N is `committed`, `skipped`, or terminally accounted for.

The **server is a single-writer SQLite state engine**. Every `spec/commit` (and every other state-mutating tool) is a synchronous, serialized write against `.crest-spec/state.db` (WAL mode, `busy_timeout=5000`), and a session holds an exclusive lock for its duration. So even though many sub-agents generate in parallel, their commits are linearized by the database — there is no in-server concurrency semaphore and no subprocess pool to size.

---

## 8A. Amendments

Section 8.6 covers *in-flight* resolution: a resource fails mid-generation and the orchestrator unblocks it with `spec/resolve` / `spec/amend` / `spec/skip`. **Amendments** address a different problem — correcting a resource that already generated cleanly but whose committed output is wrong in some durable way (for example, a SOLID violation the orchestrator surfaces on a post-hoc review of the committed code).

> **Tombstone — `spec/deep_review` / `spec/propose_amendments`.** The server used to run an LLM review (`spec/deep_review`) and draft amendment proposals from it (`spec/propose_amendments`). Both were removed in the native-workflow pivot — the server runs no LLM. The orchestrator now drafts amendments itself (using its own review of the committed code) and feeds the approved `{name, prompt, finding}` proposals to the still-mechanical, human-gated write-back tools below. Where this section says an amendment's `origin` is `"deep_review"`, read it as a label for "came from an orchestrator review", not a server call.

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
| `VERIFIED` | The amendment's `validation` passed (or an orchestrator re-review no longer reports the finding), confirming the intent was actually achieved. |
| `GRADUATED` | Human-gated: the amendment's intent has been folded into the resource's canonical `invariants` and the amendment removed, so the spec describes the system cleanly instead of accumulating a patch log. A forced clean regeneration must still pass afterward. |
| `FAILED` | Validation never passes. Surfaced for re-draft; a partial fix is **not** committed. |

**Why graduate?** Without graduation a long-lived resource accumulates an ever-growing list of patch prompts. Graduation distills the durable intent ("reject degenerate reference pitch") into a first-class `invariant` and drops the transient amendment — the spec stays a clean description of the system, not a changelog.

### 8A.5 Tools

All amendment tools that write are **human-gated**: the write path takes an explicit `apply` flag, and with `apply=false` the tool returns the CUE diff for review and writes nothing.

Amendment drafting is **orchestrator-side** — there is no server tool that runs a review or drafts proposals. The orchestrator reviews the committed code with its own sub-agent, drafts `{name, prompt, finding}` proposals, and hands the approved ones to the mechanical write-back tools:

| Tool | Sync/Async | Purpose |
|------|-----------|---------|
| `spec/apply_amendments` | sync | Human-gated write-back of orchestrator-drafted `proposals` into the CUE spec as an override file. `apply=false` returns the CUE diff for review (writes nothing); `apply=true` writes it. After approval, the next `spec/plan` / `spec/begin` re-renders the resource in UPDATE mode. |
| `spec/list_amendments` | sync | Query the materialized `amendments` table, optionally filtered by `resource_id` and/or `state`. |
| `spec/graduate_amendment` | sync | Human-gated fold of a `VERIFIED` amendment (by `resource_id` + `name`) into the resource's canonical `invariants`, removing the amendment. `apply=false` previews the CUE diff; `apply=true` writes it. |

### 8A.6 Typical Flow

1. After a clean apply, the orchestrator reviews the committed code (its own sub-agent) to surface durable findings.
2. The orchestrator drafts `{name, prompt, finding}` proposals from the findings — no server call writes anything.
3. Review the proposals; pass the approved ones to `spec/apply_amendments` with `apply=false` to preview the CUE diff, then `apply=true` to write the override.
4. `spec/plan` / `spec/begin` now shows the resource as `~` (declaration changed) and re-renders it in **UPDATE mode** with the amendment prompts in the "CHANGES TO MAKE" block. The amendment becomes `APPLIED` once committed.
5. The amendment's `validation` runs on commit (mechanical, validation-gated); on pass the amendment is marked `VERIFIED`, on persistent failure `FAILED` (re-draft, don't commit a partial fix).
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

**Output serialization:** `writeResponse` marshals and writes a single line to stdout under `outMu` (mutex), so concurrent responses don't interleave.

#### Streamable HTTP Transport

Active when `HTTPAddr` is configured. A `net/http` server (stdlib, no framework).

- **Single endpoint:** `POST /mcp` — clients send JSON-RPC requests in the POST body.
- **All tools are synchronous:** the response is a plain JSON-RPC response with the result inline. (There are no async tools and no job IDs; see §9.4.)
- **Session management:** stateless — each request is independent.
- **Shutdown:** `http.Server.Shutdown(ctx)` with a 30s drain timeout.

Both transports share the same `Server` instance — same store, metrics, and tool definitions.

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

Every tool is a **spec tool** and every tool is **synchronous** — the server does no background work. There are no engine/sub-agent-execution tools; the orchestrator runs its own sub-agents.

#### Spec Lifecycle Tools

| Tool | Purpose |
|------|---------|
| `spec/plan` | Show what would change (dry run). Loads CUE, diffs against state, returns planned actions. |
| `spec/validate` | Check structural invariants against the spec without generating code. |
| `spec/begin` | Start a session: compute plan, create waves, acquire lock. Returns `session_id`, plan, waves, and `PendingDestroys`. |
| `spec/confirm_destroys` | Confirm and execute pending resource destroys for the given `resource_ids`. |
| `spec/next` | Get the next wave of uncommitted resources. Returns `done: true` when complete. |
| `spec/context` | Get the scoped generation prompt for a resource. Returns `SystemPrompt`, `Prompt`, and `Invariants` (each `{text, rationale}`), including any prior commit failure. |
| `spec/validate-resource` | Run invariant checks and optional type check/tests against files on disk for a specific resource. |
| `spec/note` | Save a design decision note for a resource. Notes are injected into downstream prompts. |
| `spec/commit` | Commit a resource: writes `files`, runs the resource's mechanical validations + amendment validations, and enforces the supplied `invariant_checks` verdicts. Any failure rejects the commit. Records a `Generation` labelled with `model`. See §4, §8.2. |
| `spec/resolve` | Provide guidance for a failed resource; resets it to `pending` so the next wave pass regenerates with the guidance injected. See §8.6. |
| `spec/amend` | Signal that the CUE spec has been updated for a resource. Re-loads spec, re-computes hashes, updates the plan, resets for regeneration. See §8.6. |
| `spec/skip` | Skip a failed resource. Transitions to `skipped` (terminal); wave proceeds without it. See §8.6. |
| `spec/evolve` | Build the reflection prompt from a session's failure history. Returns `{reflection_prompt}` (empty when nothing to learn). The orchestrator runs it and submits to `spec/record_learnings`. See §9.10/Evolution. |
| `spec/record_learnings` | Persist learnings distilled by a reflection run. Takes `{session_id, output}` (raw reflection output); returns `{learnings_added}`. |
| `spec/learnings` | List craft-level learnings extracted by reflection, filtered by status (default `active`). |
| `spec/promote_learnings` | Human-gated promotion of active learnings into the per-language learned prompt template (`apply=false` previews the markdown block; `apply=true` writes it). |
| `spec/apply_amendments` | Human-gated write-back of orchestrator-drafted amendment proposals into the CUE spec. `apply=false` returns the CUE diff; `apply=true` writes it. See §8A. |
| `spec/list_amendments` | Query the materialized `amendments` table, optionally filtered by `resource_id` and/or `state`. See §8A. |
| `spec/graduate_amendment` | Human-gated fold of a `VERIFIED` amendment into the resource's canonical `invariants` (then removes it). `apply=false` previews the diff; `apply=true` writes it. See §8A. |
| `spec/finish` | Finalize the session: release lock, return summary; returns a `reflection_prompt` when `EVOLVE` is `finish`/`all`. |
| `spec/status` | Show current state — resources in state, active session, lock status (or per-session wave progress when `session_id` is given). |
| `spec/wave_status` | Detailed per-resource view of a specific wave (state, attempts, errors). |
| `spec/verify_wave` | Run wave-level verification (project type-check/test commands + project-level validations, executed in the project root). Returns `Passed` plus per-resource attributed errors; route failures back via `spec/resolve`. See §8.4. |
| `spec/log` | List past applies with status. |
| `spec/history` | Show generation history for a specific resource. |
| `spec/graph` | Return the resource dependency graph. |
| `spec/diff` | Reconstruct state delta between two applies (`apply_a` vs `apply_b`). |
| `spec/state` | Inspect or modify state tracking. `list` returns all resources with hashes. `rm <resourceId>` removes a resource from state without deleting its code on disk (next plan treats it as `create`). |
| `spec/inspect` | Full debug view of a resource: effective prompt, hash breakdown, dependency chain, generated files, wave assignment. |
| `spec/prompt` | Build and return the full prompt for a resource without committing. |
| `spec/import` | Scan a directory of source files and generate a skeleton CUE spec (heuristic, no LLM). |
| `spec/bootstrap` | Check environment and set up crest-spec (spec dir, database, MCP config). Idempotent. |
| `spec/mode` | Show the current mode (environment). |
| `spec/vacuum` | Compact history older than a given date while preserving current state. |
| `spec/sql` | Open a read-only SQLite shell (SELECT only) against the state database. |
| `spec/unlock` | Force-clear a stale lock. |

#### Info Tools

| Tool | Purpose |
|------|---------|
| `about` | Static system info + a one-paragraph workflow guide ("state engine only; Claude Code orchestrates"). No subprocess. |
| `live_metrics` | Self-monitoring snapshot: uptime, call counts, error rates, per-tool stats. Recorded per tool call at dispatch (`handleToolCall` times every call and records into `metrics`). |

> **Tombstone — removed tools.** The native-workflow pivot deleted every async/subprocess tool: `run_prompt`, `poll_result`, `cancel_job`, `list_jobs`, `code_review`, `bugbot`, `list_models`, `status`, and the server-orchestration tools `spec/apply`, `spec/dispatch`, `spec/run_wave`, `spec/deep_review`, `spec/propose_amendments`. None of these have replacements *in the server* — the orchestrator's own sub-agents (and the spec-generate workflow) do that work.

All tools return structured JSON. The MCP protocol handles transport (stdio or HTTP).

### 9.4 Execution Model (synchronous)

> **Tombstone — async job model.** This section previously documented the claude-mcp-derived async job machinery (UUID job IDs, owner-PID capture, a `jobCtx` goroutine, `store.Complete`/`Cancel`/`Fail` outcome dispatch, and `notifications/progress` streaming). It was removed: no tool returns a job ID, nothing runs in a background goroutine, and there are no progress notifications.

Tool calls are handled synchronously in `handleToolCall`: dispatch to the tool's handler, time it, record the result in `metrics`, and return the JSON-RPC response inline. Long-running work (generation, retries, reflection) happens in the **orchestrator's** sub-agents, outside the server entirely.

### 9.5 Orchestrator Protocol

The protocol the orchestrator follows is delivered three ways, all consistent: the `initialize` response `instructions`, the `about` tool, and the `orchestrator_instructions` MCP prompt. In summary:

- **You are the orchestrator, not the server.** The server runs no LLM; you spawn the sub-agents.
- For each resource: call `spec/context` to get `SystemPrompt` + `Prompt` + `Invariants`, generate with a sub-agent (sonnet, never haiku), judge each invariant, and call `spec/commit` with `files` + `model` + `invariant_checks`.
- On `Committed=false`, call `spec/context` again (it now includes the failure) and retry up to `MaxRetries`, then `spec/resolve` or `spec/skip`.
- Resources within a wave run in parallel (the spec-generate workflow's `parallel(...)`); waves are sequential.
- Never write generated-resource code in the orchestrator's own context — every file comes from a sub-agent via `spec/commit`.

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
| `system_prompt` | The system prompt for sub-agents for a given project | (none) |
| `resource_prompt` | The full resource prompt for a specific resource | `resource_id` (string, required) |
| `orchestrator_instructions` | The orchestrator protocol instructions | (none) |

### 9.9 Recursion Guard (retired)

> **Tombstone.** The recursion guard (`DetectRecursion` / `processTree`, `--strict-mcp-config`, child-env filtering) existed because the server spawned `claude` subprocesses that could re-enter `crest-spec` as an MCP server. The server no longer spawns anything, so the recursion risk — and the guard — are gone. The orchestrator manages its own sub-agents and is responsible for not pointing them back at a generation loop.

### 9.10 Metrics

Lock-free per-tool counters using `atomic.Int64` for Calls, Errors, TotalNs, MinNs, MaxNs (min/max via CAS loops). `Metrics` holds a `map[string]*toolMetric` under an `RWMutex` and a start time. `Snapshot()` reports uptime, total calls/errors, and per-tool `{calls, errors, avg_ms, min_ms, max_ms}`. Every tool call is timed and recorded at dispatch (`handleToolCall`), keyed by tool name; `live_metrics` returns the snapshot.

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

- **Graceful shutdown** — SIGINT/SIGTERM via `signal.NotifyContext`; the HTTP server (if active) shuts down via `http.Server.Shutdown(ctx)` with a 30s drain; `store.Close()` is deferred. There are no subprocess trees to reap — the server spawns none.
- **Logging** — zerolog to stderr with timestamps; `Panic` (not `Fatal`) per convention so deferred cleanup runs.
- **Crash recovery** — all state is persistent SQLite, so a dead orchestrator/session leaves the DB intact. Re-invoking the workflow resumes: `spec/next` re-serves non-terminal resources. The `lock` table detects a stale session lock from a dead process; `spec/unlock` is the manual escape hatch.
- **Exclusive session lock** — the `lock` table prevents concurrent sessions from corrupting generation state. Combined with single-writer SQLite, all commits are linearized.

> **Tombstone.** The subprocess-robustness machinery this section used to list — `cmd.Cancel` + process groups + `WaitDelay`, the `MaxConcurrency` semaphore, claude config isolation, and PID-liveness job reconciliation — was removed with the agent/engine layers and the jobs system. None of it applies to a server that spawns nothing.

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

- `srv.Run(ctx)` lifecycle (the MCP server runs until the signal context is cancelled).
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

Auth: the server does not authenticate to any LLM — it spawns nothing. The orchestrator (Claude Code) uses its own session for the sub-agents it runs.

### 13.2 Mode 2: Dashboard

A monitoring interface that exposes API endpoints for inspecting system state. The dashboard provides visibility into active sessions, resource state, and generation history without going through the MCP protocol.

> **Tombstone.** The dashboard lost its **jobs** and **agent-events** views in the native-workflow pivot (there are no jobs and no server-side agent events to display). What remains is state/session/generation inspection.

### 13.3 Mode 3: CLI Subcommands

The orchestrating agent (or a human) runs these as short-lived shell commands to inspect or maintain SQLite state without going through the MCP protocol.

| Invocation | Behavior |
|------------|----------|
| `crest-spec dashboard [--addr :8080]` | Start the monitoring dashboard. |
| `crest-spec state list` | Print all resources in state with hashes and settle times. |
| `crest-spec state rm <resourceId>` | Remove a resource from state without deleting code on disk. Next plan treats it as `create`. |
| `crest-spec diff <apply_a> <apply_b>` | Show what changed between two applies (created, modified, destroyed resources). |
| `crest-spec vacuum --before <date>` | Compact history older than date. Deletes old generations, invariant checks, and apply records. |
| `crest-spec sql <query>` | Run a read-only SQL query against `.crest-spec/state.db`. |
| `crest-spec -h` / `--help` | Print usage + env-var table; exit 0. |

> **Tombstone — `crest-spec run` / `crest-spec check job`.** The unattended `run` driver and the async-job result collector (`check job`, which blocked on `WaitForCompletion` and consumed a job) were removed. There are no jobs to wait on; generation is driven by the Claude Code workflow, and there is no synchronous CLI apply.

### 13.4 Multi-Phase Agent Runner

`scripts/run-phased-agent.sh` drives crest-spec through all 10 crest-synth phases with state carry-over. For each phase it symlinks the `spec-generate` skill and workflow into the workspace's `.claude/` dir, then launches an interactive `claude` session (with Remote Control enabled) and tells it to *use the spec-generate skill* to run the full generation pipeline for that phase's spec. SQLite state carries between phases, so the planner only generates what changed.

### 13.5 Typical Orchestration Flow

There is one flow — the orchestrator drives it (the spec-generate skill automates it):

1. `spec/plan` -> see planned changes (empty ⇒ up to date, stop).
2. `spec/begin` -> `session_id`, plan, waves, `PendingDestroys`.
3. If `PendingDestroys` is non-empty, `spec/confirm_destroys` for the confirmed IDs.
4. Run the spec-generate workflow. Per wave (`spec/next`), one sub-agent per resource runs in parallel:
   - `spec/context` -> `SystemPrompt` + `Prompt` + `Invariants`
   - generate the files with the sub-agent (sonnet)
   - judge each invariant -> `{invariant, passed, summary}`
   - `spec/commit` with `files` + `model` + `invariant_checks` (the server validates and records)
   - on `Committed=false`, re-pull `spec/context` (failure injected) and retry up to `MaxRetries`
5. Persistent failures are triaged: `spec/resolve` (guidance, → pending) or `spec/skip`; the stall guard force-skips anything still stuck.
6. `spec/finish` -> finalize; if `reflection_prompt` is non-empty, run it with a sub-agent and submit via `spec/record_learnings`.

The orchestrator has full control — it can inspect prompts (`spec/inspect`/`spec/prompt`), edit the CUE spec to fix root causes (`spec/amend` / amendments), and decide how to triage. The `spec/amend` path is particularly powerful: a generation failure becomes a signal to improve the spec, not just retry harder.

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
Code is verified at commit time (mechanical validations + orchestrator-judged invariant verdicts), then again at the wave level (cross-resource type check and tests), with retries at each level.

### Audit Everything
Every generation, every prompt, every commit outcome, every retry, every failure reason is recorded in SQLite. You can replay, debug, and understand every decision the system made.

### The Server Validates, the Orchestrator Generates
The server never calls an LLM and never spawns a subprocess. It hands out scoped prompts (`spec/context`) and ingests generated files plus invariant verdicts (`spec/commit`), where it runs mechanical validations and enforces the verdicts. Generation — and all the LLM judgement that goes with it — happens in the orchestrator's sub-agents. This keeps the trusted core small, deterministic, and testable without a model.

### State Survives, Sessions Resume
All state lives in one SQLite database; a session holds an exclusive lock and is the unit of progress. If a workflow dies mid-run, `spec/status`/`spec/wave_status` show where it stopped and re-invoking the workflow resumes — `spec/next` re-serves non-terminal resources. There is no async job state to reconcile because there are no background jobs.

### MCP-Native
The Go binary is an MCP server, not a CLI with subcommands. An AI agent drives the lifecycle through structured tool calls. This makes crest-spec composable with any MCP-capable orchestrator.
