package crestsynth

// Phase 3: Harden the real-time seam
// Lock-free boundary (rtrb + triple_buffer + basedrop).
// Wire up cpal so the synth plays through speakers.

// ── RealTime context ───────────────────────────────────

project: contexts: RealTime: purpose: "lock-free boundary between the audio thread and non-real-time threads"
project: contexts: RealTime: ubiquitousLanguage: {
	EventRingBuffer:   "lock-free SPSC ring buffer for discrete messages to the audio thread (rtrb)"
	ParameterSnapshot: "triple-buffered latest-wins parameter state readable by the audio thread"
	DeferredDrop:      "memory retired by the audio thread and freed later on a non-RT thread (basedrop)"
}

project: contexts: RealTime: valueObjects: BoundaryMessage: {
	state:       {kind: "BoundaryMessageKind", payload: "Vec<u8>", sequenceNumber: "u64"}
	description: "a discrete message crossing the RT boundary via the ring buffer"
}
project: contexts: RealTime: valueObjects: ParameterSnapshot: {
	state:       {oscillator: "OscillatorConfig", filter: "FilterConfig", ampEnvelope: "AmpEnvelopeConfig", version: "u64"}
	description: "latest-wins snapshot of all synth parameters, readable without locking"
}

project: contexts: RealTime: ports: EventRingBuffer:     {contract: {push: "BoundaryMessage -> Result<(), Full>", pop: "() -> Option<BoundaryMessage>"}, meta: notes: "SPSC lock-free ring buffer (rtrb)"}
project: contexts: RealTime: ports: ParameterBridge:     {contract: {write: "ParameterSnapshot -> ()", read: "() -> &ParameterSnapshot"}, meta: notes: "triple_buffer: writer publishes; reader always gets latest without blocking"}
project: contexts: RealTime: ports: DeferredDeallocator: {contract: {retire: "Arc<T> -> ()", collect: "() -> ()"}, meta: notes: "basedrop: audio thread retires; background thread frees"}

// ── CpalAudioOutput adapter ────────────────────────────

project: adapters: CpalAudioOutput: {
	implements: "port.Shell.AudioOutput"
	layer:      "infrastructure"
	meta: notes: "cpal: cross-platform audio output (ALSA/PipeWire, WASAPI, CoreAudio)"
}

project: assets: CpalAudioOutputAdapter: {
	kind:        "rust-adapter"
	description: "CpalAudioOutput: cpal-backed implementation of the AudioOutput port"
	prompts: [
		"File path: src/Shell/CpalAudioOutput.rs",
		"Implement AudioOutput and AudioStream traits from Shell::AudioOutput",
		"Use cpal::traits::{DeviceTrait, HostTrait, StreamTrait}",
		"Use std::sync::mpsc::SyncSender to push AudioFrame data into the audio callback",
		"write_buffer MUST use blocking send(), NOT try_send() — try_send silently drops frames",
		"If the receiver is empty, fill with silence (0.0) to avoid underruns",
	]
}

// ── ToneTestMain validation ────────────────────────────

project: assets: ToneTestMain: validations: [
	{kind: "integration", command: ["cargo", "run", "--", "--wav"], description: "arpeggio renders to WAV", assertions: [
		{kind: "exit_code", expected: 0},
		{kind: "file_exists", path: "tone-test.wav"},
	]},
]

// ── Invariants ─────────────────────────────────────────

project: invariants: realtimeSafety: [
	{text: "audio thread must never allocate heap memory", meta: rationale: "any allocation risks missing the audio buffer deadline"},
	{text: "audio thread must never acquire a mutex or blocking lock", meta: rationale: "lock contention causes unbounded latency"},
	{text: "audio thread must never perform blocking I/O", meta: rationale: "I/O has unpredictable latency incompatible with audio deadlines"},
	{text: "all parameter changes cross the boundary via ParameterBridge or EventRingBuffer", meta: rationale: "enforces the lock-free seam"},
	{text: "retired memory freed via DeferredDeallocator, never directly", meta: rationale: "basedrop ensures free() never runs on the audio thread"},
	{text: "rendered audio frames must never be silently dropped", meta: rationale: "try_send on a full channel causes notes to go missing"},
]
