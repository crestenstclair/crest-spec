package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"strings"

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
	SessionID          string
	ApplyID            string
	Plan               []planpkg.PlannedAction
	Waves              [][]string
	Instructions       string
	DriftActions       []planpkg.PlannedAction
	DestroyedResources []DestroyedResource
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

	var driftActions []planpkg.PlannedAction
	var destroyActions []planpkg.PlannedAction
	var otherActions []planpkg.PlannedAction
	for _, a := range actions {
		switch a.Kind {
		case planpkg.ActionDrift:
			driftActions = append(driftActions, a)
		case planpkg.ActionDestroy:
			destroyActions = append(destroyActions, a)
		default:
			otherActions = append(otherActions, a)
		}
	}

	// Execute destroys immediately — no LLM dispatch needed.
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

	// Seed session_resources for non-destroy actions as "pending"
	waveMap := make(map[string]int)
	for i, wave := range waves {
		for _, id := range wave {
			waveMap[id] = i
		}
	}
	for _, a := range otherActions {
		s.store.UpsertSessionResource(store.SessionResource{
			SessionID:  sessionID,
			ResourceID: a.ResourceID,
			State:      string(StatePending),
			WaveIndex:  waveMap[a.ResourceID],
			MaxRetries: s.cfg.MaxRetries,
		})
	}
	for _, a := range driftActions {
		s.store.UpsertSessionResource(store.SessionResource{
			SessionID:  sessionID,
			ResourceID: a.ResourceID,
			State:      string(StatePending),
			WaveIndex:  waveMap[a.ResourceID],
			MaxRetries: s.cfg.MaxRetries,
		})
	}

	return &BeginResult{
		SessionID:          sessionID,
		ApplyID:            applyID,
		Plan:               otherActions,
		Waves:              waves,
		Instructions:       orchestratorInstructions(),
		DriftActions:       driftActions,
		DestroyedResources: destroyed,
	}, nil
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
		var resources []ResourceStatus
		for _, id := range waves[w] {
			action, inPlan := planSet[id]
			if !inPlan {
				continue
			}

			sr, tracked := stateMap[id]
			if tracked {
				state := ResourceState(sr.State)
				if state.IsTerminal() {
					continue
				}
			}

			state := StatePending
			if tracked {
				state = ResourceState(sr.State)
			}

			rs := ResourceStatus{
				ResourceID: id,
				State:      state,
				WaveIndex:  w,
				MaxRetries: s.cfg.MaxRetries,
				Notes:      action.Reason,
			}
			if tracked {
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

	for _, f := range files {
		if f.Path == "" || f.Content == "" {
			continue
		}
		dir := filepath.Dir(f.Path)
		if err := s.fs.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
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
			return nil, fmt.Errorf("write %s: %w", f.Path, err)
		}
	}

	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan for commit: %w", err)
	}

	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}

	// Get current resource state from SQLite
	sr, _ := s.store.GetSessionResource(sessionID, resourceID)
	currentAttempts := 0
	if sr != nil {
		currentAttempts = sr.Attempts
	}
	currentAttempts++

	// Run resource-level validations if declared
	var validationResults []ValidationResult
	if len(resource.Validations) > 0 {
		cwd := filepath.Dir(s.cfg.SpecDir)
		validationResults, err = RunValidations(ctx, resource.Validations, cwd)
		if err != nil {
			return nil, fmt.Errorf("run validations: %w", err)
		}

		for _, v := range validationResults {
			if !v.Passed {
				actionID := uuid.NewString()
				s.store.CreateApplyAction(actionID, sess.ApplyID, resourceID, "validate")
				s.store.UpdateApplyAction(actionID, "failed", v.Message)

				// Update session_resources: rejected with validation error
				s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateRejected), v.Message, "", currentAttempts, "")

				return &CommitResult{
					Committed:   false,
					Validations: validationResults,
				}, nil
			}
		}
	}

	// Run invariant checks if the engine is available
	if s.engine != nil && len(planResult.Registry.Project.Invariants) > 0 {
		invariantResults, err := s.checkInvariants(ctx, files, planResult.Registry.Project.Invariants)
		if err == nil {
			for _, v := range invariantResults {
				if !v.Passed {
					actionID := uuid.NewString()
					s.store.CreateApplyAction(actionID, sess.ApplyID, resourceID, "invariant")
					s.store.UpdateApplyAction(actionID, "failed", v.Message)

					// Update session_resources: rejected with invariant error
					s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateRejected), v.Message, "", currentAttempts, "")

					return &CommitResult{
						Committed:   false,
						Validations: append(validationResults, invariantResults...),
					}, nil
				}
			}
			validationResults = append(validationResults, invariantResults...)
		}
	}

	// All validations passed — commit the resource
	var hashes map[string]string
	json.Unmarshal([]byte(sess.HashesJSON), &hashes)

	declHash := fmt.Sprintf("%x", sha256.Sum256(func() []byte { b, _ := json.Marshal(resource.Declaration); return b }()))

	if err := s.store.SetResource(store.Resource{
		ID:              resourceID,
		Kind:            resource.Kind,
		ContextName:     resource.ContextName,
		DeclarationHash: declHash,
		EffectiveHash:   hashes[resourceID],
		Model:           s.cfg.GenerateModel,
		SettledAt:       time.Now().UTC(),
	}); err != nil {
		return nil, fmt.Errorf("set resource: %w", err)
	}

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

	// Record successful commit in apply_actions
	actionID := uuid.NewString()
	s.store.CreateApplyAction(actionID, sess.ApplyID, resourceID, "commit")
	s.store.UpdateApplyAction(actionID, "success", "")

	// Update session_resources: committed
	s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateCommitted), "", "", currentAttempts, "")

	return &CommitResult{
		Committed:   true,
		Validations: validationResults,
	}, nil
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

	// Run type check command if configured
	if s.cfg.TypeCheckCommand != "" {
		cwd := filepath.Dir(s.cfg.SpecDir)
		_, stderr, exitCode, err := RunCommand(ctx, []string{"sh", "-c", s.cfg.TypeCheckCommand}, cwd)
		if err == nil && exitCode != 0 {
			result.Passed = false
			we := WaveError{
				Kind:    "type_check",
				Message: fmt.Sprintf("type check failed (exit %d): %s", exitCode, stderr),
			}
			we.ResourceID = s.attributeErrorToResource(stderr, resources)
			result.Errors = append(result.Errors, we)
		}
	}

	// Run test command if configured
	if s.cfg.TestCommand != "" {
		cwd := filepath.Dir(s.cfg.SpecDir)
		_, stderr, exitCode, err := RunCommand(ctx, []string{"sh", "-c", s.cfg.TestCommand}, cwd)
		if err == nil && exitCode != 0 {
			result.Passed = false
			we := WaveError{
				Kind:    "test",
				Message: fmt.Sprintf("test failed (exit %d): %s", exitCode, stderr),
			}
			we.ResourceID = s.attributeErrorToResource(stderr, resources)
			result.Errors = append(result.Errors, we)
		}
	}

	return result
}

func (s *Spec) attributeErrorToResource(errorOutput string, resources []store.SessionResource) string {
	for _, r := range resources {
		genFiles, _ := s.store.GetGeneratedFiles(r.ResourceID)
		for _, f := range genFiles {
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
  CRITICAL: ORCHESTRATOR RULES — YOU ARE A DISPATCHER, NOT A CODE GENERATOR
========================================================================

You are an orchestration agent. Your job is to drive the MCP tools and
dispatch sub-agents. You MUST NOT write implementation code yourself.

DO:
  - Call spec/plan to see what needs generating
  - Call spec/begin to start a session (returns session_id + plan + waves)
  - Call spec/next (session_id) to get the current wave of resources
  - For each resource in the wave:
    a. Call spec/context (session_id, resource_id) to get system_prompt + prompt
    b. Call run_prompt (prompt, system_prompt, session_id, resource_id) to dispatch
    c. Call poll_result (job_id) to wait for output
    d. Parse the output: extract fenced code blocks with // path: annotations
    e. Call spec/commit (session_id, resource_id, files) to commit the parsed files
       - If commit returns committed=false, the validation failed
       - Read the validation errors and either fix the code or call spec/skip
    f. Call spec/note (resource_id, content) with design decisions
  - Call spec/next again; repeat until done=true
  - Call spec/finish (session_id) to finalize

  If a resource fails validation on commit:
    - Re-dispatch with the error in the prompt (build a fix prompt)
    - Or call spec/resolve with user guidance
    - Or call spec/skip to move on

DO NOT:
  - Write implementation code directly — every file must come from a sub-agent
  - Skip the sub-agent step for any resource, even simple ones
  - Modify the sub-agent's output unless it fails validation

Resources within the same wave are independent — dispatch them in parallel.
Waves must be processed sequentially.
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
