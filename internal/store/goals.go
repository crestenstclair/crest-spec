package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/crestenstclair/crest-spec/internal/db"
)

// ProjectIntentSnapshot is the normalized, specification-derived project
// intent persisted by crest-spec. Declarations remain authoritative in CUE;
// this snapshot gives runtime state stable identities and queryable links.
type ProjectIntentSnapshot struct {
	ProjectName   string
	Mission       string
	SpecHash      string
	Actors        []IntentActor
	Goals         []IntentGoal
	Capabilities  []IntentCapability
	Requirements  []IntentRequirement
	Acceptance    []IntentAcceptance
	Evidence      []IntentEvidence
	NonGoals      []IntentNonGoal
	RequiredGoals []string
}

type IntentActor struct {
	ID          string
	Description string
}

type IntentGoal struct {
	ID          string
	Description string
	Priority    string
	Actors      []string
	DependsOn   []string
}

type IntentCapability struct {
	ID          string
	Description string
	Goals       []string
}

type IntentRequirement struct {
	ID           string
	Kind         string
	Description  string
	Goals        []string
	Capabilities []string
}

type IntentAcceptance struct {
	ID           string
	CapabilityID string
	ActorID      string
	Description  string
	Ordinal      int
	Steps        []IntentAcceptanceStep
	Evidence     []string
}

type IntentAcceptanceStep struct {
	Action   string
	Observes string
}

type IntentEvidence struct {
	ID          string
	Kind        string
	Description string
}

type IntentNonGoal struct {
	ID          string
	Description string
}

// ProjectIntentState is the canonical SQLite projection returned to APIs,
// MCP tools, and the dashboard. It includes declarations and their explicit
// relationships rather than requiring callers to reconstruct joins.
type ProjectIntentState struct {
	ProjectName      string
	Mission          string
	SpecHash         string
	CompletionStatus string
	Actors           []IntentActor
	Goals            []PersistedGoal
	Capabilities     []IntentCapability
	Requirements     []IntentRequirement
	Acceptance       []IntentAcceptance
	Evidence         []IntentEvidence
	NonGoals         []IntentNonGoal
	RequiredGoals    []string
}

type PersistedGoal struct {
	IntentGoal
	Status       string
	StatusReason string
}

// ReconcileProjectIntent atomically replaces the materialized relationships
// for one project and tombstones declarations removed from the current spec.
// Lifecycle status and history on existing goals are deliberately preserved.
func (s *Store) ReconcileProjectIntent(ctx context.Context, snapshot ProjectIntentSnapshot) (err error) {
	if snapshot.ProjectName == "" || snapshot.Mission == "" || snapshot.SpecHash == "" {
		return fmt.Errorf("reconcile project intent: project name, mission, and spec hash are required")
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reconcile project intent: begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	q := s.queries.WithTx(tx)
	timestamp := now()
	project := snapshot.ProjectName
	wrap := func(operation string, operationErr error) error {
		return fmt.Errorf("reconcile project intent: %s: %w", operation, operationErr)
	}

	if err = q.UpsertProjectState(ctx, db.UpsertProjectStateParams{
		ProjectName: project, Mission: snapshot.Mission, SpecHash: snapshot.SpecHash,
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}); err != nil {
		return wrap("upsert project", err)
	}

	clearOperations := []struct {
		name string
		fn   func(context.Context, string) error
	}{
		{"acceptance evidence", q.ClearAcceptanceEvidence},
		{"acceptance steps", q.ClearAcceptanceSteps},
		{"goal actors", q.ClearGoalIntentRelationships},
		{"goal dependencies", q.ClearGoalDependencies},
		{"capability goals", q.ClearCapabilityGoals},
		{"requirement goals", q.ClearRequirementGoals},
		{"requirement capabilities", q.ClearRequirementCapabilities},
		{"required goals", q.ClearProjectRequiredGoals},
	}
	for _, operation := range clearOperations {
		if err = operation.fn(ctx, project); err != nil {
			return wrap("clear "+operation.name, err)
		}
	}

	deactivate := []struct {
		name string
		fn   func() error
	}{
		{"acceptance scenarios", func() error {
			return q.DeactivateAcceptanceScenarios(ctx, db.DeactivateAcceptanceScenariosParams{UpdatedAt: timestamp, ProjectName: project})
		}},
		{"actors", func() error {
			return q.DeactivateProjectActors(ctx, db.DeactivateProjectActorsParams{UpdatedAt: timestamp, ProjectName: project})
		}},
		{"goals", func() error {
			return q.DeactivateProjectGoals(ctx, db.DeactivateProjectGoalsParams{UpdatedAt: timestamp, ProjectName: project})
		}},
		{"capabilities", func() error {
			return q.DeactivateProjectCapabilities(ctx, db.DeactivateProjectCapabilitiesParams{UpdatedAt: timestamp, ProjectName: project})
		}},
		{"requirements", func() error {
			return q.DeactivateProjectRequirements(ctx, db.DeactivateProjectRequirementsParams{UpdatedAt: timestamp, ProjectName: project})
		}},
		{"evidence requirements", func() error {
			return q.DeactivateEvidenceRequirements(ctx, db.DeactivateEvidenceRequirementsParams{UpdatedAt: timestamp, ProjectName: project})
		}},
		{"non-goals", func() error {
			return q.DeactivateProjectNonGoals(ctx, db.DeactivateProjectNonGoalsParams{UpdatedAt: timestamp, ProjectName: project})
		}},
	}
	for _, operation := range deactivate {
		if err = operation.fn(); err != nil {
			return wrap("deactivate "+operation.name, err)
		}
	}

	for _, actor := range sortedByID(snapshot.Actors, func(v IntentActor) string { return v.ID }) {
		if err = q.UpsertProjectActor(ctx, db.UpsertProjectActorParams{ID: actor.ID, ProjectName: project, Description: actor.Description, SpecHash: snapshot.SpecHash, UpdatedAt: timestamp}); err != nil {
			return wrap("upsert actor "+actor.ID, err)
		}
	}
	for _, goal := range sortedByID(snapshot.Goals, func(v IntentGoal) string { return v.ID }) {
		if err = q.UpsertProjectGoal(ctx, db.UpsertProjectGoalParams{ID: goal.ID, ProjectName: project, Description: goal.Description, Priority: goal.Priority, SpecHash: snapshot.SpecHash, CreatedAt: timestamp, UpdatedAt: timestamp}); err != nil {
			return wrap("upsert goal "+goal.ID, err)
		}
	}
	for _, capability := range sortedByID(snapshot.Capabilities, func(v IntentCapability) string { return v.ID }) {
		if err = q.UpsertProjectCapability(ctx, db.UpsertProjectCapabilityParams{ID: capability.ID, ProjectName: project, Description: capability.Description, SpecHash: snapshot.SpecHash, UpdatedAt: timestamp}); err != nil {
			return wrap("upsert capability "+capability.ID, err)
		}
	}
	for _, requirement := range sortedByID(snapshot.Requirements, func(v IntentRequirement) string { return v.ID }) {
		if err = q.UpsertProjectRequirement(ctx, db.UpsertProjectRequirementParams{ID: requirement.ID, ProjectName: project, Kind: requirement.Kind, Description: requirement.Description, SpecHash: snapshot.SpecHash, UpdatedAt: timestamp}); err != nil {
			return wrap("upsert requirement "+requirement.ID, err)
		}
	}
	for _, evidence := range sortedByID(snapshot.Evidence, func(v IntentEvidence) string { return v.ID }) {
		if err = q.UpsertEvidenceRequirement(ctx, db.UpsertEvidenceRequirementParams{ID: evidence.ID, ProjectName: project, Kind: evidence.Kind, Description: evidence.Description, SpecHash: snapshot.SpecHash, UpdatedAt: timestamp}); err != nil {
			return wrap("upsert evidence "+evidence.ID, err)
		}
	}
	for _, nonGoal := range sortedByID(snapshot.NonGoals, func(v IntentNonGoal) string { return v.ID }) {
		if err = q.UpsertProjectNonGoal(ctx, db.UpsertProjectNonGoalParams{ID: nonGoal.ID, ProjectName: project, Description: nonGoal.Description, SpecHash: snapshot.SpecHash, UpdatedAt: timestamp}); err != nil {
			return wrap("upsert non-goal "+nonGoal.ID, err)
		}
	}
	for _, acceptance := range sortedByID(snapshot.Acceptance, func(v IntentAcceptance) string { return v.ID }) {
		var actorID *string
		if acceptance.ActorID != "" {
			actor := acceptance.ActorID
			actorID = &actor
		}
		if err = q.UpsertAcceptanceScenario(ctx, db.UpsertAcceptanceScenarioParams{ID: acceptance.ID, CapabilityID: acceptance.CapabilityID, ActorID: actorID, Description: acceptance.Description, Ordinal: int64(acceptance.Ordinal), SpecHash: snapshot.SpecHash, UpdatedAt: timestamp}); err != nil {
			return wrap("upsert acceptance scenario "+acceptance.ID, err)
		}
	}

	for _, goal := range sortedByID(snapshot.Goals, func(v IntentGoal) string { return v.ID }) {
		for _, actorID := range sortedStrings(goal.Actors) {
			if err = q.InsertGoalActor(ctx, db.InsertGoalActorParams{GoalID: goal.ID, ActorID: actorID}); err != nil {
				return wrap("link goal actor "+goal.ID+" -> "+actorID, err)
			}
		}
		for _, dependencyID := range sortedStrings(goal.DependsOn) {
			if err = q.InsertGoalDependency(ctx, db.InsertGoalDependencyParams{GoalID: goal.ID, DependencyGoalID: dependencyID}); err != nil {
				return wrap("link goal dependency "+goal.ID+" -> "+dependencyID, err)
			}
		}
	}
	for _, capability := range sortedByID(snapshot.Capabilities, func(v IntentCapability) string { return v.ID }) {
		for _, goalID := range sortedStrings(capability.Goals) {
			if err = q.InsertCapabilityGoal(ctx, db.InsertCapabilityGoalParams{CapabilityID: capability.ID, GoalID: goalID}); err != nil {
				return wrap("link capability goal "+capability.ID+" -> "+goalID, err)
			}
		}
	}
	for _, requirement := range sortedByID(snapshot.Requirements, func(v IntentRequirement) string { return v.ID }) {
		for _, goalID := range sortedStrings(requirement.Goals) {
			if err = q.InsertRequirementGoal(ctx, db.InsertRequirementGoalParams{RequirementID: requirement.ID, GoalID: goalID}); err != nil {
				return wrap("link requirement goal "+requirement.ID+" -> "+goalID, err)
			}
		}
		for _, capabilityID := range sortedStrings(requirement.Capabilities) {
			if err = q.InsertRequirementCapability(ctx, db.InsertRequirementCapabilityParams{RequirementID: requirement.ID, CapabilityID: capabilityID}); err != nil {
				return wrap("link requirement capability "+requirement.ID+" -> "+capabilityID, err)
			}
		}
	}
	for _, acceptance := range sortedByID(snapshot.Acceptance, func(v IntentAcceptance) string { return v.ID }) {
		for ordinal, step := range acceptance.Steps {
			if err = q.InsertAcceptanceStep(ctx, db.InsertAcceptanceStepParams{ScenarioID: acceptance.ID, Ordinal: int64(ordinal), Action: step.Action, Observes: step.Observes}); err != nil {
				return wrap("insert acceptance step for "+acceptance.ID, err)
			}
		}
		for _, evidenceID := range sortedStrings(acceptance.Evidence) {
			if err = q.InsertAcceptanceEvidence(ctx, db.InsertAcceptanceEvidenceParams{ScenarioID: acceptance.ID, EvidenceID: evidenceID}); err != nil {
				return wrap("link acceptance evidence "+acceptance.ID+" -> "+evidenceID, err)
			}
		}
	}
	for ordinal, goalID := range snapshot.RequiredGoals {
		if err = q.InsertProjectRequiredGoal(ctx, db.InsertProjectRequiredGoalParams{ProjectName: project, GoalID: goalID, Ordinal: int64(ordinal)}); err != nil {
			return wrap("link required goal "+goalID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return wrap("commit transaction", err)
	}
	return nil
}

// GetProjectIntent returns the complete current intent projection for a
// project. It is intentionally SQLite-backed, not reconstructed from CUE.
func (s *Store) GetProjectIntent(ctx context.Context, projectName string) (*ProjectIntentState, error) {
	state, err := s.queries.GetProjectState(ctx, projectName)
	if err != nil {
		return nil, mapNotFound(err)
	}
	actors, err := s.queries.ListProjectActors(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list project actors: %w", err)
	}
	goals, err := s.queries.ListProjectGoals(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list project goals: %w", err)
	}
	capabilities, err := s.queries.ListProjectCapabilities(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list project capabilities: %w", err)
	}
	requirements, err := s.queries.ListProjectRequirements(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list project requirements: %w", err)
	}
	acceptance, err := s.queries.ListAcceptanceScenarios(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list acceptance scenarios: %w", err)
	}
	evidence, err := s.queries.ListEvidenceRequirements(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list evidence requirements: %w", err)
	}
	nonGoals, err := s.queries.ListProjectNonGoals(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list project non-goals: %w", err)
	}
	goalActors, err := s.queries.ListGoalActors(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list goal actors: %w", err)
	}
	goalDependencies, err := s.queries.ListGoalDependencies(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list goal dependencies: %w", err)
	}
	capabilityGoals, err := s.queries.ListCapabilityGoals(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list capability goals: %w", err)
	}
	requirementGoals, err := s.queries.ListRequirementGoals(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list requirement goals: %w", err)
	}
	requirementCapabilities, err := s.queries.ListRequirementCapabilities(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list requirement capabilities: %w", err)
	}
	steps, err := s.queries.ListAcceptanceSteps(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list acceptance steps: %w", err)
	}
	acceptanceEvidence, err := s.queries.ListAcceptanceEvidence(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list acceptance evidence: %w", err)
	}
	requiredGoals, err := s.queries.ListProjectRequiredGoals(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list required goals: %w", err)
	}

	out := &ProjectIntentState{ProjectName: state.ProjectName, Mission: state.Mission, SpecHash: state.SpecHash, CompletionStatus: state.CompletionStatus}
	for _, actor := range actors {
		out.Actors = append(out.Actors, IntentActor{ID: actor.ID, Description: actor.Description})
	}
	actorLinks := make(map[string][]string)
	for _, link := range goalActors {
		actorLinks[link.GoalID] = append(actorLinks[link.GoalID], link.ActorID)
	}
	dependencyLinks := make(map[string][]string)
	for _, link := range goalDependencies {
		dependencyLinks[link.GoalID] = append(dependencyLinks[link.GoalID], link.DependencyGoalID)
	}
	for _, goal := range goals {
		out.Goals = append(out.Goals, PersistedGoal{IntentGoal: IntentGoal{ID: goal.ID, Description: goal.Description, Priority: goal.Priority, Actors: actorLinks[goal.ID], DependsOn: dependencyLinks[goal.ID]}, Status: goal.Status, StatusReason: goal.StatusReason})
	}
	capabilityLinks := make(map[string][]string)
	for _, link := range capabilityGoals {
		capabilityLinks[link.CapabilityID] = append(capabilityLinks[link.CapabilityID], link.GoalID)
	}
	for _, capability := range capabilities {
		out.Capabilities = append(out.Capabilities, IntentCapability{ID: capability.ID, Description: capability.Description, Goals: capabilityLinks[capability.ID]})
	}
	requirementGoalLinks := make(map[string][]string)
	for _, link := range requirementGoals {
		requirementGoalLinks[link.RequirementID] = append(requirementGoalLinks[link.RequirementID], link.GoalID)
	}
	requirementCapabilityLinks := make(map[string][]string)
	for _, link := range requirementCapabilities {
		requirementCapabilityLinks[link.RequirementID] = append(requirementCapabilityLinks[link.RequirementID], link.CapabilityID)
	}
	for _, requirement := range requirements {
		out.Requirements = append(out.Requirements, IntentRequirement{ID: requirement.ID, Kind: requirement.Kind, Description: requirement.Description, Goals: requirementGoalLinks[requirement.ID], Capabilities: requirementCapabilityLinks[requirement.ID]})
	}
	stepLinks := make(map[string][]IntentAcceptanceStep)
	for _, step := range steps {
		stepLinks[step.ScenarioID] = append(stepLinks[step.ScenarioID], IntentAcceptanceStep{Action: step.Action, Observes: step.Observes})
	}
	evidenceLinks := make(map[string][]string)
	for _, link := range acceptanceEvidence {
		evidenceLinks[link.ScenarioID] = append(evidenceLinks[link.ScenarioID], link.EvidenceID)
	}
	for _, scenario := range acceptance {
		actorID := ""
		if scenario.ActorID != nil {
			actorID = *scenario.ActorID
		}
		out.Acceptance = append(out.Acceptance, IntentAcceptance{ID: scenario.ID, CapabilityID: scenario.CapabilityID, ActorID: actorID, Description: scenario.Description, Ordinal: int(scenario.Ordinal), Steps: stepLinks[scenario.ID], Evidence: evidenceLinks[scenario.ID]})
	}
	for _, item := range evidence {
		out.Evidence = append(out.Evidence, IntentEvidence{ID: item.ID, Kind: item.Kind, Description: item.Description})
	}
	for _, item := range nonGoals {
		out.NonGoals = append(out.NonGoals, IntentNonGoal{ID: item.ID, Description: item.Description})
	}
	for _, item := range requiredGoals {
		out.RequiredGoals = append(out.RequiredGoals, item.GoalID)
	}
	return out, nil
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func sortedByID[T any](values []T, id func(T) string) []T {
	out := append([]T(nil), values...)
	sort.Slice(out, func(i, j int) bool { return id(out[i]) < id(out[j]) })
	return out
}
