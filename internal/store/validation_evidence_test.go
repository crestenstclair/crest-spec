package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordValidationRunPersistsProvenanceAndSupersedesEvidence(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	snapshot := testProjectIntentSnapshot()
	snapshot.Verification = []IntentVerificationDefinition{{
		SnapshotID: "verification.snapshot-1", DefinitionID: "witness.audible_note",
		DefinitionType: "witness", Scope: "goal", Kind: "behavioral",
		DefinitionHash: "definition-hash", DefinitionJSON: `{"id":"witness.audible_note"}`,
		Targets: []IntentVerificationTarget{
			{Kind: "goal", ID: "goal.play_notes"},
			{Kind: "resource", ID: "aggregate.Engine.Voice"},
			{Kind: "evidence", ID: "evidence.note_witness"},
		},
	}}
	require.NoError(t, st.ReconcileProjectIntent(ctx, snapshot))

	started := time.Now().UTC().Add(-time.Second)
	first, err := st.RecordValidationRun(ctx, ValidationRunWrite{
		ProjectName: snapshot.ProjectName,
		Run: ValidationRun{
			DefinitionID: "witness.audible_note", SourceTreeHash: "tree-one",
			Classification: "passed", Reason: "real passed and negative failed",
			StartedAt: started, CompletedAt: started.Add(20 * time.Millisecond), DurationMS: 20,
			Executions: []ValidationExecution{
				{
					Role: "real", CommandJSON: `["tone-witness"]`, CommandHash: "real-command",
					WorkingDirectory: ".", EnvironmentJSON: `{}`, EnvironmentHash: "environment",
					SourceTreeHash: "tree-one", ExecutableHash: "executable", Stdout: `CREST_OBSERVATION {"peak":0.7}`,
					ExitCode: 0, StartedAt: started, CompletedAt: started.Add(10 * time.Millisecond), DurationMS: 10,
					Artifacts:   []ValidationArtifact{{Path: "target/tone.wav", Content: "wave", ByteSize: 4}},
					Observation: &ParsedObservation{SchemaJSON: `{"peak":"number"}`, SchemaHash: "schema", ObservationJSON: `{"peak":0.7}`, ObservationHash: "observation"},
				},
				{
					Role: "negative", CommandJSON: `["tone-witness","--silent"]`, CommandHash: "negative-command",
					WorkingDirectory: ".", EnvironmentJSON: `{}`, EnvironmentHash: "environment",
					SourceTreeHash: "tree-one", ExecutableHash: "executable", Stdout: `CREST_OBSERVATION {"peak":0}`,
					ExitCode: 0, StartedAt: started.Add(10 * time.Millisecond), CompletedAt: started.Add(20 * time.Millisecond), DurationMS: 10,
					Observation: &ParsedObservation{SchemaJSON: `{"peak":"number"}`, SchemaHash: "schema", ObservationJSON: `{"peak":0}`, ObservationHash: "negative-observation"},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, first.Executions, 2)
	require.Len(t, first.Evidence, 1)
	assert.Equal(t, first.ID, first.Evidence[0].RunID)
	assert.Equal(t, "current", first.Evidence[0].Currency)
	assert.Equal(t, "wave", first.Executions[1].Artifacts[0].Content) // executions sort negative before real
	assert.Equal(t, `{"peak":0.7}`, first.Executions[1].Observation.ObservationJSON)

	secondStart := started.Add(time.Second)
	second, err := st.RecordValidationRun(ctx, ValidationRunWrite{
		ProjectName: snapshot.ProjectName,
		Run: ValidationRun{
			DefinitionID: "witness.audible_note", SourceTreeHash: "tree-two",
			Classification: "failed", Reason: "real observation failed",
			StartedAt: secondStart, CompletedAt: secondStart.Add(time.Millisecond), DurationMS: 1,
			Executions: []ValidationExecution{{
				Role: "real", CommandJSON: `["tone-witness"]`, CommandHash: "real-command",
				WorkingDirectory: ".", EnvironmentJSON: `{}`, EnvironmentHash: "environment",
				SourceTreeHash: "tree-two", Stdout: `CREST_OBSERVATION {"peak":0}`,
				ExitCode: 0, StartedAt: secondStart, CompletedAt: secondStart.Add(time.Millisecond), DurationMS: 1,
			}},
		},
	})
	require.NoError(t, err)
	require.Len(t, second.Evidence, 1)
	assert.Equal(t, "failed", second.Evidence[0].Classification)

	reloaded, err := st.GetValidationRun(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "stale", reloaded.Evidence[0].Currency)
	assert.NotNil(t, reloaded.Evidence[0].InvalidatedAt)
}

func TestReconcileProjectIntentInvalidatesEvidenceFromChangedVerificationDefinition(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	snapshot := testProjectIntentSnapshot()
	snapshot.Verification = []IntentVerificationDefinition{{
		SnapshotID: "verification.snapshot-v1", DefinitionID: "witness.audible_note",
		DefinitionType: "witness", Scope: "goal", Kind: "behavioral",
		DefinitionHash: "definition-v1", DefinitionJSON: `{"id":"witness.audible_note","predicate":"peak > 0.1"}`,
		Targets: []IntentVerificationTarget{
			{Kind: "goal", ID: "goal.play_notes"},
			{Kind: "evidence", ID: "evidence.note_witness"},
		},
	}}
	require.NoError(t, st.ReconcileProjectIntent(ctx, snapshot))

	started := time.Now().UTC().Add(-time.Second)
	run, err := st.RecordValidationRun(ctx, ValidationRunWrite{
		ProjectName: snapshot.ProjectName,
		Run: ValidationRun{
			DefinitionID: "witness.audible_note", SourceTreeHash: "tree-v1",
			Classification: "passed", Reason: "v1 real passed and negative failed",
			StartedAt: started, CompletedAt: started.Add(time.Millisecond), DurationMS: 1,
		},
	})
	require.NoError(t, err)
	require.Len(t, run.Evidence, 1)
	require.Equal(t, "current", run.Evidence[0].Currency)

	changed := snapshot
	changed.SpecHash = "spec-v2"
	changed.Verification = []IntentVerificationDefinition{{
		SnapshotID: "verification.snapshot-v2", DefinitionID: "witness.audible_note",
		DefinitionType: "witness", Scope: "goal", Kind: "behavioral",
		DefinitionHash: "definition-v2", DefinitionJSON: `{"id":"witness.audible_note","predicate":"peak > 0.9"}`,
		Targets: []IntentVerificationTarget{
			{Kind: "goal", ID: "goal.play_notes"},
			{Kind: "evidence", ID: "evidence.note_witness"},
		},
	}}
	require.NoError(t, st.ReconcileProjectIntent(ctx, changed))

	facts, err := st.GetVerificationCompletionFacts(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Empty(t, facts.LatestRuns, "a run of an inactive definition is not a current verification result")
	require.Empty(t, facts.CurrentEvidence, "evidence from an inactive definition must not satisfy current acceptance")

	reloaded, err := st.GetValidationRun(ctx, run.ID)
	require.NoError(t, err)
	require.Len(t, reloaded.Evidence, 1)
	require.Equal(t, "stale", reloaded.Evidence[0].Currency,
		"definition reconciliation must durably invalidate superseded evidence")
	require.NotNil(t, reloaded.Evidence[0].InvalidatedAt)
	var invalidations int
	require.NoError(t, st.sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM verification_evidence_invalidations
		WHERE evidence_record_id = ? AND source_type = 'project_intent' AND source_id = ?`,
		reloaded.Evidence[0].ID, changed.SpecHash,
	).Scan(&invalidations))
	require.Equal(t, 1, invalidations,
		"definition reconciliation must preserve durable invalidation provenance")

	// Defense in depth: even a legacy or externally-corrupted row that still
	// says current cannot cross the active-definition join used by completion.
	_, err = st.sqlDB.ExecContext(ctx,
		"UPDATE verification_evidence_records SET currency = 'current', invalidated_at = NULL WHERE id = ?",
		reloaded.Evidence[0].ID,
	)
	require.NoError(t, err)
	facts, err = st.GetVerificationCompletionFacts(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Empty(t, facts.CurrentEvidence)
}

func TestReconcileProjectIntentDoesNotResurrectEvidenceAfterTargetRemovalAndReaddition(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	snapshot := testProjectIntentSnapshot()
	definition := IntentVerificationDefinition{
		SnapshotID: "verification.stable-definition", DefinitionID: "witness.audible_note",
		DefinitionType: "witness", Scope: "goal", Kind: "behavioral",
		DefinitionHash: "stable-definition", DefinitionJSON: `{"id":"witness.audible_note"}`,
		Targets: []IntentVerificationTarget{
			{Kind: "goal", ID: "goal.play_notes"},
			{Kind: "evidence", ID: "evidence.note_witness"},
		},
	}
	snapshot.Verification = []IntentVerificationDefinition{definition}
	require.NoError(t, st.ReconcileProjectIntent(ctx, snapshot))

	started := time.Now().UTC().Add(-time.Second)
	run, err := st.RecordValidationRun(ctx, ValidationRunWrite{
		ProjectName: snapshot.ProjectName,
		Run: ValidationRun{
			DefinitionID: definition.DefinitionID, SourceTreeHash: "tree-v1",
			Classification: "passed", Reason: "real passed and negative failed",
			StartedAt: started, CompletedAt: started.Add(time.Millisecond), DurationMS: 1,
		},
	})
	require.NoError(t, err)
	require.Len(t, run.Evidence, 1)
	require.Equal(t, "current", run.Evidence[0].Currency)

	withoutTarget := snapshot
	withoutTarget.SpecHash = "spec-without-evidence-target"
	definitionWithoutTarget := definition
	definitionWithoutTarget.Targets = []IntentVerificationTarget{{Kind: "goal", ID: "goal.play_notes"}}
	withoutTarget.Verification = []IntentVerificationDefinition{definitionWithoutTarget}
	require.NoError(t, st.ReconcileProjectIntent(ctx, withoutTarget))

	reloaded, err := st.GetValidationRun(ctx, run.ID)
	require.NoError(t, err)
	require.Len(t, reloaded.Evidence, 1)
	require.Equal(t, "stale", reloaded.Evidence[0].Currency,
		"removing an evidence target must durably invalidate its old result")
	require.NotNil(t, reloaded.Evidence[0].InvalidatedAt)
	var invalidations int
	require.NoError(t, st.sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM verification_evidence_invalidations
		WHERE evidence_record_id = ? AND source_type = 'project_intent' AND source_id = ?`,
		reloaded.Evidence[0].ID, withoutTarget.SpecHash,
	).Scan(&invalidations))
	require.Equal(t, 1, invalidations,
		"target removal and evidence invalidation must commit with one provenance record")

	readded := snapshot
	readded.SpecHash = "spec-readded-evidence-target"
	require.NoError(t, st.ReconcileProjectIntent(ctx, readded))
	facts, err := st.GetVerificationCompletionFacts(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Empty(t, facts.CurrentEvidence,
		"re-adding a target must require a new witness run, not resurrect old evidence")
}

func TestInvalidateVerificationEvidenceMarksResourceEvidenceStale(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	snapshot := testProjectIntentSnapshot()
	snapshot.Verification = []IntentVerificationDefinition{{
		SnapshotID: "verification.snapshot-1", DefinitionID: "witness.audible_note",
		DefinitionType: "witness", Scope: "goal", Kind: "behavioral",
		DefinitionHash: "definition-hash", DefinitionJSON: `{"id":"witness.audible_note"}`,
		Targets: []IntentVerificationTarget{
			{Kind: "goal", ID: "goal.play_notes"},
			{Kind: "resource", ID: "aggregate.Engine.Voice"},
			{Kind: "evidence", ID: "evidence.note_witness"},
		},
	}}
	require.NoError(t, st.ReconcileProjectIntent(ctx, snapshot))

	started := time.Now().UTC().Add(-time.Second)
	run, err := st.RecordValidationRun(ctx, ValidationRunWrite{
		ProjectName: snapshot.ProjectName,
		Run: ValidationRun{
			DefinitionID: "witness.audible_note", SourceTreeHash: "tree-one",
			Classification: "passed", Reason: "real passed and negative failed",
			StartedAt: started, CompletedAt: started.Add(time.Millisecond), DurationMS: 1,
		},
	})
	require.NoError(t, err)
	require.Len(t, run.Evidence, 1)
	assert.Equal(t, run.ID, run.Evidence[0].RunID)
	assert.Equal(t, "current", run.Evidence[0].Currency)

	count, err := st.InvalidateVerificationEvidence(
		ctx,
		snapshot.ProjectName,
		[]string{"aggregate.Engine.Voice", "aggregate.Engine.Voice"},
		"resource declaration changed",
		TransitionSource{Type: "plan", ID: "spec-hash"},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	reloaded, err := st.GetValidationRun(ctx, run.ID)
	require.NoError(t, err)
	require.Len(t, reloaded.Evidence, 1)
	assert.Equal(t, "stale", reloaded.Evidence[0].Currency)
	assert.NotNil(t, reloaded.Evidence[0].InvalidatedAt)

	count, err = st.InvalidateVerificationEvidence(
		ctx,
		snapshot.ProjectName,
		[]string{"aggregate.Engine.Voice"},
		"same plan evaluated again",
		TransitionSource{Type: "plan", ID: "spec-hash"},
	)
	require.NoError(t, err)
	assert.Zero(t, count)
}
