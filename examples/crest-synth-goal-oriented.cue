package crestsynth

// This executable sample is derived from fixtures/crest-synth. It shows the
// goal/acceptance layer composed with the existing DDD resource schemas:
// goals do not replace aggregates, services, ports, adapters, or assets.
project: {
	name:    "crest-synth-goal-oriented"
	mission: "Provide a standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, with external MIDI performance and a hard real-time audio core."

	actors: {
		performer: {description: "a musician playing sounds from external MIDI hardware"}
		sound_designer: {description: "a musician editing and recalling playable patches"}
	}

	goals: {
		play_instrument: {
			description: "A performer can play a note from external MIDI and hear safe stereo output"
			priority: "required"
			actors: ["actor.performer"]
			capabilities: ["capability.render_note", "capability.deliver_audio"]
			requirements: ["requirement.realtime_safe"]
		}
		edit_by_gamepad: {
			description: "A sound designer can edit a patch with keyboard or gamepad and without mouse or touch"
			priority: "required"
			actors: ["actor.sound_designer"]
			dependsOn: ["goal.play_instrument"]
			capabilities: ["capability.navigate_gamepad"]
			requirements: ["requirement.input_parity"]
		}
	}

	capabilities: {
		render_note: {
			description: "Normalize external MIDI, allocate a polyphonic voice, and render its output"
			goals: ["goal.play_instrument"]
			acceptance: audible_a4: {
				description: "A4 produces a non-clipping signal"
				actor: "actor.performer"
				steps: [
					{action: "send A4 from external MIDI hardware", observes: "the normalized event activates a voice"},
					{action: "render one second", observes: "stereo peak is audible and at most 1.0"},
				]
				evidence: ["evidence.audible_witness"]
			}
		}
		deliver_audio: {
			description: "Deliver rendered frames to the selected audio device"
			goals: ["goal.play_instrument"]
			acceptance: device_stream: {
				description: "Rendered frames reach an opened device stream"
				actor: "actor.performer"
				evidence: ["evidence.audio_integration"]
			}
		}
		navigate_gamepad: {
			description: "Map gamepad actions to every patch editor operation"
			goals: ["goal.edit_by_gamepad"]
			acceptance: edit_oscillator: {
				description: "A gamepad opens the editor and changes the oscillator without triggering a performance note"
				actor: "actor.sound_designer"
				steps: [
					{action: "open the editor by gamepad", observes: "the editor receives focus"},
					{action: "change waveform", observes: "patch state and view state update"},
				]
				evidence: ["evidence.gamepad_journey"]
			}
		}
	}

	requirements: {
		realtime_safe: {
			kind: "nonfunctional"
			description: "The audio callback does not allocate, lock, or block"
			goals: ["goal.play_instrument"]
			capabilities: ["capability.render_note", "capability.deliver_audio"]
		}
		input_parity: {
			kind: "functional"
			description: "Every editing action is reachable by keyboard and gamepad without mouse or touch"
			goals: ["goal.edit_by_gamepad"]
			capabilities: ["capability.navigate_gamepad"]
		}
	}

	evidence: {
		audible_witness: {
			kind: "behavioral_witness", description: "measured output from the accepted voice engine"
			witnesses: ["witness.audible_a4"]
		}
		audio_integration: {
			kind: "integration_validation", description: "frames observed at the audio adapter boundary"
			validations: ["validation.audio_integration"]
		}
		gamepad_journey: {
			kind: "behavioral_witness", description: "gamepad-only patch editing journey"
			witnesses: ["witness.gamepad_edit"]
		}
	}

	nonGoals: {
		cloud_presets: "Cloud preset synchronization is intentionally excluded"
		plugin_formats: "CLAP, VST3, AU, and other DAW plug-in formats are excluded"
		onscreen_keyboard: "The editor changes parameters but does not trigger notes; performance comes from external MIDI"
		midi_file_performance: "MIDI-file playback is a verification input, not the product performance workflow"
	}
	completion: {
		requiredGoals: ["goal.play_instrument", "goal.edit_by_gamepad"]
		projectChecks: ["validation.test", "validation.audio_integration"]
	}

	layers: ["domain", "application", "infrastructure"]
	layerRules: {
		application: {dependsOn: ["domain"]}
		infrastructure: {dependsOn: ["domain", "application"]}
	}
	meta: {language: "rust", avoid: ["allocation or locks on the audio thread"]}

	contexts: {
		Engine: {
			purpose: "stateful voice rendering"
			valueObjects: AudioFrame: {
				state: {left: "f32", right: "f32"}
				description: "one stereo sample"
				contributesTo: [{capability: "capability.render_note", contribution: "defines the renderer output contract"}]
			}
			aggregates: Voice: {
				root: true
				purpose: "own one sounding note and its envelope lifecycle"
				state: {note: "u8", velocity: "f32", stage: "string"}
				commands: {Trigger: {note: "u8", velocity: "f32"}, Release: {}}
				events: {Triggered: {}, Released: {}, BecameIdle: {}}
				invariants: ["a voice is reclaimable only after it becomes idle"]
				contributesTo: [{capability: "capability.render_note", contribution: "implements the stateful note lifecycle"}]
			}
			domainServices: VoiceMixer: {
				purpose: "sum active voices into bounded stereo output"
				uses: ["aggregate.Engine.Voice"]
				contributesTo: [{capability: "capability.render_note", contribution: "renders all active voices"}]
			}
		}

		Shell: {
			purpose: "external MIDI performance, editor controls, and outbound presentation/audio boundaries"
			ports: {
				MidiInput: {
					direction: "inbound"
					contract: {connect: "(onEvent: MidiCallback) -> Connection"}
					contributesTo: [{capability: "capability.render_note", contribution: "owns the external performance-input contract"}]
				}
				AudioOutput: {
					direction: "outbound"
					contract: {open: "(callback: RenderCallback) -> Stream"}
					contributesTo: [{capability: "capability.deliver_audio", contribution: "owns the audio device contract"}]
				}
				GamepadInput: {
					direction: "inbound"
					contract: {poll: "() -> list<GamepadAction>"}
					contributesTo: [{capability: "capability.navigate_gamepad", contribution: "owns normalized gamepad actions"}]
				}
				PatchView: {
					direction: "outbound"
					contract: {render: "(PatchViewState) -> ()"}
					contributesTo: [{capability: "capability.navigate_gamepad", contribution: "owns patch editor presentation"}]
				}
			}
			applicationServices: PatchController: {
				purpose: "coordinate normalized input with patch editing use cases"
				uses: ["port.Shell.GamepadInput", "port.Shell.PatchView"]
				operations: {changeWaveform: {input: {waveform: "string"}}}
				contributesTo: [{capability: "capability.navigate_gamepad", contribution: "coordinates the complete gamepad editing workflow"}]
			}
		}
	}

	adapters: {
		MidirMidi: {
			implements: "port.Shell.MidiInput"
			layer: "infrastructure"
			profile: {kind: "device_input", device: "external MIDI controller"}
			contributesTo: [{capability: "capability.render_note", contribution: "normalizes external MIDI events entering the voice engine"}]
		}
		CpalAudio: {
			implements: "port.Shell.AudioOutput"
			layer: "infrastructure"
			profile: {kind: "device_output", device: "system audio"}
			contributesTo: [{capability: "capability.deliver_audio", contribution: "delivers frames to the physical device"}]
		}
		GilrsGamepad: {
			implements: "port.Shell.GamepadInput"
			layer: "infrastructure"
			profile: {kind: "device_input", device: "gamepad"}
			contributesTo: [{capability: "capability.navigate_gamepad", contribution: "normalizes physical controller events"}]
		}
		EguiPatchView: {
			implements: "port.Shell.PatchView"
			layer: "infrastructure"
			profile: {kind: "ui", surfaces: ["patch_editor"], accessibility: ["every action has a gamepad mapping"]}
			contributesTo: [{capability: "capability.navigate_gamepad", contribution: "renders the gamepad-navigable patch editor"}]
		}
	}

	assetKinds: {
		"rust-config": {description: "typed runtime configuration", filePattern: "src/config/*.rs"}
		"rust-bin": {description: "executable verification harness", filePattern: "src/bin/*.rs"}
		"markdown-guide": {description: "user documentation", filePattern: "docs/*.md"}
	}
	assets: {
		AudioConfig: {
			kind: "rust-config"
			description: "validated device and buffer configuration"
			profile: {kind: "configuration", source: "environment and CLI", failurePolicy: "reject invalid settings before opening audio"}
			targets: ["adapter.CpalAudio"]
			contributesTo: [{capability: "capability.deliver_audio", contribution: "prevents invalid settings from reaching the audio callback"}]
		}
		ToneWitness: {
			kind: "rust-bin"
			description: "inject a normalized external-MIDI A4 event, render it, and emit a measured observation"
			profile: {kind: "verification_harness", witness: "evidence.audible_witness"}
			targets: ["adapter.MidirMidi", "domainService.Engine.VoiceMixer", "adapter.CpalAudio"]
			contributesTo: [{capability: "capability.render_note", contribution: "provides executable end-to-end acceptance evidence"}]
		}
		SteamDeckGuide: {
			kind: "markdown-guide"
			description: "gamepad map and keyboard-free editing walkthrough"
			profile: {kind: "documentation", audience: "Steam Deck performers", requiredExamples: ["open editor", "change waveform"]}
			targets: ["adapter.GilrsGamepad", "adapter.EguiPatchView"]
			contributesTo: [{capability: "capability.navigate_gamepad", contribution: "documents the same journey required by acceptance"}]
		}
	}

	validations: {
		test: {
			scope: "project", kind: "test"
			command: ["cargo", "test"]
			description: "project test suite"
			goals: ["goal.play_instrument", "goal.edit_by_gamepad"]
		}
		audio_integration: {
			scope: "integration_wave", kind: "integration"
			command: ["cargo", "test", "--test", "live_audio_pipeline"]
			description: "external MIDI reaches the audio adapter through the accepted integration path"
			resources: ["adapter.MidirMidi", "domainService.Engine.VoiceMixer", "adapter.CpalAudio"]
			capabilities: ["capability.render_note", "capability.deliver_audio"]
			goals: ["goal.play_instrument"]
		}
	}

	witnesses: {
		audible_a4: {
			scope: "goal"
			goal: "goal.play_instrument"
			capability: "capability.render_note"
			resources: ["adapter.MidirMidi", "domainService.Engine.VoiceMixer", "adapter.CpalAudio", "asset.ToneWitness"]
			evidence: ["evidence.audible_witness"]
			command: ["cargo", "run", "--bin", "tone_witness", "--", "--json"]
			negativeCommand: ["cargo", "run", "--bin", "tone_witness", "--", "--json", "--silent-stub"]
			timeout: "30s"
			observation: {
				kind: "json_stdout", marker: "CREST_OBSERVATION "
				schema: {peak: "number", clipped: "bool"}
			}
			predicates: [
				{field: "peak", op: "gt", value: 0.1},
				{field: "clipped", op: "eq", value: false},
			]
		}
		gamepad_edit: {
			scope: "goal"
			goal: "goal.edit_by_gamepad"
			capability: "capability.navigate_gamepad"
			resources: ["port.Shell.GamepadInput", "applicationService.Shell.PatchController", "adapter.GilrsGamepad", "adapter.EguiPatchView"]
			evidence: ["evidence.gamepad_journey"]
			command: ["cargo", "run", "--bin", "gamepad_demo", "--", "--json"]
			negativeCommand: ["cargo", "run", "--bin", "gamepad_demo", "--", "--json", "--drop-actions"]
			timeout: "30s"
			observation: {
				kind: "json_stdout", marker: "CREST_OBSERVATION "
				schema: {actions: "number", performanceNotes: "number"}
			}
			predicates: [
				{field: "actions", op: "gt", value: 0},
				{field: "performanceNotes", op: "eq", value: 0},
			]
		}
	}
}
