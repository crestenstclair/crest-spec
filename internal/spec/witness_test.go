package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/store"
)

const executableWitnessSpec = `package witness

project: {
	name: "witness-project"
	mission: "Prove that the committed implementation emits an audible signal"
	actors: listener: {description: "a listener"}
	goals: audible: {
		description: "the implementation produces audible output"
		priority: "required"
		actors: ["actor.listener"]
		capabilities: ["capability.render"]
		requirements: ["requirement.audible"]
	}
	capabilities: render: {
		description: "render a signal"
		goals: ["goal.audible"]
		acceptance: signal: {
			description: "the signal has a measurable peak"
			actor: "actor.listener"
			evidence: ["evidence.signal"]
		}
	}
	requirements: audible: {
		kind: "functional"
		description: "the signal peak exceeds silence"
		goals: ["goal.audible"]
		capabilities: ["capability.render"]
	}
	evidence: signal: {
		kind: "behavioral_witness"
		description: "engine-captured signal measurement"
		witnesses: ["witness.signal"]
	}
	completion: {requiredGoals: ["goal.audible"]}
	witnesses: signal: {
		scope: "goal"
		goal: "goal.audible"
		capability: "capability.render"
		evidence: ["evidence.signal"]
		command: ["sh", "real.sh"]
		negativeCommand: ["sh", "negative.sh"]
		timeout: "5s"
		observation: {kind: "json_stdout", marker: "CREST_OBSERVATION ", schema: {peak: "number", clipped: "bool"}}
		predicates: [
			{field: "peak", op: "gt", value: 0.1},
			{field: "clipped", op: "eq", value: false},
		]
	}
}
`

func newExecutableWitnessSpec(t *testing.T, negative string) (*Spec, *store.Store) {
	t.Helper()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "project.cue"), []byte(executableWitnessSpec), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "real.sh"), []byte("printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0.7,\"clipped\":false}'"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "negative.sh"), []byte(negative), 0o644))
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return New(st, OSFileSystem{}, &config.Config{SpecDir: specDir}), st
}

func TestVerifyWitnessExecutesBothCommandsAndPersistsEvidence(t *testing.T) {
	s, st := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'")

	result, err := s.VerifyWitness(context.Background(), "witness.signal", "", "")
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, "passed", result.Classification)
	assert.NotEmpty(t, result.ValidationRunID)

	run, err := st.GetValidationRun(context.Background(), result.ValidationRunID)
	require.NoError(t, err)
	require.Len(t, run.Executions, 2)
	assert.Equal(t, "negative", run.Executions[0].Role)
	assert.Equal(t, "real", run.Executions[1].Role)
	assert.Contains(t, run.Executions[1].Stdout, `"peak":0.7`)
	assert.Equal(t, run.Executions[0].SourceTreeHash, run.Executions[1].SourceTreeHash)
	require.Len(t, run.Evidence, 1)
	assert.Equal(t, "evidence.signal", run.Evidence[0].EvidenceID)
	assert.Equal(t, "current", run.Evidence[0].Currency)
	require.Len(t, run.PredicateResults, 4)
	overview, err := s.ProjectOverview(context.Background())
	require.NoError(t, err)
	require.Len(t, overview.Project.Goals, 1)
	assert.Equal(t, "complete", overview.Project.Goals[0].Status)
	assert.Equal(t, "complete", overview.Project.CompletionStatus)
}

func TestVerifyWitnessClassifiesTheaterAndMalformedOutput(t *testing.T) {
	t.Run("theater", func(t *testing.T) {
		s, _ := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0.7,\"clipped\":false}'")
		result, err := s.VerifyWitness(context.Background(), "witness.signal", "", "")
		require.NoError(t, err)
		assert.False(t, result.Passed)
		assert.True(t, result.Theater)
		assert.Equal(t, "theater", result.Classification)
	})

	t.Run("malformed", func(t *testing.T) {
		s, _ := newExecutableWitnessSpec(t, "printf '%s\\n' 'not-an-observation'")
		result, err := s.VerifyWitness(context.Background(), "witness.signal", "", "")
		require.NoError(t, err)
		assert.False(t, result.Passed)
		assert.True(t, result.Malformed)
		assert.Equal(t, "malformed", result.Classification)
	})

	t.Run("source mutation", func(t *testing.T) {
		s, _ := newExecutableWitnessSpec(t, "printf changed > source-mutation.txt; printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'")
		result, err := s.VerifyWitness(context.Background(), "witness.signal", "", "")
		require.NoError(t, err)
		assert.False(t, result.Passed)
		assert.True(t, result.Malformed)
		assert.Equal(t, "malformed", result.Classification)
		assert.Contains(t, result.Reason, "source tree changed")
	})

	t.Run("command failure", func(t *testing.T) {
		s, _ := newExecutableWitnessSpec(t, "exit 7")
		result, err := s.VerifyWitness(context.Background(), "witness.signal", "", "")
		require.NoError(t, err)
		assert.False(t, result.Passed)
		assert.Equal(t, "failed", result.Classification)
		assert.Contains(t, result.Reason, "negative command exited 7")
	})
}

func TestVerifyRejectsCallerSuppliedObservations(t *testing.T) {
	s := newBehavioralSpec(t)
	_, err := s.Verify(context.Background(), "check", map[string]any{"peak": 1}, map[string]any{"peak": 0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "caller-supplied observations are disabled")
}

func TestFinishRequiresDeclaredEvidenceCompletion(t *testing.T) {
	s, st := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'")
	ctx := context.Background()
	require.NoError(t, st.CreateApply("apply-witness", "spec-hash"))
	require.NoError(t, st.CreateSession(store.Session{
		ID: "session-witness", ApplyID: "apply-witness", PlanJSON: "[]", WavesJSON: "[]", HashesJSON: "{}",
	}))

	blocked, err := s.Finish(ctx, "session-witness", false)
	require.NoError(t, err)
	assert.True(t, blocked.Blocked)
	assert.NotEqual(t, "complete", blocked.ProjectCompletion)
	assert.Contains(t, blocked.IncompleteGoals, "goal.audible")

	verified, err := s.VerifyWitness(ctx, "witness.signal", "", "session-witness")
	require.NoError(t, err)
	require.True(t, verified.Passed)
	finished, err := s.Finish(ctx, "session-witness", false)
	require.NoError(t, err)
	assert.False(t, finished.Blocked)
	assert.Equal(t, "complete", finished.ProjectCompletion)
}
