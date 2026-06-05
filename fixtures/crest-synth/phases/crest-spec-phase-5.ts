import { command, event, invariant } from "../../../src/index.js";
import { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer } from "./crest-spec-phase-4.js";
export { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer } from "./crest-spec-phase-4.js";

// Phase 5: Modulation
// Envelopes, LFOs, a routing matrix to a handful of destinations (pitch, cutoff, gain),
// with gamepad-driven assignment. Per-note expression reserved as first-class mod sources
// to keep the MPE door open.

// ── Synth additions ─────────────────────────────────────
// Voice gains perNoteExpression field + UpdatePerNoteExpression command.
// Note: Voice is redeclared here with the expanded state.

// ── Modulation ──────────────────────────────────────────
// Sources (envelopes, LFOs, per-note expression, macros) routed to
// destinations (pitch, cutoff, gain, pan) via a mod matrix.
// Per-note expression sources (X=bend, Y=timbre, Z=pressure) are reserved
// as first-class per-voice sources from day one — even before MPE emits them.

export const modulation = app.context("Modulation", {
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

export const modMatrix = modulation.aggregate("ModMatrix", {
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
// Accumulates all invariants from phase 4 plus new ones for phase 5.

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
  // New in phase 5:
  invariant("per-note expression (X, Y, Z) reaches the voice directly, never just the patch", {
    meta: { rationale: "voices must not assume expression is patch-level — that would block MPE later" },
  }),
  invariant("MPE expression dimensions (bend, timbre, pressure) exist as named per-voice mod sources from day one", {
    meta: { rationale: "building MPE later means feeding data into sources that already exist, not adding new types" },
  }),
]);

// ── Module Assets ───────────────────────────────────────

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: ["File path: src/lib.rs", "Declare modules: kernel, Shell, Synth, RealTime, Patch, Modulation"],
});

modulation.asset("ModulationMod", {
  kind: "rust-module-declaration",
  description: "src/Modulation/mod.rs module declarations for Modulation context",
  prompts: ["File path: src/Modulation/mod.rs", "Declare modules for: PerNoteExpression, ModSourceType, ModDestinationType, LfoConfig, ModEnvelopeConfig, ModMatrix, ModRouting, ModulationProcessor"],
});
