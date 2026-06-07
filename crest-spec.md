# crest-spec: Functional Specification

## Overview

crest-spec is a **declarative, domain-driven code generation system**. You describe your software architecture as CUE specification files using a schema rooted in Domain-Driven Design (DDD) vocabulary, then generate implementation code by dispatching each declared resource to an LLM sub-agent with surgically scoped prompts. The system tracks all state in SQLite, diffs specs against prior state to build execution plans, and enforces architectural invariants at every stage.

The mental model is **Terraform for code generation**: you declare what your system *should* look like, the tool plans what needs to change, and then applies those changes — with dependency ordering, retry loops, and verification gates.

**Runtime:** A standalone Go binary that acts as an MCP (Model Context Protocol) server. An AI agent (Claude Code, or any MCP-capable client) connects to it and drives the plan/apply lifecycle through MCP tool calls. The Go server parses CUE specs natively via `cuelang.org/go`, manages state in SQLite, builds prompts, and orchestrates wave-based code generation — no shelling out, no Node.js, no Bun.

---

## 1. The CUE DSL

The spec language is [CUE](https://cuelang.org) — a data constraint language written in Go with a native Go API. CUE replaces the TypeScript builder-pattern DSL while preserving the same resource model, dependency semantics, and meta inheritance.

### 1.1 Why CUE

CUE's design maps directly to crest-spec's core needs:

- **Unification model → Phase composition.** CUE files in the same package automatically merge. A `base.cue` defines the foundation; a `phase-3.cue` in the same directory extends and overrides it — no explicit import wiring. This is exactly the phase composition pattern from the TypeScript version, but native to the language instead of simulated through import chains.

- **Constraints → Invariants for free.** CUE's type system is a constraint system. An invariant like "must be 0-127" becomes `& >=0 & <=127` — validated at load time by the CUE evaluator, before any LLM is involved.

- **Go-native parsing.** The `cuelang.org/go` library parses and evaluates CUE directly in the Go process. No subprocess, no build step, no runtime dependency.

- **Declarative, not imperative.** CUE files are pure data with constraints — no side effects, no execution order. The resource graph is the spec, not a trace of builder calls.

- **Readable without tooling.** CUE is JSON-like with types and defaults. A spec file is self-documenting — you don't need to trace through builder methods to understand what's declared.

### 1.2 Design Philosophy

- **Spec files are CUE, not code.** You declare resources as structured data. CUE gives you types, constraints, defaults, and composition — but not loops, side effects, or runtime behavior. The spec is a static description.
- **DDD vocabulary is the schema.** Aggregates, value objects, entities, ports, adapters, domain services, repositories — each has a CUE definition with typed fields and constraints.
- **Metadata flows downward.** Project-level meta (language, style, avoid rules) merges into context-level meta, which merges into resource-level meta. CUE's unification handles the merge semantics naturally.
- **Dependencies are explicit.** References between resources (`uses`, `implements`, `of`) are string IDs resolved by the Go loader into the resource graph. This drives both prompt scoping and change cascading.
- **Phases are just files.** Place CUE files in the same package directory. The Go loader selects which files to include for a given phase. CUE's unification merges them — later declarations extend or override earlier ones. Same-name fields unify; new fields add. No import chains needed.

### 1.3 Phase Composition via CUE Unification

This is the key feature that CUE enables natively.

In the TypeScript version, phases were chained through explicit imports:
```
phase-3.ts → imports phase-2.ts → imports phase-1.ts → imports base.ts
```

In CUE, phases are just files in the same package. The Go loader controls which files are included:

```
spec/
  base.cue          ← always loaded
  phase-1.cue       ← loaded for phase ≥ 1
  phase-2.cue       ← loaded for phase ≥ 2
  phase-3.cue       ← loaded for phase ≥ 3
```

When loading phase 3, the server loads `base.cue + phase-1.cue + phase-2.cue + phase-3.cue`. CUE unifies them into a single value:

- New contexts/resources in `phase-3.cue` are added to the graph.
- Redeclared resources (like `assets: LibRs: {...}`) unify — later fields extend or override earlier ones.
- Constraints accumulate — an invariant added in phase 3 persists through all later phases.
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
    {
        text: "audio thread must never perform blocking I/O"
        meta: rationale: "file/network I/O has unpredictable latency incompatible with audio deadlines"
    },
    {
        text: "all parameter changes cross the boundary via ParameterBridge or EventRingBuffer"
        meta: rationale: "enforces the lock-free seam; no shared mutable state between threads"
    },
]
```

Invariants are checked both structurally (at plan time) and against generated code (during the constraint loop). The `rationale` is injected into prompts so the LLM understands *why*.

Since phases unify, invariants declared in phase 3 automatically accumulate into phases 4-10 without re-declaration.

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
    ...                       // Extensible
}
```

Meta merges hierarchically via CUE unification: project meta → context meta → resource meta. List fields concatenate; scalar fields from more-specific levels override less-specific ones.

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
| `consumes` | Resource consumes events from another |
| `publishes` | Resource publishes events |

In CUE, dependencies are expressed as ID strings (e.g., `uses: ["aggregate.Synth.Voice"]`). The Go loader resolves these into the resource graph at load time, validating that all referenced IDs exist.

---

## 2. The Terraform-Inspired Lifecycle

The lifecycle is the same as before — plan, apply, verify — but exposed as MCP tools instead of CLI commands.

### 2.1 Plan — `spec/plan`

**What it does:**
1. Loads the CUE spec files for the selected phase (Go loader selects which `.cue` files to include, CUE evaluator unifies them into a single value)
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

**Structural kinds** (`project`, `context`, `assetKind`) are skipped during planning — they are metadata containers, not generated resources.

### 2.2 Apply — `spec/apply`

**What it does:**
1. Runs the planner to build the execution plan
2. Acquires an exclusive lock in SQLite (prevents concurrent applies)
3. Executes the plan in two phases:

   **Phase A — Destroys:** Deletes files and state for removed resources.

   **Phase B — Generates:** For each `create` or `modify` action:
   - Builds a full prompt (system prompt + resource prompt + context layers)
   - Runs the Constraint Loop (generate → verify → retry)
   - Writes files to disk
   - Records everything in SQLite

4. Releases the lock

**Two execution modes:**

| Mode | Behavior |
|------|----------|
| **Flat** (default) | Each resource goes through the constraint loop independently |
| **Wave-based** (incremental) | Resources are grouped into dependency waves; verification runs between waves |

### 2.3 Targeting and Forcing

**Target:** Filters the plan to only act on a specific resource and its cascading dependents. Useful for iterating on a single resource without re-generating the whole project.

**Force:** Bypasses the hash-based skip logic to force regeneration even when the spec hasn't changed.

---

## 3. Prompt Construction

The prompt system builds layered, surgically scoped prompts for each resource. There are two parts: the system prompt (shared across all resources in a project) and the resource prompt (unique per resource).

### 3.1 System Prompt

Built from project-level meta. Contains:

1. **Role definition**: `"You are a {language} code generator following strict SOLID principles."`
2. **Output format**: Fenced code blocks with path annotations (`// path: src/Context/Resource.ext`)
3. **Folder structure**: `src/{ContextName}/{ResourceName}/` — grouped by resource, not by architectural layer
4. **SOLID principles**: Mandatory rules for dependency injection, SRP, DIP, ISP, OCP
5. **Language-specific rules**: For Rust: module tree casing, `use` path conventions, avoid unstable APIs
6. **Code style**: From `meta.style`
7. **Avoid list**: From `meta.avoid` — anti-patterns the generated code must not exhibit
8. **Output requirement**: Both implementation files and unit tests

### 3.2 Resource Prompt

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

### 3.3 Runtime Context Injection

At apply time, the prompt is augmented with runtime context:

1. **Module tree context**: Scans the existing `src/` directory tree and injects the full module structure so the LLM uses correct `use` paths and casing — crucial for Rust where module paths must match directory structure exactly.

2. **Existing files from dependencies**: If the resource depends on other resources that were already generated in this apply, their file contents are injected so the LLM can import from them correctly. Test files are excluded.

3. **Wave error context**: If the resource failed a wave-level verification (type check or test), the error output is appended with the instruction: *"The previous generation caused build errors. Fix these errors in your output."*

4. **Agent notes from dependencies**: When a sub-agent generates a resource, it can leave "notes" (design decisions, implementation choices). These notes are injected into downstream resources' prompts as `## Notes from dependencies`.

### 3.4 The Fix Prompt

When the constraint loop retries, it builds a "fix prompt" that includes:
1. The original resource prompt
2. The previous attempt's output (all generated files)
3. The specific error to fix

This gives the LLM full context on what it produced and exactly what went wrong.

---

## 4. SQLite as the Integration Layer

SQLite is the single source of truth for all system state. It serves three roles:

### 4.1 State Tracking (What exists)

**`resources` table:** Tracks every resource that has been successfully generated.
- `declaration_hash`: Hash of the resource's declaration (detects spec changes)
- `effective_hash`: Hash including all transitive dependencies (detects cascading changes)
- `settled_at`: When this resource was last successfully generated

**`generated_files` table:** Maps file paths to the resources that generated them.
- `content_hash`: SHA256 of file content
- `prompt_hash`: Hash of the prompt that produced this file
- `model`: Which LLM model generated it

**`dependencies` table:** Stores the resource dependency graph for change detection.

### 4.2 Audit Trail (What happened)

**`applies` table:** Records every plan execution with status, timestamps, and spec hash.

**`apply_actions` table:** What action was taken per resource per apply (create/modify/destroy with outcome).

**`generations` table:** Full audit of every LLM invocation:
- Complete prompt text and output text
- Model ID, prompt hash
- Retry count and outcome (accepted/rejected)
- Rejection reason (if failed)

**`invariant_checks` table:** Records every invariant check result per apply.

### 4.3 Agent Communication (Who knows what)

**`agent_sessions` table:** Stores the active agent orchestration session:
- Serialized plan, wave groupings, and effective hashes
- Enables the orchestrator to resume, query, and advance through the plan

**`agent_notes` table:** Notes left by sub-agents during generation:
- Keyed by `(resource_id, apply_id)`
- Injected into downstream resources' prompts so later agents can learn from earlier ones' decisions
- Indexed for fast lookup

**`lock` table:** Single-row table (id=1) for exclusive apply lock:
- Prevents concurrent applies from corrupting state
- Records holder and acquisition time

### 4.4 How SQLite Enables Sub-Agent Communication

The orchestrator–sub-agent flow uses SQLite as the coordination layer, accessed through MCP tools:

1. **Orchestrator calls `spec/begin`** → Creates an apply record, computes the plan, stores it as JSON in `agent_sessions`, acquires the lock.
2. **Orchestrator calls `spec/next`** → Queries `agent_sessions` for the plan, queries `apply_actions` for completed resources, returns the next uncommitted wave.
3. **Orchestrator calls `spec/context`** → Builds the prompt for the resource, queries `agent_notes` for dependency notes, returns the combined prompt.
4. **Sub-agent generates code** → Orchestrator writes files to disk.
5. **Orchestrator calls `spec/note`** → Writes the sub-agent's design decisions to `agent_notes`.
6. **Orchestrator calls `spec/commit`** → Records the resource state, files, and action outcome.
7. **Repeat** until `spec/next` returns `done: true`.
8. **Orchestrator calls `spec/finish`** → Finalizes the apply, releases the lock, returns summary.

The SQLite database is the *only* shared state between the orchestrator and sub-agents. Sub-agents never touch the database directly — all writes go through MCP tool calls.

---

## 5. The Plan/Apply/Dispatch/Retry Loop

### 5.1 High-Level Flow

```
┌─────────────────────────────────────────────────────────────┐
│                    Spec Files (.cue)                        │
│  project: contexts: Synth: aggregates: Voice: {...}        │
└─────────────┬───────────────────────────────────────────────┘
              │ CUE load + unify
              ▼
┌─────────────────────────────────────────────────────────────┐
│              Resource Registry (in-memory graph)            │
└─────────────┬───────────────────────────────────────────────┘
              │ diff against
              ▼
┌─────────────────────────────────────────────────────────────┐
│             SQLite State Database                           │
│  effective_hash comparison → PlannedAction[]                │
└─────────────┬───────────────────────────────────────────────┘
              │ plan
              ▼
┌─────────────────────────────────────────────────────────────┐
│                    Plan                                     │
│  + aggregate.Synth.Voice (new)                             │
│  ~ domainService.Synth.Allocator (dependency changed)      │
│  - aggregate.Audio.SineVoice (removed)                     │
└─────────────┬───────────────────────────────────────────────┘
              │ apply (flat or wave-based)
              ▼
┌─────────────────────────────────────────────────────────────┐
│              Apply Engine                                   │
│  For each planned action:                                  │
│    1. Build prompt (system + resource + context)            │
│    2. Run Constraint Loop                                  │
│    3. Write files / update state                           │
└─────────────┬───────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│            Constraint Loop (per resource)                   │
│                                                             │
│  ┌──► Generate (LLM call)                                  │
│  │        │                                                 │
│  │        ▼                                                 │
│  │    Parse response → extract code blocks                  │
│  │        │                                                 │
│  │        ▼                                                 │
│  │    Type Check (optional, runs compiler)                  │
│  │        │                                                 │
│  │        ▼                                                 │
│  │    Invariant Check (structural rules)                    │
│  │        │                                                 │
│  │        ▼                                                 │
│  │    Test Run (optional, runs test suite)                  │
│  │        │                                                 │
│  │        ▼                                                 │
│  │    LLM Verify (second LLM pass for SOLID review)        │
│  │        │                                                 │
│  │        ▼                                                 │
│  │    Pass? ──yes──► Return files                           │
│  │        │                                                 │
│  │       no                                                 │
│  │        │                                                 │
│  └── Build fix prompt (original + previous output + error)  │
│      Retry up to maxRetries                                │
└─────────────────────────────────────────────────────────────┘
```

### 5.2 The Constraint Loop in Detail

The constraint loop is the core verification engine. For each resource, it:

1. **Generates**: Sends `systemPrompt + resourcePrompt` to the LLM.
2. **Parses**: Extracts fenced code blocks with `// path:` or `# path:` annotations. If no parseable blocks → retry.
3. **Type Checks**: If a `typeCheckCommand` is configured (e.g., `["cargo", "check"]`), runs it. If it fails → retry with compiler errors in the prompt.
4. **Invariant Checks**: Runs invariant rules against the parsed files. If violations → retry with violation details.
5. **Test Runs**: If a `testCommand` is configured, runs it. If it fails → retry with test errors.
6. **LLM Verify**: Sends the generated code plus the original requirements to the LLM as a *reviewer*, asking it to check SOLID principles, folder structure, DI, interfaces, and tests. If it returns `FAIL` → retry with the issues.

On retry, the fix prompt includes:
- The original resource prompt (full requirements)
- The previous attempt's output (all files)
- The specific error message

This gives the LLM maximum context to understand what it got wrong.

### 5.3 Wave-Based Execution (Incremental Mode)

Resources are organized into dependency waves:

```
Wave 0: [valueObject.Kernel.NoteId, valueObject.Kernel.Velocity, ...]
Wave 1: [aggregate.Synth.Voice, port.Shell.AudioOutput, ...]
Wave 2: [domainService.Synth.VoiceAllocator, adapter.CpalAudioOutput, ...]
Wave 3: [asset.ToneTestMain, ...]
```

**Algorithm:** Each resource is assigned to `wave = max(dependency_waves) + 1`. Resources with no dependencies go in wave 0.

**Execution:**
1. Generate all resources in wave 0 (with concurrency)
2. Run verification (type check + tests) against the whole project
3. If verification fails:
   - Attribute errors to specific resources using file path matching
   - Re-dispatch only the failed resources with error context
   - Retry up to `waveMaxRetries` times
4. If all pass, proceed to wave 1
5. Repeat until all waves complete

**Error attribution:** The verifier parses compiler/test output, looks for file paths in error messages, and maps them back to resources using the file-to-resource map. It uses a sliding window of ±10 lines to find file references near error lines.

### 5.4 Post-Wave Orchestrator Loop

After dispatching all sub-agents in a wave, the orchestrator enters a poll-based check loop instead of assuming everything succeeded:

```
dispatch wave N
  │
  ├── poll states from SQLite ──────────────────────────┐
  │                                                     │
  │   all completed/committed?  ──> advance to wave N+1 │
  │   any blocked?              ──> report to user      │
  │   any errored (retryable)?  ──> re-dispatch         │
  │   any errored (exhausted)?  ──> report to user      │
  │   any timed_out?            ──> report to user      │
  │   still dispatched?         ──> wait + re-poll      │
  │                                                     │
  └─────────────────────────────────────────────────────┘
```

The orchestrator **reports back to the user** for any non-happy-path state instead of silently skipping or failing at `finish`. The user gets a summary like:

```
Wave 2: 5 resources
  completed: VoiceAllocator, AudioRenderer, SynthEngine
  blocked:   CpalAudioOutput — "need user decision: try_send vs send for backpressure"
  errored:   FilterState — compile error (attempt 2/3, retrying)
```

The user can then:
- Answer the question (unblocks the blocked agent)
- Force-retry an errored resource
- Skip a resource and continue
- Abort the session

This loop runs per wave — wave N+1 does not begin until every resource in wave N has reached `committed`, `rejected` (with retries exhausted), or been explicitly skipped by the user.

### 5.5 LLM Invocation

The Go server invokes the LLM via the Claude CLI as a subprocess:

```
claude -p --model <modelId> --disallowedTools Bash Read Edit Write Glob Grep WebFetch WebSearch
```

Tool access is explicitly disabled — the sub-agent generates pure code output, no tool calls. The Go server handles all file I/O.

---

## 6. MCP Server Interface

The Go binary exposes the plan/apply lifecycle as MCP tools. An AI agent (Claude Code, or any MCP-capable client) connects to it and drives the lifecycle through tool calls.

### 6.1 MCP Tools

| Tool | Purpose |
|------|---------|
| `spec/plan` | Show what would change (dry run). Loads CUE, diffs against state, returns planned actions. |
| `spec/apply` | Execute the plan. Generates code, writes files, updates state. Supports flat and wave-based modes. |
| `spec/status` | Show current state — resources in state, active session, lock status. |
| `spec/validate` | Check structural invariants against the spec without generating code. |
| `spec/begin` | Start an interactive agent session: compute plan, create waves, acquire lock. Returns plan and orchestrator instructions. |
| `spec/next` | Get the next wave of uncommitted resources. Returns `done: true` when complete. |
| `spec/context` | Get the scoped prompt for a specific resource. Returns `systemPrompt`, `prompt`, `dependencyNotes`, and `dispatchInstructions`. |
| `spec/validate-resource` | Run invariant checks and optional type check/tests against files on disk for a specific resource. |
| `spec/note` | Save a design decision note for a resource. Notes are injected into downstream prompts. |
| `spec/commit` | Record a resource as complete in state. |
| `spec/finish` | Finalize the session: release lock, return summary. |
| `spec/log` | List past applies with status. |
| `spec/history` | Show generation history for a specific resource. |
| `spec/graph` | Return the resource dependency graph. |
| `spec/unlock` | Force-clear a stale lock. |

All tools return structured JSON. The MCP protocol handles transport (stdio or SSE).

### 6.2 Orchestrator Protocol

The `spec/begin` response includes `orchestratorInstructions` — a block of text that tells the calling agent exactly how to behave:

- **You are a dispatcher, not a code generator.** Do not write code yourself.
- For each resource: call `spec/context` to get its prompt, spawn a sub-agent with that prompt, parse the output, write files, call `spec/commit`.
- Resources within the same wave can be dispatched in parallel.
- Waves must be processed sequentially.

### 6.3 Sub-Agent Communication via Notes

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

### 6.4 Sub-Agent State Machine

Each resource in a wave is tracked through a state machine in SQLite. This replaces the binary "committed or not" model and gives the orchestrator (and the user) visibility into exactly what each sub-agent is doing.

**States:**

```
pending ──> dispatched ──> completed ──> committed
                │                │
                ├──> blocked     │
                ├──> errored     └──> rejected
                └──> timed_out
```

| State | Meaning |
|-------|---------|
| `pending` | In the plan, not yet dispatched to a sub-agent |
| `dispatched` | Sub-agent has been spawned and is working |
| `completed` | Sub-agent returned output; files written but not yet committed |
| `committed` | Files validated and recorded in state DB |
| `blocked` | Sub-agent needs a decision from the user or another resource |
| `errored` | Sub-agent failed (compile error, invariant violation, crash) |
| `timed_out` | Sub-agent didn't respond within deadline |
| `rejected` | Validation failed after completion; needs re-dispatch |

**Blocked context:**

When a sub-agent reports `blocked`, it stores structured context so the orchestrator can present actionable information to the user:

```cue
#BlockedContext: {
    resourceId: string
    reason:     string           // human-readable summary
    blockedOn?: string           // another resourceId, if dependency-blocked
    question?:  string           // question for the user, if decision-blocked
    retryable:  bool             // can the orchestrator retry automatically?
}
```

**Errored context:**

```cue
#ErrorContext: {
    resourceId: string
    errorKind:  "compile" | "invariant" | "runtime" | "parse" | "unknown"
    message:    string
    files?: [...string]          // which generated files were involved
    retryCount: int              // how many times we've retried
    maxRetries: int              // give up after this many
}
```

**State transitions and the post-wave loop:** The orchestrator polls resource states after dispatching a wave (see §5.4). A `completed` resource runs through resource-specific validations (see `validations` in §1.4) — if validations pass, it transitions to `committed`; if they fail, it transitions to `rejected` with the validation output attached, triggering re-dispatch with the failure details in the fix prompt. An `errored` resource with `retryCount < maxRetries` is automatically re-dispatched. All other non-terminal states (`blocked`, `timed_out`, exhausted `errored`) are reported to the user for manual resolution.

This is how information flows between independently spawned sub-agents — through SQLite via MCP tools, not through shared context.

---

## 7. The crest-synth Reference: Phase Composition in CUE

The synthesizer architecture from the TypeScript version translates directly to CUE, demonstrating how phase composition works with unification.

### Directory Structure

```
crest-synth/
  base.cue           ← project config, kernel types, shell ports, asset kinds
  phase-1.cue         ← Audio context (throwaway sine voice)
  phase-2.cue         ← Synth context (replaces Audio with real polyphonic engine)
  phase-3.cue         ← RealTime context (lock-free boundary, CpalAudioOutput adapter)
  phase-4.cue         ← Patch context (multi-patch, channel dispatch, MPE zones)
  phase-5.cue         ← Modulation context (mod matrix, LFOs, per-note expression)
  phase-6.cue         ← SampleLibrary context (SF2/WAV loading)
  phase-7.cue         ← Effects context (per-patch and global FX chains)
  phase-8.cue         ← Presets context (save/load, preset banks, session snapshots)
  phase-9.cue         ← Shell additions (gamepad, all adapters, full context map)
  phase-10.cue        ← Plugin context (nih-plug CLAP/VST3 wrapper)
```

### How Phase Selection Works

To load phase 3, the Go server includes: `base.cue + phase-1.cue + phase-2.cue + phase-3.cue`

CUE unifies all four files. The result contains:
- Everything from `base.cue` (Kernel, Shell, asset kinds)
- Audio context from `phase-1.cue`
- Synth context from `phase-2.cue` (Voice aggregate, VoiceAllocator, AudioRenderer)
- RealTime context from `phase-3.cue` (lock-free boundary, BoundaryMessage, ParameterSnapshot)
- CpalAudioOutput adapter from `phase-3.cue`
- Real-time safety invariants from `phase-3.cue`
- Updated assets (LibRs redeclared in `phase-3.cue` to add RealTime module)

### Phase Progression

| Phase | Name | New Contexts | Key Concepts |
|-------|------|-------------|--------------|
| Base | Foundation | Kernel, Shell | Project config, kernel types, shell ports, asset kinds |
| 1 | Plumbing that makes noise | Audio | Throwaway sine voice, prove MIDI-to-sound path |
| 2 | Real polyphonic engine | Synth (replaces Audio) | Voice aggregate, oscillator/filter/envelope, voice stealing |
| 3 | Harden the real-time seam | RealTime | Lock-free boundary (rtrb, triple_buffer, basedrop), CpalAudioOutput adapter |
| 4 | Multiple patches | Patch | Per-patch voice pools, channel dispatch, MPE zones, global mixer |
| 5 | Modulation | Modulation | Mod matrix, LFOs, envelopes, per-note expression (MPE-ready) |
| 6 | Sample playback | SampleLibrary | SF2/WAV loading, key/velocity zones, interpolation |
| 7 | Effects | Effects | Per-patch and global FX chains (reverb, chorus, delay) |
| 8 | Presets | Presets | Save/load patches, preset banks, full session snapshots |
| 9 | Gamepad UX | (Shell additions) | Controller input, all 8 infrastructure adapters, full context map |
| 10 | Plugin wrapper | Plugin | nih-plug shell for CLAP/VST3, parameter mapping |

### Architectural Invariants Accumulate

Phase 3 establishes the critical real-time safety invariants:
- Audio thread never allocates
- Audio thread never locks
- Audio thread never blocks on I/O
- All parameter changes cross the lock-free boundary
- Memory retired via deferred deallocator

Because CUE unifies files in the same package, these invariants persist through all subsequent phases automatically — no re-declaration needed.

---

## 8. Architecture Summary

### What's in the Go Binary

| Component | Responsibility |
|-----------|---------------|
| **CUE Loader** | Loads `.cue` files for a given phase, evaluates via `cuelang.org/go`, parses into resource registry |
| **Resource Registry** | In-memory directed acyclic graph of all resources with topological sort and dependency queries |
| **Hash Computer** | Computes effective hashes (declaration + meta + invariants + dependency hashes + model ID) |
| **Planner** | Diffs registry against SQLite state to produce planned actions (create/modify/destroy) |
| **Prompt Builder** | Builds system prompts and resource prompts from registry descriptors and meta |
| **Constraint Loop** | Generate → parse → type check → invariant check → test → LLM verify → retry |
| **Wave Computer** | Topological sort of planned actions into dependency waves for parallel execution |
| **Wave Verifier** | Runs verification commands between waves, attributes errors to resources |
| **Apply Engine** | Orchestrates the full apply lifecycle (flat or wave-based) |
| **State Database** | SQLite wrapper (resources, files, applies, generations, sessions, notes, lock) |
| **MCP Server** | Exposes all functionality as MCP tools over stdio or SSE transport |
| **Response Parser** | Extracts fenced code blocks with path annotations from LLM output |

### What's NOT in the Go Binary

- **No TypeScript / Bun / Node.js.** The entire runtime is a single Go binary.
- **No CUE CLI dependency.** CUE is parsed natively via the Go library.
- **No API keys in the binary.** LLM calls go through the Claude CLI subprocess, which handles authentication.

---

## 9. Design Principles

### Specification over Implementation
The spec describes *what* the system should look like, not *how* to build it. The LLM handles the *how*.

### Change Detection via Content Hashing
Effective hashes include the full dependency chain and the model identifier. Changing the LLM model triggers regeneration of everything — because the output would differ.

### Cascading Changes
A change to a value object cascades to every aggregate that uses it, every service that uses those aggregates, every asset that references any of them. The dependency graph ensures nothing is stale.

### Phase Composition via Unification
CUE's unification model makes phase composition a first-class language feature. No import chains, no re-exports, no builder pattern. Files in the same package merge automatically. Later declarations extend or override earlier ones.

### Verification at Every Level
Code is verified by the constraint loop (parse → type check → invariant check → test → LLM review), then again at the wave level (cross-resource type check and tests), with retries at each level.

### Audit Everything
Every LLM call, every prompt, every output, every retry, every failure reason is recorded in SQLite. You can replay, debug, and understand every decision the system made.

### The LLM is a Constrained Worker
Sub-agents have no tool access. They receive a prompt and produce code blocks with path annotations. The orchestrator handles all file I/O, state management, and verification. The LLM never touches the filesystem.

### MCP-Native
The Go binary is an MCP server, not a CLI with subcommands. An AI agent drives the lifecycle through structured tool calls. This makes crest-spec composable with any MCP-capable orchestrator — Claude Code, custom agents, CI pipelines with MCP clients.
