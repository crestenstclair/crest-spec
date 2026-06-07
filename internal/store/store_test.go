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

// ---------------------------------------------------------------------------
// Resource CRUD Tests
// ---------------------------------------------------------------------------

func TestSetResource_And_GetResource(t *testing.T) {
	s := testStore(t)
	r := Resource{
		ID: "aggregate.Synth.Voice", Kind: "aggregate", ContextName: "Synth",
		DeclarationHash: "abc123", EffectiveHash: "def456", Model: "opus",
		SettledAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, s.SetResource(r))
	got, err := s.GetResource("aggregate.Synth.Voice")
	require.NoError(t, err)
	assert.Equal(t, r.ID, got.ID)
	assert.Equal(t, r.Kind, got.Kind)
	assert.Equal(t, r.ContextName, got.ContextName)
	assert.Equal(t, r.DeclarationHash, got.DeclarationHash)
	assert.Equal(t, r.EffectiveHash, got.EffectiveHash)
	assert.Equal(t, r.Model, got.Model)
}

func TestGetResource_NotFound(t *testing.T) {
	s := testStore(t)
	_, err := s.GetResource("nonexistent")
	assert.ErrorIs(t, err, cserrors.ErrNotFound)
}

func TestSetResource_Upsert(t *testing.T) {
	s := testStore(t)
	r := Resource{ID: "aggregate.Synth.Voice", Kind: "aggregate", ContextName: "Synth",
		DeclarationHash: "abc123", EffectiveHash: "def456", Model: "opus",
		SettledAt: time.Now().UTC().Truncate(time.Millisecond)}
	require.NoError(t, s.SetResource(r))
	r.DeclarationHash = "updated"
	r.EffectiveHash = "updated2"
	require.NoError(t, s.SetResource(r))
	got, err := s.GetResource("aggregate.Synth.Voice")
	require.NoError(t, err)
	assert.Equal(t, "updated", got.DeclarationHash)
	assert.Equal(t, "updated2", got.EffectiveHash)
}

func TestListResources(t *testing.T) {
	s := testStore(t)
	r1 := Resource{ID: "aggregate.A.X", Kind: "aggregate", ContextName: "A", DeclarationHash: "h1", EffectiveHash: "e1", Model: "opus", SettledAt: time.Now().UTC()}
	r2 := Resource{ID: "aggregate.B.Y", Kind: "aggregate", ContextName: "B", DeclarationHash: "h2", EffectiveHash: "e2", Model: "opus", SettledAt: time.Now().UTC()}
	require.NoError(t, s.SetResource(r1))
	require.NoError(t, s.SetResource(r2))
	resources, err := s.ListResources()
	require.NoError(t, err)
	assert.Len(t, resources, 2)
	assert.Equal(t, "aggregate.A.X", resources[0].ID)
	assert.Equal(t, "aggregate.B.Y", resources[1].ID)
}

func TestDeleteResource(t *testing.T) {
	s := testStore(t)
	r := Resource{ID: "aggregate.Synth.Voice", Kind: "aggregate", ContextName: "Synth", DeclarationHash: "h", EffectiveHash: "e", Model: "opus", SettledAt: time.Now().UTC()}
	require.NoError(t, s.SetResource(r))
	require.NoError(t, s.DeleteResource("aggregate.Synth.Voice"))
	_, err := s.GetResource("aggregate.Synth.Voice")
	assert.ErrorIs(t, err, cserrors.ErrNotFound)
}

// ---------------------------------------------------------------------------
// GeneratedFile CRUD Tests
// ---------------------------------------------------------------------------

func TestSetGeneratedFile_And_GetGeneratedFiles(t *testing.T) {
	s := testStore(t)
	r := Resource{ID: "aggregate.Synth.Voice", Kind: "aggregate", ContextName: "Synth", DeclarationHash: "h", EffectiveHash: "e", Model: "opus", SettledAt: time.Now().UTC()}
	require.NoError(t, s.SetResource(r))
	f := GeneratedFile{Path: "src/voice.go", ResourceID: "aggregate.Synth.Voice", ContentHash: "content123", PromptHash: "prompt123", Model: "opus", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	require.NoError(t, s.SetGeneratedFile(f))
	files, err := s.GetGeneratedFiles("aggregate.Synth.Voice")
	require.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Equal(t, "src/voice.go", files[0].Path)
	assert.Equal(t, "content123", files[0].ContentHash)
}

func TestGetGeneratedFiles_Empty(t *testing.T) {
	s := testStore(t)
	files, err := s.GetGeneratedFiles("nonexistent")
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestDeleteGeneratedFiles(t *testing.T) {
	s := testStore(t)
	r := Resource{ID: "aggregate.Synth.Voice", Kind: "aggregate", ContextName: "Synth", DeclarationHash: "h", EffectiveHash: "e", Model: "opus", SettledAt: time.Now().UTC()}
	require.NoError(t, s.SetResource(r))
	f := GeneratedFile{Path: "src/voice.go", ResourceID: "aggregate.Synth.Voice", ContentHash: "c", PromptHash: "p", Model: "opus", CreatedAt: time.Now().UTC()}
	require.NoError(t, s.SetGeneratedFile(f))
	require.NoError(t, s.DeleteGeneratedFiles("aggregate.Synth.Voice"))
	files, err := s.GetGeneratedFiles("aggregate.Synth.Voice")
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestDeleteResource_CascadesToGeneratedFiles(t *testing.T) {
	s := testStore(t)
	r := Resource{ID: "aggregate.Synth.Voice", Kind: "aggregate", ContextName: "Synth", DeclarationHash: "h", EffectiveHash: "e", Model: "opus", SettledAt: time.Now().UTC()}
	require.NoError(t, s.SetResource(r))
	f := GeneratedFile{Path: "src/voice.go", ResourceID: "aggregate.Synth.Voice", ContentHash: "c", PromptHash: "p", Model: "opus", CreatedAt: time.Now().UTC()}
	require.NoError(t, s.SetGeneratedFile(f))
	require.NoError(t, s.DeleteResource("aggregate.Synth.Voice"))
	files, err := s.GetGeneratedFiles("aggregate.Synth.Voice")
	require.NoError(t, err)
	assert.Empty(t, files)
}

// ---------------------------------------------------------------------------
// Dependency CRUD Tests
// ---------------------------------------------------------------------------

func TestSetDependency_And_GetDependencies(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.SetDependency("aggregate.Synth.Voice", "aggregate.Synth.Oscillator", "uses"))
	deps, err := s.GetDependencies("aggregate.Synth.Voice")
	require.NoError(t, err)
	assert.Len(t, deps, 1)
	assert.Equal(t, "aggregate.Synth.Voice", deps[0].SourceID)
	assert.Equal(t, "aggregate.Synth.Oscillator", deps[0].TargetID)
	assert.Equal(t, "uses", deps[0].Kind)
}

func TestSetDependency_Idempotent(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.SetDependency("a", "b", "uses"))
	require.NoError(t, s.SetDependency("a", "b", "uses"))
	deps, err := s.GetDependencies("a")
	require.NoError(t, err)
	assert.Len(t, deps, 1)
}

func TestDeleteDependencies(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.SetDependency("a", "b", "uses"))
	require.NoError(t, s.SetDependency("a", "c", "uses"))
	require.NoError(t, s.DeleteDependencies("a"))
	deps, err := s.GetDependencies("a")
	require.NoError(t, err)
	assert.Empty(t, deps)
}

func TestGetDependencies_Empty(t *testing.T) {
	s := testStore(t)
	deps, err := s.GetDependencies("nonexistent")
	require.NoError(t, err)
	assert.Empty(t, deps)
}

// ---------------------------------------------------------------------------
// Apply CRUD Tests
// ---------------------------------------------------------------------------

func TestApplyCRUD(t *testing.T) {
	s := testStore(t)
	err := s.CreateApply("apply-1", "hash-abc")
	require.NoError(t, err)
	a, err := s.GetApply("apply-1")
	require.NoError(t, err)
	assert.Equal(t, "running", a.Status)
	assert.Equal(t, "hash-abc", a.SpecHash)
	err = s.CompleteApply("apply-1")
	require.NoError(t, err)
	a, err = s.GetApply("apply-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", a.Status)
	assert.NotNil(t, a.DoneAt)
	err = s.CompleteApply("apply-1")
	assert.ErrorIs(t, err, cserrors.ErrAlreadyDone)
}

func TestApplyActionCRUD(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateApply("apply-1", "hash"))
	err := s.CreateApplyAction("action-1", "apply-1", "aggregate.Synth.Voice", "create")
	require.NoError(t, err)
	err = s.UpdateApplyAction("action-1", "committed", "")
	require.NoError(t, err)
	actions, err := s.ListApplyActions("apply-1")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "committed", actions[0].Outcome)
}

func TestGenerationCRUD(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateApply("apply-1", "hash"))
	gen := Generation{
		ID: "gen-1", ApplyID: "apply-1", ResourceID: "aggregate.Synth.Voice",
		PromptText: "generate voice", PromptHash: "phash", Model: "claude-sonnet-4-6",
	}
	err := s.CreateGeneration(gen)
	require.NoError(t, err)
	err = s.UpdateGeneration("gen-1", "code output", "accepted", "", 1500, 100, 200, 0.01)
	require.NoError(t, err)
	gens, err := s.ListGenerations("aggregate.Synth.Voice", 10)
	require.NoError(t, err)
	require.Len(t, gens, 1)
	assert.Equal(t, "accepted", gens[0].Outcome)
	assert.Equal(t, int64(1500), gens[0].DurationMS)
}

func TestSessionCRUD(t *testing.T) {
	s := testStore(t)
	sess := Session{
		ID: "sess-1", PlanJSON: `[{"id":"a"}]`, WavesJSON: `[["a"]]`, HashesJSON: `{"a":"h1"}`,
	}
	err := s.CreateSession(sess)
	require.NoError(t, err)
	got, err := s.GetSession("sess-1")
	require.NoError(t, err)
	assert.Equal(t, "active", got.Status)
	got, err = s.GetActiveSession()
	require.NoError(t, err)
	assert.Equal(t, "sess-1", got.ID)
	err = s.UpdateSession("sess-1", "completed", 2)
	require.NoError(t, err)
	got, err = s.GetSession("sess-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", got.Status)
	assert.Equal(t, 2, got.CurrentWave)
}

func TestNoteCRUD(t *testing.T) {
	s := testStore(t)
	err := s.SetNote("aggregate.Synth.Voice", "apply-1", "used newtype wrappers")
	require.NoError(t, err)
	content, err := s.GetNote("aggregate.Synth.Voice", "apply-1")
	require.NoError(t, err)
	assert.Equal(t, "used newtype wrappers", content)
	content, err = s.GetNote("nonexistent", "apply-1")
	require.NoError(t, err)
	assert.Equal(t, "", content)
	err = s.SetNote("aggregate.Synth.Voice", "apply-1", "updated note")
	require.NoError(t, err)
	content, err = s.GetNote("aggregate.Synth.Voice", "apply-1")
	require.NoError(t, err)
	assert.Equal(t, "updated note", content)
	notes, err := s.ListNotes("apply-1")
	require.NoError(t, err)
	require.Len(t, notes, 1)
}

func TestListApplies(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateApply("a1", "h1"))
	require.NoError(t, s.CreateApply("a2", "h2"))
	applies, err := s.ListApplies(10)
	require.NoError(t, err)
	assert.Len(t, applies, 2)
}

func TestFailApply(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateApply("a1", "h1"))
	err := s.FailApply("a1")
	require.NoError(t, err)
	a, err := s.GetApply("a1")
	require.NoError(t, err)
	assert.Equal(t, "failed", a.Status)
}
