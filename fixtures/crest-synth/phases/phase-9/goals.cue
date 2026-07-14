package crestsynth

// Phase 9 supplies the external-system adapters and controller-first navigation
// that make the domain engine usable as a standalone Steam Deck instrument.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, built around a hard real-time audio core."
	actors: performer: {description: "a musician operating crest-synth from a Steam Deck or desktop controller"}
	goals: operate_standalone_instrument: {
		description: "A performer can connect MIDI and audio devices and navigate the standalone application by gamepad"
		priority: "required"
		actors: ["actor.performer"]
		capabilities: ["capability.connect_devices_and_navigate"]
		requirements: ["requirement.controller_first_navigation"]
	}
	capabilities: connect_devices_and_navigate: {
		description: "Adapt cpal, midir, eframe/egui, and gilrs boundaries while keeping navigation logic device-independent"
		goals: ["goal.operate_standalone_instrument"]
		acceptance: headless_gamepad_journey: {
			description: "Scripted controller events drive semantic actions, cursor movement, and controller-specific glyphs without opening devices"
			actor: "actor.performer"
			steps: [{action: "feed raw controller events", observes: "normalized GamepadActions update the cursor model"}, {action: "change controller type", observes: "the correct button glyphs resolve"}]
			evidence: ["evidence.gamepad_check"]
		}
	}
	requirements: controller_first_navigation: {kind: "functional", description: "Navigation uses the application cursor model and every required operation has a gamepad action", goals: ["goal.operate_standalone_instrument"], capabilities: ["capability.connect_devices_and_navigate"]}
	evidence: gamepad_check: {kind: "behavioral_witness", description: "make check-gamepad exercises navigation and glyph resolution without a window or controller"}
	nonGoals: {device_bound_tests: "Mechanical validation does not require physical MIDI, audio, display, or gamepad devices"}
	completion: {requiredGoals: ["goal.operate_standalone_instrument"], projectChecks: ["validation.build", "validation.check_gamepad"]}
}
