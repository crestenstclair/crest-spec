package crestsynth

// Product intent for crest-synth. These outcomes describe the complete
// standalone instrument; DDD resources below this layer explain how the
// product is implemented.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer built in Rust — designed for the Steam Deck, runs on any desktop. Hexagonal architecture around a hard real-time audio thread: the audio callback has a hard deadline and must never allocate, lock, or block. Two threads — real-time audio and UI/MIDI — communicate only across a lock-free boundary (ring buffer for events, latest-wins snapshots for parameters, deferred deallocation for retired memory)."

	actors: {
		performer: {
			description: "a musician playing crest-synth from external MIDI hardware"
		}
		sound_designer: {
			description: "a musician creating, editing, mixing, and recalling playable sounds"
		}
	}

	goals: {
		perform_live: {
			description: "A performer can play expressive, multitimbral sounds from external MIDI and hear a stable, non-clipping stereo output"
			priority: "required"
			actors: ["actor.performer"]
			capabilities: [
				"capability.accept_external_midi",
				"capability.render_expressive_sound",
				"capability.mix_to_stereo",
				"capability.operate_audio_and_midi_devices",
				"capability.preserve_realtime_safety",
			]
			requirements: [
				"requirement.external_performance_input",
				"requirement.hard_realtime_callback",
				"requirement.canonical_signal_flow",
			]
		}
		design_playable_sounds: {
			description: "A sound designer can create complete playable patches from synthesis or samples, modulation, effects, MIDI routing, and mixer configuration"
			priority: "required"
			actors: ["actor.sound_designer"]
			dependsOn: ["goal.perform_live"]
			capabilities: [
				"capability.configure_complete_patch",
				"capability.mix_to_stereo",
				"capability.edit_without_pointer",
			]
			requirements: [
				"requirement.canonical_signal_flow",
				"requirement.complete_patch_definition",
				"requirement.gamepad_keyboard_operation",
			]
		}
		preserve_work: {
			description: "A sound designer can organize, save, migrate, and restore patches, banks, and complete sessions without losing the current instrument state"
			priority: "required"
			actors: ["actor.sound_designer"]
			dependsOn: ["goal.design_playable_sounds"]
			capabilities: ["capability.save_and_restore_sound_library"]
			requirements: ["requirement.versioned_atomic_restore"]
		}
		operate_standalone: {
			description: "A musician can operate the complete instrument on a Steam Deck or desktop using external MIDI plus keyboard or gamepad controls"
			priority: "required"
			actors: ["actor.performer", "actor.sound_designer"]
			dependsOn: ["goal.perform_live", "goal.design_playable_sounds"]
			capabilities: [
				"capability.operate_audio_and_midi_devices",
				"capability.edit_without_pointer",
			]
			requirements: ["requirement.gamepad_keyboard_operation"]
		}
	}

	capabilities: {
		accept_external_midi: {
			description: "Normalize incoming MIDI, preserve channel and per-note expression, and dispatch each event to exactly the matching patches"
			goals: ["goal.perform_live"]
			acceptance: routed_performance: {
				description: "External note and expression events reach every intentionally layered patch and no unrelated patch"
				actor: "actor.performer"
				steps: [
					{action: "send note, controller, pitch, and pressure events on a configured address", observes: "all patches mapped to that address receive normalized events with stable note identity"},
					{action: "send the same events on an unmapped address", observes: "unrelated patches remain unchanged"},
				]
				evidence: ["evidence.midi_routing_contract"]
			}
		}
		render_expressive_sound: {
			description: "Render bounded polyphonic voices from virtual-analog, wavetable, FM, or sample sources with envelopes, filtering, and per-note expression"
			goals: ["goal.perform_live"]
			acceptance: expressive_polyphony: {
				description: "An over-polyphonic expressive passage produces audible stereo output while applying the configured voice-stealing policy"
				actor: "actor.performer"
				steps: [
					{action: "play more simultaneous notes than the patch polyphony limit", observes: "voices are allocated and stolen according to the selected policy"},
					{action: "apply pitch and pressure expression while rendering", observes: "the output remains audible, bounded, and responds per note"},
				]
				evidence: ["evidence.polyphonic_render"]
			}
		}
		configure_complete_patch: {
			description: "Create and edit a playable patch containing engine or sample configuration, modulation routes, MIDI mapping, and a mixer-strip assignment"
			goals: ["goal.design_playable_sounds"]
			acceptance: complete_patch_edit: {
				description: "Editing each part of a patch changes the intended sound without changing unrelated patch state"
				actor: "actor.sound_designer"
				steps: [
					{action: "configure a sound source, envelopes, filter, modulation routes, channel mapping, and mixer strip", observes: "the patch contains a coherent playable configuration"},
					{action: "play the edited patch beside a second patch", observes: "only the edited patch reflects its new configuration"},
				]
				evidence: ["evidence.patch_configuration"]
			}
		}
		mix_to_stereo: {
			description: "Process patch output through ordered inserts, volume and pan, sends, aux buses, master inserts, and the limiter"
			goals: ["goal.perform_live", "goal.design_playable_sounds"]
			acceptance: multitimbral_signal_path: {
				description: "Multiple patches remain independently controllable while following the canonical signal path to bounded stereo output"
				actor: "actor.sound_designer"
				steps: [
					{action: "route differently addressed patches through separate strips and effects", observes: "each strip controls only its assigned patch output"},
					{action: "send strips through aux and master processing", observes: "processors run in declared order and the limiter keeps the final output bounded"},
				]
				evidence: ["evidence.mixer_and_effects_path"]
			}
		}
		save_and_restore_sound_library: {
			description: "Browse and persist versioned presets and banks, and atomically replace the complete instrument from a saved session"
			goals: ["goal.preserve_work"]
			acceptance: versioned_round_trip: {
				description: "Saved sound and session state round-trips across versions without partial restoration"
				actor: "actor.sound_designer"
				steps: [
					{action: "save and reload a configured patch and session", observes: "patch, mixer, routing, tempo, and time-signature state is equivalent"},
					{action: "attempt to restore malformed or unsupported data", observes: "the previous complete session remains active and unchanged"},
				]
				evidence: ["evidence.preset_and_session_roundtrip"]
			}
		}
		operate_audio_and_midi_devices: {
			description: "Select and connect external MIDI input, open the desktop audio stream, and drive the same host-agnostic engine used by offline verification"
			goals: ["goal.perform_live", "goal.operate_standalone"]
			acceptance: live_standalone_pipeline: {
				description: "The standalone shell connects MIDI input to the real-time engine and delivers its frames to the selected audio output"
				actor: "actor.performer"
				steps: [
					{action: "select a MIDI input and audio output, then play an external note", observes: "the event crosses the real-time seam and the device callback receives non-silent frames"},
					{action: "close the application", observes: "device streams and MIDI connections shut down cleanly"},
				]
				evidence: ["evidence.live_standalone_pipeline"]
			}
		}
		preserve_realtime_safety: {
			description: "Move events, parameter snapshots, and retired memory across the audio boundary without allocation, locks, blocking I/O, or audio-thread deallocation"
			goals: ["goal.perform_live"]
			acceptance: realtime_boundary: {
				description: "Continuous performance and parameter editing remain within the declared lock-free audio-thread contract"
				actor: "actor.performer"
				steps: [
					{action: "publish parameter snapshots and MIDI events while audio renders", observes: "the callback reads the latest snapshot and consumes events without blocking"},
					{action: "replace owned audio state", observes: "retired memory is reclaimed away from the audio thread"},
				]
				evidence: ["evidence.realtime_boundary_contract"]
			}
		}
		edit_without_pointer: {
			description: "Navigate every instrument view and edit bounded parameters from keyboard or gamepad without mouse, touch, or an on-screen performance keyboard"
			goals: ["goal.design_playable_sounds", "goal.operate_standalone"]
			acceptance: complete_gamepad_journey: {
				description: "A gamepad can reach patch, mixer, preset, modulation, and MIDI configuration and perform the same edits as the keyboard controls"
				actor: "actor.sound_designer"
				steps: [
					{action: "navigate across all instrument views using only gamepad actions", observes: "focus reaches every editable control and clearly identifies its current mode"},
					{action: "enter momentary edit mode and adjust a bounded value", observes: "the value remains in range and the published parameter snapshot changes"},
					{action: "save and reload the edited setup", observes: "the complete journey succeeds without pointer or touch input"},
				]
				evidence: ["evidence.gamepad_editor_journey"]
			}
		}
	}

	requirements: {
		external_performance_input: {
			kind: "functional"
			description: "Performance notes originate from external MIDI hardware; routing supports layering and non-overlapping MPE zones"
			goals: ["goal.perform_live"]
			capabilities: ["capability.accept_external_midi"]
		}
		hard_realtime_callback: {
			kind: "nonfunctional"
			description: "The audio callback never allocates, locks, blocks, performs I/O, or frees retired memory"
			goals: ["goal.perform_live"]
			capabilities: ["capability.preserve_realtime_safety", "capability.operate_audio_and_midi_devices"]
		}
		canonical_signal_flow: {
			kind: "functional"
			description: "Audio follows engine or sample source through strip inserts, volume and pan, sends and buses, master inserts, limiter, and output"
			goals: ["goal.perform_live", "goal.design_playable_sounds"]
			capabilities: ["capability.mix_to_stereo"]
		}
		complete_patch_definition: {
			kind: "functional"
			description: "A patch owns its sound source, voice behavior, optional samples, modulation, MIDI mapping, and mixer assignment as one playable instrument"
			goals: ["goal.design_playable_sounds"]
			capabilities: ["capability.configure_complete_patch"]
		}
		versioned_atomic_restore: {
			kind: "functional"
			description: "Persisted formats are explicitly versioned and a failed session restore leaves all prior state unchanged"
			goals: ["goal.preserve_work"]
			capabilities: ["capability.save_and_restore_sound_library"]
		}
		gamepad_keyboard_operation: {
			kind: "functional"
			description: "Every instrument editing action is reachable from keyboard and gamepad without requiring mouse or touch input"
			goals: ["goal.design_playable_sounds", "goal.operate_standalone"]
			capabilities: ["capability.edit_without_pointer"]
		}
	}

	evidence: {
		midi_routing_contract: {
			kind: "integration_validation"
			description: "Normalized MIDI dispatch proves exact address matching, intentional layering, and isolated MPE zones"
		}
		polyphonic_render: {
			kind: "behavioral_witness"
			description: "A rendered expressive passage proves audible bounded output, envelope behavior, and voice stealing"
		}
		patch_configuration: {
			kind: "integration_validation"
			description: "A complete patch configuration drives its sound source, modulation, routing, and mixer assignment together"
		}
		mixer_and_effects_path: {
			kind: "behavioral_witness"
			description: "A multitimbral render proves independent strips, ordered effects, sends, aux returns, master processing, and limiting"
		}
		preset_and_session_roundtrip: {
			kind: "behavioral_witness"
			description: "Versioned patch and complete-session round trips prove equivalence and failed-load atomicity"
		}
		live_standalone_pipeline: {
			kind: "integration_validation"
			description: "The live pipeline wires external MIDI through the lock-free engine to desktop audio output"
		}
		realtime_boundary_contract: {
			kind: "behavioral_witness"
			description: "Instrumented execution proves event, snapshot, and deferred-deallocation behavior against the accepted implementation"
		}
		gamepad_editor_journey: {
			kind: "behavioral_witness"
			description: "A headless keyboard/gamepad journey edits bounded state, publishes it to audio, and saves the resulting setup"
		}
	}

	nonGoals: {
		plugin_formats: "crest-synth is a standalone instrument; CLAP, VST3, AU, and other DAW plug-in formats are outside the product"
		onscreen_performance: "The editor does not trigger notes and has no on-screen keyboard; performance input comes from external MIDI hardware"
		midi_sequencing: "MIDI-file playback is verification and demonstration input, not the product's performance workflow"
		mouse_touch_ui: "The instrument is intentionally operable without mouse or touch input"
		cloud_library: "Online accounts and cloud synchronization for presets or sessions are outside crest-synth"
	}

	completion: {
		requiredGoals: [
			"goal.perform_live",
			"goal.design_playable_sounds",
			"goal.preserve_work",
			"goal.operate_standalone",
		]
		projectChecks: [
			"validation.format",
			"validation.clippy",
			"validation.build",
			"validation.test",
			"validation.live_pipeline",
			"validation.ui_smoke",
		]
	}
}
