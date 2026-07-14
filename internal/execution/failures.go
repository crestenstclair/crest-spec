package execution

import "fmt"

type FailureCategory string

const (
	FailureMissingProjectIntent       FailureCategory = "missing_project_intent"
	FailureMissingContext             FailureCategory = "missing_context"
	FailureIncorrectContextSelection  FailureCategory = "incorrect_context_selection"
	FailureStaleContext               FailureCategory = "stale_context"
	FailureContextTruncation          FailureCategory = "context_truncation"
	FailureAmbiguousResourceContract  FailureCategory = "ambiguous_resource_contract"
	FailureArchitecturalInconsistency FailureCategory = "architectural_inconsistency"
	FailureImplementationError        FailureCategory = "implementation_error"
	FailureIntegrationError           FailureCategory = "integration_error"
	FailureInvalidValidation          FailureCategory = "invalid_validation"
	FailureBehavioralTheater          FailureCategory = "behavioral_theater"
	FailureToolFailure                FailureCategory = "tool_failure"
	FailureHostFailure                FailureCategory = "host_failure"
	FailureModelFailure               FailureCategory = "model_failure"
	FailureUnsupportedProjectPattern  FailureCategory = "unsupported_project_pattern"
)

var failureCategories = map[FailureCategory]bool{
	FailureMissingProjectIntent: true, FailureMissingContext: true,
	FailureIncorrectContextSelection: true, FailureStaleContext: true,
	FailureContextTruncation: true, FailureAmbiguousResourceContract: true,
	FailureArchitecturalInconsistency: true, FailureImplementationError: true,
	FailureIntegrationError: true, FailureInvalidValidation: true,
	FailureBehavioralTheater: true, FailureToolFailure: true, FailureHostFailure: true,
	FailureModelFailure: true, FailureUnsupportedProjectPattern: true,
}

type FailureRoute struct {
	CorrectiveAction string
	NextRole         Role
	BlocksRetry      bool
}

func ValidateFailureCategory(category FailureCategory) error {
	if !failureCategories[category] {
		return fmt.Errorf("unsupported failure category %q", category)
	}
	return nil
}

func RouteFailure(category FailureCategory) FailureRoute {
	switch category {
	case FailureMissingProjectIntent, FailureAmbiguousResourceContract:
		return FailureRoute{CorrectiveAction: "repair_specification", BlocksRetry: true}
	case FailureMissingContext, FailureIncorrectContextSelection, FailureStaleContext, FailureContextTruncation:
		return FailureRoute{CorrectiveAction: "rebuild_context", NextRole: RoleResourceImplementer}
	case FailureArchitecturalInconsistency:
		return FailureRoute{CorrectiveAction: "architecture_review_and_replan", NextRole: RoleArchitectureReviewer, BlocksRetry: true}
	case FailureImplementationError:
		return FailureRoute{CorrectiveAction: "minimal_diff_repair", NextRole: RoleMinimalDiffRepair}
	case FailureIntegrationError:
		return FailureRoute{CorrectiveAction: "integration_repair", NextRole: RoleIntegrationImplementer}
	case FailureInvalidValidation:
		return FailureRoute{CorrectiveAction: "repair_validation", BlocksRetry: true}
	case FailureBehavioralTheater:
		return FailureRoute{CorrectiveAction: "reauthor_behavioral_witness", NextRole: RoleBehavioralWitnessAuthor, BlocksRetry: true}
	case FailureToolFailure, FailureHostFailure:
		return FailureRoute{CorrectiveAction: "confirm_infrastructure_health_then_retry"}
	case FailureModelFailure:
		return FailureRoute{CorrectiveAction: "retry_or_change_host_model"}
	case FailureUnsupportedProjectPattern:
		return FailureRoute{CorrectiveAction: "human_schema_extension", BlocksRetry: true}
	default:
		return FailureRoute{CorrectiveAction: "human_triage", BlocksRetry: true}
	}
}
