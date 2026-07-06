package crestsynth

// Phase 2: Real polyphonic engine
// Voice allocation with stealing, oscillator, filter, amp envelope.
// Replaces throwaway Audio context from phase 1.

// ── Kernel additions ───────────────────────────────────

project: contexts: Kernel: valueObjects: Frequency: {from: "f64", description: "frequency in Hz", invariants: ["must be positive"]}
project: contexts: Kernel: valueObjects: Amplitude: {from: "f64", description: "linear amplitude (0.0 = silence, 1.0 = unity)", invariants: ["must be non-negative"]}

// ── Synth context ──────────────────────────────────────

project: contexts: Synth: purpose: "polyphonic synthesis engine: voice management, oscillator, filter, envelope"
project: contexts: Synth: ubiquitousLanguage: {
	Voice:         "a single sounding note with its own oscillator, filter, and envelope state"
	VoiceStealing: "reusing the oldest or quietest voice when polyphony limit is reached"
	EnvelopeStage: "current phase of an ADSR envelope: attack, decay, sustain, release, idle"
}

project: contexts: Synth: valueObjects: EnvelopeStage:    {from: "enum", description: "ADSR envelope phase: Idle, Attack, Decay, Sustain, Release"}
project: contexts: Synth: valueObjects: OscillatorConfig: {state: {waveform: "Waveform", detune: "f64", pulseWidth: "f64"}, description: "oscillator parameters"}
project: contexts: Synth: valueObjects: FilterConfig: {
	state:       {cutoff: "Frequency", resonance: "f64", filterType: "FilterType"}
	description: "resonant filter parameters"
	invariants: ["resonance must be 0.0-1.0", "cutoff must be within audible range"]
}
project: contexts: Synth: valueObjects: AmpEnvelopeConfig: {
	state:       {attack: "f64", decay: "f64", sustain: "f64", release: "f64"}
	description: "ADSR envelope times (seconds) and sustain level (0.0-1.0)"
	invariants: ["attack, decay, release must be non-negative", "sustain must be 0.0-1.0"]
}

project: contexts: Synth: aggregates: Voice: {
	root:    true
	purpose: "a single sounding note: oscillator + filter + amp envelope"
	state: {
		noteId: "NoteId", noteNumber: "NoteNumber", velocity: "Velocity", frequency: "Frequency",
		oscillatorPhase: "f64", filterState: "FilterState",
		envelopeStage: "EnvelopeStage", envelopeLevel: "Amplitude", active: "bool",
	}
	commands: [
		{name: "NoteOn", payload: {noteId: "NoteId", noteNumber: "NoteNumber", velocity: "Velocity"}},
		{name: "NoteOff", payload: {noteId: "NoteId"}},
	]
	events: [
		{name: "VoiceActivated", payload: {noteId: "NoteId", noteNumber: "NoteNumber", frequency: "Frequency"}},
		{name: "VoiceReleased", payload: {noteId: "NoteId"}},
		{name: "VoiceFinished", payload: {noteId: "NoteId"}},
		{name: "VoiceStolen", payload: {oldNoteId: "NoteId", newNoteId: "NoteId"}},
	]
	invariants: [
		"frequency derived from noteNumber and any pitch modulation",
		"envelope progresses Idle -> Attack -> Decay -> Sustain -> Release -> Idle",
		"voice is reclaimable only when envelope reaches Idle",
	]
}

project: contexts: Synth: ports: SynthEngine: contract: {
	renderBlock: "(Voice, OscillatorConfig, FilterConfig) -> [AudioFrame]"
	noteOn:      "(Voice, NoteOn) -> Voice"
	noteOff:     "(Voice, NoteOff) -> Voice"
	isFinished:  "Voice -> bool"
}
project: contexts: Synth: invariants: synthEngineTuning: [
	{text: "note number maps to frequency by equal temperament: hz = 440 * 2^((note - 69)/12)", meta: rationale: "A4 (note 69) = 440 Hz; standard 12-TET"},
	{text: "detune is expressed in SEMITONES and applied as a frequency ratio of 2^(semitones/12) — never 2^semitones; the per-sample oscillator phase increment is frequency_hz * 2^(detune/12) / sample_rate", meta: rationale: "omitting the /12 makes detune act in octaves, dropping pitch catastrophically (e.g. detune -2 → two octaves down)"},
]

project: contexts: Synth: domainServices: VoiceAllocator: {purpose: "assigns incoming notes to voices, stealing oldest/quietest when pool is full", uses: ["aggregate.Synth.Voice"]}
project: contexts: Synth: domainServices: AudioRenderer:  {purpose: "iterates all active voices, renders each through the engine, mixes to output", uses: ["aggregate.Synth.Voice"]}

// ── Voice stealing made audible (the phase-2 behavior prover) ──────────
// voice_demo deliberately over-drives polyphony so the VoiceAllocator's
// stealing path actually fires, then renders the result to WAV and reports
// a steal count and per-stage envelope markers. This is what mechanically
// proves "voice allocation with stealing" instead of merely declaring it.

project: assets: VoiceDemoMain: {
	kind:        "rust-bin-target"
	description: "src/bin/voice_demo.rs: over-polyphonic passage through SynthEngine/VoiceAllocator that forces voice stealing, renders to WAV"
	uses: ["aggregate.Synth.Voice", "domainService.Synth.VoiceAllocator", "domainService.Synth.AudioRenderer", "port.Synth.SynthEngine"]
	prompts: [
		"File path: src/bin/voice_demo.rs",
		"CLI: `voice_demo [--out OUT.wav]`. Default output path voice-demo.wav.",
		"Build a VoiceAllocator with a DELIBERATELY SMALL polyphony limit (e.g. maxVoices = 4) and feed it a built-in passage that holds MORE simultaneous notes than the limit (e.g. a rolling cluster of 8-12 overlapping sustained notes), so the allocator is FORCED to steal voices to service new note-ons. The passage must guarantee stealing actually occurs for the chosen limit.",
		"Drive each note through the real engine: trigger note_on/note_off via the VoiceAllocator, render every active Voice through the SynthEngine port (oscillator → filter → amp envelope), and mix via the AudioRenderer in fixed sample blocks.",
		"Track and count every voice steal (each time the allocator reclaims an active voice to service a new note). Maintain a running total across the whole passage.",
		"Print per-stage envelope markers so the envelope progression is observable: at minimum print a line for each envelope stage transition observed (Attack/Decay/Sustain/Release) at least once, and a per-section summary.",
		#"Print EXACTLY a line containing the token `steals=` followed by the integer total number of voice steals for this passage, e.g. `steals=37`. The passage MUST be constructed so this count is NONZERO. Print this token verbatim (lowercase, no spaces around `=`) so a validation can assert on it."#,
		"Write 16-bit mono WAV (default voice-demo.wav, or --out) using a pure-Rust WAV writer (no external WAV crate).",
		"Exit 0 on success.",
	]
	validations: [
		{kind: "compiles", command: ["make", "build"], description: "voice demo builds"},
		{kind: "integration", command: ["make", "demo-voices"], description: "over-polyphonic passage renders to WAV and forces voice stealing", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "file_exists", path: "voice-demo.wav"},
			{kind: "stdout_contains", pattern: "steals="},
		]},
	]
}
