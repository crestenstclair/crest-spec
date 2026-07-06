package store

import (
	"context"
	"time"

	"github.com/crestenstclair/crest-spec/internal/db"
)

// ---------------------------------------------------------------------------
// Behavioral pipeline domain types
// ---------------------------------------------------------------------------

// Design is the store's domain type for a bounded-context design record.
type Design struct {
	ContextName  string
	SpecHash     string
	ContractJSON string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// BehavioralTask is the store's domain type for a per-resource behavioral task.
type BehavioralTask struct {
	ID          string
	ResourceID  string
	ContextName string
	SpecHash    string
	Description string
	State       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// BehavioralCheck is the store's domain type for a falsification-gated
// behavioral claim attached to a task.
type BehavioralCheck struct {
	ID         string
	TaskID     string
	ResourceID string
	Behavior   string
	CheckJSON  string
	State      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Verification is the store's domain type for a single verification run record.
type Verification struct {
	ID          string
	CheckID     string
	ResourceID  string
	RealObsJSON string
	StubObsJSON string
	Passed      bool
	Theater     bool
	Reason      string
	CreatedAt   time.Time
}

// ---------------------------------------------------------------------------
// Converters
// ---------------------------------------------------------------------------

func dbDesignToDesign(d db.Design) Design {
	return Design{
		ContextName:  d.ContextName,
		SpecHash:     d.SpecHash,
		ContractJSON: d.ContractJson,
		CreatedAt:    parseTime(d.CreatedAt),
		UpdatedAt:    parseTime(d.UpdatedAt),
	}
}

func dbTaskToBehavioralTask(t db.Task) BehavioralTask {
	return BehavioralTask{
		ID:          t.ID,
		ResourceID:  t.ResourceID,
		ContextName: t.ContextName,
		SpecHash:    t.SpecHash,
		Description: t.Description,
		State:       t.State,
		CreatedAt:   parseTime(t.CreatedAt),
		UpdatedAt:   parseTime(t.UpdatedAt),
	}
}

func dbCheckToBehavioralCheck(c db.Check) BehavioralCheck {
	return BehavioralCheck{
		ID:         c.ID,
		TaskID:     c.TaskID,
		ResourceID: c.ResourceID,
		Behavior:   c.Behavior,
		CheckJSON:  c.CheckJson,
		State:      c.State,
		CreatedAt:  parseTime(c.CreatedAt),
		UpdatedAt:  parseTime(c.UpdatedAt),
	}
}

func dbVerificationToVerification(v db.Verification) Verification {
	return Verification{
		ID:          v.ID,
		CheckID:     v.CheckID,
		ResourceID:  v.ResourceID,
		RealObsJSON: v.RealObsJson,
		StubObsJSON: v.StubObsJson,
		Passed:      v.Passed != 0,
		Theater:     v.Theater != 0,
		Reason:      v.Reason,
		CreatedAt:   parseTime(v.CreatedAt),
	}
}

// ---------------------------------------------------------------------------
// Design CRUD
// ---------------------------------------------------------------------------

// UpsertDesign inserts or updates a design record keyed on context_name.
func (s *Store) UpsertDesign(contextName, specHash, contractJSON string) error {
	ts := now()
	return s.queries.UpsertDesign(context.Background(), db.UpsertDesignParams{
		ContextName:  contextName,
		SpecHash:     specHash,
		ContractJson: contractJSON,
		CreatedAt:    ts,
		UpdatedAt:    ts,
	})
}

// GetDesign retrieves a design by context name. Returns ErrNotFound if absent.
func (s *Store) GetDesign(contextName string) (*Design, error) {
	d, err := s.queries.GetDesign(context.Background(), contextName)
	if err != nil {
		return nil, mapNotFound(err)
	}
	out := dbDesignToDesign(d)
	return &out, nil
}

// ListDesigns returns all designs, ordered by context_name.
func (s *Store) ListDesigns() ([]Design, error) {
	rows, err := s.queries.ListDesigns(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]Design, len(rows))
	for i, r := range rows {
		out[i] = dbDesignToDesign(r)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// BehavioralTask CRUD
// ---------------------------------------------------------------------------

// InsertTask inserts a new behavioral task in the pending state.
func (s *Store) InsertTask(id, resourceID, contextName, specHash, description string) error {
	ts := now()
	return s.queries.InsertTask(context.Background(), db.InsertTaskParams{
		ID:          id,
		ResourceID:  resourceID,
		ContextName: contextName,
		SpecHash:    specHash,
		Description: description,
		CreatedAt:   ts,
		UpdatedAt:   ts,
	})
}

// GetTask retrieves a task by ID. Returns ErrNotFound if absent.
func (s *Store) GetTask(id string) (*BehavioralTask, error) {
	t, err := s.queries.GetTask(context.Background(), id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	out := dbTaskToBehavioralTask(t)
	return &out, nil
}

// ListTasksByResource returns all tasks for a resource, ordered by created_at.
func (s *Store) ListTasksByResource(resourceID string) ([]BehavioralTask, error) {
	rows, err := s.queries.ListTasksByResource(context.Background(), resourceID)
	if err != nil {
		return nil, err
	}
	out := make([]BehavioralTask, len(rows))
	for i, r := range rows {
		out[i] = dbTaskToBehavioralTask(r)
	}
	return out, nil
}

// SetTaskState updates the state of a task.
func (s *Store) SetTaskState(id, state string) error {
	return s.queries.SetTaskState(context.Background(), db.SetTaskStateParams{
		State:     state,
		UpdatedAt: now(),
		ID:        id,
	})
}

// ---------------------------------------------------------------------------
// BehavioralCheck CRUD
// ---------------------------------------------------------------------------

// InsertCheck inserts a new behavioral check in the pending state.
func (s *Store) InsertCheck(id, taskID, resourceID, behavior, checkJSON string) error {
	ts := now()
	return s.queries.InsertCheck(context.Background(), db.InsertCheckParams{
		ID:         id,
		TaskID:     taskID,
		ResourceID: resourceID,
		Behavior:   behavior,
		CheckJson:  checkJSON,
		CreatedAt:  ts,
		UpdatedAt:  ts,
	})
}

// GetCheck retrieves a check by ID. Returns ErrNotFound if absent.
func (s *Store) GetCheck(id string) (*BehavioralCheck, error) {
	c, err := s.queries.GetCheck(context.Background(), id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	out := dbCheckToBehavioralCheck(c)
	return &out, nil
}

// ListChecksByTask returns all checks for a task, ordered by created_at.
func (s *Store) ListChecksByTask(taskID string) ([]BehavioralCheck, error) {
	rows, err := s.queries.ListChecksByTask(context.Background(), taskID)
	if err != nil {
		return nil, err
	}
	out := make([]BehavioralCheck, len(rows))
	for i, r := range rows {
		out[i] = dbCheckToBehavioralCheck(r)
	}
	return out, nil
}

// ListChecksByResource returns all checks for a resource, ordered by created_at.
func (s *Store) ListChecksByResource(resourceID string) ([]BehavioralCheck, error) {
	rows, err := s.queries.ListChecksByResource(context.Background(), resourceID)
	if err != nil {
		return nil, err
	}
	out := make([]BehavioralCheck, len(rows))
	for i, r := range rows {
		out[i] = dbCheckToBehavioralCheck(r)
	}
	return out, nil
}

// DeleteChecksByResource deletes all checks for a resource.
func (s *Store) DeleteChecksByResource(resourceID string) error {
	return s.queries.DeleteChecksByResource(context.Background(), resourceID)
}

// DeleteTasksByResource deletes all tasks for a resource.
func (s *Store) DeleteTasksByResource(resourceID string) error {
	return s.queries.DeleteTasksByResource(context.Background(), resourceID)
}

// SetCheckState updates the state of a check.
func (s *Store) SetCheckState(id, state string) error {
	return s.queries.SetCheckState(context.Background(), db.SetCheckStateParams{
		State:     state,
		UpdatedAt: now(),
		ID:        id,
	})
}

// ---------------------------------------------------------------------------
// Verification CRUD
// ---------------------------------------------------------------------------

// InsertVerification inserts a new verification run record.
func (s *Store) InsertVerification(id, checkID, resourceID, realObsJSON, stubObsJSON, reason string, passed, theater bool) error {
	var passedInt, theaterInt int64
	if passed {
		passedInt = 1
	}
	if theater {
		theaterInt = 1
	}
	return s.queries.InsertVerification(context.Background(), db.InsertVerificationParams{
		ID:          id,
		CheckID:     checkID,
		ResourceID:  resourceID,
		RealObsJson: realObsJSON,
		StubObsJson: stubObsJSON,
		Passed:      passedInt,
		Theater:     theaterInt,
		Reason:      reason,
		CreatedAt:   now(),
	})
}

// ListVerificationsByCheck returns all verifications for a check, ordered by created_at.
func (s *Store) ListVerificationsByCheck(checkID string) ([]Verification, error) {
	rows, err := s.queries.ListVerificationsByCheck(context.Background(), checkID)
	if err != nil {
		return nil, err
	}
	out := make([]Verification, len(rows))
	for i, r := range rows {
		out[i] = dbVerificationToVerification(r)
	}
	return out, nil
}
