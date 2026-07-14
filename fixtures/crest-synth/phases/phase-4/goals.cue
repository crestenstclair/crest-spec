package crestsynth

// Required project intent. Capability-to-resource contributions are added by
// the DDD traceability phase; this phase establishes completion intent.
project: {
	mission: "Generate a complete, playable crest-synth increment while preserving real-time safety and deterministic validation."
	actors: developer: {description: "a developer building and verifying the synthesizer"}
	goals: phase_complete: {
		description:  "The declared synthesizer increment is implemented and mechanically verified"
		priority:     "required"
		actors:       ["actor.developer"]
		capabilities: ["capability.generate_increment"]
		requirements: ["requirement.validated_increment"]
	}
	capabilities: generate_increment: {
		description: "Generate the declared resources as a coherent, validated increment"
		goals:       ["goal.phase_complete"]
		acceptance: generated_and_validated: {
			description: "Declared resources are generated and project validation passes"
			actor:       "actor.developer"
			evidence:    ["evidence.project_validation"]
		}
	}
	requirements: validated_increment: {
		kind:         "functional"
		description:  "The generated increment passes its declared mechanical validations"
		goals:        ["goal.phase_complete"]
		capabilities: ["capability.generate_increment"]
	}
	evidence: project_validation: {
		kind:        "validation"
		description: "The declared project validation commands pass"
	}
	completion: requiredGoals: ["goal.phase_complete"]
}

