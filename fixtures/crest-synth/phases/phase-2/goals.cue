package crestsynth

// Cumulative product intent for the crest-synth design represented here.
// New functionality extends the prior definition of done; it does not replace it.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer built in Rust — designed for the Steam Deck, runs on any desktop. Hexagonal architecture around a hard real-time audio thread: the audio callback has a hard deadline and must never allocate, lock, or block. Two threads — real-time audio and UI/MIDI — communicate only across a lock-free boundary (ring buffer for events, latest-wins snapshots for parameters, deferred deallocation for retired memory)."

	actors: {
		performer: {description: "a musician playing crest-synth from arranged events or external MIDI hardware"}
		sound_designer: {description: "a musician creating, editing, mixing, and recalling playable sounds"}
	}

	goals: {
		produce_audible_synthesis: {
			description: "A musician can turn an arranged stream of note events into audible, bounded stereo sound"
			priority: "required"
			actors: ["actor.performer"]
			capabilities: ["capability.render_scheduled_notes"]
			requirements: ["requirement.audible_bounded_output"]
		}
		perform_polyphonic_music: {
			description: "A performer can play overlapping notes with oscillator, filter, envelope, and deterministic voice-stealing behavior"
			priority: "required"
			actors: ["actor.performer"]
			dependsOn: ["goal.produce_audible_synthesis"]
			capabilities: ["capability.render_polyphonic_voices"]
			requirements: ["requirement.bounded_polyphony"]
		}
	}

	capabilities: {
		render_scheduled_notes: {
			description: "Translate scheduled note-on and note-off events into a rendered stereo performance"
			goals: ["goal.produce_audible_synthesis"]
			acceptance: audible_arrangement: {
				description: "An arranged note sequence renders a non-silent, non-clipping stereo artifact with correct note timing"
				actor: "actor.performer"
				steps: [
					{action: "load or construct an arranged sequence of note events", observes: "note-on and note-off events retain their intended timing"},
					{action: "render the complete sequence", observes: "the stereo result is audible, bounded, and contains every scheduled note"},
				]
				evidence: ["evidence.offline_audio_render"]
			}
		}
		render_polyphonic_voices: {
			description: "Allocate, render, release, and when necessary steal voices from a bounded polyphonic pool"
			goals: ["goal.perform_polyphonic_music"]
			acceptance: polyphonic_voice_lifecycle: {
				description: "An over-polyphonic passage remains audible while envelopes and the selected stealing policy govern every voice"
				actor: "actor.performer"
				steps: [
					{action: "play overlapping notes up to the configured voice limit", observes: "each note owns an active voice with the configured oscillator, filter, and envelope"},
					{action: "play another note after the pool is full", observes: "exactly one voice is stolen according to policy and the passage continues"},
				]
				evidence: ["evidence.polyphonic_voice_render"]
			}
		}
	}

	requirements: {
		audible_bounded_output: {
			kind: "functional"
			description: "Rendered note events produce non-silent stereo output whose samples remain in the declared amplitude bounds"
			goals: ["goal.produce_audible_synthesis"]
			capabilities: ["capability.render_scheduled_notes"]
		}
		bounded_polyphony: {
			kind: "functional"
			description: "Voice count never exceeds the configured limit and exhausted pools apply the declared stealing policy"
			goals: ["goal.perform_polyphonic_music"]
			capabilities: ["capability.render_polyphonic_voices"]
		}
	}

	evidence: {
		offline_audio_render: {
			kind: "behavioral_witness"
			description: "A complete scheduled performance renders to a measurable non-silent, bounded stereo artifact"
		}
		polyphonic_voice_render: {
			kind: "behavioral_witness"
			description: "An over-polyphonic render proves voice allocation, envelope stages, release, and stealing behavior"
		}
	}

	nonGoals: {
		plugin_formats: "crest-synth is a standalone instrument; CLAP, VST3, AU, and other DAW plug-in formats are outside the product"
		onscreen_performance: "The editor does not trigger notes and has no on-screen keyboard; live performance input comes from external MIDI hardware"
		midi_file_replacement: "Arranged-event and MIDI-file rendering support verification and demonstration; they do not replace external MIDI as the live performance surface"
		mouse_touch_requirement: "The instrument must remain operable without requiring mouse or touch input"
		cloud_library: "Online accounts and cloud synchronization for presets or setups are outside crest-synth"
	}

	completion: {
		requiredGoals: [
			"goal.produce_audible_synthesis",
			"goal.perform_polyphonic_music",
		]
		projectChecks: [
			"validation.format",
			"validation.clippy",
			"validation.build",
			"validation.test",
			"validation.offline_audio_render",
			"validation.polyphonic_voice_render",
		]
	}
}
