package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	"github.com/crestenstclair/crest-spec/internal/execution"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type commitAttemptState struct {
	attemptID          string
	notes              string
	metadata           CommitMetadata
	execution          *store.ExecutionManifest
	context            *store.ContextManifest
	policy             execution.RolePolicy
	candidate          *store.CandidateSet
	session            *store.Session
	plan               *PlanResult
	resourceID         string
	resource           cuepkg.Resource
	action             planpkg.PlannedAction
	validations        []ValidationResult
	validationDeferred bool
	workspaceJournal   *store.WorkspaceCommitJournal
}

// submitAndStageCommitCandidate snapshots the immutable candidate before
// mutating the coordinated workspace, then advances governance to validation.
// The caller owns the workspace coordinator for the entire commit use case.
func (s *Spec) submitAndStageCommitCandidate(ctx context.Context, commit *commitAttemptState, files []CommitFile) error {
	candidateFiles, err := s.snapshotCommitFiles(files)
	if err != nil {
		return err
	}
	commit.workspaceJournal, err = s.prepareWorkspaceCommitJournal(ctx, commit, candidateFiles)
	if err != nil {
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureToolFailure, err.Error())
		return err
	}
	s.runWorkspaceCommitFailpoint("after_journal_prepared")
	commit.candidate, err = s.store.SubmitCandidate(ctx, commit.execution.ID, candidateFiles)
	if err != nil {
		return s.rollbackWorkspaceCommitAfterError(ctx, commit, err)
	}
	s.runWorkspaceCommitFailpoint("after_candidate_submitted")
	if err := s.store.BindWorkspaceCommitCandidate(ctx, commit.workspaceJournal.ID, commit.candidate.ID); err != nil {
		return s.rollbackWorkspaceCommitAfterError(ctx, commit, err)
	}
	if err := s.writeWorkspaceCommitCandidate(commit.workspaceJournal); err != nil {
		return s.rollbackWorkspaceCommitAfterError(ctx, commit, err)
	}
	if err := s.store.MarkWorkspaceCommitApplied(ctx, commit.workspaceJournal.ID); err != nil {
		return s.rollbackWorkspaceCommitAfterError(ctx, commit, err)
	}
	s.runWorkspaceCommitFailpoint("after_workspace_writes")
	_, err = s.store.TransitionExecution(ctx, store.ExecutionTransition{
		ExecutionID: commit.execution.ID, ToStatus: execution.StatusValidating,
		Details: map[string]any{"candidate_id": commit.candidate.ID},
	})
	if err != nil {
		return s.rollbackWorkspaceCommitAfterError(ctx, commit, err)
	}
	s.runWorkspaceCommitFailpoint("after_validating_transition")
	return nil
}

// loadCommitTarget reloads the canonical session and current declaration
// before candidate submission or workspace mutation. This fail-closed ordering
// prevents a context prepared from stale CUE inputs from writing any files.
func (s *Spec) loadCommitTarget(ctx context.Context, commit *commitAttemptState) error {
	var err error
	commit.session, err = s.store.GetSession(commit.context.Attempt.SessionID)
	if err != nil {
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureToolFailure, err.Error())
		return fmt.Errorf("commit attempt: session: %w", err)
	}
	if commit.session.Status != "active" {
		reason := fmt.Sprintf("session is %s", commit.session.Status)
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureMissingProjectIntent, reason)
		return fmt.Errorf("commit attempt: %s", reason)
	}
	commit.plan, err = s.Plan(ctx)
	if err != nil {
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureArchitecturalInconsistency, err.Error())
		return fmt.Errorf("commit attempt: plan: %w", err)
	}
	commit.resourceID = commit.context.Attempt.ResourceID
	resource, ok := commit.plan.Registry.Resources[commit.resourceID]
	if !ok {
		reason := "resource is absent from current specification"
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureMissingProjectIntent, reason)
		return fmt.Errorf("commit attempt: resource not found: %s", commit.resourceID)
	}
	commit.resource = resource
	action, ok := actionForSession(commit.session, commit.resourceID)
	if !ok || action.OperationID != commit.context.Attempt.PlanOperationID {
		reason := "persisted plan operation is unavailable"
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureMissingProjectIntent, reason)
		return fmt.Errorf("commit attempt: %s", reason)
	}
	if !operationInputsMatchCurrent(action, commit.plan) {
		reason := "plan inputs changed after context preparation; corrective_action=rebuild_context"
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureStaleContext, reason)
		return fmt.Errorf("commit attempt: %s", reason)
	}
	if err := s.validateSelectedRepositorySources(commit.context); err != nil {
		reason := fmt.Sprintf("repository context changed after preparation: %v; corrective_action=rebuild_context", err)
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureStaleContext, reason)
		return fmt.Errorf("commit attempt: %s", reason)
	}
	commit.action = action
	return nil
}

// operationInputsMatchCurrent checks the immutable inputs that govern
// generation, downstream compatibility, and explicit repair verification.
// Current planner actions may be absent after a candidate is staged or when a
// validation repair is reopened, so the comparison deliberately uses the full
// current registry fingerprints. An authenticated pre-verification operation
// may cross the hash-version boundary only when both of its historical
// projections exactly reproduce from the current registry; this is the same
// semantic operation, not permission to accept real specification drift.
func operationInputsMatchCurrent(action planpkg.PlannedAction, current *PlanResult) bool {
	if current == nil || action.GenerationInputHash == "" || action.DependencyContractHash == "" {
		return false
	}
	if action.GenerationInputHash == current.GenerationInputHashes[action.ResourceID] &&
		action.DependencyContractHash == current.DependencyContractHashes[action.ResourceID] &&
		action.VerificationContractHash == current.VerificationContractHashes[action.ResourceID] {
		return true
	}
	return action.VerificationContractHash == "" &&
		planpkg.HasLegacyOperationIdentity(action) &&
		current.LegacyGenerationInputHashesV2[action.ResourceID] != "" &&
		action.GenerationInputHash == current.LegacyGenerationInputHashesV2[action.ResourceID] &&
		current.LegacyDependencyContractHashesV2[action.ResourceID] != "" &&
		action.DependencyContractHash == current.LegacyDependencyContractHashesV2[action.ResourceID]
}

// validateSelectedRepositorySources is optimistic concurrency control for the
// repository bytes actually served to a worker. It prevents a delayed worker
// from overwriting edits made after its immutable context was prepared.
func (s *Spec) validateSelectedRepositorySources(manifest *store.ContextManifest) error {
	if manifest == nil {
		return fmt.Errorf("context manifest is unavailable")
	}
	for _, section := range manifest.Sections {
		if section.SourceKind != "repository_file" || section.SourcePath == "" ||
			(section.Decision != string(contextmanifest.Included) && section.Decision != string(contextmanifest.Truncated)) {
			continue
		}
		path, err := s.projectFilePath(section.SourcePath)
		if err != nil {
			return fmt.Errorf("%s: %w", section.SourcePath, err)
		}
		current, err := s.fs.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", section.SourcePath, err)
		}
		if contextmanifest.Hash(string(current)) == section.OriginalHash {
			continue
		}
		// Contexts prepared before exact owned-code provenance used the UPDATE
		// MODE presentation hash. Keep active attempts resumable while requiring
		// every newly prepared context to match the raw hash above.
		if section.Kind == "owned_code" {
			_, expected, ok := parseUpdateModeContent(section.Content)
			if ok && string(current) == expected {
				continue
			}
		}
		return fmt.Errorf("%s no longer matches the repository source served to the worker", section.SourcePath)
	}
	return nil
}

// validateUnrepresentedCandidatePaths closes the create/create race left by
// source-only provenance checks. A worker may create a path it was not shown,
// but only while that path remains both absent from the repository and absent
// from generated-file ownership. The caller holds the project commit lock, so
// accepted commits and governed destroys cannot change either fact until the
// candidate has been finalized.
func (s *Spec) validateUnrepresentedCandidatePaths(
	manifest *store.ContextManifest,
	resourceID string,
	files []CommitFile,
) error {
	if manifest == nil {
		return fmt.Errorf("context manifest is unavailable")
	}
	represented := make(map[string]struct{})
	for _, section := range manifest.Sections {
		if section.SourceKind != "repository_file" || section.SourcePath == "" ||
			(section.Decision != string(contextmanifest.Included) && section.Decision != string(contextmanifest.Truncated)) {
			continue
		}
		represented[canonicalRepositoryPath(section.SourcePath)] = struct{}{}
	}

	var unrepresented []CommitFile
	for _, file := range files {
		if _, ok := represented[canonicalRepositoryPath(file.Path)]; !ok {
			unrepresented = append(unrepresented, file)
		}
	}
	if len(unrepresented) == 0 {
		return nil
	}

	ownedByResource := make(map[string]struct{})
	owned, err := s.store.GetGeneratedFiles(resourceID)
	if err != nil {
		return fmt.Errorf("list generated-file ownership for %s: %w", resourceID, err)
	}
	for _, file := range owned {
		ownedByResource[canonicalRepositoryPath(file.Path)] = struct{}{}
	}
	for _, file := range unrepresented {
		canonical := canonicalRepositoryPath(file.Path)
		path, err := s.projectFilePath(file.Path)
		if err != nil {
			return fmt.Errorf("candidate path %s: %w", file.Path, err)
		}
		if _, err := s.fs.Stat(path); err == nil {
			return fmt.Errorf("candidate path was absent from the prepared context but now exists: %s", file.Path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect candidate path %s: %w", file.Path, err)
		}
		if _, ok := ownedByResource[canonical]; ok {
			return fmt.Errorf("candidate path was absent from the prepared context but is already owned: %s", file.Path)
		}
		otherOwners, err := s.store.CountOtherFileOwners(file.Path, resourceID)
		if err != nil {
			return fmt.Errorf("count generated-file owners for %s: %w", file.Path, err)
		}
		if otherOwners > 0 {
			return fmt.Errorf("candidate path was absent from the prepared context but is already owned: %s", file.Path)
		}
	}
	return nil
}

func canonicalRepositoryPath(path string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
}

func (s *Spec) rejectCommitCandidate(ctx context.Context, commit *commitAttemptState, failureMessage string) (*CommitResult, error) {
	category := execution.FailureImplementationError
	if commit.action.Category == "integrative" {
		category = execution.FailureIntegrationError
	}
	if strings.HasPrefix(failureMessage, "validation could not run:") {
		category = execution.FailureInvalidValidation
	}
	evidence, _ := json.Marshal(commit.validations)
	route := execution.RouteFailure(category)
	metadata := commit.metadata
	_, err := s.store.FinalizeCandidate(ctx, store.CandidateDisposition{
		ExecutionID: commit.execution.ID, Reason: failureMessage,
		Details: map[string]any{"validations": commit.validations}, Attempts: commit.context.Attempt.RetryNumber,
		DurationMS: metadata.DurationMS, InputTokens: metadata.InputTokens,
		OutputTokens: metadata.OutputTokens, CostUSD: metadata.CostUSD, HostCommitRef: metadata.HostCommitRef,
		GoalProgress: executionGoalProgress(commit.action, "blocked", failureMessage),
		Failure: &store.FailureClassificationWrite{
			Category: category, Origin: "engine", Confidence: 1, EvidenceSource: "mechanical_validation",
			EvidenceReference: commit.execution.ID, Evidence: string(evidence), GoalIDs: commit.action.Goals,
		},
		HandoffRole: permittedFailureHandoff(commit.policy, route.NextRole),
	})
	if err != nil {
		return nil, s.rollbackWorkspaceCommitAfterError(ctx, commit, err)
	}
	s.runWorkspaceCommitFailpoint("after_finalize_candidate")
	if commit.workspaceJournal != nil {
		if err := s.recoverWorkspaceCommitJournalLocked(ctx, commit.workspaceJournal); err != nil {
			return nil, fmt.Errorf("commit attempt: resolve rejected workspace journal: %w", err)
		}
	}
	s.markAmendmentVerification(commit.resourceID, commit.resource, false)
	return &CommitResult{
		Committed: false, Validations: commit.validations, AttemptID: commit.attemptID,
		ExecutionID: commit.execution.ID, CandidateID: commit.candidate.ID, FailureCategory: string(category),
	}, nil
}

func (s *Spec) acceptCommitCandidate(ctx context.Context, commit *commitAttemptState) (*CommitResult, error) {
	if commit.workspaceJournal == nil {
		return nil, fmt.Errorf("commit attempt: accepted candidate requires a workspace commit journal")
	}
	if err := s.verifyWorkspaceCommitCandidate(commit.workspaceJournal); err != nil {
		return nil, s.rollbackWorkspaceCommitAfterError(
			ctx, commit, fmt.Errorf("commit attempt: candidate workspace verification before finalization: %w", err),
		)
	}
	var hashes map[string]string
	if err := json.Unmarshal([]byte(commit.session.HashesJSON), &hashes); err != nil {
		reason := fmt.Sprintf("decode session hashes: %v", err)
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureArchitecturalInconsistency, reason)
		return nil, fmt.Errorf("commit attempt: %s", reason)
	}
	effectiveHash, ok := hashes[commit.resourceID]
	if !ok || effectiveHash == "" {
		reason := fmt.Sprintf("effective hash for %s is unavailable", commit.resourceID)
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureArchitecturalInconsistency, reason)
		return nil, fmt.Errorf("commit attempt: %s", reason)
	}
	generationInputHash := commit.plan.GenerationInputHashes[commit.resourceID]
	codeDeclarationHash := commit.plan.CodeDeclarationHashes[commit.resourceID]
	verificationContractHash := commit.plan.VerificationContractHashes[commit.resourceID]
	if contract := commit.plan.Registry.VerificationContractForResource(commit.resourceID); len(contract.RepairWitnesses) > 0 {
		// A generated candidate is not proof of its behavioral witness. Preserve
		// the previously witnessed contract (or the empty baseline for a new
		// resource) until a current witness passes against the verified closure.
		verificationContractHash = ""
		previous, previousErr := s.store.GetResource(commit.resourceID)
		if previousErr == nil {
			verificationContractHash = previous.VerificationContractHash
		} else if !errors.Is(previousErr, cserrors.ErrNotFound) {
			reason := fmt.Sprintf("load previous repair-verification fingerprint for %s: %v", commit.resourceID, previousErr)
			s.failExecution(ctx, commit.execution, commit.context, execution.FailureArchitecturalInconsistency, reason)
			return nil, fmt.Errorf("commit attempt: %s", reason)
		}
	}
	exportedContractHash := commit.plan.ExportedContractHashes[commit.resourceID]
	dependencyContractHash := commit.plan.DependencyContractHashes[commit.resourceID]
	if codeDeclarationHash == "" || generationInputHash == "" || exportedContractHash == "" || dependencyContractHash == "" {
		reason := fmt.Sprintf("incremental hashes for %s are unavailable", commit.resourceID)
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureArchitecturalInconsistency, reason)
		return nil, fmt.Errorf("commit attempt: %s", reason)
	}
	declaration, err := json.Marshal(commit.resource.Declaration)
	if err != nil {
		reason := fmt.Sprintf("encode resource declaration: %v", err)
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureArchitecturalInconsistency, reason)
		return nil, fmt.Errorf("commit attempt: %s", reason)
	}
	declarationHash := fmt.Sprintf("%x", sha256.Sum256(declaration))
	acceptedFiles := make([]store.GeneratedFile, 0, len(commit.candidate.Files))
	for _, file := range commit.candidate.Files {
		acceptedFiles = append(acceptedFiles, store.GeneratedFile{
			Path: file.Path, ResourceID: commit.resourceID, ContentHash: file.ContentHash,
			PromptHash: commit.execution.ContextHash, Model: commit.execution.Model, CreatedAt: time.Now().UTC(),
		})
	}
	dependencies := make([]store.Dependency, 0, len(commit.resource.Dependencies))
	for _, dependency := range commit.resource.Dependencies {
		dependencies = append(dependencies, store.Dependency{SourceID: commit.resourceID, TargetID: dependency.TargetID, Kind: dependency.Kind})
	}
	metadata := commit.metadata
	dispositionReason := "mechanical validations passed"
	goalReason := "accepted resource candidate"
	if commit.validationDeferred {
		dispositionReason = "candidate staged; batch validation deferred until the affected closure is migrated"
		goalReason = "staged resource candidate pending affected-closure validation"
	}
	_, err = s.store.FinalizeCandidate(ctx, store.CandidateDisposition{
		ExecutionID: commit.execution.ID, Accepted: true, Reason: dispositionReason,
		Details: map[string]any{"validations": commit.validations, "validation_deferred": commit.validationDeferred}, Attempts: commit.context.Attempt.RetryNumber,
		Resource: &store.Resource{
			ID: commit.resourceID, Kind: commit.resource.Kind, ContextName: commit.resource.ContextName,
			DeclarationHash: declarationHash, EffectiveHash: effectiveHash,
			CodeDeclarationHash:      codeDeclarationHash,
			VerificationContractHash: verificationContractHash,
			GenerationInputHash:      generationInputHash, ExportedContractHash: exportedContractHash,
			DependencyContractHash: dependencyContractHash,
			Model:                  commit.execution.Model,
		},
		Files: acceptedFiles, Dependencies: dependencies, Notes: commit.notes,
		DurationMS: metadata.DurationMS, InputTokens: metadata.InputTokens,
		OutputTokens: metadata.OutputTokens, CostUSD: metadata.CostUSD, HostCommitRef: metadata.HostCommitRef,
		GoalProgress: executionGoalProgress(commit.action, "advanced", goalReason),
	})
	if err != nil {
		return nil, s.rollbackWorkspaceCommitAfterError(ctx, commit, err)
	}
	s.runWorkspaceCommitFailpoint("after_finalize_candidate")
	if err := s.verifyWorkspaceCommitCandidate(commit.workspaceJournal); err != nil {
		// FinalizeCandidate is already durable. Do not report success and do not
		// complete the journal against divergent bytes; restart recovery will
		// fail closed on the same CAS until the external edit is reconciled.
		return nil, fmt.Errorf("commit attempt: accepted candidate workspace verification after finalization: %w", err)
	}
	if commit.workspaceJournal != nil {
		if err := s.store.ResolveWorkspaceCommitJournal(
			ctx, commit.workspaceJournal.ID, store.WorkspaceCommitCommitted,
			"candidate accepted and workspace commit completed",
		); err != nil {
			return nil, fmt.Errorf("commit attempt: complete workspace journal: %w", err)
		}
	}
	_ = s.reprojectCompletion(ctx, commit.plan, store.TransitionSource{
		Type: "generation_attempt", ID: commit.context.Attempt.ID, SessionID: commit.context.Attempt.SessionID,
	})
	if !commit.validationDeferred {
		s.markAmendmentVerification(commit.resourceID, commit.resource, true)
	}
	return &CommitResult{
		Committed: true, Validations: commit.validations, ValidationDeferred: commit.validationDeferred, AttemptID: commit.attemptID,
		ExecutionID: commit.execution.ID, CandidateID: commit.candidate.ID,
	}, nil
}

func (s *Spec) runWorkspaceCommitFailpoint(stage string) {
	if s.workspaceCommitFailpoint != nil {
		s.workspaceCommitFailpoint(stage)
	}
}

func (s *Spec) rollbackWorkspaceCommitAfterError(
	ctx context.Context,
	commit *commitAttemptState,
	cause error,
) error {
	if commit == nil || commit.workspaceJournal == nil {
		return cause
	}
	if recoveryErr := s.recoverWorkspaceCommitJournalLocked(ctx, commit.workspaceJournal); recoveryErr != nil {
		return errors.Join(cause, fmt.Errorf("recover interrupted workspace commit: %w", recoveryErr))
	}
	return cause
}
