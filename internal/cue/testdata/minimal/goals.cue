project: {
	mission: "Generate and verify a complete test project"
	actors: developer: {description: "the developer using the generated project"}
	goals: generated_project: {
		description: "The declared project behavior is generated and verified"
		priority: "required"
		actors: ["actor.developer"]
		capabilities: ["capability.run_project"]
		requirements: ["requirement.valid_output"]
	}
	capabilities: run_project: {
		description: "Run the generated project behavior"
		goals: ["goal.generated_project"]
		acceptance: successful_run: {
			description: "The generated project runs successfully"
			actor: "actor.developer"
			evidence: ["evidence.project_test"]
		}
	}
	requirements: valid_output: {
		kind: "functional"
		description: "The generated project produces valid output"
		goals: ["goal.generated_project"]
		capabilities: ["capability.run_project"]
	}
	evidence: project_test: {kind: "validation", description: "The generated project test passes"}
	completion: requiredGoals: ["goal.generated_project"]
}
