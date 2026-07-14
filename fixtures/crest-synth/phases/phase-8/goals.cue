package crestsynth

// Phase 8 preserves creative work as versioned patches, banks, and full setups.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, built around a hard real-time audio core."
	actors: sound_designer: {description: "a musician saving, browsing, and restoring sounds and complete setups"}
	goals: preserve_and_recall_sound: {
		description: "A sound designer can save a complete setup and later restore the same state and sound"
		priority: "required"
		actors: ["actor.sound_designer"]
		capabilities: ["capability.roundtrip_presets_and_setups"]
		requirements: ["requirement.atomic_versioned_restore"]
	}
	capabilities: roundtrip_presets_and_setups: {
		description: "Serialize versioned patch, bank, mixer, modulation, effects, and setup state and restore it atomically"
		goals: ["goal.preserve_and_recall_sound"]
		acceptance: bit_exact_setup_roundtrip: {
			description: "A multi-patch setup reloads to equivalent state and re-renders bit-identical audio"
			actor: "actor.sound_designer"
			steps: [{action: "save the configured setup", observes: "versioned serialized state"}, {action: "reload and render it", observes: "equal state and bit-identical output"}]
			evidence: ["evidence.preset_demo"]
		}
	}
	requirements: atomic_versioned_restore: {kind: "functional", description: "Serialized formats carry a version and failed setup restoration leaves all prior state unchanged", goals: ["goal.preserve_and_recall_sound"], capabilities: ["capability.roundtrip_presets_and_setups"]}
	evidence: preset_demo: {kind: "behavioral_witness", description: "make demo-presets proves state equality and bit-identical audio across a setup round-trip"}
	completion: {requiredGoals: ["goal.preserve_and_recall_sound"], projectChecks: ["validation.build", "validation.demo_presets"]}
}
