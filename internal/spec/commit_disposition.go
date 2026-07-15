package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/execution"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type commitAttemptState struct {
	attemptID   string
	notes       string
	metadata    CommitMetadata
	execution   *store.ExecutionManifest
	context     *store.ContextManifest
	policy      execution.RolePolicy
	candidate   *store.CandidateSet
	session     *store.Session
	plan        *PlanResult
	resourceID  string
	resource    cuepkg.Resource
	action      planpkg.PlannedAction
	validations []ValidationResult
}

// submitAndStageCommitCandidate snapshots the immutable candidate before
// mutating the coordinated workspace, then advances governance to validation.
// The caller owns the workspace coordinator for the entire commit use case.
func (s *Spec) submitAndStageCommitCandidate(ctx context.Context, commit *commitAttemptState, files []CommitFile) error {
	candidateFiles, err := s.snapshotCommitFiles(files)
	if err != nil {
		return err
	}
	commit.candidate, err = s.store.SubmitCandidate(ctx, commit.execution.ID, candidateFiles)
	if err != nil {
		return err
	}
	if err := s.writeChangedFiles(files); err != nil {
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureToolFailure, err.Error())
		return err
	}
	_, err = s.store.TransitionExecution(ctx, store.ExecutionTransition{
		ExecutionID: commit.execution.ID, ToStatus: execution.StatusValidating,
		Details: map[string]any{"candidate_id": commit.candidate.ID},
	})
	return err
}

// loadCommitTarget reloads the canonical session and current declaration after
// workspace mutation, preserving the original governed ordering.
func (s *Spec) loadCommitTarget(ctx context.Context, commit *commitAttemptState) error {
	var err error
	commit.session, err = s.store.GetSession(commit.context.Attempt.SessionID)
	if err != nil {
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureToolFailure, err.Error())
		return fmt.Errorf("commit attempt: session: %w", err)
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
	commit.action = action
	return nil
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
		return nil, err
	}
	s.markAmendmentVerification(commit.resourceID, commit.resource, false)
	return &CommitResult{
		Committed: false, Validations: commit.validations, AttemptID: commit.attemptID,
		ExecutionID: commit.execution.ID, CandidateID: commit.candidate.ID, FailureCategory: string(category),
	}, nil
}

func (s *Spec) acceptCommitCandidate(ctx context.Context, commit *commitAttemptState) (*CommitResult, error) {
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
	_, err = s.store.FinalizeCandidate(ctx, store.CandidateDisposition{
		ExecutionID: commit.execution.ID, Accepted: true, Reason: "mechanical validations passed",
		Details: map[string]any{"validations": commit.validations}, Attempts: commit.context.Attempt.RetryNumber,
		Resource: &store.Resource{
			ID: commit.resourceID, Kind: commit.resource.Kind, ContextName: commit.resource.ContextName,
			DeclarationHash: declarationHash, EffectiveHash: effectiveHash, Model: commit.execution.Model,
		},
		Files: acceptedFiles, Dependencies: dependencies, Notes: commit.notes,
		DurationMS: metadata.DurationMS, InputTokens: metadata.InputTokens,
		OutputTokens: metadata.OutputTokens, CostUSD: metadata.CostUSD, HostCommitRef: metadata.HostCommitRef,
		GoalProgress: executionGoalProgress(commit.action, "advanced", "accepted resource candidate"),
	})
	if err != nil {
		return nil, err
	}
	_ = s.reprojectCompletion(ctx, commit.plan, store.TransitionSource{
		Type: "generation_attempt", ID: commit.context.Attempt.ID, SessionID: commit.context.Attempt.SessionID,
	})
	s.markAmendmentVerification(commit.resourceID, commit.resource, true)
	return &CommitResult{
		Committed: true, Validations: commit.validations, AttemptID: commit.attemptID,
		ExecutionID: commit.execution.ID, CandidateID: commit.candidate.ID,
	}, nil
}
