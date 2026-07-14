package crestsynth

// Phase 6 adds sample-based instruments with file decoding, key/velocity zone
// selection, looping metadata, and pitch-shifted interpolation.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, built around a hard real-time audio core."
	actors: sound_designer: {description: "a musician building playable instruments from recorded samples"}
	goals: play_sample_instrument: {
		description: "A sound designer can map samples across keys and velocities and play them at the requested pitch"
		priority: "required"
		actors: ["actor.sound_designer"]
		capabilities: ["capability.load_and_render_sample_zones"]
		requirements: ["requirement.deterministic_zone_resolution"]
	}
	capabilities: load_and_render_sample_zones: {
		description: "Decode WAV or SF2 data, resolve key/velocity zones, and interpolate sample playback at performance pitch"
		goals: ["goal.play_sample_instrument"]
		acceptance: hermetic_sample_passage: {
			description: "A synthesized fixture sample loads, maps to several zones, pitch-shifts, and renders audibly"
			actor: "actor.sound_designer"
			steps: [{action: "load a sample set", observes: "decoded samples and valid zones are stored"}, {action: "play notes across zones", observes: "the matching samples render with interpolation"}]
			evidence: ["evidence.sample_demo"]
		}
	}
	requirements: deterministic_zone_resolution: {kind: "functional", description: "Every key and velocity resolves only to matching zones and interpolation reads valid sample data", goals: ["goal.play_sample_instrument"], capabilities: ["capability.load_and_render_sample_zones"]}
	evidence: sample_demo: {kind: "behavioral_witness", description: "make demo-samples synthesizes its own fixture, loads it, resolves zones, and renders a WAV"}
	completion: {requiredGoals: ["goal.play_sample_instrument"], projectChecks: ["validation.build", "validation.demo_samples"]}
}
