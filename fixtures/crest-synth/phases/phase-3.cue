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
		"Use an internal lock-free SPSC ring buffer (rtrb) of interleaved stereo f32: the producer half lives on the writing thread; the cpal data callback drains the consumer half, filling any shortfall with silence (0.0) so it never underruns or blocks.",
		"Implement availableFrames() -> usize: return the number of whole STEREO FRAMES of FREE space currently in the ring buffer (i.e. producer free f32 slots / 2). Callers use this to render exactly what fits so the buffer never overflows. It must be cheap and non-blocking.",
		"write_buffer(frames): push interleaved L,R into the ring. If the buffer is full, drop the excess SILENTLY — do NOT print to stderr per dropped frame (a paced caller that respects availableFrames never overflows; logging here just floods the console). At most, you may keep an internal dropped-frame counter, but emit nothing on the hot path.",
	]
}

// ── Live MIDI playback (the spine, now through speakers) ───────────────
// Same player as phase 1's midi_play, but live through the default output
// device via the cpal AudioOutput adapter instead of rendering to WAV.

project: assets: MidiPlayLiveMain: {
	kind:        "rust-bin-target"
	description: "src/bin/midi_play_live.rs: live MIDI-file player — streams a .mid (or built-in demo tune) through the default output device via cpal"
	uses: ["asset.MidiFileLoader", "asset.CpalAudioOutputAdapter", "domainService.Synth.AudioRenderer", "aggregate.Synth.Voice"]
	prompts: [
		"File path: src/bin/midi_play_live.rs",
		"CLI: `midi_play_live [FILE.mid] [--seconds N]`. If FILE is omitted, play the same built-in demo melody as midi_play. `--seconds N` optionally caps playback duration.",
		"Load FILE (when given) with the MidiFileLoader module into the time-ordered (seconds, MidiEvent) timeline; otherwise use the built-in demo timeline.",
		"Open the default output device through the CpalAudioOutput adapter (the Shell::AudioOutput port). Render the timeline through the phase-2/3 engine (Voice + AudioRenderer) in real time, writing rendered AudioFrames to the output stream as the wall clock advances; respect --seconds if set.",
		"If NO output device is available, exit with a clear non-zero status and a human-readable stderr message (e.g. \"no default output device\") — never panic.",
		"Print a startup line (device name, event count, duration) before streaming. Do NOT write a WAV file — this binary is for live audio only.",
		#"Support a `--no-device-dry-run` flag (mutually exclusive with live playback). In dry-run mode, parse the args and the timeline, and CONSTRUCT the full real-time pipeline objects — the rtrb event ring buffer, the triple_buffer ParameterBridge, and the basedrop DeferredDeallocator plumbing that the live path would use — WITHOUT opening any audio device. Then print EXACTLY a line containing the token `dry-run ok: pipeline constructed` and exit 0. This makes the realtime wiring mechanically checkable with no audio device present."#,
		"In dry-run mode never touch cpal's host/device APIs and never block on the wall clock; it must return 0 quickly and deterministically on any machine, including CI.",
	]
	validations: [
		// compiles-only for the live path: validations must NEVER open an audio device.
		{kind: "compiles", command: ["make", "build"], description: "live player compiles"},
		// device-free behavioral check: the --no-device-dry-run mode builds the
		// realtime pipeline (ring buffer / param bridge / deferred dealloc) and
		// exits 0 without opening a device.
		{kind: "integration", command: ["make", "check-live"], description: "realtime pipeline constructs without an audio device", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "stdout_contains", pattern: "dry-run ok"},
		]},
	]
}

// ── ToneTestMain validation ────────────────────────────
// Lives in phase-3.override-ToneTestMain.cue so it can replace phase 1's
// validation without a CUE list-unification conflict (see that file).

// ── Invariants ─────────────────────────────────────────

project: invariants: realtimeSafety: [
	{text: "audio thread must never allocate heap memory", meta: rationale: "any allocation risks missing the audio buffer deadline"},
	{text: "audio thread must never acquire a mutex or blocking lock", meta: rationale: "lock contention causes unbounded latency"},
	{text: "audio thread must never perform blocking I/O", meta: rationale: "I/O has unpredictable latency incompatible with audio deadlines"},
	{text: "all parameter changes cross the boundary via ParameterBridge or EventRingBuffer", meta: rationale: "enforces the lock-free seam"},
	{text: "retired memory freed via DeferredDeallocator, never directly", meta: rationale: "basedrop ensures free() never runs on the audio thread"},
	{text: "rendered audio frames must never be silently dropped", meta: rationale: "try_send on a full channel causes notes to go missing"},
]
