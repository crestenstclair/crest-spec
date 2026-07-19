package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	graphpkg "github.com/crestenstclair/crest-spec/internal/graph"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
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
	Done                bool
	WaveIndex           int
	Resources           []ResourceStatus
	ValidationReady     bool
	ValidationWaveIndex int
}

type ContextResult struct {
	DispatchID            string
	AttemptID             string
	ContextManifestID     string
	ContextHash           string
	Role                  string
	RecommendedRole       string
	RolePolicyVersion     string
	ContextPolicy         string
	HandoffID             string
	SelectorVersion       string
	EstimatorVersion      string
	TemplateHashes        map[string]string
	BudgetTokens          int
	EstimatedTokens       int
	Blocked               bool
	BlockedReason         string
	NoDispatch            bool
	ReinstatedCandidateID string
	Sections              []contextmanifest.Section
	SystemPrompt          string
	Prompt                string
	Instructions          string
	Invariants            []InvariantInfo
}

type ContextOptions struct {
	SessionID       string
	ResourceID      string
	DispatchID      string
	Role            string
	BudgetTokens    int
	ParentAttemptID string
}

// WaveContextOptions prepares a capacity-bounded set of unclaimed contexts in
// one dependency wave. Role and budget apply uniformly; callers that need
// per-resource overrides can continue to use PrepareContext.
type WaveContextOptions struct {
	SessionID    string
	Role         string
	BudgetTokens int
	// Limit is the number of currently idle worker slots the host can assign
	// immediately. Contexts are immutable source snapshots, so the engine must
	// not prepare a whole wave and leave excess work queued behind a smaller
	// worker pool. A zero value safely defaults to one.
	Limit int
}

// WaveContextItem reports the independently persisted outcome for one wave
// resource. Context is present when an immutable attempt was created. Error is
// scoped to this resource and does not erase other prepared contexts.
type WaveContextItem struct {
	ResourceID string         `json:"resource_id"`
	Context    *ContextResult `json:"context,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// WaveContextResult is the one-round-trip preparation envelope returned to a
// host worker pool. Each context/attempt remains unclaimed until the host
// records dispatch_requested for an actual idle worker.
type WaveContextResult struct {
	Done                bool              `json:"done"`
	WaveIndex           int               `json:"wave_index"`
	Contexts            []WaveContextItem `json:"contexts"`
	ValidationReady     bool              `json:"validation_ready"`
	ValidationWaveIndex int               `json:"validation_wave_index"`
}

// InvariantInfo is a project invariant surfaced to the generator as a
// constraint to honor. Enforcement is NOT self-judged: mechanical validations
// gate the commit, and behavioral checks (spec/verify) gate the wave/finish.
type InvariantInfo struct {
	Text      string `json:"text"`
	Rationale string `json:"rationale,omitempty"`
}

type CommitFile struct {
	Path    string
	Content string
}

type CommitResult struct {
	Committed          bool
	Validations        []ValidationResult
	ValidationDeferred bool
	AttemptID          string
	ExecutionID        string
	CandidateID        string
	FailureCategory    string
}

type FinishResult struct {
	Committed            int      `json:"committed"`
	Skipped              int      `json:"skipped"`
	Errored              int      `json:"errored"`
	ProjectCompletion    string   `json:"project_completion,omitempty"`
	CompletionReason     string   `json:"completion_reason,omitempty"`
	IncompleteGoals      []string `json:"incomplete_goals,omitempty"`
	ValidationPending    []string `json:"validation_pending,omitempty"`
	ValidationUnverified []string `json:"validation_unverified,omitempty"`
	// Blocked is set when the session cannot finalize because behavioral
	// checks are unresolved. The session stays open; BlockingChecks lists
	// what must be resolved. force=true is the only override, and it is an
	// explicit, human-visible decision — there is no quiet path past the gate.
	Blocked          bool            `json:"blocked,omitempty"`
	BlockingChecks   []BlockingCheck `json:"blocking_checks,omitempty"`
	ReflectionPrompt string          `json:"reflection_prompt,omitempty"`
}

// BlockingCheck is a behavioral check that prevents spec/finish from
// finalizing. State semantics: pending = never verified, failed/theater =
// verified and rejected (regenerate the resource), malformed = the check
// itself is structurally broken (re-run the tasks stage to re-author it).
type BlockingCheck struct {
	CheckID    string `json:"check_id"`
	ResourceID string `json:"resource_id"`
	Behavior   string `json:"behavior"`
	State      string `json:"state"`
}

func (s *Spec) Begin(ctx context.Context, opts BeginOpts) (*BeginResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	intent, err := projectIntentSnapshot(planResult.Registry.Project)
	if err != nil {
		return nil, fmt.Errorf("project intent: %w", err)
	}
	intent.ResourceTrace = resourceTraceSnapshot(planResult.Registry)
	intent.Verification, err = verificationDefinitionSnapshot(planResult.Registry)
	if err != nil {
		return nil, fmt.Errorf("verification definitions: %w", err)
	}
	if err := s.store.ReconcileProjectIntent(ctx, intent); err != nil {
		return nil, fmt.Errorf("reconcile project intent: %w", err)
	}
	var reverifyResources []string
	for _, action := range planResult.Actions {
		if action.Kind == planpkg.ActionModify || action.Kind == planpkg.ActionDestroy {
			reverifyResources = append(reverifyResources, action.ResourceID)
		}
	}
	if len(reverifyResources) > 0 {
		if _, err := s.store.InvalidateVerificationEvidence(ctx, intent.ProjectName, reverifyResources,
			"resource change requires verification evidence to be regenerated",
			store.TransitionSource{Type: "plan", ID: intent.SpecHash}); err != nil {
			return nil, fmt.Errorf("invalidate verification evidence: %w", err)
		}
	}
	if err := s.reprojectCompletion(ctx, planResult, store.TransitionSource{Type: "plan", ID: intent.SpecHash}); err != nil {
		return nil, fmt.Errorf("project completion: %w", err)
	}

	actions := planResult.Actions
	waves := planResult.Waves

	// Force: when a target is specified and force is true, ensure the target
	// appears in the plan even if its hashes haven't changed.
	if opts.Target != "" && opts.Force {
		actions = forceTargetIntoActions(
			actions,
			opts.Target,
			planResult.Registry,
			planResult.GenerationInputHashes,
			planResult.DependencyContractHashes,
			planResult.VerificationContractHashes,
		)
	}

	// Target: filter the plan to only include the target resource and its
	// transitive dependencies (ancestors in the dependency graph).
	if opts.Target != "" {
		if !planResult.Graph.Has(opts.Target) {
			return nil, fmt.Errorf("target resource not found: %s", opts.Target)
		}
		actions, waves = filterForTarget(actions, waves, opts.Target, planResult.Graph)
	}

	// Target filtering can remove the only validator that made a fast path
	// safe in the full plan. Re-evaluate the actual session scope before any
	// durable state is created.
	scopedPlan := *planResult
	scopedPlan.Actions = append([]planpkg.PlannedAction(nil), actions...)
	s.requireClosureValidationForFastPaths(&scopedPlan)
	actions = scopedPlan.Actions
	// Persist only the affected action frontier. Registry-only/unchanged nodes
	// do not need empty scheduler waves or status rows.
	waves = filterWavesForActions(waves, actions)

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
		if releaseErr := s.store.ReleaseLock(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release lock after session creation failure: %w", releaseErr))
		}
		return nil, fmt.Errorf("create session: %w", err)
	}

	destroyActions, otherActions := partitionActions(actions)

	if err := s.seedSessionResources(sessionID, waves, otherActions); err != nil {
		return nil, s.failSessionStart(sessionID, fmt.Errorf("seed session resources: %w", err))
	}
	if err := s.applyCandidateReuseFastPaths(ctx, sessionID, otherActions); err != nil {
		return nil, s.failSessionStart(sessionID, fmt.Errorf("apply candidate reuse: %w", err))
	}

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

func (s *Spec) executeDestroys(ctx context.Context, destroyActions []planpkg.PlannedAction, applyID string) ([]DestroyedResource, error) {
	var destroyed []DestroyedResource
	if len(destroyActions) == 0 {
		return destroyed, nil
	}
	releaseProject, err := s.projectWorkspace().AcquireCommit(ctx)
	if err != nil {
		return destroyed, fmt.Errorf("acquire project commit lock for destroy: %w", err)
	}
	defer releaseProject()
	for _, a := range destroyActions {
		actionID := uuid.NewString()
		if err := s.store.CreateApplyAction(actionID, applyID, a.ResourceID, "destroy"); err != nil {
			return destroyed, fmt.Errorf("create destroy action for %s: %w", a.ResourceID, err)
		}
		fail := func(err error) ([]DestroyedResource, error) {
			if updateErr := s.store.UpdateApplyAction(actionID, "failed", err.Error()); updateErr != nil {
				err = errors.Join(err, fmt.Errorf("record destroy failure: %w", updateErr))
			}
			return destroyed, err
		}

		var deletedFiles []string
		files, err := s.store.GetGeneratedFiles(a.ResourceID)
		if err != nil {
			return fail(fmt.Errorf("list generated files for %s: %w", a.ResourceID, err))
		}

		// Resolve every ownership decision and compare every removable file with
		// its accepted content hash before touching the filesystem. A failed
		// lookup is not evidence that a file is sole-owned, and divergent bytes
		// may be user work. Preflighting the complete set prevents a later
		// mismatch from leaving an earlier file deleted.
		type removableFile struct {
			generated store.GeneratedFile
			path      string
		}
		var removable []removableFile
		for _, f := range files {
			owners, err := s.store.CountOtherFileOwners(f.Path, a.ResourceID)
			if err != nil {
				return fail(fmt.Errorf("count other owners for %s: %w", f.Path, err))
			}
			if owners > 0 {
				continue
			}
			path, err := s.projectFilePath(f.Path)
			if err != nil {
				return fail(fmt.Errorf("resolve generated file %s: %w", f.Path, err))
			}
			content, err := s.fs.ReadFile(path)
			if err != nil {
				if !os.IsNotExist(err) {
					return fail(fmt.Errorf("read generated file %s before destroy: %w", f.Path, err))
				}
				removable = append(removable, removableFile{generated: f, path: path})
				continue
			}
			currentHash := contextmanifest.Hash(string(content))
			if f.ContentHash == "" || currentHash != f.ContentHash {
				return fail(fmt.Errorf(
					"generated file %s changed since CrestSpec accepted it (expected hash %s, current hash %s); refusing destroy",
					f.Path, f.ContentHash, currentHash,
				))
			}
			removable = append(removable, removableFile{generated: f, path: path})
		}

		for _, f := range removable {
			if err := s.fs.Remove(f.path); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return fail(fmt.Errorf("remove generated file %s: %w", f.generated.Path, err))
			}
			deletedFiles = append(deletedFiles, f.generated.Path)
		}
		if err := s.store.DeleteGeneratedFiles(a.ResourceID); err != nil {
			return fail(fmt.Errorf("delete generated-file records for %s: %w", a.ResourceID, err))
		}
		if err := s.store.DeleteDependencies(a.ResourceID); err != nil {
			return fail(fmt.Errorf("delete dependencies for %s: %w", a.ResourceID, err))
		}
		// Behavioral state goes with the resource — dangling checks for a
		// destroyed resource would haunt every future reconcile.
		if err := s.store.DeleteChecksByResource(a.ResourceID); err != nil {
			return fail(fmt.Errorf("delete behavioral checks for %s: %w", a.ResourceID, err))
		}
		if err := s.store.DeleteTasksByResource(a.ResourceID); err != nil {
			return fail(fmt.Errorf("delete behavioral tasks for %s: %w", a.ResourceID, err))
		}
		if err := s.store.DeleteResource(a.ResourceID); err != nil {
			return fail(fmt.Errorf("delete resource %s: %w", a.ResourceID, err))
		}
		destroyed = append(destroyed, DestroyedResource{
			ResourceID:   a.ResourceID,
			DeletedFiles: deletedFiles,
		})
		if err := s.store.UpdateApplyAction(actionID, "destroyed", ""); err != nil {
			return destroyed, fmt.Errorf("complete destroy action for %s: %w", a.ResourceID, err)
		}
	}
	return destroyed, nil
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

	return s.executeDestroys(ctx, destroyActions, sess.ApplyID)
}

func (s *Spec) seedSessionResources(sessionID string, waves [][]string, actionSets ...[]planpkg.PlannedAction) error {
	waveMap := make(map[string]int)
	for i, wave := range waves {
		for _, id := range wave {
			waveMap[id] = i
		}
	}
	for _, actions := range actionSets {
		for _, a := range actions {
			state := string(StatePending)
			phase := ""
			if a.ExecutionStrategy == planpkg.ExecutionStrategyValidateExisting {
				state = string(StateCommitted)
				phase = "integration_pending"
			}
			if err := s.store.UpsertSessionResource(store.SessionResource{
				SessionID:         sessionID,
				ResourceID:        a.ResourceID,
				State:             state,
				WaveIndex:         waveMap[a.ResourceID],
				MaxRetries:        s.cfg.MaxRetries,
				Phase:             phase,
				ExecutionStrategy: a.ExecutionStrategy,
				SourceCandidateID: a.SourceCandidateID,
			}); err != nil {
				return fmt.Errorf("upsert %s: %w", a.ResourceID, err)
			}
		}
	}
	return nil
}

// failSessionStart prevents a partially initialized session from remaining
// active or retaining the global reconciliation lock. The original failure is
// always preserved alongside any cleanup failures.
func (s *Spec) failSessionStart(sessionID string, cause error) error {
	if err := s.store.UpdateSession(sessionID, "aborted", 0); err != nil {
		cause = errors.Join(cause, fmt.Errorf("mark session aborted: %w", err))
	}
	if err := s.store.ReleaseLock(); err != nil {
		cause = errors.Join(cause, fmt.Errorf("release lock: %w", err))
	}
	return cause
}

func (s *Spec) Next(ctx context.Context, sessionID string) (*NextResult, error) {
	if _, err := s.RecoverWorkspaceCommits(ctx); err != nil {
		return nil, fmt.Errorf("recover interrupted workspace commits before next wave: %w", err)
	}
	// Scheduling is also a recovery boundary. A long-lived MCP server may
	// outlive a disconnected host, so waiting for process restart alone would
	// leave its pre-execution claims invisible to the next-wave selector.
	dispatchTimeout := config.DefaultDispatchTimeout
	var executionTimeout time.Duration
	if s.cfg != nil {
		dispatchTimeout = s.cfg.EffectiveDispatchTimeout()
		executionTimeout = s.cfg.ExecutionTimeout
	}
	if dispatchTimeout > 0 || executionTimeout > 0 {
		now := time.Now().UTC()
		var dispatchBefore, executionBefore time.Time
		if dispatchTimeout > 0 {
			dispatchBefore = now.Add(-dispatchTimeout)
		}
		if executionTimeout > 0 {
			executionBefore = now.Add(-executionTimeout)
		}
		if _, err := s.RecoverExecutions(ctx, dispatchBefore, executionBefore); err != nil {
			return nil, fmt.Errorf("recover stale lifecycle before next wave: %w", err)
		}
	}
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var waves [][]string
	if err := json.Unmarshal([]byte(sess.WavesJSON), &waves); err != nil {
		return nil, fmt.Errorf("unmarshal waves: %w", err)
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
	allResources, err := s.store.ListSessionResources(sessionID)
	if err != nil {
		return nil, fmt.Errorf("list session resources: %w", err)
	}
	reinstated, err := s.reconcilePoisonedValidationResources(ctx, allResources, plan)
	if err != nil {
		return nil, err
	}
	if reinstated {
		allResources, err = s.store.ListSessionResources(sessionID)
		if err != nil {
			return nil, fmt.Errorf("reload session resources after candidate reinstatement: %w", err)
		}
	}
	stateMap := make(map[string]store.SessionResource)
	for _, r := range allResources {
		stateMap[r.ResourceID] = r
	}

	// Scan from wave 0, not sess.CurrentWave: a resource in an already-passed
	// wave can be reset to pending (e.g. by wave verification or Resolve) after
	// CurrentWave has advanced. Starting at CurrentWave would leave it
	// unreachable forever and Finish would count it as stuck. Waves whose
	// resources are all terminal/absent are skipped instantly by
	// pendingResourcesInWave, so the rewind cost is negligible.
	for w := 0; w < len(waves); w++ {
		resources := s.pendingResourcesInWave(waves[w], planSet, stateMap, w)
		if len(resources) > 0 {
			// Persist the found wave in either direction: forward when work
			// remains ahead, backward when an earlier wave reopened.
			if w != sess.CurrentWave {
				if err := s.store.UpdateSession(sessionID, sess.Status, w); err != nil {
					return nil, fmt.Errorf("update current wave to %d: %w", w, err)
				}
			}
			return &NextResult{
				Done:      false,
				WaveIndex: w,
				Resources: resources,
			}, nil
		}
	}

	for _, resource := range allResources {
		if ResourceState(resource.State) == StateCommitted && resource.Phase != "verified" {
			gateWave := integrationValidationGateWave(allResources)
			return &NextResult{
				Done: false, WaveIndex: gateWave, ValidationReady: true,
				ValidationWaveIndex: gateWave,
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
			ResourceID:        id,
			State:             StatePending,
			WaveIndex:         waveIndex,
			MaxRetries:        s.cfg.MaxRetries,
			Notes:             action.Reason,
			ExecutionStrategy: action.ExecutionStrategy,
			SourceCandidateID: action.SourceCandidateID,
		}
		if tracked {
			rs.State = ResourceState(sr.State)
			rs.Attempts = sr.Attempts
			rs.ExecutionStrategy = sr.ExecutionStrategy
			rs.SourceCandidateID = sr.SourceCandidateID
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
	return s.PrepareContext(ctx, ContextOptions{SessionID: sessionID, ResourceID: resourceID})
}

func (s *Spec) Commit(ctx context.Context, sessionID, resourceID string, files []CommitFile, notes string, model string) (*CommitResult, error) {
	return nil, fmt.Errorf("legacy commit is disabled: call spec/context, dispatch_requested, worker_acknowledged, spec/execution_start, then commit by attempt_id")
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
		path, err := s.projectFilePath(f.Path)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", f.Path, err)
		}
		dir := filepath.Dir(path)
		if err := s.fs.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}

		contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(f.Content)))

		existing, readErr := s.fs.ReadFile(path)
		if readErr == nil {
			existingHash := fmt.Sprintf("%x", sha256.Sum256(existing))
			if existingHash == contentHash {
				continue
			}
		}

		if err := s.fs.WriteFile(path, []byte(f.Content), 0o644); err != nil {
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

func (s *Spec) Finish(ctx context.Context, sessionID string, force bool) (*FinishResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	// Count from session_resources — the authoritative state store
	allResources, err := s.store.ListSessionResources(sessionID)
	if err != nil {
		return nil, fmt.Errorf("list session resources: %w", err)
	}

	committed := 0
	skipped := 0
	errored := 0
	var validationPending []string
	var validationUnverified []string
	for _, r := range allResources {
		switch ResourceState(r.State) {
		case StateCommitted:
			committed++
			if r.Phase != "verified" {
				validationUnverified = append(validationUnverified, r.ResourceID)
			}
		case StateSkipped:
			skipped++
		case StateErrored, StateRejected, StateTimedOut, StateBlocked:
			errored++
			validationPending = append(validationPending, r.ResourceID)
		default:
			validationPending = append(validationPending, r.ResourceID)
		}
	}
	sort.Strings(validationPending)
	sort.Strings(validationUnverified)
	overview, err := s.ProjectOverview(ctx)
	if err != nil {
		return nil, fmt.Errorf("finish gate: project completion: %w", err)
	}
	completionFields := func(result *FinishResult) *FinishResult {
		result.ProjectCompletion = overview.Project.CompletionStatus
		result.CompletionReason = overview.Project.CompletionReason
		for _, goal := range overview.Project.Goals {
			if goal.Status != "complete" {
				result.IncompleteGoals = append(result.IncompleteGoals, goal.ID)
			}
		}
		return result
	}

	// Mechanical closure validation is never force-skippable. A staged
	// candidate is accepted provisionally so downstream resources can migrate,
	// but the session cannot finish until the final affected-closure gate marks
	// every committed resource verified.
	if len(validationPending) > 0 || len(validationUnverified) > 0 {
		return completionFields(&FinishResult{
			Committed: committed, Skipped: skipped, Errored: errored, Blocked: true,
			ValidationPending: validationPending, ValidationUnverified: validationUnverified,
		}), nil
	}

	// Behavioral gate: the session does not finalize while any of its
	// resources carry unresolved checks. Everything that is not proven
	// (passed/graduated) blocks — including 'malformed', which means the
	// check itself needs re-authoring, not that it may be waved through.
	if !force {
		blocking, err := s.unresolvedChecks(allResources)
		if err != nil {
			return nil, fmt.Errorf("finish gate: %w", err)
		}
		if len(blocking) > 0 {
			return completionFields(&FinishResult{
				Committed:      committed,
				Skipped:        skipped,
				Errored:        errored,
				Blocked:        true,
				BlockingChecks: blocking,
			}), nil
		}
		if overview.CompletionEnforced && overview.Project.CompletionStatus != "complete" {
			return completionFields(&FinishResult{
				Committed: committed, Skipped: skipped, Errored: errored, Blocked: true,
			}), nil
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

	if err := s.store.UpdateSession(sessionID, status, sess.CurrentWave); err != nil {
		return nil, fmt.Errorf("finalize session: %w", err)
	}
	if err := s.store.CompleteApply(sess.ApplyID); err != nil {
		return nil, fmt.Errorf("complete apply: %w", err)
	}
	if err := s.store.ReleaseLock(); err != nil {
		return nil, fmt.Errorf("release lock: %w", err)
	}

	return completionFields(&FinishResult{
		Committed:        committed,
		Skipped:          skipped,
		Errored:          errored,
		ReflectionPrompt: reflectionPrompt,
	}), nil
}

// unresolvedChecks returns every behavioral check attached to the given
// session resources that is not in a proven state. Errors fail closed: if the
// checks cannot be listed, finish is refused rather than waved through.
func (s *Spec) unresolvedChecks(resources []store.SessionResource) ([]BlockingCheck, error) {
	var blocking []BlockingCheck
	for _, r := range resources {
		checks, err := s.store.ListChecksByResource(r.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("list checks for %s: %w", r.ResourceID, err)
		}
		for _, c := range checks {
			switch c.State {
			case "passed", "graduated":
			default:
				blocking = append(blocking, BlockingCheck{
					CheckID:    c.ID,
					ResourceID: c.ResourceID,
					Behavior:   c.Behavior,
					State:      c.State,
				})
			}
		}
	}
	return blocking, nil
}

type WaveVerifyResult struct {
	WaveIndex                     int
	Passed                        bool
	Errors                        []WaveError
	RetryCount                    int
	MaxRetries                    int
	Deferred                      bool
	DeferredReason                string
	IntegrationReady              bool
	IntegrationValidated          bool
	IntegrationGateWave           int
	PendingResources              []string
	ResourceValidationDefinitions int
	ResourceValidationCommands    int
	ValidationAliases             int
	ValidationCommands            int
	ReopenedResources             []string
}

type WaveError struct {
	ResourceID      string
	ResourceIDs     []string
	ValidationIDs   []string
	ValidationKinds []string
	Kind            string
	Message         string
	Files           []string
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
	// A closure gate is one observation of one repository tree followed by one
	// durable state transition. Serialize it with commits, destroys, and other
	// hosts verifying this project so two MCP processes cannot execute and
	// persist the same wave outcome independently. All session state is loaded
	// only after this cross-process boundary is held, allowing a follower to
	// observe the canonical caller's reopen/verification outcome.
	releaseProject, lockErr := s.projectWorkspace().AcquireCommit(ctx)
	if lockErr != nil {
		return &WaveVerifyResult{
			WaveIndex:  waveIndex,
			MaxRetries: s.cfg.WaveMaxRetries,
			Passed:     false,
			Errors: []WaveError{{
				Kind:    "validation_infrastructure",
				Message: fmt.Sprintf("serialize affected-closure validation: %v", lockErr),
			}},
		}
	}
	defer releaseProject()

	result := &WaveVerifyResult{
		WaveIndex:  waveIndex,
		MaxRetries: s.cfg.WaveMaxRetries,
		Passed:     true,
	}
	retryCount, maxRetries, retryErr := s.affectedClosureRetryStatus(ctx, sessionID)
	result.RetryCount = retryCount
	result.MaxRetries = maxRetries
	if retryErr != nil {
		result.Passed = false
		result.Errors = append(result.Errors, WaveError{
			Kind: "session_resources", Message: retryErr.Error(),
		})
		return result
	}

	resources, err := s.store.ListSessionResourcesByWave(sessionID, waveIndex)
	if err != nil {
		result.Passed = false
		result.Errors = append(result.Errors, WaveError{
			Kind:    "session_resources",
			Message: fmt.Sprintf("list wave resources: %v", err),
		})
		return result
	}
	if len(resources) == 0 {
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

	allResources, err := s.store.ListSessionResources(sessionID)
	if err != nil {
		result.Passed = false
		result.Errors = append(result.Errors, WaveError{
			Kind:    "session_resources",
			Message: fmt.Sprintf("list affected resources: %v", err),
		})
		return result
	}
	schedule := scheduleIntegrationValidation(allResources, waveIndex)
	result.IntegrationReady = schedule.Ready
	result.IntegrationGateWave = schedule.GateWave
	result.PendingResources = schedule.PendingResources
	if !schedule.Ready {
		result.Deferred = true
		result.DeferredReason = schedule.Reason
		return result
	}

	plan, err := s.Plan(ctx)
	if err != nil {
		result.Passed = false
		result.Errors = append(result.Errors, WaveError{
			Kind:    "plan",
			Message: fmt.Sprintf("load project validations: %v", err),
		})
	} else if plan != nil {
		if driftErrors := s.sessionOperationFingerprintErrors(sessionID, plan, allResources); len(driftErrors) > 0 {
			result.Passed = false
			result.Errors = append(result.Errors, driftErrors...)
		} else {
			fastPathResourceIDs := committedFastPathResourceIDs(allResources)
			if len(fastPathResourceIDs) > 0 && !s.closureValidationAvailable(plan, fastPathResourceIDs) {
				result.Passed = false
				for _, resourceID := range fastPathResourceIDs {
					result.Errors = append(result.Errors, WaveError{
						ResourceID: resourceID, Kind: "closure_validation_unavailable",
						Message: "fast-path compatibility cannot be certified because no executable validator covers this committed resource",
					})
				}
			} else {
				s.runAffectedClosureValidationBatch(ctx, plan.Registry, allResources, sessionID, result)
				result.IntegrationValidated = true
			}
		}
	}
	if result.Passed {
		if plan != nil {
			s.settleFastPathResources(plan, allResources, result)
			s.settleLegacyVerificationContracts(sessionID, plan, allResources, result)
		}
	}
	if result.Passed {
		s.setIntegrationValidationPhase(allResources, result, "verified")
		if plan != nil {
			for _, resource := range allResources {
				if ResourceState(resource.State) == StateCommitted {
					s.markAmendmentVerification(resource.ResourceID, plan.Registry.Resources[resource.ResourceID], true)
				}
			}
		}
	} else {
		s.reopenValidationOwners(ctx, sessionID, allResources, result)
	}

	return result
}

func (s *Spec) setIntegrationValidationPhase(resources []store.SessionResource, result *WaveVerifyResult, phase string) {
	for _, resource := range resources {
		if ResourceState(resource.State) != StateCommitted {
			continue
		}
		if err := s.store.UpdateSessionResourcePhase(
			resource.SessionID, resource.ResourceID, phase, resource.Attempts,
		); err != nil {
			result.Passed = false
			result.Errors = append(result.Errors, WaveError{
				ResourceID: resource.ResourceID,
				Kind:       "session_resources",
				Message:    fmt.Sprintf("record integration validation phase: %v", err),
			})
		}
	}
}

func (s *Spec) reopenValidationOwners(ctx context.Context, sessionID string, resources []store.SessionResource, result *WaveVerifyResult) {
	// A closure batch is one observation of one toolchain state. If any command
	// could not execute, diagnostics from wrapper commands in the same batch
	// (for example `make` returning 2 because `cargo` was absent) are not valid
	// evidence that accepted source is broken. Preserve every candidate and
	// rerun the whole gate after the validation environment is repaired.
	for _, waveErr := range result.Errors {
		if waveErr.Kind == "validation_infrastructure" {
			s.setIntegrationValidationPhase(resources, result, "integration_failed")
			return
		}
	}

	matched := make(map[string]bool)
	fingerprintDrift := make(map[string]bool)
	var reasons []string
	for _, waveErr := range result.Errors {
		ids := append([]string(nil), waveErr.ResourceIDs...)
		if waveErr.ResourceID != "" {
			ids = append(ids, waveErr.ResourceID)
		}
		for _, resourceID := range ids {
			matched[resourceID] = true
			if waveErr.Kind == "operation_fingerprint_drift" {
				fingerprintDrift[resourceID] = true
			}
		}
		if len(ids) > 0 && waveErr.Message != "" {
			reasons = append(reasons, waveErr.Message)
		}
	}
	var committed []string
	var staleSkipped []store.SessionResource
	for _, resource := range resources {
		if !matched[resource.ResourceID] {
			continue
		}
		switch ResourceState(resource.State) {
		case StateCommitted:
			committed = append(committed, resource.ResourceID)
		case StateSkipped:
			// A skip resolves one immutable operation. If the CUE inputs changed,
			// that waiver cannot silently carry over to the new operation.
			if fingerprintDrift[resource.ResourceID] {
				staleSkipped = append(staleSkipped, resource)
			}
		}
	}
	if len(committed) == 0 && len(staleSkipped) == 0 {
		s.setIntegrationValidationPhase(resources, result, "integration_failed")
		return
	}
	reason := "affected-closure validation failed"
	if len(reasons) > 0 {
		reason += ": " + strings.Join(reasons, "\n")
	}
	if !s.routeClosureValidationOwners(ctx, sessionID, committed, reason, resources, result) {
		return
	}
	for _, resource := range staleSkipped {
		resource.State = string(StatePending)
		resource.Phase = "integration_failed"
		resource.ExecutionStrategy = planpkg.ExecutionStrategyGenerate
		resource.SourceCandidateID = ""
		resource.LastError = reason
		resource.JobID = ""
		if err := s.store.UpsertSessionResource(resource); err != nil {
			result.Errors = append(result.Errors, WaveError{
				ResourceID: resource.ResourceID, Kind: "session_resources",
				Message: fmt.Sprintf("reopen stale skipped operation: %v", err),
			})
			continue
		}
		result.ReopenedResources = append(result.ReopenedResources, resource.ResourceID)
	}
	sort.Strings(result.ReopenedResources)
}

func declaredValidationOwners(declared []string, resources []store.SessionResource) []string {
	available := make(map[string]bool, len(resources))
	for _, resource := range resources {
		if ResourceState(resource.State) == StateCommitted {
			available[resource.ResourceID] = true
		}
	}
	owners := make([]string, 0, len(declared))
	for _, resourceID := range declared {
		if available[resourceID] {
			owners = append(owners, resourceID)
		}
	}
	sort.Strings(owners)
	return uniqueSortedValidationOwners(owners)
}

func committedResourceIDs(resources []store.SessionResource) []string {
	owners := make([]string, 0, len(resources))
	for _, resource := range resources {
		if ResourceState(resource.State) == StateCommitted {
			owners = append(owners, resource.ResourceID)
		}
	}
	sort.Strings(owners)
	return uniqueSortedValidationOwners(owners)
}

// runProjectValidations is retained for focused validation tests and callers
// that do not have a reconciled session. Operational wave verification uses
// runAffectedClosureValidationBatch so all aliases share a per-wave schedule.
func (s *Spec) runProjectValidations(ctx context.Context, validations []cuepkg.Validation, resources []store.SessionResource, result *WaveVerifyResult) {
	if len(validations) == 0 {
		return
	}
	results, err := RunValidations(ctx, validations, filepath.Dir(s.cfg.SpecDir))
	if err != nil {
		result.Passed = false
		result.Errors = append(result.Errors, WaveError{Kind: "project_validation", Message: fmt.Sprintf("project validation error: %v", err)})
		return
	}
	for _, validation := range results {
		if validation.Passed {
			continue
		}
		result.Passed = false
		if validationInfrastructureFailure(validation) {
			result.Errors = append(result.Errors, validationInfrastructureWaveError(validation))
			continue
		}
		result.Errors = append(result.Errors, waveErrorForResources(
			s.attributeErrorToResources(validationDiagnosticOutput(validation), resources),
			"project_validation", fmt.Sprintf("%s: %s", validation.Kind, validation.Message),
		))
	}
}

func validationInfrastructureFailure(result ValidationResult) bool {
	return result.Classification == "error"
}

func validationInfrastructureWaveError(result ValidationResult) WaveError {
	return WaveError{
		Kind:    "validation_infrastructure",
		Message: fmt.Sprintf("%s: %s", result.Kind, result.Message),
	}
}

// attributeErrorToResource pins a verification/validation failure to a single
// resource only when exactly ONE resource's generated files appear in the error
// output. Tree-wide failures (rustfmt diffs, multi-file compiler output) mention
// files from several resources; attributing such a failure to the first match
// churns an innocent resource until the stall guard force-skips it. When zero or
// multiple resources match, this returns "" (unattributed) and the
// orchestrator-side verify agent maps files itself.
func (s *Spec) attributeErrorToResource(errorOutput string, resources []store.SessionResource) string {
	matched := s.attributeErrorToResources(errorOutput, resources)
	if len(matched) == 1 {
		return matched[0]
	}
	return ""
}

// attributeErrorToResources returns every accepted resource owner mentioned
// by diagnostics. A closure failure may span a provider and several consumers;
// all matched owners must be repaired together rather than charging the first
// file in the compiler output.
func (s *Spec) attributeErrorToResources(errorOutput string, resources []store.SessionResource) []string {
	filePaths := parseErrorFilePaths(errorOutput)
	if len(filePaths) == 0 {
		// Ordinary tool output names files for many non-diagnostic reasons (for
		// example, `Running unittests src/lib.rs`). Only structured diagnostic
		// spans are safe evidence for automatic repair ownership.
		return nil
	}

	generatedByResource := make(map[string][]store.GeneratedFile, len(resources))
	for _, r := range resources {
		genFiles, err := s.store.GetGeneratedFiles(r.ResourceID)
		if err != nil {
			// A partial ownership snapshot can manufacture false uniqueness. Leave
			// the diagnostic unattributed so the validation's declared repair owners
			// or triage policy handles it without guessing.
			return nil
		}
		generatedByResource[r.ResourceID] = genFiles
	}

	// A module/build file may be recorded against every resource allowed to
	// integrate through it. If a diagnostic also names uniquely-owned source
	// files, repair those roots first; treating the shared integration path as
	// another root fans one syntax error out to every consumer. A later gate can
	// still surface a genuine shared-only error after the unique roots are clean.
	allMatched := make(map[string]bool)
	uniqueMatched := make(map[string]bool)
	for _, errorPath := range filePaths {
		pathOwners := make(map[string]bool)
		for _, r := range resources {
			for _, f := range generatedByResource[r.ResourceID] {
				if resourceFileMatchesError(f.Path, []string{errorPath}, "") {
					pathOwners[r.ResourceID] = true
					break
				}
			}
		}
		for resourceID := range pathOwners {
			allMatched[resourceID] = true
		}
		if len(pathOwners) == 1 {
			for resourceID := range pathOwners {
				uniqueMatched[resourceID] = true
			}
		}
	}

	matched := allMatched
	if len(uniqueMatched) > 0 {
		matched = uniqueMatched
	}
	result := make([]string, 0, len(matched))
	for id := range matched {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func waveErrorForResources(resourceIDs []string, kind, message string) WaveError {
	ids := append([]string(nil), resourceIDs...)
	sort.Strings(ids)
	result := WaveError{ResourceIDs: ids, Kind: kind, Message: message}
	if len(ids) == 1 {
		result.ResourceID = ids[0]
	}
	return result
}

// resourceFileMatchesError reports whether a generated file path matches a
// structured diagnostic path. Raw output substring matching is intentionally
// forbidden because tool progress lines are not ownership evidence.
func resourceFileMatchesError(filePath string, filePaths []string, _ string) bool {
	for _, errPath := range filePaths {
		if errPath == filePath || strings.HasSuffix(errPath, "/"+filePath) || strings.HasSuffix(filePath, "/"+errPath) {
			return true
		}
	}
	return false
}

func validationDiagnosticOutput(result ValidationResult) string {
	if result.Execution == nil {
		return result.Message
	}
	return result.Execution.Stdout + "\n" + result.Execution.Stderr
}

// forceTargetIntoActions ensures the target resource appears in the action
// list. If it is already present, the list is returned unchanged. Otherwise a
// new ActionModify with reason "forced regeneration" is appended.
func forceTargetIntoActions(
	actions []planpkg.PlannedAction,
	target string,
	reg *cuepkg.Registry,
	hashMaps ...map[string]string,
) []planpkg.PlannedAction {
	if _, exists := reg.Resources[target]; !exists {
		return actions
	}
	for _, a := range actions {
		if a.ResourceID == target {
			return actions
		}
	}
	action := planpkg.PlannedAction{
		ResourceID:        target,
		Kind:              planpkg.ActionModify,
		Reason:            "forced regeneration",
		ExecutionStrategy: planpkg.ExecutionStrategyGenerate,
	}
	var generationInputHash, dependencyContractHash, verificationContractHash string
	if len(hashMaps) > 0 {
		generationInputHash = hashMaps[0][target]
	}
	if len(hashMaps) > 1 {
		dependencyContractHash = hashMaps[1][target]
	}
	if len(hashMaps) > 2 {
		verificationContractHash = hashMaps[2][target]
	}
	planpkg.AssignOperationIdentity(
		&action,
		generationInputHash,
		dependencyContractHash,
		verificationContractHash,
	)
	return append(actions, action)
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

func filterWavesForActions(waves [][]string, actions []planpkg.PlannedAction) [][]string {
	actionIDs := make(map[string]bool, len(actions))
	for _, action := range actions {
		if action.Kind != planpkg.ActionDestroy {
			actionIDs[action.ResourceID] = true
		}
	}
	filtered := make([][]string, 0, len(waves))
	for _, wave := range waves {
		kept := make([]string, 0, len(wave))
		for _, resourceID := range wave {
			if actionIDs[resourceID] {
				kept = append(kept, resourceID)
			}
		}
		if len(kept) > 0 {
			filtered = append(filtered, kept)
		}
	}
	return filtered
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
4. spec/context_batch   → pass limit=the number of immediately idle workers;
                          receive that many immutable, unclaimed contexts
5. Feed those contexts to a bounded, reusable host worker pool. Workers process
   one resource at a time and are reassigned across waves instead of recreated:
   a. inspect the prepared context. NoDispatch=true is already reconciled and
      Blocked=true is not executable: do not assign either one; return to
      spec/context_batch or spec/next.
   b. spec/dispatch_event dispatch_requested → reserve the actual worker and
                          atomically claim the resource by prepared AttemptID
   c. feed the prepared context to that worker; it immediately records
      spec/dispatch_event worker_acknowledged with the same identities
   d. use the prepared context → scoped prompt + system_prompt + invariants
                          (constraints to honor, not to self-grade)
   e. spec/execution_start → register crest-execution-v1 and exact
                          host/model/settings/tools/template/context metadata
   f. Generate with the role returned by spec/context using that prompt
   g. spec/commit       → attempt_id + files + notes
                          The server stages immutable candidate files. Resource
                          commands are intentionally deferred because focused
                          tests may still compile stale downstream consumers.
   h. If Committed=false: call spec/context for the retry (it now carries the
      failure under '## Previous Errors' and any triage guidance under
      '## Guidance'), then record dispatch_requested and worker_acknowledged
      for the new DispatchID before execution_start; regenerate and re-commit
      up to max_retries. Then
      spec/resolve (guidance) or spec/skip.
6. After each terminal wave call spec/verify_wave. Deferred=true means no
   project command ran; continue migrating. The final affected wave runs the
   deduplicated resource/type/test/project batch and reopens every owner named
   by a failure together.
7. Repeat from step 4 until context_batch returns done=true and final validation
   passes. If validation_ready=true, call spec/verify_wave with
   validation_wave_index even when no worker ran; fast-path resources must not
   suppress the closure gate.
8. spec/finish          → finalize; if reflection_prompt is non-empty, run it
                          with a sub-agent and submit via spec/record_learnings

The core loop is generate → commit → validate → retry-with-feedback.

## Observability

  spec/status (session_id) → session overview; spec/wave_status → per-resource view.

DO NOT:
  - Write implementation code directly — every file must come from a sub-agent
  - Spawn a fresh sub-agent for every resource; reuse a bounded host worker pool
    and let CrestSpec's explicit fast paths skip generation safely
========================================================================`
}

func dispatchInstructions(resourceID string) string {
	return fmt.Sprintf(`Generate code for %s with a sub-agent (sonnet by default).

1. Reserve a real idle worker and call spec/dispatch_event dispatch_requested
   with this DispatchID, the actual worker_id, and stable host_session_id. This
   atomically claims the resource by this AttemptID. A replay is idempotent.
2. Give the sub-agent the system_prompt and prompt from this result. It must
   immediately record worker_acknowledged with the same dispatch and identities.
3. Before generation, call spec/execution_start with protocol_version
   crest-execution-v1, this attempt_id/context_hash/template_hashes, and the
   exact host, model, settings, tools, permissions, and system instructions.
4. The sub-agent for the returned role produces the files (path + full
   content), honoring the
   invariants in this result as hard constraints.
5. Call spec/commit with attempt_id, files, and notes. The server derives the
   session/resource/model from SQLite and stages the immutable candidate.
   Resource commands run later in the affected-closure batch so stale planned
   consumers cannot reject a correct provider. Verification of behavior belongs
   to behavioral checks (spec/verify), not self-judgment.
6. If Committed=false, call spec/context again — the failure, corrective route,
   role handoff,
   guidance are injected into the new prompt — then repeat dispatch_requested,
   worker_acknowledged, and execution_start for the new DispatchID before retrying.
   A rebuild_context response abandons the stale snapshot without charging a
   retry; request a fresh context and repeat the same ordering.`, resourceID)
}
