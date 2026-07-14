package crestsynth

// Phase 11 completes the standalone product with a parameter editor. It is not
// a performance surface: external MIDI plays notes while keyboard/gamepad edits
// flow one way through EditorEvent -> EditorState -> ParameterSnapshot -> audio.
project: {
	mission: "A standalone, gamepad-friendly external-MIDI synthesizer for Steam Deck and desktop, with a hard real-time engine and a device-independent one-way editor."
	actors: {
		performer: {description: "a musician playing notes from external MIDI hardware"}
		sound_designer: {description: "a musician editing the live instrument by keyboard or gamepad"}
	}
	goals: edit_live_instrument: {
		description: "A sound designer can navigate and edit a sounding patch by keyboard or gamepad without mouse, touch, or direct view mutation"
		priority: "required"
		actors: ["actor.performer", "actor.sound_designer"]
		capabilities: ["capability.edit_through_one_way_loop"]
		requirements: ["requirement.one_way_device_parity"]
	}
	capabilities: edit_through_one_way_loop: {
		description: "Translate keyboard and gamepad input into common EditorEvents, reduce one EditorState store, and publish bounded parameter snapshots to the audio engine"
		goals: ["goal.edit_live_instrument"]
		acceptance: headless_live_edit: {
			description: "A device-free event sequence navigates fields, uses momentary edit mode, changes bounded values, and renders non-silent audio"
			actor: "actor.sound_designer"
			steps: [{action: "navigate and enter edit mode", observes: "the single editor store changes focus and mode"}, {action: "apply fine and coarse adjustments", observes: "values clamp within bounds and a new snapshot reaches the engine"}, {action: "render an audio block", observes: "external-MIDI engine output remains non-silent"}]
			evidence: ["evidence.ui_smoke"]
		}
	}
	requirements: one_way_device_parity: {kind: "functional", description: "Views only render state; keyboard and gamepad emit the same semantic events; every edit is reachable without mouse or touch", goals: ["goal.edit_live_instrument"], capabilities: ["capability.edit_through_one_way_loop"]}
	evidence: ui_smoke: {kind: "behavioral_witness", description: "make ui-smoke validates the one-way editor journey and non-silent engine render without opening a window or device"}
	nonGoals: {onscreen_performance: "The editor does not trigger notes and has no on-screen keyboard; all performance notes come from external MIDI"}
	completion: {requiredGoals: ["goal.edit_live_instrument"], projectChecks: ["validation.build", "validation.test", "validation.ui_smoke"]}
}
