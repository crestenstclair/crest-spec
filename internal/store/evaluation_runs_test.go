package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
)

func evaluationConfiguration(name, selector string) EvaluationConfiguration {
	return EvaluationConfiguration{
		Name: name, PlannerVersion: "planner-v1", PlannerPolicy: map[string]any{"vertical_slices": true},
		ContextSelectorVersion: selector, ContextBudgetTokens: 4096,
		TemplateHashes:    map[string]string{"resource": "template-v1"},
		RolePolicyVersion: execution.RolePolicyVersion, HostName: "test-host", HostVersion: "1.0",
		Provider: "test-provider", Model: "test-model", InferenceConfig: map[string]any{"temperature": 0},
		ValidationVersion: "validation-v1",
	}
}

func evaluationRunFixture(t *testing.T, st *Store, requireHeldOut bool) (*EvaluationRun, string, string) {
	t.Helper()
	ctx := context.Background()
	development := curatedEvaluationCase()
	development.RepositoryHash = "tree-development"
	development.Payload = map[string]any{"case": "development"}
	development.ExpectedOutcome = map[string]any{"accepted": true, "split": "development"}
	developmentCase, err := st.CreateCuratedEvaluationCase(ctx, development)
	require.NoError(t, err)
	heldOut := curatedEvaluationCase()
	heldOut.RepositoryHash = "tree-held-out"
	heldOut.Payload = map[string]any{"case": "held-out"}
	heldOut.ExpectedOutcome = map[string]any{"accepted": true, "split": "held_out"}
	heldOutCase, err := st.CreateCuratedEvaluationCase(ctx, heldOut)
	require.NoError(t, err)

	dataset, err := st.CreateEvaluationDataset(ctx, "comparison cases", "development and held-out cases")
	require.NoError(t, err)
	require.NoError(t, st.AddEvaluationDatasetCase(ctx, dataset.ID, developmentCase.ID, "development"))
	require.NoError(t, st.AddEvaluationDatasetCase(ctx, dataset.ID, heldOutCase.ID, "held_out"))
	_, err = st.SealEvaluationDataset(ctx, dataset.ID)
	require.NoError(t, err)

	baseline, err := st.CreateEvaluationConfiguration(ctx, evaluationConfiguration("baseline", "selector-v1"))
	require.NoError(t, err)
	candidate, err := st.CreateEvaluationConfiguration(ctx, evaluationConfiguration("candidate", "selector-v2"))
	require.NoError(t, err)
	run, err := st.CreateEvaluationRun(ctx, dataset.ID, "selector comparison", []EvaluationRunVariantInput{
		{Name: "baseline", ConfigurationID: baseline.ID, Baseline: true},
		{Name: "candidate", ConfigurationID: candidate.ID},
	}, EvaluationMetricPolicy{}, 1, 0, requireHeldOut)
	require.NoError(t, err)
	return run, baseline.ID, candidate.ID
}

func primaryEvaluationMetrics(value float64) []EvaluationMetricObservation {
	result := make([]EvaluationMetricObservation, 0, 7)
	for _, name := range []string{"accepted_implementation", "goal_completion", "integration_success", "behavioral_success"} {
		metricValue := value
		result = append(result, EvaluationMetricObservation{
			Name: name, Value: &metricValue, SourceType: "controlled_verifier", SourceID: "fixture-verifier",
		})
	}
	for _, name := range []string{"regression", "human_intervention"} {
		metricValue := 0.0
		result = append(result, EvaluationMetricObservation{
			Name: name, Value: &metricValue, SourceType: "controlled_verifier", SourceID: "fixture-verifier",
		})
	}
	cost := 0.0
	result = append(result, EvaluationMetricObservation{
		Name: "cost_usd", Value: &cost, Unit: "usd", SourceType: "execution_manifest", SourceID: "fixture-execution",
	})
	return result
}

func completeEvaluationAttempt(t *testing.T, st *Store, claim *EvaluationAssignmentClaim, ordinal int) string {
	t.Helper()
	ctx := context.Background()
	attemptID := fmt.Sprintf("evaluation-attempt-%d", ordinal)
	manifestID := fmt.Sprintf("evaluation-context-%d", ordinal)
	manifest := testContextManifest(attemptID, manifestID, claim.Case.ResourceID)
	manifest.SelectorVersion = claim.Configuration.ContextSelectorVersion
	manifest.BudgetTokens = claim.Configuration.ContextBudgetTokens
	manifest.TemplateHashes = claim.Configuration.TemplateHashes
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{Manifest: manifest, Dispatch: true})
	require.NoError(t, err)
	executionInput := testExecutionManifest()
	executionInput.AttemptID = attemptID
	executionInput.ContextManifestID = manifestID
	executionInput.IdempotencyKey = "evaluation-host-request-" + attemptID
	executionInput.HostName = claim.Configuration.HostName
	executionInput.HostVersion = claim.Configuration.HostVersion
	executionInput.Provider = claim.Configuration.Provider
	executionInput.Model = claim.Configuration.Model
	executionInput.TemplateHashes = claim.Configuration.TemplateHashes
	executionInput.ContextHash = manifest.ContextHash
	started, err := st.StartExecution(ctx, executionInput)
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID, ToStatus: execution.StatusCompleted,
		Reason: "controlled evaluation completed", DurationMS: 10, InputTokens: 20, OutputTokens: 10,
	})
	require.NoError(t, err)
	return attemptID
}

func TestEvaluationRunClaimsHideHeldOutOutcomesAndLeaseSafely(t *testing.T) {
	st := newContextManifestStore(t, "aggregate.Engine.Voice")
	ctx := context.Background()
	run, _, _ := evaluationRunFixture(t, st, true)
	require.Len(t, run.Assignments, 4)

	claim, err := st.ClaimEvaluationAssignment(ctx, run.ID, "worker-one", "held_out", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "held_out", claim.Assignment.Split)
	assert.Nil(t, claim.Case.ExpectedOutcome)
	assert.Empty(t, claim.Case.ExpectedOutcomeHash)
	assert.NotEmpty(t, claim.LeaseToken)
	_, err = st.HeartbeatEvaluationAssignment(ctx, claim.Assignment.ID, claim.LeaseID, "worker-one", "wrong-token", time.Minute)
	require.Error(t, err)
	expires, err := st.HeartbeatEvaluationAssignment(ctx, claim.Assignment.ID, claim.LeaseID, "worker-one", claim.LeaseToken, time.Minute)
	require.NoError(t, err)
	assert.True(t, expires.After(time.Now().UTC()))
	require.NoError(t, st.ReleaseEvaluationAssignment(ctx, claim.Assignment.ID, claim.LeaseID, "worker-one", claim.LeaseToken))

	reclaimed, err := st.ClaimEvaluationAssignment(ctx, run.ID, "worker-two", "held_out", time.Nanosecond)
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	afterExpiry, err := st.ClaimEvaluationAssignment(ctx, run.ID, "worker-three", "held_out", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, reclaimed.Assignment.ID, afterExpiry.Assignment.ID)
	assert.NotEqual(t, reclaimed.LeaseID, afterExpiry.LeaseID)
	_, err = st.CancelEvaluationAssignment(ctx, afterExpiry.Assignment.ID, afterExpiry.LeaseID, "worker-three", "wrong-token", "host cannot execute case")
	require.Error(t, err)
	cancelled, err := st.CancelEvaluationAssignment(ctx, afterExpiry.Assignment.ID, afterExpiry.LeaseID, "worker-three", afterExpiry.LeaseToken, "host cannot execute case")
	require.NoError(t, err)
	assert.Equal(t, "cancelled", cancelled.Status)
	assert.Equal(t, "host cannot execute case", cancelled.TerminalReason)
}

func TestEvaluationClaimPropagatesLeaseExpiryFailure(t *testing.T) {
	st := newContextManifestStore(t, "aggregate.Engine.Voice")
	ctx := context.Background()
	run, _, _ := evaluationRunFixture(t, st, false)
	_, err := st.ClaimEvaluationAssignment(ctx, run.ID, "first-worker", "development", time.Nanosecond)
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	_, err = st.sqlDB.ExecContext(ctx, `
		CREATE TRIGGER reject_lease_expiry
		BEFORE UPDATE ON evaluation_assignment_leases
		WHEN OLD.status = 'active'
		BEGIN
			SELECT RAISE(ABORT, 'lease expiry unavailable');
		END
	`)
	require.NoError(t, err)

	claim, err := st.ClaimEvaluationAssignment(ctx, run.ID, "second-worker", "development", time.Minute)
	require.ErrorContains(t, err, "expire leases")
	require.ErrorContains(t, err, "lease expiry unavailable")
	assert.Nil(t, claim)
}

func TestEvaluationClaimPropagatesExpiredAssignmentRequeueFailure(t *testing.T) {
	st := newContextManifestStore(t, "aggregate.Engine.Voice")
	ctx := context.Background()
	run, _, _ := evaluationRunFixture(t, st, false)
	_, err := st.ClaimEvaluationAssignment(ctx, run.ID, "first-worker", "development", time.Nanosecond)
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	_, err = st.sqlDB.ExecContext(ctx, `
		CREATE TRIGGER reject_assignment_requeue
		BEFORE UPDATE ON evaluation_assignments
		WHEN OLD.status = 'leased'
		BEGIN
			SELECT RAISE(ABORT, 'assignment requeue unavailable');
		END
	`)
	require.NoError(t, err)

	reclaimed, err := st.ClaimEvaluationAssignment(ctx, run.ID, "second-worker", "development", time.Minute)
	require.ErrorContains(t, err, "requeue expired assignments")
	require.ErrorContains(t, err, "assignment requeue unavailable")
	assert.Nil(t, reclaimed)
}

func TestEvaluationSubmissionRequiresOrdinaryAttemptProvenance(t *testing.T) {
	st := newContextManifestStore(t, "aggregate.Engine.Voice")
	ctx := context.Background()
	run, _, _ := evaluationRunFixture(t, st, false)
	claim, err := st.ClaimEvaluationAssignment(ctx, run.ID, "worker", "development", time.Minute)
	require.NoError(t, err)
	_, err = st.SubmitEvaluationAssignment(ctx, claim.Assignment.ID, claim.LeaseID, "worker", claim.LeaseToken, EvaluationAssignmentResult{
		TerminalStatus: "completed", Metrics: primaryEvaluationMetrics(1),
	})
	require.ErrorContains(t, err, "ordinary generation attempt")
	require.NoError(t, st.ReleaseEvaluationAssignment(ctx, claim.Assignment.ID, claim.LeaseID, "worker", claim.LeaseToken))
}

func TestEvaluationRunComparesAllSplitsAndSelectsUniqueWinner(t *testing.T) {
	st := newContextManifestStore(t, "aggregate.Engine.Voice")
	ctx := context.Background()
	run, baselineID, _ := evaluationRunFixture(t, st, true)

	for index := range run.Assignments {
		claim, err := st.ClaimEvaluationAssignment(ctx, run.ID, "comparison-worker", "", time.Minute)
		require.NoError(t, err)
		value := 1.0
		if claim.Assignment.ConfigurationID == baselineID {
			value = 0
		}
		result := EvaluationAssignmentResult{
			AttemptID: completeEvaluationAttempt(t, st, claim, index), TerminalStatus: "completed", Metrics: primaryEvaluationMetrics(value),
		}
		submitted, err := st.SubmitEvaluationAssignment(ctx, claim.Assignment.ID, claim.LeaseID, "comparison-worker", claim.LeaseToken, result)
		require.NoError(t, err)
		assert.Equal(t, "submitted", submitted.Status)
		// A repeated submission of the same authoritative result is idempotent.
		_, err = st.SubmitEvaluationAssignment(ctx, claim.Assignment.ID, claim.LeaseID, "comparison-worker", claim.LeaseToken, result)
		require.NoError(t, err)
	}

	completed, err := st.FinalizeEvaluationRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", completed.Status)
	assert.Equal(t, "candidate_wins", completed.Conclusion)
	assert.Equal(t, "candidate", completed.WinningVariant)
	assert.NotEmpty(t, completed.Aggregates)
	assert.NotEmpty(t, completed.Comparisons)
	assert.NotEmpty(t, completed.Observations)
	assert.NotEmpty(t, completed.Observations[0].AssignmentID)
	assert.NotEmpty(t, completed.Observations[0].CaseID)
	assert.NotEmpty(t, completed.Observations[0].SourceType)

	seenSplits := map[string]bool{}
	for _, comparison := range completed.Comparisons {
		seenSplits[comparison.Split] = true
		if comparison.MetricName == "accepted_implementation" {
			assert.Equal(t, "no_material_change", comparison.Conclusion, "engine-derived acceptance overrides host-reported values")
		}
	}
	assert.True(t, seenSplits["all"])
	assert.True(t, seenSplits["development"])
	assert.True(t, seenSplits["held_out"])
}

func TestEvaluationRunFinalizationIsHonestlyInconclusive(t *testing.T) {
	st := newContextManifestStore(t, "aggregate.Engine.Voice")
	ctx := context.Background()
	run, _, _ := evaluationRunFixture(t, st, true)
	completed, err := st.FinalizeEvaluationRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, "inconclusive", completed.Conclusion)
	assert.Contains(t, completed.ConclusionReason, "incomplete")
	assert.Empty(t, completed.WinningVariant)
}
