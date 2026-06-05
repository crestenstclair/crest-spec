import { invariant } from "../../../src/index.js";
import { audioOutputPort } from "./base.js";
import { app, kernel, shell, synth, voice } from "./crest-spec-phase-2.js";
export { app, kernel, shell, synth, voice } from "./crest-spec-phase-2.js";

// Phase 3: Harden the real-time seam + live audio output
// Introduce rtrb + triple_buffer + basedrop.
// Move all parameter and patch changes across the lock-free boundary.
// Everything after this respects the audio deadline.
// Also: wire up cpal so the synth plays through speakers — a synth that
// can't make sound isn't a synth yet.

// ── RealTime ────────────────────────────────────────────
// The lock-free boundary between the audio thread and everything else.
// rtrb for discrete messages, triple_buffer for latest-wins parameters,
// basedrop for deferred deallocation so the audio thread never frees.

export const realtime = app.context("RealTime", {
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
  invariant("rendered audio frames must never be silently dropped between the renderer and the output device", {
    meta: { rationale: "silently dropping frames (e.g. via try_send on a full channel) causes notes after the first to go missing; the write path must apply backpressure instead" },
  }),
]);

// ── CpalAudioOutput Adapter ────────────────────────────
// Pulled forward from phase 9: the synth must make sound now, not eight
// phases from now. Uses cpal for cross-platform audio output.

app.adapter("CpalAudioOutput", {
  implements: audioOutputPort,
  layer: "infrastructure",
  meta: { notes: "cpal: cross-platform audio output (ALSA/PipeWire on Linux, WASAPI, CoreAudio)" },
});

// ── Asset Kind: Rust Adapter ───────────────────────────

app.assetKind("rust-adapter", {
  description: "Rust infrastructure adapter implementing a port trait",
  prompts: [
    "Implement the port trait using the specified crate",
    "Include proper error handling and resource cleanup",
    "Add unit tests with mocks where appropriate",
  ],
});

// ── Module Assets ───────────────────────────────────────

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: [
    "File path: src/lib.rs",
    "Declare modules: kernel, Shell, Synth, RealTime",
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

realtime.asset("RealTimeMod", {
  kind: "rust-module-declaration",
  description: "src/RealTime/mod.rs module declarations for RealTime context",
  prompts: [
    "File path: src/RealTime/mod.rs",
    "Declare modules for: BoundaryMessage, ParameterSnapshot, EventRingBuffer, ParameterBridge, DeferredDeallocator",
  ],
});

// ── Cargo Manifest (override) ──────────────────────────
// Phase 3 adds cpal for real-time audio output.

app.asset("RootCargoToml", {
  kind: "cargo-manifest",
  description: "Root Cargo.toml for the crest-synth project",
  prompts: [
    "Package name: crest-synth, version 0.1.0",
    "Dependencies: cpal (audio output — cross-platform via CoreAudio/WASAPI/ALSA)",
    "Include [[bin]] section: name = \"crest-synth\", path = \"src/main.rs\"",
  ],
});

// ── CpalAudioOutput Adapter Asset ──────────────────────

app.asset("CpalAudioOutputAdapter", {
  kind: "rust-adapter",
  description: "CpalAudioOutput: cpal-backed implementation of the AudioOutput port",
  prompts: [
    "File path: src/Shell/CpalAudioOutput.rs",
    "Implement the AudioOutput and AudioStream traits from Shell::AudioOutput",
    "Use cpal::traits::{DeviceTrait, HostTrait, StreamTrait}",
    "CpalAudioStream wraps a cpal::Stream and stores the SampleRate",
    "CpalAudioOutput struct implements AudioOutput::open_stream by selecting the default output device and building an output stream",
    "The audio callback receives samples via a ring buffer or channel — do NOT use Arc<Mutex> on the audio thread",
    "Use std::sync::mpsc::SyncSender to push AudioFrame data from write_buffer into the audio callback",
    "write_buffer MUST use blocking send(), NOT try_send() — the caller must block when the channel is full so the render loop rate-limits to the audio callback's consumption speed; try_send silently drops frames and causes only the first note of a sequence to be heard",
    "The callback drains the receiver and writes interleaved f32 samples (left, right) to the cpal output buffer",
    "If the receiver is empty, fill with silence (0.0) to avoid underruns",
    "Include a Drop impl on CpalAudioStream that stops the stream",
  ],
});

// ── Shell Module (override) ────────────────────────────
// Shell gains the CpalAudioOutput adapter module.

shell.asset("ShellMod", {
  kind: "rust-module-declaration",
  description: "src/Shell/mod.rs module declarations for Shell context",
  prompts: [
    "File path: src/Shell/mod.rs",
    "Declare modules for: AudioOutput, MidiInput, MidiNormalizer, AppWindow, CpalAudioOutput",
  ],
});

// ── ToneTestMain (override) ────────────────────────────
// The binary now plays through speakers in real time, not just WAV.

app.asset("ToneTestMain", {
  kind: "rust-binary",
  description: "src/main.rs: real-time audio playback through speakers using cpal",
  prompts: [
    "File path: src/main.rs",
    "Import kernel types, Synth::AudioRenderer, and Shell::CpalAudioOutput",
    "Support two modes: real-time (default) and WAV (--wav flag)",
    "Real-time mode:",
    "  - Create a CpalAudioOutput adapter and open a stream at 44100 Hz",
    "  - Create an AudioRenderer at the stream's sample rate",
    "  - Play the same C4-E4-G4 arpeggio: notes at 0.0s, 0.5s, 1.0s; each ~0.4s duration",
    "  - Use a loop: render 256-sample blocks with AudioRenderer, write each block to the audio stream via AudioOutput::write_buffer",
    "  - Trigger note_on/note_off at the correct sample offsets within the loop",
    "  - Sleep for the total duration (3 seconds) then close the stream",
    "  - Print 'Playing C4-E4-G4 arpeggio through speakers...' at start",
    "WAV mode (--wav flag):",
    "  - Keep the existing behavior: render to memory and write tone-test.wav",
    "  - Use the pure-Rust WAV writer (no external crates)",
    "Parse args with std::env::args (no clap dependency needed)",
  ],
});
