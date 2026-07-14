package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
	"github.com/crestenstclair/crest-spec/internal/store"
)

const defaultContextRole = "resource_implementer"

// PrepareContext creates an immutable generation attempt and context manifest.
// Selection, persistence, and the dispatched transition are one logical
// operation: a blocked budget is recorded but never dispatches the resource.
func (s *Spec) PrepareContext(ctx context.Context, opts ContextOptions) (*ContextResult, error) {
	if opts.Role == "" {
		opts.Role = defaultContextRole
	}
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
	if _, err := s.store.GetSessionResource(opts.SessionID, opts.ResourceID); err != nil {
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

	sessionResource, _ := s.store.GetSessionResource(opts.SessionID, opts.ResourceID)
	runtimeContext, runtimeErr := s.buildRuntimeContext(resource, planResult.Registry, sess.ApplyID)
	if sessionResource != nil && sessionResource.LastError != "" {
		runtimeContext.WaveErrors = sessionResource.LastError
	}
	if guidance, noteErr := s.store.GetNote(opts.ResourceID, sess.ApplyID); noteErr == nil && guidance != "" {
		runtimeContext.UserGuidance = guidance
	}

	systemPrompt := promptpkg.BuildSystemPrompt(planResult.Registry.Project)
	resourcePrompt := promptpkg.BuildResourcePrompt(resource, planResult.Registry)
	candidates := s.contextCandidates(ctx, action, resource, planResult.Registry, runtimeContext, runtimeErr, systemPrompt, resourcePrompt)
	selection := contextmanifest.Select(candidates, contextmanifest.BudgetForRole(opts.Role, opts.BudgetTokens))
	templateHashes := map[string]string{
		"system_prompt":   contextmanifest.Hash(systemPrompt),
		"resource_prompt": contextmanifest.Hash(resourcePrompt),
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
	persisted, err := s.store.CreateContextManifest(ctx, store.ContextManifestWrite{Manifest: manifest, Dispatch: !selection.Blocked})
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
		SelectorVersion: persisted.SelectorVersion, EstimatorVersion: persisted.EstimatorVersion,
		TemplateHashes: persisted.TemplateHashes, BudgetTokens: persisted.BudgetTokens,
		EstimatedTokens: persisted.EstimatedTokens, Blocked: persisted.Blocked, BlockedReason: persisted.BlockedReason,
		Sections: selection.Sections, SystemPrompt: persisted.SystemPrompt, Prompt: persisted.RenderedPrompt,
		Instructions: instructions, Invariants: invariants,
	}, nil
}

func contextPlanAction(plan []planpkg.PlannedAction, resourceID string) (planpkg.PlannedAction, bool) {
	for _, action := range plan {
		if action.ResourceID == resourceID && action.Kind != planpkg.ActionDestroy {
			return action, true
		}
	}
	return planpkg.PlannedAction{}, false
}

func (s *Spec) contextCandidates(
	ctx context.Context,
	action planpkg.PlannedAction,
	resource cuepkg.Resource,
	registry *cuepkg.Registry,
	runtimeContext promptpkg.RuntimeContext,
	runtimeErr error,
	systemPrompt, resourcePrompt string,
) []contextmanifest.Candidate {
	candidates := []contextmanifest.Candidate{
		{
			Kind: "project_goal", Title: "Project Goal and Completion Context", SourceKind: "project_specification",
			SourceID: registry.Project.Name, Content: s.projectGoalContext(ctx, action, registry),
			Priority: contextmanifest.PriorityGoal, Mandatory: true, Truncatable: false,
			InclusionReason: "project intent and target functionality govern implementation decisions",
		},
		{
			Kind: "task", Title: "Exact Planned Task", SourceKind: "plan_operation", SourceID: action.OperationID,
			Content: contextJSON(map[string]any{
				"resource": action.ResourceID, "operation": action.Kind, "category": action.Category,
				"reason": action.Reason, "expected_behavior": action.ExpectedBehavior,
				"expected_evidence": action.ExpectedEvidence, "allowed_generated_files": action.Files,
				"resource_validations": resource.Validations, "project_validations": registry.Project.Validations,
			}),
			Priority: contextmanifest.PriorityTask, Mandatory: true,
			InclusionReason: "the agent must know the exact operation, expected outcome, files, and validation commands",
		},
		{
			Kind: "resource_contract", Title: "Resource Contract", SourceKind: "resource_specification", SourceID: resource.ID,
			Content: resourcePrompt, Priority: contextmanifest.PriorityContract, Mandatory: true,
			InclusionReason: "the resource declaration and architectural contract define the implementation boundary",
		},
		{
			Kind: "system_instructions", Title: "Agent System Instructions", SourceKind: "prompt_template",
			SourceID: "system:" + registry.Project.Meta.Language, Content: systemPrompt,
			Priority: contextmanifest.PriorityContract, Mandatory: true,
			InclusionReason: "the host requires output, safety, language, and repository-wide implementation instructions",
		},
	}

	acceptance := targetAcceptanceContext(action, registry.Project)
	if acceptance != "" {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "acceptance", Title: "Behavioral Acceptance and Evidence", SourceKind: "project_specification",
			SourceID: resource.ID + ":acceptance", Content: acceptance,
			Priority: contextmanifest.PriorityAcceptance, Mandatory: true,
			InclusionReason: "observable acceptance behavior must remain visible while implementing its contributing resource",
		})
	} else {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "acceptance", Title: "Behavioral Acceptance and Evidence", SourceKind: "project_specification",
			SourceID: resource.ID + ":acceptance", Priority: contextmanifest.PriorityAcceptance,
			UnavailableReason: "no acceptance scenario is linked to this shared infrastructure resource",
		})
	}

	consumerIDs := directConsumerIDs(resource.ID, registry)
	candidates = append(candidates, contextmanifest.Candidate{
		Kind: "integration_contract", Title: "Integration Responsibility", SourceKind: "resource_graph",
		SourceID: resource.ID, Content: contextJSON(map[string]any{
			"contributions": resource.Contributions, "dependencies": resource.Dependencies,
			"consumers": consumerIDs, "downstream_goal_impact": action.Goals,
		}),
		Priority:        contextmanifest.PriorityConsumer,
		InclusionReason: "shows how data, calls, and goal impact cross the resource boundary",
	})

	for _, dependency := range resource.Dependencies {
		target, exists := registry.Resources[dependency.TargetID]
		if !exists {
			continue
		}
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "dependency_contract", Title: "Dependency Contract: " + dependency.TargetID,
			SourceKind: "resource_specification", SourceID: dependency.TargetID,
			Content:  contextJSON(map[string]any{"relationship": dependency.Kind, "kind": target.Kind, "declaration": target.Declaration}),
			Priority: contextmanifest.PriorityDependency, Truncatable: true,
			InclusionReason: "direct dependency contract constrains calls made by this resource",
		})
	}
	for _, consumerID := range consumerIDs {
		consumer := registry.Resources[consumerID]
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "consumer_contract", Title: "Consumer Contract: " + consumerID,
			SourceKind: "resource_specification", SourceID: consumerID,
			Content:  contextJSON(map[string]any{"kind": consumer.Kind, "declaration": consumer.Declaration, "dependencies": consumer.Dependencies}),
			Priority: contextmanifest.PriorityConsumer, Truncatable: true,
			InclusionReason: "direct consumer contract shows how the generated resource will be called",
		})
		for _, file := range generatedFileContents(s, consumerID) {
			candidates = append(candidates, codeCandidate("consumer_code", "Consumer implementation: "+file.path, consumerID, file.path, file.content, contextmanifest.PriorityConsumer, "existing consumer code exposes concrete call and data-flow expectations"))
		}
	}

	for dependencyID, content := range runtimeContext.DependencyFiles {
		candidates = append(candidates, codeCandidate("dependency_code", "Dependency implementation: "+dependencyID, dependencyID, "", content, contextmanifest.PriorityDependency, "accepted dependency implementation provides concrete types and interfaces"))
	}
	for dependencyID, note := range runtimeContext.AgentNotes {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "dependency_note", Title: "Dependency handoff: " + dependencyID, SourceKind: "agent_note",
			SourceID: dependencyID, Content: note, Priority: contextmanifest.PriorityDependency, Truncatable: true,
			InclusionReason: "dependency implementer recorded integration guidance for downstream consumers",
		})
	}
	if runtimeContext.DesignContract != "" {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "design_contract", Title: "Bounded-Context Design Contract", SourceKind: "behavioral_design",
			SourceID: resource.ContextName, Content: runtimeContext.DesignContract,
			Priority: contextmanifest.PriorityContract, Truncatable: false,
			InclusionReason: "committed behavioral design defines cross-resource observable invariants",
		})
	}
	for path, content := range runtimeContext.ExistingFiles {
		candidates = append(candidates, codeCandidate("owned_code", "Existing Generated File: "+path, resource.ID, path, updateModeContent(path, content, runtimeContext.ChangesRequired), contextmanifest.PriorityCode, "current owned implementation enables minimal-diff update and retry"))
	}
	for path, content := range runtimeContext.ModuleFiles {
		candidates = append(candidates, codeCandidate("module_configuration", "Module or Build Configuration: "+path, path, path, content, contextmanifest.PriorityCode, "module and build declarations may require additive integration changes"))
	}
	if runtimeContext.ModuleTree != "" {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "module_tree", Title: "Current Module Tree", SourceKind: "repository_snapshot", SourceID: "src-tree",
			Content: runtimeContext.ModuleTree, Priority: contextmanifest.PriorityBackground, Truncatable: true,
			InclusionReason: "repository layout helps place files consistently without including unrelated contents",
		})
	}
	if runtimeContext.WaveErrors != "" {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "previous_failure", Title: "Previous Errors", SourceKind: "session_failure", SourceID: resource.ID,
			Content:  "The previous attempt was rejected. Address this raw failure and do not resubmit the same approach.\n\n" + runtimeContext.WaveErrors,
			Priority: contextmanifest.PriorityFailure, Truncatable: true,
			InclusionReason: "raw rejection evidence is required to make a retry corrective rather than blind",
		})
	}
	if runtimeContext.UserGuidance != "" {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "triage_guidance", Title: "Guidance", SourceKind: "agent_note", SourceID: resource.ID,
			Content: runtimeContext.UserGuidance, Priority: contextmanifest.PriorityFailure, Truncatable: true,
			InclusionReason: "recorded triage guidance identifies the corrective action for this attempt",
		})
	}
	if len(runtimeContext.Learnings) > 0 {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "active_learnings", Title: "Learnings From Past Runs", SourceKind: "operational_learning",
			SourceID: registry.Project.Meta.Language + ":" + resource.Kind, Content: "- " + strings.Join(runtimeContext.Learnings, "\n- "),
			Priority: contextmanifest.PriorityConvention, Truncatable: true,
			InclusionReason: "accepted patterns from previous runs provide relevant implementation conventions",
		})
	}
	conventions := contextJSON(map[string]any{
		"style": registry.Project.Meta.Style, "rules": registry.Project.Meta.Rules,
		"avoid": registry.Project.Meta.Avoid, "examples": registry.Project.Meta.Examples,
		"invariants": registry.Project.Invariants, "context_map": registry.Project.ContextMap,
	})
	candidates = append(candidates, contextmanifest.Candidate{
		Kind: "repository_conventions", Title: "Repository and Architectural Conventions", SourceKind: "project_specification",
		SourceID: registry.Project.Name + ":conventions", Content: conventions,
		Priority: contextmanifest.PriorityConvention, Truncatable: true,
		InclusionReason: "repository-wide conventions and invariants constrain a locally valid implementation",
	})
	candidates = append(candidates, s.referenceCandidates(registry.Project.Meta.References)...)

	if runtimeErr != nil {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "runtime_discovery", Title: "Runtime Context Discovery", SourceKind: "selector_plugin",
			SourceID: "runtime", Priority: contextmanifest.PriorityBackground,
			UnavailableReason: "runtime context discovery failed: " + runtimeErr.Error(),
		})
	}
	candidates = append(candidates,
		contextmanifest.Candidate{
			Kind: "call_sites", Title: "Language-Aware Call Sites", SourceKind: "selector_plugin", SourceID: resource.ID,
			Priority: contextmanifest.PriorityCode, UnavailableReason: "language-specific call-site discovery is not available for this selector version",
		},
		contextmanifest.Candidate{
			Kind: "recent_diff", Title: "Recent Relevant Diff", SourceKind: "selector_plugin", SourceID: resource.ID,
			Priority: contextmanifest.PriorityBackground, UnavailableReason: "git diff discovery is not available through the repository filesystem abstraction",
		},
	)
	return candidates
}

func (s *Spec) projectGoalContext(ctx context.Context, action planpkg.PlannedAction, registry *cuepkg.Registry) string {
	project := registry.Project
	goals := make(map[string]cuepkg.Goal)
	actors := make(map[string]cuepkg.Actor)
	capabilities := make(map[string]cuepkg.Capability)
	requirements := make(map[string]cuepkg.Requirement)
	for _, goalID := range action.Goals {
		name := strings.TrimPrefix(goalID, "goal.")
		if goal, ok := project.Goals[name]; ok {
			goals[goalID] = goal
			for _, actorID := range goal.Actors {
				if actor, exists := project.Actors[strings.TrimPrefix(actorID, "actor.")]; exists {
					actors[actorID] = actor
				}
			}
		}
	}
	for _, capabilityID := range action.Capabilities {
		if capability, ok := project.Capabilities[strings.TrimPrefix(capabilityID, "capability.")]; ok {
			capabilities[capabilityID] = capability
		}
	}
	for name, requirement := range project.Requirements {
		if intersects(requirement.Goals, action.Goals) || intersects(requirement.Capabilities, action.Capabilities) {
			requirements["requirement."+name] = requirement
		}
	}
	status := map[string]any{}
	if state, err := s.store.GetProjectIntent(ctx, project.Name); err == nil && state != nil {
		status["project_completion"] = state.CompletionStatus
		status["completion_reason"] = state.CompletionReason
		for _, goal := range state.Goals {
			if _, targeted := goals[goal.ID]; targeted {
				status[goal.ID] = map[string]string{"status": goal.Status, "reason": goal.StatusReason}
			}
		}
	}
	return contextJSON(map[string]any{
		"mission": project.Mission, "target_actors": actors, "target_goals": goals,
		"target_capabilities": capabilities, "relevant_requirements": requirements,
		"completion_policy": project.Completion, "explicit_non_goals": project.NonGoals,
		"current_status": status, "current_gap": action.Reason,
		"resource_contributions": action.Contributions,
	})
}

func targetAcceptanceContext(action planpkg.PlannedAction, project *cuepkg.Project) string {
	result := make(map[string]any)
	for _, capabilityID := range action.Capabilities {
		capability, exists := project.Capabilities[strings.TrimPrefix(capabilityID, "capability.")]
		if !exists || len(capability.Acceptance) == 0 {
			continue
		}
		result[capabilityID] = map[string]any{
			"description": capability.Description, "acceptance": capability.Acceptance,
		}
	}
	if len(result) == 0 {
		return ""
	}
	return contextJSON(result)
}

func directConsumerIDs(resourceID string, registry *cuepkg.Registry) []string {
	var consumers []string
	for candidateID, candidate := range registry.Resources {
		for _, dependency := range candidate.Dependencies {
			if dependency.TargetID == resourceID {
				consumers = append(consumers, candidateID)
				break
			}
		}
	}
	sort.Strings(consumers)
	return consumers
}

type contextFile struct {
	path    string
	content string
}

func generatedFileContents(s *Spec, resourceID string) []contextFile {
	files, err := s.store.GetGeneratedFiles(resourceID)
	if err != nil {
		return nil
	}
	result := make([]contextFile, 0, len(files))
	for _, file := range files {
		content, readErr := s.fs.ReadFile(file.Path)
		if readErr == nil {
			result = append(result, contextFile{path: file.Path, content: string(content)})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].path < result[j].path })
	return result
}

func codeCandidate(kind, title, sourceID, path, content string, priority contextmanifest.Priority, reason string) contextmanifest.Candidate {
	return contextmanifest.Candidate{
		Kind: kind, Title: title, SourceKind: "repository_file", SourceID: sourceID,
		SourcePath: path, Content: content, Priority: priority, Truncatable: true, InclusionReason: reason,
	}
}

func updateModeContent(path, content, changes string) string {
	var b strings.Builder
	b.WriteString("UPDATE MODE: preserve the existing implementation and make the smallest change that satisfies the task.\n\n")
	b.WriteString("File: " + path + "\n\n```\n" + content + "\n```\n")
	if changes != "" {
		b.WriteString("\nCHANGES TO MAKE\n\n" + changes)
	}
	return b.String()
}

func (s *Spec) referenceCandidates(references []string) []contextmanifest.Candidate {
	root := filepath.Clean(filepath.Dir(s.cfg.SpecDir))
	result := make([]contextmanifest.Candidate, 0, len(references))
	for _, reference := range references {
		candidate := contextmanifest.Candidate{
			Kind: "repository_instruction", Title: "Repository Instruction: " + reference,
			SourceKind: "repository_file", SourceID: reference, SourcePath: reference,
			Priority: contextmanifest.PriorityConvention, Truncatable: true,
			InclusionReason: "the project specification explicitly marks this repository instruction as relevant",
		}
		path := reference
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path = filepath.Clean(path)
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			candidate.UnavailableReason = "reference path is outside the project root and was not captured"
		} else if content, readErr := s.fs.ReadFile(path); readErr != nil {
			candidate.UnavailableReason = "referenced repository instruction could not be read"
		} else {
			candidate.SourcePath = relative
			candidate.Content = string(content)
		}
		result = append(result, candidate)
	}
	return result
}

func contextJSON(value any) string {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
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

func intersects(left, right []string) bool {
	set := make(map[string]bool, len(left))
	for _, value := range left {
		set[value] = true
	}
	for _, value := range right {
		if set[value] {
			return true
		}
	}
	return false
}
