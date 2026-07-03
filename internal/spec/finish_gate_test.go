package spec

import (
	"context"

	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/check"
)

// The finish gate: a session must not finalize while behavioral checks are
// unresolved. pending = never verified, failed/theater = verified bad,
// malformed = the CHECK itself is broken and needs re-authoring. All of them
// block. Silently finalizing past any of these states is the quiet-workaround
// path the gate exists to close.

func seedChecks(t *testing.T, s *Spec, resourceID string, states []string) []string {
	t.Helper()
	tasks := []TaskInput{{Description: "behavioral task", Checks: []CheckInput{}}}
	for range states {
		tasks[0].Checks = append(tasks[0].Checks, CheckInput{
			Behavior: "output is non-silent",
			Check: check.Check{Predicates: []check.Predicate{
				{Field: "peak", Op: "gt", Value: check.F(0)},
			}},
		})
	}
	require.NoError(t, s.TasksCommit(context.Background(), resourceID, tasks))

	checks, err := s.store.ListChecksByResource(resourceID)
	require.NoError(t, err)
	require.Len(t, checks, len(states))
	ids := make([]string, len(checks))
	for i, c := range checks {
		ids[i] = c.ID
		if states[i] != "pending" {
			require.NoError(t, s.store.SetCheckState(c.ID, states[i]))
		}
	}
	return ids
}

func TestFinishBlocksOnUnresolvedChecks(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()

	_, err := s.Commit(ctx, st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "", "claude-sonnet-4-6")
	require.NoError(t, err)

	seedChecks(t, s, st.resourceID, []string{"pending", "malformed", "passed"})

	res, err := s.Finish(ctx, st.sessionID, false)
	require.NoError(t, err)
	require.True(t, res.Blocked, "finish must refuse while checks are pending or malformed")
	require.Len(t, res.BlockingChecks, 2, "the passed check must not block; pending and malformed must")

	states := map[string]bool{}
	for _, bc := range res.BlockingChecks {
		states[bc.State] = true
		require.Equal(t, st.resourceID, bc.ResourceID)
		require.NotEmpty(t, bc.Behavior)
	}
	require.True(t, states["pending"], "pending checks block finish")
	require.True(t, states["malformed"], "malformed checks block finish LOUDLY — they are not a quiet exit")

	// The session must still be open: finish was refused, not performed.
	sess, err := s.store.GetSession(st.sessionID)
	require.NoError(t, err)
	require.NotEqual(t, "completed", sess.Status)
}

func TestFinishForceOverridesGate(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()

	_, err := s.Commit(ctx, st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "", "claude-sonnet-4-6")
	require.NoError(t, err)

	seedChecks(t, s, st.resourceID, []string{"failed"})

	res, err := s.Finish(ctx, st.sessionID, true)
	require.NoError(t, err)
	require.False(t, res.Blocked, "force=true is the explicit, human-visible override")
}

func TestFinishProceedsWhenChecksResolved(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()

	_, err := s.Commit(ctx, st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "", "claude-sonnet-4-6")
	require.NoError(t, err)

	seedChecks(t, s, st.resourceID, []string{"passed", "graduated"})

	res, err := s.Finish(ctx, st.sessionID, false)
	require.NoError(t, err)
	require.False(t, res.Blocked)
	require.Equal(t, 1, res.Committed)
}

// Destroying a resource removes its behavioral state too — otherwise checks
// for a deleted resource dangle forever in every reconcile.
func TestDestroyRemovesBehavioralState(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()

	_, err := s.Commit(ctx, st.sessionID, st.resourceID,
		[]CommitFile{{Path: t.TempDir() + "/a.go", Content: "package out\n"}}, "", "claude-sonnet-5")
	require.NoError(t, err)
	seedChecks(t, s, st.resourceID, []string{"passed"})

	sess, err := s.store.GetSession(st.sessionID)
	require.NoError(t, err)
	destroyed := s.executeDestroys([]planpkg.PlannedAction{{ResourceID: st.resourceID, Kind: planpkg.ActionDestroy}}, sess.ApplyID)
	require.Len(t, destroyed, 1)

	checks, err := s.store.ListChecksByResource(st.resourceID)
	require.NoError(t, err)
	require.Empty(t, checks, "destroy must cascade to behavioral checks")
}
