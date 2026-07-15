package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// contextCandidateInput is the immutable input shared by context sources.
// Runtime and project status I/O is completed before candidate construction so
// sources only describe available context or an explicit unavailable decision.
type contextCandidateInput struct {
	action         planpkg.PlannedAction
	resource       cuepkg.Resource
	registry       *cuepkg.Registry
	runtime        promptpkg.RuntimeContext
	runtimeErr     error
	systemPrompt   string
	resourcePrompt string
	projectStatus  map[string]any
}

func (s *Spec) loadGenerationRuntimeContext(
	ctx context.Context,
	resource cuepkg.Resource,
	registry *cuepkg.Registry,
	applyID, sessionID string,
	sessionResource *store.SessionResource,
) (promptpkg.RuntimeContext, error) {
	runtimeContext, runtimeErr := s.buildRuntimeContext(resource, registry, applyID)
	if rejectedCandidate, candidateErr := s.store.GetLatestRejectedCandidate(ctx, sessionID, resource.ID); candidateErr == nil {
		runtimeContext.ExistingFiles = make(map[string]string, len(rejectedCandidate.Files))
		for _, file := range rejectedCandidate.Files {
			runtimeContext.ExistingFiles[file.Path] = file.Content
		}
	}
	if sessionResource != nil && sessionResource.LastError != "" {
		runtimeContext.WaveErrors = sessionResource.LastError
	}
	if guidance, noteErr := s.store.GetNote(resource.ID, applyID); noteErr == nil && guidance != "" {
		runtimeContext.UserGuidance = guidance
	}
	return runtimeContext, runtimeErr
}

func (s *Spec) contextCandidates(input contextCandidateInput) []contextmanifest.Candidate {
	var candidates []contextmanifest.Candidate
	candidates = append(candidates, coreContextSource(input)...)
	candidates = append(candidates, acceptanceContextSource(input)...)
	candidates = append(candidates, s.architectureContextSource(input)...)
	candidates = append(candidates, runtimeImplementationContextSource(input)...)
	candidates = append(candidates, failureContextSource(input)...)
	candidates = append(candidates, s.conventionContextSource(input)...)
	candidates = append(candidates, selectorDecisionContextSource(input)...)
	return candidates
}

func coreContextSource(input contextCandidateInput) []contextmanifest.Candidate {
	return []contextmanifest.Candidate{
		{
			Kind: "project_goal", Title: "Project Goal and Completion Context", SourceKind: "project_specification",
			SourceID: input.registry.Project.Name, Content: projectGoalContext(input.action, input.registry, input.projectStatus),
			Priority: contextmanifest.PriorityGoal, Mandatory: true, Truncatable: false,
			InclusionReason: "project intent and target functionality govern implementation decisions",
		},
		{
			Kind: "task", Title: "Exact Planned Task", SourceKind: "plan_operation", SourceID: input.action.OperationID,
			Content: contextJSON(map[string]any{
				"resource": input.action.ResourceID, "operation": input.action.Kind, "category": input.action.Category,
				"reason": input.action.Reason, "expected_behavior": input.action.ExpectedBehavior,
				"expected_evidence": input.action.ExpectedEvidence, "allowed_generated_files": input.action.Files,
				"resource_validations": input.resource.Validations, "project_validations": input.registry.Project.Validations,
			}),
			Priority: contextmanifest.PriorityTask, Mandatory: true,
			InclusionReason: "the agent must know the exact operation, expected outcome, files, and validation commands",
		},
		{
			Kind: "resource_contract", Title: "Resource Contract", SourceKind: "resource_specification", SourceID: input.resource.ID,
			Content: input.resourcePrompt, Priority: contextmanifest.PriorityContract, Mandatory: true,
			InclusionReason: "the resource declaration and architectural contract define the implementation boundary",
		},
		{
			Kind: "system_instructions", Title: "Agent System Instructions", SourceKind: "prompt_template",
			SourceID: "system:" + input.registry.Project.Meta.Language, Content: input.systemPrompt,
			Priority: contextmanifest.PriorityContract, Mandatory: true,
			InclusionReason: "the host requires output, safety, language, and repository-wide implementation instructions",
		},
	}
}

func acceptanceContextSource(input contextCandidateInput) []contextmanifest.Candidate {
	acceptance := targetAcceptanceContext(input.action, input.registry.Project)
	candidate := contextmanifest.Candidate{
		Kind: "acceptance", Title: "Behavioral Acceptance and Evidence", SourceKind: "project_specification",
		SourceID: input.resource.ID + ":acceptance", Priority: contextmanifest.PriorityAcceptance,
	}
	if acceptance == "" {
		candidate.UnavailableReason = "no acceptance scenario is linked to this shared infrastructure resource"
	} else {
		candidate.Content = acceptance
		candidate.Mandatory = true
		candidate.InclusionReason = "observable acceptance behavior must remain visible while implementing its contributing resource"
	}
	return []contextmanifest.Candidate{candidate}
}

func (s *Spec) architectureContextSource(input contextCandidateInput) []contextmanifest.Candidate {
	consumerIDs := directConsumerIDs(input.resource.ID, input.registry)
	candidates := []contextmanifest.Candidate{{
		Kind: "integration_contract", Title: "Integration Responsibility", SourceKind: "resource_graph",
		SourceID: input.resource.ID, Content: contextJSON(map[string]any{
			"contributions": input.resource.Contributions, "dependencies": input.resource.Dependencies,
			"consumers": consumerIDs, "downstream_goal_impact": input.action.Goals,
		}),
		Priority:        contextmanifest.PriorityConsumer,
		InclusionReason: "shows how data, calls, and goal impact cross the resource boundary",
	}}

	for _, dependency := range input.resource.Dependencies {
		target, exists := input.registry.Resources[dependency.TargetID]
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
		consumer := input.registry.Resources[consumerID]
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
	return candidates
}

func runtimeImplementationContextSource(input contextCandidateInput) []contextmanifest.Candidate {
	var candidates []contextmanifest.Candidate
	for dependencyID, content := range input.runtime.DependencyFiles {
		candidates = append(candidates, codeCandidate("dependency_code", "Dependency implementation: "+dependencyID, dependencyID, "", content, contextmanifest.PriorityDependency, "accepted dependency implementation provides concrete types and interfaces"))
	}
	for dependencyID, note := range input.runtime.AgentNotes {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "dependency_note", Title: "Dependency handoff: " + dependencyID, SourceKind: "agent_note",
			SourceID: dependencyID, Content: note, Priority: contextmanifest.PriorityDependency, Truncatable: true,
			InclusionReason: "dependency implementer recorded integration guidance for downstream consumers",
		})
	}
	if input.runtime.DesignContract != "" {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "design_contract", Title: "Bounded-Context Design Contract", SourceKind: "behavioral_design",
			SourceID: input.resource.ContextName, Content: input.runtime.DesignContract,
			Priority: contextmanifest.PriorityContract, Truncatable: false,
			InclusionReason: "committed behavioral design defines cross-resource observable invariants",
		})
	}
	for path, content := range input.runtime.ExistingFiles {
		candidates = append(candidates, codeCandidate("owned_code", "Existing Generated File: "+path, input.resource.ID, path, updateModeContent(path, content, input.runtime.ChangesRequired), contextmanifest.PriorityCode, "current owned implementation enables minimal-diff update and retry"))
	}
	for path, content := range input.runtime.ModuleFiles {
		candidates = append(candidates, codeCandidate("module_configuration", "Module or Build Configuration: "+path, path, path, content, contextmanifest.PriorityCode, "module and build declarations may require additive integration changes"))
	}
	if input.runtime.ModuleTree != "" {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "module_tree", Title: "Current Module Tree", SourceKind: "repository_snapshot", SourceID: "src-tree",
			Content: input.runtime.ModuleTree, Priority: contextmanifest.PriorityBackground, Truncatable: true,
			InclusionReason: "repository layout helps place files consistently without including unrelated contents",
		})
	}
	return candidates
}

func failureContextSource(input contextCandidateInput) []contextmanifest.Candidate {
	var candidates []contextmanifest.Candidate
	if input.runtime.WaveErrors != "" {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "previous_failure", Title: "Previous Errors", SourceKind: "session_failure", SourceID: input.resource.ID,
			Content:  "The previous attempt was rejected. Address this raw failure and do not resubmit the same approach.\n\n" + input.runtime.WaveErrors,
			Priority: contextmanifest.PriorityFailure, Truncatable: true,
			InclusionReason: "raw rejection evidence is required to make a retry corrective rather than blind",
		})
	}
	if input.runtime.UserGuidance != "" {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "triage_guidance", Title: "Guidance", SourceKind: "agent_note", SourceID: input.resource.ID,
			Content: input.runtime.UserGuidance, Priority: contextmanifest.PriorityFailure, Truncatable: true,
			InclusionReason: "recorded triage guidance identifies the corrective action for this attempt",
		})
	}
	return candidates
}

func (s *Spec) conventionContextSource(input contextCandidateInput) []contextmanifest.Candidate {
	var candidates []contextmanifest.Candidate
	if len(input.runtime.Learnings) > 0 {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "active_learnings", Title: "Learnings From Past Runs", SourceKind: "operational_learning",
			SourceID: input.registry.Project.Meta.Language + ":" + input.resource.Kind, Content: "- " + strings.Join(input.runtime.Learnings, "\n- "),
			Priority: contextmanifest.PriorityConvention, Truncatable: true,
			InclusionReason: "accepted patterns from previous runs provide relevant implementation conventions",
		})
	}
	conventions := contextJSON(map[string]any{
		"style": input.registry.Project.Meta.Style, "rules": input.registry.Project.Meta.Rules,
		"avoid": input.registry.Project.Meta.Avoid, "examples": input.registry.Project.Meta.Examples,
		"invariants": input.registry.Project.Invariants, "context_map": input.registry.Project.ContextMap,
	})
	candidates = append(candidates, contextmanifest.Candidate{
		Kind: "repository_conventions", Title: "Repository and Architectural Conventions", SourceKind: "project_specification",
		SourceID: input.registry.Project.Name + ":conventions", Content: conventions,
		Priority: contextmanifest.PriorityConvention, Truncatable: true,
		InclusionReason: "repository-wide conventions and invariants constrain a locally valid implementation",
	})
	candidates = append(candidates, s.referenceCandidates(input.registry.Project.Meta.References)...)
	return candidates
}

func selectorDecisionContextSource(input contextCandidateInput) []contextmanifest.Candidate {
	var candidates []contextmanifest.Candidate
	if input.runtimeErr != nil {
		candidates = append(candidates, contextmanifest.Candidate{
			Kind: "runtime_discovery", Title: "Runtime Context Discovery", SourceKind: "selector_plugin",
			SourceID: "runtime", Priority: contextmanifest.PriorityBackground,
			UnavailableReason: "runtime context discovery failed: " + input.runtimeErr.Error(),
		})
	}
	return append(candidates,
		contextmanifest.Candidate{
			Kind: "call_sites", Title: "Language-Aware Call Sites", SourceKind: "selector_plugin", SourceID: input.resource.ID,
			Priority: contextmanifest.PriorityCode, UnavailableReason: "language-specific call-site discovery is not available for this selector version",
		},
		contextmanifest.Candidate{
			Kind: "recent_diff", Title: "Recent Relevant Diff", SourceKind: "selector_plugin", SourceID: input.resource.ID,
			Priority: contextmanifest.PriorityBackground, UnavailableReason: "git diff discovery is not available through the repository filesystem abstraction",
		},
	)
}

func (s *Spec) contextProjectStatus(ctx context.Context, action planpkg.PlannedAction, registry *cuepkg.Registry) map[string]any {
	status := map[string]any{}
	state, err := s.store.GetProjectIntent(ctx, registry.Project.Name)
	if err != nil || state == nil {
		return status
	}
	status["project_completion"] = state.CompletionStatus
	status["completion_reason"] = state.CompletionReason
	targetedGoals := make(map[string]bool, len(action.Goals))
	for _, goalID := range action.Goals {
		if _, exists := registry.Project.Goals[strings.TrimPrefix(goalID, "goal.")]; exists {
			targetedGoals[goalID] = true
		}
	}
	for _, goal := range state.Goals {
		if targetedGoals[goal.ID] {
			status[goal.ID] = map[string]string{"status": goal.Status, "reason": goal.StatusReason}
		}
	}
	return status
}

func projectGoalContext(action planpkg.PlannedAction, registry *cuepkg.Registry, status map[string]any) string {
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
	var builder strings.Builder
	builder.WriteString("UPDATE MODE: preserve the existing implementation and make the smallest change that satisfies the task.\n\n")
	builder.WriteString("File: " + path + "\n\n```\n" + content + "\n```\n")
	if changes != "" {
		builder.WriteString("\nCHANGES TO MAKE\n\n" + changes)
	}
	return builder.String()
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
		} else if sensitiveContextPath(relative) {
			candidate.UnavailableReason = "reference path may contain secrets and was not captured"
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

func sensitiveContextPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	if strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") {
		return true
	}
	for _, marker := range []string{"credential", "credentials", "secret", "private-key", "private_key"} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return false
}

func contextJSON(value any) string {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
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
