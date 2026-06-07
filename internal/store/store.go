package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/crestenstclair/crest-spec/internal/db"
	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	"github.com/crestenstclair/crest-spec/migrations"
	_ "modernc.org/sqlite"
)

// Job is the store's clean domain type for a job record.
type Job struct {
	ID        string
	Tool      string
	Status    string
	Result    string
	Error     string
	PID       int
	StartedAt time.Time
	DoneAt    *time.Time
}

// Lock is the store's clean domain type for the apply lock.
type Lock struct {
	Holder     string
	PID        int
	AcquiredAt time.Time
}

// Resource is the store's domain type for a resource state record.
type Resource struct {
	ID              string
	Kind            string
	ContextName     string
	DeclarationHash string
	EffectiveHash   string
	Model           string
	SettledAt       time.Time
}

// GeneratedFile is the store's domain type for a generated file record.
type GeneratedFile struct {
	Path        string
	ResourceID  string
	ContentHash string
	PromptHash  string
	Model       string
	CreatedAt   time.Time
}

// Dependency is the store's domain type for a resource dependency edge.
type Dependency struct {
	SourceID string
	TargetID string
	Kind     string
}

// Apply is the store's domain type for a spec apply run.
type Apply struct {
	ID        string
	Status    string
	SpecHash  string
	StartedAt time.Time
	DoneAt    *time.Time
}

// ApplyAction is the store's domain type for an action taken during an apply.
type ApplyAction struct {
	ID         string
	ApplyID    string
	ResourceID string
	Action     string
	Outcome    string
	Error      string
	StartedAt  time.Time
	DoneAt     *time.Time
}

// Generation is the store's domain type for a single LLM generation attempt.
type Generation struct {
	ID              string
	ApplyID         string
	ResourceID      string
	PromptText      string
	PromptHash      string
	OutputText      string
	Model           string
	Outcome         string
	RejectionReason string
	RetryCount      int
	DurationMS      int64
	InputTokens     int64
	OutputTokens    int64
	CostUSD         float64
	CreatedAt       time.Time
}

// Session is the store's domain type for a spec engine agent session.
type Session struct {
	ID          string
	PlanJSON    string
	WavesJSON   string
	HashesJSON  string
	CurrentWave int
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AgentNote is the store's domain type for a per-resource note left by the agent.
type AgentNote struct {
	ResourceID string
	ApplyID    string
	Content    string
	CreatedAt  time.Time
}

// Store wraps a SQLite database and provides domain operations for
// jobs, locks, and migrations.
type Store struct {
	sqlDB   *sql.DB
	queries *db.Queries
}

// New opens a SQLite database at dbPath, configures it with WAL mode
// and busy timeout, runs migrations, and returns a ready Store.
func New(dbPath string) (*Store, error) {
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("exec %q: %w", p, err)
		}
	}

	queries := db.New(sqlDB)

	s := &Store{
		sqlDB:   sqlDB,
		queries: queries,
	}

	if err := s.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// migrate applies any pending SQL migration files from the embedded FS.
func (s *Store) migrate() error {
	_, err := s.sqlDB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read migration dir: %w", err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var count int
		err := s.sqlDB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE filename = ?", name).Scan(&count)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if count > 0 {
			continue
		}

		content, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := s.sqlDB.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("exec migration %s: %w", name, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_migrations (filename) VALUES (?)", name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}

	return nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.sqlDB.Close()
}

// ---------------------------------------------------------------------------
// Job CRUD
// ---------------------------------------------------------------------------

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func dbJobToJob(j db.Job) Job {
	out := Job{
		ID:     j.ID,
		Tool:   j.Tool,
		Status: j.Status,
		PID:    int(j.Pid),
	}
	if j.Result != nil {
		out.Result = *j.Result
	}
	if j.Error != nil {
		out.Error = *j.Error
	}
	if t, err := time.Parse(time.RFC3339Nano, j.StartedAt); err == nil {
		out.StartedAt = t
	}
	if j.DoneAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *j.DoneAt); err == nil {
			out.DoneAt = &t
		}
	}
	return out
}

// CreateJob inserts a new running job.
func (s *Store) CreateJob(id, tool string, pid int) error {
	return s.queries.CreateJob(context.Background(), db.CreateJobParams{
		ID:        id,
		Tool:      tool,
		Pid:       int64(pid),
		StartedAt: now(),
	})
}

// GetJob retrieves a job by ID. Returns ErrNotFound if the job does not exist.
func (s *Store) GetJob(id string) (*Job, error) {
	j, err := s.queries.GetJob(context.Background(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	out := dbJobToJob(j)
	return &out, nil
}

// CompleteJob marks a running job as completed with the given result.
// Returns ErrAlreadyDone if the job is not in running state.
func (s *Store) CompleteJob(id, result string) error {
	ts := now()
	res, err := s.queries.CompleteJob(context.Background(), db.CompleteJobParams{
		Result: &result,
		DoneAt: &ts,
		ID:     id,
	})
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

// FailJob marks a running job as failed with the given error.
// Returns ErrAlreadyDone if the job is not in running state.
func (s *Store) FailJob(id string, jobErr error) error {
	ts := now()
	errStr := jobErr.Error()
	res, err := s.queries.FailJob(context.Background(), db.FailJobParams{
		Error:  &errStr,
		DoneAt: &ts,
		ID:     id,
	})
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

// CancelJob marks a running job as cancelled.
// Returns ErrAlreadyDone if the job is not in running state.
func (s *Store) CancelJob(id string) error {
	ts := now()
	res, err := s.queries.CancelJob(context.Background(), db.CancelJobParams{
		DoneAt: &ts,
		ID:     id,
	})
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

// DeleteJob soft-deletes a job by setting its status to 'deleted'.
func (s *Store) DeleteJob(id string) error {
	ts := now()
	return s.queries.DeleteJob(context.Background(), db.DeleteJobParams{
		DoneAt: &ts,
		ID:     id,
	})
}

// ListJobs returns up to limit non-deleted jobs, ordered by started_at DESC.
func (s *Store) ListJobs(limit int) ([]Job, error) {
	rows, err := s.queries.ListJobs(context.Background(), int64(limit))
	if err != nil {
		return nil, err
	}
	out := make([]Job, len(rows))
	for i, r := range rows {
		out[i] = dbJobToJob(r)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// CleanupOrphans + WaitForCompletion
// ---------------------------------------------------------------------------

// CleanupOrphans checks all running jobs and fails any whose owner process
// is no longer alive (as determined by aliveFn). Returns the count of
// orphaned jobs that were cleaned up.
func (s *Store) CleanupOrphans(aliveFn func(int) bool) (int, error) {
	running, err := s.queries.ListRunningJobs(context.Background())
	if err != nil {
		return 0, err
	}

	cleaned := 0
	for _, j := range running {
		pid := int(j.Pid)
		if !aliveFn(pid) {
			errMsg := fmt.Sprintf("orphan: owner process %d is dead", pid)
			if err := s.FailJob(j.ID, fmt.Errorf("%s", errMsg)); err != nil {
				return cleaned, err
			}
			cleaned++
		}
	}
	return cleaned, nil
}

// WaitForCompletion polls for a job to leave the "running" state, using
// exponential backoff (100ms initial, doubling, capped at 2s).
// Returns the job once it is no longer running, or an error if the context
// is cancelled or the job is not found.
func (s *Store) WaitForCompletion(ctx context.Context, id string) (*Job, error) {
	delay := 100 * time.Millisecond
	const maxDelay = 2 * time.Second

	for {
		j, err := s.GetJob(id)
		if err != nil {
			return nil, err
		}
		if j.Status != "running" {
			return j, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}

		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

// ---------------------------------------------------------------------------
// Lock operations
// ---------------------------------------------------------------------------

// AcquireLock attempts to acquire the singleton apply lock.
// Returns ErrLocked if the lock is already held.
func (s *Store) AcquireLock(holder string, pid int) error {
	err := s.queries.InsertLock(context.Background(), db.InsertLockParams{
		Holder:     holder,
		Pid:        int64(pid),
		AcquiredAt: now(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") ||
			strings.Contains(err.Error(), "PRIMARY KEY constraint failed") {
			return cserrors.ErrLocked
		}
		return err
	}
	return nil
}

// ReleaseLock releases the singleton apply lock.
func (s *Store) ReleaseLock() error {
	return s.queries.DeleteLock(context.Background())
}

// GetLock retrieves the current lock holder. Returns ErrNotFound if no lock is held.
func (s *Store) GetLock() (*Lock, error) {
	l, err := s.queries.GetLock(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	out := Lock{
		Holder: l.Holder,
		PID:    int(l.Pid),
	}
	if t, err := time.Parse(time.RFC3339Nano, l.AcquiredAt); err == nil {
		out.AcquiredAt = t
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// Resource/GeneratedFile/Dependency converters
// ---------------------------------------------------------------------------

func dbResourceToResource(r db.Resource) Resource {
	out := Resource{
		ID:              r.ID,
		Kind:            r.Kind,
		DeclarationHash: r.DeclarationHash,
		EffectiveHash:   r.EffectiveHash,
	}
	if r.ContextName != nil {
		out.ContextName = *r.ContextName
	}
	if r.Model != nil {
		out.Model = *r.Model
	}
	if t, err := time.Parse(time.RFC3339Nano, r.SettledAt); err == nil {
		out.SettledAt = t
	}
	return out
}

func dbGeneratedFileToGeneratedFile(f db.GeneratedFile) GeneratedFile {
	out := GeneratedFile{
		Path:        f.Path,
		ResourceID:  f.ResourceID,
		ContentHash: f.ContentHash,
		PromptHash:  f.PromptHash,
		Model:       f.Model,
	}
	if t, err := time.Parse(time.RFC3339Nano, f.CreatedAt); err == nil {
		out.CreatedAt = t
	}
	return out
}

func dbDependencyToDependency(d db.Dependency) Dependency {
	return Dependency{
		SourceID: d.SourceID,
		TargetID: d.TargetID,
		Kind:     d.Kind,
	}
}

// ---------------------------------------------------------------------------
// Resource CRUD
// ---------------------------------------------------------------------------

func (s *Store) GetResource(id string) (*Resource, error) {
	r, err := s.queries.GetResource(context.Background(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	out := dbResourceToResource(r)
	return &out, nil
}

func (s *Store) ListResources() ([]Resource, error) {
	rows, err := s.queries.ListResources(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]Resource, len(rows))
	for i, r := range rows {
		out[i] = dbResourceToResource(r)
	}
	return out, nil
}

func (s *Store) SetResource(r Resource) error {
	var contextName *string
	if r.ContextName != "" {
		contextName = &r.ContextName
	}
	var model *string
	if r.Model != "" {
		model = &r.Model
	}
	return s.queries.SetResource(context.Background(), db.SetResourceParams{
		ID:              r.ID,
		Kind:            r.Kind,
		ContextName:     contextName,
		DeclarationHash: r.DeclarationHash,
		EffectiveHash:   r.EffectiveHash,
		Model:           model,
		SettledAt:       r.SettledAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *Store) DeleteResource(id string) error {
	return s.queries.DeleteResource(context.Background(), id)
}

// ---------------------------------------------------------------------------
// GeneratedFile CRUD
// ---------------------------------------------------------------------------

func (s *Store) GetGeneratedFiles(resourceID string) ([]GeneratedFile, error) {
	rows, err := s.queries.GetGeneratedFiles(context.Background(), resourceID)
	if err != nil {
		return nil, err
	}
	out := make([]GeneratedFile, len(rows))
	for i, f := range rows {
		out[i] = dbGeneratedFileToGeneratedFile(f)
	}
	return out, nil
}

func (s *Store) SetGeneratedFile(f GeneratedFile) error {
	return s.queries.SetGeneratedFile(context.Background(), db.SetGeneratedFileParams{
		Path:        f.Path,
		ResourceID:  f.ResourceID,
		ContentHash: f.ContentHash,
		PromptHash:  f.PromptHash,
		Model:       f.Model,
		CreatedAt:   f.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *Store) DeleteGeneratedFiles(resourceID string) error {
	return s.queries.DeleteGeneratedFiles(context.Background(), resourceID)
}

// ---------------------------------------------------------------------------
// Dependency CRUD
// ---------------------------------------------------------------------------

func (s *Store) SetDependency(sourceID, targetID, kind string) error {
	return s.queries.SetDependency(context.Background(), db.SetDependencyParams{
		SourceID: sourceID,
		TargetID: targetID,
		Kind:     kind,
	})
}

func (s *Store) GetDependencies(sourceID string) ([]Dependency, error) {
	rows, err := s.queries.GetDependencies(context.Background(), sourceID)
	if err != nil {
		return nil, err
	}
	out := make([]Dependency, len(rows))
	for i, d := range rows {
		out[i] = dbDependencyToDependency(d)
	}
	return out, nil
}

func (s *Store) DeleteDependencies(sourceID string) error {
	return s.queries.DeleteDependencies(context.Background(), sourceID)
}

// ---------------------------------------------------------------------------
// Apply converters
// ---------------------------------------------------------------------------

func dbApplyToApply(a db.Apply) Apply {
	out := Apply{
		ID:       a.ID,
		Status:   a.Status,
		SpecHash: a.SpecHash,
	}
	if t, err := time.Parse(time.RFC3339Nano, a.StartedAt); err == nil {
		out.StartedAt = t
	}
	if a.DoneAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *a.DoneAt); err == nil {
			out.DoneAt = &t
		}
	}
	return out
}

func dbApplyActionToApplyAction(a db.ApplyAction) ApplyAction {
	out := ApplyAction{
		ID:         a.ID,
		ApplyID:    a.ApplyID,
		ResourceID: a.ResourceID,
		Action:     a.Action,
	}
	if a.Outcome != nil {
		out.Outcome = *a.Outcome
	}
	if a.Error != nil {
		out.Error = *a.Error
	}
	if t, err := time.Parse(time.RFC3339Nano, a.StartedAt); err == nil {
		out.StartedAt = t
	}
	if a.DoneAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *a.DoneAt); err == nil {
			out.DoneAt = &t
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Apply CRUD
// ---------------------------------------------------------------------------

// CreateApply inserts a new running apply record.
func (s *Store) CreateApply(id, specHash string) error {
	return s.queries.CreateApply(context.Background(), db.CreateApplyParams{
		ID:        id,
		SpecHash:  specHash,
		StartedAt: now(),
	})
}

// GetApply retrieves an apply by ID. Returns ErrNotFound if not found.
func (s *Store) GetApply(id string) (*Apply, error) {
	a, err := s.queries.GetApply(context.Background(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	out := dbApplyToApply(a)
	return &out, nil
}

// CompleteApply marks a running apply as completed.
// Returns ErrAlreadyDone if the apply is not in running state.
func (s *Store) CompleteApply(id string) error {
	ts := now()
	res, err := s.queries.CompleteApply(context.Background(), db.CompleteApplyParams{
		DoneAt: &ts,
		ID:     id,
	})
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

// FailApply marks a running apply as failed.
// Returns ErrAlreadyDone if the apply is not in running state.
func (s *Store) FailApply(id string) error {
	ts := now()
	res, err := s.queries.FailApply(context.Background(), db.FailApplyParams{
		DoneAt: &ts,
		ID:     id,
	})
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

// ListApplies returns up to limit applies ordered by started_at DESC.
func (s *Store) ListApplies(limit int) ([]Apply, error) {
	rows, err := s.queries.ListApplies(context.Background(), int64(limit))
	if err != nil {
		return nil, err
	}
	out := make([]Apply, len(rows))
	for i, r := range rows {
		out[i] = dbApplyToApply(r)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// ApplyAction CRUD
// ---------------------------------------------------------------------------

// CreateApplyAction inserts a new apply action record.
func (s *Store) CreateApplyAction(id, applyID, resourceID, action string) error {
	return s.queries.CreateApplyAction(context.Background(), db.CreateApplyActionParams{
		ID:         id,
		ApplyID:    applyID,
		ResourceID: resourceID,
		Action:     action,
		StartedAt:  now(),
	})
}

// UpdateApplyAction sets the outcome and optional error on an apply action.
func (s *Store) UpdateApplyAction(id, outcome, errMsg string) error {
	ts := now()
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	return s.queries.UpdateApplyAction(context.Background(), db.UpdateApplyActionParams{
		Outcome: &outcome,
		Error:   errPtr,
		DoneAt:  &ts,
		ID:      id,
	})
}

// ListApplyActions returns all actions for an apply, ordered by started_at.
func (s *Store) ListApplyActions(applyID string) ([]ApplyAction, error) {
	rows, err := s.queries.ListApplyActions(context.Background(), applyID)
	if err != nil {
		return nil, err
	}
	out := make([]ApplyAction, len(rows))
	for i, r := range rows {
		out[i] = dbApplyActionToApplyAction(r)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Generation converter
// ---------------------------------------------------------------------------

func dbGenerationToGeneration(g db.Generation) Generation {
	out := Generation{
		ID:         g.ID,
		ResourceID: g.ResourceID,
		PromptText: g.PromptText,
		PromptHash: g.PromptHash,
		Model:      g.Model,
		RetryCount: int(g.RetryCount),
	}
	if g.ApplyID != nil {
		out.ApplyID = *g.ApplyID
	}
	if g.OutputText != nil {
		out.OutputText = *g.OutputText
	}
	if g.Outcome != nil {
		out.Outcome = *g.Outcome
	}
	if g.RejectionReason != nil {
		out.RejectionReason = *g.RejectionReason
	}
	if g.DurationMs != nil {
		out.DurationMS = *g.DurationMs
	}
	if g.InputTokens != nil {
		out.InputTokens = *g.InputTokens
	}
	if g.OutputTokens != nil {
		out.OutputTokens = *g.OutputTokens
	}
	if g.CostUsd != nil {
		out.CostUSD = *g.CostUsd
	}
	if t, err := time.Parse(time.RFC3339Nano, g.CreatedAt); err == nil {
		out.CreatedAt = t
	}
	return out
}

// ---------------------------------------------------------------------------
// Generation CRUD
// ---------------------------------------------------------------------------

// CreateGeneration inserts a new generation record.
func (s *Store) CreateGeneration(g Generation) error {
	var applyID *string
	if g.ApplyID != "" {
		applyID = &g.ApplyID
	}
	return s.queries.CreateGeneration(context.Background(), db.CreateGenerationParams{
		ID:         g.ID,
		ApplyID:    applyID,
		ResourceID: g.ResourceID,
		PromptText: g.PromptText,
		PromptHash: g.PromptHash,
		Model:      g.Model,
		RetryCount: int64(g.RetryCount),
		CreatedAt:  now(),
	})
}

// UpdateGeneration sets the output and metrics on a generation record.
func (s *Store) UpdateGeneration(id, outputText, outcome, rejectionReason string, durationMS, inputTokens, outputTokens int64, costUSD float64) error {
	var outputPtr *string
	if outputText != "" {
		outputPtr = &outputText
	}
	var outcomePtr *string
	if outcome != "" {
		outcomePtr = &outcome
	}
	var rejectionPtr *string
	if rejectionReason != "" {
		rejectionPtr = &rejectionReason
	}
	return s.queries.UpdateGeneration(context.Background(), db.UpdateGenerationParams{
		OutputText:      outputPtr,
		Outcome:         outcomePtr,
		RejectionReason: rejectionPtr,
		DurationMs:      &durationMS,
		InputTokens:     &inputTokens,
		OutputTokens:    &outputTokens,
		CostUsd:         &costUSD,
		ID:              id,
	})
}

// ListGenerations returns up to limit generations for a resource, ordered by created_at DESC.
func (s *Store) ListGenerations(resourceID string, limit int) ([]Generation, error) {
	rows, err := s.queries.ListGenerations(context.Background(), db.ListGenerationsParams{
		ResourceID: resourceID,
		Limit:      int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Generation, len(rows))
	for i, r := range rows {
		out[i] = dbGenerationToGeneration(r)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Session converter
// ---------------------------------------------------------------------------

func dbSessionToSession(s db.AgentSession) Session {
	out := Session{
		ID:          s.ID,
		PlanJSON:    s.PlanJson,
		WavesJSON:   s.WavesJson,
		HashesJSON:  s.HashesJson,
		CurrentWave: int(s.CurrentWave),
		Status:      s.Status,
	}
	if t, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err == nil {
		out.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, s.UpdatedAt); err == nil {
		out.UpdatedAt = t
	}
	return out
}

// ---------------------------------------------------------------------------
// Session CRUD
// ---------------------------------------------------------------------------

// CreateSession inserts a new active agent session.
func (s *Store) CreateSession(sess Session) error {
	ts := now()
	return s.queries.CreateSession(context.Background(), db.CreateSessionParams{
		ID:         sess.ID,
		PlanJson:   sess.PlanJSON,
		WavesJson:  sess.WavesJSON,
		HashesJson: sess.HashesJSON,
		CreatedAt:  ts,
		UpdatedAt:  ts,
	})
}

// GetSession retrieves a session by ID. Returns ErrNotFound if not found.
func (s *Store) GetSession(id string) (*Session, error) {
	sess, err := s.queries.GetSession(context.Background(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	out := dbSessionToSession(sess)
	return &out, nil
}

// GetActiveSession returns the current active session. Returns ErrNotFound if none.
func (s *Store) GetActiveSession() (*Session, error) {
	sess, err := s.queries.GetActiveSession(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	out := dbSessionToSession(sess)
	return &out, nil
}

// UpdateSession sets the status and current wave on a session.
func (s *Store) UpdateSession(id, status string, currentWave int) error {
	return s.queries.UpdateSessionStatus(context.Background(), db.UpdateSessionStatusParams{
		Status:      status,
		CurrentWave: int64(currentWave),
		UpdatedAt:   now(),
		ID:          id,
	})
}

// ---------------------------------------------------------------------------
// Note converter
// ---------------------------------------------------------------------------

func dbNoteToAgentNote(n db.AgentNote) AgentNote {
	out := AgentNote{
		ResourceID: n.ResourceID,
		ApplyID:    n.ApplyID,
		Content:    n.Content,
	}
	if t, err := time.Parse(time.RFC3339Nano, n.CreatedAt); err == nil {
		out.CreatedAt = t
	}
	return out
}

// ---------------------------------------------------------------------------
// Note CRUD
// ---------------------------------------------------------------------------

// SetNote upserts a note for a resource+apply combination.
func (s *Store) SetNote(resourceID, applyID, content string) error {
	return s.queries.CreateNote(context.Background(), db.CreateNoteParams{
		ResourceID: resourceID,
		ApplyID:    applyID,
		Content:    content,
		CreatedAt:  now(),
	})
}

// GetNote retrieves the content of a note. Returns ("", nil) if not found.
func (s *Store) GetNote(resourceID, applyID string) (string, error) {
	n, err := s.queries.GetNote(context.Background(), db.GetNoteParams{
		ResourceID: resourceID,
		ApplyID:    applyID,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return n.Content, nil
}

// ListNotes returns all notes for an apply.
func (s *Store) ListNotes(applyID string) ([]AgentNote, error) {
	rows, err := s.queries.ListNotes(context.Background(), applyID)
	if err != nil {
		return nil, err
	}
	out := make([]AgentNote, len(rows))
	for i, r := range rows {
		out[i] = dbNoteToAgentNote(r)
	}
	return out, nil
}
