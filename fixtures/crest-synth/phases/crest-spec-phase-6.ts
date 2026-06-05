import { command, event, invariant } from "../../../src/index.js";
import { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer, modulation, modMatrix } from "./crest-spec-phase-5.js";
export { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer, modulation, modMatrix } from "./crest-spec-phase-5.js";

// Phase 6: Sample-playback engine
// SF2/sample loading via symphonia + dasp interpolation, with basedrop handling
// sample-set swaps. A SampleLibrary context manages sample data; the Synth context
// gains a SamplePlayer engine type alongside the existing wavetable/VA engine.

// ── Synth additions ─────────────────────────────────────
// SamplePlayerConfig is new — Voice also gains samplePlaybackPosition.

synth.valueObject("SamplePlayerConfig", {
  state: {
    sampleSetId: "SampleSetId",
    interpolation: "InterpolationMode",
    loopMode: "LoopMode",
  },
  description: "sample player engine config: which sample set to use, interpolation quality, loop behavior",
});

// ── Modulation addition ──────────────────────────────────
// ModDestinationType gains SampleStart destination.

modulation.valueObject("ModDestinationType", {
  from: "enum",
  description: "Pitch, FilterCutoff, FilterResonance, Gain, Pan, SampleStart",
});

// ── SampleLibrary ───────────────────────────────────────
// Sample data management: loading SF2/WAV files, organizing into sample sets,
// and making them available to the SamplePlayer engine. Sample-set swaps use
// basedrop (DeferredDeallocator) so the audio thread never frees sample memory.

export const sampleLib = app.context("SampleLibrary", {
  purpose: "sample data management: loading, organizing, and serving sample sets to the engine",
  ubiquitousLanguage: {
    "SampleSet": "a loaded collection of samples (e.g. an SF2 soundbank) mapped by key/velocity zones",
    "SampleZone": "a region of the keyboard + velocity range mapped to a specific sample",
    "SampleData": "raw audio sample data (f32 frames) held in memory, swapped via basedrop",
  },
});

sampleLib.valueObject("SampleSetId", {
  from: "u32",
  description: "unique identifier for a loaded sample set",
});

sampleLib.valueObject("SampleMetadata", {
  state: {
    sampleRate: "SampleRate",
    channels: "u8",
    lengthFrames: "u64",
    loopStart: "Option<u64>",
    loopEnd: "Option<u64>",
    rootNote: "NoteNumber",
  },
  description: "metadata about a single sample: rate, length, loop points, root pitch",
});

sampleLib.valueObject("KeyVelocityRange", {
  state: {
    keyLow: "NoteNumber",
    keyHigh: "NoteNumber",
    velocityLow: "Velocity",
    velocityHigh: "Velocity",
  },
  description: "the note and velocity range a sample zone responds to",
  invariants: ["keyLow <= keyHigh", "velocityLow <= velocityHigh"],
});

sampleLib.valueObject("InterpolationMode", {
  from: "enum",
  description: "sample interpolation quality: Nearest, Linear, Cubic, Sinc",
});

export const sampleSet = sampleLib.aggregate("SampleSet", {
  root: true,
  purpose: "a loaded collection of samples mapped to key/velocity zones",
  state: {
    id: "SampleSetId",
    name: "string",
    zones: "Vec<SampleZone>",
    format: "SampleFormat",
  },
  commands: [
    command("LoadSampleSet", { path: "string", format: "SampleFormat" }),
    command("UnloadSampleSet", { id: "SampleSetId" }),
  ],
  events: [
    event("SampleSetLoaded", { id: "SampleSetId", name: "string", zoneCount: "u32" }),
    event("SampleSetUnloaded", { id: "SampleSetId" }),
  ],
  invariants: [
    "zones must not have overlapping key+velocity ranges within the same set",
    "sample data is held via Arc; audio thread reads via shared reference",
    "unloading retires the Arc through DeferredDeallocator, never frees on audio thread",
  ],
});

sampleSet.entity("SampleZone", {
  state: {
    range: "KeyVelocityRange",
    metadata: "SampleMetadata",
    sampleDataRef: "Arc<[f32]>",
  },
});

sampleLib.applicationService("SampleLoader", {
  purpose: "decodes sample files (SF2, WAV) from disk and creates SampleSet aggregates",
  uses: [sampleSet],
});

sampleLib.domainService("SampleInterpolator", {
  purpose: "reads sample data with pitch-shifted interpolation (linear, cubic, sinc via dasp)",
  uses: [sampleSet],
});

sampleLib.repository("SampleSetRepository", {
  of: sampleSet,
  contract: {
    findById: "SampleSetId -> Option<SampleSet>",
    save: "SampleSet -> ()",
    listAll: "() -> Vec<SampleSet>",
  },
});

// ── Invariants ──────────────────────────────────────────
// Accumulates all invariants from phase 5 plus new ones for phase 6.

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
  // New in phase 6:
  invariant("sample-set swaps are performed via Arc + DeferredDeallocator; the audio thread never loads or frees sample data", {
    meta: { rationale: "sample sets can be multi-megabyte; loading and freeing must happen off the audio thread" },
  }),
]);

// ── Module Assets ───────────────────────────────────────

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: ["File path: src/lib.rs", "Declare modules: kernel, Shell, Synth, RealTime, Patch, Modulation, SampleLibrary"],
});

synth.asset("SynthMod", {
  kind: "rust-module-declaration",
  description: "src/Synth/mod.rs module declarations for Synth context",
  prompts: ["File path: src/Synth/mod.rs", "Declare modules for: EnvelopeStage, OscillatorConfig, FilterConfig, AmpEnvelopeConfig, SamplePlayerConfig, Voice, SynthEngine, VoiceAllocator, AudioRenderer"],
});

sampleLib.asset("SampleLibraryMod", {
  kind: "rust-module-declaration",
  description: "src/SampleLibrary/mod.rs module declarations for SampleLibrary context",
  prompts: ["File path: src/SampleLibrary/mod.rs", "Declare modules for: SampleSetId, SampleMetadata, KeyVelocityRange, InterpolationMode, SampleSet, SampleZone, SampleLoader, SampleInterpolator, SampleSetRepository"],
});
