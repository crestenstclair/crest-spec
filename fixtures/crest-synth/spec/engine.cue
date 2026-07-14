package crestsynth

// Engine — polyphonic synthesis. Multiple engine types (virtual analog,
// wavetable, sample playback, FM) share one voice lifecycle.

project: contexts: Engine: purpose: "core sound generation: voices, oscillators, filters, envelopes, and voice allocation with stealing"

project: contexts: Engine: valueObjects: {
	EngineType: {description: "which synthesis engine a patch uses: virtual analog, wavetable, sample playback (delegates to the Sample context), or FM"}
	Waveform: {description: "oscillator waveform: sine, saw, square, triangle, noise, or wavetable"}
	OscillatorConfig: {
		state: {waveform: "Waveform", detuneCents: "f64", pulseWidth: "f64", unisonVoices: "u8", unisonSpread: "f64"}
		description: "oscillator settings for one voice"
		invariants: ["unisonVoices must be 1-16", "pulseWidth must be 0.0-1.0"]
	}
	FilterType: {description: "filter response: low-pass, high-pass, band-pass, notch, low shelf, high shelf, or peak"}
	FilterConfig: {
		state: {filterType: "FilterType", cutoffHz: "Frequency", resonance: "f64", drive: "f64", keyTracking: "f64", envelopeAmount: "f64"}
		description: "filter settings for one voice"
		invariants: ["cutoffHz must be 20-20000 Hz"]
	}
	EnvelopeConfig: {
		state: {attack: "f64", decay: "f64", sustain: "f64", release: "f64"}
		description: "ADSR envelope times (seconds) and sustain level"
		invariants: ["attack, decay, and release must be non-negative", "sustain must be 0.0-1.0"]
	}
	StealPolicy: {description: "which voice to steal when polyphony is exhausted: oldest, quietest, lowest velocity, or refuse"}
	VoiceConfig: {
		state: {engineType: "EngineType", oscillator: "OscillatorConfig", filter: "FilterConfig", ampEnvelope: "EnvelopeConfig", filterEnvelope: "EnvelopeConfig", pitchEnvelope: "EnvelopeConfig", maxPolyphony: "u8", stealPolicy: "StealPolicy"}
		description: "complete per-patch voice configuration"
		invariants: ["maxPolyphony must be positive (typically 8-64)"]
	}
}

project: contexts: Engine: aggregates: Voice: {
	root:    true
	purpose: "one sounding note: oscillator through filter through amp/filter/pitch envelopes, with per-note expression"
	state: {noteId: "NoteId", note: "NoteNumber", velocity: "Velocity", phase: "f64", config: "VoiceConfig"}
	commands: {
		Trigger: {note: "NoteNumber", velocity: "Velocity", noteId: "NoteId"}
		Release: {noteId: "NoteId"}
		ApplyExpression: {noteId: "NoteId", pressure: "f64", slide: "f64", pitchBend: "f64"}
	}
	events: {
		Triggered: {noteId: "NoteId"}
		Released: {noteId: "NoteId"}
		BecameIdle: {noteId: "NoteId"}
	}
	invariants: [
		"the amp envelope progresses Idle → Attack → Decay → Sustain → Release → Idle",
		"a voice is reclaimable only when its amp envelope has reached Idle",
		"per-note expression affects only the voice with the matching NoteId",
	]
	contributesTo: [{capability: "capability.render_expressive_sound", contribution: "owns the oscillator, filter, envelope, and per-note-expression lifecycle for one sounding note"}]
}

project: contexts: Engine: ports: {
	Oscillator: {
		contract: {
			render:  "(phase: f64, config: OscillatorConfig) -> f64"
			advance: "(phase: f64, frequency: Frequency, sampleRate: SampleRate) -> f64"
		}
	}
	Filter: {
		contract: {
			process: "(sample: f64, config: FilterConfig) -> f64"
			reset:   "() -> ()"
		}
	}
	EnvelopeGenerator: {
		contract: {
			trigger: "() -> ()"
			release: "() -> ()"
			tick:    "() -> f64"
		}
	}
}

project: contexts: Engine: domainServices: {
	VoiceAllocator: {
		purpose: "assigns incoming notes to voices, stealing per the configured policy when polyphony is exhausted"
		uses: ["aggregate.Engine.Voice"]
		contributesTo: [{capability: "capability.render_expressive_sound", contribution: "keeps polyphony bounded and applies the configured voice-stealing policy"}]
	}
	VoiceRenderer: {
		purpose: "renders one voice for one buffer: oscillator → filter → envelopes"
		uses: ["aggregate.Engine.Voice", "port.Engine.Oscillator", "port.Engine.Filter", "port.Engine.EnvelopeGenerator"]
	}
	EngineRenderer: {
		purpose: "iterates all active voices and sums their output to stereo"
		uses: ["domainService.Engine.VoiceRenderer", "domainService.Engine.VoiceAllocator"]
		contributesTo: [{capability: "capability.render_expressive_sound", contribution: "renders active voices into the stereo stream consumed by the mixer"}]
	}
}
