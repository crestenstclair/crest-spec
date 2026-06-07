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

project: contexts: Synth: domainServices: VoiceAllocator: {purpose: "assigns incoming notes to voices, stealing oldest/quietest when pool is full", uses: ["aggregate.Synth.Voice"]}
project: contexts: Synth: domainServices: AudioRenderer:  {purpose: "iterates all active voices, renders each through the engine, mixes to output", uses: ["aggregate.Synth.Voice"]}
