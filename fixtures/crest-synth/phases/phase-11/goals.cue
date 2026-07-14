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
		run_realtime_audio: {
			description: "A performer can hear the synthesis engine through a live audio stream without compromising the audio callback deadline"
			priority: "required"
			actors: ["actor.performer"]
			dependsOn: ["goal.perform_polyphonic_music"]
			capabilities: ["capability.cross_realtime_boundary", "capability.deliver_live_audio"]
			requirements: ["requirement.hard_realtime_callback"]
		}
		perform_multitimbral_music: {
			description: "A performer can layer or separate multiple complete patches by MIDI address and hear their independently mixed output"
			priority: "required"
			actors: ["actor.performer", "actor.sound_designer"]
			dependsOn: ["goal.run_realtime_audio"]
			capabilities: ["capability.route_and_mix_patches"]
			requirements: ["requirement.exact_patch_routing"]
		}
		perform_expressively: {
			description: "A performer can shape each patch and sounding note with envelopes, LFOs, MIDI controls, and per-note expression"
			priority: "required"
			actors: ["actor.performer", "actor.sound_designer"]
			dependsOn: ["goal.perform_multitimbral_music"]
			capabilities: ["capability.modulate_sound"]
			requirements: ["requirement.isolated_expression"]
		}
		play_sample_based_instruments: {
			description: "A sound designer can build playable instruments from WAV or SF2 material mapped by key and velocity"
			priority: "required"
			actors: ["actor.performer", "actor.sound_designer"]
			dependsOn: ["goal.perform_expressively"]
			capabilities: ["capability.render_sample_sets"]
			requirements: ["requirement.sample_zone_resolution"]
		}
		shape_sound_with_effects: {
			description: "A sound designer can process individual patches and the complete mix through ordered, bypassable reverb, chorus, and delay chains"
			priority: "required"
			actors: ["actor.sound_designer"]
			dependsOn: ["goal.perform_multitimbral_music"]
			capabilities: ["capability.process_effect_chains"]
			requirements: ["requirement.ordered_effect_processing"]
		}
		preserve_and_recall_sounds: {
			description: "A sound designer can save, browse, and restore complete patches, preset banks, and full multitimbral setups"
			priority: "required"
			actors: ["actor.sound_designer"]
			dependsOn: ["goal.perform_multitimbral_music"]
			capabilities: ["capability.roundtrip_presets_and_setups"]
			requirements: ["requirement.versioned_atomic_restore"]
		}
		operate_standalone_instrument: {
			description: "A musician can connect external MIDI, use desktop audio, and navigate the standalone instrument from a gamepad-friendly interface"
			priority: "required"
			actors: ["actor.performer", "actor.sound_designer"]
			dependsOn: ["goal.run_realtime_audio", "goal.perform_multitimbral_music"]
			capabilities: ["capability.accept_external_midi", "capability.navigate_instrument_by_gamepad"]
			requirements: ["requirement.external_midi_performance", "requirement.gamepad_reachable_interface"]
		}
		edit_live_instrument: {
			description: "A sound designer can edit the live instrument from keyboard or gamepad while external MIDI remains the only performance input"
			priority: "required"
			actors: ["actor.sound_designer"]
			dependsOn: ["goal.operate_standalone_instrument", "goal.preserve_and_recall_sounds"]
			capabilities: ["capability.edit_parameters_one_way"]
			requirements: ["requirement.editor_audio_separation"]
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
		cross_realtime_boundary: {
			description: "Move events, latest parameter snapshots, and retired memory across one lock-free audio boundary"
			goals: ["goal.run_realtime_audio"]
			acceptance: lock_free_control_path: {
				description: "Events and parameter changes reach the audio thread while memory reclamation remains off that thread"
				actor: "actor.performer"
				steps: [
					{action: "publish events and parameter snapshots while rendering", observes: "the callback consumes events and reads the latest snapshot without blocking"},
					{action: "replace audio-owned state", observes: "retired memory is reclaimed by the non-audio side"},
				]
				evidence: ["evidence.realtime_boundary_contract"]
			}
		}
		deliver_live_audio: {
			description: "Drive the polyphonic renderer from a desktop audio callback and deliver its frames to the selected output"
			goals: ["goal.run_realtime_audio"]
			acceptance: live_audio_stream: {
				description: "The live pipeline constructs and continuously requests audible frames from the accepted engine"
				actor: "actor.performer"
				steps: [
					{action: "open a configured audio output", observes: "the device invokes the real-time render callback"},
					{action: "feed scheduled note events through the live path", observes: "the output receives non-silent bounded frames"},
				]
				evidence: ["evidence.live_audio_pipeline"]
			}
		}
		route_and_mix_patches: {
			description: "Dispatch addressed events to matching patch voice pools and combine their independently controlled stereo output"
			goals: ["goal.perform_multitimbral_music"]
			acceptance: multichannel_patch_performance: {
				description: "A multichannel performance reaches exactly the configured patches and renders them through independent gain and pan"
				actor: "actor.performer"
				steps: [
					{action: "map different patches to separate MIDI addresses and intentionally layer two on one address", observes: "the dispatcher selects exactly the matching patch set"},
					{action: "render all active patches through the global mix", observes: "each patch retains its own voice pool, gain, and pan"},
				]
				evidence: ["evidence.multitimbral_patch_render"]
			}
		}
		modulate_sound: {
			description: "Evaluate modulation sources and routes into bounded per-sample parameter offsets for the intended patch or note"
			goals: ["goal.perform_expressively"]
			acceptance: audible_modulation: {
				description: "Configured modulation changes the intended sound over time without leaking into unrelated notes or patches"
				actor: "actor.performer"
				steps: [
					{action: "route an LFO and envelope to audible destinations", observes: "the rendered patch follows the configured rate, depth, curve, and envelope"},
					{action: "apply per-note bend, timbre, and pressure", observes: "only the voice with the matching note identity changes"},
				]
				evidence: ["evidence.modulated_patch_render"]
			}
		}
		render_sample_sets: {
			description: "Load sample material, resolve matching key and velocity zones, and render pitched playback with interpolation and looping"
			goals: ["goal.play_sample_based_instruments"]
			acceptance: zoned_sample_performance: {
				description: "Notes select the correct sample zones and render them at the requested pitch through the existing patch path"
				actor: "actor.performer"
				steps: [
					{action: "load a sample set with distinct key and velocity zones", observes: "decoded material and zone metadata are available to the engine"},
					{action: "play notes across zone boundaries", observes: "every and only matching zone renders with configured interpolation, tuning, pan, and loop mode"},
				]
				evidence: ["evidence.sample_instrument_render"]
			}
		}
		process_effect_chains: {
			description: "Process per-patch and global audio through effect slots in declared order with independent bypass"
			goals: ["goal.shape_sound_with_effects"]
			acceptance: patch_and_master_effects: {
				description: "Patch and master chains audibly process the correct signal while bypass remains transparent"
				actor: "actor.sound_designer"
				steps: [
					{action: "configure different ordered chains on a patch and the master", observes: "each chain processes only its assigned signal in slot order"},
					{action: "bypass a slot or complete chain", observes: "the bypassed processor passes input through unchanged"},
				]
				evidence: ["evidence.effects_signal_path"]
			}
		}
		roundtrip_presets_and_setups: {
			description: "Serialize versioned patch and setup state, browse saved sounds, and restore complete state atomically"
			goals: ["goal.preserve_and_recall_sounds"]
			acceptance: complete_sound_roundtrip: {
				description: "A configured multitimbral setup reloads equivalently and invalid data cannot leave a partial restore"
				actor: "actor.sound_designer"
				steps: [
					{action: "save and reload patches, their bank ordering, and a complete setup", observes: "voice, sample, modulation, routing, mix, and effect state is equivalent"},
					{action: "attempt to restore malformed or unsupported data", observes: "the previously active complete setup remains unchanged"},
				]
				evidence: ["evidence.preset_setup_roundtrip"]
			}
		}
		accept_external_midi: {
			description: "Discover and connect MIDI hardware, normalize incoming messages, and deliver them to the patch dispatcher"
			goals: ["goal.operate_standalone_instrument"]
			acceptance: external_controller_performance: {
				description: "A selected external MIDI controller drives the complete real-time patch and audio path"
				actor: "actor.performer"
				steps: [
					{action: "select a MIDI port and play notes and controls", observes: "normalized addressed events enter the matching patches"},
					{action: "disconnect or close the instrument", observes: "the MIDI connection and audio stream shut down cleanly"},
				]
				evidence: ["evidence.external_midi_pipeline"]
			}
		}
		navigate_instrument_by_gamepad: {
			description: "Map controller input to complete navigation, selection, adjustment, page switching, and quick-save actions"
			goals: ["goal.operate_standalone_instrument"]
			acceptance: gamepad_navigation: {
				description: "A supported controller reaches the standalone instrument controls with correct glyphs and bounded adjustments"
				actor: "actor.sound_designer"
				steps: [
					{action: "navigate pages and controls from a connected gamepad", observes: "focus follows the normalized gamepad actions and displays the correct controller glyphs"},
					{action: "adjust a value and quick-save", observes: "the bounded value changes and the save workflow is invoked"},
				]
				evidence: ["evidence.gamepad_navigation_path"]
			}
		}
		edit_parameters_one_way: {
			description: "Reduce keyboard and gamepad editor events through one store and publish bounded parameter snapshots to audio"
			goals: ["goal.edit_live_instrument"]
			acceptance: live_parameter_editing: {
				description: "Keyboard and gamepad can navigate and edit the same live parameter model without directly mutating audio state"
				actor: "actor.sound_designer"
				steps: [
					{action: "navigate fields and enter momentary edit mode from keyboard and gamepad", observes: "both inputs emit the same semantic events into the single editor store"},
					{action: "adjust a parameter while external MIDI plays", observes: "the bounded store value changes and a new parameter snapshot reaches audio"},
					{action: "interact with the editor without external MIDI notes", observes: "no performance note is triggered by the UI"},
				]
				evidence: ["evidence.editor_audio_smoke"]
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
		hard_realtime_callback: {
			kind: "nonfunctional"
			description: "The audio callback never allocates, locks, blocks, performs I/O, or frees retired memory"
			goals: ["goal.run_realtime_audio"]
			capabilities: ["capability.cross_realtime_boundary", "capability.deliver_live_audio"]
		}
		exact_patch_routing: {
			kind: "functional"
			description: "Each event reaches exactly the patches whose channel subscription matches; intentional layering is allowed and MPE zones do not overlap"
			goals: ["goal.perform_multitimbral_music"]
			capabilities: ["capability.route_and_mix_patches"]
		}
		isolated_expression: {
			kind: "functional"
			description: "Patch modulation remains within that patch and per-note expression affects only the voice with the matching note identity"
			goals: ["goal.perform_expressively"]
			capabilities: ["capability.modulate_sound"]
		}
		sample_zone_resolution: {
			kind: "functional"
			description: "A sample note renders every zone whose key and velocity ranges match and no zone that does not match"
			goals: ["goal.play_sample_based_instruments"]
			capabilities: ["capability.render_sample_sets"]
		}
		ordered_effect_processing: {
			kind: "functional"
			description: "Effect slots process strictly in declared order, contain no internal feedback loop, and bypass passes input through unchanged"
			goals: ["goal.shape_sound_with_effects"]
			capabilities: ["capability.process_effect_chains"]
		}
		versioned_atomic_restore: {
			kind: "functional"
			description: "Persisted formats carry explicit versions and a failed complete-setup restore leaves all prior state unchanged"
			goals: ["goal.preserve_and_recall_sounds"]
			capabilities: ["capability.roundtrip_presets_and_setups"]
		}
		external_midi_performance: {
			kind: "functional"
			description: "Performance notes originate from external MIDI hardware and are normalized before entering the domain model"
			goals: ["goal.operate_standalone_instrument"]
			capabilities: ["capability.accept_external_midi"]
		}
		gamepad_reachable_interface: {
			kind: "functional"
			description: "Standalone navigation and instrument-management actions are reachable from the declared gamepad action vocabulary"
			goals: ["goal.operate_standalone_instrument"]
			capabilities: ["capability.navigate_instrument_by_gamepad"]
		}
		editor_audio_separation: {
			kind: "functional"
			description: "Widgets only render store state, semantic editor events are its sole mutation input, and the audio model only receives parameter snapshots plus external MIDI"
			goals: ["goal.edit_live_instrument"]
			capabilities: ["capability.edit_parameters_one_way"]
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
		realtime_boundary_contract: {
			kind: "behavioral_witness"
			description: "Instrumented execution proves event, snapshot, and deferred-deallocation behavior"
		}
		live_audio_pipeline: {
			kind: "integration_validation"
			description: "The configured audio callback drives the accepted engine and receives non-silent bounded frames"
		}
		multitimbral_patch_render: {
			kind: "behavioral_witness"
			description: "A multichannel render proves dispatch, per-patch voice pools, layering, isolation, gain, pan, and final summing"
		}
		modulated_patch_render: {
			kind: "behavioral_witness"
			description: "An audible render proves LFO, envelope, routed parameter, and per-note expression behavior"
		}
		sample_instrument_render: {
			kind: "behavioral_witness"
			description: "A hermetic sample performance proves loading, zone matching, pitch, interpolation, and looping"
		}
		effects_signal_path: {
			kind: "behavioral_witness"
			description: "A rendered multitimbral mix proves per-patch and global effect assignment, slot order, and bypass transparency"
		}
		preset_setup_roundtrip: {
			kind: "behavioral_witness"
			description: "A save/reload render proves complete state equivalence, version handling, and failed-load atomicity"
		}
		external_midi_pipeline: {
			kind: "integration_validation"
			description: "The hardware-input adapter, normalizer, dispatcher, real-time seam, and audio output construct as one live path"
		}
		gamepad_navigation_path: {
			kind: "behavioral_witness"
			description: "A headless controller journey proves action mapping, focus movement, bounded adjustment, and controller glyph resolution"
		}
		editor_audio_smoke: {
			kind: "behavioral_witness"
			description: "A headless keyboard/gamepad event journey proves focus, edit mode, bounded values, snapshot publication, and non-silent audio"
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
			"goal.run_realtime_audio",
			"goal.perform_multitimbral_music",
			"goal.perform_expressively",
			"goal.play_sample_based_instruments",
			"goal.shape_sound_with_effects",
			"goal.preserve_and_recall_sounds",
			"goal.operate_standalone_instrument",
			"goal.edit_live_instrument",
		]
		projectChecks: [
			"validation.format",
			"validation.clippy",
			"validation.build",
			"validation.test",
			"validation.offline_audio_render",
			"validation.polyphonic_voice_render",
			"validation.realtime_boundary",
			"validation.live_audio_pipeline",
			"validation.multitimbral_patch_render",
			"validation.modulated_patch_render",
			"validation.sample_instrument_render",
			"validation.effects_signal_path",
			"validation.preset_setup_roundtrip",
			"validation.external_midi_pipeline",
			"validation.gamepad_navigation",
			"validation.editor_audio_smoke",
		]
	}
}
