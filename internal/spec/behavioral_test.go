package spec

import (
	"context"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/check"
	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newBehavioralSpec creates an in-memory Spec for behavioral pipeline tests.
func newBehavioralSpec(t *testing.T) *Spec {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return New(st, OSFileSystem{}, &config.Config{})
}

// ---------------------------------------------------------------------------
// DesignCommit
// ---------------------------------------------------------------------------

func TestDesignCommit_InsertAndRetrieve(t *testing.T) {
	s := newBehavioralSpec(t)

	err := s.DesignCommit(context.Background(), "auth", `{"domains":["login"]}`)
	require.NoError(t, err)

	d, err := s.GetDesign(context.Background(), "auth")
	require.NoError(t, err)
	assert.Equal(t, "auth", d.ContextName)
	assert.Equal(t, `{"domains":["login"]}`, d.ContractJSON)
}

func TestDesignCommit_RequiresContextName(t *testing.T) {
	s := newBehavioralSpec(t)
	err := s.DesignCommit(context.Background(), "", `{}`)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// TasksCommit
// ---------------------------------------------------------------------------

func TestTasksCommit_InsertsTasksAndChecks(t *testing.T) {
	s := newBehavioralSpec(t)

	tasks := []TaskInput{
		{
			Description: "ensure fast response",
			Checks: []CheckInput{
				{
					Behavior: "latency under 200ms",
					Check: check.Check{
						Behavior:   "latency under 200ms",
						Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: 200}},
					},
				},
			},
		},
	}

	err := s.TasksCommit(context.Background(), "res-A", tasks)
	require.NoError(t, err)

	// Check task was inserted
	dbTasks, err := s.ListTasksByResource(context.Background(), "res-A")
	require.NoError(t, err)
	require.Len(t, dbTasks, 1)
	assert.Equal(t, "ensure fast response", dbTasks[0].Description)
	assert.Equal(t, "res-A", dbTasks[0].ResourceID)

	// Check behavioral check was inserted
	checks, err := s.ListChecksByResource(context.Background(), "res-A")
	require.NoError(t, err)
	require.Len(t, checks, 1)
	assert.Equal(t, "latency under 200ms", checks[0].Behavior)
	assert.Equal(t, "pending", checks[0].State)
}

func TestTasksCommit_RequiresResourceID(t *testing.T) {
	s := newBehavioralSpec(t)
	err := s.TasksCommit(context.Background(), "", []TaskInput{{Description: "t"}})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Verify — fail-closed semantics
// ---------------------------------------------------------------------------

// commitTasksAndGetCheckID is a helper that commits one task+check and returns
// the resulting check ID.
func commitTasksAndGetCheckID(t *testing.T, s *Spec, c check.Check) string {
	t.Helper()
	tasks := []TaskInput{{
		Description: "test task",
		Checks:      []CheckInput{{Behavior: c.Behavior, Check: c}},
	}}
	require.NoError(t, s.TasksCommit(context.Background(), "res", tasks))

	checks, err := s.ListChecksByResource(context.Background(), "res")
	require.NoError(t, err)
	require.NotEmpty(t, checks)
	return checks[len(checks)-1].ID
}

// TestVerify_RealPassStubFail asserts a real-pass + stub-fail verdict is accepted.
func TestVerify_RealPassStubFail(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "latency under 200ms",
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: 200}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// real: latency_ms = 50  → passes (50 < 200)
	// stub: latency_ms = 9999 → fails (9999 >= 200) — check has teeth
	result, err := s.Verify(context.Background(), checkID,
		map[string]any{"latency_ms": float64(50)},
		map[string]any{"latency_ms": float64(9999)},
	)
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.False(t, result.Theater)

	// Check state must be "passed"
	bc, err := s.store.GetCheck(checkID)
	require.NoError(t, err)
	assert.Equal(t, "passed", bc.State)
}

// TestVerify_Theater asserts that a check that the stub also passes is rejected.
func TestVerify_Theater(t *testing.T) {
	s := newBehavioralSpec(t)

	// A trivially-true check: field > -999 always passes
	c := check.Check{
		Behavior:   "always true",
		Predicates: []check.Predicate{{Field: "x", Op: check.OpGt, Value: -999}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// Both real and stub pass → theater
	result, err := s.Verify(context.Background(), checkID,
		map[string]any{"x": float64(1)},
		map[string]any{"x": float64(1)},
	)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.True(t, result.Theater)

	bc, err := s.store.GetCheck(checkID)
	require.NoError(t, err)
	assert.Equal(t, "theater", bc.State)
}

// TestVerify_RealFail asserts that when the real observation fails, the check is rejected.
func TestVerify_RealFail(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "latency under 200ms",
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: 200}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// real: latency_ms = 500 → fails; stub: latency_ms = 9999 → also fails
	// → real-fail, not theater (stub fails = good), but overall rejected
	result, err := s.Verify(context.Background(), checkID,
		map[string]any{"latency_ms": float64(500)},
		map[string]any{"latency_ms": float64(9999)},
	)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.False(t, result.Theater)

	bc, err := s.store.GetCheck(checkID)
	require.NoError(t, err)
	assert.Equal(t, "failed", bc.State)
}

// TestVerify_PersistsVerification asserts that a Verification record is written.
func TestVerify_PersistsVerification(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "latency under 200ms",
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: 200}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	_, err := s.Verify(context.Background(), checkID,
		map[string]any{"latency_ms": float64(50)},
		map[string]any{"latency_ms": float64(9999)},
	)
	require.NoError(t, err)

	verifications, err := s.store.ListVerificationsByCheck(checkID)
	require.NoError(t, err)
	assert.Len(t, verifications, 1)
	assert.True(t, verifications[0].Passed)
}

// ---------------------------------------------------------------------------
// Graduate
// ---------------------------------------------------------------------------

func TestGraduate_TransitionsStateAndReturnsPredicates(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "latency under 200ms",
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: 200}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// Must verify-pass first
	_, err := s.Verify(context.Background(), checkID,
		map[string]any{"latency_ms": float64(50)},
		map[string]any{"latency_ms": float64(9999)},
	)
	require.NoError(t, err)

	result, err := s.Graduate(context.Background(), checkID)
	require.NoError(t, err)
	assert.Equal(t, checkID, result.CheckID)
	assert.Equal(t, "res", result.ResourceID)
	assert.Equal(t, "latency under 200ms", result.Behavior)
	assert.Equal(t, c, result.Check)

	// State must be "graduated"
	bc, err := s.store.GetCheck(checkID)
	require.NoError(t, err)
	assert.Equal(t, "graduated", bc.State)
}

func TestGraduate_RejectsNonPassedCheck(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "always true",
		Predicates: []check.Predicate{{Field: "x", Op: check.OpGt, Value: -999}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// Check is still pending — graduate should fail
	_, err := s.Graduate(context.Background(), checkID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "passed")
}

func TestGraduate_RejectsTheaterCheck(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "always true",
		Predicates: []check.Predicate{{Field: "x", Op: check.OpGt, Value: -999}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// Both pass → theater
	_, err := s.Verify(context.Background(), checkID,
		map[string]any{"x": float64(1)},
		map[string]any{"x": float64(1)},
	)
	require.NoError(t, err)

	_, err = s.Graduate(context.Background(), checkID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "theater")
}
