package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Design round-trips
// ---------------------------------------------------------------------------

func TestUpsertDesign_InsertAndGet(t *testing.T) {
	s := testStore(t)

	err := s.UpsertDesign("auth", "abc123", `{"context":"auth"}`)
	require.NoError(t, err)

	d, err := s.GetDesign("auth")
	require.NoError(t, err)
	assert.Equal(t, "auth", d.ContextName)
	assert.Equal(t, "abc123", d.SpecHash)
	assert.Equal(t, `{"context":"auth"}`, d.ContractJSON)
	assert.False(t, d.CreatedAt.IsZero())
	assert.False(t, d.UpdatedAt.IsZero())
}

func TestUpsertDesign_UpdateExisting(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.UpsertDesign("auth", "v1", `{"v":1}`))
	require.NoError(t, s.UpsertDesign("auth", "v2", `{"v":2}`))

	d, err := s.GetDesign("auth")
	require.NoError(t, err)
	assert.Equal(t, "v2", d.SpecHash)
	assert.Equal(t, `{"v":2}`, d.ContractJSON)
}

func TestListDesigns(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.UpsertDesign("billing", "", "{}"))
	require.NoError(t, s.UpsertDesign("auth", "", "{}"))

	designs, err := s.ListDesigns()
	require.NoError(t, err)
	assert.Len(t, designs, 2)
	// ordered by context_name ASC
	assert.Equal(t, "auth", designs[0].ContextName)
	assert.Equal(t, "billing", designs[1].ContextName)
}

func TestGetDesign_NotFound(t *testing.T) {
	s := testStore(t)
	_, err := s.GetDesign("missing")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// BehavioralTask round-trips
// ---------------------------------------------------------------------------

func TestInsertTask_GetTask(t *testing.T) {
	s := testStore(t)

	err := s.InsertTask("task-1", "resource-A", "auth", "abc", "implement login")
	require.NoError(t, err)

	task, err := s.GetTask("task-1")
	require.NoError(t, err)
	assert.Equal(t, "task-1", task.ID)
	assert.Equal(t, "resource-A", task.ResourceID)
	assert.Equal(t, "auth", task.ContextName)
	assert.Equal(t, "abc", task.SpecHash)
	assert.Equal(t, "implement login", task.Description)
	assert.Equal(t, "pending", task.State)
}

func TestListTasksByResource(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.InsertTask("t1", "res-A", "", "", "first"))
	require.NoError(t, s.InsertTask("t2", "res-A", "", "", "second"))
	require.NoError(t, s.InsertTask("t3", "res-B", "", "", "other"))

	tasks, err := s.ListTasksByResource("res-A")
	require.NoError(t, err)
	assert.Len(t, tasks, 2)
}

func TestSetTaskState(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.InsertTask("t1", "res", "", "", "desc"))
	require.NoError(t, s.SetTaskState("t1", "generated"))

	task, err := s.GetTask("t1")
	require.NoError(t, err)
	assert.Equal(t, "generated", task.State)
}

// ---------------------------------------------------------------------------
// BehavioralCheck round-trips
// ---------------------------------------------------------------------------

func TestInsertCheck_GetCheck(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.InsertTask("t1", "res", "", "", "desc"))
	err := s.InsertCheck("c1", "t1", "res", "fast response", `{"behavior":"fast response","predicates":[]}`)
	require.NoError(t, err)

	c, err := s.GetCheck("c1")
	require.NoError(t, err)
	assert.Equal(t, "c1", c.ID)
	assert.Equal(t, "t1", c.TaskID)
	assert.Equal(t, "res", c.ResourceID)
	assert.Equal(t, "fast response", c.Behavior)
	assert.Equal(t, "pending", c.State)
}

func TestListChecksByTask(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.InsertTask("t1", "res", "", "", "desc"))
	require.NoError(t, s.InsertCheck("c1", "t1", "res", "b1", `{}`))
	require.NoError(t, s.InsertCheck("c2", "t1", "res", "b2", `{}`))

	checks, err := s.ListChecksByTask("t1")
	require.NoError(t, err)
	assert.Len(t, checks, 2)
}

func TestListChecksByResource(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.InsertTask("t1", "res-A", "", "", ""))
	require.NoError(t, s.InsertTask("t2", "res-B", "", "", ""))
	require.NoError(t, s.InsertCheck("c1", "t1", "res-A", "b1", `{}`))
	require.NoError(t, s.InsertCheck("c2", "t1", "res-A", "b2", `{}`))
	require.NoError(t, s.InsertCheck("c3", "t2", "res-B", "b3", `{}`))

	checks, err := s.ListChecksByResource("res-A")
	require.NoError(t, err)
	assert.Len(t, checks, 2)
}

func TestSetCheckState(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.InsertTask("t1", "res", "", "", ""))
	require.NoError(t, s.InsertCheck("c1", "t1", "res", "b", `{}`))
	require.NoError(t, s.SetCheckState("c1", "passed"))

	c, err := s.GetCheck("c1")
	require.NoError(t, err)
	assert.Equal(t, "passed", c.State)
}

// ---------------------------------------------------------------------------
// Verification round-trips
// ---------------------------------------------------------------------------

func TestInsertVerification_ListVerifications(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.InsertTask("t1", "res", "", "", ""))
	require.NoError(t, s.InsertCheck("c1", "t1", "res", "b", `{}`))

	err := s.InsertVerification("v1", "c1", "res", `{"latency":100}`, `{"latency":9999}`, "behavior exhibited; check rejects the stub", true, false)
	require.NoError(t, err)

	verifications, err := s.ListVerificationsByCheck("c1")
	require.NoError(t, err)
	require.Len(t, verifications, 1)

	v := verifications[0]
	assert.Equal(t, "v1", v.ID)
	assert.Equal(t, "c1", v.CheckID)
	assert.Equal(t, "res", v.ResourceID)
	assert.True(t, v.Passed)
	assert.False(t, v.Theater)
	assert.Equal(t, "behavior exhibited; check rejects the stub", v.Reason)
}

func TestInsertVerification_TheaterFlag(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.InsertTask("t1", "res", "", "", ""))
	require.NoError(t, s.InsertCheck("c1", "t1", "res", "b", `{}`))

	require.NoError(t, s.InsertVerification("v1", "c1", "res", `{}`, `{}`, "theater", false, true))

	verifications, err := s.ListVerificationsByCheck("c1")
	require.NoError(t, err)
	require.Len(t, verifications, 1)
	assert.False(t, verifications[0].Passed)
	assert.True(t, verifications[0].Theater)
}
