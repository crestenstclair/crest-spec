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
