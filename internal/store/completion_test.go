package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/completion"
)

func TestApplyCompletionProjectionSerializesConcurrentWritersBeforeReading(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "completion-concurrency.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	st.sqlDB.SetMaxOpenConns(8)

	ctx := context.Background()
	snapshot := testProjectIntentSnapshot()
	definitions := []IntentVerificationDefinition{
		{
			SnapshotID: "verification.concurrent-a", DefinitionID: "witness.concurrent_a",
			DefinitionType: "witness", Scope: "goal", Kind: "behavioral",
			DefinitionHash: "definition-a", DefinitionJSON: `{"id":"witness.concurrent_a"}`,
			Targets: []IntentVerificationTarget{{Kind: "goal", ID: "goal.play_notes"}, {Kind: "evidence", ID: "evidence.concurrent_a"}},
		},
		{
			SnapshotID: "verification.concurrent-b", DefinitionID: "witness.concurrent_b",
			DefinitionType: "witness", Scope: "goal", Kind: "behavioral",
			DefinitionHash: "definition-b", DefinitionJSON: `{"id":"witness.concurrent_b"}`,
			Targets: []IntentVerificationTarget{{Kind: "goal", ID: "goal.play_notes"}, {Kind: "evidence", ID: "evidence.concurrent_b"}},
		},
		{
			SnapshotID: "verification.concurrent-c", DefinitionID: "witness.concurrent_c",
			DefinitionType: "witness", Scope: "goal", Kind: "behavioral",
			DefinitionHash: "definition-c", DefinitionJSON: `{"id":"witness.concurrent_c"}`,
			Targets: []IntentVerificationTarget{{Kind: "goal", ID: "goal.play_notes"}, {Kind: "evidence", ID: "evidence.concurrent_c"}},
		},
	}
	snapshot.Verification = definitions
	for _, definition := range definitions {
		snapshot.Evidence = append(snapshot.Evidence, IntentEvidence{
			ID: "evidence." + definition.DefinitionID[len("witness."):], Kind: "behavioral_witness",
			Description: "independent concurrent witness evidence",
		})
	}
	require.NoError(t, st.ReconcileProjectIntent(ctx, snapshot))

	goal := completion.DeriveGoal(completion.GoalFacts{
		GoalID: "goal.play_notes", DependenciesComplete: true, AcceptanceVerified: true,
		RequiredEvidenceCurrent: true, RequiredGoalChecksPass: true,
	})
	project := completion.DeriveProject(completion.ProjectFacts{
		ProjectName: snapshot.ProjectName, RequiredGoals: []completion.GoalProjection{goal}, ProjectChecksPass: true,
	})

	type workerResult struct {
		runID string
		err   error
	}
	ready := make(chan workerResult, len(definitions))
	finished := make(chan workerResult, len(definitions))
	projectNow := make(chan struct{})
	startedAt := time.Now().UTC().Add(-time.Second)
	for index, definition := range definitions {
		index, definition := index, definition
		go func() {
			persisted, recordErr := st.RecordValidationRun(ctx, ValidationRunWrite{
				ProjectName: snapshot.ProjectName,
				Run: ValidationRun{
					DefinitionID:   definition.DefinitionID,
					SourceTreeHash: "concurrent-tree", Classification: "passed",
					Reason:      "real passed and negative failed",
					StartedAt:   startedAt.Add(time.Duration(index) * time.Millisecond),
					CompletedAt: startedAt.Add(time.Duration(index+1) * time.Millisecond), DurationMS: 1,
				},
			})
			if recordErr != nil {
				ready <- workerResult{err: recordErr}
				finished <- workerResult{err: recordErr}
				return
			}
			ready <- workerResult{runID: persisted.ID}
			<-projectNow
			projectionErr := st.ApplyCompletionProjection(
				ctx, snapshot.ProjectName, []completion.GoalProjection{goal}, project,
				TransitionSource{Type: "validation_run", ID: persisted.ID},
			)
			finished <- workerResult{runID: persisted.ID, err: projectionErr}
		}()
	}

	runIDs := make(map[string]bool, len(definitions))
	var recordErrors []error
	for range definitions {
		result := <-ready
		if result.err != nil {
			recordErrors = append(recordErrors, result.err)
			continue
		}
		runIDs[result.runID] = true
	}
	if len(recordErrors) > 0 {
		close(projectNow)
		for range definitions {
			<-finished
		}
		require.Empty(t, recordErrors)
	}
	require.Len(t, runIDs, len(definitions), "each concurrent witness must persist its own run before projection")
	runs, err := st.ListValidationRuns(ctx, 10)
	require.NoError(t, err)
	require.Len(t, runs, len(definitions))
	completionFacts, err := st.GetVerificationCompletionFacts(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Len(t, completionFacts.LatestRuns, len(definitions))
	require.Len(t, completionFacts.CurrentEvidence, len(definitions))

	// Hold a real SQLite writer open while all three completion projections
	// begin. With deferred transactions they can read a now-stale snapshot and
	// then fail the read-to-write upgrade with SQLITE_BUSY when this commits.
	// Immediate transactions instead wait here, then serialize before reading.
	blocker, err := st.sqlDB.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, blocker.Close()) })
	_, err = blocker.ExecContext(ctx, "BEGIN IMMEDIATE")
	require.NoError(t, err)
	blockerOpen := true
	t.Cleanup(func() {
		if blockerOpen {
			_, _ = blocker.ExecContext(context.Background(), "ROLLBACK")
		}
	})
	_, err = blocker.ExecContext(ctx,
		"UPDATE project_state SET completion_reason = ? WHERE project_name = ?",
		"concurrent writer", snapshot.ProjectName,
	)
	require.NoError(t, err)

	close(projectNow)

	// Give every projection time to reach SQLite while the writer is held.
	// A deferred transaction reaches its update and either waits or returns
	// SQLITE_BUSY; an immediate transaction remains blocked at BEGIN.
	time.Sleep(100 * time.Millisecond)
	_, err = blocker.ExecContext(ctx, "COMMIT")
	require.NoError(t, err)
	blockerOpen = false

	for range definitions {
		result := <-finished
		require.True(t, runIDs[result.runID])
		require.NoError(t, result.err)
	}

	intent, err := st.GetProjectIntent(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Equal(t, "complete", intent.CompletionStatus)
	for _, persistedGoal := range intent.Goals {
		if persistedGoal.ID == goal.GoalID {
			require.Equal(t, "complete", persistedGoal.Status)
		}
	}
	goalHistory, err := st.ListGoalStatusHistory(ctx, goal.GoalID)
	require.NoError(t, err)
	require.Len(t, goalHistory, 1, "concurrent idempotent projections must emit one transition")
	projectHistory, err := st.ListProjectStatusHistory(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Len(t, projectHistory, 1, "concurrent idempotent projections must emit one transition")
}

func TestApplyCompletionProjectionIsAtomicAndIdempotent(t *testing.T) {
	st, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	ctx := context.Background()
	snapshot := testProjectIntentSnapshot()
	require.NoError(t, st.ReconcileProjectIntent(ctx, snapshot))

	goal := completion.DeriveGoal(completion.GoalFacts{GoalID: "goal.play_notes", HasActivePlan: true})
	project := completion.DeriveProject(completion.ProjectFacts{ProjectName: snapshot.ProjectName, RequiredGoals: []completion.GoalProjection{goal}})
	source := TransitionSource{Type: "plan", ID: "plan-1", SessionID: "session-1"}
	require.NoError(t, st.ApplyCompletionProjection(ctx, snapshot.ProjectName, []completion.GoalProjection{goal}, project, source))
	require.NoError(t, st.ApplyCompletionProjection(ctx, snapshot.ProjectName, []completion.GoalProjection{goal}, project, source))

	goalHistory, err := st.ListGoalStatusHistory(ctx, goal.GoalID)
	require.NoError(t, err)
	require.Len(t, goalHistory, 1)
	require.Equal(t, "declared", goalHistory[0].FromStatus)
	require.Equal(t, "planned", goalHistory[0].ToStatus)
	require.Equal(t, "session-1", goalHistory[0].Source.SessionID)
	projectHistory, err := st.ListProjectStatusHistory(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Len(t, projectHistory, 1)

	intent, err := st.GetProjectIntent(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Equal(t, "planned", intent.CompletionStatus)
	require.NotEmpty(t, intent.CompletionReason)
}

func TestApplyCompletionProjectionRollsBackAllTransitions(t *testing.T) {
	st, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	ctx := context.Background()
	snapshot := testProjectIntentSnapshot()
	require.NoError(t, st.ReconcileProjectIntent(ctx, snapshot))

	valid := completion.GoalProjection{GoalID: "goal.play_notes", Status: completion.Planned, Reason: "planned"}
	unknown := completion.GoalProjection{GoalID: "goal.unknown", Status: completion.Planned, Reason: "planned"}
	project := completion.ProjectProjection{ProjectName: snapshot.ProjectName, Status: completion.Planned, Reason: "planned"}
	err = st.ApplyCompletionProjection(ctx, snapshot.ProjectName, []completion.GoalProjection{valid, unknown}, project, TransitionSource{Type: "test", ID: "failure"})
	require.ErrorContains(t, err, "goal.unknown")

	intent, err := st.GetProjectIntent(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Equal(t, "declared", intent.CompletionStatus)
	for _, goal := range intent.Goals {
		require.Equal(t, "declared", goal.Status)
	}
	history, err := st.ListGoalStatusHistory(ctx, valid.GoalID)
	require.NoError(t, err)
	require.Empty(t, history)
}
