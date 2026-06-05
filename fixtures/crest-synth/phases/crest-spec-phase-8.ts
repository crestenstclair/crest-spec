import { command, event, invariant } from "../../../src/index.js";
import { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer, modulation, modMatrix, sampleLib, sampleSet, effects, effectChain, effectProcessor } from "./crest-spec-phase-7.js";
export { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer, modulation, modMatrix, sampleLib, sampleSet, effects, effectChain, effectProcessor } from "./crest-spec-phase-7.js";

// Phase 8: Patches and presets
// serde save/load for individual patches and the full setup (the patch list
// and each patch's channel subscription); a preset browser.

// ── Presets ──────────────────────────────────────────────
// A Preset is a serialized snapshot of a Patch's complete state.
// A Setup is the full app state: the ordered list of patches, their
// channel subscriptions, the master mixer config, and all effect chains.

export const presetsCtx = app.context("Presets", {
  purpose: "persistence: save/load individual patches and full setups via serde",
  ubiquitousLanguage: {
    "Preset": "a serialized snapshot of a single patch's complete state",
    "PresetBank": "a named collection of presets, organized for browsing",
    "Setup": "the full application state: patch list, subscriptions, mixer, effects — everything needed to restore a session",
  },
});

presetsCtx.valueObject("PresetId", {
  from: "string",
  description: "unique identifier for a preset (UUID or slug)",
});

presetsCtx.valueObject("PresetMetadata", {
  state: {
    name: "string",
    author: "string",
    category: "string",
    tags: "Vec<string>",
    createdAt: "string",
  },
  description: "metadata about a preset for browsing and search",
});

export const preset = presetsCtx.aggregate("Preset", {
  root: true,
  purpose: "a serialized snapshot of a single patch's complete sound and routing configuration",
  state: {
    id: "PresetId",
    metadata: "PresetMetadata",
    engineType: "EngineType",
    oscillator: "OscillatorConfig",
    filter: "FilterConfig",
    ampEnvelope: "AmpEnvelopeConfig",
    samplePlayer: "Option<SamplePlayerConfig>",
    modMatrix: "SerializedModMatrix",
    effectChain: "SerializedEffectChain",
  },
  commands: [
    command("SavePreset", { patchId: "PatchId", metadata: "PresetMetadata" }),
    command("LoadPreset", { presetId: "PresetId" }),
    command("DeletePreset", { presetId: "PresetId" }),
    command("UpdateMetadata", { presetId: "PresetId", metadata: "PresetMetadata" }),
  ],
  events: [
    event("PresetSaved", { id: "PresetId", name: "string" }),
    event("PresetLoaded", { id: "PresetId", targetPatchId: "PatchId" }),
    event("PresetDeleted", { id: "PresetId" }),
    event("PresetMetadataUpdated", { id: "PresetId" }),
  ],
});

export const presetBank = presetsCtx.aggregate("PresetBank", {
  root: true,
  purpose: "a named collection of presets for organized browsing",
  state: {
    name: "string",
    presetIds: "Vec<PresetId>",
    isFactory: "bool",
  },
  commands: [
    command("CreateBank", { name: "string" }),
    command("AddPresetToBank", { presetId: "PresetId" }),
    command("RemovePresetFromBank", { presetId: "PresetId" }),
  ],
  events: [
    event("BankCreated", { name: "string" }),
    event("PresetAddedToBank", { presetId: "PresetId" }),
    event("PresetRemovedFromBank", { presetId: "PresetId" }),
  ],
  invariants: [
    "factory banks are read-only; user cannot modify them",
  ],
});

export const setup = presetsCtx.aggregate("Setup", {
  root: true,
  purpose: "the full application state: patch list + subscriptions + mixer + effects — restored on load",
  state: {
    name: "string",
    patches: "Vec<SerializedPatch>",
    masterGain: "Amplitude",
    masterEffectChain: "SerializedEffectChain",
  },
  commands: [
    command("SaveSetup", { name: "string" }),
    command("LoadSetup", { path: "string" }),
  ],
  events: [
    event("SetupSaved", { name: "string", patchCount: "u32" }),
    event("SetupLoaded", { name: "string", patchCount: "u32" }),
  ],
});

export const presetCodec = presetsCtx.port("PresetCodec", {
  contract: {
    serialize: "Preset -> Vec<u8>",
    deserialize: "Vec<u8> -> Result<Preset, CodecError>",
    serializeSetup: "Setup -> Vec<u8>",
    deserializeSetup: "Vec<u8> -> Result<Setup, CodecError>",
  },
  meta: {
    notes: "serde with serde_json (human-readable) or bincode (compact binary)",
  },
});

presetsCtx.applicationService("PresetBrowser", {
  purpose: "lists, searches, and previews presets from all banks; handles load/save workflow",
  uses: [preset, presetBank],
});

presetsCtx.repository("PresetRepository", {
  of: preset,
  contract: {
    findById: "PresetId -> Option<Preset>",
    findByCategory: "string -> Vec<Preset>",
    search: "string -> Vec<Preset>",
    save: "Preset -> ()",
    listAll: "() -> Vec<Preset>",
  },
});

// ── Invariants ──────────────────────────────────────────
// Accumulates all invariants from phase 7 plus new ones for phase 8.

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
  invariant("effect chains process in slot order; signal flow is patch voices -> patch FX -> mix -> master FX -> output", {
    meta: { rationale: "deterministic signal routing; per-patch FX before the mix bus, master FX after" },
  }),
  // New in phase 8:
  invariant("preset serialization captures the complete patch state including modulation and effects", {
    meta: { rationale: "a loaded preset must reproduce the saved sound exactly" },
  }),
  invariant("setup save/load preserves the full session: all patches, subscriptions, mixer, and effect chains", {
    meta: { rationale: "restoring a setup must return the app to its exact prior state" },
  }),
]);

// ── Module Assets ───────────────────────────────────────

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: ["File path: src/lib.rs", "Declare modules: kernel, Shell, Synth, RealTime, Patch, Modulation, SampleLibrary, Effects, Presets"],
});

presetsCtx.asset("PresetsMod", {
  kind: "rust-module-declaration",
  description: "src/Presets/mod.rs module declarations for Presets context",
  prompts: ["File path: src/Presets/mod.rs", "Declare modules for: PresetId, PresetMetadata, Preset, PresetBank, Setup, PresetCodec, PresetBrowser, PresetRepository"],
});
