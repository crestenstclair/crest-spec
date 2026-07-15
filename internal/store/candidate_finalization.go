package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/db"
	"github.com/crestenstclair/crest-spec/internal/execution"
)

// FinalizeCandidate commits the complete governance disposition as one SQLite
// transaction. For acceptance that includes resource/file ownership and
// dependency state; for rejection it includes failure routing and handoff.
func (s *Store) FinalizeCandidate(ctx context.Context, input CandidateDisposition) (*ExecutionManifest, error) {
	if input.DurationMS < 0 || input.InputTokens < 0 || input.OutputTokens < 0 || input.CostUSD < 0 {
		return nil, fmt.Errorf("finalize candidate: metrics cannot be negative")
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("finalize candidate: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	q := s.queries.WithTx(tx)
	state, err := prepareCandidateFinalization(ctx, q, input)
	if err != nil {
		return nil, err
	}
	if err := persistCandidateTerminalState(ctx, q, state, input); err != nil {
		return nil, err
	}
	if input.Accepted {
		if err := persistAcceptedCandidate(ctx, q, state, input); err != nil {
			return nil, err
		}
	}
	if err := persistCandidateFailureRouting(ctx, q, state, input); err != nil {
		return nil, err
	}
	if err := persistCandidateOutcomeProjections(ctx, q, state, input); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("finalize candidate: commit: %w", err)
	}
	return s.GetExecutionManifest(ctx, state.manifest.ID)
}

type candidateFinalizationState struct {
	manifest       db.ExecutionManifest
	attempt        db.GenerationAttempt
	candidate      db.CandidateSet
	candidateFiles []db.CandidateFile
	outcome        candidateFinalizationOutcome
	timestamp      string
	progressJSON   string
	progressHash   string
}

type candidateFinalizationOutcome struct {
	executionStatus      execution.Status
	candidateDisposition string
	attemptStatus        string
	sessionState         string
	actionOutcome        string
}

func prepareCandidateFinalization(
	ctx context.Context,
	q *db.Queries,
	input CandidateDisposition,
) (candidateFinalizationState, error) {
	manifest, err := q.GetExecutionManifest(ctx, input.ExecutionID)
	if err != nil {
		return candidateFinalizationState{}, fmt.Errorf("finalize candidate: execution: %w", mapNotFound(err))
	}
	if manifest.Status != string(execution.StatusValidating) {
		return candidateFinalizationState{}, fmt.Errorf(
			"finalize candidate: execution %s must be validating, got %s",
			manifest.ID,
			manifest.Status,
		)
	}
	attempt, err := q.GetGenerationAttempt(ctx, manifest.AttemptID)
	if err != nil {
		return candidateFinalizationState{}, fmt.Errorf("finalize candidate: attempt: %w", err)
	}
	candidate, err := q.GetCandidateSetByExecution(ctx, manifest.ID)
	if err != nil {
		return candidateFinalizationState{}, fmt.Errorf("finalize candidate: candidate: %w", mapNotFound(err))
	}
	candidateFiles, err := q.ListCandidateFiles(ctx, candidate.ID)
	if err != nil {
		return candidateFinalizationState{}, fmt.Errorf("finalize candidate: files: %w", err)
	}
	if input.Accepted {
		if input.Resource == nil || input.Resource.ID != attempt.ResourceID {
			return candidateFinalizationState{}, fmt.Errorf("finalize candidate: accepted disposition requires the attempt resource")
		}
		if err := validateAcceptedFiles(candidateFiles, input.Files, attempt.ResourceID); err != nil {
			return candidateFinalizationState{}, err
		}
	}

	progressJSON, progressHash := manifest.GoalProgressJson, manifest.GoalProgressHash
	if input.GoalProgress != nil {
		progressJSON, progressHash, err = execution.CanonicalRedacted(input.GoalProgress)
		if err != nil {
			return candidateFinalizationState{}, fmt.Errorf("finalize candidate: goal progress: %w", err)
		}
	}
	return candidateFinalizationState{
		manifest:       manifest,
		attempt:        attempt,
		candidate:      candidate,
		candidateFiles: candidateFiles,
		outcome:        candidateOutcome(input.Accepted),
		timestamp:      now(),
		progressJSON:   progressJSON,
		progressHash:   progressHash,
	}, nil
}

func candidateOutcome(accepted bool) candidateFinalizationOutcome {
	if accepted {
		return candidateFinalizationOutcome{
			executionStatus:      execution.StatusAccepted,
			candidateDisposition: "accepted",
			attemptStatus:        "accepted",
			sessionState:         "committed",
			actionOutcome:        "committed",
		}
	}
	return candidateFinalizationOutcome{
		executionStatus:      execution.StatusRejected,
		candidateDisposition: "rejected",
		attemptStatus:        "rejected",
		sessionState:         "rejected",
		actionOutcome:        "rejected",
	}
}

func persistCandidateTerminalState(
	ctx context.Context,
	q *db.Queries,
	state candidateFinalizationState,
	input CandidateDisposition,
) error {
	changed, err := q.UpdateExecutionManifestStatus(ctx, db.UpdateExecutionManifestStatusParams{
		Status: string(state.outcome.executionStatus), DispositionReason: input.Reason, CompletedAt: &state.timestamp,
		DurationMs: input.DurationMS, InputTokens: input.InputTokens, OutputTokens: input.OutputTokens,
		CostUsd: input.CostUSD, UpdatedAt: state.timestamp, HostCommitRef: input.HostCommitRef,
		GoalProgressJson: state.progressJSON, GoalProgressHash: state.progressHash,
		ID: state.manifest.ID, Status_2: state.manifest.Status,
	})
	if err != nil || changed != 1 {
		return fmt.Errorf("finalize candidate: execution transition: affected=%d: %w", changed, err)
	}
	changed, err = q.UpdateCandidateSetDisposition(ctx, db.UpdateCandidateSetDispositionParams{
		Status: state.outcome.candidateDisposition, DispositionReason: input.Reason,
		DisposedAt: &state.timestamp, ID: state.candidate.ID,
	})
	if err != nil || changed != 1 {
		return fmt.Errorf("finalize candidate: candidate disposition: affected=%d: %w", changed, err)
	}
	changed, err = q.UpdateGenerationAttemptStatus(ctx, db.UpdateGenerationAttemptStatusParams{
		Status: state.outcome.attemptStatus, ID: state.attempt.ID, Status_2: "candidate_submitted",
	})
	if err != nil || changed != 1 {
		return fmt.Errorf("finalize candidate: attempt disposition: affected=%d: %w", changed, err)
	}
	return nil
}

func persistAcceptedCandidate(
	ctx context.Context,
	q *db.Queries,
	state candidateFinalizationState,
	input CandidateDisposition,
) error {
	resource := *input.Resource
	if err := q.SetResource(ctx, db.SetResourceParams{
		ID: resource.ID, Kind: resource.Kind, ContextName: stringPtr(resource.ContextName),
		DeclarationHash: resource.DeclarationHash, EffectiveHash: resource.EffectiveHash,
		Model: stringPtr(resource.Model), SettledAt: state.timestamp,
	}); err != nil {
		return fmt.Errorf("finalize candidate: resource: %w", err)
	}
	if err := replaceAcceptedFileOwnership(ctx, q, state, input.Files); err != nil {
		return err
	}
	if err := acceptCandidateFiles(ctx, q, state); err != nil {
		return err
	}
	if err := replaceAcceptedDependencies(ctx, q, state.attempt.ResourceID, input.Dependencies); err != nil {
		return err
	}
	if input.Notes != "" {
		if err := q.CreateNote(ctx, db.CreateNoteParams{
			ResourceID: state.attempt.ResourceID, ApplyID: state.attempt.ApplyID,
			Content: input.Notes, CreatedAt: state.timestamp,
		}); err != nil {
			return fmt.Errorf("finalize candidate: note: %w", err)
		}
	}
	return resolveAcceptedRetryFailures(ctx, q, state.attempt, state.timestamp)
}

func replaceAcceptedFileOwnership(
	ctx context.Context,
	q *db.Queries,
	state candidateFinalizationState,
	files []GeneratedFile,
) error {
	if len(files) == 0 {
		return nil
	}
	if err := q.DeleteGeneratedFiles(ctx, state.attempt.ResourceID); err != nil {
		return fmt.Errorf("finalize candidate: replace file ownership: %w", err)
	}
	for _, file := range files {
		if err := q.SetGeneratedFile(ctx, db.SetGeneratedFileParams{
			Path: file.Path, ResourceID: state.attempt.ResourceID, ContentHash: file.ContentHash,
			PromptHash: file.PromptHash, Model: file.Model, CreatedAt: state.timestamp,
		}); err != nil {
			return fmt.Errorf("finalize candidate: own file %s: %w", file.Path, err)
		}
	}
	return nil
}

func acceptCandidateFiles(ctx context.Context, q *db.Queries, state candidateFinalizationState) error {
	for _, file := range state.candidateFiles {
		if err := q.AcceptCandidateFile(ctx, db.AcceptCandidateFileParams{
			CandidateFileID: file.ID, ResourceID: state.attempt.ResourceID,
			Path: file.Path, AcceptedAt: state.timestamp,
		}); err != nil {
			return fmt.Errorf("finalize candidate: accept file %s: %w", file.Path, err)
		}
	}
	return nil
}

func replaceAcceptedDependencies(
	ctx context.Context,
	q *db.Queries,
	resourceID string,
	dependencies []Dependency,
) error {
	if err := q.DeleteDependencies(ctx, resourceID); err != nil {
		return fmt.Errorf("finalize candidate: clear dependencies: %w", err)
	}
	for _, dependency := range dependencies {
		if dependency.SourceID != resourceID {
			return fmt.Errorf("finalize candidate: dependency source must be %s", resourceID)
		}
		if err := q.SetDependency(ctx, db.SetDependencyParams{
			SourceID: dependency.SourceID, TargetID: dependency.TargetID, Kind: dependency.Kind,
		}); err != nil {
			return fmt.Errorf("finalize candidate: dependency: %w", err)
		}
	}
	return nil
}

func resolveAcceptedRetryFailures(
	ctx context.Context,
	q *db.Queries,
	attempt db.GenerationAttempt,
	timestamp string,
) error {
	ancestorID := stringVal(attempt.ParentAttemptID)
	for ancestorID != "" {
		failures, err := q.ListFailureClassificationsByAttempt(ctx, ancestorID)
		if err != nil {
			return fmt.Errorf("finalize candidate: list ancestor failures: %w", err)
		}
		for _, failure := range failures {
			if failure.ResolvedAt != nil {
				continue
			}
			if _, err := q.ResolveFailureClassification(ctx, db.ResolveFailureClassificationParams{
				Resolution: "resolved by accepted retry", ResolvedByAttempt: &attempt.ID,
				ResolvedAt: &timestamp, ID: failure.ID,
			}); err != nil {
				return fmt.Errorf("finalize candidate: resolve ancestor failure: %w", err)
			}
		}
		ancestor, err := q.GetGenerationAttempt(ctx, ancestorID)
		if err != nil {
			return fmt.Errorf("finalize candidate: ancestor attempt: %w", err)
		}
		ancestorID = stringVal(ancestor.ParentAttemptID)
	}
	return nil
}

func persistCandidateFailureRouting(
	ctx context.Context,
	q *db.Queries,
	state candidateFinalizationState,
	input CandidateDisposition,
) error {
	var failure *FailureClassification
	if input.Failure != nil {
		write := *input.Failure
		write.AttemptID = state.attempt.ID
		write.ExecutionID = state.manifest.ID
		created, err := createFailureClassificationTx(ctx, q, write, state.timestamp)
		if err != nil {
			return fmt.Errorf("finalize candidate: failure: %w", err)
		}
		failure = created
	}
	if input.HandoffRole == "" {
		return nil
	}
	failureID := ""
	if failure != nil {
		failureID = failure.ID
	}
	_, err := createAttemptHandoffTx(ctx, q, AttemptHandoff{
		SourceAttemptID: state.attempt.ID, SourceRole: state.attempt.Role,
		TargetRole: input.HandoffRole, Reason: input.Reason,
		ExpectedOutcome: "produce a corrected candidate", FailureID: failureID,
	}, state.timestamp)
	if err != nil {
		return fmt.Errorf("finalize candidate: handoff: %w", err)
	}
	return nil
}

func persistCandidateOutcomeProjections(
	ctx context.Context,
	q *db.Queries,
	state candidateFinalizationState,
	input CandidateDisposition,
) error {
	lastError := ""
	if !input.Accepted {
		lastError = input.Reason
	}
	if err := q.UpdateSessionResourceState(ctx, db.UpdateSessionResourceStateParams{
		State: state.outcome.sessionState, LastError: stringPtr(lastError), Attempts: int64(input.Attempts),
		UpdatedAt: state.timestamp, SessionID: state.attempt.SessionID, ResourceID: state.attempt.ResourceID,
	}); err != nil {
		return fmt.Errorf("finalize candidate: session resource: %w", err)
	}

	actionID := uuid.NewString()
	if err := q.CreateApplyAction(ctx, db.CreateApplyActionParams{
		ID: actionID, ApplyID: state.attempt.ApplyID, ResourceID: state.attempt.ResourceID,
		Action: "commit", StartedAt: state.timestamp,
	}); err != nil {
		return fmt.Errorf("finalize candidate: apply action: %w", err)
	}
	var rejectionReason *string
	if !input.Accepted && input.Reason != "" {
		rejectionReason = &input.Reason
	}
	if err := q.UpdateApplyAction(ctx, db.UpdateApplyActionParams{
		Outcome: &state.outcome.actionOutcome, Error: rejectionReason,
		DoneAt: &state.timestamp, ID: actionID,
	}); err != nil {
		return fmt.Errorf("finalize candidate: finish apply action: %w", err)
	}
	if err := q.CreateExecutionGeneration(ctx, db.CreateExecutionGenerationParams{
		ID: uuid.NewString(), ApplyID: stringPtr(state.attempt.ApplyID), ResourceID: state.attempt.ResourceID,
		PromptHash: state.manifest.ContextHash, Model: state.manifest.Model,
		Outcome: &state.outcome.candidateDisposition, RejectionReason: rejectionReason,
		RetryCount: state.attempt.RetryNumber - 1, DurationMs: &input.DurationMS,
		InputTokens: &input.InputTokens, OutputTokens: &input.OutputTokens,
		CostUsd: &input.CostUSD, CreatedAt: state.timestamp, ExecutionID: &state.manifest.ID,
	}); err != nil {
		return fmt.Errorf("finalize candidate: generation projection: %w", err)
	}
	if err := createExecutionEvent(
		ctx,
		q,
		state.manifest.ID,
		state.manifest.Status,
		string(state.outcome.executionStatus),
		"candidate_"+state.outcome.candidateDisposition,
		input.Details,
		state.timestamp,
	); err != nil {
		return fmt.Errorf("finalize candidate: event: %w", err)
	}
	return nil
}

func validateAcceptedFiles(candidateFiles []db.CandidateFile, files []GeneratedFile, resourceID string) error {
	if len(candidateFiles) != len(files) {
		return fmt.Errorf("finalize candidate: accepted file set differs from immutable candidate")
	}
	accepted := make(map[string]GeneratedFile, len(files))
	for _, file := range files {
		if file.ResourceID != resourceID {
			return fmt.Errorf("finalize candidate: file %s has wrong resource owner", file.Path)
		}
		accepted[file.Path] = file
	}
	for _, candidate := range candidateFiles {
		file, ok := accepted[candidate.Path]
		if !ok || file.ContentHash != candidate.ContentHash {
			return fmt.Errorf("finalize candidate: accepted file %s differs from immutable candidate", candidate.Path)
		}
	}
	return nil
}
