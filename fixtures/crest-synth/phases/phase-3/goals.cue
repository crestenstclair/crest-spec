package crestsynth

// Phase 3 establishes the product's defining constraint: the live audio
// callback is connected to non-real-time control only through one lock-free seam.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, built around a hard real-time audio core."
	actors: performer: {description: "a musician hearing crest-synth through a physical audio device"}
	goals: hear_live_realtime_audio: {
		description: "A performer can stream synthesized MIDI playback through the default audio device without compromising the callback deadline"
		priority: "required"
		actors: ["actor.performer"]
		capabilities: ["capability.stream_across_realtime_seam"]
		requirements: ["requirement.callback_never_blocks"]
	}
	capabilities: stream_across_realtime_seam: {
		description: "Move events, latest parameter snapshots, and retired memory across lock-free boundaries and render through cpal"
		goals: ["goal.hear_live_realtime_audio"]
		acceptance: device_free_live_pipeline: {
			description: "The complete live pipeline constructs and exchanges data without requiring an audio device in validation"
			actor: "actor.performer"
			steps: [{action: "push events and publish parameters", observes: "the audio side receives events and the latest snapshot without locking"}, {action: "construct live output", observes: "the cpal callback path is ready to stream"}]
			evidence: ["evidence.live_pipeline_check"]
		}
	}
	requirements: callback_never_blocks: {kind: "nonfunctional", description: "The audio callback never allocates, locks, blocks, performs I/O, or frees retired memory", goals: ["goal.hear_live_realtime_audio"], capabilities: ["capability.stream_across_realtime_seam"]}
	evidence: live_pipeline_check: {kind: "integration_validation", description: "make check-live constructs and exercises the real-time pipeline without opening a device"}
	nonGoals: {device_validation: "Automated validation does not depend on a particular host audio device"}
	completion: {requiredGoals: ["goal.hear_live_realtime_audio"], projectChecks: ["validation.build", "validation.check_live"]}
}
