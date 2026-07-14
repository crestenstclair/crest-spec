package crestsynth

// Phase 10 is an alternate delivery boundary over the same engine, not a new
// synthesis domain: a DAW host owns transport, MIDI, automation, and state I/O.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, with an optional CLAP/VST3 host boundary over the same engine."
	actors: producer: {description: "a musician using crest-synth as an instrument inside a DAW"}
	goals: use_in_plugin_host: {
		description: "A producer can load the crest-synth engine as a CLAP or VST3 instrument without forking its audio model"
		priority: "required"
		actors: ["actor.producer"]
		capabilities: ["capability.process_through_plugin_host"]
		requirements: ["requirement.stable_host_contract"]
	}
	capabilities: process_through_plugin_host: {
		description: "Map host MIDI, audio buffers, automation, parameters, and saved state onto the existing engine"
		goals: ["goal.use_in_plugin_host"]
		acceptance: host_lifecycle: {
			description: "A host can initialize an instance, process MIDI/audio, automate stable parameters, and restore state"
			actor: "actor.producer"
			steps: [{action: "initialize and process a block", observes: "host MIDI produces bounded audio through the shared engine"}, {action: "save and restore plugin state", observes: "stable parameters and state round-trip"}]
			evidence: ["evidence.plugin_contract"]
		}
	}
	requirements: stable_host_contract: {kind: "functional", description: "Parameter IDs remain stable and the wrapper performs no blocking or allocation in process", goals: ["goal.use_in_plugin_host"], capabilities: ["capability.process_through_plugin_host"]}
	evidence: plugin_contract: {kind: "integration_validation", description: "The nih-plug wrapper builds and its host-facing lifecycle, parameter, and state tests pass"}
	nonGoals: {separate_plugin_engine: "The plugin wrapper reuses the standalone engine and does not define a second audio architecture"}
	completion: {requiredGoals: ["goal.use_in_plugin_host"], projectChecks: ["validation.build", "validation.test"]}
}
