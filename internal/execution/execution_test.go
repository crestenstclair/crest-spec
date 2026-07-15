package execution

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClosedRoleRegistryAndHandoffs(t *testing.T) {
	roles := Roles()
	require.Len(t, roles, 11)
	policy, err := LookupRole(string(RoleResourceImplementer))
	require.NoError(t, err)
	require.True(t, policy.Allows(ActionSubmitCandidate))
	require.False(t, policy.Allows(ActionCompleteAdvisory))
	require.True(t, policy.AllowsHandoff(RoleMinimalDiffRepair))
	_, err = LookupRole("universal_agent")
	require.ErrorContains(t, err, "unsupported agent role")

	reviewer, err := LookupRole(string(RoleArchitectureReviewer))
	require.NoError(t, err)
	require.False(t, reviewer.Allows(ActionSubmitCandidate), "review roles never accept or submit implementation output")
}

func TestExecutionStateMachineFailsClosed(t *testing.T) {
	require.NoError(t, ValidateTransition(StatusExecuting, StatusCandidateSubmitted))
	require.NoError(t, ValidateTransition(StatusCandidateSubmitted, StatusValidating))
	require.NoError(t, ValidateTransition(StatusValidating, StatusAccepted))
	require.ErrorContains(t, ValidateTransition(StatusExecuting, StatusAccepted), "invalid execution transition")
	require.ErrorContains(t, ValidateTransition(StatusRejected, StatusExecuting), "invalid execution transition")
	require.True(t, StatusCancelled.Terminal())
}

func TestCanonicalRedactionIsRecursiveCaseInsensitiveAndStable(t *testing.T) {
	input := map[string]any{
		"temperature": 0.2,
		"API_Key":     "very-secret",
		"token_config": map[string]any{
			"TOKEN": "also-secret", "max_tokens": 4096,
		},
		"nested": map[string]any{
			"aUtHoRiZaTiOn": "Bearer token-value",
			"headers":       []any{map[string]any{"client-secret": "hidden"}, "Bearer another-token"},
		},
	}
	encoded, hash, err := CanonicalRedacted(input)
	require.NoError(t, err)
	require.NotContains(t, encoded, "very-secret")
	require.NotContains(t, encoded, "token-value")
	require.NotContains(t, encoded, "another-token")
	require.NotContains(t, encoded, "also-secret")
	require.Contains(t, encoded, `"max_tokens":4096`)
	require.Contains(t, encoded, "[REDACTED]")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(encoded), &decoded))

	encodedAgain, hashAgain, err := CanonicalRedacted(map[string]any{
		"nested": input["nested"], "API_Key": "different-secret", "temperature": 0.2,
		"token_config": map[string]any{"TOKEN": "different-token", "max_tokens": 4096},
	})
	require.NoError(t, err)
	require.Equal(t, encoded, encodedAgain)
	require.Equal(t, hash, hashAgain)
}

func TestFailureRoutingBlocksNonImplementationProblems(t *testing.T) {
	require.Equal(t, RoleMinimalDiffRepair, RouteFailure(FailureImplementationError).NextRole)
	require.Equal(t, RoleIntegrationImplementer, RouteFailure(FailureIntegrationError).NextRole)
	require.True(t, RouteFailure(FailureInvalidValidation).BlocksRetry)
	require.True(t, RouteFailure(FailureMissingProjectIntent).BlocksRetry)
	require.NoError(t, ValidateFailureCategory(FailureModelFailure))
	require.Error(t, ValidateFailureCategory("prompt_was_bad"))
}
