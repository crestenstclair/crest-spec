package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

// ---------------------------------------------------------------------------
// Constructor + Migration Tests
// ---------------------------------------------------------------------------

func TestNew_AppliesMigrations(t *testing.T) {
	s := testStore(t)

	// Verify the jobs table exists by inserting and querying.
	_, err := s.sqlDB.Exec(`INSERT INTO jobs (id, tool, status, pid, started_at)
		VALUES ('test', 'tool', 'running', 1, '2024-01-01T00:00:00Z')`)
	require.NoError(t, err)

	var count int
	err = s.sqlDB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestNew_AllTablesExist(t *testing.T) {
	s := testStore(t)

	expected := []string{
		"jobs",
		"resources",
		"generated_files",
		"dependencies",
		"applies",
		"apply_actions",
		"generations",
		"invariant_checks",
		"agent_sessions",
		"agent_notes",
		"lock",
		"schema_migrations",
	}

	for _, table := range expected {
		var name string
		err := s.sqlDB.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		require.NoError(t, err, "table %q should exist", table)
		assert.Equal(t, table, name)
	}
}

func TestNew_MigrationsIdempotent(t *testing.T) {
	s := testStore(t)

	// Running migrate a second time should not error.
	err := s.migrate()
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Job CRUD Tests
// ---------------------------------------------------------------------------

func TestCreateJob_And_GetJob(t *testing.T) {
	s := testStore(t)

	before := time.Now().UTC().Add(-time.Second)
	err := s.CreateJob("j1", "generate", 42)
	require.NoError(t, err)

	j, err := s.GetJob("j1")
	require.NoError(t, err)

	assert.Equal(t, "j1", j.ID)
	assert.Equal(t, "generate", j.Tool)
	assert.Equal(t, "running", j.Status)
	assert.Equal(t, "", j.Result)
	assert.Equal(t, "", j.Error)
	assert.Equal(t, 42, j.PID)
	assert.True(t, j.StartedAt.After(before), "StartedAt should be recent")
	assert.Nil(t, j.DoneAt)
}

func TestGetJob_NotFound(t *testing.T) {
	s := testStore(t)

	_, err := s.GetJob("nonexistent")
	assert.ErrorIs(t, err, cserrors.ErrNotFound)
}

func TestCompleteJob(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("j1", "generate", 1))

	err := s.CompleteJob("j1", "some result")
	require.NoError(t, err)

	j, err := s.GetJob("j1")
	require.NoError(t, err)
	assert.Equal(t, "completed", j.Status)
	assert.Equal(t, "some result", j.Result)
	assert.NotNil(t, j.DoneAt)
}

func TestCompleteJob_AlreadyDone(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("j1", "generate", 1))
	require.NoError(t, s.CompleteJob("j1", "result"))

	err := s.CompleteJob("j1", "again")
	assert.ErrorIs(t, err, cserrors.ErrAlreadyDone)
}

func TestFailJob(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("j1", "generate", 1))

	err := s.FailJob("j1", fmt.Errorf("something broke"))
	require.NoError(t, err)

	j, err := s.GetJob("j1")
	require.NoError(t, err)
	assert.Equal(t, "failed", j.Status)
	assert.Equal(t, "something broke", j.Error)
	assert.NotNil(t, j.DoneAt)
}

func TestFailJob_AlreadyDone(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("j1", "generate", 1))
	require.NoError(t, s.CompleteJob("j1", "ok"))

	err := s.FailJob("j1", fmt.Errorf("too late"))
	assert.ErrorIs(t, err, cserrors.ErrAlreadyDone)
}

func TestCancelJob(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("j1", "generate", 1))

	err := s.CancelJob("j1")
	require.NoError(t, err)

	j, err := s.GetJob("j1")
	require.NoError(t, err)
	assert.Equal(t, "cancelled", j.Status)
	assert.NotNil(t, j.DoneAt)
}

func TestDeleteJob(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("j1", "generate", 1))

	err := s.DeleteJob("j1")
	require.NoError(t, err)

	j, err := s.GetJob("j1")
	require.NoError(t, err)
	assert.Equal(t, "deleted", j.Status)
}

func TestListJobs(t *testing.T) {
	s := testStore(t)

	// Create 3 jobs; delete one.
	require.NoError(t, s.CreateJob("j1", "tool1", 1))
	require.NoError(t, s.CreateJob("j2", "tool2", 2))
	require.NoError(t, s.CreateJob("j3", "tool3", 3))
	require.NoError(t, s.DeleteJob("j2"))

	// List all non-deleted (should be 2).
	jobs, err := s.ListJobs(10)
	require.NoError(t, err)
	assert.Len(t, jobs, 2)

	// Verify deleted job is excluded.
	for _, j := range jobs {
		assert.NotEqual(t, "deleted", j.Status)
	}

	// Respect limit.
	jobs, err = s.ListJobs(1)
	require.NoError(t, err)
	assert.Len(t, jobs, 1)
}

// ---------------------------------------------------------------------------
// CleanupOrphans + WaitForCompletion Tests
// ---------------------------------------------------------------------------

func TestCleanupOrphans(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.CreateJob("alive", "tool", 100))
	require.NoError(t, s.CreateJob("dead", "tool", 999))

	aliveFn := func(pid int) bool {
		return pid == 100
	}

	n, err := s.CleanupOrphans(aliveFn)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// The dead job should be failed.
	j, err := s.GetJob("dead")
	require.NoError(t, err)
	assert.Equal(t, "failed", j.Status)
	assert.Contains(t, j.Error, "orphan: owner process 999 is dead")

	// The alive job should still be running.
	j, err = s.GetJob("alive")
	require.NoError(t, err)
	assert.Equal(t, "running", j.Status)
}

func TestCleanupOrphans_NoRunningJobs(t *testing.T) {
	s := testStore(t)

	n, err := s.CleanupOrphans(func(int) bool { return true })
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestWaitForCompletion_AlreadyDone(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("j1", "tool", 1))
	require.NoError(t, s.CompleteJob("j1", "done"))

	ctx := context.Background()
	j, err := s.WaitForCompletion(ctx, "j1")
	require.NoError(t, err)
	assert.Equal(t, "completed", j.Status)
}

func TestWaitForCompletion_WaitsForCompletion(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("j1", "tool", 1))

	// Complete after a short delay.
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.CompleteJob("j1", "async result")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	j, err := s.WaitForCompletion(ctx, "j1")
	require.NoError(t, err)
	assert.Equal(t, "completed", j.Status)
	assert.Equal(t, "async result", j.Result)
}

func TestWaitForCompletion_RespectsContext(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("j1", "tool", 1))

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := s.WaitForCompletion(ctx, "j1")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWaitForCompletion_NotFound(t *testing.T) {
	s := testStore(t)

	ctx := context.Background()
	_, err := s.WaitForCompletion(ctx, "nonexistent")
	assert.ErrorIs(t, err, cserrors.ErrNotFound)
}

// ---------------------------------------------------------------------------
// Lock Tests
// ---------------------------------------------------------------------------

func TestAcquireLock(t *testing.T) {
	s := testStore(t)

	err := s.AcquireLock("agent-1", 42)
	require.NoError(t, err)

	l, err := s.GetLock()
	require.NoError(t, err)
	assert.Equal(t, "agent-1", l.Holder)
	assert.Equal(t, 42, l.PID)
}

func TestAcquireLock_AlreadyHeld(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.AcquireLock("agent-1", 1))

	err := s.AcquireLock("agent-2", 2)
	assert.ErrorIs(t, err, cserrors.ErrLocked)
}

func TestReleaseLock(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.AcquireLock("agent-1", 1))
	require.NoError(t, s.ReleaseLock())

	_, err := s.GetLock()
	assert.ErrorIs(t, err, cserrors.ErrNotFound)
}

func TestReleaseLock_NoLock(t *testing.T) {
	s := testStore(t)

	// Releasing when no lock is held should not error.
	err := s.ReleaseLock()
	assert.NoError(t, err)
}

func TestGetLock_NoLock(t *testing.T) {
	s := testStore(t)

	_, err := s.GetLock()
	assert.ErrorIs(t, err, cserrors.ErrNotFound)
}

func TestAcquireLock_AfterRelease(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.AcquireLock("agent-1", 1))
	require.NoError(t, s.ReleaseLock())

	// Should be able to re-acquire.
	err := s.AcquireLock("agent-2", 2)
	require.NoError(t, err)

	l, err := s.GetLock()
	require.NoError(t, err)
	assert.Equal(t, "agent-2", l.Holder)
	assert.Equal(t, 2, l.PID)
}
