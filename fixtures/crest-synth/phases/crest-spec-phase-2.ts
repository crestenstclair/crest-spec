import { command, event } from "../../../src/index.js";
import { app, kernel, shell } from "./base.js";
export { app, kernel, shell } from "./base.js";

// Phase 2: A real polyphonic engine
// One wavetable/virtual-analog engine: voice allocation with basic stealing,
// oscillator, filter, amp envelope. Single instrument.
// Replaces the throwaway SineVoice from phase 1 with a real Synth context.

// ── Kernel additions ────────────────────────────────────

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

// ── Synth ───────────────────────────────────────────────
// Replaces phase 1's Audio context. Real polyphonic engine with
// voice allocation, wavetable oscillator, resonant filter, and amp envelope.

export const synth = app.context("Synth", {
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

export const voice = synth.aggregate("Voice", {
  root: true,
  purpose: "a single sounding note: oscillator + filter + amp envelope",
  state: {
    noteId: "NoteId",
    noteNumber: "NoteNumber",
    velocity: "Velocity",
    frequency: "Frequency",
    oscillatorPhase: "f64",
    filterState: "FilterState",
    envelopeStage: "EnvelopeStage",
    envelopeLevel: "Amplitude",
    active: "bool",
  },
  commands: [
    command("NoteOn", { noteId: "NoteId", noteNumber: "NoteNumber", velocity: "Velocity" }),
    command("NoteOff", { noteId: "NoteId" }),
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

// ── Module Assets ───────────────────────────────────────

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: [
    "File path: src/lib.rs",
    "Declare modules: kernel, Shell, Synth",
  ],
});

kernel.asset("KernelMod", {
  kind: "rust-module-declaration",
  description: "src/kernel/mod.rs module declarations for Kernel context",
  prompts: [
    "File path: src/kernel/mod.rs",
    "Declare modules for: MidiGroup, MidiChannel, NoteId, NoteNumber, Velocity, MidiEvent, SampleRate, AudioFrame, Frequency, Amplitude",
  ],
});

synth.asset("SynthMod", {
  kind: "rust-module-declaration",
  description: "src/Synth/mod.rs module declarations for Synth context",
  prompts: [
    "File path: src/Synth/mod.rs",
    "Declare modules for: EnvelopeStage, OscillatorConfig, FilterConfig, AmpEnvelopeConfig, Voice, SynthEngine, VoiceAllocator, AudioRenderer",
  ],
});
