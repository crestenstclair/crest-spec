package crestsynth

// Phase 7 adds ordered, bypassable effect chains at patch and global scope.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, built around a hard real-time audio core."
	actors: sound_designer: {description: "a musician shaping patch and master audio with effects"}
	goals: process_sound_with_effects: {
		description: "A sound designer can process patches and the global mix through ordered reverb, chorus, and delay chains"
		priority: "required"
		actors: ["actor.sound_designer"]
		capabilities: ["capability.process_ordered_effect_chains"]
		requirements: ["requirement.effect_chain_integrity"]
	}
	capabilities: process_ordered_effect_chains: {
		description: "Run enabled effect slots strictly in order and support transparent chain bypass"
		goals: ["goal.process_sound_with_effects"]
		acceptance: patch_and_master_effects: {
			description: "Per-patch and global chains alter the render in order while a bypassed chain is transparent"
			actor: "actor.sound_designer"
			steps: [{action: "render through differently ordered slots", observes: "slot order changes the result"}, {action: "bypass the chain", observes: "output equals input"}]
			evidence: ["evidence.effects_demo"]
		}
	}
	requirements: effect_chain_integrity: {kind: "functional", description: "Slots execute strictly in declared order with no internal feedback and bypass passes audio unchanged", goals: ["goal.process_sound_with_effects"], capabilities: ["capability.process_ordered_effect_chains"]}
	evidence: effects_demo: {kind: "behavioral_witness", description: "make demo-effects proves slot ordering and bypass against rendered output"}
	completion: {requiredGoals: ["goal.process_sound_with_effects"], projectChecks: ["validation.build", "validation.demo_effects"]}
}
