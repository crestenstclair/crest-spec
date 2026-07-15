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
						Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: check.F(200)}},
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

func TestTasksCommit_Idempotent(t *testing.T) {
	s := newBehavioralSpec(t)

	firstTasks := []TaskInput{
		{
			Description: "first task",
			Checks: []CheckInput{
				{
					Behavior: "first check A",
					Check: check.Check{
						Behavior:   "first check A",
						Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: check.F(200)}},
					},
				},
				{
					Behavior: "first check B",
					Check: check.Check{
						Behavior:   "first check B",
						Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: check.F(200)}},
					},
				},
			},
		},
	}

	// First commit: 1 task, 2 checks
	require.NoError(t, s.TasksCommit(context.Background(), "res-idem", firstTasks))

	tasks, err := s.ListTasksByResource(context.Background(), "res-idem")
	require.NoError(t, err)
	require.Len(t, tasks, 1, "first commit: expected 1 task")

	checks, err := s.ListChecksByResource(context.Background(), "res-idem")
	require.NoError(t, err)
	require.Len(t, checks, 2, "first commit: expected 2 checks")

	secondTasks := []TaskInput{
		{
			Description: "second task A",
			Checks: []CheckInput{
				{
					Behavior: "second check A",
					Check: check.Check{
						Behavior:   "second check A",
						Predicates: []check.Predicate{{Field: "rps", Op: check.OpGt, Value: check.F(100)}},
					},
				},
			},
		},
		{
			Description: "second task B",
			Checks:      []CheckInput{},
		},
	}

	// Second commit for SAME resource: 2 tasks, 1 check — must replace, not accumulate
	require.NoError(t, s.TasksCommit(context.Background(), "res-idem", secondTasks))

	tasks, err = s.ListTasksByResource(context.Background(), "res-idem")
	require.NoError(t, err)
	assert.Len(t, tasks, 2, "second commit: expected exactly 2 tasks (no accumulation)")

	checks, err = s.ListChecksByResource(context.Background(), "res-idem")
	require.NoError(t, err)
	assert.Len(t, checks, 1, "second commit: expected exactly 1 check (no accumulation)")
	assert.Equal(t, "second check A", checks[0].Behavior, "check must be from second commit")
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
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: check.F(200)}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// real: latency_ms = 50  → passes (50 < 200)
	// stub: latency_ms = 9999 → fails (9999 >= 200) — check has teeth
	result, err := s.verifySuppliedObservationsLegacy(context.Background(), checkID,
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
		Predicates: []check.Predicate{{Field: "x", Op: check.OpGt, Value: check.F(-999)}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// Both real and stub pass → theater
	result, err := s.verifySuppliedObservationsLegacy(context.Background(), checkID,
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
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: check.F(200)}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// real: latency_ms = 500 → fails; stub: latency_ms = 9999 → also fails
	// → real-fail, not theater (stub fails = good), but overall rejected
	result, err := s.verifySuppliedObservationsLegacy(context.Background(), checkID,
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

// TestVerify_EmptyStubRejected asserts the stub side is fail-CLOSED: an empty
// stub cannot falsify the behavior, so a passing real observation must NOT
// graduate alone. (Regression for the stub fail-open hole.)
func TestVerify_EmptyStubRejected(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "latency under 200ms",
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: check.F(200)}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// real passes, but stub is empty → no falsification possible → rejected.
	result, err := s.verifySuppliedObservationsLegacy(context.Background(), checkID,
		map[string]any{"latency_ms": float64(50)},
		map[string]any{},
	)
	require.NoError(t, err)
	assert.False(t, result.Passed, "a passing real observation must not pass with an empty stub")
	assert.False(t, result.Theater)
	assert.Contains(t, result.Reason, "malformed verification")

	bc, err := s.store.GetCheck(checkID)
	require.NoError(t, err)
	assert.Equal(t, "failed", bc.State)
}

// TestVerify_StubMissingPredicateFieldRejected asserts that a stub which omits a
// predicate field (so check.Verify would score it as "stub failed" for the wrong
// reason) is rejected rather than letting the real observation pass alone.
func TestVerify_StubMissingPredicateFieldRejected(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "latency under 200ms",
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: check.F(200)}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// real passes; stub is non-empty but lacks latency_ms → not a real measurement.
	result, err := s.verifySuppliedObservationsLegacy(context.Background(), checkID,
		map[string]any{"latency_ms": float64(50)},
		map[string]any{"unrelated": float64(1)},
	)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.False(t, result.Theater)
	assert.Contains(t, result.Reason, "latency_ms")

	bc, err := s.store.GetCheck(checkID)
	require.NoError(t, err)
	assert.Equal(t, "failed", bc.State)
}

// TestVerify_PersistsVerification asserts that a Verification record is written.
func TestVerify_PersistsVerification(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "latency under 200ms",
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: check.F(200)}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	_, err := s.verifySuppliedObservationsLegacy(context.Background(), checkID,
		map[string]any{"latency_ms": float64(50)},
		map[string]any{"latency_ms": float64(9999)},
	)
	require.NoError(t, err)

	verifications, err := s.store.ListVerificationsByCheck(checkID)
	require.NoError(t, err)
	assert.Len(t, verifications, 1)
	assert.True(t, verifications[0].Passed)
}

// TestVerify_StructuralPredicateMarkedMalformed asserts that a structurally
// invalid predicate (eq on a non-numeric field with no member to compare
// against) is classified as 'malformed', not 'failed' — regenerating the
// implementation can never make this predicate pass, so it must be
// quarantined rather than cycled through the verify→regenerate loop forever.
func TestVerify_StructuralPredicateMarkedMalformed(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "error field equals expected value",
		Predicates: []check.Predicate{{Field: "error", Op: check.OpEq, Value: check.F(0)}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	result, err := s.verifySuppliedObservationsLegacy(context.Background(), checkID,
		map[string]any{"error": "boom"},
		map[string]any{"error": "boom"},
	)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.True(t, result.Malformed)
	assert.Contains(t, result.Reason, "malformed check:")

	bc, err := s.store.GetCheck(checkID)
	require.NoError(t, err)
	assert.Equal(t, "malformed", bc.State)
}

// TestVerify_StubMalformedStaysFailedNotMalformed asserts the pre-existing
// stubMalformed guard (empty/incomplete stub) keeps rejecting into 'failed',
// not the new 'malformed' state — that path is about a bad verification
// call, not a structurally broken predicate.
func TestVerify_StubMalformedStaysFailedNotMalformed(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "latency under 200ms",
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: check.F(200)}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	result, err := s.verifySuppliedObservationsLegacy(context.Background(), checkID,
		map[string]any{"latency_ms": float64(50)},
		map[string]any{},
	)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.False(t, result.Malformed)

	bc, err := s.store.GetCheck(checkID)
	require.NoError(t, err)
	assert.Equal(t, "failed", bc.State)
}

// ---------------------------------------------------------------------------
// Graduate
// ---------------------------------------------------------------------------

func TestGraduate_TransitionsStateAndReturnsPredicates(t *testing.T) {
	s := newBehavioralSpec(t)

	c := check.Check{
		Behavior:   "latency under 200ms",
		Predicates: []check.Predicate{{Field: "latency_ms", Op: check.OpLt, Value: check.F(200)}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// Must verify-pass first
	_, err := s.verifySuppliedObservationsLegacy(context.Background(), checkID,
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
		Predicates: []check.Predicate{{Field: "x", Op: check.OpGt, Value: check.F(-999)}},
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
		Predicates: []check.Predicate{{Field: "x", Op: check.OpGt, Value: check.F(-999)}},
	}
	checkID := commitTasksAndGetCheckID(t, s, c)

	// Both pass → theater
	_, err := s.verifySuppliedObservationsLegacy(context.Background(), checkID,
		map[string]any{"x": float64(1)},
		map[string]any{"x": float64(1)},
	)
	require.NoError(t, err)

	_, err = s.Graduate(context.Background(), checkID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "theater")
}
