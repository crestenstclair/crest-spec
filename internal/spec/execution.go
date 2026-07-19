package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
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
	if manifest.Attempt.Status == "abandoned" {
		return nil, fmt.Errorf("execution start: corrective_action=rebuild_context; prepared attempt was abandoned before execution registration")
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
	currentPlan, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("execution start: current plan: %w", err)
	}
	if !operationInputsMatchCurrent(action, currentPlan) {
		reason := "plan inputs changed after context preparation"
		if err := s.invalidatePreparedContext(ctx, opts.AttemptID, reason, "execution_start_plan_inputs_changed"); err != nil {
			return nil, fmt.Errorf("execution start: invalidate stale context: %w", err)
		}
		return nil, fmt.Errorf("execution start: %s; corrective_action=rebuild_context; the prepared attempt was abandoned", reason)
	}
	if err := s.validateSelectedRepositorySources(manifest); err != nil {
		reason := fmt.Sprintf("repository context changed after preparation: %v", err)
		if recoveryErr := s.invalidatePreparedContext(ctx, opts.AttemptID, reason, "execution_start_stale_context"); recoveryErr != nil {
			return nil, fmt.Errorf("execution start: invalidate stale repository context: %w", recoveryErr)
		}
		return nil, fmt.Errorf("execution start: %s; corrective_action=rebuild_context; the prepared attempt was abandoned", reason)
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
	updated, err := s.store.TransitionExecution(ctx, transition)
	if err != nil {
		return nil, err
	}
	return updated, nil
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

// RecoverExecutions applies separate recovery policies to work that has not
// registered an execution and work that has. Dispatches are expected to reach
// execution_start quickly, while a registered model execution may legitimately
// run much longer. A zero cutoff disables recovery for that lifecycle class.
func (s *Spec) RecoverExecutions(
	ctx context.Context,
	dispatchBefore time.Time,
	executionBefore time.Time,
) (int, error) {
	recovered := 0
	if !dispatchBefore.IsZero() {
		preExecution, err := s.store.RecoverStalePreExecutionAttempts(ctx, dispatchBefore)
		if err != nil {
			return 0, fmt.Errorf("recover executions: prepared contexts: %w", err)
		}
		recovered = len(preExecution)
	}
	if executionBefore.IsZero() {
		return recovered, nil
	}
	manifests, err := s.store.ListStaleExecutionManifests(ctx, executionBefore)
	if err != nil {
		return recovered, fmt.Errorf("recover executions: list stale attempts: %w", err)
	}
	for index := range manifests {
		manifest := &manifests[index]
		reason := fmt.Sprintf("host execution exceeded recovery threshold before %s", executionBefore.UTC().Format(time.RFC3339Nano))
		_, err := s.store.TransitionExecution(ctx, store.ExecutionTransition{
			ExecutionID: manifest.ID, ToStatus: execution.StatusTimedOut, Reason: reason,
			Details: map[string]any{"recovery_threshold": executionBefore.UTC().Format(time.RFC3339Nano)},
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

// invalidatePreparedContext performs the pre-execution stale-context edge. It
// is intentionally safe when another caller wins: the store owns the CAS and
// immutable audit transaction, while this caller still returns the original
// provenance failure instead of starting against stale repository bytes.
func (s *Spec) invalidatePreparedContext(
	ctx context.Context,
	attemptID string,
	reason string,
	evidenceSource string,
) error {
	_, err := s.store.AbandonPreparedContext(ctx, store.PreparedContextAbandonment{
		AttemptID: attemptID, Reason: reason, EvidenceSource: evidenceSource,
	})
	return err
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
	commit := &commitAttemptState{
		attemptID: attemptID, notes: notes, metadata: metadata,
		execution: executionManifest, context: contextManifest, policy: policy,
	}
	if result, terminal, err := s.terminalCommitResult(ctx, executionManifest, attemptID); terminal || err != nil {
		return result, err
	}
	normalizedFiles, err := s.normalizeCommitFiles(files)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(normalizedFiles))
	for _, file := range normalizedFiles {
		paths = append(paths, file.Path)
	}
	releaseWorkspace, err := s.commitCoordinator().Acquire(ctx, paths)
	if err != nil {
		return nil, err
	}
	defer releaseWorkspace()
	releaseProject, err := s.projectWorkspace().AcquireCommit(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseProject()
	if _, err := s.recoverWorkspaceCommitsLocked(ctx); err != nil {
		return nil, fmt.Errorf("commit attempt: recover interrupted workspace commits: %w", err)
	}
	executionManifest, err = s.store.GetExecutionManifestByAttempt(ctx, attemptID)
	if err != nil {
		return nil, fmt.Errorf("commit attempt: refresh execution: %w", err)
	}
	if result, terminal, err := s.terminalCommitResult(ctx, executionManifest, attemptID); terminal || err != nil {
		return result, err
	}
	if execution.Status(executionManifest.Status).Terminal() {
		return nil, fmt.Errorf(
			"commit attempt: execution is %s after interrupted workspace recovery; prepare a fresh context",
			executionManifest.Status,
		)
	}
	commit.execution = executionManifest
	if err := s.loadCommitTarget(ctx, commit); err != nil {
		return nil, err
	}
	if err := s.validateUnrepresentedCandidatePaths(commit.context, commit.resourceID, normalizedFiles); err != nil {
		reason := fmt.Sprintf("candidate repository context changed after preparation: %v; corrective_action=rebuild_context", err)
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureStaleContext, reason)
		return nil, fmt.Errorf("commit attempt: %s", reason)
	}
	if err := s.validateGovernedCommitTopologies(normalizedFiles); err != nil {
		reason := fmt.Sprintf("candidate workspace topology is unsafe: %v", err)
		s.failExecution(ctx, commit.execution, commit.context, execution.FailureToolFailure, reason)
		return nil, fmt.Errorf("commit attempt: %s", reason)
	}
	if err := s.submitAndStageCommitCandidate(ctx, commit, normalizedFiles); err != nil {
		return nil, err
	}
	// Resource-declared commands are not necessarily isolated: a focused Cargo
	// test, for example, still compiles every crate consumer. Running it here can
	// reject a correct provider while planned downstream resources are stale.
	// Stage the immutable candidate now and run every resource command once in
	// the affected-closure batch scheduled by VerifyWave.
	commit.validationDeferred = true
	return s.acceptCommitCandidate(ctx, commit)
}

func (s *Spec) terminalCommitResult(ctx context.Context, manifest *store.ExecutionManifest, attemptID string) (*CommitResult, bool, error) {
	if manifest.Status != string(execution.StatusAccepted) && manifest.Status != string(execution.StatusRejected) {
		return nil, false, nil
	}
	candidate, err := s.store.GetCandidateSetByAttempt(ctx, attemptID)
	if err != nil {
		return nil, true, fmt.Errorf("commit attempt: terminal candidate: %w", err)
	}
	result := &CommitResult{
		Committed: manifest.Status == string(execution.StatusAccepted), AttemptID: attemptID,
		ExecutionID: manifest.ID, CandidateID: candidate.ID,
	}
	if contextManifest, contextErr := s.store.GetContextManifestByAttempt(ctx, attemptID); contextErr == nil {
		if resource, resourceErr := s.store.GetSessionResource(
			contextManifest.Attempt.SessionID, contextManifest.Attempt.ResourceID,
		); resourceErr == nil {
			result.ValidationDeferred = resource.Phase == "integration_pending"
		}
	}
	failures, err := s.store.ListFailureClassificationsByAttempt(ctx, attemptID)
	if err != nil {
		return nil, true, fmt.Errorf("commit attempt: terminal failure classifications: %w", err)
	}
	if len(failures) > 0 {
		result.FailureCategory = failures[len(failures)-1].Category
	}
	return result, true, nil
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
		path, err := s.projectWorkspace().Resolve(file.Path)
		if err != nil {
			return nil, fmt.Errorf("snapshot candidate path %s: %w", file.Path, err)
		}
		if file.Content == "" {
			data, err := s.fs.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("snapshot candidate claim %s: %w", file.Path, err)
			}
			content = string(data)
			intent = "preserve"
		} else if _, err := s.fs.ReadFile(path); err != nil {
			intent = "create"
		}
		if err := store.ValidateCandidateRepositoryBytes(file.Path, []byte(content)); err != nil {
			return nil, fmt.Errorf("snapshot candidate %s: %w", file.Path, err)
		}
		result = append(result, store.CandidateFile{Path: filepath.ToSlash(filepath.Clean(file.Path)), Content: content, WriteIntent: intent})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	for index := range result {
		result[index].ContentHash = hashWorkspaceContent([]byte(result[index].Content))
		result[index].ByteSize = len([]byte(result[index].Content))
		result[index].Ordinal = index
	}
	return result, nil
}

func (s *Spec) normalizeCommitFiles(files []CommitFile) ([]CommitFile, error) {
	result := make([]CommitFile, 0, len(files))
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		path, err := s.projectWorkspace().Relative(file.Path)
		if err != nil {
			return nil, fmt.Errorf("commit attempt: %w", err)
		}
		result = append(result, CommitFile{Path: path, Content: file.Content})
	}
	return result, nil
}

func (s *Spec) projectFilePath(path string) (string, error) {
	return s.projectWorkspace().Resolve(path)
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
}
