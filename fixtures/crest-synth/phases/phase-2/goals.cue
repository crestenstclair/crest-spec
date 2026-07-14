package crestsynth

// Phase 2 replaces the proof-of-path sine voice with the real polyphonic
// engine: oscillator, resonant filter, ADSR envelope, and voice stealing.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, built around a hard real-time audio core."
	actors: performer: {description: "a musician playing polyphonic synthesized sounds"}
	goals: play_polyphonic_instrument: {
		description: "A performer can hear expressive overlapping notes from a bounded polyphonic voice pool"
		priority: "required"
		actors: ["actor.performer"]
		capabilities: ["capability.synthesize_polyphony"]
		requirements: ["requirement.deterministic_voice_lifecycle"]
	}
	capabilities: synthesize_polyphony: {
		description: "Allocate voices, render oscillator/filter/envelope state, and steal a voice when polyphony is exhausted"
		goals: ["goal.play_polyphonic_instrument"]
		acceptance: forced_voice_steal: {
			description: "An over-polyphonic passage remains audible and exercises the configured stealing path"
			actor: "actor.performer"
			steps: [{action: "trigger more notes than available voices", observes: "a voice is stolen according to policy"}, {action: "render the passage", observes: "non-silent bounded audio and envelope-stage markers"}]
			evidence: ["evidence.voice_demo"]
		}
	}
	requirements: deterministic_voice_lifecycle: {kind: "functional", description: "Voice allocation is bounded and voices are reclaimable only after their envelope reaches Idle", goals: ["goal.play_polyphonic_instrument"], capabilities: ["capability.synthesize_polyphony"]}
	evidence: voice_demo: {kind: "behavioral_witness", description: "make demo-voices forces stealing and validates the rendered WAV and lifecycle markers"}
	nonGoals: {live_audio: "This slice still renders offline; device streaming belongs to the real-time boundary slice"}
	completion: {requiredGoals: ["goal.play_polyphonic_instrument"], projectChecks: ["validation.build", "validation.demo_voices"]}
}
