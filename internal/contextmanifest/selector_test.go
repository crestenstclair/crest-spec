package contextmanifest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectPreservesPriorityAndIsDeterministic(t *testing.T) {
	candidates := []Candidate{
		{Kind: "background", Title: "Tree", SourceKind: "repository", SourceID: "tree", Content: strings.Repeat("b", 5000), Priority: PriorityBackground, Truncatable: true, InclusionReason: "repository orientation"},
		{Kind: "contract", Title: "Resource", SourceKind: "spec", SourceID: "resource.A", Content: strings.Repeat("c", 800), Priority: PriorityContract, Mandatory: true, InclusionReason: "target contract"},
		{Kind: "goal", Title: "Goal", SourceKind: "spec", SourceID: "goal.A", Content: strings.Repeat("g", 800), Priority: PriorityGoal, Mandatory: true, InclusionReason: "target goal"},
	}

	first := Select(candidates, MinimumBudget)
	second := Select([]Candidate{candidates[2], candidates[0], candidates[1]}, MinimumBudget)
	require.False(t, first.Blocked)
	require.Equal(t, first.ContextHash, second.ContextHash)
	require.Equal(t, "goal", first.Sections[0].Kind)
	require.Equal(t, Included, first.Sections[0].Decision)
	require.Equal(t, Included, first.Sections[1].Decision)
	require.Equal(t, Truncated, first.Sections[2].Decision)
	require.Contains(t, first.Sections[2].Content, "[truncated by crest-spec:")
}

func TestSelectBlocksWhenMandatoryMaterialExceedsBudget(t *testing.T) {
	result := Select([]Candidate{
		{Kind: "goal", Title: "Goal", SourceKind: "spec", SourceID: "goal.A", Content: strings.Repeat("g", 5000), Priority: PriorityGoal, Mandatory: true},
	}, MinimumBudget)
	require.True(t, result.Blocked)
	require.Contains(t, result.BlockedReason, "mandatory context requires")
	require.Equal(t, Omitted, result.Sections[0].Decision)
}

func TestSelectReservesBudgetForLaterMandatorySections(t *testing.T) {
	result := Select([]Candidate{
		{Kind: "goal", Title: "Goal", SourceKind: "spec", SourceID: "goal.A", Content: strings.Repeat("g", 800), Priority: PriorityGoal, Mandatory: true, InclusionReason: "target goal"},
		// Canonical kind ordering puts design_contract before resource_contract
		// at the same priority. It must not consume the contract's reservation.
		{Kind: "design_contract", Title: "Design", SourceKind: "spec", SourceID: "design.A", Content: strings.Repeat("d", 3000), Priority: PriorityContract, Truncatable: true, InclusionReason: "design detail"},
		{Kind: "resource_contract", Title: "Resource", SourceKind: "spec", SourceID: "resource.A", Content: strings.Repeat("r", 800), Priority: PriorityContract, Mandatory: true, InclusionReason: "target contract"},
	}, MinimumBudget)
	require.False(t, result.Blocked)
	require.Equal(t, Truncated, result.Sections[1].Decision)
	require.Equal(t, Included, result.Sections[2].Decision)
}

func TestSelectRecordsUnavailableAndDuplicateSources(t *testing.T) {
	result := Select([]Candidate{
		{Kind: "code", Title: "Call sites", SourceKind: "discovery", SourceID: "calls", Priority: PriorityCode, UnavailableReason: "selector unavailable"},
		{Kind: "code", Title: "A", SourceKind: "file", SourceID: "a.go", Content: "one", Priority: PriorityCode},
		{Kind: "code", Title: "A duplicate", SourceKind: "file", SourceID: "a.go", Content: "two", Priority: PriorityCode},
		{Kind: "example", Title: "Same content elsewhere", SourceKind: "file", SourceID: "example.go", Content: "one", Priority: PriorityConvention},
	}, MinimumBudget)
	require.Equal(t, Omitted, result.Sections[0].Decision)
	require.Equal(t, "selector unavailable", result.Sections[0].Reason)
	require.Equal(t, Included, result.Sections[1].Decision)
	require.Equal(t, Omitted, result.Sections[2].Decision)
	require.Equal(t, "duplicate source identity", result.Sections[2].Reason)
	require.Equal(t, Omitted, result.Sections[3].Decision)
	require.Contains(t, result.Sections[3].Reason, "duplicate content hash")
}

func TestNormalizeBudgetUsesStableBounds(t *testing.T) {
	require.Equal(t, DefaultBudget, NormalizeBudget(0))
	require.Equal(t, MinimumBudget, NormalizeBudget(1))
	require.Equal(t, MaximumBudget, NormalizeBudget(MaximumBudget+1))
}

func TestBudgetForRoleUsesStableHostIndependentDefaults(t *testing.T) {
	require.Equal(t, DefaultBudget, BudgetForRole("resource_implementer", 0))
	require.Equal(t, 49152, BudgetForRole("integration_implementer", 0))
	require.Equal(t, 24576, BudgetForRole("minimal_diff_repair", 0))
	require.Equal(t, MinimumBudget, BudgetForRole("integration_implementer", 1))
}
