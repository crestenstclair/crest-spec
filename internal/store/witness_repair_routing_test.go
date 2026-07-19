package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/stretchr/testify/require"
)

func TestBehavioralWitnessRepairRoutingDurablyBoundsExactEpoch(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "state.db")
	st := newBehavioralWitnessRoutingStore(t, databasePath)

	recordFailure := func(sourceTreeHash string) *ValidationRun {
		t.Helper()
		started := time.Now().UTC()
		run, err := st.RecordValidationRun(ctx, ValidationRunWrite{
			ProjectName: "witness-routing-project",
			Run: ValidationRun{
				DefinitionID: "witness.signal", SessionID: "session-witness-routing",
				SourceTreeHash: sourceTreeHash, Classification: "failed",
				Reason: "signal predicate failed", StartedAt: started,
				CompletedAt: started.Add(time.Millisecond), DurationMS: 1,
			},
		})
		require.NoError(t, err)
		return run
	}
	route := func(runID string) *BehavioralWitnessRepairRoutingResult {
		t.Helper()
		result, err := st.RouteBehavioralWitnessFailure(ctx, BehavioralWitnessRepairRoutingInput{
			SessionID: "session-witness-routing", ValidationRunID: runID,
			ResourceIDs: []string{"resource.signal"}, Reason: "behavioral signal failed",
			MaxRepairRetries: 2,
		})
		require.NoError(t, err)
		return result
	}
	recommitUnchanged := func() {
		t.Helper()
		require.NoError(t, st.UpdateSessionResourceState(
			"session-witness-routing", "resource.signal", "committed", "", "", 0, "",
		))
	}

	first := route(recordFailure("tree-v1").ID)
	require.Equal(t, []string{"resource.signal"}, first.ReopenedResources)
	require.Empty(t, first.ExhaustedResources)
	require.Equal(t, 1, first.Epochs["resource.signal"].RetryCount)
	require.Equal(t, "source_tree", first.Epochs["resource.signal"].SourceKind)

	recommitUnchanged()
	second := route(recordFailure("tree-v1").ID)
	require.Equal(t, []string{"resource.signal"}, second.ReopenedResources)
	require.Equal(t, 2, second.Epochs["resource.signal"].RetryCount)

	recommitUnchanged()
	third := route(recordFailure("tree-v1").ID)
	require.Empty(t, third.ReopenedResources)
	require.Equal(t, []string{"resource.signal"}, third.ExhaustedResources)
	require.Equal(t, 3, third.Epochs["resource.signal"].RetryCount)
	require.Equal(t, "exhausted", third.Epochs["resource.signal"].Disposition)

	resource, err := st.GetSessionResource("session-witness-routing", "resource.signal")
	require.NoError(t, err)
	require.Equal(t, "errored", resource.State)
	require.Contains(t, resource.LastError, "behavioral-witness repair retries exhausted")
	exhausted, err := st.ActiveBehavioralWitnessRepairExhaustion(
		ctx, "session-witness-routing", "resource.signal",
	)
	require.NoError(t, err)
	require.NotNil(t, exhausted)
	require.Equal(t, 3, exhausted.RetryCount)
	require.Equal(t, 2, exhausted.MaxRetries)

	// Exhaustion is SQLite provenance, not an in-memory counter. It survives a
	// complete Store restart and still identifies the exact source/definition.
	require.NoError(t, st.Close())
	st = nil
	restarted, err := New(databasePath)
	require.NoError(t, err)
	st = restarted
	t.Cleanup(func() {
		if st != nil {
			require.NoError(t, st.Close())
		}
	})
	exhausted, err = st.ActiveBehavioralWitnessRepairExhaustion(
		ctx, "session-witness-routing", "resource.signal",
	)
	require.NoError(t, err)
	require.NotNil(t, exhausted)
	require.Equal(t, 3, exhausted.RetryCount)

	// A genuinely new accepted candidate is a new content-addressed epoch. A
	// candidate with unchanged bytes would retain the same candidate hash and
	// therefore would not evade the budget.
	require.NoError(t, st.UpdateSessionResourceState(
		"session-witness-routing", "resource.signal", "pending", "", "", 0, "",
	))
	manifest := testContextManifest("attempt-new-source", "manifest-new-source", "resource.signal")
	manifest.Attempt.SessionID = "session-witness-routing"
	manifest.Attempt.ApplyID = "apply-witness-routing"
	prepareAndClaimTestContext(t, st, manifest)
	executionManifest := testExecutionManifest()
	executionManifest.AttemptID = manifest.Attempt.ID
	executionManifest.ContextManifestID = manifest.ID
	startedExecution, err := st.StartExecution(ctx, executionManifest)
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(ctx, startedExecution.ID, []CandidateFile{{
		Path: "signal.go", Content: "package signal\nconst Version = 2\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: startedExecution.ID, ToStatus: execution.StatusValidating,
	})
	require.NoError(t, err)
	_, err = st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: startedExecution.ID, Accepted: true, Reason: "new source accepted",
		Resource: &Resource{
			ID: "resource.signal", Kind: "asset", DeclarationHash: "declaration-v2",
			EffectiveHash: "effective-v2", GenerationInputHash: "generation-v2",
			DependencyContractHash: "dependency-v2", Model: "test-model",
		},
		Files: []GeneratedFile{{
			Path: "signal.go", ResourceID: "resource.signal",
			ContentHash: candidate.Files[0].ContentHash,
			PromptHash:  startedExecution.ContextHash, Model: "test-model",
		}},
		Attempts: 1,
	})
	require.NoError(t, err)
	exhausted, err = st.ActiveBehavioralWitnessRepairExhaustion(
		ctx, "session-witness-routing", "resource.signal",
	)
	require.NoError(t, err)
	require.Nil(t, exhausted, "a genuinely new accepted candidate starts a distinct repair epoch")

	newSource := route(recordFailure("tree-v2").ID)
	require.Equal(t, []string{"resource.signal"}, newSource.ReopenedResources)
	require.Equal(t, 1, newSource.Epochs["resource.signal"].RetryCount)
	require.Equal(t, "candidate", newSource.Epochs["resource.signal"].SourceKind)

	// A changed immutable witness definition is also a deliberate epoch change,
	// even when it observes the same accepted candidate.
	recommitUnchanged()
	reconcileBehavioralWitnessDefinition(t, st, "witness-snapshot-v2", "witness-definition-v2")
	newDefinition := route(recordFailure("tree-v2").ID)
	require.Equal(t, []string{"resource.signal"}, newDefinition.ReopenedResources)
	require.Equal(t, 1, newDefinition.Epochs["resource.signal"].RetryCount)
	require.Equal(t, "witness-definition-v2", newDefinition.Epochs["resource.signal"].DefinitionHash)
}

func TestBehavioralWitnessRepairRoutingRejectsUndeclaredOrOutOfSessionOwners(t *testing.T) {
	ctx := context.Background()
	st := newBehavioralWitnessRoutingStore(t, ":memory:")
	started := time.Now().UTC()
	run, err := st.RecordValidationRun(ctx, ValidationRunWrite{
		ProjectName: "witness-routing-project",
		Run: ValidationRun{
			DefinitionID: "witness.signal", SessionID: "session-witness-routing",
			SourceTreeHash: "tree-v1", Classification: "failed", Reason: "failed",
			StartedAt: started, CompletedAt: started.Add(time.Millisecond), DurationMS: 1,
		},
	})
	require.NoError(t, err)

	result, err := st.RouteBehavioralWitnessFailure(ctx, BehavioralWitnessRepairRoutingInput{
		SessionID: "session-witness-routing", ValidationRunID: run.ID,
		ResourceIDs: []string{"resource.not-declared"}, Reason: "must fail closed",
		MaxRepairRetries: 2,
	})
	require.ErrorContains(t, err, "not declared")
	require.Nil(t, result)
	owner, err := st.GetSessionResource("session-witness-routing", "resource.signal")
	require.NoError(t, err)
	require.Equal(t, "committed", owner.State)

	theaterRun, err := st.RecordValidationRun(ctx, ValidationRunWrite{
		ProjectName: "witness-routing-project",
		Run: ValidationRun{
			DefinitionID: "witness.signal", SessionID: "session-witness-routing",
			SourceTreeHash: "tree-v1", Classification: "theater", Reason: "negative passed",
			StartedAt: started, CompletedAt: started.Add(time.Millisecond), DurationMS: 1,
		},
	})
	require.NoError(t, err)
	result, err = st.RouteBehavioralWitnessFailure(ctx, BehavioralWitnessRepairRoutingInput{
		SessionID: "session-witness-routing", ValidationRunID: theaterRun.ID,
		ResourceIDs: []string{"resource.signal"}, Reason: "must not blame production for theater",
		MaxRepairRetries: 2,
	})
	require.ErrorContains(t, err, "not declared in witness.signal counterexampleRepairResources")
	require.Nil(t, result)
}

func newBehavioralWitnessRoutingStore(t *testing.T, databasePath string) *Store {
	t.Helper()
	st, err := New(databasePath)
	require.NoError(t, err)
	if databasePath == ":memory:" {
		t.Cleanup(func() { require.NoError(t, st.Close()) })
	}
	require.NoError(t, st.CreateApply("apply-witness-routing", "spec-hash"))
	require.NoError(t, st.CreateSession(Session{
		ID: "session-witness-routing", ApplyID: "apply-witness-routing",
		PlanJSON: "[]", WavesJSON: "[]", HashesJSON: "{}",
	}))
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-witness-routing", ResourceID: "resource.signal",
		State: "committed", Phase: "verified", ExecutionStrategy: "validate_existing",
		MaxRetries: 3,
	}))
	reconcileBehavioralWitnessDefinition(t, st, "witness-snapshot-v1", "witness-definition-v1")
	return st
}

func reconcileBehavioralWitnessDefinition(t *testing.T, st *Store, snapshotID, definitionHash string) {
	t.Helper()
	require.NoError(t, st.ReconcileProjectIntent(context.Background(), ProjectIntentSnapshot{
		ProjectName: "witness-routing-project", Mission: "prove witness repair routing",
		SpecHash: "spec-hash",
		Verification: []IntentVerificationDefinition{{
			SnapshotID: snapshotID, DefinitionID: "witness.signal",
			DefinitionType: "witness", Scope: "project", Kind: "behavioral",
			DefinitionHash: definitionHash,
			DefinitionJSON: `{"repairResources":["resource.signal"]}`,
		}},
	}))
}
