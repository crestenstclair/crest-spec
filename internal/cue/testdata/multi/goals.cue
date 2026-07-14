project: {
	mission: "Generate and verify the composed multi-file project"
	actors: developer: {description: "the developer using the composed project"}
	goals: composition_works: {
		description: "The multi-file declaration composes into working behavior"
		priority: "required"
		actors: ["actor.developer"]
		capabilities: ["capability.compose_project"]
		requirements: ["requirement.valid_composition"]
	}
	capabilities: compose_project: {
		description: "Compose resources declared across CUE files"
		goals: ["goal.composition_works"]
		acceptance: successful_composition: {
			description: "All declarations load into one registry"
			actor: "actor.developer"
			evidence: ["evidence.loader_validation"]
		}
	}
	requirements: valid_composition: {
		kind: "functional"
		description: "All cross-file references resolve"
		goals: ["goal.composition_works"]
		capabilities: ["capability.compose_project"]
	}
	evidence: loader_validation: {kind: "validation", description: "The composed project loads successfully"}
	completion: requiredGoals: ["goal.composition_works"]
}
