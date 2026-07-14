package crestsynth

// Product intent for crest-synth itself. The actors are people using the
// instrument; generation and verification are crest-spec concerns, not goals
// the synthesizer is aware of.
project: {
	actors: {
		performer: {description: "a musician playing crest-synth from external MIDI hardware"}
		sound_designer: {description: "a musician shaping patches, routing, effects, and saved setups"}
		producer: {description: "a musician hosting the same engine in a CLAP or VST3 workflow"}
	}

	goals: {
		perform_instrument: {
			description: "A performer can play expressive polyphonic sounds from external MIDI and hear safe stereo output"
			priority: "required"
			actors: ["actor.performer"]
			capabilities: ["capability.play_external_midi"]
			requirements: ["requirement.hard_realtime_audio"]
		}
		build_multitimbral_sound: {
			description: "A sound designer can combine patches, samples, modulation, effects, and mixer routing into a complete sound"
			priority: "required"
			actors: ["actor.sound_designer"]
			dependsOn: ["goal.perform_instrument"]
			capabilities: ["capability.shape_and_mix_sound"]
			requirements: ["requirement.canonical_signal_flow"]
		}
		preserve_work: {
			description: "A sound designer can save and restore patches and complete setups without corrupting current state"
			priority: "required"
			actors: ["actor.sound_designer"]
			dependsOn: ["goal.build_multitimbral_sound"]
			capabilities: ["capability.roundtrip_presets_and_setups"]
			requirements: ["requirement.atomic_versioned_restore"]
		}
		edit_from_steam_deck: {
			description: "A sound designer can edit a live instrument with keyboard or gamepad and without mouse or touch input"
			priority: "required"
			actors: ["actor.performer", "actor.sound_designer"]
			dependsOn: ["goal.perform_instrument"]
			capabilities: ["capability.edit_via_one_way_control_loop"]
			requirements: ["requirement.gamepad_keyboard_parity"]
		}
		host_as_plugin: {
			description: "A producer can use the same synthesis engine as a CLAP or VST3 instrument"
			priority: "optional"
			actors: ["actor.producer"]
			dependsOn: ["goal.perform_instrument"]
			capabilities: ["capability.run_in_plugin_host"]
		}
	}

	capabilities: {
		play_external_midi: {
			description: "Normalize external MIDI, dispatch notes, render voices, and deliver non-silent stereo audio"
			goals: ["goal.perform_instrument"]
			acceptance: audible_external_note: {
				description: "An external MIDI note drives the live engine and produces audible, non-clipping output"
				actor: "actor.performer"
				steps: [
					{action: "send note-on from external MIDI", observes: "the matching patch activates a voice"},
					{action: "render the audio callback", observes: "the output is non-silent and bounded"},
				]
				evidence: ["evidence.live_audio_smoke"]
			}
		}
		shape_and_mix_sound: {
			description: "Combine multiple patches with sample playback, modulation, ordered effects, sends, buses, and master output"
			goals: ["goal.build_multitimbral_sound"]
			acceptance: multitimbral_render: {
				description: "A multi-channel arrangement renders distinct patches through the canonical mixer signal path"
				actor: "actor.sound_designer"
				evidence: ["evidence.multitimbral_demo"]
			}
		}
		roundtrip_presets_and_setups: {
			description: "Serialize versioned patch and setup state and restore it atomically"
			goals: ["goal.preserve_work"]
			acceptance: bit_exact_roundtrip: {
				description: "A saved setup reloads to equivalent state and renders bit-identical audio"
				actor: "actor.sound_designer"
				evidence: ["evidence.preset_roundtrip"]
			}
		}
		edit_via_one_way_control_loop: {
			description: "Translate keyboard and gamepad input into EditorEvents reduced through one store and published as parameter snapshots"
			goals: ["goal.edit_from_steam_deck"]
			acceptance: device_free_editor_journey: {
				description: "Keyboard and gamepad can navigate, enter momentary edit mode, and change bounded parameters headlessly"
				actor: "actor.sound_designer"
				evidence: ["evidence.editor_audio_smoke"]
			}
		}
		run_in_plugin_host: {
			description: "Expose engine processing, stable parameters, MIDI routing, and state through CLAP/VST3"
			goals: ["goal.host_as_plugin"]
			acceptance: host_processes_audio: {
				description: "A plugin host can initialize the instrument, automate parameters, process MIDI, and persist state"
				actor: "actor.producer"
				evidence: ["evidence.plugin_contract"]
			}
		}
	}

	requirements: {
		hard_realtime_audio: {kind: "nonfunctional", description: "The audio callback never allocates, locks, blocks, performs I/O, or frees retired memory", goals: ["goal.perform_instrument"], capabilities: ["capability.play_external_midi"]}
		canonical_signal_flow: {kind: "functional", description: "Audio follows the declared strip, send, bus, master, limiter, and output order", goals: ["goal.build_multitimbral_sound"], capabilities: ["capability.shape_and_mix_sound"]}
		atomic_versioned_restore: {kind: "functional", description: "Persisted data is versioned and a failed restore leaves all prior state unchanged", goals: ["goal.preserve_work"], capabilities: ["capability.roundtrip_presets_and_setups"]}
		gamepad_keyboard_parity: {kind: "functional", description: "Every editor operation is reachable by keyboard and gamepad without mouse or touch", goals: ["goal.edit_from_steam_deck"], capabilities: ["capability.edit_via_one_way_control_loop"]}
	}

	evidence: {
		live_audio_smoke: {kind: "integration_validation", description: "The live pipeline constructs and the UI smoke path renders non-silent audio without opening devices"}
		multitimbral_demo: {kind: "behavioral_witness", description: "The patch, modulation, sample, effects, and mixer demos produce their declared audible artifacts"}
		preset_roundtrip: {kind: "behavioral_witness", description: "The preset demo reloads equivalent state and compares rendered output bit-for-bit"}
		editor_audio_smoke: {kind: "behavioral_witness", description: "The headless editor event journey changes bounded state and renders non-silent audio"}
		plugin_contract: {kind: "integration_validation", description: "The plugin wrapper builds and its host-facing parameter and state contracts pass"}
	}

	nonGoals: {
		onscreen_keyboard: "The editor does not trigger notes; performance input comes from external MIDI hardware"
		mouse_touch_ui: "The editor is intentionally keyboard/gamepad driven"
		cloud_library: "Cloud preset synchronization and online accounts are outside crest-synth"
	}

	completion: {
		requiredGoals: ["goal.perform_instrument", "goal.build_multitimbral_sound", "goal.preserve_work", "goal.edit_from_steam_deck"]
		projectChecks: ["validation.clippy", "validation.build", "validation.test", "validation.ui_smoke"]
	}
}
