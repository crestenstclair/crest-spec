package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
)

func TestVacuumRetainsBlobsReferencedByExecutionProvenance(t *testing.T) {
	st := newContextManifestStore(t, "resource.vacuum-execution")
	ctx := context.Background()
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-1", "manifest-1", "resource.vacuum-execution"), Dispatch: true,
	})
	require.NoError(t, err)
	executionInput := testExecutionManifest()
	started, err := st.StartExecution(ctx, executionInput)
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "synth.go", Content: "package synth\n", WriteIntent: "modify",
	}})
	require.NoError(t, err)

	_, err = st.Vacuum(time.Now().Add(time.Hour))
	require.NoError(t, err)
	manifest, err := st.GetContextManifest(ctx, "manifest-1")
	require.NoError(t, err)
	require.Equal(t, "attempt-1", manifest.Attempt.ID)
	gotExecution, err := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	require.Equal(t, "use Bearer [REDACTED]", gotExecution.SystemInstructions)
	gotCandidate, err := st.GetCandidateSetByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, candidate.ID, gotCandidate.ID)
	require.Equal(t, "package synth\n", gotCandidate.Files[0].Content)
}

func TestExecutionCandidateFailureAndHandoffRoundTrip(t *testing.T) {
	st := newContextManifestStore(t, "resource.synth")
	ctx := context.Background()
	require.NoError(t, st.ReconcileProjectIntent(ctx, ProjectIntentSnapshot{
		ProjectName: "synth", Mission: "make sound", SpecHash: "spec-hash",
		Goals:         []IntentGoal{{ID: "goal.play", Description: "play a note", Priority: "required"}},
		Capabilities:  []IntentCapability{{ID: "capability.play", Description: "render a note", Goals: []string{"goal.play"}}},
		RequiredGoals: []string{"goal.play"},
	}))
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-1", "manifest-1", "resource.synth"), Dispatch: true,
	})
	require.NoError(t, err)

	input := testExecutionManifest()
	input.GoalIDs = []string{"goal.play"}
	input.CapabilityIDs = []string{"capability.play"}
	started, err := st.StartExecution(ctx, input)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusExecuting), started.Status)
	require.Equal(t, "[REDACTED]", started.AgentConfig["Api_Key"])
	nested := started.InferenceConfig["nested"].(map[string]any)
	require.Equal(t, "[REDACTED]", nested["AUTHORIZATION"])
	require.Equal(t, "use Bearer [REDACTED]", started.SystemInstructions)
	require.Len(t, started.Tools, 2)
	require.Equal(t, "filesystem", started.Tools[0].Name)
	require.Equal(t, "execution_started", started.Events[0].Kind)
	require.Equal(t, []string{"goal.play"}, started.GoalIDs)
	require.Equal(t, []string{"capability.play"}, started.CapabilityIDs)

	// A network retry returns the original record, while reusing the key with
	// changed governed inputs fails closed.
	replayed, err := st.StartExecution(ctx, input)
	require.NoError(t, err)
	require.Equal(t, started.ID, replayed.ID)
	conflict := input
	conflict.Model = "different-model"
	_, err = st.StartExecution(ctx, conflict)
	require.ErrorContains(t, err, "already used for different governed inputs")

	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{
		{Path: "z_test.go", Content: "package synth\n", WriteIntent: "create"},
		{Path: "synth.go", Content: "package synth\nfunc Play() {}\n", WriteIntent: "modify"},
	})
	require.NoError(t, err)
	require.Equal(t, "submitted", candidate.Status)
	require.Equal(t, "synth.go", candidate.Files[0].Path)
	require.NotEmpty(t, candidate.CandidateHash)

	// Candidate submission is idempotent for the same immutable bytes.
	replayedCandidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{
		{Path: "synth.go", Content: "package synth\nfunc Play() {}\n", WriteIntent: "modify"},
		{Path: "z_test.go", Content: "package synth\n", WriteIntent: "create"},
	})
	require.NoError(t, err)
	require.Equal(t, candidate.ID, replayedCandidate.ID)

	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID, ToStatus: execution.StatusValidating,
		Details: map[string]any{"command": "go test ./..."},
	})
	require.NoError(t, err)
	finished, err := st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID, ToStatus: execution.StatusRejected,
		Reason: "validation failed", DurationMS: 1200, InputTokens: 120, OutputTokens: 80, CostUSD: 0.02,
		Failure: &FailureClassificationWrite{
			Category: execution.FailureImplementationError, Origin: "engine", Confidence: 1,
			EvidenceSource: "validation_run", EvidenceReference: "validation-1",
			Evidence: "Authorization: Bearer secret-value", GoalIDs: []string{"goal.play"},
		},
		HandoffTargetRole: "minimal_diff_repair",
	})
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusRejected), finished.Status)
	require.NotNil(t, finished.CompletedAt)
	require.Equal(t, int64(1200), finished.DurationMS)

	rejected, err := st.GetLatestRejectedCandidate(ctx, "session-1", "resource.synth")
	require.NoError(t, err)
	require.Equal(t, "rejected", rejected.Status)
	require.Equal(t, "package synth\nfunc Play() {}\n", rejected.Files[0].Content)
	owned, err := st.GetGeneratedFiles("resource.synth")
	require.NoError(t, err)
	require.Empty(t, owned, "rejected candidates must not become accepted generated-file ownership")

	failures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Len(t, failures, 1)
	require.Equal(t, "minimal_diff_repair", failures[0].NextRole)
	require.Equal(t, "Authorization: Bearer [REDACTED]", failures[0].Evidence)
	require.Equal(t, []string{"goal.play"}, failures[0].GoalIDs)
	handoffs, err := st.ListAttemptHandoffsBySource(ctx, "attempt-1")
	require.NoError(t, err)
	require.Len(t, handoffs, 1)
	require.Equal(t, "minimal_diff_repair", handoffs[0].TargetRole)
	require.Equal(t, failures[0].ID, handoffs[0].FailureID)

	retryManifest := testContextManifest("attempt-2", "manifest-2", "resource.synth")
	retryManifest.Attempt.Role = "minimal_diff_repair"
	retryManifest.Attempt.ParentAttemptID = "attempt-1"
	_, err = st.CreateContextManifest(ctx, ContextManifestWrite{Manifest: retryManifest, Dispatch: true})
	require.NoError(t, err)
	require.NoError(t, st.AcceptAttemptHandoff(ctx, handoffs[0].ID, "attempt-2"))
	handoffs, err = st.ListAttemptHandoffsBySource(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, "accepted", handoffs[0].Status)
	require.Equal(t, "attempt-2", handoffs[0].TargetAttemptID)

	retryExecution := testExecutionManifest()
	retryExecution.AttemptID = "attempt-2"
	retryExecution.ContextManifestID = "manifest-2"
	retryExecution.IdempotencyKey = "host-request-2"
	retryExecution.Role = "minimal_diff_repair"
	retryExecution.ContextPolicy = "minimal-diff-repair-v1"
	retryExecution.GoalIDs = []string{"goal.play"}
	retryExecution.CapabilityIDs = []string{"capability.play"}
	retry, err := st.StartExecution(ctx, retryExecution)
	require.NoError(t, err)
	retryCandidate, err := st.SubmitCandidate(ctx, retry.ID, []CandidateFile{{
		Path: "synth.go", Content: "package synth\nfunc Play() {}\n", WriteIntent: "modify",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{ExecutionID: retry.ID, ToStatus: execution.StatusValidating})
	require.NoError(t, err)
	_, err = st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: retry.ID, Accepted: true, Reason: "retry passed",
		Resource: &Resource{ID: "resource.synth", Kind: "asset", DeclarationHash: "decl", EffectiveHash: "effective", Model: "test-model"},
		Files: []GeneratedFile{{
			Path: "synth.go", ResourceID: "resource.synth", ContentHash: retryCandidate.Files[0].ContentHash,
			PromptHash: retry.ContextHash, Model: "test-model",
		}},
		Attempts: 2,
	})
	require.NoError(t, err)
	resolvedFailures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, "resolved by accepted retry", resolvedFailures[0].Resolution)
	require.Equal(t, "attempt-2", resolvedFailures[0].ResolvedByAttempt)
	require.NotNil(t, resolvedFailures[0].ResolvedAt)
}

func TestExecutionRejectsMismatchedContextAndIllegalTransitions(t *testing.T) {
	st := newContextManifestStore(t, "resource.synth")
	ctx := context.Background()
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-1", "manifest-1", "resource.synth"), Dispatch: true,
	})
	require.NoError(t, err)

	input := testExecutionManifest()
	input.ContextHash = "stale-context"
	_, err = st.StartExecution(ctx, input)
	require.ErrorContains(t, err, "context hash mismatch")

	input = testExecutionManifest()
	input.TemplateHashes = map[string]string{"resource": "tampered-template"}
	_, err = st.StartExecution(ctx, input)
	require.ErrorContains(t, err, "template hashes do not match")

	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{ExecutionID: started.ID, ToStatus: execution.StatusAccepted})
	require.ErrorContains(t, err, "invalid execution transition")
	_, err = st.SubmitCandidate(ctx, started.ID, []CandidateFile{{Path: "../escape.go", Content: "bad"}})
	require.ErrorContains(t, err, "must be project-relative")
}

func TestFinalizeCandidateAtomicallyAcceptsResourceOwnershipAndGeneration(t *testing.T) {
	st := newContextManifestStore(t, "resource.synth")
	ctx := context.Background()
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-1", "manifest-1", "resource.synth"), Dispatch: true,
	})
	require.NoError(t, err)
	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "synth.go", Content: "package synth\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{ExecutionID: started.ID, ToStatus: execution.StatusValidating})
	require.NoError(t, err)

	// A mismatched accepted hash rolls back every state projection.
	_, err = st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: started.ID, Accepted: true,
		Resource: &Resource{ID: "resource.synth", Kind: "asset", DeclarationHash: "decl", EffectiveHash: "effective", Model: "test-model"},
		Files:    []GeneratedFile{{Path: "synth.go", ResourceID: "resource.synth", ContentHash: "tampered"}},
		Attempts: 1,
	})
	require.ErrorContains(t, err, "differs from immutable candidate")
	stillValidating, err := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusValidating), stillValidating.Status)
	_, err = st.GetResource("resource.synth")
	require.Error(t, err)

	accepted, err := st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: started.ID, Accepted: true, Reason: "all validations passed",
		Resource: &Resource{ID: "resource.synth", Kind: "asset", ContextName: "Audio", DeclarationHash: "decl", EffectiveHash: "effective", Model: "test-model"},
		Files: []GeneratedFile{{
			Path: "synth.go", ResourceID: "resource.synth", ContentHash: candidate.Files[0].ContentHash,
			PromptHash: started.ContextHash, Model: "test-model",
		}},
		Dependencies: []Dependency{{SourceID: "resource.synth", TargetID: "resource.clock", Kind: "uses"}},
		Notes:        "keep the oscillator deterministic", Attempts: 1, DurationMS: 30,
		HostCommitRef: "commit-abc", GoalProgress: map[string]any{"goal.play": "advanced"},
	})
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusAccepted), accepted.Status)
	require.Equal(t, "commit-abc", accepted.HostCommitRef)
	require.Equal(t, "advanced", accepted.GoalProgress["goal.play"])
	resource, err := st.GetResource("resource.synth")
	require.NoError(t, err)
	require.Equal(t, "effective", resource.EffectiveHash)
	files, err := st.GetGeneratedFiles("resource.synth")
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, candidate.Files[0].ContentHash, files[0].ContentHash)
	dependencies, err := st.GetDependencies("resource.synth")
	require.NoError(t, err)
	require.Equal(t, "resource.clock", dependencies[0].TargetID)
	note, err := st.GetNote("resource.synth", "apply-1")
	require.NoError(t, err)
	require.Equal(t, "keep the oscillator deterministic", note)
	sessionResource, err := st.GetSessionResource("session-1", "resource.synth")
	require.NoError(t, err)
	require.Equal(t, "committed", sessionResource.State)
	generationRows, err := st.ListGenerations("resource.synth", 10)
	require.NoError(t, err)
	require.Len(t, generationRows, 1)
	require.Equal(t, started.ID, generationRows[0].ExecutionID)
	require.False(t, generationRows[0].Legacy)
	require.Equal(t, "accepted", generationRows[0].Outcome)
}

func TestFinalizeCandidateRollsBackEveryProjectionWhenLateEventWriteFails(t *testing.T) {
	st := newContextManifestStore(t, "resource.rollback")
	ctx := context.Background()
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-1", "manifest-1", "resource.rollback"), Dispatch: true,
	})
	require.NoError(t, err)
	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "rollback.go", Content: "package rollback\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID,
		ToStatus:    execution.StatusValidating,
	})
	require.NoError(t, err)

	before, err := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	sessionResourceBefore, err := st.GetSessionResource("session-1", "resource.rollback")
	require.NoError(t, err)
	_, err = st.sqlDB.Exec(`CREATE TRIGGER inject_candidate_event_failure
		BEFORE INSERT ON execution_events
		WHEN NEW.event_kind = 'candidate_accepted'
		BEGIN
			SELECT RAISE(ABORT, 'injected candidate event failure');
		END`)
	require.NoError(t, err)

	_, err = st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: started.ID, Accepted: true, Reason: "valid candidate",
		Resource: &Resource{
			ID: "resource.rollback", Kind: "asset", DeclarationHash: "decl",
			EffectiveHash: "effective", Model: "test-model",
		},
		Files: []GeneratedFile{{
			Path: "rollback.go", ResourceID: "resource.rollback", ContentHash: candidate.Files[0].ContentHash,
			PromptHash: started.ContextHash, Model: "test-model",
		}},
		Dependencies: []Dependency{{SourceID: "resource.rollback", TargetID: "resource.clock", Kind: "uses"}},
		Notes:        "this write must roll back", Attempts: 1, DurationMS: 30,
		HostCommitRef: "commit-that-must-roll-back", GoalProgress: map[string]any{"goal.play": "advanced"},
	})
	require.ErrorContains(t, err, "injected candidate event failure")

	after, err := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusValidating), after.Status)
	require.Equal(t, before.HostCommitRef, after.HostCommitRef)
	require.Equal(t, before.GoalProgress, after.GoalProgress)
	require.Len(t, after.Events, len(before.Events), "the rejected event insert must not leave an execution transition")

	attempt, err := st.GetContextManifest(ctx, "manifest-1")
	require.NoError(t, err)
	require.Equal(t, "candidate_submitted", attempt.Attempt.Status)
	rolledBackCandidate, err := st.GetCandidateSetByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, "submitted", rolledBackCandidate.Status)
	require.Nil(t, rolledBackCandidate.DisposedAt)

	_, err = st.GetResource("resource.rollback")
	require.Error(t, err)
	ownedFiles, err := st.GetGeneratedFiles("resource.rollback")
	require.NoError(t, err)
	require.Empty(t, ownedFiles)
	dependencies, err := st.GetDependencies("resource.rollback")
	require.NoError(t, err)
	require.Empty(t, dependencies)
	note, err := st.GetNote("resource.rollback", "apply-1")
	require.NoError(t, err)
	require.Empty(t, note)

	sessionResource, err := st.GetSessionResource("session-1", "resource.rollback")
	require.NoError(t, err)
	require.Equal(t, sessionResourceBefore, sessionResource)
	generations, err := st.ListGenerations("resource.rollback", 10)
	require.NoError(t, err)
	require.Empty(t, generations)

	var applyActions int
	require.NoError(t, st.sqlDB.QueryRow("SELECT COUNT(*) FROM apply_actions WHERE resource_id = ?", "resource.rollback").Scan(&applyActions))
	require.Zero(t, applyActions)
	var acceptances int
	require.NoError(t, st.sqlDB.QueryRow(`SELECT COUNT(*) FROM candidate_file_acceptances
		WHERE resource_id = ?`, "resource.rollback").Scan(&acceptances))
	require.Zero(t, acceptances)
}

func TestFinalizeCandidateRejectsWithFailureAndHandoffInOneTransaction(t *testing.T) {
	st := newContextManifestStore(t, "resource.rejected")
	ctx := context.Background()
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-1", "manifest-1", "resource.rejected"), Dispatch: true,
	})
	require.NoError(t, err)
	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)
	_, err = st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "rejected.go", Content: "package rejected\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID,
		ToStatus:    execution.StatusValidating,
	})
	require.NoError(t, err)

	rejected, err := st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: started.ID, Reason: "validation failed", Attempts: 1,
		Failure: &FailureClassificationWrite{
			Category: execution.FailureImplementationError, Origin: "engine", Confidence: 1,
			EvidenceSource: "validation_run", EvidenceReference: "validation-1", Evidence: "compile failed",
		},
		HandoffRole: "minimal_diff_repair",
	})
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusRejected), rejected.Status)

	candidate, err := st.GetCandidateSetByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, "rejected", candidate.Status)
	require.Equal(t, "validation failed", candidate.DispositionReason)
	attempt, err := st.GetContextManifest(ctx, "manifest-1")
	require.NoError(t, err)
	require.Equal(t, "rejected", attempt.Attempt.Status)

	failures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Len(t, failures, 1)
	require.Equal(t, string(execution.FailureImplementationError), failures[0].Category)
	handoffs, err := st.ListAttemptHandoffsBySource(ctx, "attempt-1")
	require.NoError(t, err)
	require.Len(t, handoffs, 1)
	require.Equal(t, failures[0].ID, handoffs[0].FailureID)
	require.Equal(t, "minimal_diff_repair", handoffs[0].TargetRole)

	sessionResource, err := st.GetSessionResource("session-1", "resource.rejected")
	require.NoError(t, err)
	require.Equal(t, "rejected", sessionResource.State)
	require.Equal(t, "validation failed", sessionResource.LastError)
	generations, err := st.ListGenerations("resource.rejected", 10)
	require.NoError(t, err)
	require.Len(t, generations, 1)
	require.Equal(t, "rejected", generations[0].Outcome)
	require.Equal(t, "validation failed", generations[0].RejectionReason)
	_, err = st.GetResource("resource.rejected")
	require.Error(t, err, "rejected finalization must not accept resource ownership")
}

func TestFailureRoutingBlocksSpecificationAndValidationDefects(t *testing.T) {
	st := newContextManifestStore(t, "resource.synth")
	ctx := context.Background()
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-1", "manifest-1", "resource.synth"), Dispatch: true,
	})
	require.NoError(t, err)

	failure, err := st.CreateFailureClassification(ctx, FailureClassificationWrite{
		AttemptID: "attempt-1", Category: execution.FailureInvalidValidation,
		Origin: "human", Confidence: 0.9, EvidenceSource: "review", Evidence: "test asserts the wrong result",
	})
	require.NoError(t, err)
	require.True(t, failure.BlocksRetry)
	require.Equal(t, "repair_validation", failure.CorrectiveAction)
	require.Empty(t, failure.NextRole)
}

func testExecutionManifest() ExecutionManifest {
	return ExecutionManifest{
		AttemptID: "attempt-1", ContextManifestID: "manifest-1",
		ProtocolVersion: execution.ProtocolVersion, IdempotencyKey: "host-request-1",
		PlanOperationID: "operation-1", Role: "resource_implementer",
		RolePolicyVersion: execution.RolePolicyVersion, ContextPolicy: "resource-implementation-v1",
		HostName: "test-host", HostVersion: "1.2.3", Provider: "test-provider", Model: "test-model",
		InferenceConfig: map[string]any{
			"temperature": 0, "nested": map[string]any{"AUTHORIZATION": "Bearer secret-value"},
		},
		AgentConfig: map[string]any{"Api_Key": "secret-value", "mode": "implementation"},
		Tools: []ExecutionTool{
			{Name: "shell", Permission: "allow:test-only"},
			{Name: "filesystem", Permission: "allow:project-files"},
		},
		TemplateHashes:     map[string]string{"resource": "template-v1"},
		SystemInstructions: "use Bearer secret-value", ContextHash: contextHashForTest(), HostSessionID: "host-session-1",
	}
}

func contextHashForTest() string {
	return testContextManifest("attempt", "manifest", "resource").ContextHash
}
