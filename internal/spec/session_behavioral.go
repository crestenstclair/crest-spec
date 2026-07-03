package spec

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/check"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// ---------------------------------------------------------------------------
// Input / result types
// ---------------------------------------------------------------------------

// CheckInput is a single behavioral claim with its full falsification check.
type CheckInput struct {
	Behavior string      `json:"behavior"`
	Check    check.Check `json:"check"`
}

// TaskInput is a single task to commit for a resource, including its checks.
type TaskInput struct {
	Description string       `json:"description"`
	Checks      []CheckInput `json:"checks"`
}

// VerifyResult is the outcome of running Verify on a single check.
type VerifyResult struct {
	Passed    bool   `json:"passed"`
	Theater   bool   `json:"theater"`
	Malformed bool   `json:"malformed"`
	Reason    string `json:"reason"`
}

// GraduateResult is the outcome of graduating a check: the structured claim
// the orchestrator authors back into the spec.
type GraduateResult struct {
	CheckID    string      `json:"check_id"`
	ResourceID string      `json:"resource_id"`
	Behavior   string      `json:"behavior"`
	Check      check.Check `json:"check"`
}

// ---------------------------------------------------------------------------
// Spec methods
// ---------------------------------------------------------------------------

// DesignCommit upserts a bounded-context design record keyed on contextName.
func (s *Spec) DesignCommit(ctx context.Context, contextName, contractJSON string) error {
	if contextName == "" {
		return fmt.Errorf("context_name is required")
	}
	return s.store.UpsertDesign(contextName, "", contractJSON)
}

// TasksCommit replaces the set of tasks and their associated behavioral checks
// for the given resource. Any prior tasks and checks for the resource are
// deleted before the new ones are inserted, making repeated calls idempotent
// (re-committing replaces, not appends). Task and check IDs are generated
// server-side. Deletion order is checks first (they reference task_id), then
// tasks. The delete + insert is sequential rather than transactional.
func (s *Spec) TasksCommit(ctx context.Context, resourceID string, tasks []TaskInput) error {
	if resourceID == "" {
		return fmt.Errorf("resource_id is required")
	}
	// Delete existing checks first (FK dependency on task_id), then tasks.
	if err := s.store.DeleteChecksByResource(resourceID); err != nil {
		return fmt.Errorf("delete prior checks: %w", err)
	}
	if err := s.store.DeleteTasksByResource(resourceID); err != nil {
		return fmt.Errorf("delete prior tasks: %w", err)
	}
	for _, t := range tasks {
		taskID := uuid.New().String()
		if err := s.store.InsertTask(taskID, resourceID, "", "", t.Description); err != nil {
			return fmt.Errorf("insert task: %w", err)
		}
		for _, ci := range t.Checks {
			// Loud early gate: reject descriptions-as-field-names at authoring
			// time so the tasks agent fixes them immediately, instead of the
			// check dying structurally at verification.
			for _, p := range ci.Check.Predicates {
				if !check.FieldNameValid(p.Field) {
					return fmt.Errorf("check %q: predicate field name %q is a description, not a measurable key — use a short key the witness can emit verbatim", ci.Behavior, p.Field)
				}
			}
			checkBytes, err := json.Marshal(ci.Check)
			if err != nil {
				return fmt.Errorf("marshal check: %w", err)
			}
			checkID := uuid.New().String()
			if err := s.store.InsertCheck(checkID, taskID, resourceID, ci.Behavior, string(checkBytes)); err != nil {
				return fmt.Errorf("insert check: %w", err)
			}
		}
	}
	return nil
}

// Verify loads a check by ID, evaluates check.Verify against the supplied
// real and stub observations, persists the verification record, and updates
// the check's state (passed/failed/theater). Returns a VerifyResult.
//
// This is FAIL-CLOSED: any evaluation failure (including theater) is treated
// as a rejection. A check passes only when real passes AND stub fails.
func (s *Spec) Verify(ctx context.Context, checkID string, real, stub map[string]any) (VerifyResult, error) {
	bc, err := s.store.GetCheck(checkID)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("get check %q: %w", checkID, err)
	}

	var c check.Check
	if err := json.Unmarshal([]byte(bc.CheckJSON), &c); err != nil {
		return VerifyResult{}, fmt.Errorf("parse check_json: %w", err)
	}

	// Fail-closed stub guard. check.Verify reads an absent predicate field as
	// "the stub fails the behavior" — good discrimination. That makes the stub
	// side fail-OPEN: an empty or field-missing stub would let a passing real
	// observation graduate alone, defeating the falsification gate. Require the
	// stub to be a genuine measurement: non-empty, and carrying every predicate
	// field that the real observation is judged on. A malformed stub is a
	// rejection, not a pass.
	var verdict check.Verdict
	if malformed, reason := stubMalformed(c, real, stub); malformed {
		verdict = check.Verdict{Passed: false, Theater: false, Reason: "malformed verification: " + reason}
	} else {
		verdict = check.Verify(c, check.Observation(real), check.Observation(stub))
	}

	// Serialize observations for storage.
	realJSON, err := json.Marshal(real)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("marshal real_obs: %w", err)
	}
	stubJSON, err := json.Marshal(stub)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("marshal stub_obs: %w", err)
	}

	// Persist verification record.
	verID := uuid.New().String()
	if err := s.store.InsertVerification(
		verID, checkID, bc.ResourceID,
		string(realJSON), string(stubJSON),
		verdict.Reason,
		verdict.Passed, verdict.Theater,
	); err != nil {
		return VerifyResult{}, fmt.Errorf("insert verification: %w", err)
	}

	// Transition check state (fail-closed: theater and real-fail both reject).
	// A structural verdict (the predicate can never pass regardless of
	// implementation — e.g. eq on a non-numeric field with no member) is
	// quarantined as 'malformed' rather than 'failed', so the orchestrator
	// does not cycle it through the verify->regenerate loop forever. The
	// pre-existing stubMalformed guard above is unaffected: it sets
	// verdict.Structural=false, so it still lands on 'failed'.
	var newState string
	reason := verdict.Reason
	switch {
	case verdict.Theater:
		newState = "theater"
	case verdict.Structural:
		newState = "malformed"
		reason = "malformed check: " + verdict.Reason
	case !verdict.Passed:
		newState = "failed"
	default:
		newState = "passed"
	}
	if err := s.store.SetCheckState(checkID, newState); err != nil {
		return VerifyResult{}, fmt.Errorf("set check state: %w", err)
	}

	return VerifyResult{
		Passed:    verdict.Passed,
		Theater:   verdict.Theater,
		Malformed: newState == "malformed",
		Reason:    reason,
	}, nil
}

// Graduate transitions a check that is in the "passed" state to "graduated"
// and returns the structured check so the orchestrator can author it into the
// spec. The server never writes .cue files directly.
func (s *Spec) Graduate(ctx context.Context, checkID string) (GraduateResult, error) {
	bc, err := s.store.GetCheck(checkID)
	if err != nil {
		return GraduateResult{}, fmt.Errorf("get check %q: %w", checkID, err)
	}
	if bc.State != "passed" {
		return GraduateResult{}, fmt.Errorf("check %q is in state %q; must be 'passed' to graduate", checkID, bc.State)
	}

	var c check.Check
	if err := json.Unmarshal([]byte(bc.CheckJSON), &c); err != nil {
		return GraduateResult{}, fmt.Errorf("parse check_json: %w", err)
	}

	if err := s.store.SetCheckState(checkID, "graduated"); err != nil {
		return GraduateResult{}, fmt.Errorf("set check state: %w", err)
	}

	return GraduateResult{
		CheckID:    checkID,
		ResourceID: bc.ResourceID,
		Behavior:   bc.Behavior,
		Check:      c,
	}, nil
}

// stubMalformed reports whether a verification's observations are unfit to
// falsify the check. The stub must be a genuine measurement: non-empty, and
// containing every predicate field that the behavior is judged on (the real
// observation must too). If a predicate field is absent from the stub,
// check.Verify would score the stub as "failed the behavior" for the wrong
// reason — absence, not a measured no-op — and let the real observation pass
// alone. Returns (true, reason) when the verification must be rejected.
func stubMalformed(c check.Check, real, stub map[string]any) (bool, string) {
	if len(stub) == 0 {
		return true, "stub observation is empty; it cannot falsify the behavior"
	}
	for _, p := range c.Predicates {
		if _, ok := stub[p.Field]; !ok {
			return true, fmt.Sprintf("stub observation is missing predicate field %q; the stub is not a real measurement", p.Field)
		}
		if _, ok := real[p.Field]; !ok {
			return true, fmt.Sprintf("real observation is missing predicate field %q", p.Field)
		}
	}
	return false, ""
}

// GetDesign retrieves a design by context name.
func (s *Spec) GetDesign(ctx context.Context, contextName string) (*store.Design, error) {
	return s.store.GetDesign(contextName)
}

// ListDesigns returns all design records.
func (s *Spec) ListDesigns(ctx context.Context) ([]store.Design, error) {
	return s.store.ListDesigns()
}

// ListTasksByResource returns all behavioral tasks for a resource.
func (s *Spec) ListTasksByResource(ctx context.Context, resourceID string) ([]store.BehavioralTask, error) {
	return s.store.ListTasksByResource(resourceID)
}

// ListChecksByResource returns all behavioral checks for a resource.
func (s *Spec) ListChecksByResource(ctx context.Context, resourceID string) ([]store.BehavioralCheck, error) {
	return s.store.ListChecksByResource(resourceID)
}
