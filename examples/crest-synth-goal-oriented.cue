package crestsynth

// This executable sample is derived from fixtures/crest-synth. It shows the
// goal/acceptance layer composed with the existing DDD resource schemas:
// goals do not replace aggregates, services, ports, adapters, or assets.
project: {
	name:    "crest-synth-goal-oriented"
	mission: "Let a performer play and edit a real-time software synthesizer from a desktop or Steam Deck."

	actors: {
		performer: {description: "a musician performing and editing sounds"}
	}

	goals: {
		play_instrument: {
			description: "A performer can trigger a note and hear safe stereo output"
			priority: "required"
			actors: ["actor.performer"]
			capabilities: ["capability.render_note", "capability.deliver_audio"]
			requirements: ["requirement.realtime_safe"]
		}
		edit_by_gamepad: {
			description: "A performer can edit a patch without keyboard or mouse"
			priority: "required"
			actors: ["actor.performer"]
			dependsOn: ["goal.play_instrument"]
			capabilities: ["capability.navigate_gamepad"]
			requirements: ["requirement.input_parity"]
		}
	}

	capabilities: {
		render_note: {
			description: "Allocate and render a polyphonic voice"
			goals: ["goal.play_instrument"]
			acceptance: audible_a4: {
				description: "A4 produces a non-clipping signal"
				actor: "actor.performer"
				steps: [
					{action: "press A4", observes: "a voice becomes active"},
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
				description: "A gamepad opens the editor and changes the oscillator"
				actor: "actor.performer"
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
			description: "Every pointer editing action has a gamepad equivalent"
			goals: ["goal.edit_by_gamepad"]
			capabilities: ["capability.navigate_gamepad"]
		}
	}

	evidence: {
		audible_witness: {kind: "behavioral_witness", description: "measured output from the accepted voice engine"}
		audio_integration: {kind: "integration_validation", description: "frames observed at the audio adapter boundary"}
		gamepad_journey: {kind: "behavioral_witness", description: "gamepad-only patch editing journey"}
	}

	nonGoals: {
		cloud_presets: "Cloud preset synchronization is intentionally excluded"
	}
	completion: {
		requiredGoals: ["goal.play_instrument", "goal.edit_by_gamepad"]
		projectChecks: ["validation.test"]
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
			purpose: "inbound controls and outbound presentation/device boundaries"
			ports: {
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
			description: "render A4 and emit a measured observation"
			profile: {kind: "verification_harness", witness: "evidence.audible_witness"}
			targets: ["domainService.Engine.VoiceMixer", "adapter.CpalAudio"]
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

	validations: [{kind: "test", command: ["cargo", "test"], description: "project test suite"}]
}
