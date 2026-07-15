package execution

import (
	"fmt"
	"sort"
)

const (
	ProtocolVersion   = "crest-execution-v1"
	RolePolicyVersion = "role-policy-v1"
)

type Role string
type Action string

const (
	RoleProjectGoalAnalyst        Role = "project_goal_analyst"
	RolePlanner                   Role = "planner"
	RoleResourceImplementer       Role = "resource_implementer"
	RoleIntegrationImplementer    Role = "integration_implementer"
	RoleTestGenerator             Role = "test_generator"
	RoleBehavioralWitnessAuthor   Role = "behavioral_witness_author"
	RoleFailureTriage             Role = "failure_triage"
	RoleArchitectureReviewer      Role = "architecture_reviewer"
	RoleSecurityReviewer          Role = "security_reviewer"
	RoleMinimalDiffRepair         Role = "minimal_diff_repair"
	RoleProjectCompletionReviewer Role = "project_completion_reviewer"
)

const (
	ActionStartExecution   Action = "start_execution"
	ActionSubmitCandidate  Action = "submit_candidate"
	ActionCompleteAdvisory Action = "complete_advisory"
	ActionClassifyFailure  Action = "classify_failure"
	ActionCreateHandoff    Action = "create_handoff"
)

type RolePolicy struct {
	Role                  Role
	Version               string
	ContextPolicy         string
	DefaultBudgetTokens   int
	ExpectedOutputKind    string
	PermittedActions      []Action
	PermittedHandoffRoles []Role
}

var rolePolicies = map[Role]RolePolicy{
	RoleProjectGoalAnalyst:      advisoryPolicy(RoleProjectGoalAnalyst, "goal-analysis-v1", "project_goal_analysis", []Role{RolePlanner, RoleArchitectureReviewer}),
	RolePlanner:                 advisoryPolicy(RolePlanner, "goal-plan-v1", "goal_directed_plan", []Role{RoleResourceImplementer, RoleIntegrationImplementer, RoleArchitectureReviewer}),
	RoleResourceImplementer:     implementationPolicy(RoleResourceImplementer, "resource-implementation-v1", 32768, []Role{RoleMinimalDiffRepair, RoleIntegrationImplementer, RoleTestGenerator, RoleFailureTriage, RoleArchitectureReviewer, RoleSecurityReviewer}),
	RoleIntegrationImplementer:  implementationPolicy(RoleIntegrationImplementer, "integration-implementation-v1", 49152, []Role{RoleMinimalDiffRepair, RoleTestGenerator, RoleFailureTriage, RoleArchitectureReviewer, RoleSecurityReviewer}),
	RoleTestGenerator:           implementationPolicy(RoleTestGenerator, "test-generation-v1", 24576, []Role{RoleMinimalDiffRepair, RoleFailureTriage, RoleSecurityReviewer}),
	RoleBehavioralWitnessAuthor: implementationPolicy(RoleBehavioralWitnessAuthor, "behavioral-witness-v1", 24576, []Role{RoleFailureTriage, RoleSecurityReviewer}),
	RoleFailureTriage: {
		Role: RoleFailureTriage, Version: RolePolicyVersion, ContextPolicy: "failure-triage-v1",
		DefaultBudgetTokens: 24576, ExpectedOutputKind: "failure_classification",
		PermittedActions:      []Action{ActionStartExecution, ActionCompleteAdvisory, ActionClassifyFailure, ActionCreateHandoff},
		PermittedHandoffRoles: []Role{RoleMinimalDiffRepair, RoleIntegrationImplementer, RoleTestGenerator, RoleBehavioralWitnessAuthor, RoleArchitectureReviewer, RoleSecurityReviewer},
	},
	RoleArchitectureReviewer:      advisoryPolicy(RoleArchitectureReviewer, "architecture-review-v1", "architecture_review", []Role{RolePlanner, RoleResourceImplementer, RoleIntegrationImplementer}),
	RoleSecurityReviewer:          advisoryPolicy(RoleSecurityReviewer, "security-review-v1", "security_review", []Role{RoleMinimalDiffRepair, RoleResourceImplementer, RoleIntegrationImplementer}),
	RoleMinimalDiffRepair:         implementationPolicy(RoleMinimalDiffRepair, "minimal-diff-repair-v1", 24576, []Role{RoleFailureTriage, RoleIntegrationImplementer, RoleArchitectureReviewer, RoleSecurityReviewer}),
	RoleProjectCompletionReviewer: advisoryPolicy(RoleProjectCompletionReviewer, "project-completion-review-v1", "completion_review", []Role{RolePlanner, RoleFailureTriage, RoleArchitectureReviewer}),
}

func implementationPolicy(role Role, contextPolicy string, budget int, handoffs []Role) RolePolicy {
	return RolePolicy{
		Role: role, Version: RolePolicyVersion, ContextPolicy: contextPolicy,
		DefaultBudgetTokens: budget, ExpectedOutputKind: "candidate_files",
		PermittedActions:      []Action{ActionStartExecution, ActionSubmitCandidate, ActionCreateHandoff},
		PermittedHandoffRoles: handoffs,
	}
}

func advisoryPolicy(role Role, contextPolicy, output string, handoffs []Role) RolePolicy {
	return RolePolicy{
		Role: role, Version: RolePolicyVersion, ContextPolicy: contextPolicy,
		DefaultBudgetTokens: 32768, ExpectedOutputKind: output,
		PermittedActions:      []Action{ActionStartExecution, ActionCompleteAdvisory, ActionCreateHandoff},
		PermittedHandoffRoles: handoffs,
	}
}

func LookupRole(value string) (RolePolicy, error) {
	policy, ok := rolePolicies[Role(value)]
	if !ok {
		return RolePolicy{}, fmt.Errorf("unsupported agent role %q", value)
	}
	return policy, nil
}

func Roles() []RolePolicy {
	result := make([]RolePolicy, 0, len(rolePolicies))
	for _, policy := range rolePolicies {
		result = append(result, policy)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Role < result[j].Role })
	return result
}

func (p RolePolicy) Allows(action Action) bool {
	for _, permitted := range p.PermittedActions {
		if permitted == action {
			return true
		}
	}
	return false
}

func (p RolePolicy) AllowsHandoff(target Role) bool {
	for _, permitted := range p.PermittedHandoffRoles {
		if permitted == target {
			return true
		}
	}
	return false
}

func RecommendedRole(operationCategory string) Role {
	switch operationCategory {
	case "integrative":
		return RoleIntegrationImplementer
	case "verification":
		return RoleTestGenerator
	case "corrective":
		return RoleMinimalDiffRepair
	default:
		return RoleResourceImplementer
	}
}
