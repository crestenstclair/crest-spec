// Package completion derives explainable goal and project lifecycle state
// from concrete planning, implementation, validation, and evidence facts.
package completion

import (
	"fmt"
	"sort"
	"strings"
)

type Status string

const (
	Declared             Status = "declared"
	Planned              Status = "planned"
	PartiallyImplemented Status = "partially_implemented"
	LocallyValidated     Status = "locally_validated"
	Integrated           Status = "integrated"
	BehaviorallyVerified Status = "behaviorally_verified"
	Complete             Status = "complete"
	Blocked              Status = "blocked"
	Regressed            Status = "regressed"
)

type Blocker struct {
	Category   string
	Reason     string
	SourceType string
	SourceID   string
}

// GoalFacts contains observations only. Callers cannot supply the resulting
// status, which prevents generators or hosts from declaring their own work
// complete.
type GoalFacts struct {
	GoalID string

	PreviouslyComplete      bool
	ReverificationRequired  bool
	RequiredEvidenceCurrent bool
	RequiredGoalChecksPass  bool
	DependenciesComplete    bool
	AcceptanceVerified      bool

	ContributionCount          int
	AcceptedContributions      int
	AllContributionsLocal      bool
	AllContributionsIntegrated bool
	HasActivePlan              bool

	Blockers []Blocker
}

type GoalProjection struct {
	GoalID   string
	Status   Status
	Reason   string
	Missing  []string
	Blockers []Blocker
}

func DeriveGoal(facts GoalFacts) GoalProjection {
	projection := GoalProjection{GoalID: facts.GoalID, Blockers: append([]Blocker(nil), facts.Blockers...)}

	if facts.PreviouslyComplete && facts.ReverificationRequired &&
		(!facts.RequiredEvidenceCurrent || !facts.RequiredGoalChecksPass || !facts.AcceptanceVerified) {
		projection.Status = Regressed
		projection.Reason = "previously complete goal requires re-verification after an impact event"
		projection.Missing = goalCompletionGaps(facts)
		return projection
	}
	if len(facts.Blockers) > 0 {
		projection.Status = Blocked
		projection.Reason = summarizeBlockers(facts.Blockers)
		return projection
	}
	if facts.DependenciesComplete && facts.AcceptanceVerified && facts.RequiredEvidenceCurrent && facts.RequiredGoalChecksPass {
		projection.Status = Complete
		projection.Reason = "dependencies, acceptance evidence, and goal checks are current and passing"
		return projection
	}
	if facts.AcceptanceVerified && facts.RequiredEvidenceCurrent {
		projection.Status = BehaviorallyVerified
		projection.Reason = "acceptance evidence passes; dependency or goal-level completion checks remain"
		projection.Missing = goalCompletionGaps(facts)
		return projection
	}
	if facts.ContributionCount > 0 && facts.AllContributionsIntegrated {
		projection.Status = Integrated
		projection.Reason = "all declared contributions pass integration validation"
		projection.Missing = []string{"behavioral acceptance evidence"}
		return projection
	}
	if facts.ContributionCount > 0 && facts.AllContributionsLocal {
		projection.Status = LocallyValidated
		projection.Reason = "all declared contributions pass resource-level validation"
		projection.Missing = []string{"integration validation", "behavioral acceptance evidence"}
		return projection
	}
	if facts.AcceptedContributions > 0 {
		projection.Status = PartiallyImplemented
		projection.Reason = fmt.Sprintf("%d of %d declared contributions are accepted", facts.AcceptedContributions, facts.ContributionCount)
		projection.Missing = []string{"remaining resource contributions"}
		return projection
	}
	if facts.HasActivePlan {
		projection.Status = Planned
		projection.Reason = "an active reconciliation plan targets this goal"
		projection.Missing = []string{"accepted resource contributions"}
		return projection
	}
	projection.Status = Declared
	projection.Reason = "goal is declared but no active implementation plan targets it"
	projection.Missing = []string{"goal-directed plan"}
	return projection
}

type ProjectFacts struct {
	ProjectName            string
	PreviouslyComplete     bool
	ReverificationRequired bool
	ProjectChecksPass      bool
	RequiredGoals          []GoalProjection
	Blockers               []Blocker
}

type ProjectProjection struct {
	ProjectName  string
	Status       Status
	Reason       string
	MissingGoals []string
	Blockers     []Blocker
}

func DeriveProject(facts ProjectFacts) ProjectProjection {
	projection := ProjectProjection{ProjectName: facts.ProjectName, Blockers: append([]Blocker(nil), facts.Blockers...)}
	for _, goal := range facts.RequiredGoals {
		if goal.Status == Regressed {
			projection.Status = Regressed
			projection.Reason = "one or more required goals have regressed"
			projection.MissingGoals = incompleteGoalIDs(facts.RequiredGoals)
			return projection
		}
	}
	if facts.PreviouslyComplete && facts.ReverificationRequired && !facts.ProjectChecksPass {
		projection.Status = Regressed
		projection.Reason = "previously complete project has stale or failed project checks"
		projection.MissingGoals = incompleteGoalIDs(facts.RequiredGoals)
		return projection
	}
	for _, goal := range facts.RequiredGoals {
		if goal.Status == Blocked {
			projection.Blockers = append(projection.Blockers, goal.Blockers...)
		}
	}
	if len(projection.Blockers) > 0 {
		projection.Status = Blocked
		projection.Reason = summarizeBlockers(projection.Blockers)
		projection.MissingGoals = incompleteGoalIDs(facts.RequiredGoals)
		return projection
	}
	if len(facts.RequiredGoals) > 0 && allGoalsAt(facts.RequiredGoals, Complete) && facts.ProjectChecksPass {
		projection.Status = Complete
		projection.Reason = "all required goals and project checks are complete"
		return projection
	}
	projection.MissingGoals = incompleteGoalIDs(facts.RequiredGoals)
	if len(facts.RequiredGoals) > 0 && allGoalsAtLeast(facts.RequiredGoals, BehaviorallyVerified) {
		projection.Status = BehaviorallyVerified
		projection.Reason = "required goals are behaviorally verified; project completion checks remain"
		return projection
	}
	if len(facts.RequiredGoals) > 0 && allGoalsAtLeast(facts.RequiredGoals, Integrated) {
		projection.Status = Integrated
		projection.Reason = "all required goals are integrated"
		return projection
	}
	if len(facts.RequiredGoals) > 0 && allGoalsAtLeast(facts.RequiredGoals, LocallyValidated) {
		projection.Status = LocallyValidated
		projection.Reason = "all required goals are locally validated"
		return projection
	}
	if anyGoalAtLeast(facts.RequiredGoals, PartiallyImplemented) {
		projection.Status = PartiallyImplemented
		projection.Reason = "at least one required goal has accepted implementation"
		return projection
	}
	if anyGoalAtLeast(facts.RequiredGoals, Planned) {
		projection.Status = Planned
		projection.Reason = "at least one required goal has an active plan"
		return projection
	}
	projection.Status = Declared
	projection.Reason = "required goals are declared but not yet planned"
	return projection
}

func goalCompletionGaps(facts GoalFacts) []string {
	var gaps []string
	if !facts.DependenciesComplete {
		gaps = append(gaps, "goal dependencies")
	}
	if !facts.AcceptanceVerified {
		gaps = append(gaps, "behavioral acceptance evidence")
	}
	if !facts.RequiredEvidenceCurrent {
		gaps = append(gaps, "current required evidence")
	}
	if !facts.RequiredGoalChecksPass {
		gaps = append(gaps, "goal-level checks")
	}
	return gaps
}

func summarizeBlockers(blockers []Blocker) string {
	reasons := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		reasons = append(reasons, blocker.Category+": "+blocker.Reason)
	}
	sort.Strings(reasons)
	return "blocked by " + strings.Join(reasons, "; ")
}

func incompleteGoalIDs(goals []GoalProjection) []string {
	var ids []string
	for _, goal := range goals {
		if goal.Status != Complete {
			ids = append(ids, goal.GoalID)
		}
	}
	sort.Strings(ids)
	return ids
}

var ranks = map[Status]int{Declared: 0, Planned: 1, PartiallyImplemented: 2, LocallyValidated: 3, Integrated: 4, BehaviorallyVerified: 5, Complete: 6}

func allGoalsAt(goals []GoalProjection, status Status) bool {
	for _, goal := range goals {
		if goal.Status != status {
			return false
		}
	}
	return true
}

func allGoalsAtLeast(goals []GoalProjection, status Status) bool {
	for _, goal := range goals {
		if ranks[goal.Status] < ranks[status] {
			return false
		}
	}
	return true
}

func anyGoalAtLeast(goals []GoalProjection, status Status) bool {
	for _, goal := range goals {
		if ranks[goal.Status] >= ranks[status] {
			return true
		}
	}
	return false
}
