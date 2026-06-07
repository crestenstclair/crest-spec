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
