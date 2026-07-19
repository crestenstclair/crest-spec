package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
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
	contexts: Audio: {
		purpose: "own the behavior exercised by the witness"
		valueObjects: {
			Signal: {description: "the exact repair owner", from: "f64"}
			Clipping: {description: "the clipping predicate repair owner", from: "bool"}
			WitnessHarness: {description: "the controlled counterexample repair owner", from: "string"}
			Unrelated: {description: "an observed resource that must not be reopened", from: "f64"}
		}
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
		resources: ["valueObject.Audio.Signal", "valueObject.Audio.Clipping", "valueObject.Audio.WitnessHarness", "valueObject.Audio.Unrelated"]
		repairResources: ["valueObject.Audio.Signal", "valueObject.Audio.Clipping", "valueObject.Audio.WitnessHarness"]
		counterexampleRepairResources: ["valueObject.Audio.WitnessHarness"]
		evidence: ["evidence.signal"]
		command: ["sh", "real.sh"]
		negativeCommand: ["sh", "negative.sh"]
		timeout: "5s"
		observation: {kind: "json_stdout", marker: "CREST_OBSERVATION ", schema: {peak: "number", clipped: "bool"}}
		predicates: [
			{field: "peak", op: "gt", value: 0.1, repairResources: ["valueObject.Audio.Signal"]},
			{field: "clipped", op: "eq", value: false, repairResources: ["valueObject.Audio.Clipping"]},
		]
	}
}
`

const (
	witnessRepairResourceID   = "valueObject.Audio.Signal"
	witnessClippingResourceID = "valueObject.Audio.Clipping"
	witnessHarnessResourceID  = "valueObject.Audio.WitnessHarness"
	witnessUnrelatedID        = "valueObject.Audio.Unrelated"
)

func newExecutableWitnessSpec(t *testing.T, negative string) (*Spec, *store.Store) {
	return newExecutableWitnessSpecWithExpectedNegativeExit(t, negative, 0)
}

func newExecutableWitnessSpecWithExpectedNegativeExit(t *testing.T, negative string, expectedExitCode int) (*Spec, *store.Store) {
	t.Helper()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	specSource := executableWitnessSpec
	if expectedExitCode != 0 {
		specSource = strings.Replace(specSource,
			"\t\tnegativeCommand: [\"sh\", \"negative.sh\"]",
			fmt.Sprintf("\t\tnegativeCommand: [\"sh\", \"negative.sh\"]\n\t\tnegativeExpectedExitCode: %d", expectedExitCode), 1)
	}
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "project.cue"), []byte(specSource), 0o644))
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

func seedExecutableWitnessSession(t *testing.T, st *store.Store) string {
	return seedExecutableWitnessSessionResources(t, st, []string{
		witnessRepairResourceID, witnessUnrelatedID,
	})
}

func seedExecutableWitnessSessionResources(t *testing.T, st *store.Store, resourceIDs []string) string {
	t.Helper()
	const sessionID = "session-witness-repair"
	actionsList := make([]planpkg.PlannedAction, 0, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		actionsList = append(actionsList, planpkg.PlannedAction{
			ResourceID: resourceID, Kind: planpkg.ActionModify,
			ExecutionStrategy: planpkg.ExecutionStrategyValidateExisting,
		})
	}
	actions, err := json.Marshal(actionsList)
	require.NoError(t, err)
	waves, err := json.Marshal([][]string{resourceIDs})
	require.NoError(t, err)
	require.NoError(t, st.CreateApply("apply-witness-repair", "spec-hash"))
	require.NoError(t, st.CreateSession(store.Session{
		ID: sessionID, ApplyID: "apply-witness-repair", PlanJSON: string(actions), WavesJSON: string(waves), HashesJSON: "{}",
	}))
	for _, resourceID := range resourceIDs {
		require.NoError(t, st.UpsertSessionResource(store.SessionResource{
			SessionID: sessionID, ResourceID: resourceID, State: string(StateCommitted),
			WaveIndex: 0, MaxRetries: 3, Attempts: 2, Phase: "verified",
			ExecutionStrategy: planpkg.ExecutionStrategyValidateExisting,
		}))
	}
	return sessionID
}

func writeRealWitness(t *testing.T, s *Spec, observation string) {
	t.Helper()
	require.NoError(t, os.WriteFile(
		filepath.Join(s.projectRoot(), "real.sh"),
		[]byte("printf '%s\\n' '"+observation+"'"),
		0o644,
	))
}

func TestFailedWitnessReopensOnlyDeclaredRepairResources(t *testing.T) {
	s, st := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'")
	sessionID := seedExecutableWitnessSession(t, st)
	writeRealWitness(t, s, `CREST_OBSERVATION {"peak":0,"clipped":false}`)

	result, err := s.VerifyWitness(context.Background(), "witness.signal", "", sessionID)
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Equal(t, "failed", result.Classification)
	require.Equal(t, []string{witnessRepairResourceID}, result.ReopenedResources)

	repair, err := st.GetSessionResource(sessionID, witnessRepairResourceID)
	require.NoError(t, err)
	require.Equal(t, string(StatePending), repair.State)
	require.Equal(t, "integration_failed", repair.Phase)
	require.Equal(t, "generate", repair.ExecutionStrategy)
	require.Zero(t, repair.Attempts)
	require.Contains(t, repair.LastError, "witness.signal")
	require.Contains(t, repair.LastError, result.ValidationRunID)
	require.Contains(t, repair.LastError, result.Reason)

	unrelated, err := st.GetSessionResource(sessionID, witnessUnrelatedID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), unrelated.State)
	require.Equal(t, "verified", unrelated.Phase)
	require.Equal(t, 2, unrelated.Attempts)

	next, err := s.Next(context.Background(), sessionID)
	require.NoError(t, err)
	require.False(t, next.Done)
	require.Equal(t, 0, next.WaveIndex)
	require.Len(t, next.Resources, 1,
		"a witness repair must become reachable scheduler work without reopening unrelated observers")
	require.Equal(t, witnessRepairResourceID, next.Resources[0].ResourceID)
	require.Equal(t, planpkg.ExecutionStrategyGenerate, next.Resources[0].ExecutionStrategy)
}

func TestFailedWitnessRoutesOnlyOwnersOfFailedPositivePredicates(t *testing.T) {
	s, st := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'")
	resourceIDs := []string{
		witnessRepairResourceID, witnessClippingResourceID,
		witnessHarnessResourceID, witnessUnrelatedID,
	}
	sessionID := seedExecutableWitnessSessionResources(t, st, resourceIDs)
	writeRealWitness(t, s, `CREST_OBSERVATION {"peak":0,"clipped":true}`)

	result, err := s.VerifyWitness(context.Background(), "witness.signal", "", sessionID)
	require.NoError(t, err)
	require.Equal(t, "failed", result.Classification)
	require.Equal(t,
		[]string{witnessClippingResourceID, witnessRepairResourceID},
		result.ReopenedResources,
	)
	for _, resourceID := range []string{witnessHarnessResourceID, witnessUnrelatedID} {
		resource, getErr := st.GetSessionResource(sessionID, resourceID)
		require.NoError(t, getErr)
		require.Equal(t, string(StateCommitted), resource.State,
			"a positive product failure must not reopen the counterexample harness or unrelated observer")
	}
}

func TestWitnessTheaterRoutesOnlyOptedInCounterexampleOwner(t *testing.T) {
	s, st := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0.7,\"clipped\":false}'")
	resourceIDs := []string{
		witnessRepairResourceID, witnessClippingResourceID,
		witnessHarnessResourceID, witnessUnrelatedID,
	}
	sessionID := seedExecutableWitnessSessionResources(t, st, resourceIDs)

	result, err := s.VerifyWitness(context.Background(), "witness.signal", "", sessionID)
	require.NoError(t, err)
	require.Equal(t, "theater", result.Classification)
	require.Equal(t, []string{witnessHarnessResourceID}, result.ReopenedResources)
	for _, resourceID := range []string{
		witnessRepairResourceID, witnessClippingResourceID, witnessUnrelatedID,
	} {
		resource, getErr := st.GetSessionResource(sessionID, resourceID)
		require.NoError(t, getErr)
		require.Equal(t, string(StateCommitted), resource.State,
			"counterexample failure must never be attributed to production owners")
	}
}

func TestUnexpectedNegativeExitRoutesOnlyCounterexampleOwner(t *testing.T) {
	s, st := newExecutableWitnessSpecWithExpectedNegativeExit(t,
		"printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'", 1)
	resourceIDs := []string{
		witnessRepairResourceID, witnessClippingResourceID,
		witnessHarnessResourceID, witnessUnrelatedID,
	}
	sessionID := seedExecutableWitnessSessionResources(t, st, resourceIDs)

	result, err := s.VerifyWitness(context.Background(), "witness.signal", "", sessionID)
	require.NoError(t, err)
	require.Equal(t, "failed", result.Classification)
	require.Contains(t, result.Reason, "negative command exited 0, expected 1")
	require.Equal(t, []string{witnessHarnessResourceID}, result.ReopenedResources)
}

func TestRealProcessFailureUsesWitnessWideRepairFallback(t *testing.T) {
	s, st := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'")
	resourceIDs := []string{
		witnessRepairResourceID, witnessClippingResourceID,
		witnessHarnessResourceID, witnessUnrelatedID,
	}
	sessionID := seedExecutableWitnessSessionResources(t, st, resourceIDs)
	require.NoError(t, os.WriteFile(filepath.Join(s.projectRoot(), "real.sh"), []byte("exit 1"), 0o644))

	result, err := s.VerifyWitness(context.Background(), "witness.signal", "", sessionID)
	require.NoError(t, err)
	require.Equal(t, "failed", result.Classification)
	require.Equal(t, []string{
		witnessClippingResourceID, witnessRepairResourceID, witnessHarnessResourceID,
	}, result.ReopenedResources)
	unrelated, err := st.GetSessionResource(sessionID, witnessUnrelatedID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), unrelated.State)
}

func TestWitnessTheaterWithoutCounterexampleOptInNeverReopens(t *testing.T) {
	s, st := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0.7,\"clipped\":false}'")
	specPath := filepath.Join(s.cfg.SpecDir, "project.cue")
	source, err := os.ReadFile(specPath)
	require.NoError(t, err)
	source = []byte(strings.Replace(
		string(source),
		`counterexampleRepairResources: ["valueObject.Audio.WitnessHarness"]`,
		`counterexampleRepairResources: []`,
		1,
	))
	require.NoError(t, os.WriteFile(specPath, source, 0o644))
	resourceIDs := []string{
		witnessRepairResourceID, witnessClippingResourceID,
		witnessHarnessResourceID, witnessUnrelatedID,
	}
	sessionID := seedExecutableWitnessSessionResources(t, st, resourceIDs)

	result, err := s.VerifyWitness(context.Background(), "witness.signal", "", sessionID)
	require.NoError(t, err)
	require.Equal(t, "theater", result.Classification)
	require.Empty(t, result.ReopenedResources)
	for _, resourceID := range resourceIDs {
		resource, getErr := st.GetSessionResource(sessionID, resourceID)
		require.NoError(t, getErr)
		require.Equal(t, string(StateCommitted), resource.State)
	}
}

func TestFailedWitnessRepairIsBoundedForUnchangedSourceAndDefinition(t *testing.T) {
	s, st := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'")
	s.cfg.WaveMaxRetries = 2
	sessionID := seedExecutableWitnessSession(t, st)
	writeRealWitness(t, s, `CREST_OBSERVATION {"peak":0,"clipped":false}`)

	recommitUnchanged := func() {
		t.Helper()
		require.NoError(t, st.UpdateSessionResourceState(
			sessionID, witnessRepairResourceID, string(StateCommitted), "", "", 0, "",
		))
		require.NoError(t, st.UpdateSessionResourcePhase(
			sessionID, witnessRepairResourceID, "verified", 0,
		))
	}

	first, err := s.VerifyWitness(context.Background(), "witness.signal", "", sessionID)
	require.NoError(t, err)
	require.Equal(t, []string{witnessRepairResourceID}, first.ReopenedResources)
	require.Equal(t, 1, first.RepairEpochs[witnessRepairResourceID].RetryCount)
	require.Equal(t, 2, first.RepairEpochs[witnessRepairResourceID].MaxRetries)

	recommitUnchanged()
	second, err := s.VerifyWitness(context.Background(), "witness.signal", "", sessionID)
	require.NoError(t, err)
	require.Equal(t, []string{witnessRepairResourceID}, second.ReopenedResources)
	require.Equal(t, 2, second.RepairEpochs[witnessRepairResourceID].RetryCount)

	recommitUnchanged()
	exhausted, err := s.VerifyWitness(context.Background(), "witness.signal", "", sessionID)
	require.NoError(t, err)
	require.Empty(t, exhausted.ReopenedResources)
	require.Equal(t, []string{witnessRepairResourceID}, exhausted.ExhaustedResources)
	require.Equal(t, 3, exhausted.RepairEpochs[witnessRepairResourceID].RetryCount)
	require.Equal(t, "exhausted", exhausted.RepairEpochs[witnessRepairResourceID].Disposition)

	owner, err := st.GetSessionResource(sessionID, witnessRepairResourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateErrored), owner.State)
	require.Contains(t, owner.LastError, "behavioral-witness repair retries exhausted")
	require.ErrorContains(t,
		s.enforceBehavioralWitnessRepairBudget(context.Background(), sessionID, witnessRepairResourceID),
		"exhausted behavioral-witness repair retries",
		"an exhausted epoch must be visible but cannot consume another worker",
	)

	next, err := s.Next(context.Background(), sessionID)
	require.NoError(t, err)
	require.Len(t, next.Resources, 1)
	require.Equal(t, witnessRepairResourceID, next.Resources[0].ResourceID)
	require.Equal(t, StateErrored, next.Resources[0].State)
}

func TestWitnessTheaterAndMalformedDoNotReopenRepairResources(t *testing.T) {
	for _, test := range []struct {
		name     string
		negative string
		class    string
	}{
		{
			name:     "theater",
			negative: "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0.7,\"clipped\":false}'",
			class:    "theater",
		},
		{
			name:     "malformed",
			negative: "printf '%s\\n' 'not-an-observation'",
			class:    "malformed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, st := newExecutableWitnessSpec(t, test.negative)
			sessionID := seedExecutableWitnessSession(t, st)

			result, err := s.VerifyWitness(context.Background(), "witness.signal", "", sessionID)
			require.NoError(t, err)
			require.Equal(t, test.class, result.Classification)
			require.Empty(t, result.ReopenedResources)

			owner, err := st.GetSessionResource(sessionID, witnessRepairResourceID)
			require.NoError(t, err)
			require.Equal(t, string(StateCommitted), owner.State)
			require.Equal(t, "verified", owner.Phase)
			require.Equal(t, 2, owner.Attempts)
		})
	}
}

func TestFailedWitnessWithoutSessionRemainsEvidenceOnly(t *testing.T) {
	s, st := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'")
	writeRealWitness(t, s, `CREST_OBSERVATION {"peak":0,"clipped":false}`)

	result, err := s.VerifyWitness(context.Background(), "witness.signal", "", "")
	require.NoError(t, err)
	require.Equal(t, "failed", result.Classification)
	require.Empty(t, result.ReopenedResources)

	run, err := st.GetValidationRun(context.Background(), result.ValidationRunID)
	require.NoError(t, err)
	require.Equal(t, "failed", run.Classification)
	require.Empty(t, run.SessionID)
	require.Len(t, run.Evidence, 1)
	require.Equal(t, "current", run.Evidence[0].Currency)
}

func TestVerifyWitnessAcceptsFalsifyingNegativeObservationFromExpectedNonzeroExit(t *testing.T) {
	s, st := newExecutableWitnessSpecWithExpectedNegativeExit(t,
		"printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'; exit 1", 1)

	result, err := s.VerifyWitness(context.Background(), "witness.signal", "", "")
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, "passed", result.Classification)
	assert.Equal(t, "behavior exhibited; check rejects the stub", result.Reason)

	run, err := st.GetValidationRun(context.Background(), result.ValidationRunID)
	require.NoError(t, err)
	require.Len(t, run.Executions, 2)
	require.Len(t, run.PredicateResults, 4)
	var negative *store.ValidationExecution
	for index := range run.Executions {
		if run.Executions[index].Role == "negative" {
			negative = &run.Executions[index]
			break
		}
	}
	require.NotNil(t, negative)
	assert.Equal(t, 1, negative.ExitCode)
	require.NotNil(t, negative.Observation)
	assert.Empty(t, negative.Observation.ParseError)
	assert.JSONEq(t, `{"peak":0,"clipped":false}`, negative.Observation.ObservationJSON)
}

func TestVerifyWitnessExpectedNegativeExitFailsClosed(t *testing.T) {
	t.Run("unexpected status", func(t *testing.T) {
		s, _ := newExecutableWitnessSpecWithExpectedNegativeExit(t,
			"printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'; exit 2", 1)
		result, err := s.VerifyWitness(context.Background(), "witness.signal", "", "")
		require.NoError(t, err)
		assert.False(t, result.Passed)
		assert.Equal(t, "failed", result.Classification)
		assert.Contains(t, result.Reason, "negative command exited 2, expected 1")
	})

	t.Run("missing structured observation", func(t *testing.T) {
		s, _ := newExecutableWitnessSpecWithExpectedNegativeExit(t, "printf '%s\\n' 'not-an-observation'; exit 1", 1)
		result, err := s.VerifyWitness(context.Background(), "witness.signal", "", "")
		require.NoError(t, err)
		assert.False(t, result.Passed)
		assert.True(t, result.Malformed)
		assert.Contains(t, result.Reason, "expected exactly one")
	})

	t.Run("negative still exhibits behavior", func(t *testing.T) {
		s, _ := newExecutableWitnessSpecWithExpectedNegativeExit(t,
			"printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0.7,\"clipped\":false}'; exit 1", 1)
		result, err := s.VerifyWitness(context.Background(), "witness.signal", "", "")
		require.NoError(t, err)
		assert.False(t, result.Passed)
		assert.True(t, result.Theater)
		assert.Equal(t, "theater", result.Classification)
	})
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
