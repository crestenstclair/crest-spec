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
// Selection, persistence, and the dispatched transition are one logical
// operation: a blocked budget is recorded but never dispatches the resource.
func (s *Spec) PrepareContext(ctx context.Context, opts ContextOptions) (*ContextResult, error) {
	if opts.SessionID == "" || opts.ResourceID == "" {
		return nil, fmt.Errorf("session_id and resource_id are required")
	}
	sess, err := s.store.GetSession(opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var plan []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(sess.PlanJSON), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}
	action, ok := contextPlanAction(plan, opts.ResourceID)
	if !ok {
		return nil, fmt.Errorf("resource %s is not an operation in session %s", opts.ResourceID, opts.SessionID)
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
	sessionResource, err := s.store.GetSessionResource(opts.SessionID, opts.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("resource %s is not dispatchable in session %s: %w", opts.ResourceID, opts.SessionID, err)
	}

	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	resource, ok := planResult.Registry.Resources[opts.ResourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", opts.ResourceID)
	}

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
		Manifest: manifest, Dispatch: !selection.Blocked, HandoffID: handoffID,
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
		AttemptID: persisted.Attempt.ID, ContextManifestID: persisted.ID, ContextHash: persisted.ContextHash,
		Role: opts.Role, RecommendedRole: action.RecommendedRole, RolePolicyVersion: rolePolicy.Version,
		ContextPolicy: rolePolicy.ContextPolicy, HandoffID: handoffID,
		SelectorVersion: persisted.SelectorVersion, EstimatorVersion: persisted.EstimatorVersion,
		TemplateHashes: persisted.TemplateHashes, BudgetTokens: persisted.BudgetTokens,
		EstimatedTokens: persisted.EstimatedTokens, Blocked: persisted.Blocked, BlockedReason: persisted.BlockedReason,
		Sections: selection.Sections, SystemPrompt: persisted.SystemPrompt, Prompt: persisted.RenderedPrompt,
		Instructions: instructions, Invariants: invariants,
	}, nil
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
