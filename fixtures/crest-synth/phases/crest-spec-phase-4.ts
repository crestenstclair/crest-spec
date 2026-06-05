import { command, event, invariant } from "../../../src/index.js";
import { app, kernel, shell, synth, voice, realtime } from "./crest-spec-phase-3.js";
export { app, kernel, shell, synth, voice, realtime } from "./crest-spec-phase-3.js";

// Phase 4: Multiple patches subscribed to channels
// A list of patches, each subscribing to a (group, channel) address.
// Dispatch incoming events to subscribers. Per-patch voice pools. A global mix.

// ── Kernel additions ────────────────────────────────────
// ChannelAddress is new in phase 4 — the Patch context needs it.

kernel.valueObject("ChannelAddress", {
  state: { group: "MidiGroup", channel: "MidiChannel" },
  description: "a (group, channel) pair — the 256-destination address space for MIDI 2.0",
});

// ── Patch ───────────────────────────────────────────────
// A patch is a complete instrument: engine instance, sound parameters,
// voice pool, and a channel subscription. Incoming MIDI dispatches to
// every patch subscribed to that (group, channel) address.

export const patchCtx = app.context("Patch", {
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

export const patchAggregate = patchCtx.aggregate("Patch", {
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

export const globalMixer = patchCtx.aggregate("GlobalMixer", {
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

// ── Invariants ──────────────────────────────────────────
// Accumulates all invariants from phase 3 plus new ones for phase 4.

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
  // New in phase 4:
  invariant("each patch has an independent voice pool; one patch's polyphony cannot exhaust another's", {
    meta: { rationale: "a busy pad patch must not starve a bass patch of voices" },
  }),
  invariant("channel dispatch delivers events to all subscribed patches, not just the first match", {
    meta: { rationale: "two patches on the same channel layer automatically by design" },
  }),
]);

// ── Module Assets ───────────────────────────────────────

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: [
    "File path: src/lib.rs",
    "Declare modules: kernel, Shell, Synth, RealTime, Patch",
  ],
});

kernel.asset("KernelMod", {
  kind: "rust-module-declaration",
  description: "src/kernel/mod.rs module declarations for Kernel context",
  prompts: [
    "File path: src/kernel/mod.rs",
    "Declare modules for: MidiGroup, MidiChannel, ChannelAddress, NoteId, NoteNumber, Velocity, MidiEvent, SampleRate, AudioFrame, Frequency, Amplitude",
  ],
});

patchCtx.asset("PatchMod", {
  kind: "rust-module-declaration",
  description: "src/Patch/mod.rs module declarations for Patch context",
  prompts: [
    "File path: src/Patch/mod.rs",
    "Declare modules for: MpeZone, ChannelSubscription, VoicePoolConfig, Patch, PatchId, ChannelDispatcher, PatchMixer, GlobalMixer, PatchRepository",
  ],
});
