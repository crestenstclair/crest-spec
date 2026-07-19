package spec

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/execution"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// PrepareContext creates an immutable generation attempt and context manifest.
// Preparation never claims the resource. The host must reserve a real worker
// slot and record dispatch_requested with the returned DispatchID before it
// assigns the context or acknowledges the worker.
func (s *Spec) PrepareContext(ctx context.Context, opts ContextOptions) (*ContextResult, error) {
	if opts.SessionID == "" || opts.ResourceID == "" {
		return nil, fmt.Errorf("session_id and resource_id are required")
	}
	state, err := s.loadContextPreparationState(ctx, opts.SessionID)
	if err != nil {
		return nil, err
	}
	return s.prepareContext(ctx, opts, state)
}

type contextPreparationState struct {
	session    *store.Session
	plan       []planpkg.PlannedAction
	planResult *PlanResult
}

func (s *Spec) loadContextPreparationState(ctx context.Context, sessionID string) (*contextPreparationState, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
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
	return &contextPreparationState{session: sess, plan: plan, planResult: planResult}, nil
}

// PrepareWaveContexts prepares at most the host's immediately available worker
// capacity. Contexts remain unclaimed until dispatch_requested atomically binds
// the prepared attempt to an actual worker. Repeated polling replays durable
// unclaimed contexts rather than consuming attempts or producing snapshots
// that wait behind a smaller worker pool.
func (s *Spec) PrepareWaveContexts(ctx context.Context, opts WaveContextOptions) (*WaveContextResult, error) {
	if opts.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if opts.Limit < 0 {
		return nil, fmt.Errorf("context batch limit must be nonnegative")
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 1
	}
	wave, err := s.Next(ctx, opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("next wave: %w", err)
	}
	result := &WaveContextResult{
		Done: wave.Done, WaveIndex: wave.WaveIndex, Contexts: []WaveContextItem{}, ValidationWaveIndex: -1,
	}
	if wave.ValidationReady {
		result.ValidationReady = true
		result.ValidationWaveIndex = wave.ValidationWaveIndex
		return result, nil
	}
	if wave.Done {
		return result, nil
	}
	eligible := make([]ResourceStatus, 0, len(wave.Resources))
	for _, resource := range wave.Resources {
		if contextStateDispatchable(string(resource.State)) {
			eligible = append(eligible, resource)
		}
	}
	if len(eligible) == 0 {
		return result, nil
	}

	prepared, err := s.preparedUnclaimedContexts(ctx, opts.SessionID)
	if err != nil {
		return nil, err
	}
	assigned := 0
	var state *contextPreparationState
	loadSharedState := func() (*contextPreparationState, error) {
		if state != nil {
			return state, nil
		}
		batch, batchErr := s.store.StartContextBatch(ctx, opts.SessionID, wave.WaveIndex)
		if batchErr != nil {
			return nil, fmt.Errorf("start shared context preparation: %w", batchErr)
		}
		loaded, loadErr := s.loadContextPreparationState(ctx, opts.SessionID)
		if loadErr != nil {
			_, _ = s.store.FinishContextBatch(ctx, batch.ID, true)
			return nil, loadErr
		}
		if _, finishErr := s.store.FinishContextBatch(ctx, batch.ID, false); finishErr != nil {
			return nil, fmt.Errorf("finish shared context preparation: %w", finishErr)
		}
		state = loaded
		return state, nil
	}
	for _, resource := range eligible {
		if assigned >= limit {
			break
		}
		if existing := prepared[resource.ResourceID]; existing != nil {
			if compatibilityErr := preparedContextCompatibility(
				existing.manifest, opts.Role, opts.BudgetTokens, "",
			); compatibilityErr != nil {
				result.Contexts = append(result.Contexts, WaveContextItem{
					ResourceID: resource.ResourceID, Error: compatibilityErr.Error(),
				})
				continue
			}
			contextResult, invalidated, replayErr := s.replayPreparedContext(ctx, existing.lifecycle, existing.manifest)
			if replayErr != nil {
				result.Contexts = append(result.Contexts, WaveContextItem{
					ResourceID: resource.ResourceID, Error: replayErr.Error(),
				})
				continue
			}
			if invalidated {
				delete(prepared, resource.ResourceID)
			} else {
				result.Contexts = append(result.Contexts, WaveContextItem{
					ResourceID: resource.ResourceID, Context: contextResult,
				})
				if !contextResult.Blocked && !contextResult.NoDispatch {
					assigned++
				}
				continue
			}
		}
		shared, sharedErr := loadSharedState()
		if sharedErr != nil {
			return nil, sharedErr
		}
		contextResult, prepareErr := s.prepareContext(ctx, ContextOptions{
			SessionID: opts.SessionID, ResourceID: resource.ResourceID,
			Role: opts.Role, BudgetTokens: opts.BudgetTokens,
		}, shared)
		item := WaveContextItem{ResourceID: resource.ResourceID, Context: contextResult}
		if prepareErr != nil {
			item.Error = prepareErr.Error()
		}
		result.Contexts = append(result.Contexts, item)
		if contextResult != nil && !contextResult.Blocked && !contextResult.NoDispatch {
			assigned++
		}
	}
	return result, nil
}

type preparedUnclaimedContext struct {
	lifecycle store.DispatchLifecycle
	manifest  *store.ContextManifest
}

func preparedContextCompatibility(
	manifest *store.ContextManifest,
	role string,
	budgetTokens int,
	parentAttemptID string,
) error {
	if manifest == nil {
		return fmt.Errorf("prepared context is unavailable")
	}
	if role != "" && role != manifest.Attempt.Role {
		return fmt.Errorf(
			"resource %s already has unclaimed prepared attempt %s with role %s; dispatch it or abandon it before requesting role %s",
			manifest.Attempt.ResourceID, manifest.Attempt.ID, manifest.Attempt.Role, role,
		)
	}
	if budgetTokens != 0 && contextmanifest.NormalizeBudget(budgetTokens) != manifest.BudgetTokens {
		return fmt.Errorf(
			"resource %s already has unclaimed prepared attempt %s with budget %d; dispatch it or abandon it before requesting budget %d",
			manifest.Attempt.ResourceID, manifest.Attempt.ID, manifest.BudgetTokens,
			contextmanifest.NormalizeBudget(budgetTokens),
		)
	}
	if parentAttemptID != "" && parentAttemptID != manifest.Attempt.ParentAttemptID {
		return fmt.Errorf(
			"resource %s already has unclaimed prepared attempt %s with parent %s; dispatch it or abandon it before requesting parent %s",
			manifest.Attempt.ResourceID, manifest.Attempt.ID, manifest.Attempt.ParentAttemptID, parentAttemptID,
		)
	}
	return nil
}

// preparedUnclaimedContexts returns the durable queue that context_batch may
// replay after a host restart or duplicate poll. A prepared attempt is not a
// resource claim: dispatch_requested_at must still be absent.
func (s *Spec) preparedUnclaimedContexts(
	ctx context.Context,
	sessionID string,
) (map[string]*preparedUnclaimedContext, error) {
	lifecycles, err := s.store.ListDispatchLifecyclesBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list prepared dispatch contexts: %w", err)
	}
	result := make(map[string]*preparedUnclaimedContext)
	for _, lifecycle := range lifecycles {
		if lifecycle.AttemptID == "" || lifecycle.ContextPreparedAt == nil ||
			lifecycle.DispatchRequestedAt != nil || lifecycle.ExecutionRegisteredAt != nil ||
			lifecycle.CandidateSubmittedAt != nil || lifecycle.CandidateAcceptedAt != nil ||
			lifecycle.CandidateRejectedAt != nil {
			continue
		}
		manifest, manifestErr := s.store.GetContextManifestByAttempt(ctx, lifecycle.AttemptID)
		if manifestErr != nil {
			return nil, fmt.Errorf("load prepared context attempt %s: %w", lifecycle.AttemptID, manifestErr)
		}
		// A budget-blocked context is an audit record, not queued work. Replaying
		// it would ignore a later larger budget and permanently occupy the batch.
		if manifest.Attempt.Status != "context_prepared" {
			continue
		}
		copy := lifecycle
		result[lifecycle.ResourceID] = &preparedUnclaimedContext{lifecycle: copy, manifest: manifest}
	}
	return result, nil
}

// replayPreparedContext returns the exact immutable prompt from an unclaimed
// attempt. If repository bytes changed while it was idle, the attempt is
// atomically abandoned and the caller prepares a replacement in the same
// batch without charging a model retry.
func (s *Spec) replayPreparedContext(
	ctx context.Context,
	lifecycle store.DispatchLifecycle,
	manifest *store.ContextManifest,
) (*ContextResult, bool, error) {
	if !manifest.Blocked {
		if sourceErr := s.validateSelectedRepositorySources(manifest); sourceErr != nil {
			reason := fmt.Sprintf("prepared context became stale before dispatch: %v", sourceErr)
			recovered, recoverErr := s.store.AbandonPreparedContext(ctx, store.PreparedContextAbandonment{
				AttemptID: manifest.Attempt.ID, Reason: reason,
				EvidenceSource: "context_batch_source_refresh",
			})
			if recoverErr != nil {
				return nil, false, fmt.Errorf("refresh stale prepared context: %w", recoverErr)
			}
			if !recovered.Recovered {
				return nil, false, fmt.Errorf("rebuild_context: stale prepared attempt changed concurrently; poll context_batch again")
			}
			return nil, true, nil
		}
	}
	policy, err := execution.LookupRole(manifest.Attempt.Role)
	if err != nil {
		return nil, false, fmt.Errorf("replay prepared context role: %w", err)
	}
	sections := make([]contextmanifest.Section, 0, len(manifest.Sections))
	for _, section := range manifest.Sections {
		sections = append(sections, contextmanifest.Section{
			Ordinal: section.Ordinal, Kind: section.Kind, Title: section.Title,
			SourceKind: section.SourceKind, SourceID: section.SourceID, SourcePath: section.SourcePath,
			Priority: contextmanifest.Priority(section.Priority), Mandatory: section.Mandatory,
			Decision: contextmanifest.Decision(section.Decision), Reason: section.Reason,
			OriginalHash: section.OriginalHash, SelectedHash: section.ContentHash,
			OriginalBytes: section.OriginalBytes, SelectedBytes: section.SelectedBytes,
			EstimatedTokens: section.EstimatedTokens, Content: section.Content,
		})
	}
	instructions := dispatchInstructions(manifest.Attempt.ResourceID)
	if manifest.Blocked {
		instructions = "Context was not dispatched: " + manifest.BlockedReason
	}
	return &ContextResult{
		DispatchID: lifecycle.ID, AttemptID: manifest.Attempt.ID,
		ContextManifestID: manifest.ID, ContextHash: manifest.ContextHash,
		Role: manifest.Attempt.Role, RecommendedRole: manifest.Attempt.Role,
		RolePolicyVersion: manifest.Attempt.RolePolicyVersion, ContextPolicy: policy.ContextPolicy,
		SelectorVersion: manifest.SelectorVersion, EstimatorVersion: manifest.EstimatorVersion,
		TemplateHashes: manifest.TemplateHashes, BudgetTokens: manifest.BudgetTokens,
		EstimatedTokens: manifest.EstimatedTokens, Blocked: manifest.Blocked,
		BlockedReason: manifest.BlockedReason, Sections: sections,
		SystemPrompt: manifest.SystemPrompt, Prompt: manifest.RenderedPrompt,
		Instructions: instructions,
	}, false, nil
}

func (s *Spec) prepareContext(ctx context.Context, opts ContextOptions, state *contextPreparationState) (*ContextResult, error) {
	sess := state.session
	plan := state.plan
	planResult := state.planResult
	action, ok := contextPlanAction(plan, opts.ResourceID)
	if !ok {
		return nil, fmt.Errorf("resource %s is not an operation in session %s", opts.ResourceID, opts.SessionID)
	}
	if !operationInputsMatchCurrent(action, state.planResult) {
		return nil, fmt.Errorf(
			"resource %s plan inputs changed after session creation; abandon and re-plan before preparing context",
			opts.ResourceID,
		)
	}
	sessionResource, err := s.store.GetSessionResource(opts.SessionID, opts.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("resource %s is not dispatchable in session %s: %w", opts.ResourceID, opts.SessionID, err)
	}
	if err := s.enforceBehavioralWitnessRepairBudget(ctx, opts.SessionID, opts.ResourceID); err != nil {
		return nil, err
	}
	reconciled, err := s.reconcilePoisonedValidationResource(ctx, sessionResource, &action, planResult)
	if err != nil {
		return nil, err
	}
	if reconciled.changed {
		instructions := "The poisoned validation projection was reconciled from immutable session provenance. " +
			"No generation attempt was created and no worker may be dispatched. Call spec/next."
		if reconciled.strategy == planpkg.ExecutionStrategyValidateExisting {
			instructions = "The original immutable validate_existing operation was restored. " +
				"No generation attempt was created and no worker may be dispatched. Call spec/next to run closure validation."
		}
		return &ContextResult{
			NoDispatch: true, ReinstatedCandidateID: reconciled.candidateID,
			HandoffID: reconciled.handoffID, Instructions: instructions,
		}, nil
	}
	if err := s.enforceAffectedClosureRepairBudget(ctx, opts.SessionID, opts.ResourceID); err != nil {
		return nil, err
	}
	if !contextStateDispatchable(sessionResource.State) {
		return nil, fmt.Errorf(
			"resource %s is not dispatchable in session %s: state is %s",
			opts.ResourceID,
			opts.SessionID,
			sessionResource.State,
		)
	}
	// Context creation is intentionally unclaimed, so response loss must be
	// idempotent at the durable attempt level. Reuse the active immutable
	// context instead of creating a second attempt for the same resource.
	prepared, err := s.preparedUnclaimedContexts(ctx, opts.SessionID)
	if err != nil {
		return nil, err
	}
	if existing := prepared[opts.ResourceID]; existing != nil {
		if compatibilityErr := preparedContextCompatibility(
			existing.manifest, opts.Role, opts.BudgetTokens, opts.ParentAttemptID,
		); compatibilityErr != nil {
			return nil, compatibilityErr
		}
		replayed, invalidated, replayErr := s.replayPreparedContext(ctx, existing.lifecycle, existing.manifest)
		if replayErr != nil {
			return nil, replayErr
		}
		if !invalidated {
			return replayed, nil
		}
	}
	handoff, err := s.pendingContextHandoff(ctx, opts.SessionID, opts.ResourceID)
	if err != nil {
		return nil, err
	}
	if handoff != nil {
		if opts.Role != "" && opts.Role != handoff.TargetRole {
			return nil, fmt.Errorf("pending handoff requires role %s, got %s", handoff.TargetRole, opts.Role)
		}
		opts.Role = handoff.TargetRole
	} else if opts.Role == "" {
		opts.Role = action.RecommendedRole
	}
	rolePolicy, err := execution.LookupRole(opts.Role)
	if err != nil {
		return nil, err
	}
	usedAttempts, err := s.usedGenerationAttempts(ctx, opts.SessionID, opts.ResourceID)
	if err != nil {
		return nil, err
	}
	if sessionResource.MaxRetries > 0 && usedAttempts >= sessionResource.MaxRetries {
		return nil, fmt.Errorf(
			"resource %s exhausted max retries in session %s: attempts=%d max_retries=%d",
			opts.ResourceID,
			opts.SessionID,
			usedAttempts,
			sessionResource.MaxRetries,
		)
	}

	resource, ok := planResult.Registry.Resources[opts.ResourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", opts.ResourceID)
	}
	lifecycle, err := s.store.RequestContext(ctx, store.DispatchRequest{
		ID: opts.DispatchID, SessionID: opts.SessionID, ResourceID: opts.ResourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("record context request: %w", err)
	}
	opts.DispatchID = lifecycle.ID

	runtimeContext, runtimeErr := s.loadGenerationRuntimeContext(
		ctx, resource, planResult.Registry, sess.ApplyID, opts.SessionID, sessionResource,
	)

	systemPrompt := promptpkg.BuildSystemPrompt(planResult.Registry.Project)
	resourcePrompt := promptpkg.BuildResourcePrompt(resource, planResult.Registry)
	candidates := s.contextCandidates(contextCandidateInput{
		action: action, resource: resource, registry: planResult.Registry,
		runtime: runtimeContext, runtimeErr: runtimeErr,
		systemPrompt: systemPrompt, resourcePrompt: resourcePrompt,
		projectStatus: s.contextProjectStatus(ctx, action, planResult.Registry),
	})
	budgetTokens := opts.BudgetTokens
	if budgetTokens == 0 {
		budgetTokens = rolePolicy.DefaultBudgetTokens
	}
	selection := contextmanifest.Select(candidates, budgetTokens)
	templateHashes := map[string]string{
		promptpkg.SystemTemplateVersion:   contextmanifest.Hash(systemPrompt),
		promptpkg.ResourceTemplateVersion: contextmanifest.Hash(resourcePrompt),
	}
	templateJSON, _ := json.Marshal(templateHashes)
	selection.ContextHash = contextmanifest.Hash(selection.ContextHash + "\n" + string(templateJSON))

	selectedSystemPrompt := ""
	for _, section := range selection.Sections {
		if section.Kind == "system_instructions" && (section.Decision == contextmanifest.Included || section.Decision == contextmanifest.Truncated) {
			selectedSystemPrompt = section.Content
			break
		}
	}
	userPrompt := renderContextPrompt(selection, "system_instructions")
	originalBytes, selectedBytes := contextByteTotals(selection.Sections)

	manifest := store.ContextManifest{
		ID: uuid.NewString(),
		Attempt: store.GenerationAttempt{
			ID: uuid.NewString(), SessionID: opts.SessionID, ApplyID: sess.ApplyID,
			ResourceID: opts.ResourceID, PlanOperationID: action.OperationID,
			ParentAttemptID: opts.ParentAttemptID, Role: opts.Role,
			RolePolicyVersion: rolePolicy.Version,
		},
		SelectorVersion: selection.SelectorVersion, EstimatorVersion: selection.EstimatorVersion,
		SelectionStrategy: "goal-directed-priority-v1", BudgetTokens: selection.BudgetTokens,
		EstimatedTokens: selection.EstimatedTokens, OriginalBytes: originalBytes, SelectedBytes: selectedBytes,
		TemplateHashes: templateHashes, SystemPrompt: selectedSystemPrompt, RenderedPrompt: userPrompt,
		ContextHash: selection.ContextHash, Blocked: selection.Blocked, BlockedReason: selection.BlockedReason,
	}
	for _, section := range selection.Sections {
		manifest.Sections = append(manifest.Sections, store.ContextManifestSection{
			ID: uuid.NewString(), Ordinal: section.Ordinal, Kind: section.Kind, Title: section.Title,
			SourceKind: section.SourceKind, SourceID: section.SourceID, SourcePath: section.SourcePath,
			Priority: int(section.Priority), Mandatory: section.Mandatory, Decision: string(section.Decision),
			Reason: section.Reason, OriginalHash: section.OriginalHash, OriginalBytes: section.OriginalBytes,
			SelectedBytes: section.SelectedBytes, EstimatedTokens: section.EstimatedTokens, Content: section.Content,
		})
	}
	handoffID := ""
	if handoff != nil && !selection.Blocked {
		handoffID = handoff.ID
	}
	persisted, err := s.store.CreateContextManifest(ctx, store.ContextManifestWrite{
		Manifest: manifest, HandoffID: handoffID,
		DispatchLifecycleID: opts.DispatchID,
	})
	if err != nil {
		return nil, fmt.Errorf("persist context attempt: %w", err)
	}

	var invariants []InvariantInfo
	for _, invariant := range planResult.Registry.Project.Invariants {
		invariants = append(invariants, InvariantInfo{Text: invariant.Text, Rationale: invariant.Meta.Rationale})
	}
	instructions := dispatchInstructions(opts.ResourceID)
	if selection.Blocked {
		instructions = "Context was not dispatched: " + selection.BlockedReason
	}
	return &ContextResult{
		DispatchID: opts.DispatchID, AttemptID: persisted.Attempt.ID,
		ContextManifestID: persisted.ID, ContextHash: persisted.ContextHash,
		Role: opts.Role, RecommendedRole: action.RecommendedRole, RolePolicyVersion: rolePolicy.Version,
		ContextPolicy: rolePolicy.ContextPolicy, HandoffID: handoffID,
		SelectorVersion: persisted.SelectorVersion, EstimatorVersion: persisted.EstimatorVersion,
		TemplateHashes: persisted.TemplateHashes, BudgetTokens: persisted.BudgetTokens,
		EstimatedTokens: persisted.EstimatedTokens, Blocked: persisted.Blocked, BlockedReason: persisted.BlockedReason,
		Sections: selection.Sections, SystemPrompt: persisted.SystemPrompt, Prompt: persisted.RenderedPrompt,
		Instructions: instructions, Invariants: invariants,
	}, nil
}

func contextStateDispatchable(state string) bool {
	switch state {
	case "pending", "rejected", "errored", "timed_out", "blocked":
		return true
	default:
		return false
	}
}

func (s *Spec) usedGenerationAttempts(ctx context.Context, sessionID, resourceID string) (int, error) {
	attempts, err := s.store.CountRepairEpochExecutionAttempts(ctx, sessionID, resourceID)
	if err != nil {
		return 0, fmt.Errorf("count current repair-epoch execution attempts for retry limit: %w", err)
	}
	return attempts, nil
}

func (s *Spec) pendingContextHandoff(ctx context.Context, sessionID, resourceID string) (*store.AttemptHandoff, error) {
	attempts, err := s.store.ListGenerationAttemptsBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list generation attempts for handoff: %w", err)
	}
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := attempts[index]
		if attempt.ResourceID != resourceID {
			continue
		}
		handoffs, listErr := s.store.ListAttemptHandoffsBySource(ctx, attempt.ID)
		if listErr != nil {
			return nil, fmt.Errorf("list role handoffs: %w", listErr)
		}
		for handoffIndex := len(handoffs) - 1; handoffIndex >= 0; handoffIndex-- {
			if handoffs[handoffIndex].Status == "pending" {
				return &handoffs[handoffIndex], nil
			}
		}
	}
	return nil, nil
}

func contextPlanAction(plan []planpkg.PlannedAction, resourceID string) (planpkg.PlannedAction, bool) {
	for _, action := range plan {
		if action.ResourceID == resourceID && action.Kind != planpkg.ActionDestroy {
			return action, true
		}
	}
	return planpkg.PlannedAction{}, false
}

func renderContextPrompt(result contextmanifest.Result, excludedKind string) string {
	filtered := result
	filtered.Sections = nil
	for _, section := range result.Sections {
		if section.Kind != excludedKind {
			filtered.Sections = append(filtered.Sections, section)
		}
	}
	return contextmanifest.Render(filtered)
}

func contextByteTotals(sections []contextmanifest.Section) (original, selected int) {
	for _, section := range sections {
		original += section.OriginalBytes
		selected += section.SelectedBytes
	}
	return original, selected
}
