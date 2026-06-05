import { command, event, invariant } from "../../../src/index.js";
import { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer, modulation, modMatrix, sampleLib, sampleSet } from "./crest-spec-phase-6.js";
export { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer, modulation, modMatrix, sampleLib, sampleSet } from "./crest-spec-phase-6.js";

// Phase 7: Effects
// Per-patch and global reverb/chorus/delay via fundsp.
// EffectChain aggregate models an ordered list of effect slots;
// each patch gets its own chain, plus a master chain on the global mixer.

// ── Modulation addition ──────────────────────────────────
// ModDestinationType gains FxSend destination.

modulation.valueObject("ModDestinationType", {
  from: "enum",
  description: "Pitch, FilterCutoff, FilterResonance, Gain, Pan, SampleStart, FxSend",
});

// ── Effects ─────────────────────────────────────────────
// Per-patch and global effect chains. Each chain is an ordered list of
// effect slots (reverb, chorus, delay) processed via fundsp.
// Signal flow: patch voices -> patch FX chain -> patch mixer -> master FX chain -> output.

export const effects = app.context("Effects", {
  purpose: "audio effects processing: per-patch and global reverb, chorus, delay via fundsp",
  ubiquitousLanguage: {
    "EffectChain": "an ordered list of effect slots applied to a patch's or the master mix's audio",
    "EffectSlot": "a single effect processor with its own type and parameters in a chain",
    "DryWet": "mix ratio between unprocessed (dry) and processed (wet) signal",
  },
});

effects.valueObject("EffectChainId", {
  from: "u32",
  description: "unique identifier for an effect chain",
});

effects.valueObject("ReverbConfig", {
  state: {
    roomSize: "f64",
    damping: "f64",
    dryWet: "f64",
    preDelay: "f64",
  },
  description: "reverb parameters",
  invariants: [
    "roomSize, damping, dryWet must be 0.0-1.0",
    "preDelay must be non-negative",
  ],
});

effects.valueObject("ChorusConfig", {
  state: {
    rate: "f64",
    depth: "f64",
    dryWet: "f64",
    voices: "u8",
  },
  description: "chorus parameters",
  invariants: [
    "rate must be positive",
    "depth, dryWet must be 0.0-1.0",
    "voices must be at least 1",
  ],
});

effects.valueObject("DelayConfig", {
  state: {
    time: "f64",
    feedback: "f64",
    dryWet: "f64",
    syncToTempo: "bool",
  },
  description: "delay parameters",
  invariants: [
    "time must be positive",
    "feedback must be 0.0-1.0 (>1.0 causes runaway)",
    "dryWet must be 0.0-1.0",
  ],
});

export const effectChain = effects.aggregate("EffectChain", {
  root: true,
  purpose: "an ordered list of effect slots processed in series",
  state: {
    id: "EffectChainId",
    slots: "Vec<EffectSlot>",
    bypass: "bool",
  },
  commands: [
    command("AddEffect", { effectType: "EffectType", position: "u8" }),
    command("RemoveEffect", { slotIndex: "u8" }),
    command("ReorderEffect", { fromIndex: "u8", toIndex: "u8" }),
    command("UpdateEffectParams", { slotIndex: "u8", params: "EffectParams" }),
    command("BypassChain", {}),
    command("EnableChain", {}),
  ],
  events: [
    event("EffectAdded", { effectType: "EffectType", position: "u8" }),
    event("EffectRemoved", { slotIndex: "u8" }),
    event("EffectReordered", { fromIndex: "u8", toIndex: "u8" }),
    event("EffectParamsUpdated", { slotIndex: "u8" }),
    event("ChainBypassed", { id: "EffectChainId" }),
    event("ChainEnabled", { id: "EffectChainId" }),
  ],
  invariants: [
    "effects process in slot order: slot 0 first, slot N last",
    "bypassed chain passes audio through unmodified",
  ],
});

effectChain.entity("EffectSlot", {
  state: {
    effectType: "EffectType",
    params: "EffectParams",
    bypass: "bool",
  },
});

export const effectProcessor = effects.port("EffectProcessor", {
  contract: {
    process: "([AudioFrame], EffectParams) -> [AudioFrame]",
    reset: "() -> ()",
  },
  meta: {
    notes: "implemented via fundsp nodes; enum dispatch for the supported effect types",
  },
});

effects.repository("EffectChainRepository", {
  of: effectChain,
  contract: {
    findById: "EffectChainId -> Option<EffectChain>",
    save: "EffectChain -> ()",
  },
});

// ── Invariants ──────────────────────────────────────────
// Accumulates all invariants from phase 6 plus new ones for phase 7.

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
  invariant("sample-set swaps are performed via Arc + DeferredDeallocator; the audio thread never loads or frees sample data", {
    meta: { rationale: "sample sets can be multi-megabyte; loading and freeing must happen off the audio thread" },
  }),
  // New in phase 7:
  invariant("effect chains process in slot order; signal flow is patch voices -> patch FX -> mix -> master FX -> output", {
    meta: { rationale: "deterministic signal routing; per-patch FX before the mix bus, master FX after" },
  }),
]);

// ── Module Assets ───────────────────────────────────────

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: ["File path: src/lib.rs", "Declare modules: kernel, Shell, Synth, RealTime, Patch, Modulation, SampleLibrary, Effects"],
});

effects.asset("EffectsMod", {
  kind: "rust-module-declaration",
  description: "src/Effects/mod.rs module declarations for Effects context",
  prompts: ["File path: src/Effects/mod.rs", "Declare modules for: EffectChainId, ReverbConfig, ChorusConfig, DelayConfig, EffectChain, EffectSlot, EffectProcessor, EffectChainRepository"],
});
