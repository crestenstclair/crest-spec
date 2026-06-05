import { invariant } from "../../../src/index.js";
import { app, kernel, shell, synth, voice } from "./crest-spec-phase-2.js";
export { app, kernel, shell, synth, voice } from "./crest-spec-phase-2.js";

// Phase 3: Harden the real-time seam
// Introduce rtrb + triple_buffer + basedrop.
// Move all parameter and patch changes across the lock-free boundary.
// Everything after this respects the audio deadline.

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
]);

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
