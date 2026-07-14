project: {
	mission: "Generate a bounded counter project and verify its behavior end to end"
	actors: developer: {description: "the developer exercising the counter"}
	goals: usable_counter: {
		description: "A developer can create and safely update bounded counters"
		priority: "required"
		actors: ["actor.developer"]
		capabilities: ["capability.manage_counter"]
		requirements: ["requirement.counter_bounds"]
	}
	capabilities: manage_counter: {
		description: "Create, increment, decrement, reset, and persist a bounded counter"
		goals: ["goal.usable_counter"]
		acceptance: counter_round_trip: {
			description: "A counter changes within its bounds and persists"
			actor: "actor.developer"
			evidence: ["evidence.counter_test"]
		}
	}
	requirements: counter_bounds: {
		kind: "functional"
		description: "Counter values never exceed their declared minimum or maximum"
		goals: ["goal.usable_counter"]
		capabilities: ["capability.manage_counter"]
	}
	evidence: counter_test: {kind: "validation", description: "The generated counter tests pass"}
	completion: requiredGoals: ["goal.usable_counter"]
}
