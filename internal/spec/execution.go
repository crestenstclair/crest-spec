package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/execution"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type ExecutionStartOptions struct {
	AttemptID          string
	ProtocolVersion    string
	IdempotencyKey     string
	ContextHash        string
	Role               string
	HostName           string
	HostVersion        string
	Provider           string
	Model              string
	InferenceConfig    map[string]any
	AgentConfig        map[string]any
	Tools              []store.ExecutionTool
	TemplateHashes     map[string]string
	SystemInstructions string
	HostSessionID      string
}

type CommitMetadata struct {
	DurationMS    int64
	InputTokens   int64
	OutputTokens  int64
	CostUSD       float64
	HostCommitRef string
}

type ExecutionReportOptions struct {
	AttemptID    string
	Status       string
	Reason       string
	Details      map[string]any
	DurationMS   int64
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
	Category     string
	Confidence   float64
}

// FailureClassifyOptions represents an append-only advisory or human failure
// classification. Human corrections use OverrideOf to preserve the original
// classification instead of rewriting history.
type FailureClassifyOptions struct {
	AttemptID         string
	Category          string
	Origin            string
	Confidence        float64
	EvidenceSource    string
	EvidenceReference string
	Evidence          string
	OverrideOf        string
	GoalIDs           []string
}

func (s *Spec) StartExecution(ctx context.Context, opts ExecutionStartOptions) (*store.ExecutionManifest, error) {
	if opts.AttemptID == "" {
		return nil, fmt.Errorf("attempt_id is required")
	}
	manifest, err := s.store.GetContextManifestByAttempt(ctx, opts.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("execution start: context attempt: %w", err)
	}
	if manifest.Blocked {
		return nil, fmt.Errorf("execution start: context attempt is blocked: %s", manifest.BlockedReason)
	}
	policy, err := execution.LookupRole(manifest.Attempt.Role)
	if err != nil {
		return nil, fmt.Errorf("execution start: %w", err)
	}
	if opts.Model == "" {
		opts.Model = s.cfg.GenerateModel
	}
	if opts.Role != "" && opts.Role != manifest.Attempt.Role {
		return nil, fmt.Errorf("execution start: role %s does not match prepared role %s", opts.Role, manifest.Attempt.Role)
	}
	session, err := s.store.GetSession(manifest.Attempt.SessionID)
	if err != nil {
		return nil, fmt.Errorf("execution start: session: %w", err)
	}
	action, ok := actionForSession(session, manifest.Attempt.ResourceID)
	if !ok || action.OperationID != manifest.Attempt.PlanOperationID {
		return nil, fmt.Errorf("execution start: persisted plan operation is unavailable")
	}
	return s.store.StartExecution(ctx, store.ExecutionManifest{
		AttemptID: opts.AttemptID, ContextManifestID: manifest.ID,
		ProtocolVersion: opts.ProtocolVersion, IdempotencyKey: opts.IdempotencyKey,
		PlanOperationID: manifest.Attempt.PlanOperationID, Role: manifest.Attempt.Role,
		RolePolicyVersion: manifest.Attempt.RolePolicyVersion, ContextPolicy: policy.ContextPolicy,
		HostName: opts.HostName, HostVersion: opts.HostVersion, Provider: opts.Provider, Model: opts.Model,
		InferenceConfig: opts.InferenceConfig, AgentConfig: opts.AgentConfig, Tools: opts.Tools,
		TemplateHashes: opts.TemplateHashes, SystemInstructions: opts.SystemInstructions,
		ContextHash: opts.ContextHash, HostSessionID: opts.HostSessionID,
		GoalIDs: action.Goals, CapabilityIDs: action.Capabilities,
	})
}

func (s *Spec) ReportExecution(ctx context.Context, opts ExecutionReportOptions) (*store.ExecutionManifest, error) {
	manifest, err := s.store.GetExecutionManifestByAttempt(ctx, opts.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("report execution: %w", err)
	}
	policy, err := execution.LookupRole(manifest.Role)
	if err != nil {
		return nil, err
	}
	status := execution.Status(opts.Status)
	switch status {
	case execution.StatusCompleted:
		if !policy.Allows(execution.ActionCompleteAdvisory) {
			return nil, fmt.Errorf("report execution: role %s must submit candidate files", manifest.Role)
		}
	case execution.StatusCancelled, execution.StatusFailed, execution.StatusTimedOut:
	default:
		return nil, fmt.Errorf("report execution: status must be completed, cancelled, failed, or timed_out")
	}
	transition := store.ExecutionTransition{
		ExecutionID: manifest.ID, ToStatus: status, Reason: opts.Reason, Details: opts.Details,
		DurationMS: opts.DurationMS, InputTokens: opts.InputTokens, OutputTokens: opts.OutputTokens, CostUSD: opts.CostUSD,
	}
	if status == execution.StatusFailed || status == execution.StatusTimedOut {
		category := execution.FailureCategory(opts.Category)
		if category == "" {
			category = execution.FailureHostFailure
		}
		confidence := opts.Confidence
		if confidence == 0 {
			confidence = 1
		}
		transition.Failure = &store.FailureClassificationWrite{
			Category: category, Origin: "host", Confidence: confidence,
			EvidenceSource: "execution_report", EvidenceReference: manifest.ID, Evidence: opts.Reason,
		}
		transition.HandoffTargetRole = permittedFailureHandoff(policy, execution.RouteFailure(category).NextRole)
	}
	return s.store.TransitionExecution(ctx, transition)
}

func (s *Spec) ExecutionRoles() []execution.RolePolicy {
	return execution.Roles()
}

func (s *Spec) InspectExecution(ctx context.Context, id, attemptID string) (*store.ExecutionManifest, error) {
	if (id == "") == (attemptID == "") {
		return nil, fmt.Errorf("provide exactly one of execution_id or attempt_id")
	}
	if id != "" {
		return s.store.GetExecutionManifest(ctx, id)
	}
	return s.store.GetExecutionManifestByAttempt(ctx, attemptID)
}

func (s *Spec) ListExecutions(ctx context.Context, limit int) ([]store.ExecutionManifest, error) {
	return s.store.ListExecutionManifests(ctx, limit)
}

// RecoverExecutions marks active host attempts older than the configured
// policy threshold as timed out. It records infrastructure provenance and
// deliberately does not infer a model failure from a missing host heartbeat.
func (s *Spec) RecoverExecutions(ctx context.Context, before time.Time) (int, error) {
	manifests, err := s.store.ListStaleExecutionManifests(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("recover executions: list stale attempts: %w", err)
	}
	recovered := 0
	for index := range manifests {
		manifest := &manifests[index]
		reason := fmt.Sprintf("host execution exceeded recovery threshold before %s", before.UTC().Format(time.RFC3339Nano))
		_, err := s.store.TransitionExecution(ctx, store.ExecutionTransition{
			ExecutionID: manifest.ID, ToStatus: execution.StatusTimedOut, Reason: reason,
			Details: map[string]any{"recovery_threshold": before.UTC().Format(time.RFC3339Nano)},
			Failure: &store.FailureClassificationWrite{
				Category: execution.FailureHostFailure, Origin: "engine", Confidence: 1,
				EvidenceSource: "execution_recovery_timeout", EvidenceReference: manifest.ID,
				Evidence: reason, GoalIDs: manifest.GoalIDs,
			},
		})
		if err != nil {
			return recovered, fmt.Errorf("recover executions: attempt %s: %w", manifest.AttemptID, err)
		}
		recovered++
	}
	return recovered, nil
}

func (s *Spec) ListFailures(ctx context.Context, attemptID string, limit int) ([]store.FailureClassification, error) {
	if attemptID != "" {
		return s.store.ListFailureClassificationsByAttempt(ctx, attemptID)
	}
	return s.store.ListFailureClassifications(ctx, limit)
}

func (s *Spec) ListHandoffs(ctx context.Context, attemptID string, limit int) ([]store.AttemptHandoff, error) {
	if attemptID != "" {
		return s.store.ListAttemptHandoffsBySource(ctx, attemptID)
	}
	return s.store.ListPendingAttemptHandoffs(ctx, limit)
}

func (s *Spec) ClassifyFailure(ctx context.Context, opts FailureClassifyOptions) (*store.FailureClassification, error) {
	if opts.Origin != "host" && opts.Origin != "human" {
		return nil, fmt.Errorf("classify failure: origin must be host or human")
	}
	if opts.OverrideOf != "" && opts.Origin != "human" {
		return nil, fmt.Errorf("classify failure: only a human classification may override an earlier record")
	}
	category := execution.FailureCategory(opts.Category)
	if err := execution.ValidateFailureCategory(category); err != nil {
		return nil, fmt.Errorf("classify failure: %w", err)
	}
	manifest, manifestErr := s.store.GetExecutionManifestByAttempt(ctx, opts.AttemptID)
	if opts.Origin == "host" {
		if manifestErr != nil {
			return nil, fmt.Errorf("classify failure: governed execution is required: %w", manifestErr)
		}
		policy, err := execution.LookupRole(manifest.Role)
		if err != nil || !policy.Allows(execution.ActionClassifyFailure) {
			return nil, fmt.Errorf("classify failure: role %s cannot classify failures", manifest.Role)
		}
	}
	confidence := opts.Confidence
	if confidence == 0 {
		confidence = 1
	}
	executionID := ""
	goals := opts.GoalIDs
	if manifestErr == nil {
		executionID = manifest.ID
		if len(goals) == 0 {
			goals = manifest.GoalIDs
		}
	}
	return s.store.CreateFailureClassification(ctx, store.FailureClassificationWrite{
		AttemptID: opts.AttemptID, ExecutionID: executionID, Category: category,
		Origin: opts.Origin, Confidence: confidence, EvidenceSource: opts.EvidenceSource,
		EvidenceReference: opts.EvidenceReference, Evidence: opts.Evidence,
		OverrideOf: opts.OverrideOf, GoalIDs: goals,
	})
}

// ResolveFailure records an explicit resolution. Accepted descendant attempts
// resolve ancestor failures automatically; this operation covers human fixes
// such as specification, validation, or infrastructure repair.
func (s *Spec) ResolveFailure(ctx context.Context, id, resolution, resolvedByAttempt string) error {
	return s.store.ResolveFailureClassification(ctx, id, resolution, resolvedByAttempt)
}

// CommitAttempt is the only governed candidate-commit path. Identity and model
// are derived from the immutable attempt/execution chain, never caller fields.
func (s *Spec) CommitAttempt(ctx context.Context, attemptID string, files []CommitFile, notes string, metadata CommitMetadata) (*CommitResult, error) {
	executionManifest, err := s.store.GetExecutionManifestByAttempt(ctx, attemptID)
	if err != nil {
		return nil, fmt.Errorf("commit attempt: execution_start is required: %w", err)
	}
	policy, err := execution.LookupRole(executionManifest.Role)
	if err != nil || !policy.Allows(execution.ActionSubmitCandidate) {
		return nil, fmt.Errorf("commit attempt: role %s cannot submit candidate files", executionManifest.Role)
	}
	contextManifest, err := s.store.GetContextManifestByAttempt(ctx, attemptID)
	if err != nil {
		return nil, fmt.Errorf("commit attempt: context: %w", err)
	}
	normalizedFiles, err := s.normalizeCommitFiles(files)
	if err != nil {
		return nil, err
	}
	candidateFiles, err := s.snapshotCommitFiles(normalizedFiles)
	if err != nil {
		return nil, err
	}
	candidate, err := s.store.SubmitCandidate(ctx, executionManifest.ID, candidateFiles)
	if err != nil {
		return nil, err
	}
	if executionManifest.Status == string(execution.StatusAccepted) || executionManifest.Status == string(execution.StatusRejected) {
		result := &CommitResult{
			Committed: executionManifest.Status == string(execution.StatusAccepted), AttemptID: attemptID,
			ExecutionID: executionManifest.ID, CandidateID: candidate.ID,
		}
		if failures, listErr := s.store.ListFailureClassificationsByAttempt(ctx, attemptID); listErr == nil && len(failures) > 0 {
			result.FailureCategory = failures[len(failures)-1].Category
		}
		return result, nil
	}
	if err := s.writeChangedFiles(normalizedFiles); err != nil {
		s.failExecution(ctx, executionManifest, contextManifest, execution.FailureToolFailure, err.Error())
		return nil, err
	}
	if _, err := s.store.TransitionExecution(ctx, store.ExecutionTransition{
		ExecutionID: executionManifest.ID, ToStatus: execution.StatusValidating,
		Details: map[string]any{"candidate_id": candidate.ID},
	}); err != nil {
		return nil, err
	}

	sess, err := s.store.GetSession(contextManifest.Attempt.SessionID)
	if err != nil {
		s.failExecution(ctx, executionManifest, contextManifest, execution.FailureToolFailure, err.Error())
		return nil, fmt.Errorf("commit attempt: session: %w", err)
	}
	planResult, err := s.Plan(ctx)
	if err != nil {
		s.failExecution(ctx, executionManifest, contextManifest, execution.FailureArchitecturalInconsistency, err.Error())
		return nil, fmt.Errorf("commit attempt: plan: %w", err)
	}
	resourceID := contextManifest.Attempt.ResourceID
	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		s.failExecution(ctx, executionManifest, contextManifest, execution.FailureMissingProjectIntent, "resource is absent from current specification")
		return nil, fmt.Errorf("commit attempt: resource not found: %s", resourceID)
	}
	action, _ := actionForSession(sess, resourceID)
	validations := s.executeCommitValidations(ctx, planResult.Registry.Project.Name, resource, validationProvenance{
		SessionID: contextManifest.Attempt.SessionID, AttemptID: contextManifest.Attempt.ID,
		ExecutionID: executionManifest.ID,
	})
	if failureMessage := firstFailureMessage(validations); failureMessage != "" {
		category := execution.FailureImplementationError
		if action.Category == "integrative" {
			category = execution.FailureIntegrationError
		}
		if strings.HasPrefix(failureMessage, "validation could not run:") {
			category = execution.FailureInvalidValidation
		}
		evidence, _ := json.Marshal(validations)
		route := execution.RouteFailure(category)
		_, finalizeErr := s.store.FinalizeCandidate(ctx, store.CandidateDisposition{
			ExecutionID: executionManifest.ID, Reason: failureMessage,
			Details: map[string]any{"validations": validations}, Attempts: contextManifest.Attempt.RetryNumber,
			DurationMS: metadata.DurationMS, InputTokens: metadata.InputTokens,
			OutputTokens: metadata.OutputTokens, CostUSD: metadata.CostUSD, HostCommitRef: metadata.HostCommitRef,
			GoalProgress: executionGoalProgress(action, "blocked", failureMessage),
			Failure: &store.FailureClassificationWrite{
				Category: category, Origin: "engine", Confidence: 1, EvidenceSource: "mechanical_validation",
				EvidenceReference: executionManifest.ID, Evidence: string(evidence), GoalIDs: action.Goals,
			},
			HandoffRole: permittedFailureHandoff(policy, route.NextRole),
		})
		if finalizeErr != nil {
			return nil, finalizeErr
		}
		s.markAmendmentVerification(resourceID, resource, false)
		return &CommitResult{
			Committed: false, Validations: validations, AttemptID: attemptID,
			ExecutionID: executionManifest.ID, CandidateID: candidate.ID, FailureCategory: string(category),
		}, nil
	}

	var hashes map[string]string
	_ = json.Unmarshal([]byte(sess.HashesJSON), &hashes)
	declaration, _ := json.Marshal(resource.Declaration)
	declarationHash := fmt.Sprintf("%x", sha256.Sum256(declaration))
	acceptedFiles := make([]store.GeneratedFile, 0, len(candidate.Files))
	for _, file := range candidate.Files {
		acceptedFiles = append(acceptedFiles, store.GeneratedFile{
			Path: file.Path, ResourceID: resourceID, ContentHash: file.ContentHash,
			PromptHash: executionManifest.ContextHash, Model: executionManifest.Model, CreatedAt: time.Now().UTC(),
		})
	}
	dependencies := make([]store.Dependency, 0, len(resource.Dependencies))
	for _, dependency := range resource.Dependencies {
		dependencies = append(dependencies, store.Dependency{SourceID: resourceID, TargetID: dependency.TargetID, Kind: dependency.Kind})
	}
	_, err = s.store.FinalizeCandidate(ctx, store.CandidateDisposition{
		ExecutionID: executionManifest.ID, Accepted: true, Reason: "mechanical validations passed",
		Details: map[string]any{"validations": validations}, Attempts: contextManifest.Attempt.RetryNumber,
		Resource: &store.Resource{
			ID: resourceID, Kind: resource.Kind, ContextName: resource.ContextName,
			DeclarationHash: declarationHash, EffectiveHash: hashes[resourceID], Model: executionManifest.Model,
		},
		Files: acceptedFiles, Dependencies: dependencies, Notes: notes,
		DurationMS: metadata.DurationMS, InputTokens: metadata.InputTokens,
		OutputTokens: metadata.OutputTokens, CostUSD: metadata.CostUSD, HostCommitRef: metadata.HostCommitRef,
		GoalProgress: executionGoalProgress(action, "advanced", "accepted resource candidate"),
	})
	if err != nil {
		return nil, err
	}
	s.markAmendmentVerification(resourceID, resource, true)
	return &CommitResult{
		Committed: true, Validations: validations, AttemptID: attemptID,
		ExecutionID: executionManifest.ID, CandidateID: candidate.ID,
	}, nil
}

func executionGoalProgress(action planpkg.PlannedAction, status, reason string) map[string]any {
	goals := make([]map[string]string, 0, len(action.Goals))
	for _, goalID := range action.Goals {
		goals = append(goals, map[string]string{"goal_id": goalID, "status": status, "reason": reason})
	}
	capabilities := make([]map[string]string, 0, len(action.Capabilities))
	for _, capabilityID := range action.Capabilities {
		capabilities = append(capabilities, map[string]string{"capability_id": capabilityID, "status": status, "reason": reason})
	}
	return map[string]any{"goals": goals, "capabilities": capabilities}
}

func (s *Spec) snapshotCommitFiles(files []CommitFile) ([]store.CandidateFile, error) {
	result := make([]store.CandidateFile, 0, len(files))
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		content := file.Content
		intent := "modify"
		if file.Content == "" {
			data, err := s.fs.ReadFile(s.projectFilePath(file.Path))
			if err != nil {
				return nil, fmt.Errorf("snapshot candidate claim %s: %w", file.Path, err)
			}
			content = string(data)
			intent = "preserve"
		} else if _, err := s.fs.ReadFile(file.Path); err != nil {
			intent = "create"
		}
		result = append(result, store.CandidateFile{Path: filepath.Clean(file.Path), Content: content, WriteIntent: intent})
	}
	return result, nil
}

func (s *Spec) normalizeCommitFiles(files []CommitFile) ([]CommitFile, error) {
	root := filepath.Clean(filepath.Dir(s.cfg.SpecDir))
	result := make([]CommitFile, 0, len(files))
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		path := filepath.Clean(file.Path)
		if filepath.IsAbs(path) {
			relative, err := filepath.Rel(root, path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("commit attempt: path %q is outside project root", file.Path)
			}
			path = relative
		}
		if path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("commit attempt: path %q is outside project root", file.Path)
		}
		result = append(result, CommitFile{Path: filepath.ToSlash(path), Content: file.Content})
	}
	return result, nil
}

func (s *Spec) projectFilePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(s.cfg.SpecDir), filepath.FromSlash(path))
}

func (s *Spec) executeCommitValidations(ctx context.Context, projectName string, resource cuepkg.Resource, provenance validationProvenance) []ValidationResult {
	validations := resourceValidations(resource)
	if len(validations) == 0 {
		return nil
	}
	return s.executeAndRecordValidations(ctx, projectName, validations, filepath.Dir(s.cfg.SpecDir), provenance)
}

func actionForSession(session *store.Session, resourceID string) (planpkg.PlannedAction, bool) {
	var actions []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(session.PlanJSON), &actions); err != nil {
		return planpkg.PlannedAction{}, false
	}
	return contextPlanAction(actions, resourceID)
}

func permittedFailureHandoff(policy execution.RolePolicy, target execution.Role) string {
	if target != "" && policy.AllowsHandoff(target) {
		return string(target)
	}
	return ""
}

func (s *Spec) failExecution(ctx context.Context, manifest *store.ExecutionManifest, contextManifest *store.ContextManifest, category execution.FailureCategory, reason string) {
	policy, _ := execution.LookupRole(manifest.Role)
	route := execution.RouteFailure(category)
	_, _ = s.store.TransitionExecution(ctx, store.ExecutionTransition{
		ExecutionID: manifest.ID, ToStatus: execution.StatusFailed, Reason: reason,
		Failure: &store.FailureClassificationWrite{
			Category: category, Origin: "engine", Confidence: 1,
			EvidenceSource: "engine_error", EvidenceReference: manifest.ID, Evidence: reason,
		},
		HandoffTargetRole: permittedFailureHandoff(policy, route.NextRole),
	})
	_ = s.store.UpdateSessionResourceState(
		contextManifest.Attempt.SessionID, contextManifest.Attempt.ResourceID,
		string(StateErrored), reason, "", contextManifest.Attempt.RetryNumber, "",
	)
}
