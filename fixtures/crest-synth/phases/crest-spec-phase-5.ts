import { project, command, event, invariant, layer } from "../../../src/index.js";

// Phase 5: Modulation
// Envelopes, LFOs, a routing matrix to a handful of destinations (pitch, cutoff, gain),
// with gamepad-driven assignment. Per-note expression reserved as first-class mod sources
// to keep the MPE door open.

const app = project("crest-synth", {
  layers: ["domain", "application", "infrastructure"],
  rules: [
    layer("domain").dependsOn([]),
    layer("application").dependsOn(["domain"]),
    layer("infrastructure").dependsOn(["domain", "application"]),
  ],
  meta: {
    language: "rust",
    style: "idiomatic Rust; lock-free audio thread; gamepad-driven UI",
    avoid: [
      "heap allocation on audio thread",
      "mutex locks on audio thread",
      "blocking I/O on audio thread",
    ],
  },
});

// ── Kernel ──────────────────────────────────────────────

const kernel = app.context("Kernel", {
  purpose: "shared value types for MIDI addressing, audio primitives, and note identity",
  ubiquitousLanguage: {
    "MidiEvent": "normalized internal event addressed by (group, channel) with high-res values and note-id",
    "NoteId": "unique identifier for a sounding note, enabling per-note expression",
    "ChannelAddress": "a (group, channel) pair — 256 addressable destinations",
  },
});

kernel.valueObject("MidiGroup", {
  from: "u8",
  description: "MIDI 2.0 group index (0-15)",
  invariants: ["must be 0-15"],
});

kernel.valueObject("MidiChannel", {
  from: "u8",
  description: "MIDI channel (0-15 within a group)",
  invariants: ["must be 0-15"],
});

kernel.valueObject("ChannelAddress", {
  state: { group: "MidiGroup", channel: "MidiChannel" },
  description: "a (group, channel) pair — the 256-destination address space for MIDI 2.0",
});

kernel.valueObject("NoteId", {
  from: "u32",
  description: "unique identifier for a sounding note, enabling per-note expression",
});

kernel.valueObject("NoteNumber", {
  from: "u8",
  description: "MIDI note number (0-127)",
  invariants: ["must be 0-127"],
});

kernel.valueObject("Velocity", {
  from: "f64",
  description: "normalized note velocity (0.0-1.0), upconverted from MIDI 1.0 7-bit to high-res",
  invariants: ["must be 0.0-1.0"],
});

kernel.valueObject("MidiEvent", {
  state: {
    group: "MidiGroup",
    channel: "MidiChannel",
    noteId: "NoteId",
    kind: "MidiEventKind",
    noteNumber: "NoteNumber",
    velocity: "Velocity",
    value: "f64",
  },
  description: "normalized internal event: (group, channel) addressed, high-res values, note-id tagged",
});

kernel.valueObject("SampleRate", {
  from: "u32",
  description: "audio sample rate in Hz (e.g. 44100, 48000)",
  invariants: ["must be positive"],
});

kernel.valueObject("AudioFrame", {
  state: { left: "f32", right: "f32" },
  description: "one stereo sample pair",
});

kernel.valueObject("Frequency", {
  from: "f64",
  description: "frequency in Hz",
  invariants: ["must be positive"],
});

kernel.valueObject("Amplitude", {
  from: "f64",
  description: "linear amplitude (0.0 = silence, 1.0 = unity)",
  invariants: ["must be non-negative"],
});

// ── Shell ───────────────────────────────────────────────

const shell = app.context("Shell", {
  purpose: "application shell: wires audio output, MIDI input, and the window to the engine",
});

shell.port("AudioOutput", {
  contract: {
    openStream: "SampleRate -> AudioStream",
    writeBuffer: "[AudioFrame] -> ()",
  },
});

shell.port("MidiInput", {
  contract: {
    listPorts: "() -> Vec<MidiPortInfo>",
    connect: "MidiPortId -> MidiConnection",
    nextEvent: "() -> Option<RawMidiMessage>",
  },
});

shell.port("MidiNormalizer", {
  contract: {
    normalize: "RawMidiMessage -> MidiEvent",
  },
});

shell.port("AppWindow", {
  contract: {
    create: "WindowConfig -> Window",
    runLoop: "FrameCallback -> ()",
  },
});

// ── Synth ───────────────────────────────────────────────

const synth = app.context("Synth", {
  purpose: "polyphonic synthesis engine: voice management, oscillator, filter, envelope",
  ubiquitousLanguage: {
    "Voice": "a single sounding note with its own oscillator, filter, and envelope state",
    "VoiceStealing": "reusing the oldest or quietest voice when polyphony limit is reached",
    "EnvelopeStage": "current phase of an ADSR envelope: attack, decay, sustain, release, idle",
  },
});

synth.valueObject("EnvelopeStage", {
  from: "enum",
  description: "ADSR envelope phase: Idle, Attack, Decay, Sustain, Release",
});

synth.valueObject("OscillatorConfig", {
  state: {
    waveform: "Waveform",
    detune: "f64",
    pulseWidth: "f64",
  },
  description: "oscillator parameters: waveform shape, detune in cents, pulse width for square",
});

synth.valueObject("FilterConfig", {
  state: {
    cutoff: "Frequency",
    resonance: "f64",
    filterType: "FilterType",
  },
  description: "resonant filter parameters",
  invariants: ["resonance must be 0.0-1.0", "cutoff must be within audible range"],
});

synth.valueObject("AmpEnvelopeConfig", {
  state: {
    attack: "f64",
    decay: "f64",
    sustain: "f64",
    release: "f64",
  },
  description: "ADSR envelope times (seconds) and sustain level (0.0-1.0)",
  invariants: [
    "attack, decay, release must be non-negative",
    "sustain must be 0.0-1.0",
  ],
});

const voice = synth.aggregate("Voice", {
  root: true,
  purpose: "a single sounding note: oscillator + filter + amp envelope + per-note mod inputs",
  state: {
    noteId: "NoteId",
    noteNumber: "NoteNumber",
    velocity: "Velocity",
    frequency: "Frequency",
    oscillatorPhase: "f64",
    filterState: "FilterState",
    envelopeStage: "EnvelopeStage",
    envelopeLevel: "Amplitude",
    perNoteExpression: "PerNoteExpression",
    active: "bool",
  },
  commands: [
    command("NoteOn", { noteId: "NoteId", noteNumber: "NoteNumber", velocity: "Velocity" }),
    command("NoteOff", { noteId: "NoteId" }),
    command("UpdatePerNoteExpression", { noteId: "NoteId", expression: "PerNoteExpression" }),
  ],
  events: [
    event("VoiceActivated", { noteId: "NoteId", noteNumber: "NoteNumber", frequency: "Frequency" }),
    event("VoiceReleased", { noteId: "NoteId" }),
    event("VoiceFinished", { noteId: "NoteId" }),
    event("VoiceStolen", { oldNoteId: "NoteId", newNoteId: "NoteId" }),
  ],
  invariants: [
    "frequency derived from noteNumber and any pitch modulation",
    "envelope progresses Idle -> Attack -> Decay -> Sustain -> Release -> Idle",
    "voice is reclaimable only when envelope reaches Idle",
    "per-note expression reaches the voice, not just the patch",
  ],
});

synth.port("SynthEngine", {
  contract: {
    renderBlock: "(Voice, OscillatorConfig, FilterConfig) -> [AudioFrame]",
    noteOn: "(Voice, NoteOn) -> Voice",
    noteOff: "(Voice, NoteOff) -> Voice",
    isFinished: "Voice -> bool",
  },
});

synth.domainService("VoiceAllocator", {
  purpose: "assigns incoming notes to voices, stealing the oldest/quietest when the pool is full",
  uses: [voice],
});

synth.domainService("AudioRenderer", {
  purpose: "iterates all active voices, renders each through the engine, and mixes to output",
  uses: [voice],
});

// ── RealTime ────────────────────────────────────────────

const realtime = app.context("RealTime", {
  purpose: "lock-free boundary between the audio thread and non-real-time threads",
  ubiquitousLanguage: {
    "EventRingBuffer": "lock-free SPSC ring buffer for discrete messages to the audio thread (rtrb)",
    "ParameterSnapshot": "triple-buffered latest-wins parameter state readable by the audio thread",
    "DeferredDrop": "memory retired by the audio thread and freed later on a non-RT thread (basedrop)",
  },
});

realtime.valueObject("BoundaryMessage", {
  state: {
    kind: "BoundaryMessageKind",
    payload: "Vec<u8>",
    sequenceNumber: "u64",
  },
  description: "a discrete message crossing the RT boundary via the ring buffer",
});

realtime.valueObject("ParameterSnapshot", {
  state: {
    oscillator: "OscillatorConfig",
    filter: "FilterConfig",
    ampEnvelope: "AmpEnvelopeConfig",
    version: "u64",
  },
  description: "latest-wins snapshot of all synth parameters, readable by the audio thread without locking",
});

realtime.port("EventRingBuffer", {
  contract: {
    push: "BoundaryMessage -> Result<(), Full>",
    pop: "() -> Option<BoundaryMessage>",
  },
  meta: {
    notes: "SPSC lock-free ring buffer (rtrb). Producer: MIDI/UI thread. Consumer: audio thread.",
  },
});

realtime.port("ParameterBridge", {
  contract: {
    write: "ParameterSnapshot -> ()",
    read: "() -> &ParameterSnapshot",
  },
  meta: {
    notes: "triple_buffer: writer publishes a new snapshot; reader always gets the latest without blocking",
  },
});

realtime.port("DeferredDeallocator", {
  contract: {
    retire: "Arc<T> -> ()",
    collect: "() -> ()",
  },
  meta: {
    notes: "basedrop: audio thread retires owned memory; a background thread frees it later",
  },
});

// ── Patch ───────────────────────────────────────────────

const patchCtx = app.context("Patch", {
  purpose: "patch management: each patch is a complete instrument subscribed to a MIDI channel",
  ubiquitousLanguage: {
    "Patch": "a complete instrument: engine + parameters + voice pool + channel subscription",
    "ChannelSubscription": "which (group, channel) address a patch listens to; two patches on the same address layer",
    "VoicePool": "per-patch pool of voices with its own polyphony limit and stealing policy",
    "MpeZone": "a span of channels treated as one expressive instrument (Lower or Upper zone)",
  },
});

patchCtx.valueObject("MpeZone", {
  state: {
    managerChannel: "MidiChannel",
    memberChannelStart: "MidiChannel",
    memberChannelEnd: "MidiChannel",
  },
  description: "MPE zone configuration: manager channel plus a span of member channels for per-note expression",
  invariants: [
    "memberChannelStart < memberChannelEnd",
    "manager channel must not overlap member channels",
  ],
});

patchCtx.valueObject("ChannelSubscription", {
  state: {
    address: "ChannelAddress",
    mpeZone: "Option<MpeZone>",
  },
  description: "what a patch listens to: a single (group, channel) or an MPE zone",
});

patchCtx.valueObject("VoicePoolConfig", {
  state: {
    maxVoices: "u8",
    stealingPolicy: "StealingPolicy",
  },
  description: "per-patch voice pool sizing and stealing behavior",
  invariants: ["maxVoices must be at least 1"],
});

const patchAggregate = patchCtx.aggregate("Patch", {
  root: true,
  purpose: "a complete instrument: engine type, parameters, voice pool, channel subscription",
  state: {
    id: "PatchId",
    name: "string",
    engineType: "EngineType",
    oscillator: "OscillatorConfig",
    filter: "FilterConfig",
    ampEnvelope: "AmpEnvelopeConfig",
    subscription: "ChannelSubscription",
    voicePoolConfig: "VoicePoolConfig",
    gain: "Amplitude",
    pan: "f64",
    active: "bool",
  },
  commands: [
    command("CreatePatch", { name: "string", engineType: "EngineType", subscription: "ChannelSubscription" }),
    command("UpdateSubscription", { subscription: "ChannelSubscription" }),
    command("UpdateOscillator", { config: "OscillatorConfig" }),
    command("UpdateFilter", { config: "FilterConfig" }),
    command("UpdateEnvelope", { config: "AmpEnvelopeConfig" }),
    command("SetGain", { gain: "Amplitude" }),
    command("SetPan", { pan: "f64" }),
    command("ActivatePatch", {}),
    command("DeactivatePatch", {}),
  ],
  events: [
    event("PatchCreated", { id: "PatchId", name: "string", engineType: "EngineType" }),
    event("SubscriptionChanged", { id: "PatchId", subscription: "ChannelSubscription" }),
    event("PatchParametersUpdated", { id: "PatchId" }),
    event("PatchActivated", { id: "PatchId" }),
    event("PatchDeactivated", { id: "PatchId" }),
  ],
  invariants: [
    "each patch has its own independent voice pool",
    "a busy patch cannot starve another patch's voice pool",
    "pan must be -1.0 (left) to 1.0 (right)",
  ],
});

patchCtx.valueObject("PatchId", {
  from: "u32",
  description: "unique identifier for a patch in the patch list",
});

patchCtx.domainService("ChannelDispatcher", {
  purpose: "routes incoming MidiEvents to every patch subscribed to the event's (group, channel) address",
  uses: [patchAggregate],
});

patchCtx.domainService("PatchMixer", {
  purpose: "sums audio output from all active patches, applying per-patch gain and pan",
  uses: [patchAggregate],
});

patchCtx.aggregate("GlobalMixer", {
  root: true,
  purpose: "master mix bus: sums all patch outputs and applies master gain",
  state: {
    masterGain: "Amplitude",
  },
  commands: [
    command("SetMasterGain", { gain: "Amplitude" }),
  ],
  events: [
    event("MasterGainChanged", { gain: "Amplitude" }),
  ],
});

patchCtx.repository("PatchRepository", {
  of: patchAggregate,
  contract: {
    findById: "PatchId -> Option<Patch>",
    findByChannel: "ChannelAddress -> Vec<Patch>",
    save: "Patch -> ()",
    listAll: "() -> Vec<Patch>",
  },
});

// ── Modulation ──────────────────────────────────────────
// Sources (envelopes, LFOs, per-note expression, macros) routed to
// destinations (pitch, cutoff, gain, pan) via a mod matrix.
// Per-note expression sources (X=bend, Y=timbre, Z=pressure) are reserved
// as first-class per-voice sources from day one — even before MPE emits them.

const modulation = app.context("Modulation", {
  purpose: "modulation routing: sources (envelopes, LFOs, expression) mapped to destinations via a matrix",
  ubiquitousLanguage: {
    "ModSource": "a signal that drives modulation: envelope, LFO, per-note expression, macro, random",
    "ModDestination": "a parameter target: pitch, filter cutoff, gain, pan, etc.",
    "ModRouting": "a single source-to-destination connection with a depth/amount control",
    "ModMatrix": "the full set of active routings for a patch",
    "PerNoteExpression": "X (pitch bend), Y (timbre/CC74), Z (pressure) — per-voice mod sources for MPE readiness",
  },
});

modulation.valueObject("PerNoteExpression", {
  state: {
    bendX: "f64",
    timbreY: "f64",
    pressureZ: "f64",
  },
  description: "per-note expression triple: X=pitch bend, Y=timbre (CC74), Z=pressure. Per-voice, not per-patch.",
  invariants: [
    "all values normalized 0.0-1.0 (bend is bipolar but stored as 0.0-1.0 with 0.5 center)",
  ],
});

modulation.valueObject("ModSourceType", {
  from: "enum",
  description: "Envelope, LFO, Random, Macro, Velocity, KeyTrack, PerNoteBendX, PerNoteTimbreY, PerNotePressureZ",
});

modulation.valueObject("ModDestinationType", {
  from: "enum",
  description: "Pitch, FilterCutoff, FilterResonance, Gain, Pan",
});

modulation.valueObject("LfoConfig", {
  state: {
    waveform: "LfoWaveform",
    rate: "f64",
    depth: "f64",
    syncToTempo: "bool",
    phase: "f64",
  },
  description: "LFO parameters: waveform, rate (Hz or beat division), depth, tempo sync, initial phase",
  invariants: ["rate must be positive", "depth must be 0.0-1.0"],
});

modulation.valueObject("ModEnvelopeConfig", {
  state: {
    attack: "f64",
    decay: "f64",
    sustain: "f64",
    release: "f64",
  },
  description: "modulation envelope (same ADSR shape as amp, but routed to arbitrary destinations)",
  invariants: [
    "attack, decay, release must be non-negative",
    "sustain must be 0.0-1.0",
  ],
});

const modMatrix = modulation.aggregate("ModMatrix", {
  root: true,
  purpose: "per-patch modulation routing: maps sources to destinations with adjustable depth",
  state: {
    patchId: "PatchId",
    routings: "Vec<ModRouting>",
    lfoConfigs: "Vec<LfoConfig>",
    modEnvelopes: "Vec<ModEnvelopeConfig>",
  },
  commands: [
    command("AddRouting", { source: "ModSourceType", destination: "ModDestinationType", depth: "f64" }),
    command("RemoveRouting", { routingIndex: "u8" }),
    command("UpdateRoutingDepth", { routingIndex: "u8", depth: "f64" }),
    command("ConfigureLfo", { lfoIndex: "u8", config: "LfoConfig" }),
    command("ConfigureModEnvelope", { envIndex: "u8", config: "ModEnvelopeConfig" }),
  ],
  events: [
    event("RoutingAdded", { source: "ModSourceType", destination: "ModDestinationType", depth: "f64" }),
    event("RoutingRemoved", { routingIndex: "u8" }),
    event("RoutingDepthChanged", { routingIndex: "u8", depth: "f64" }),
    event("LfoConfigured", { lfoIndex: "u8" }),
    event("ModEnvelopeConfigured", { envIndex: "u8" }),
  ],
  invariants: [
    "depth is bipolar: -1.0 to 1.0",
    "per-note expression sources (X, Y, Z) are per-voice, not per-patch",
    "LFOs and macros are per-patch (shared across all voices)",
  ],
});

modMatrix.entity("ModRouting", {
  state: {
    source: "ModSourceType",
    destination: "ModDestinationType",
    depth: "f64",
  },
});

modulation.domainService("ModulationProcessor", {
  purpose: "evaluates all mod sources and applies routed modulation to destination parameters each audio block",
  uses: [modMatrix],
});

// ── Invariants ──────────────────────────────────────────

app.invariants([
  invariant("audio thread must never allocate heap memory", {
    meta: { rationale: "any allocation risks missing the audio buffer deadline and causing a dropout" },
  }),
  invariant("audio thread must never acquire a mutex or blocking lock", {
    meta: { rationale: "lock contention causes unbounded latency on the real-time thread" },
  }),
  invariant("audio thread must never perform blocking I/O", {
    meta: { rationale: "file/network I/O has unpredictable latency incompatible with audio deadlines" },
  }),
  invariant("all parameter changes cross the boundary via ParameterBridge or EventRingBuffer", {
    meta: { rationale: "enforces the lock-free seam; no shared mutable state between threads" },
  }),
  invariant("retired memory from the audio thread is freed via DeferredDeallocator, never directly", {
    meta: { rationale: "basedrop ensures free() never runs on the audio thread" },
  }),
  invariant("each patch has an independent voice pool; one patch's polyphony cannot exhaust another's", {
    meta: { rationale: "a busy pad patch must not starve a bass patch of voices" },
  }),
  invariant("channel dispatch delivers events to all subscribed patches, not just the first match", {
    meta: { rationale: "two patches on the same channel layer automatically by design" },
  }),
  invariant("per-note expression (X, Y, Z) reaches the voice directly, never just the patch", {
    meta: { rationale: "voices must not assume expression is patch-level — that would block MPE later" },
  }),
  invariant("MPE expression dimensions (bend, timbre, pressure) exist as named per-voice mod sources from day one", {
    meta: { rationale: "building MPE later means feeding data into sources that already exist, not adding new types" },
  }),
]);

// ── Asset Kinds ─────────────────────────────────────────

app.assetKind("cargo-manifest", {
  description: "Rust Cargo.toml project manifest",
  filePattern: "Cargo.toml",
  prompts: [
    "Use edition 2021",
    "Only include dependencies actually needed by the generated code",
    "Include [lib] section with path = \"src/lib.rs\"",
  ],
});

app.assetKind("makefile", {
  description: "GNU Makefile for build automation",
  filePattern: "Makefile",
  prompts: [
    "Include targets: build, test, clean, check, run",
    "Use cargo for all Rust operations",
  ],
});

app.assetKind("rust-module-declaration", {
  description: "Rust mod.rs or lib.rs module declaration file",
  prompts: [
    "Only output module declarations (pub mod) and re-exports",
    "Add #![allow(non_snake_case)] if any module name uses PascalCase",
    "Do not add any implementation code",
  ],
});

// ── Project Assets ──────────────────────────────────────

app.asset("RootCargoToml", {
  kind: "cargo-manifest",
  description: "Root Cargo.toml for the crest-synth project",
  prompts: ["Package name: crest-synth, version 0.1.0", "No external dependencies needed (pure Rust)"],
});

app.asset("BuildMakefile", {
  kind: "makefile",
  description: "Build automation for the crest-synth project",
  prompts: ["Default target: build", "test: cargo test", "check: cargo check", "clean: cargo clean"],
});

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: ["File path: src/lib.rs", "Declare modules: kernel, Shell, Synth, RealTime, Patch, Modulation"],
});

// ── Context Module Assets ───────────────────────────────

kernel.asset("KernelMod", {
  kind: "rust-module-declaration",
  description: "src/kernel/mod.rs module declarations for Kernel context",
  prompts: ["File path: src/kernel/mod.rs", "Declare modules for: MidiGroup, MidiChannel, ChannelAddress, NoteId, NoteNumber, Velocity, MidiEvent, SampleRate, AudioFrame, Frequency, Amplitude"],
});

shell.asset("ShellMod", {
  kind: "rust-module-declaration",
  description: "src/Shell/mod.rs module declarations for Shell context",
  prompts: ["File path: src/Shell/mod.rs", "Declare modules for: AudioOutput, MidiInput, MidiNormalizer, AppWindow"],
});

synth.asset("SynthMod", {
  kind: "rust-module-declaration",
  description: "src/Synth/mod.rs module declarations for Synth context",
  prompts: ["File path: src/Synth/mod.rs", "Declare modules for: EnvelopeStage, OscillatorConfig, FilterConfig, AmpEnvelopeConfig, Voice, SynthEngine, VoiceAllocator, AudioRenderer"],
});

realtime.asset("RealTimeMod", {
  kind: "rust-module-declaration",
  description: "src/RealTime/mod.rs module declarations for RealTime context",
  prompts: ["File path: src/RealTime/mod.rs", "Declare modules for: BoundaryMessage, ParameterSnapshot, EventRingBuffer, ParameterBridge, DeferredDeallocator"],
});

patchCtx.asset("PatchMod", {
  kind: "rust-module-declaration",
  description: "src/Patch/mod.rs module declarations for Patch context",
  prompts: ["File path: src/Patch/mod.rs", "Declare modules for: MpeZone, ChannelSubscription, VoicePoolConfig, Patch, PatchId, ChannelDispatcher, PatchMixer, GlobalMixer, PatchRepository"],
});

modulation.asset("ModulationMod", {
  kind: "rust-module-declaration",
  description: "src/Modulation/mod.rs module declarations for Modulation context",
  prompts: ["File path: src/Modulation/mod.rs", "Declare modules for: PerNoteExpression, ModSourceType, ModDestinationType, LfoConfig, ModEnvelopeConfig, ModMatrix, ModRouting, ModulationProcessor"],
});
