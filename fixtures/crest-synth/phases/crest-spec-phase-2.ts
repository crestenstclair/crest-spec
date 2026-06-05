import { project, command, event, layer } from "../../../src/index.js";

// Phase 2: A real polyphonic engine
// One wavetable/virtual-analog engine: voice allocation with basic stealing,
// oscillator, filter, amp envelope. Single instrument.
// Replaces the throwaway SineVoice from phase 1 with a real Synth context.

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
// Replaces phase 1's Audio context. Real polyphonic engine with
// voice allocation, wavetable oscillator, resonant filter, and amp envelope.

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
  prompts: [
    "Package name: crest-synth, version 0.1.0",
    "No external dependencies needed for phase 2 (pure Rust)",
  ],
});

app.asset("BuildMakefile", {
  kind: "makefile",
  description: "Build automation for the crest-synth project",
  prompts: [
    "Default target: build",
    "test: cargo test",
    "check: cargo check",
    "clean: cargo clean",
    "run: cargo run (if binary, otherwise skip)",
  ],
});

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: [
    "File path: src/lib.rs",
    "Declare modules: kernel, Shell, Synth",
  ],
});

// ── Context Module Assets ───────────────────────────────

kernel.asset("KernelMod", {
  kind: "rust-module-declaration",
  description: "src/kernel/mod.rs module declarations for Kernel context",
  prompts: [
    "File path: src/kernel/mod.rs",
    "Declare modules for: MidiGroup, MidiChannel, NoteId, NoteNumber, Velocity, MidiEvent, SampleRate, AudioFrame, Frequency, Amplitude",
  ],
});

shell.asset("ShellMod", {
  kind: "rust-module-declaration",
  description: "src/Shell/mod.rs module declarations for Shell context",
  prompts: [
    "File path: src/Shell/mod.rs",
    "Declare modules for: AudioOutput, MidiInput, MidiNormalizer, AppWindow",
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
