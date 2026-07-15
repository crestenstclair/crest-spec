package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
)

func curatedEvaluationCase() CuratedEvaluationCase {
	return CuratedEvaluationCase{
		ProjectName: "synth", GoalID: "goal.play", CapabilityID: "capability.render",
		ResourceID: "aggregate.Engine.Voice", SpecHash: "spec-hash", RepositoryHash: "tree-hash",
		ResourceDeclarationHash: "declaration-hash", PlanOperationID: "modify:aggregate.Engine.Voice",
		Payload: map[string]any{
			"project": map[string]any{"mission": "play a note"},
			"context": map[string]any{"manifest_id": "context-one"},
		},
		ExpectedOutcome: map[string]any{"accepted": true, "retries": 1},
	}
}

func concurrentEvaluationStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(filepath.Join(t.TempDir(), "evaluations.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	return st
}

func TestEvaluationCaseCreationConvergesUnderConcurrency(t *testing.T) {
	st := concurrentEvaluationStore(t)
	ctx := context.Background()
	const workers = 8

	start := make(chan struct{})
	results := make(chan *EvaluationCase, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			created, err := st.CreateCuratedEvaluationCase(ctx, curatedEvaluationCase())
			results <- created
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	var identityID string
	for created := range results {
		require.NotNil(t, created)
		if identityID == "" {
			identityID = created.ID
		}
		assert.Equal(t, identityID, created.ID)
	}
	cases, err := st.ListEvaluationCases(ctx, workers)
	require.NoError(t, err)
	assert.Len(t, cases, 1)
}

func TestEvaluationCasesAreStableAndDatasetsSealImmutably(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	before, err := st.CountContentBlobs(ctx)
	require.NoError(t, err)

	first, err := st.CreateCuratedEvaluationCase(ctx, curatedEvaluationCase())
	require.NoError(t, err)
	second, err := st.CreateCuratedEvaluationCase(ctx, curatedEvaluationCase())
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, first.IdentityHash, second.IdentityHash)
	after, err := st.CountContentBlobs(ctx)
	require.NoError(t, err)
	assert.Equal(t, before+2, after, "case payload and expected outcome reuse content-addressed blobs")

	dataset, err := st.CreateEvaluationDataset(ctx, "voice cases", "voice implementation history")
	require.NoError(t, err)
	require.NoError(t, st.AddEvaluationDatasetCase(ctx, dataset.ID, first.ID, "held_out"))
	sealed, err := st.SealEvaluationDataset(ctx, dataset.ID)
	require.NoError(t, err)
	assert.Equal(t, "sealed", sealed.Status)
	assert.NotEmpty(t, sealed.IdentityHash)
	require.Len(t, sealed.Cases, 1)
	assert.Equal(t, "held_out", sealed.Cases[0].Split)
	require.Error(t, st.AddEvaluationDatasetCase(ctx, dataset.ID, first.ID, "training"))

	resealed, err := st.SealEvaluationDataset(ctx, dataset.ID)
	require.NoError(t, err)
	assert.Equal(t, sealed.IdentityHash, resealed.IdentityHash)
}

func TestEvaluationDatasetMembershipUsesAtomicOrdinals(t *testing.T) {
	st := concurrentEvaluationStore(t)
	ctx := context.Background()
	const workers = 8

	dataset, err := st.CreateEvaluationDataset(ctx, "concurrent cases", "membership ordering")
	require.NoError(t, err)
	cases := make([]*EvaluationCase, 0, workers)
	for index := range workers {
		input := curatedEvaluationCase()
		input.ResourceID = fmt.Sprintf("aggregate.Engine.Voice.%d", index)
		created, createErr := st.CreateCuratedEvaluationCase(ctx, input)
		require.NoError(t, createErr)
		cases = append(cases, created)
	}

	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for _, evaluationCase := range cases {
		wg.Add(1)
		go func(caseID string) {
			defer wg.Done()
			<-start
			errs <- st.AddEvaluationDatasetCase(ctx, dataset.ID, caseID, "held_out")
		}(evaluationCase.ID)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	populated, err := st.GetEvaluationDataset(ctx, dataset.ID)
	require.NoError(t, err)
	require.Len(t, populated.Cases, workers)
	for index, member := range populated.Cases {
		assert.Equal(t, index, member.Ordinal)
	}
}

func TestEvaluationDatasetSealIncludesEverySuccessfulConcurrentAddition(t *testing.T) {
	st := concurrentEvaluationStore(t)
	ctx := context.Background()
	base, err := st.CreateCuratedEvaluationCase(ctx, curatedEvaluationCase())
	require.NoError(t, err)
	extraInput := curatedEvaluationCase()
	extraInput.ResourceID = "aggregate.Engine.ConcurrentVoice"
	extra, err := st.CreateCuratedEvaluationCase(ctx, extraInput)
	require.NoError(t, err)

	for iteration := range 12 {
		dataset, createErr := st.CreateEvaluationDataset(
			ctx,
			fmt.Sprintf("seal race %d", iteration),
			"successful additions must be included in the sealed identity",
		)
		require.NoError(t, createErr)
		require.NoError(t, st.AddEvaluationDatasetCase(ctx, dataset.ID, base.ID, "held_out"))

		start := make(chan struct{})
		addResult := make(chan error, 1)
		sealResult := make(chan error, 1)
		go func() {
			<-start
			addResult <- st.AddEvaluationDatasetCase(ctx, dataset.ID, extra.ID, "held_out")
		}()
		go func() {
			<-start
			_, sealErr := st.SealEvaluationDataset(ctx, dataset.ID)
			sealResult <- sealErr
		}()
		close(start)

		addErr := <-addResult
		require.NoError(t, <-sealResult)
		sealed, getErr := st.GetEvaluationDataset(ctx, dataset.ID)
		require.NoError(t, getErr)
		assert.Equal(t, "sealed", sealed.Status)
		if addErr == nil {
			assert.Len(t, sealed.Cases, 2)
		} else {
			assert.ErrorContains(t, addErr, "dataset is sealed")
			assert.Len(t, sealed.Cases, 1)
		}
	}
}

func baseEvaluationConfiguration() EvaluationConfiguration {
	return EvaluationConfiguration{
		Name: "candidate selector", PlannerVersion: "planner-v2",
		PlannerPolicy:          map[string]any{"vertical_slices": true},
		ContextSelectorVersion: "selector-v2", ContextBudgetTokens: 32000,
		TemplateHashes:    map[string]string{"task": "task-hash", "system": "system-hash"},
		RolePolicyVersion: "role-policy-v1", HostName: "test-host", HostVersion: "1.0",
		Provider: "provider", Model: "model", ValidationVersion: "validation-v1",
		InferenceConfig: map[string]any{"temperature": 0.2, "api_key": "top-secret"},
		LearningIDs:     []string{"learning-b", "learning-a", "learning-a"},
	}
}

func TestEvaluationConfigurationIdentityIsRedactedAndOrderIndependent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	input := baseEvaluationConfiguration()
	first, err := st.CreateEvaluationConfiguration(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, []string{"learning-a", "learning-b"}, first.LearningIDs)
	assert.Equal(t, "[REDACTED]", first.InferenceConfig["api_key"])

	input.Name = "same governed configuration, different label"
	input.InferenceConfig["api_key"] = "different-secret"
	input.TemplateHashes = map[string]string{"system": "system-hash", "task": "task-hash"}
	second, err := st.CreateEvaluationConfiguration(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, first.IdentityHash, second.IdentityHash)
}

func TestEvaluationConfigurationCreationConvergesUnderConcurrency(t *testing.T) {
	st := concurrentEvaluationStore(t)
	ctx := context.Background()
	const workers = 8

	start := make(chan struct{})
	results := make(chan *EvaluationConfiguration, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			created, err := st.CreateEvaluationConfiguration(ctx, baseEvaluationConfiguration())
			results <- created
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	var identityID string
	for created := range results {
		require.NotNil(t, created)
		if identityID == "" {
			identityID = created.ID
		}
		assert.Equal(t, identityID, created.ID)
	}
	configurations, err := st.ListEvaluationConfigurations(ctx, workers)
	require.NoError(t, err)
	assert.Len(t, configurations, 1)
}

func TestHistoricalEvaluationCaseRecordsIneligibility(t *testing.T) {
	st := newContextManifestStore(t, "resource.ineligible")
	ctx := context.Background()
	manifest := testContextManifest("attempt-ineligible", "manifest-ineligible", "resource.ineligible")
	manifest.Blocked = true
	manifest.BlockedReason = "mandatory context exceeds budget"
	manifest.Sections[0].Decision = "omitted"
	manifest.Sections[0].Content = ""
	manifest.Sections[0].Reason = manifest.BlockedReason
	manifest.Sections[0].SelectedBytes = 0
	manifest.Sections[0].EstimatedTokens = 0
	manifest.EstimatedTokens = 0
	manifest.SelectedBytes = 0
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{Manifest: manifest})
	require.NoError(t, err)

	// The immutable chain is incomplete, but the assessment remains canonical
	// SQLite state rather than a discarded diagnostic log.
	assessment, err := st.CreateHistoricalEvaluationCase(ctx, "attempt-ineligible", "synth")
	require.NoError(t, err)
	assert.False(t, assessment.Eligible)
	assert.Contains(t, assessment.Reason, "context manifest")
}

func TestHistoricalEvaluationCaseReferencesCompleteAttemptChain(t *testing.T) {
	st := newContextManifestStore(t, "resource.synth")
	ctx := context.Background()
	require.NoError(t, st.ReconcileProjectIntent(ctx, ProjectIntentSnapshot{
		ProjectName: "synth", Mission: "make sound", SpecHash: "spec-hash",
		Goals:         []IntentGoal{{ID: "goal.play", Description: "play", Priority: "required"}},
		Capabilities:  []IntentCapability{{ID: "capability.play", Description: "render", Goals: []string{"goal.play"}}},
		RequiredGoals: []string{"goal.play"},
		Verification: []IntentVerificationDefinition{{
			SnapshotID: "verification.local", DefinitionID: "validation.local", DefinitionType: "validation",
			Scope: "resource", Kind: "test", DefinitionHash: "definition-hash", DefinitionJSON: `{}`,
			Targets: []IntentVerificationTarget{{Kind: "resource", ID: "resource.synth"}},
		}},
	}))
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-1", "manifest-1", "resource.synth"), Dispatch: true,
	})
	require.NoError(t, err)
	executionInput := testExecutionManifest()
	executionInput.GoalIDs = []string{"goal.play"}
	executionInput.CapabilityIDs = []string{"capability.play"}
	started, err := st.StartExecution(ctx, executionInput)
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "synth.go", Content: "package synth\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{ExecutionID: started.ID, ToStatus: execution.StatusValidating})
	require.NoError(t, err)
	_, err = st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: started.ID, Accepted: true, Reason: "passed",
		Resource: &Resource{ID: "resource.synth", Kind: "asset", DeclarationHash: "decl", EffectiveHash: "effective", Model: "model"},
		Files: []GeneratedFile{{
			Path: "synth.go", ResourceID: "resource.synth", ContentHash: candidate.Files[0].ContentHash,
			PromptHash: started.ContextHash, Model: "model",
		}},
		Attempts: 1, GoalProgress: map[string]any{"goal.play": "advanced"},
	})
	require.NoError(t, err)
	startedAt := time.Now().UTC().Add(-time.Second)
	_, err = st.RecordValidationRun(ctx, ValidationRunWrite{
		ProjectName: "synth",
		Run: ValidationRun{
			DefinitionID: "validation.local", AttemptID: "attempt-1", ExecutionID: started.ID,
			SourceTreeHash: "tree-hash", Classification: "passed", Reason: "tests passed",
			StartedAt: startedAt, CompletedAt: startedAt.Add(time.Millisecond), DurationMS: 1,
		},
	})
	require.NoError(t, err)

	assessment, err := st.CreateHistoricalEvaluationCase(ctx, "attempt-1", "synth")
	require.NoError(t, err)
	require.True(t, assessment.Eligible)
	require.NotNil(t, assessment.Case)
	assert.Equal(t, "historical", assessment.Case.Provenance)
	assert.Equal(t, "manifest-1", assessment.Case.ContextManifestID)
	assert.Equal(t, started.ID, assessment.Case.ExecutionID)
	assert.Equal(t, candidate.ID, assessment.Case.CandidateID)
	assert.Equal(t, "tree-hash", assessment.Case.RepositoryHash)
	assert.Equal(t, "goal.play", assessment.Case.GoalID)
	assert.NotEmpty(t, assessment.Case.ResourceDeclarationHash)

	replayed, err := st.CreateHistoricalEvaluationCase(ctx, "attempt-1", "synth")
	require.NoError(t, err)
	assert.Equal(t, assessment.Case.ID, replayed.Case.ID)
}
