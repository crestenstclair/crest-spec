import { project, command, event, invariant, layer } from "../../../src/index.js";

// Phase 3: Harden the real-time seam
// Introduce rtrb + triple_buffer + basedrop.
// Move all parameter and patch changes across the lock-free boundary.
// Everything after this respects the audio deadline.

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

// ── RealTime ────────────────────────────────────────────
// The lock-free boundary between the audio thread and everything else.
// rtrb for discrete messages, triple_buffer for latest-wins parameters,
// basedrop for deferred deallocation so the audio thread never frees.

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
]);
