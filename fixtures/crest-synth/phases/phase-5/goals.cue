package crestsynth

// Phase 5 makes patches expressive through a modulation matrix evaluated as
// part of rendering, including LFOs, envelopes, MIDI, and per-note expression.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, built around a hard real-time audio core."
	actors: performer: {description: "a musician using evolving and per-note expressive sound"}
	goals: perform_expressive_sound: {
		description: "A performer can hear modulation and per-note expression shape a patch while it plays"
		priority: "required"
		actors: ["actor.performer"]
		capabilities: ["capability.apply_modulation_routes"]
		requirements: ["requirement.bounded_modulation"]
	}
	capabilities: apply_modulation_routes: {
		description: "Evaluate envelopes, LFOs, MIDI and MPE sources through a per-patch routing matrix into synthesis destinations"
		goals: ["goal.perform_expressive_sound"]
		acceptance: audible_vibrato_and_sweep: {
			description: "An LFO vibrato and filter sweep are audibly applied through declared routes"
			actor: "actor.performer"
			steps: [{action: "configure modulation routes", observes: "source values map to bounded destination offsets"}, {action: "render the patch", observes: "vibrato and filter motion are present in the output"}]
			evidence: ["evidence.modulation_demo"]
		}
	}
	requirements: bounded_modulation: {kind: "functional", description: "Modulation routes remain within declared bounds and per-note expression affects only its voice", goals: ["goal.perform_expressive_sound"], capabilities: ["capability.apply_modulation_routes"]}
	evidence: modulation_demo: {kind: "behavioral_witness", description: "make demo-mod renders an arrangement with active LFO and filter routes"}
	completion: {requiredGoals: ["goal.perform_expressive_sound"], projectChecks: ["validation.build", "validation.demo_mod"]}
}
