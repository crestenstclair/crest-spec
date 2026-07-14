package crestsynth

// Phase 1 product slice: the first audible MIDI-to-audio spine. MIDI-file
// playback is the hermetic stand-in for the external MIDI source added later.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, built around a hard real-time audio core."
	actors: performer: {description: "a musician listening to a MIDI performance through crest-synth"}
	goals: hear_midi_performance: {
		description: "A performer can turn a Standard MIDI File into an audible stereo performance"
		priority: "required"
		actors: ["actor.performer"]
		capabilities: ["capability.render_midi_file"]
		requirements: ["requirement.audible_bounded_output"]
	}
	capabilities: render_midi_file: {
		description: "Parse timed MIDI events, drive sine voices, and render a stereo WAV"
		goals: ["goal.hear_midi_performance"]
		acceptance: built_in_tune: {
			description: "The built-in tune renders through the phase-1 voice path to a non-empty WAV"
			actor: "actor.performer"
			steps: [{action: "load the built-in tune", observes: "time-ordered MIDI events"}, {action: "render the events", observes: "an audible stereo WAV"}]
			evidence: ["evidence.midi_demo"]
		}
	}
	requirements: audible_bounded_output: {kind: "functional", description: "The rendered file contains non-silent stereo samples within the valid amplitude range", goals: ["goal.hear_midi_performance"], capabilities: ["capability.render_midi_file"]}
	evidence: midi_demo: {kind: "behavioral_witness", description: "make demo-midi renders the built-in tune and its WAV assertions pass"}
	nonGoals: {live_devices: "This slice is device-free; live MIDI and audio devices arrive after the real-time seam"}
	completion: {requiredGoals: ["goal.hear_midi_performance"], projectChecks: ["validation.build", "validation.demo_midi"]}
}
