package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/engine"
	graphpkg "github.com/crestenstclair/crest-spec/internal/graph"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type BeginOpts struct {
	Target string
	Force  bool
	Model  string
}

type BeginResult struct {
	SessionID       string
	ApplyID         string
	Plan            []planpkg.PlannedAction
	Waves           [][]string
	Instructions    string
	DriftActions    []planpkg.PlannedAction
	PendingDestroys []planpkg.PlannedAction
}

type DestroyedResource struct {
	ResourceID   string
	DeletedFiles []string
}

type NextResult struct {
	Done      bool
	WaveIndex int
	Resources []ResourceStatus
}

type ContextResult struct {
	SystemPrompt string
	Prompt       string
	Instructions string
}

type CommitFile struct {
	Path    string
	Content string
}

type CommitResult struct {
	Committed   bool
	Validations []ValidationResult
}

type FinishResult struct {
	Committed int
	Skipped   int
	Errored   int
}

func (s *Spec) Begin(ctx context.Context, opts BeginOpts) (*BeginResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	actions := planResult.Actions
	waves := planResult.Waves

	// Force: when a target is specified and force is true, ensure the target
	// appears in the plan even if its hashes haven't changed.
	if opts.Target != "" && opts.Force {
		actions = forceTargetIntoActions(actions, opts.Target, planResult.Registry)
	}

	// Target: filter the plan to only include the target resource and its
	// transitive dependencies (ancestors in the dependency graph).
	if opts.Target != "" {
		if !planResult.Graph.Has(opts.Target) {
			return nil, fmt.Errorf("target resource not found: %s", opts.Target)
		}
		actions, waves = filterForTarget(actions, waves, opts.Target, planResult.Graph)
	}

	if len(actions) == 0 {
		return &BeginResult{
			Instructions: "No changes detected. The spec is up to date.",
		}, nil
	}

	applyID := uuid.NewString()
	sessionID := uuid.NewString()

	specJSON, _ := json.Marshal(actions)
	specHash := fmt.Sprintf("%x", sha256.Sum256(specJSON))

	if err := s.store.CreateApply(applyID, specHash); err != nil {
		return nil, fmt.Errorf("create apply: %w", err)
	}

	if err := s.store.AcquireLock(sessionID, os.Getpid()); err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	planJSON, _ := json.Marshal(actions)
	wavesJSON, _ := json.Marshal(waves)
	hashesJSON, _ := json.Marshal(planResult.Hashes)

	if err := s.store.CreateSession(store.Session{
		ID:         sessionID,
		ApplyID:    applyID,
		PlanJSON:   string(planJSON),
		WavesJSON:  string(wavesJSON),
		HashesJSON: string(hashesJSON),
	}); err != nil {
		s.store.ReleaseLock()
		return nil, fmt.Errorf("create session: %w", err)
	}

	driftActions, destroyActions, otherActions := partitionActions(actions)

	s.seedSessionResources(sessionID, waves, otherActions, driftActions)

	return &BeginResult{
		SessionID:       sessionID,
		ApplyID:         applyID,
		Plan:            otherActions,
		Waves:           waves,
		Instructions:    orchestratorInstructions(),
		DriftActions:    driftActions,
		PendingDestroys: destroyActions,
	}, nil
}

func partitionActions(actions []planpkg.PlannedAction) (drift, destroy, other []planpkg.PlannedAction) {
	for _, a := range actions {
		switch a.Kind {
		case planpkg.ActionDrift:
			drift = append(drift, a)
		case planpkg.ActionDestroy:
			destroy = append(destroy, a)
		default:
			other = append(other, a)
		}
	}
	return
}

func (s *Spec) executeDestroys(destroyActions []planpkg.PlannedAction, applyID string) []DestroyedResource {
	var destroyed []DestroyedResource
	for _, a := range destroyActions {
		actionID := uuid.NewString()
		s.store.CreateApplyAction(actionID, applyID, a.ResourceID, "destroy")

		var deletedFiles []string
		files, _ := s.store.GetGeneratedFiles(a.ResourceID)
		for _, f := range files {
			if err := s.fs.Remove(f.Path); err == nil {
				deletedFiles = append(deletedFiles, f.Path)
			}
		}
		s.store.DeleteGeneratedFiles(a.ResourceID)
		s.store.DeleteDependencies(a.ResourceID)
		s.store.DeleteResource(a.ResourceID)
		s.store.UpdateApplyAction(actionID, "destroyed", "")

		destroyed = append(destroyed, DestroyedResource{
			ResourceID:   a.ResourceID,
			DeletedFiles: deletedFiles,
		})
	}
	return destroyed
}

func (s *Spec) ConfirmDestroys(ctx context.Context, sessionID string, resourceIDs []string) ([]DestroyedResource, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	confirmed := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		confirmed[id] = true
	}

	var plan []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(sess.PlanJSON), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}

	var destroyActions []planpkg.PlannedAction
	for _, a := range plan {
		if a.Kind == planpkg.ActionDestroy && confirmed[a.ResourceID] {
			destroyActions = append(destroyActions, a)
		}
	}

	return s.executeDestroys(destroyActions, sess.ApplyID), nil
}

func (s *Spec) seedSessionResources(sessionID string, waves [][]string, actionSets ...[]planpkg.PlannedAction) {
	waveMap := make(map[string]int)
	for i, wave := range waves {
		for _, id := range wave {
			waveMap[id] = i
		}
	}
	for _, actions := range actionSets {
		for _, a := range actions {
			s.store.UpsertSessionResource(store.SessionResource{
				SessionID:  sessionID,
				ResourceID: a.ResourceID,
				State:      string(StatePending),
				WaveIndex:  waveMap[a.ResourceID],
				MaxRetries: s.cfg.MaxRetries,
			})
		}
	}
}

func (s *Spec) Next(ctx context.Context, sessionID string) (*NextResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var waves [][]string
	if err := json.Unmarshal([]byte(sess.WavesJSON), &waves); err != nil {
		return nil, fmt.Errorf("unmarshal waves: %w", err)
	}

	if sess.CurrentWave >= len(waves) {
		return &NextResult{Done: true, WaveIndex: sess.CurrentWave}, nil
	}

	var plan []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(sess.PlanJSON), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}

	planSet := make(map[string]planpkg.PlannedAction)
	for _, a := range plan {
		planSet[a.ResourceID] = a
	}

	// Build a lookup of session_resource states from SQLite
	allResources, _ := s.store.ListSessionResources(sessionID)
	stateMap := make(map[string]store.SessionResource)
	for _, r := range allResources {
		stateMap[r.ResourceID] = r
	}

	for w := sess.CurrentWave; w < len(waves); w++ {
		resources := s.pendingResourcesInWave(waves[w], planSet, stateMap, w)
		if len(resources) > 0 {
			if w != sess.CurrentWave {
				s.store.UpdateSession(sessionID, sess.Status, w)
			}
			return &NextResult{
				Done:      false,
				WaveIndex: w,
				Resources: resources,
			}, nil
		}
	}

	return &NextResult{Done: true, WaveIndex: len(waves)}, nil
}

func (s *Spec) pendingResourcesInWave(
	waveIDs []string,
	planSet map[string]planpkg.PlannedAction,
	stateMap map[string]store.SessionResource,
	waveIndex int,
) []ResourceStatus {
	var resources []ResourceStatus
	for _, id := range waveIDs {
		action, inPlan := planSet[id]
		if !inPlan {
			continue
		}

		sr, tracked := stateMap[id]
		if tracked && ResourceState(sr.State).IsTerminal() {
			continue
		}

		rs := ResourceStatus{
			ResourceID: id,
			State:      StatePending,
			WaveIndex:  waveIndex,
			MaxRetries: s.cfg.MaxRetries,
			Notes:      action.Reason,
		}
		if tracked {
			rs.State = ResourceState(sr.State)
			rs.Attempts = sr.Attempts
			if sr.LastError != "" {
				rs.Error = &ErrorContext{
					Message:    sr.LastError,
					RetryCount: sr.Attempts,
					MaxRetries: sr.MaxRetries,
				}
			}
		}
		resources = append(resources, rs)
	}
	return resources
}

func (s *Spec) Context(ctx context.Context, sessionID, resourceID string) (*ContextResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var plan []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(sess.PlanJSON), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}

	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}

	// Transition resource to "dispatched" in session_resources
	sr, _ := s.store.GetSessionResource(sessionID, resourceID)
	if sr != nil {
		s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateDispatched), sr.LastError, sr.LastOutput, sr.Attempts, sr.JobID)
	}

	systemPrompt := promptpkg.BuildSystemPrompt(planResult.Registry.Project)
	resourcePrompt := promptpkg.BuildResourcePrompt(resource, planResult.Registry)

	runtimeCtx, _ := s.buildRuntimeContext(resource, planResult.Registry, sess.ApplyID)
	fullPrompt := promptpkg.InjectRuntimeContext(resourcePrompt, runtimeCtx)

	return &ContextResult{
		SystemPrompt: systemPrompt,
		Prompt:       fullPrompt,
		Instructions: dispatchInstructions(resourceID),
	}, nil
}

func (s *Spec) Commit(ctx context.Context, sessionID, resourceID string, files []CommitFile, notes string) (*CommitResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	if err := s.writeChangedFiles(files); err != nil {
		return nil, err
	}

	// Transition to "completed" — files are written, validation pending.
	// This matches the Terraform model: apply writes, then verifies, then marks done.
	sr, _ := s.store.GetSessionResource(sessionID, resourceID)
	currentAttempts := 0
	if sr != nil {
		currentAttempts = sr.Attempts
	}
	currentAttempts++
	s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateCompleted), "", "", currentAttempts, "")

	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan for commit: %w", err)
	}

	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}

	validationResults, rejected := s.runCommitValidations(ctx, sess, sessionID, resourceID, resource, files, planResult, currentAttempts)
	if rejected != nil {
		return rejected, nil
	}

	s.persistCommittedResource(sess, sessionID, resourceID, resource, files, planResult, notes, currentAttempts)

	return &CommitResult{
		Committed:   true,
		Validations: validationResults,
	}, nil
}

func (s *Spec) writeChangedFiles(files []CommitFile) error {
	for _, f := range files {
		if f.Path == "" || f.Content == "" {
			continue
		}
		dir := filepath.Dir(f.Path)
		if err := s.fs.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}

		contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(f.Content)))

		existing, readErr := s.fs.ReadFile(f.Path)
		if readErr == nil {
			existingHash := fmt.Sprintf("%x", sha256.Sum256(existing))
			if existingHash == contentHash {
				continue
			}
		}

		if err := s.fs.WriteFile(f.Path, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	}
	return nil
}

func (s *Spec) runCommitValidations(
	ctx context.Context,
	sess *store.Session,
	sessionID, resourceID string,
	resource cuepkg.Resource,
	files []CommitFile,
	planResult *PlanResult,
	attempts int,
) ([]ValidationResult, *CommitResult) {
	var validationResults []ValidationResult

	if len(resource.Validations) > 0 {
		cwd := filepath.Dir(s.cfg.SpecDir)
		results, err := RunValidations(ctx, resource.Validations, cwd)
		if err == nil {
			validationResults = results
			if rejection := s.checkForFailure(validationResults, sess.ApplyID, sessionID, resourceID, "validate", attempts); rejection != nil {
				return nil, rejection
			}
		}
	}

	if s.engine != nil && len(planResult.Registry.Project.Invariants) > 0 {
		invariantResults, err := s.checkInvariants(ctx, files, planResult.Registry.Project.Invariants)
		if err == nil {
			if rejection := s.checkForFailure(invariantResults, sess.ApplyID, sessionID, resourceID, "invariant", attempts); rejection != nil {
				rejection.Validations = append(validationResults, invariantResults...)
				return nil, rejection
			}
			validationResults = append(validationResults, invariantResults...)
		}
	}

	return validationResults, nil
}

func (s *Spec) checkForFailure(results []ValidationResult, applyID, sessionID, resourceID, actionKind string, attempts int) *CommitResult {
	for _, v := range results {
		if !v.Passed {
			actionID := uuid.NewString()
			s.store.CreateApplyAction(actionID, applyID, resourceID, actionKind)
			s.store.UpdateApplyAction(actionID, "failed", v.Message)
			s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateRejected), v.Message, "", attempts, "")

			return &CommitResult{
				Committed:   false,
				Validations: results,
			}
		}
	}
	return nil
}

func (s *Spec) persistCommittedResource(
	sess *store.Session,
	sessionID, resourceID string,
	resource cuepkg.Resource,
	files []CommitFile,
	planResult *PlanResult,
	notes string,
	attempts int,
) {
	var hashes map[string]string
	json.Unmarshal([]byte(sess.HashesJSON), &hashes)

	declData, _ := json.Marshal(resource.Declaration)
	declHash := fmt.Sprintf("%x", sha256.Sum256(declData))

	s.store.SetResource(store.Resource{
		ID:              resourceID,
		Kind:            resource.Kind,
		ContextName:     resource.ContextName,
		DeclarationHash: declHash,
		EffectiveHash:   hashes[resourceID],
		Model:           s.cfg.GenerateModel,
		SettledAt:       time.Now().UTC(),
	})

	s.store.DeleteGeneratedFiles(resourceID)
	for _, f := range files {
		if f.Path == "" {
			continue
		}
		contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(f.Content)))
		s.store.SetGeneratedFile(store.GeneratedFile{
			Path:        f.Path,
			ResourceID:  resourceID,
			ContentHash: contentHash,
			Model:       s.cfg.GenerateModel,
			CreatedAt:   time.Now().UTC(),
		})
	}

	s.store.DeleteDependencies(resourceID)
	for _, dep := range resource.Dependencies {
		s.store.SetDependency(resourceID, dep.TargetID, dep.Kind)
	}

	if notes != "" {
		s.store.SetNote(resourceID, sess.ApplyID, notes)
	}

	actionID := uuid.NewString()
	s.store.CreateApplyAction(actionID, sess.ApplyID, resourceID, "commit")
	s.store.UpdateApplyAction(actionID, "success", "")

	s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateCommitted), "", "", attempts, "")
}

func (s *Spec) Finish(ctx context.Context, sessionID string, force bool) (*FinishResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	// Count from session_resources — the authoritative state store
	allResources, _ := s.store.ListSessionResources(sessionID)

	committed := 0
	skipped := 0
	errored := 0
	for _, r := range allResources {
		switch ResourceState(r.State) {
		case StateCommitted:
			committed++
		case StateSkipped:
			skipped++
		case StateErrored, StateRejected, StateTimedOut, StateBlocked:
			errored++
		}
	}

	status := "completed"
	if errored > 0 {
		status = "failed"
	}

	s.store.UpdateSession(sessionID, status, sess.CurrentWave)
	s.store.CompleteApply(sess.ApplyID)
	s.store.ReleaseLock()

	return &FinishResult{
		Committed: committed,
		Skipped:   skipped,
		Errored:   errored,
	}, nil
}

type WaveVerifyResult struct {
	WaveIndex     int
	Passed        bool
	Errors        []WaveError
	RetryCount    int
	MaxRetries    int
}

type WaveError struct {
	ResourceID string
	Kind       string
	Message    string
	Files      []string
}

func (s *Spec) AdvanceWave(ctx context.Context, sessionID string) error {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	verification := s.VerifyWave(ctx, sessionID, sess.CurrentWave)
	if !verification.Passed {
		for _, we := range verification.Errors {
			sr, _ := s.store.GetSessionResource(sessionID, we.ResourceID)
			if sr != nil {
				s.store.UpdateSessionResourceState(
					sessionID, we.ResourceID, string(StateErrored),
					we.Message, sr.LastOutput, sr.Attempts, sr.JobID,
				)
			}
		}
	}

	return s.store.UpdateSession(sessionID, sess.Status, sess.CurrentWave+1)
}

func (s *Spec) VerifyWave(ctx context.Context, sessionID string, waveIndex int) *WaveVerifyResult {
	result := &WaveVerifyResult{
		WaveIndex:  waveIndex,
		MaxRetries: s.cfg.WaveMaxRetries,
		Passed:     true,
	}

	resources, err := s.store.ListSessionResourcesByWave(sessionID, waveIndex)
	if err != nil || len(resources) == 0 {
		return result
	}

	var errors []WaveError
	for _, r := range resources {
		state := ResourceState(r.State)
		if state == StateErrored || state == StateRejected {
			var files []string
			genFiles, _ := s.store.GetGeneratedFiles(r.ResourceID)
			for _, f := range genFiles {
				files = append(files, f.Path)
			}
			errors = append(errors, WaveError{
				ResourceID: r.ResourceID,
				Kind:       r.State,
				Message:    r.LastError,
				Files:      files,
			})
		}
	}

	if len(errors) > 0 {
		result.Passed = false
		result.Errors = errors
	}

	s.runVerificationCommand(ctx, s.cfg.TypeCheckCommand, "type_check", resources, result)
	s.runVerificationCommand(ctx, s.cfg.TestCommand, "test", resources, result)

	return result
}

func (s *Spec) runVerificationCommand(ctx context.Context, command, kind string, resources []store.SessionResource, result *WaveVerifyResult) {
	if command == "" {
		return
	}
	cwd := filepath.Dir(s.cfg.SpecDir)
	_, stderr, exitCode, err := RunCommand(ctx, []string{"sh", "-c", command}, cwd)
	if err != nil || exitCode == 0 {
		return
	}
	result.Passed = false
	result.Errors = append(result.Errors, WaveError{
		ResourceID: s.attributeErrorToResource(stderr, resources),
		Kind:       kind,
		Message:    fmt.Sprintf("%s failed (exit %d): %s", kind, exitCode, stderr),
	})
}

func (s *Spec) attributeErrorToResource(errorOutput string, resources []store.SessionResource) string {
	filePaths := parseErrorFilePaths(errorOutput)

	for _, r := range resources {
		genFiles, _ := s.store.GetGeneratedFiles(r.ResourceID)
		for _, f := range genFiles {
			for _, errPath := range filePaths {
				if errPath == f.Path || strings.HasSuffix(errPath, "/"+f.Path) || strings.HasSuffix(f.Path, "/"+errPath) {
					return r.ResourceID
				}
			}
			if strings.Contains(errorOutput, f.Path) {
				return r.ResourceID
			}
		}
	}
	return ""
}

func (s *Spec) checkInvariants(ctx context.Context, files []CommitFile, invariants []cuepkg.Invariant) ([]ValidationResult, error) {
	if len(files) == 0 || len(invariants) == 0 {
		return nil, nil
	}

	var codeBuilder string
	for _, f := range files {
		codeBuilder += fmt.Sprintf("// path: %s\n%s\n\n", f.Path, f.Content)
	}

	var results []ValidationResult
	for _, inv := range invariants {
		reviewPrompt := fmt.Sprintf(
			"Check if this code violates the following invariant:\n\nINVARIANT: %s\n",
			inv.Text,
		)
		if inv.Meta.Rationale != "" {
			reviewPrompt += fmt.Sprintf("RATIONALE: %s\n", inv.Meta.Rationale)
		}
		reviewPrompt += fmt.Sprintf("\nCODE:\n%s\n\nRespond with PASS if the code respects the invariant, or FAIL with explanation if it violates it.", codeBuilder)

		res, err := s.engine.Review(ctx, engine.ReviewOpts{
			Code:         codeBuilder,
			Requirements: reviewPrompt,
		})
		if err != nil {
			continue
		}

		passed := !strings.Contains(strings.ToUpper(res.Output), "FAIL")
		result := ValidationResult{
			Passed: passed,
			Kind:   "invariant",
		}
		if !passed {
			result.Message = fmt.Sprintf("Invariant violated: %s\n%s", inv.Text, res.Output)
		}
		results = append(results, result)
	}

	return results, nil
}

// forceTargetIntoActions ensures the target resource appears in the action
// list. If it is already present, the list is returned unchanged. Otherwise a
// new ActionModify with reason "forced regeneration" is appended.
func forceTargetIntoActions(actions []planpkg.PlannedAction, target string, reg *cuepkg.Registry) []planpkg.PlannedAction {
	if _, exists := reg.Resources[target]; !exists {
		return actions
	}
	for _, a := range actions {
		if a.ResourceID == target {
			return actions
		}
	}
	return append(actions, planpkg.PlannedAction{
		ResourceID: target,
		Kind:       planpkg.ActionModify,
		Reason:     "forced regeneration",
	})
}

// filterForTarget narrows actions and waves to only the target resource and
// its transitive dependencies (ancestors in the dependency graph). Empty
// waves are dropped from the result.
func filterForTarget(actions []planpkg.PlannedAction, waves [][]string, target string, g *graphpkg.Graph) ([]planpkg.PlannedAction, [][]string) {
	keep := make(map[string]bool)
	keep[target] = true
	for _, id := range g.Ancestors(target) {
		keep[id] = true
	}

	var filtered []planpkg.PlannedAction
	for _, a := range actions {
		if keep[a.ResourceID] {
			filtered = append(filtered, a)
		}
	}

	var filteredWaves [][]string
	for _, wave := range waves {
		var kept []string
		for _, id := range wave {
			if keep[id] {
				kept = append(kept, id)
			}
		}
		if len(kept) > 0 {
			filteredWaves = append(filteredWaves, kept)
		}
	}

	return filtered, filteredWaves
}

func orchestratorInstructions() string {
	return `========================================================================
  ORCHESTRATOR RULES — YOU ARE A DISPATCHER, NOT A CODE GENERATOR
========================================================================

You are the orchestrator. Your job is to coordinate sub-agents and make
decisions. You MUST NOT write implementation code yourself.

## Recommended flow (fewest tool calls):

  1. spec/plan          → see what needs generating
  2. spec/begin         → start session (returns session_id + plan + waves)
  3. spec/run_wave      → dispatch entire wave in parallel, returns summary
     - committed: resources that succeeded
     - rejected: resources that failed validation (with error context)
     - errored: resources that failed generation
  4. Handle destroys (if PendingDestroys is non-empty):
     - Review the list of resources pending deletion
     - spec/confirm_destroys → confirm which resources to delete
  5. Handle failures:
     - spec/resolve     → provide guidance, re-dispatch
     - spec/amend       → fix CUE spec, re-dispatch
     - spec/skip        → skip and move on
  6. Repeat step 3 until done=true
  7. spec/finish        → finalize session

  Use model_overrides in spec/run_wave to assign cheap models (haiku)
  to simple resources and capable models (sonnet/opus) to complex ones.

## Single-resource dispatch:

  spec/dispatch (session_id, resource_id, model) → atomic generate-and-commit.
  Useful for re-dispatching individual failed resources after providing guidance.

## Manual pipeline (full control):

  spec/context → run_prompt → poll_result → parse code blocks → spec/commit
  Use this when you need to inspect or modify sub-agent output before committing.

DO NOT:
  - Write implementation code directly — every file must come from a sub-agent
  - Skip the sub-agent step for any resource, even simple ones
========================================================================`
}

func dispatchInstructions(resourceID string) string {
	return fmt.Sprintf(`Dispatch a sub-agent to generate code for %s.
1. Call run_prompt with prompt, system_prompt, session_id, and resource_id
   (session_id and resource_id enable generation tracking in SQLite)
2. Call poll_result to collect the generated code
3. Parse code blocks with // path: annotations
4. Call spec/commit with the parsed files
5. If validation fails, re-dispatch with the error details`, resourceID)
}
