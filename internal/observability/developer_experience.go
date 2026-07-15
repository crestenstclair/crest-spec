package observability

// DeveloperQuestionRoute names the equivalent inspection surfaces for one
// operational question. Keeping this mapping executable prevents the human
// dashboard and agent-facing interfaces from drifting apart.
type DeveloperQuestionRoute struct {
	Question  string
	API       string
	MCP       string
	CLI       string
	Dashboard string
}

var DeveloperQuestionRoutes = []DeveloperQuestionRoute{
	{
		Question: "What is the project's end goal and current completion state?",
		API:      "/api/v1/project", MCP: "spec/project_overview", CLI: "crest-spec status", Dashboard: "?view=project",
	},
	{
		Question: "Which required capabilities remain incomplete, blocked, or regressed?",
		API:      "/api/v1/project and /api/v1/capabilities", MCP: "spec/project_overview and spec/capabilities", CLI: "crest-spec inspect project", Dashboard: "?view=project",
	},
	{
		Question: "Why is this resource being generated and what progress is expected?",
		API:      "/api/v1/plan/operations/{id}", MCP: "spec/plan_inspect", CLI: "crest-spec inspect plan", Dashboard: "?view=plan&operation={id}",
	},
	{
		Question: "What exact context did the agent receive, and why was each item selected, truncated, or omitted?",
		API:      "/api/v1/contexts/{id}", MCP: "spec/context_inspect", CLI: "crest-spec inspect context {id}", Dashboard: "?view=contexts&context={id}",
	},
	{
		Question: "Which host, model, role, tools, and settings produced this candidate?",
		API:      "/api/v1/attempts/{id}", MCP: "spec/attempt_inspect", CLI: "crest-spec inspect attempt {id}", Dashboard: "?view=executions&attempt={id}",
	},
	{
		Question: "Which validation rejected it and what raw evidence was captured?",
		API:      "/api/v1/verifications/{id}", MCP: "spec/verification_inspect", CLI: "crest-spec inspect validation {id}", Dashboard: "?view=verifications&verification={id}",
	},
	{
		Question: "Was the failure caused by intent, specification, context, architecture, implementation, integration, validation, tool, host, or model behavior?",
		API:      "/api/v1/failures/{id}", MCP: "spec/failures", CLI: "crest-spec inspect failure {id}", Dashboard: "?view=failures&failure={id}",
	},
	{
		Question: "What evidence proves a goal complete and for which exact source tree?",
		API:      "/api/v1/goals/{id}", MCP: "spec/goals and spec/verification_inspect", CLI: "crest-spec inspect goal {id}", Dashboard: "?view=project&goal={id}",
	},
	{
		Question: "Which completed goals were invalidated by a change?",
		API:      "/api/v1/resources/{id}/impact", MCP: "spec/impact", CLI: "crest-spec inspect impact {id}", Dashboard: "?view=plan&operation={id}",
	},
	{
		Question: "What should happen next?",
		API:      "/api/v1/project", MCP: "spec/project_overview", CLI: "crest-spec status", Dashboard: "?view=project",
	},
	{
		Question: "How do two attempts differ across context, execution, candidate, validation, cost, and outcome?",
		API:      "/api/v1/attempt-comparison?left={a}&right={b}", MCP: "spec/attempt_compare", CLI: "crest-spec compare attempts {a} {b}", Dashboard: "?view=executions&attempt_left={a}&attempt_right={b}",
	},
}
