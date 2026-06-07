project: {
	name: "test-project"
	layers: ["domain", "application", "infrastructure"]
	meta: {
		language: "go"
		style:    "DDD"
	}
	contexts: Synth: {
		purpose: "Audio synthesis engine"
		ubiquitousLanguage: oscillator: "Waveform generator"
		meta: {
			notes: "Core audio context"
		}
		aggregates: Voice: {
			root:    true
			purpose: "Manages a single voice"
			state: {
				frequency: "float64"
				amplitude: "float64"
			}
			commands: NoteOn: {
				frequency: "float64"
				velocity:  "float64"
			}
			events: VoiceStarted: {
				frequency: "float64"
			}
			invariants: ["frequency > 0"]
			entities: Oscillator: {
				state: {
					waveform: "string"
					phase:    "float64"
				}
			}
			valueObjects: NoteId: {
				state: value: "int"
				description: "MIDI note number"
			}
		}
		valueObjects: Frequency: {
			state: hz: "float64"
			invariants: ["hz > 0", "hz < 20000"]
		}
		repositories: VoicePool: {
			of: "aggregate.Synth.Voice"
			contract: acquire: "() -> Voice"
		}
		ports: AudioOutput: {
			contract: write: "([]float64) -> error"
		}
		domainServices: Mixer: {
			purpose: "Combines voice outputs"
			uses: ["aggregate.Synth.Voice"]
		}
		assets: VoiceImpl: {
			kind: "source_file"
			description: "Voice implementation"
			targets: ["aggregate.Synth.Voice"]
		}
	}
	adapters: CoreAudioAdapter: {
		implements: "port.Synth.AudioOutput"
		layer:      "infrastructure"
	}
	assetKinds: source_file: {
		description: "Go source file"
		filePattern: "{{snakeCase .Name}}.go"
	}
	assets: README: {
		kind:        "source_file"
		description: "Project readme"
	}
	invariants: [{text: "All aggregates must have a root entity"}]
	contextMap: [{from: "Synth", to: "Synth", kind: "identity"}]
}
