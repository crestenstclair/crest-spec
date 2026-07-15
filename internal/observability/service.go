package observability

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type Service struct {
	store Repository
}

type Repository interface {
	GetProjectIntent(ctx context.Context, projectName string) (*store.ProjectIntentState, error)
	ListCompletionBlockers(ctx context.Context, projectName string) ([]store.CompletionBlocker, error)
	ListProjectStatusHistory(ctx context.Context, projectName string) ([]store.StatusTransition, error)
	ListResourceContributions(ctx context.Context, projectName string) ([]store.PersistedContribution, error)
	ListGoalStatusHistory(ctx context.Context, goalID string) ([]store.StatusTransition, error)
	GetVerificationCompletionFacts(ctx context.Context, projectName string) (*store.VerificationCompletionFacts, error)
	ListResources() ([]store.Resource, error)
	ListContributionsByResource(ctx context.Context, resourceID string) ([]store.PersistedContribution, error)
	GetResource(id string) (*store.Resource, error)
	GetDependencies(sourceID string) ([]store.Dependency, error)
	GetGeneratedFiles(resourceID string) ([]store.GeneratedFile, error)
	ListGenerationAttemptsByResource(ctx context.Context, resourceID string, limit int) ([]store.GenerationAttempt, error)
	GetContextManifestByAttempt(ctx context.Context, attemptID string) (*store.ContextManifest, error)
	GetExecutionManifestByAttempt(ctx context.Context, attemptID string) (*store.ExecutionManifest, error)
	ListValidationRuns(ctx context.Context, limit int) ([]store.ValidationRun, error)
	GetActiveSession() (*store.Session, error)
	ListSessionResources(sessionID string) ([]store.SessionResource, error)
	ListFailureClassifications(ctx context.Context, limit int) ([]store.FailureClassification, error)
	ListFailureClassificationsByAttempt(ctx context.Context, attemptID string) ([]store.FailureClassification, error)
	GetFailureClassification(ctx context.Context, id string) (*store.FailureClassification, error)
	GetCandidateSetByAttempt(ctx context.Context, attemptID string) (*store.CandidateSet, error)
}

func NewService(st Repository) *Service {
	return &Service{store: st}
}

func (s *Service) Project(ctx context.Context, projectName string, completionEnforced bool) (*ProjectOverview, error) {
	intent, err := s.store.GetProjectIntent(ctx, projectName)
	if err != nil {
		return nil, err
	}
	blockers, err := s.store.ListCompletionBlockers(ctx, projectName)
	if err != nil {
		return nil, err
	}
	history, err := s.store.ListProjectStatusHistory(ctx, projectName)
	if err != nil {
		return nil, err
	}
	contributions, err := s.store.ListResourceContributions(ctx, projectName)
	if err != nil {
		return nil, err
	}
	goals, capabilities := projectRelationships(intent, contributions)
	result := &ProjectOverview{
		Version: APIVersion, CompletionEnforced: completionEnforced,
		Project: Project{
			Name: intent.ProjectName, Mission: intent.Mission, SpecHash: intent.SpecHash,
			CompletionStatus: intent.CompletionStatus, CompletionReason: intent.CompletionReason,
			Actors: actors(intent.Actors), Goals: goals, Capabilities: capabilities,
			RequiredGoals: append([]string(nil), intent.RequiredGoals...), NonGoals: nonGoals(intent.NonGoals),
			State: RecordState{}, Links: []Link{
				link("self", "project", intent.ProjectName, "/api/v1/project"),
				link("goals", "goal", "", "/api/v1/goals"),
				link("resources", "resource", "", "/api/v1/resources"),
				link("plan", "plan", "current", "/api/v1/plan"),
			},
		},
		Blockers: blockersView(blockers), RecentHistory: transitions(history, 20),
		Links: []Link{
			link("goals", "goal", "", "/api/v1/goals"),
			link("resources", "resource", "", "/api/v1/resources"),
			link("evaluations", "evaluation_run", "", "/api/v1/evaluations/runs"),
		},
	}
	result.Status = explainProjectStatus(intent, goals, result.Blockers)
	return result, nil
}

func (s *Service) Capabilities(ctx context.Context, projectName string, request PageRequest) (*Page[CapabilitySummary], error) {
	request, err := NormalizePageRequest(request)
	if err != nil {
		return nil, err
	}
	intent, err := s.store.GetProjectIntent(ctx, projectName)
	if err != nil {
		return nil, err
	}
	contributions, err := s.store.ListResourceContributions(ctx, projectName)
	if err != nil {
		return nil, err
	}
	_, capabilities := projectRelationships(intent, contributions)
	filtered := make([]CapabilitySummary, 0, len(capabilities))
	for _, capability := range capabilities {
		if request.GoalID != "" && !contains(capability.Goals, request.GoalID) {
			continue
		}
		if request.Query != "" && !containsFold(capability.ID+" "+capability.Description, request.Query) {
			continue
		}
		filtered = append(filtered, capability)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID < filtered[j].ID })
	if request.Cursor != "" {
		decoded, _ := decodeCursor(request.Cursor)
		start := 0
		for start < len(filtered) && filtered[start].ID <= decoded.ID {
			start++
		}
		filtered = filtered[start:]
	}
	page, more := take(filtered, request.Limit)
	result := &Page[CapabilitySummary]{Version: APIVersion, Items: page, Page: PageInfo{Limit: request.Limit, HasMore: more}}
	if more && len(page) > 0 {
		result.Page.NextCursor = encodeCursor(time.Unix(0, 0), page[len(page)-1].ID)
	}
	return result, nil
}

func (s *Service) Capability(ctx context.Context, projectName, capabilityID string) (*CapabilityDetail, error) {
	intent, err := s.store.GetProjectIntent(ctx, projectName)
	if err != nil {
		return nil, err
	}
	contributions, err := s.store.ListResourceContributions(ctx, projectName)
	if err != nil {
		return nil, err
	}
	goals, capabilities := projectRelationships(intent, contributions)
	var capability *CapabilitySummary
	for index := range capabilities {
		if capabilities[index].ID == capabilityID {
			capability = &capabilities[index]
			break
		}
	}
	if capability == nil {
		return nil, cserrors.ErrNotFound
	}
	result := &CapabilityDetail{Version: APIVersion, Capability: *capability}
	for _, goal := range goals {
		if contains(capability.Goals, goal.ID) {
			result.Goals = append(result.Goals, goal)
		}
	}
	for _, requirement := range intent.Requirements {
		if contains(requirement.Capabilities, capabilityID) {
			result.Requirements = append(result.Requirements, Requirement{
				ID: requirement.ID, Kind: requirement.Kind, Description: requirement.Description,
				Goals: append([]string(nil), requirement.Goals...), Capabilities: append([]string(nil), requirement.Capabilities...),
			})
		}
	}
	for _, scenario := range intent.Acceptance {
		if scenario.CapabilityID != capabilityID {
			continue
		}
		view := AcceptanceScenario{ID: scenario.ID, CapabilityID: scenario.CapabilityID, ActorID: scenario.ActorID, Description: scenario.Description, Evidence: append([]string(nil), scenario.Evidence...)}
		for _, step := range scenario.Steps {
			view.Steps = append(view.Steps, AcceptanceStep{Action: step.Action, Observes: step.Observes})
		}
		result.Acceptance = append(result.Acceptance, view)
	}
	resources, err := s.Resources(ctx, projectName, PageRequest{Limit: MaximumPageLimit, CapabilityID: capabilityID})
	if err != nil {
		return nil, err
	}
	result.Resources = resources.Items
	result.Status = explainCapabilityStatus(*capability, result.Goals, result.Resources)
	result.Links = []Link{
		link("self", "capability", capabilityID, "/api/v1/capabilities/"+capabilityID),
		link("project", "project", projectName, "/api/v1/project"),
		link("resources", "resource", "", "/api/v1/resources?capability="+capabilityID),
	}
	return result, nil
}

func (s *Service) Goals(ctx context.Context, projectName string, request PageRequest) (*Page[GoalSummary], error) {
	request, err := NormalizePageRequest(request)
	if err != nil {
		return nil, err
	}
	intent, err := s.store.GetProjectIntent(ctx, projectName)
	if err != nil {
		return nil, err
	}
	contributions, err := s.store.ListResourceContributions(ctx, projectName)
	if err != nil {
		return nil, err
	}
	goals, _ := projectRelationships(intent, contributions)
	filtered := make([]GoalSummary, 0, len(goals))
	for _, goal := range goals {
		if request.Status != "" && goal.Status != request.Status {
			continue
		}
		if request.CapabilityID != "" && !contains(goal.Capabilities, request.CapabilityID) {
			continue
		}
		if request.Query != "" && !containsFold(goal.ID+" "+goal.Description, request.Query) {
			continue
		}
		filtered = append(filtered, goal)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID < filtered[j].ID })
	if request.Cursor != "" {
		decoded, _ := decodeCursor(request.Cursor)
		start := 0
		for start < len(filtered) && filtered[start].ID <= decoded.ID {
			start++
		}
		filtered = filtered[start:]
	}
	page, more := take(filtered, request.Limit)
	result := &Page[GoalSummary]{Version: APIVersion, Items: page, Page: PageInfo{Limit: request.Limit, HasMore: more}}
	if more && len(page) > 0 {
		result.Page.NextCursor = encodeCursor(time.Unix(0, 0), page[len(page)-1].ID)
	}
	return result, nil
}

func (s *Service) Goal(ctx context.Context, projectName, goalID string) (*GoalDetail, error) {
	intent, err := s.store.GetProjectIntent(ctx, projectName)
	if err != nil {
		return nil, err
	}
	contributions, err := s.store.ListResourceContributions(ctx, projectName)
	if err != nil {
		return nil, err
	}
	goals, _ := projectRelationships(intent, contributions)
	var goal *GoalSummary
	for index := range goals {
		if goals[index].ID == goalID {
			goal = &goals[index]
			break
		}
	}
	if goal == nil {
		return nil, cserrors.ErrNotFound
	}
	result := &GoalDetail{Version: APIVersion, Goal: *goal}
	actorIDs := make(map[string]bool)
	for _, persisted := range intent.Goals {
		if persisted.ID == goalID {
			for _, id := range persisted.Actors {
				actorIDs[id] = true
			}
		}
	}
	for _, actor := range intent.Actors {
		if actorIDs[actor.ID] {
			result.Actors = append(result.Actors, Actor{ID: actor.ID, Description: actor.Description})
		}
	}
	for _, requirement := range intent.Requirements {
		if contains(requirement.Goals, goalID) || intersects(requirement.Capabilities, goal.Capabilities) {
			result.Requirements = append(result.Requirements, Requirement{
				ID: requirement.ID, Kind: requirement.Kind, Description: requirement.Description,
				Goals: append([]string(nil), requirement.Goals...), Capabilities: append([]string(nil), requirement.Capabilities...),
			})
		}
	}
	for _, scenario := range intent.Acceptance {
		if !contains(goal.Capabilities, scenario.CapabilityID) {
			continue
		}
		view := AcceptanceScenario{
			ID: scenario.ID, CapabilityID: scenario.CapabilityID, ActorID: scenario.ActorID,
			Description: scenario.Description, Evidence: append([]string(nil), scenario.Evidence...),
		}
		for _, step := range scenario.Steps {
			view.Steps = append(view.Steps, AcceptanceStep{Action: step.Action, Observes: step.Observes})
		}
		result.Acceptance = append(result.Acceptance, view)
	}
	facts, err := s.store.GetVerificationCompletionFacts(ctx, projectName)
	if err != nil {
		if !errors.Is(err, cserrors.ErrNotFound) {
			return nil, fmt.Errorf("load verification completion facts for project %q: %w", projectName, err)
		}
		facts = nil
	}
	for _, declaration := range intent.Evidence {
		if !evidenceUsedByScenarios(declaration.ID, result.Acceptance) {
			continue
		}
		view := EvidenceStatus{ID: declaration.ID, Kind: declaration.Kind, Description: declaration.Description, Currency: "missing"}
		if facts != nil {
			for _, evidence := range facts.CurrentEvidence[declaration.ID] {
				view.Currency = evidence.Currency
				view.Classification = evidence.Classification
				view.SourceTreeHash = evidence.SourceTreeHash
				view.RunID = evidence.RunID
				view.Links = append(view.Links, link("validation", "validation", evidence.RunID, "/api/v1/verifications/"+evidence.RunID))
				break
			}
		}
		result.Evidence = append(result.Evidence, view)
	}
	allBlockers, err := s.store.ListCompletionBlockers(ctx, projectName)
	if err != nil {
		return nil, err
	}
	for _, blocker := range allBlockers {
		if blocker.GoalID == "" || blocker.GoalID == goalID {
			result.Blockers = append(result.Blockers, blockerView(blocker))
		}
	}
	history, err := s.store.ListGoalStatusHistory(ctx, goalID)
	if err != nil {
		return nil, err
	}
	result.History = transitions(history, 100)
	result.Status = explainGoalStatus(*goal, result.Evidence, result.Blockers)
	result.Links = []Link{
		link("self", "goal", goalID, "/api/v1/goals/"+goalID),
		link("project", "project", projectName, "/api/v1/project"),
		link("resources", "resource", "", "/api/v1/resources?goal="+goalID),
	}
	return result, nil
}

func (s *Service) Resources(ctx context.Context, projectName string, request PageRequest) (*Page[ResourceSummary], error) {
	request, err := NormalizePageRequest(request)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListResources()
	if err != nil {
		return nil, err
	}
	contributions, err := s.store.ListResourceContributions(ctx, projectName)
	if err != nil {
		return nil, err
	}
	intent, err := s.store.GetProjectIntent(ctx, projectName)
	if err != nil {
		return nil, err
	}
	capabilityGoals := make(map[string][]string)
	for _, capability := range intent.Capabilities {
		capabilityGoals[capability.ID] = capability.Goals
	}
	byResource := make(map[string][]store.PersistedContribution)
	for _, contribution := range contributions {
		byResource[contribution.ResourceID] = append(byResource[contribution.ResourceID], contribution)
	}
	views := make([]ResourceSummary, 0, len(rows))
	for _, row := range rows {
		if request.Kind != "" && row.Kind != request.Kind {
			continue
		}
		if request.Query != "" && !containsFold(row.ID+" "+row.Kind+" "+row.ContextName, request.Query) {
			continue
		}
		capabilities, goals := []string{}, []string{}
		for _, contribution := range byResource[row.ID] {
			capabilities = append(capabilities, contribution.CapabilityID)
			goals = append(goals, capabilityGoals[contribution.CapabilityID]...)
		}
		capabilities, goals = unique(capabilities), unique(goals)
		if request.CapabilityID != "" && !contains(capabilities, request.CapabilityID) {
			continue
		}
		if request.GoalID != "" && !contains(goals, request.GoalID) {
			continue
		}
		view := resourceSummary(row, capabilities, goals)
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].SettledAt.Equal(views[j].SettledAt) {
			return views[i].ID > views[j].ID
		}
		return views[i].SettledAt.After(views[j].SettledAt)
	})
	if request.Cursor != "" {
		decoded, _ := decodeCursor(request.Cursor)
		filtered := views[:0]
		for _, view := range views {
			if afterCursor(view.SettledAt, view.ID, decoded) {
				filtered = append(filtered, view)
			}
		}
		views = filtered
	}
	page, more := take(views, request.Limit)
	result := &Page[ResourceSummary]{Version: APIVersion, Items: page, Page: PageInfo{Limit: request.Limit, HasMore: more}}
	if more && len(page) > 0 {
		last := page[len(page)-1]
		result.Page.NextCursor = encodeCursor(last.SettledAt, last.ID)
	}
	return result, nil
}

func (s *Service) Resource(ctx context.Context, projectName, resourceID string) (*ResourceDetail, error) {
	row, err := s.store.GetResource(resourceID)
	if err != nil {
		return nil, err
	}
	contributions, err := s.store.ListContributionsByResource(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	intent, err := s.store.GetProjectIntent(ctx, projectName)
	if err != nil {
		return nil, err
	}
	capabilityGoals := make(map[string][]string)
	for _, capability := range intent.Capabilities {
		capabilityGoals[capability.ID] = capability.Goals
	}
	capabilities, goals := []string{}, []string{}
	result := &ResourceDetail{Version: APIVersion}
	for _, contribution := range contributions {
		capabilities = append(capabilities, contribution.CapabilityID)
		goals = append(goals, capabilityGoals[contribution.CapabilityID]...)
		result.Contributions = append(result.Contributions, Contribution{
			CapabilityID: contribution.CapabilityID, Description: contribution.Description,
			Links: []Link{link("capability", "capability", contribution.CapabilityID, "/api/v1/capabilities/"+contribution.CapabilityID)},
		})
	}
	result.Resource = resourceSummary(*row, unique(capabilities), unique(goals))
	dependencies, err := s.store.GetDependencies(resourceID)
	if err != nil {
		return nil, err
	}
	for _, dependency := range dependencies {
		result.Dependencies = append(result.Dependencies, relationship(dependency.TargetID, dependency.Kind))
	}
	allResources, err := s.store.ListResources()
	if err != nil {
		return nil, err
	}
	for _, candidate := range allResources {
		candidateDependencies, getErr := s.store.GetDependencies(candidate.ID)
		if getErr != nil {
			return nil, getErr
		}
		for _, dependency := range candidateDependencies {
			if dependency.TargetID == resourceID {
				result.Consumers = append(result.Consumers, relationship(candidate.ID, dependency.Kind))
			}
		}
	}
	files, err := s.store.GetGeneratedFiles(resourceID)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		result.Files = append(result.Files, FileRef{
			Path: file.Path, ContentHash: file.ContentHash, PromptHash: file.PromptHash,
			Model: file.Model, CreatedAt: file.CreatedAt,
		})
	}
	attempts, err := s.store.ListGenerationAttemptsByResource(ctx, resourceID, 100)
	if err != nil {
		return nil, err
	}
	for _, attempt := range attempts {
		ref := AttemptRef{
			ID: attempt.ID, RetryNumber: attempt.RetryNumber, Role: attempt.Role,
			Status: attempt.Status, CreatedAt: attempt.CreatedAt,
			Links: []Link{link("attempt", "attempt", attempt.ID, "/api/v1/attempts/"+attempt.ID)},
		}
		manifest, getErr := s.store.GetContextManifestByAttempt(ctx, attempt.ID)
		if getErr != nil && !errors.Is(getErr, cserrors.ErrNotFound) {
			return nil, fmt.Errorf("load context manifest for attempt %q: %w", attempt.ID, getErr)
		}
		if getErr == nil {
			ref.ContextManifestID = manifest.ID
			ref.Links = append(ref.Links, link("context", "context", manifest.ID, "/api/v1/contexts/"+manifest.ID))
		}
		execution, getErr := s.store.GetExecutionManifestByAttempt(ctx, attempt.ID)
		if getErr != nil && !errors.Is(getErr, cserrors.ErrNotFound) {
			return nil, fmt.Errorf("load execution manifest for attempt %q: %w", attempt.ID, getErr)
		}
		if getErr == nil {
			ref.ExecutionID = execution.ID
			ref.Links = append(ref.Links, link("execution", "execution", execution.ID, "/api/v1/executions/"+execution.ID))
		}
		result.Attempts = append(result.Attempts, ref)
	}
	runs, err := s.store.ListValidationRuns(ctx, 1000)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if !targets(run.Targets, "resource", resourceID) {
			continue
		}
		currency := "missing"
		for _, evidence := range run.Evidence {
			if evidence.Currency == "current" {
				currency = "current"
				break
			}
			currency = evidence.Currency
		}
		result.Validations = append(result.Validations, ValidationRef{
			ID: run.ID, DefinitionID: run.DefinitionID, Classification: run.Classification,
			SourceTreeHash: run.SourceTreeHash, Currency: currency, CreatedAt: run.CreatedAt,
			Links: []Link{link("validation", "validation", run.ID, "/api/v1/verifications/"+run.ID)},
		})
	}
	result.Status = explainResourceStatus(result)
	result.Links = append(result.Resource.Links,
		link("impact", "impact", resourceID, "/api/v1/resources/"+resourceID+"/impact"),
		link("contexts", "context", "", "/api/v1/contexts?resource_id="+resourceID),
		link("executions", "execution", "", "/api/v1/executions?resource_id="+resourceID),
	)
	return result, nil
}

func projectRelationships(intent *store.ProjectIntentState, contributions []store.PersistedContribution) ([]GoalSummary, []CapabilitySummary) {
	resourcesByCapability := make(map[string][]string)
	for _, contribution := range contributions {
		resourcesByCapability[contribution.CapabilityID] = append(resourcesByCapability[contribution.CapabilityID], contribution.ResourceID)
	}
	capabilitiesByGoal := make(map[string][]string)
	capabilities := make([]CapabilitySummary, 0, len(intent.Capabilities))
	for _, capability := range intent.Capabilities {
		for _, goalID := range capability.Goals {
			capabilitiesByGoal[goalID] = append(capabilitiesByGoal[goalID], capability.ID)
		}
		capabilities = append(capabilities, CapabilitySummary{
			ID: capability.ID, Description: capability.Description, Goals: append([]string(nil), capability.Goals...),
			Resources: unique(resourcesByCapability[capability.ID]),
			Links:     []Link{link("self", "capability", capability.ID, "/api/v1/capabilities/"+capability.ID)},
		})
	}
	goals := make([]GoalSummary, 0, len(intent.Goals))
	for _, persisted := range intent.Goals {
		capabilityIDs := unique(capabilitiesByGoal[persisted.ID])
		resources := []string{}
		for _, capabilityID := range capabilityIDs {
			resources = append(resources, resourcesByCapability[capabilityID]...)
		}
		goals = append(goals, GoalSummary{
			ID: persisted.ID, Description: persisted.Description, Priority: persisted.Priority,
			Status: persisted.Status, StatusReason: persisted.StatusReason,
			DependsOn: append([]string(nil), persisted.DependsOn...), Capabilities: capabilityIDs,
			Resources: unique(resources), Links: []Link{
				link("self", "goal", persisted.ID, "/api/v1/goals/"+persisted.ID),
				link("resources", "resource", "", "/api/v1/resources?goal="+persisted.ID),
			},
		})
	}
	return goals, capabilities
}

func resourceSummary(row store.Resource, capabilities, goals []string) ResourceSummary {
	state := RecordState{}
	if row.DeclarationHash == "" || row.EffectiveHash == "" {
		state.Legacy = true
		state.Reason = "resource predates complete declaration and effective-hash provenance"
	}
	return ResourceSummary{
		ID: row.ID, Kind: row.Kind, ContextName: row.ContextName,
		DeclarationHash: row.DeclarationHash, EffectiveHash: row.EffectiveHash,
		Model: row.Model, SettledAt: row.SettledAt, Goals: goals, Capabilities: capabilities,
		State: state, Links: []Link{
			link("self", "resource", row.ID, "/api/v1/resources/"+row.ID),
			link("impact", "impact", row.ID, "/api/v1/resources/"+row.ID+"/impact"),
		},
	}
}

func explainProjectStatus(intent *store.ProjectIntentState, goals []GoalSummary, blockers []Blocker) StatusExplanation {
	result := StatusExplanation{State: intent.CompletionStatus, Reason: intent.CompletionReason}
	required := make(map[string]bool)
	for _, id := range intent.RequiredGoals {
		required[id] = true
	}
	for _, goal := range goals {
		if !required[goal.ID] || goal.Status == "complete" {
			continue
		}
		result.Missing = append(result.Missing, goal.ID)
		if goal.Status == "regressed" {
			result.Regressions = append(result.Regressions, goal.ID)
		}
	}
	for _, blocker := range blockers {
		result.Blockers = append(result.Blockers, blocker.Reason)
	}
	if len(result.Blockers) > 0 {
		result.RecommendedNext = append(result.RecommendedNext, "Resolve the current completion blocker before retrying generation")
	} else if len(result.Missing) > 0 {
		result.RecommendedNext = append(result.RecommendedNext, "Advance required goal "+result.Missing[0])
	} else if result.State == "complete" {
		result.RecommendedNext = append(result.RecommendedNext, "Monitor current evidence and re-run affected regression checks after changes")
	}
	return result
}

func explainGoalStatus(goal GoalSummary, evidence []EvidenceStatus, blockers []Blocker) StatusExplanation {
	result := StatusExplanation{State: goal.Status, Reason: goal.StatusReason}
	for _, item := range evidence {
		if item.Currency != "current" || item.Classification != "passed" {
			result.Missing = append(result.Missing, item.ID)
		}
	}
	for _, blocker := range blockers {
		result.Blockers = append(result.Blockers, blocker.Reason)
	}
	if goal.Status == "regressed" {
		result.Regressions = append(result.Regressions, goal.ID)
		result.RecommendedNext = append(result.RecommendedNext, "Re-run stale or invalidated evidence for this goal")
	} else if len(result.Blockers) > 0 {
		result.RecommendedNext = append(result.RecommendedNext, "Resolve the first blocker for this goal")
	} else if len(result.Missing) > 0 {
		result.RecommendedNext = append(result.RecommendedNext, "Produce current passing evidence: "+result.Missing[0])
	}
	return result
}

func explainCapabilityStatus(capability CapabilitySummary, goals []GoalSummary, resources []ResourceSummary) StatusExplanation {
	result := StatusExplanation{State: "declared", Reason: "capability is declared and linked to project goals"}
	if len(resources) == 0 {
		result.State = "incomplete"
		result.Reason = "capability has no contributing resources"
		result.Missing = append(result.Missing, "contributing resources")
		result.RecommendedNext = append(result.RecommendedNext, "Link an architectural resource to "+capability.ID)
		return result
	}
	complete := len(goals) > 0
	for _, goal := range goals {
		if goal.Status == "regressed" {
			result.State = "regressed"
			result.Reason = "a dependent project goal has regressed"
			result.Regressions = append(result.Regressions, goal.ID)
			complete = false
		} else if goal.Status != "complete" {
			result.Missing = append(result.Missing, goal.ID)
			complete = false
		}
	}
	if complete {
		result.State = "complete"
		result.Reason = "all linked project goals are complete"
	} else if result.State != "regressed" {
		result.State = "partially_implemented"
		result.Reason = "contributing resources exist but linked goals are incomplete"
		result.RecommendedNext = append(result.RecommendedNext, "Advance linked goal "+result.Missing[0])
	}
	return result
}

func explainResourceStatus(resource *ResourceDetail) StatusExplanation {
	result := StatusExplanation{State: "settled", Reason: "resource declaration has accepted generated state"}
	if len(resource.Files) == 0 {
		result.State = "incomplete"
		result.Reason = "resource has no generated files"
		result.Missing = append(result.Missing, "generated files")
		result.RecommendedNext = append(result.RecommendedNext, "Generate or reconcile this resource")
	}
	for _, validation := range resource.Validations {
		if validation.Currency == "stale" || validation.Classification != "passed" {
			result.State = "regressed"
			result.Regressions = append(result.Regressions, validation.DefinitionID)
		}
	}
	return result
}

func actors(values []store.IntentActor) []Actor {
	result := make([]Actor, 0, len(values))
	for _, value := range values {
		result = append(result, Actor{ID: value.ID, Description: value.Description})
	}
	return result
}

func nonGoals(values []store.IntentNonGoal) []NonGoal {
	result := make([]NonGoal, 0, len(values))
	for _, value := range values {
		result = append(result, NonGoal{ID: value.ID, Description: value.Description})
	}
	return result
}

func blockersView(values []store.CompletionBlocker) []Blocker {
	result := make([]Blocker, 0, len(values))
	for _, value := range values {
		result = append(result, blockerView(value))
	}
	return result
}

func blockerView(value store.CompletionBlocker) Blocker {
	result := Blocker{
		ID: value.ID, GoalID: value.GoalID, Category: value.Category, Reason: value.Reason,
		SourceType: value.SourceType, SourceID: value.SourceID, CreatedAt: value.CreatedAt,
	}
	if value.GoalID != "" {
		result.Links = append(result.Links, link("goal", "goal", value.GoalID, "/api/v1/goals/"+value.GoalID))
	}
	return result
}

func transitions(values []store.StatusTransition, limit int) []StatusTransition {
	if limit > 0 && len(values) > limit {
		values = values[len(values)-limit:]
	}
	result := make([]StatusTransition, 0, len(values))
	for _, value := range values {
		result = append(result, StatusTransition{
			ID: value.ID, FromStatus: value.FromStatus, ToStatus: value.ToStatus,
			Reason: value.Reason, SourceType: value.Source.Type, SourceID: value.Source.ID,
			SessionID: value.Source.SessionID, CreatedAt: value.CreatedAt,
		})
	}
	return result
}

func relationship(resourceID, kind string) RelationshipRef {
	return RelationshipRef{ResourceID: resourceID, Kind: kind, Links: []Link{link("resource", "resource", resourceID, "/api/v1/resources/"+resourceID)}}
}

func link(rel, kind, id, href string) Link {
	return Link{Rel: rel, Kind: kind, ID: id, HREF: href}
}

func take[T any](values []T, limit int) ([]T, bool) {
	if len(values) <= limit {
		return values, false
	}
	return values[:limit], true
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func intersects(left, right []string) bool {
	for _, value := range left {
		if contains(right, value) {
			return true
		}
	}
	return false
}

func containsFold(value, query string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(query))
}

func evidenceUsedByScenarios(evidenceID string, scenarios []AcceptanceScenario) bool {
	for _, scenario := range scenarios {
		if contains(scenario.Evidence, evidenceID) {
			return true
		}
	}
	return false
}

func targets(values []store.IntentVerificationTarget, kind, id string) bool {
	for _, value := range values {
		if value.Kind == kind && value.ID == id {
			return true
		}
	}
	return false
}
