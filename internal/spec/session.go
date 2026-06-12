package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
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
	Invariants   []InvariantInfo
}

// InvariantInfo is a project invariant surfaced to the orchestrator so it can
// judge the generated files against it and supply a verdict at commit.
type InvariantInfo struct {
	Text      string `json:"text"`
	Rationale string `json:"rationale,omitempty"`
}

// InvariantCheckInput is an orchestrator-supplied verdict for one project
// invariant, judged against the files being committed. The server records and
// enforces it; the LLM judgment happens outside the server.
type InvariantCheckInput struct {
	Invariant string `json:"invariant"`
	Passed    bool   `json:"passed"`
	Summary   string `json:"summary"`
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
	Committed        int
	Skipped          int
	Errored          int
	ReflectionPrompt string `json:"reflection_prompt,omitempty"`
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

	destroyActions, otherActions := partitionActions(actions)

	s.seedSessionResources(sessionID, waves, otherActions)

	// Materialize amendment lifecycle state from the spec (best-effort: a
	// reconcile failure must not block a generation session).
	if err := s.ReconcileAmendments(ctx); err != nil {
		log.Printf("amendments: reconcile during begin failed (swallowed): %v", err)
	}

	return &BeginResult{
		SessionID:       sessionID,
		ApplyID:         applyID,
		Plan:            otherActions,
		Waves:           waves,
		Instructions:    orchestratorInstructions(),
		PendingDestroys: destroyActions,
	}, nil
}

func partitionActions(actions []planpkg.PlannedAction) (destroy, other []planpkg.PlannedAction) {
	for _, a := range actions {
		switch a.Kind {
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

	var invariants []InvariantInfo
	for _, inv := range planResult.Registry.Project.Invariants {
		invariants = append(invariants, InvariantInfo{Text: inv.Text, Rationale: inv.Meta.Rationale})
	}

	return &ContextResult{
		SystemPrompt: systemPrompt,
		Prompt:       fullPrompt,
		Instructions: dispatchInstructions(resourceID),
		Invariants:   invariants,
	}, nil
}

func (s *Spec) Commit(ctx context.Context, sessionID, resourceID string, files []CommitFile, notes string, invariantChecks []InvariantCheckInput, model string) (*CommitResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	if model == "" {
		model = s.cfg.GenerateModel
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

	// Create the generation row only once all early-return paths have passed,
	// so every created row is guaranteed to receive an outcome update on both
	// remaining paths (rejected/success). Creating it earlier left dangling
	// rows that rendered as eternal "pending" in the dashboard.
	genID := uuid.NewString()
	s.store.CreateGeneration(store.Generation{
		ID: genID, ApplyID: sess.ApplyID, ResourceID: resourceID, Model: model,
	})

	validationResults, rejected := s.runCommitValidations(ctx, sess, sessionID, resourceID, resource, planResult, invariantChecks, currentAttempts)
	if rejected != nil {
		// Validation gate failed: amendments declaring a validation are FAILED.
		s.store.UpdateGeneration(genID, "", "rejected", firstFailureMessage(rejected.Validations), 0, 0, 0, 0)
		s.markAmendmentVerification(resourceID, resource, false)
		return rejected, nil
	}

	s.persistCommittedResource(sess, sessionID, resourceID, resource, files, planResult, notes, currentAttempts)

	// Validation gate passed: amendments declaring a validation are VERIFIED.
	s.markAmendmentVerification(resourceID, resource, true)

	s.store.UpdateGeneration(genID, "", "success", "", 0, 0, 0, 0)

	return &CommitResult{
		Committed:   true,
		Validations: validationResults,
	}, nil
}

// firstFailureMessage returns the message of the first failed validation
// result, or "" when all passed.
func firstFailureMessage(results []ValidationResult) string {
	for _, v := range results {
		if !v.Passed {
			return v.Message
		}
	}
	return ""
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

// resourceValidations returns the full set of validations to run when a
// resource is committed: the resource's own declared validations PLUS any
// validation declared on its amendments. Amendment-declared validations prove
// the amendment's intent was actually satisfied (applied != fixed).
func resourceValidations(resource cuepkg.Resource) []cuepkg.Validation {
	validations := append([]cuepkg.Validation(nil), resource.Validations...)
	for _, a := range cuepkg.ResourceAmendments(resource) {
		if a.Validation != nil {
			validations = append(validations, *a.Validation)
		}
	}
	return validations
}

func (s *Spec) runCommitValidations(
	ctx context.Context,
	sess *store.Session,
	sessionID, resourceID string,
	resource cuepkg.Resource,
	planResult *PlanResult,
	invariantChecks []InvariantCheckInput,
	attempts int,
) ([]ValidationResult, *CommitResult) {
	var validationResults []ValidationResult

	validations := resourceValidations(resource)
	if len(validations) > 0 {
		cwd := filepath.Dir(s.cfg.SpecDir)
		results, err := RunValidations(ctx, validations, cwd)
		if err == nil {
			validationResults = results
			if rejection := s.checkForFailure(validationResults, sess.ApplyID, sessionID, resourceID, "validate", attempts); rejection != nil {
				return nil, rejection
			}
		}
	}

	if len(invariantChecks) > 0 {
		invariantResults := s.ingestInvariantChecks(sess.ApplyID, resourceID, invariantChecks)
		if rejection := s.checkForFailure(invariantResults, sess.ApplyID, sessionID, resourceID, "invariant", attempts); rejection != nil {
			rejection.Validations = append(validationResults, invariantResults...)
			return nil, rejection
		}
		validationResults = append(validationResults, invariantResults...)
	}

	return validationResults, nil
}

// ingestInvariantChecks converts orchestrator-supplied verdicts into
// ValidationResults and persists them for the audit trail / reflection.
func (s *Spec) ingestInvariantChecks(applyID, resourceID string, checks []InvariantCheckInput) []ValidationResult {
	results := make([]ValidationResult, 0, len(checks))
	for _, c := range checks {
		s.store.RecordInvariantCheck(store.InvariantCheck{
			ID:         uuid.NewString(),
			ApplyID:    applyID,
			ResourceID: resourceID,
			CheckType:  c.Invariant,
			Passed:     c.Passed,
			Output:     c.Summary,
			CreatedAt:  time.Now().UTC(),
		})
		r := ValidationResult{Kind: "invariant", Passed: c.Passed}
		if !c.Passed {
			r.Message = fmt.Sprintf("Invariant violated: %s\n%s", c.Invariant, c.Summary)
		}
		results = append(results, r)
	}
	return results
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

	// Session-scoped reflection at finish (Component 6, trigger 3). The server
	// only builds the prompt; the orchestrator runs it and submits the output
	// via RecordLearnings. Errors are swallowed — reflection must never fail a
	// session.
	reflectionPrompt := ""
	if s.reflector != nil && (s.cfg.Evolve == "finish" || s.cfg.Evolve == "all") {
		reflectionPrompt, _ = s.reflector.BuildSessionPrompt(sessionID, sess.ApplyID)
	}

	s.store.UpdateSession(sessionID, status, sess.CurrentWave)
	s.store.CompleteApply(sess.ApplyID)
	s.store.ReleaseLock()

	return &FinishResult{
		Committed:        committed,
		Skipped:          skipped,
		Errored:          errored,
		ReflectionPrompt: reflectionPrompt,
	}, nil
}

type WaveVerifyResult struct {
	WaveIndex  int
	Passed     bool
	Errors     []WaveError
	RetryCount int
	MaxRetries int
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

	if plan, err := s.Plan(ctx); err == nil && plan != nil {
		s.runProjectValidations(ctx, plan.Registry.Project.Validations, resources, result)
	}

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

// runProjectValidations runs whole-crate validations declared at project level
// (e.g. clippy/fmt/build/test) in the project root and records any failure as a
// WaveError. Command output is already truncated by RunValidations.
func (s *Spec) runProjectValidations(ctx context.Context, validations []cuepkg.Validation, resources []store.SessionResource, result *WaveVerifyResult) {
	if len(validations) == 0 {
		return
	}
	cwd := filepath.Dir(s.cfg.SpecDir)
	results, err := RunValidations(ctx, validations, cwd)
	if err != nil {
		result.Passed = false
		result.Errors = append(result.Errors, WaveError{
			Kind:    "project_validation",
			Message: fmt.Sprintf("project validation error: %v", err),
		})
		return
	}
	for _, r := range results {
		if r.Passed {
			continue
		}
		result.Passed = false
		result.Errors = append(result.Errors, WaveError{
			ResourceID: s.attributeErrorToResource(r.Message, resources),
			Kind:       "project_validation",
			Message:    fmt.Sprintf("%s: %s", r.Kind, r.Message),
		})
	}
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

You are the orchestrator. The crest-spec server is a pure state engine — it
never calls an LLM. You run all generation with your own sub-agents/workflows.
Default generation model: sonnet.

## Pipeline

1. spec/plan            → see what needs generating
2. spec/begin           → start a session (session_id, plan, waves, pending destroys)
3. spec/confirm_destroys → if PendingDestroys is non-empty, confirm deletions
4. spec/next            → next wave of resources (dependency order)
5. For each resource in the wave (parallelize across the wave):
   a. spec/context      → scoped prompt + system_prompt + project invariants
   b. Generate with a sub-agent (sonnet) using that prompt
   c. Judge each returned invariant against the generated files (pass/fail + summary)
   d. spec/commit       → files + notes + model + invariant_checks
                          The server writes files and runs the resource's
                          mechanical validations (compile/test/custom).
                          A failed validation or a failed invariant verdict
                          rejects the commit.
   e. If Committed=false: call spec/context again (it now includes the
      failure), regenerate, re-commit — up to max_retries. Then
      spec/resolve (guidance) or spec/skip.
6. Repeat from step 4 until spec/next returns done=true
7. spec/finish          → finalize; if reflection_prompt is non-empty, run it
                          with a sub-agent and submit via spec/record_learnings

The core loop is generate → commit → validate → retry-with-feedback.

## Observability

  spec/status (session_id) → session overview; spec/wave_status → per-resource view.

DO NOT:
  - Write implementation code directly — every file must come from a sub-agent
  - Skip the sub-agent step for any resource, even simple ones
========================================================================`
}

func dispatchInstructions(resourceID string) string {
	return fmt.Sprintf(`Generate code for %s with a sub-agent (sonnet by default).

1. Give the sub-agent the system_prompt and prompt from this result.
2. The sub-agent produces the files (path + full content).
3. Judge each invariant in this result against the files (passed + summary).
4. Call spec/commit with session_id, resource_id, files, model, and
   invariant_checks. The server runs mechanical validations and rejects on
   any failure.
5. If Committed=false, call spec/context again — the failure context is
   injected into the new prompt — and retry (respect max_retries).`, resourceID)
}
