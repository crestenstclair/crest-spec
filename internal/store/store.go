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
	ApplyID     string
	PlanJSON    string
	WavesJSON   string
	HashesJSON  string
	CurrentWave int
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// InvariantCheck is the store's domain type for an invariant check record.
type InvariantCheck struct {
	ID         string
	ApplyID    string
	ResourceID string
	CheckType  string
	Passed     bool
	Output     string
	DurationMS int64
	CreatedAt  time.Time
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

func mapNotFound(err error) error {
	if err == sql.ErrNoRows {
		return cserrors.ErrNotFound
	}
	return err
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func parseOptionalTime(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return nil
	}
	return &t
}

func dbJobToJob(j db.Job) Job {
	return Job{
		ID:        j.ID,
		Tool:      j.Tool,
		Status:    j.Status,
		Result:    stringVal(j.Result),
		Error:     stringVal(j.Error),
		PID:       int(j.Pid),
		StartedAt: parseTime(j.StartedAt),
		DoneAt:    parseOptionalTime(j.DoneAt),
	}
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
		return nil, mapNotFound(err)
	}
	out := dbJobToJob(j)
	return &out, nil
}

func requireRowAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

func (s *Store) CompleteJob(id, result string) error {
	ts := now()
	res, err := s.queries.CompleteJob(context.Background(), db.CompleteJobParams{
		Result: &result, DoneAt: &ts, ID: id,
	})
	if err != nil {
		return err
	}
	return requireRowAffected(res)
}

func (s *Store) FailJob(id string, jobErr error) error {
	ts := now()
	errStr := jobErr.Error()
	res, err := s.queries.FailJob(context.Background(), db.FailJobParams{
		Error: &errStr, DoneAt: &ts, ID: id,
	})
	if err != nil {
		return err
	}
	return requireRowAffected(res)
}

func (s *Store) CancelJob(id string) error {
	ts := now()
	res, err := s.queries.CancelJob(context.Background(), db.CancelJobParams{
		DoneAt: &ts, ID: id,
	})
	if err != nil {
		return err
	}
	return requireRowAffected(res)
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
		return nil, mapNotFound(err)
	}
	out := Lock{
		Holder:     l.Holder,
		PID:        int(l.Pid),
		AcquiredAt: parseTime(l.AcquiredAt),
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// Resource/GeneratedFile/Dependency converters
// ---------------------------------------------------------------------------

func dbResourceToResource(r db.Resource) Resource {
	return Resource{
		ID:              r.ID,
		Kind:            r.Kind,
		ContextName:     stringVal(r.ContextName),
		DeclarationHash: r.DeclarationHash,
		EffectiveHash:   r.EffectiveHash,
		Model:           stringVal(r.Model),
		SettledAt:       parseTime(r.SettledAt),
	}
}

func dbGeneratedFileToGeneratedFile(f db.GeneratedFile) GeneratedFile {
	return GeneratedFile{
		Path:        f.Path,
		ResourceID:  f.ResourceID,
		ContentHash: f.ContentHash,
		PromptHash:  f.PromptHash,
		Model:       f.Model,
		CreatedAt:   parseTime(f.CreatedAt),
	}
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
		return nil, mapNotFound(err)
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
	return Apply{
		ID:        a.ID,
		Status:    a.Status,
		SpecHash:  a.SpecHash,
		StartedAt: parseTime(a.StartedAt),
		DoneAt:    parseOptionalTime(a.DoneAt),
	}
}

func dbApplyActionToApplyAction(a db.ApplyAction) ApplyAction {
	return ApplyAction{
		ID:         a.ID,
		ApplyID:    a.ApplyID,
		ResourceID: a.ResourceID,
		Action:     a.Action,
		Outcome:    stringVal(a.Outcome),
		Error:      stringVal(a.Error),
		StartedAt:  parseTime(a.StartedAt),
		DoneAt:     parseOptionalTime(a.DoneAt),
	}
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
		return nil, mapNotFound(err)
	}
	out := dbApplyToApply(a)
	return &out, nil
}

func (s *Store) CompleteApply(id string) error {
	ts := now()
	res, err := s.queries.CompleteApply(context.Background(), db.CompleteApplyParams{
		DoneAt: &ts, ID: id,
	})
	if err != nil {
		return err
	}
	return requireRowAffected(res)
}

func (s *Store) FailApply(id string) error {
	ts := now()
	res, err := s.queries.FailApply(context.Background(), db.FailApplyParams{
		DoneAt: &ts, ID: id,
	})
	if err != nil {
		return err
	}
	return requireRowAffected(res)
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
	return Generation{
		ID:              g.ID,
		ApplyID:         stringVal(g.ApplyID),
		ResourceID:      g.ResourceID,
		PromptText:      g.PromptText,
		PromptHash:      g.PromptHash,
		OutputText:      stringVal(g.OutputText),
		Model:           g.Model,
		Outcome:         stringVal(g.Outcome),
		RejectionReason: stringVal(g.RejectionReason),
		RetryCount:      int(g.RetryCount),
		DurationMS:      int64Val(g.DurationMs),
		InputTokens:     int64Val(g.InputTokens),
		OutputTokens:    int64Val(g.OutputTokens),
		CostUSD:         float64Val(g.CostUsd),
		CreatedAt:       parseTime(g.CreatedAt),
	}
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
	return Session{
		ID:          s.ID,
		ApplyID:     s.ApplyID,
		PlanJSON:    s.PlanJson,
		WavesJSON:   s.WavesJson,
		HashesJSON:  s.HashesJson,
		CurrentWave: int(s.CurrentWave),
		Status:      s.Status,
		CreatedAt:   parseTime(s.CreatedAt),
		UpdatedAt:   parseTime(s.UpdatedAt),
	}
}

// ---------------------------------------------------------------------------
// Session CRUD
// ---------------------------------------------------------------------------

// CreateSession inserts a new active agent session.
func (s *Store) CreateSession(sess Session) error {
	ts := now()
	return s.queries.CreateSession(context.Background(), db.CreateSessionParams{
		ID:         sess.ID,
		ApplyID:    sess.ApplyID,
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
		return nil, mapNotFound(err)
	}
	out := dbSessionToSession(sess)
	return &out, nil
}

func (s *Store) GetActiveSession() (*Session, error) {
	sess, err := s.queries.GetActiveSession(context.Background())
	if err != nil {
		return nil, mapNotFound(err)
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
	return AgentNote{
		ResourceID: n.ResourceID,
		ApplyID:    n.ApplyID,
		Content:    n.Content,
		CreatedAt:  parseTime(n.CreatedAt),
	}
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

// ---------------------------------------------------------------------------
// InvariantCheck CRUD
// ---------------------------------------------------------------------------

// RecordInvariantCheck inserts a new invariant check record.
func (s *Store) RecordInvariantCheck(ic InvariantCheck) error {
	var passedInt int64
	if ic.Passed {
		passedInt = 1
	}
	var details *string
	if ic.Output != "" {
		details = &ic.Output
	}
	return s.queries.CreateInvariantCheck(context.Background(), db.CreateInvariantCheckParams{
		ID:         ic.ID,
		ApplyID:    ic.ApplyID,
		ResourceID: ic.ResourceID,
		Invariant:  ic.CheckType,
		Passed:     passedInt,
		Details:    details,
		CheckedAt:  ic.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}

// ListInvariantChecks returns all invariant checks for an apply, ordered by checked_at.
func (s *Store) ListInvariantChecks(applyID string) ([]InvariantCheck, error) {
	rows, err := s.queries.ListInvariantChecks(context.Background(), applyID)
	if err != nil {
		return nil, err
	}
	out := make([]InvariantCheck, len(rows))
	for i, r := range rows {
		out[i] = dbInvariantCheckToInvariantCheck(r)
	}
	return out, nil
}

func dbInvariantCheckToInvariantCheck(ic db.InvariantCheck) InvariantCheck {
	return InvariantCheck{
		ID:         ic.ID,
		ApplyID:    ic.ApplyID,
		ResourceID: ic.ResourceID,
		CheckType:  ic.Invariant,
		Passed:     ic.Passed != 0,
		Output:     stringVal(ic.Details),
		CreatedAt:  parseTime(ic.CheckedAt),
	}
}

// ---------------------------------------------------------------------------
// SessionResource — per-resource state within a session
// ---------------------------------------------------------------------------

// SessionResource tracks a resource's state within an active session.
type SessionResource struct {
	SessionID  string
	ResourceID string
	State      string
	WaveIndex  int
	Attempts   int
	MaxRetries int
	LastError  string
	LastOutput string
	JobID      string
	UpdatedAt  time.Time
}

func dbSessionResourceToSessionResource(r db.SessionResource) SessionResource {
	return SessionResource{
		SessionID:  r.SessionID,
		ResourceID: r.ResourceID,
		State:      r.State,
		WaveIndex:  int(r.WaveIndex),
		Attempts:   int(r.Attempts),
		MaxRetries: int(r.MaxRetries),
		LastError:  stringVal(r.LastError),
		LastOutput: stringVal(r.LastOutput),
		JobID:      stringVal(r.JobID),
		UpdatedAt:  parseTime(r.UpdatedAt),
	}
}

func stringVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func int64Val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func float64Val(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// UpsertSessionResource creates or updates a resource state within a session.
func (s *Store) UpsertSessionResource(r SessionResource) error {
	return s.queries.UpsertSessionResource(context.Background(), db.UpsertSessionResourceParams{
		SessionID:  r.SessionID,
		ResourceID: r.ResourceID,
		State:      r.State,
		WaveIndex:  int64(r.WaveIndex),
		Attempts:   int64(r.Attempts),
		MaxRetries: int64(r.MaxRetries),
		LastError:  stringPtr(r.LastError),
		LastOutput: stringPtr(r.LastOutput),
		JobID:      stringPtr(r.JobID),
		UpdatedAt:  now(),
	})
}

// GetSessionResource retrieves a single resource state.
func (s *Store) GetSessionResource(sessionID, resourceID string) (*SessionResource, error) {
	r, err := s.queries.GetSessionResource(context.Background(), db.GetSessionResourceParams{
		SessionID:  sessionID,
		ResourceID: resourceID,
	})
	if err != nil {
		return nil, mapNotFound(err)
	}
	out := dbSessionResourceToSessionResource(r)
	return &out, nil
}

// ListSessionResources returns all resource states for a session.
func (s *Store) ListSessionResources(sessionID string) ([]SessionResource, error) {
	rows, err := s.queries.ListSessionResources(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionResource, len(rows))
	for i, r := range rows {
		out[i] = dbSessionResourceToSessionResource(r)
	}
	return out, nil
}

// ListSessionResourcesByWave returns resource states for a specific wave.
func (s *Store) ListSessionResourcesByWave(sessionID string, wave int) ([]SessionResource, error) {
	rows, err := s.queries.ListSessionResourcesByWave(context.Background(), db.ListSessionResourcesByWaveParams{
		SessionID: sessionID,
		WaveIndex: int64(wave),
	})
	if err != nil {
		return nil, err
	}
	out := make([]SessionResource, len(rows))
	for i, r := range rows {
		out[i] = dbSessionResourceToSessionResource(r)
	}
	return out, nil
}

// UpdateSessionResourceState transitions a resource's state.
func (s *Store) UpdateSessionResourceState(sessionID, resourceID, state, lastError, lastOutput string, attempts int, jobID string) error {
	return s.queries.UpdateSessionResourceState(context.Background(), db.UpdateSessionResourceStateParams{
		State:      state,
		LastError:  stringPtr(lastError),
		LastOutput: stringPtr(lastOutput),
		Attempts:   int64(attempts),
		JobID:      stringPtr(jobID),
		UpdatedAt:  now(),
		SessionID:  sessionID,
		ResourceID: resourceID,
	})
}

// ListSessionResourcesByState returns resources filtered by state.
func (s *Store) ListSessionResourcesByState(sessionID, state string) ([]SessionResource, error) {
	rows, err := s.queries.ListSessionResourcesByState(context.Background(), db.ListSessionResourcesByStateParams{
		SessionID: sessionID,
		State:     state,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SessionResource, len(rows))
	for i, r := range rows {
		out[i] = dbSessionResourceToSessionResource(r)
	}
	return out, nil
}

// DeleteSessionResources removes all resource state for a session.
func (s *Store) DeleteSessionResources(sessionID string) error {
	return s.queries.DeleteSessionResources(context.Background(), sessionID)
}

// ---------------------------------------------------------------------------
// Vacuum — compact old history
// ---------------------------------------------------------------------------

// Vacuum deletes generations, invariant checks, and apply records
// (along with their actions) that were created before the given time.
// Returns the total number of rows deleted across all tables.
func (s *Store) Vacuum(before time.Time) (int, error) {
	ts := before.UTC().Format(time.RFC3339Nano)
	total := 0

	tables := []string{
		"DELETE FROM generations WHERE created_at < ?",
		"DELETE FROM invariant_checks WHERE checked_at < ?",
		"DELETE FROM apply_actions WHERE started_at < ?",
		"DELETE FROM applies WHERE started_at < ?",
	}

	for _, stmt := range tables {
		res, err := s.sqlDB.Exec(stmt, ts)
		if err != nil {
			return total, fmt.Errorf("vacuum: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("vacuum rows affected: %w", err)
		}
		total += int(n)
	}

	return total, nil
}

// ---------------------------------------------------------------------------
// ReadOnlyQuery — execute arbitrary SELECT queries
// ---------------------------------------------------------------------------

// ReadOnlyQuery executes a read-only SQL query against the database.
// Only SELECT statements are allowed; any other statement is rejected.
// Returns the result rows as a slice of column-name-to-value maps.
func (s *Store) ReadOnlyQuery(query string) ([]map[string]interface{}, error) {
	trimmed := strings.TrimSpace(query)
	if len(trimmed) < 6 || strings.ToUpper(trimmed[:6]) != "SELECT" {
		return nil, fmt.Errorf("only SELECT queries are allowed")
	}

	rows, err := s.sqlDB.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}

	var results []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		row := make(map[string]interface{}, len(cols))
		for i, col := range cols {
			val := values[i]
			// Convert []byte to string for readability
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			row[col] = val
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	return results, nil
}
