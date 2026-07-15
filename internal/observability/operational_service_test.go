package observability

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	executionpkg "github.com/crestenstclair/crest-spec/internal/execution"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func seedOperationalAttempt(t *testing.T, st *store.Store, sessionID, applyID, resourceID, attemptID string, withExecution bool) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, st.UpsertSessionResource(store.SessionResource{SessionID: sessionID, ResourceID: resourceID, State: "pending", MaxRetries: 3}))
	manifest, err := st.CreateContextManifest(ctx, store.ContextManifestWrite{Dispatch: true, Manifest: store.ContextManifest{
		ID: "context-" + attemptID,
		Attempt: store.GenerationAttempt{
			ID: attemptID, SessionID: sessionID, ApplyID: applyID, ResourceID: resourceID,
			PlanOperationID: "modify:" + resourceID, Role: "resource_implementer", RolePolicyVersion: executionpkg.RolePolicyVersion,
		},
		SelectorVersion: contextmanifest.SelectorVersion, EstimatorVersion: contextmanifest.EstimatorVersion,
		SelectionStrategy: "goal-directed-priority-v1", BudgetTokens: 4096, EstimatedTokens: 4,
		OriginalBytes: 12, SelectedBytes: 12, TemplateHashes: map[string]string{"task": "template-v1"},
		SystemPrompt: "governed system", RenderedPrompt: "implement task", ContextHash: contextmanifest.Hash("context-" + attemptID),
		Sections: []store.ContextManifestSection{{
			ID: "section-" + attemptID, Kind: "task", Title: "Task", SourceKind: "plan_operation", SourceID: "modify:" + resourceID,
			Priority: int(contextmanifest.PriorityTask), Mandatory: true, Decision: string(contextmanifest.Included),
			Reason: "the operation is mandatory", OriginalHash: contextmanifest.Hash("implement task"),
			OriginalBytes: 12, SelectedBytes: 12, EstimatedTokens: 4, Content: "implement task",
		}},
	}})
	require.NoError(t, err)
	if !withExecution {
		return
	}
	execution, err := st.StartExecution(ctx, store.ExecutionManifest{
		AttemptID: attemptID, ContextManifestID: manifest.ID, ProtocolVersion: executionpkg.ProtocolVersion,
		IdempotencyKey: "execution-" + attemptID, PlanOperationID: "modify:" + resourceID,
		Role: "resource_implementer", RolePolicyVersion: executionpkg.RolePolicyVersion,
		ContextPolicy: "resource-implementation-v1", HostName: "fixture-host", HostVersion: "1.0",
		Provider: "fixture", Model: "fixture-model", ContextHash: manifest.ContextHash,
		TemplateHashes: map[string]string{"task": "template-v1"}, SystemInstructions: "governed system",
		Tools: []store.ExecutionTool{{Name: "filesystem", Permission: "project"}},
	})
	require.NoError(t, err)
	_, err = st.SubmitCandidate(ctx, execution.ID, []store.CandidateFile{{Path: "src/generated.go", Content: "package generated\n", WriteIntent: "modify"}})
	require.NoError(t, err)
	_, err = st.CreateFailureClassification(ctx, store.FailureClassificationWrite{
		ID: "failure-" + attemptID, AttemptID: attemptID, ExecutionID: execution.ID,
		Category: executionpkg.FailureImplementationError, Origin: "engine", Confidence: 1,
		EvidenceSource: "validation", EvidenceReference: "validation-fixture", Evidence: "compile failed",
		GoalIDs: []string{"goal.play"},
	})
	require.NoError(t, err)
}

func TestPlanViewExplainsVerticalSlicesAndActualProgress(t *testing.T) {
	service, st := seededService(t)
	actions := []planpkg.PlannedAction{{
		ResourceID: "aggregate.Voice", Kind: planpkg.ActionModify, Reason: "declaration changed",
		OperationID: "modify:aggregate.Voice", Category: "corrective", RecommendedRole: "resource_implementer",
		Capabilities: []string{"capability.voice"}, Goals: []string{"goal.play"},
		ExpectedBehavior: []string{"A4 can be heard"}, ExpectedEvidence: []string{"evidence.audible"},
	}}
	encoded, err := json.Marshal(actions)
	require.NoError(t, err)
	require.NoError(t, st.CreateApply("apply-plan", "spec-v1"))
	require.NoError(t, st.CreateSession(store.Session{ID: "session-plan", ApplyID: "apply-plan", PlanJSON: string(encoded), WavesJSON: `[["aggregate.Voice"]]`, HashesJSON: `{}`}))
	require.NoError(t, st.UpsertSessionResource(store.SessionResource{SessionID: "session-plan", ResourceID: "aggregate.Voice", State: "committed", Attempts: 1, MaxRetries: 3}))

	view, err := service.Plan(context.Background(), "synth", "spec-v1", actions, [][]string{{"aggregate.Voice"}}, []planpkg.CapabilitySlice{{
		Capability: "capability.voice", Goals: []string{"goal.play"}, Required: true,
		CurrentGap: "resource implementation is stale", ExpectedBehavior: []string{"A4 can be heard"},
		ExpectedEvidence: []string{"evidence.audible"}, OperationIDs: []string{"modify:aggregate.Voice"},
	}})
	require.NoError(t, err)
	assert.Equal(t, "session-plan", view.ActiveSessionID)
	require.Len(t, view.Operations, 1)
	assert.Equal(t, "committed", view.Operations[0].ExecutionStatus)
	assert.Equal(t, "implemented", view.Slices[0].Status)
	assert.Equal(t, "complete", view.Waves[0].State)
	assert.False(t, view.State.Stale)
	operation, err := PlanOperationByID(view, "modify:aggregate.Voice")
	require.NoError(t, err)
	assert.Equal(t, "A4 can be heard", operation.ExpectedBehavior[0])
}

func TestAttemptComparisonAndFailureViewsAreLinkedAndBounded(t *testing.T) {
	service, st := seededService(t)
	require.NoError(t, st.CreateApply("apply-attempt", "spec-v1"))
	require.NoError(t, st.CreateSession(store.Session{ID: "session-attempt", ApplyID: "apply-attempt", PlanJSON: `[]`, WavesJSON: `[]`, HashesJSON: `{}`}))
	seedOperationalAttempt(t, st, "session-attempt", "apply-attempt", "aggregate.Voice", "attempt-left", true)
	seedOperationalAttempt(t, st, "session-attempt", "apply-attempt", "adapter.Audio", "attempt-right", false)

	detail, err := service.Attempt(context.Background(), "attempt-left")
	require.NoError(t, err)
	require.NotNil(t, detail.Context)
	require.NotNil(t, detail.Execution)
	require.NotNil(t, detail.Candidate)
	require.Len(t, detail.Failures, 1)
	assert.True(t, detail.Execution.State.Redacted)
	assert.False(t, detail.Execution.State.Legacy)
	assert.True(t, detail.Failures[0].State.Redacted)
	assert.NotEmpty(t, detail.Candidate.Files[0].ContentHash)
	assert.Contains(t, detail.Links[1].HREF, "aggregate.Voice")

	comparison, err := service.CompareAttempts(context.Background(), "attempt-left", "attempt-right")
	require.NoError(t, err)
	require.Len(t, comparison.Changes, 8)
	assert.Contains(t, comparison.Summary, "context changed")
	assert.Contains(t, comparison.Summary, "execution changed")
	assert.Contains(t, comparison.Summary, "candidate changed")

	page, err := service.Failures(context.Background(), PageRequest{Limit: 1, Kind: "implementation_error", Status: "unresolved"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "/api/v1/attempts/attempt-left", page.Items[0].Links[1].HREF)
	failure, err := service.Failure(context.Background(), "failure-attempt-left")
	require.NoError(t, err)
	assert.Equal(t, "compile failed", failure.Evidence)
	assert.Equal(t, "unresolved", failure.Status.State)
	assert.Equal(t, len("compile failed"), failure.EvidenceBytes)
	assert.False(t, failure.EvidenceTruncated)
}

func TestInlineFailureEvidenceHasAnExplicitBound(t *testing.T) {
	value := strings.Repeat("e", maximumInlineEvidenceBytes+1)
	bounded, truncated := boundedText(value, maximumInlineEvidenceBytes)
	assert.True(t, truncated)
	assert.Len(t, bounded, maximumInlineEvidenceBytes)
}
