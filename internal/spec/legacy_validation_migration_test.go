package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/execution"
	graphpkg "github.com/crestenstclair/crest-spec/internal/graph"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// This is the exact durable shape left by the legacy invalid-validation loop:
// an accepted candidate and resolved blocking classification have no handoff,
// and a later host dispatch died before execution registration. Recovery must
// select the older accepted candidate, prove the newer attempt was harmless,
// and route directly back to the closure gate after a process restart.
func TestPersistentSessionReinstatesHandofflessCandidatePastAbandonedNewerContext(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"), []byte(withTestIntent(commitTestSpecCUE)), 0o644,
	))
	statePath := filepath.Join(root, "state.db")
	open := func() (*Spec, *store.Store) {
		st, err := store.New(statePath)
		require.NoError(t, err)
		return New(st, OSFileSystem{}, &config.Config{
			SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3,
			TypeCheckCommand: "true",
		}), st
	}

	initial, initialStore := open()
	begin, err := initial.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.Len(t, begin.Plan, 1)
	resourceID := begin.Plan[0].ResourceID
	accepted, err := commitTestAttempt(t, initial, ctx, begin.SessionID, resourceID, []CommitFile{{
		Path: "generated/tone.go", Content: "package generated\n",
	}}, "", "test-model")
	require.NoError(t, err)

	_, err = initialStore.ReopenSessionResources(
		ctx, begin.SessionID, []string{resourceID}, "legacy affected-closure validation could not run",
	)
	require.NoError(t, err)
	manifest, err := initialStore.GetExecutionManifestByAttempt(ctx, accepted.AttemptID)
	require.NoError(t, err)
	failure, err := initialStore.CreateFailureClassification(ctx, store.FailureClassificationWrite{
		AttemptID: accepted.AttemptID, ExecutionID: manifest.ID,
		Category: execution.FailureInvalidValidation, Origin: "engine", Confidence: 1,
		EvidenceSource: "affected_closure_validation", EvidenceReference: begin.SessionID,
		Evidence: "cargo was absent from the validator process PATH",
	})
	require.NoError(t, err)
	require.True(t, failure.BlocksRetry)
	handoffs, err := initialStore.ListAttemptHandoffsBySource(ctx, accepted.AttemptID)
	require.NoError(t, err)
	require.Empty(t, handoffs, "invalid_validation deliberately has no repair handoff")

	// Before triage resolves the validation failure, the old scheduler creates
	// another context. The host then reports that dispatch failed before the
	// worker could register execution.
	orphan, err := initial.PrepareContext(ctx, ContextOptions{
		SessionID: begin.SessionID, ResourceID: resourceID,
	})
	require.NoError(t, err)
	const hostID = "legacy-host"
	const workerID = "legacy-worker"
	_, err = initial.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: orphan.DispatchID,
		SessionID: begin.SessionID, ResourceID: resourceID,
		HostSessionID: hostID, WorkerID: workerID,
	})
	require.NoError(t, err)
	_, err = initial.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_failed", DispatchID: orphan.DispatchID,
		SessionID: begin.SessionID, ResourceID: resourceID,
		HostSessionID: hostID, WorkerID: workerID, Reason: "host exited before worker acknowledgement",
	})
	require.NoError(t, err)
	recoveredProjection, err := initialStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", recoveredProjection.State)
	require.Equal(t, "pre_execution_recovered", recoveredProjection.Phase)
	require.NotEmpty(t, recoveredProjection.LastError)
	require.NoError(t, initial.ResolveFailure(
		ctx, failure.ID, "triage proved the accepted source was not responsible for the toolchain failure", "",
	))

	attemptsBefore, err := initialStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, attemptsBefore, 2)
	require.NoError(t, initialStore.Close())

	restarted, restartedStore := open()
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	next, err := restarted.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.True(t, next.ValidationReady)
	require.Empty(t, next.Resources, "reconciliation must not dispatch a third generation attempt")

	reinstated, err := restartedStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, "committed", reinstated.State)
	require.Equal(t, "integration_pending", reinstated.Phase)
	require.Empty(t, reinstated.LastError)
	require.Empty(t, reinstated.JobID)
	attemptsAfter, err := restartedStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, len(attemptsBefore))
	orphanManifest, err := restartedStore.GetContextManifestByAttempt(ctx, orphan.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "abandoned", orphanManifest.Attempt.Status)
	restoredCandidate, err := restartedStore.GetCandidateSetByAttempt(ctx, accepted.AttemptID)
	require.NoError(t, err)
	require.Equal(t, accepted.CandidateID, restoredCandidate.ID)
	require.Equal(t, "accepted", restoredCandidate.Status)
	session, err := restartedStore.GetSession(begin.SessionID)
	require.NoError(t, err)
	actions, err := restartedStore.ListApplyActions(session.ApplyID)
	require.NoError(t, err)
	require.Equal(t, "reinstate_candidate", actions[len(actions)-1].Action)
	require.Equal(t, "reinstated", actions[len(actions)-1].Outcome)
}

// A dependency-only consumer such as CargoManifest never had a candidate. The
// old reopen loop nevertheless rewrote its projection to generate. The
// immutable plan is sufficient to restore validate_existing when its operation
// hashes still match, and closure validation—not a model—is the next action.
func TestPersistentSessionRestoresPoisonedValidateExistingOperationWithoutAttempt(t *testing.T) {
	const specCUE = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "represents the valid result under test"}]
	validations: [{kind: "custom", command: ["true"]}]
}
project: invariants: [{text: "no global state"}]
`
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"), []byte(withTestIntent(specCUE)), 0o644,
	))
	statePath := filepath.Join(root, "state.db")
	open := func() (*Spec, *store.Store) {
		st, err := store.New(statePath)
		require.NoError(t, err)
		return New(st, OSFileSystem{}, &config.Config{
			SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3,
		}), st
	}

	initial, initialStore := open()
	planned, err := initial.Plan(ctx)
	require.NoError(t, err)
	const resourceID = "valueObject.Audio.Tone"
	resource := planned.Registry.Resources[resourceID]
	declarationJSON, err := json.Marshal(resource.Declaration)
	require.NoError(t, err)
	declarationHash := fmt.Sprintf("%x", sha256.Sum256(declarationJSON))
	legacyGenerationHash := graphpkg.ComputeLegacyGenerationInputHashesV1(planned.Registry.Resources)[resourceID]
	generatedPath := "tone.go"
	generatedContent := "package audio\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, generatedPath), []byte(generatedContent), 0o644))
	require.NoError(t, initialStore.SetResource(store.Resource{
		ID: resourceID, Kind: resource.Kind, DeclarationHash: declarationHash,
		EffectiveHash: "legacy-settled", GenerationInputHash: legacyGenerationHash,
		ExportedContractHash:   planned.ExportedContractHashes[resourceID],
		DependencyContractHash: planned.DependencyContractHashes[resourceID], Model: "old-model",
	}))
	require.NoError(t, initialStore.SetGeneratedFile(store.GeneratedFile{
		Path: generatedPath, ResourceID: resourceID, ContentHash: contextmanifest.Hash(generatedContent),
	}))

	migrationPlan, err := initial.Plan(ctx)
	require.NoError(t, err)
	require.Len(t, migrationPlan.Actions, 1)
	require.Equal(t, planpkg.ExecutionStrategyValidateExisting, migrationPlan.Actions[0].ExecutionStrategy)
	begin, err := initial.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	poisoned, err := initialStore.ReopenSessionResources(
		ctx, begin.SessionID, []string{resourceID}, "legacy validation environment failure",
	)
	require.NoError(t, err)
	require.Equal(t, 1, poisoned)
	require.NoError(t, initialStore.UpdateSessionResourceState(
		begin.SessionID, resourceID, "pending", "", "", 0, "",
	))
	before, err := initialStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, planpkg.ExecutionStrategyGenerate, before.ExecutionStrategy)
	require.Zero(t, before.Attempts)
	attemptsBefore, err := initialStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Empty(t, attemptsBefore)
	require.NoError(t, initialStore.Close())

	restarted, restartedStore := open()
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	contextResult, err := restarted.Context(ctx, begin.SessionID, resourceID)
	require.NoError(t, err)
	require.True(t, contextResult.NoDispatch)
	require.Empty(t, contextResult.AttemptID)
	require.Empty(t, contextResult.ReinstatedCandidateID)
	require.Contains(t, contextResult.Instructions, "closure validation")
	restored, err := restartedStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, "committed", restored.State)
	require.Equal(t, "integration_pending", restored.Phase)
	require.Equal(t, planpkg.ExecutionStrategyValidateExisting, restored.ExecutionStrategy)
	require.Zero(t, restored.Attempts)
	session, err := restartedStore.GetSession(begin.SessionID)
	require.NoError(t, err)
	actions, err := restartedStore.ListApplyActions(session.ApplyID)
	require.NoError(t, err)
	require.Equal(t, "restore_validate_existing", actions[len(actions)-1].Action)
	require.Equal(t, "restored", actions[len(actions)-1].Outcome)

	next, err := restarted.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.True(t, next.ValidationReady)
	require.Empty(t, next.Resources)
	verified := restarted.VerifyWave(ctx, begin.SessionID, next.ValidationWaveIndex)
	require.True(t, verified.Passed)
	require.True(t, verified.IntegrationValidated)
	afterAttempts, err := restartedStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Empty(t, afterAttempts)
}

func TestPersistentSessionReinstatesCandidateWhoseSharedFileWasSupersededByLaterAcceptedCandidate(t *testing.T) {
	const specCUE = `package crestsynth

project: name: "rt"
project: meta: language: "rust"
project: contexts: Testing: purpose: "testing"
project: contexts: Testing: valueObjects: AutomaticMidiTest: {
	from: "events"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "drives automated MIDI"}]
}
project: contexts: Testing: valueObjects: ExhaustiveGuiDemo: {
	from: "events"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "drives the GUI demo"}]
}
project: invariants: [{text: "no global state"}]
`
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"), []byte(withTestIntent(specCUE)), 0o644,
	))
	statePath := filepath.Join(root, "state.db")
	open := func() (*Spec, *store.Store) {
		st, err := store.New(statePath)
		require.NoError(t, err)
		return New(st, OSFileSystem{}, &config.Config{
			SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3, TypeCheckCommand: "true",
		}), st
	}

	initial, initialStore := open()
	begin, err := initial.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	const sourceID = "valueObject.Testing.AutomaticMidiTest"
	const laterID = "valueObject.Testing.ExhaustiveGuiDemo"
	const sharedPath = "src/testing/mod.rs"
	const sourceShared = "pub mod automatic_midi_test;\n"
	const laterShared = "pub mod automatic_midi_test;\npub mod exhaustive_gui_demo;\n"
	source, err := commitTestAttempt(t, initial, ctx, begin.SessionID, sourceID, []CommitFile{
		{Path: "src/testing/automatic_midi_test.rs", Content: "pub fn automatic_midi_test() {}\n"},
		{Path: sharedPath, Content: sourceShared},
	}, "", "test-model")
	require.NoError(t, err)
	_, err = commitTestAttempt(t, initial, ctx, begin.SessionID, laterID, []CommitFile{
		{Path: "src/testing/exhaustive_gui_demo.rs", Content: "pub fn exhaustive_gui_demo() {}\n"},
		{Path: sharedPath, Content: laterShared},
	}, "", "test-model")
	require.NoError(t, err)

	sourceCandidate, err := initialStore.GetCandidateSetByAttempt(ctx, source.AttemptID)
	require.NoError(t, err)
	strictMatch, err := initial.candidateMatchesOwnedWorkspace(sourceID, sourceCandidate)
	require.NoError(t, err)
	require.False(t, strictMatch, "the legacy exact-byte check must reproduce the shared-file migration miss")
	currentShared, err := os.ReadFile(filepath.Join(root, sharedPath))
	require.NoError(t, err)
	require.Equal(t, laterShared, string(currentShared))

	poisonAcceptedCandidateForMigration(
		t, ctx, initial, initialStore, begin.SessionID, sourceID, source.AttemptID,
	)
	attemptsBefore, err := initialStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.NoError(t, initialStore.Close())

	restarted, restartedStore := open()
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	next, err := restarted.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.True(t, next.ValidationReady)
	require.Empty(t, next.Resources)
	reinstated, err := restartedStore.GetSessionResource(begin.SessionID, sourceID)
	require.NoError(t, err)
	require.Equal(t, "committed", reinstated.State)
	require.Equal(t, "integration_pending", reinstated.Phase)
	attemptsAfter, err := restartedStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, len(attemptsBefore))
	currentShared, err = os.ReadFile(filepath.Join(root, sharedPath))
	require.NoError(t, err)
	require.Equal(t, laterShared, string(currentShared), "reconciliation is read-only")
}

func TestPersistentSessionReinstatesCandidateWithExtraLegacyGeneratedFileOwnership(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"), []byte(withTestIntent(commitTestSpecCUE)), 0o644,
	))
	statePath := filepath.Join(root, "state.db")
	open := func() (*Spec, *store.Store) {
		st, err := store.New(statePath)
		require.NoError(t, err)
		return New(st, OSFileSystem{}, &config.Config{
			SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3, TypeCheckCommand: "true",
		}), st
	}

	initial, initialStore := open()
	begin, err := initial.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.Len(t, begin.Plan, 1)
	resourceID := begin.Plan[0].ResourceID
	accepted, err := commitTestAttempt(t, initial, ctx, begin.SessionID, resourceID, []CommitFile{{
		Path: "src/shell/standalone_application.rs", Content: "pub fn standalone_application() {}\n",
	}}, "", "test-model")
	require.NoError(t, err)
	const legacyPath = "Cargo.toml"
	const legacyContent = "[package]\nname = \"legacy\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, legacyPath), []byte(legacyContent), 0o644))
	require.NoError(t, initialStore.SetGeneratedFile(store.GeneratedFile{
		Path: legacyPath, ResourceID: resourceID, ContentHash: contextmanifest.Hash(legacyContent),
		PromptHash: "legacy-prompt", Model: "legacy-model",
	}))
	candidate, err := initialStore.GetCandidateSetByAttempt(ctx, accepted.AttemptID)
	require.NoError(t, err)
	strictMatch, err := initial.candidateMatchesOwnedWorkspace(resourceID, candidate)
	require.NoError(t, err)
	require.False(t, strictMatch, "the legacy count-equality check must reproduce the extra-ownership migration miss")

	poisonAcceptedCandidateForMigration(
		t, ctx, initial, initialStore, begin.SessionID, resourceID, accepted.AttemptID,
	)
	attemptsBefore, err := initialStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.NoError(t, initialStore.Close())

	restarted, restartedStore := open()
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	next, err := restarted.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.True(t, next.ValidationReady)
	require.Empty(t, next.Resources)
	reinstated, err := restartedStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, "committed", reinstated.State)
	require.Equal(t, "integration_pending", reinstated.Phase)
	attemptsAfter, err := restartedStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, len(attemptsBefore))
	legacyBytes, err := os.ReadFile(filepath.Join(root, legacyPath))
	require.NoError(t, err)
	require.Equal(t, legacyContent, string(legacyBytes), "reconciliation must not rewrite legacy ownership files")
}

func poisonAcceptedCandidateForMigration(
	t *testing.T,
	ctx context.Context,
	s *Spec,
	st *store.Store,
	sessionID string,
	resourceID string,
	attemptID string,
) {
	t.Helper()
	_, err := st.ReopenSessionResources(
		ctx, sessionID, []string{resourceID}, "legacy affected-closure validation could not run",
	)
	require.NoError(t, err)
	manifest, err := st.GetExecutionManifestByAttempt(ctx, attemptID)
	require.NoError(t, err)
	failure, err := st.CreateFailureClassification(ctx, store.FailureClassificationWrite{
		AttemptID: attemptID, ExecutionID: manifest.ID,
		Category: execution.FailureInvalidValidation, Origin: "engine", Confidence: 1,
		EvidenceSource: "affected_closure_validation", EvidenceReference: sessionID,
		Evidence: "validator environment could not execute cargo",
	})
	require.NoError(t, err)
	require.NoError(t, s.ResolveFailure(
		ctx, failure.ID, "triage proved accepted source was not responsible for validation infrastructure", "",
	))
	resource, err := st.GetSessionResource(sessionID, resourceID)
	require.NoError(t, err)
	require.NoError(t, st.UpdateSessionResourceState(
		sessionID, resourceID, "pending", "", "", resource.Attempts, "",
	))
}

func TestClosureFailureProvenanceUsesAcceptedAttemptBeforeNewerAbandonedContext(t *testing.T) {
	const specCUE = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Control: purpose: "control"
project: contexts: Control: valueObjects: AppLoop: {
	from: "events"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "runs the app loop"}]
}
project: contexts: Control: valueObjects: UnrelatedWindow: {
	from: "events"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "renders an unrelated window"}]
}
project: invariants: [{text: "no global state"}]
`
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"), []byte(withTestIntent(specCUE)), 0o644,
	))
	st, err := store.New(filepath.Join(root, "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	s := New(st, OSFileSystem{}, &config.Config{
		SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3, TypeCheckCommand: "true",
	})
	begin, err := s.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	const sourceID = "valueObject.Control.AppLoop"
	const unrelatedID = "valueObject.Control.UnrelatedWindow"
	const sourcePath = "src/control/app_loop.rs"
	accepted, err := commitTestAttempt(t, s, ctx, begin.SessionID, sourceID, []CommitFile{{
		Path: sourcePath, Content: "pub fn app_loop() {}\n",
	}}, "", "test-model")
	require.NoError(t, err)
	_, err = commitTestAttempt(t, s, ctx, begin.SessionID, unrelatedID, []CommitFile{{
		Path: "src/control/unrelated_window.rs", Content: "pub fn unrelated_window() {}\n",
	}}, "", "test-model")
	require.NoError(t, err)

	// Reproduce the old invalid-validation repair followed by a host dispatch
	// that never reached execution registration.
	_, err = st.ReopenSessionResources(
		ctx, begin.SessionID, []string{sourceID}, "legacy validation infrastructure failure",
	)
	require.NoError(t, err)
	acceptedExecution, err := st.GetExecutionManifestByAttempt(ctx, accepted.AttemptID)
	require.NoError(t, err)
	invalidFailure, err := st.CreateFailureClassification(ctx, store.FailureClassificationWrite{
		AttemptID: accepted.AttemptID, ExecutionID: acceptedExecution.ID,
		Category: execution.FailureInvalidValidation, Origin: "engine", Confidence: 1,
		EvidenceSource: "affected_closure_validation", EvidenceReference: begin.SessionID,
		Evidence: "cargo was missing from the validation process",
	})
	require.NoError(t, err)
	orphan, err := s.PrepareContext(ctx, ContextOptions{SessionID: begin.SessionID, ResourceID: sourceID})
	require.NoError(t, err)
	_, err = s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: orphan.DispatchID,
		SessionID: begin.SessionID, ResourceID: sourceID,
		HostSessionID: "closure-host", WorkerID: "closure-worker",
	})
	require.NoError(t, err)
	_, err = s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_failed", DispatchID: orphan.DispatchID,
		SessionID: begin.SessionID, ResourceID: sourceID,
		HostSessionID: "closure-host", WorkerID: "closure-worker",
		Reason: "host exited before execution registration",
	})
	require.NoError(t, err)
	require.NoError(t, s.ResolveFailure(
		ctx, invalidFailure.ID, "validation environment was repaired without source changes", "",
	))
	next, err := s.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.True(t, next.ValidationReady)

	// The real closure failure is source-attributed to AppLoop only. Provenance
	// must attach it to the accepted attempt, not the newer abandoned context.
	s.cfg.TypeCheckCommand = "printf 'error: app loop mismatch\\n --> " + sourcePath + ":1:1\\n' >&2; exit 1"
	verified := s.VerifyWave(ctx, begin.SessionID, next.ValidationWaveIndex)
	require.False(t, verified.Passed)
	require.Equal(t, []string{sourceID}, verified.ReopenedResources)
	for _, waveErr := range verified.Errors {
		require.NotEqual(t, "failure_provenance", waveErr.Kind, waveErr.Message)
		require.NotContains(t, waveErr.ResourceIDs, unrelatedID)
		require.NotEqual(t, unrelatedID, waveErr.ResourceID)
	}

	acceptedFailures, err := st.ListFailureClassificationsByAttempt(ctx, accepted.AttemptID)
	require.NoError(t, err)
	var closureFailure *store.FailureClassification
	for index := range acceptedFailures {
		if acceptedFailures[index].EvidenceSource == "affected_closure_validation" &&
			acceptedFailures[index].Category == string(execution.FailureIntegrationError) {
			closureFailure = &acceptedFailures[index]
			break
		}
	}
	require.NotNil(t, closureFailure, "real closure failure must use the newest accepted execution provenance")
	handoffs, err := st.ListAttemptHandoffsBySource(ctx, accepted.AttemptID)
	require.NoError(t, err)
	require.Condition(t, func() bool {
		for _, handoff := range handoffs {
			if handoff.FailureID == closureFailure.ID && handoff.Status == "pending" {
				return true
			}
		}
		return false
	}, "the source-attributed closure failure must create its governed repair handoff")
	orphanFailures, err := st.ListFailureClassificationsByAttempt(ctx, orphan.AttemptID)
	require.NoError(t, err)
	for _, failure := range orphanFailures {
		require.NotEqual(t, "affected_closure_validation", failure.EvidenceSource)
	}
	unrelated, err := st.GetSessionResource(begin.SessionID, unrelatedID)
	require.NoError(t, err)
	require.Equal(t, "committed", unrelated.State)
}
