package store

import (
	"context"
	"testing"
	"time"

	"github.com/crestenstclair/crest-spec/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionRequiresAcknowledgedAttemptClaim(t *testing.T) {
	st := newContextManifestStore(t, "resource.synth")
	ctx := context.Background()
	prepared := prepareStoredDispatchContext(t, st, "resource.synth", "attempt-1", "manifest-1")

	_, err := st.StartExecution(ctx, testExecutionManifest())
	require.ErrorContains(t, err, "dispatch_requested and worker_acknowledged are required")
	resource, err := st.GetSessionResource("session-1", "resource.synth")
	require.NoError(t, err)
	require.Equal(t, "pending", resource.State)
	require.Empty(t, resource.JobID)

	_, err = st.RequestDispatch(ctx, DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.synth",
		HostSessionID: "host-session-1", WorkerID: "worker-1",
	})
	require.NoError(t, err)
	resource, err = st.GetSessionResource("session-1", "resource.synth")
	require.NoError(t, err)
	require.Equal(t, "dispatched", resource.State)
	require.Equal(t, "attempt-1", resource.JobID)

	_, err = st.StartExecution(ctx, testExecutionManifest())
	require.ErrorContains(t, err, "dispatch_requested and worker_acknowledged are required")
	_, err = st.AcknowledgeDispatch(ctx, DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.synth",
		HostSessionID: "host-session-1", WorkerID: "worker-1",
	})
	require.NoError(t, err)
	_, err = st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)
}

func TestAcknowledgementWriteCannotOutrunClaimRecovery(t *testing.T) {
	st := newContextManifestStore(t, "resource.ack-race")
	ctx := context.Background()
	prepared := prepareStoredDispatchContext(t, st, "resource.ack-race", "attempt-ack-race", "manifest-ack-race")
	_, err := st.RequestDispatch(ctx, DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.ack-race",
		HostSessionID: "host-ack-race", WorkerID: "worker-1",
	})
	require.NoError(t, err)

	// Model the recovery transaction winning after AcknowledgeDispatch's read
	// checks but before its final UPDATE. The SQL write itself must compare the
	// exact attempt claim, otherwise both recovery and acknowledgement can win.
	require.NoError(t, st.UpdateSessionResourceState(
		"session-1", "resource.ack-race", "pending", "recovered", "", 0, "",
	))
	changed, err := st.queries.AcknowledgeDispatchLifecycle(ctx, db.AcknowledgeDispatchLifecycleParams{
		AcknowledgedAt: now(), HostSessionID: "host-ack-race", WorkerID: "worker-1",
		UpdatedAt: now(), ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.ack-race",
	})
	require.NoError(t, err)
	require.Zero(t, changed)
	lifecycle, err := st.GetDispatchLifecycle(ctx, prepared.ID)
	require.NoError(t, err)
	require.Nil(t, lifecycle.WorkerAcknowledgedAt)
}

func TestDispatchRequestedRejectsUnpreparedLifecycleWithoutClaim(t *testing.T) {
	st := newContextManifestStore(t, "resource.unprepared")
	_, err := st.RequestDispatch(context.Background(), DispatchRequest{
		ID: "host-invented-dispatch", SessionID: "session-1", ResourceID: "resource.unprepared",
		HostSessionID: "host-1", WorkerID: "worker-1",
	})
	require.ErrorContains(t, err, "prepared lifecycle")
	resource, err := st.GetSessionResource("session-1", "resource.unprepared")
	require.NoError(t, err)
	require.Equal(t, "pending", resource.State)
	require.Empty(t, resource.JobID)
}

func TestDispatchReplayRejectsRecoveredPreparedAttempt(t *testing.T) {
	st := newContextManifestStore(t, "resource.recovered-dispatch")
	ctx := context.Background()
	prepared := prepareStoredDispatchContext(
		t, st, "resource.recovered-dispatch", "attempt-recovered-dispatch", "manifest-recovered-dispatch",
	)
	request := DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.recovered-dispatch",
		HostSessionID: "host-recovered", WorkerID: "worker-1",
	}
	_, err := st.RequestDispatch(ctx, request)
	require.NoError(t, err)
	recovered, err := st.AbandonPreparedContext(ctx, PreparedContextAbandonment{
		AttemptID: "attempt-recovered-dispatch", Reason: "host disappeared before execution",
	})
	require.NoError(t, err)
	require.True(t, recovered.Recovered)
	require.True(t, recovered.ClaimReleased)

	_, err = st.RequestDispatch(ctx, request)
	require.ErrorContains(t, err, "rebuild_context")
	resource, err := st.GetSessionResource("session-1", "resource.recovered-dispatch")
	require.NoError(t, err)
	require.Equal(t, "pending", resource.State)
	require.Empty(t, resource.JobID)
}

func TestDispatchLifecycleHostEventsAreDurableIdempotentAndServerTimed(t *testing.T) {
	st := newContextManifestStore(t, "resource.one")
	ctx := context.Background()
	prepared := prepareStoredDispatchContext(t, st, "resource.one", "attempt-dispatch-1", "manifest-dispatch-1")
	requested, err := st.RequestDispatch(ctx, DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.one",
		HostSessionID: "host-1", WorkerID: "worker-7",
	})
	require.NoError(t, err)
	require.NotNil(t, requested.DispatchRequestedAt)
	assert.Nil(t, requested.WorkerAcknowledgedAt)

	replayed, err := st.RequestDispatch(ctx, DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.one",
		HostSessionID: "host-1", WorkerID: "worker-7",
	})
	require.NoError(t, err)
	require.Equal(t, requested.DispatchRequestedAt, replayed.DispatchRequestedAt,
		"idempotent reporting must preserve the first server observation")
	assert.Equal(t, requested.UpdatedAt, replayed.UpdatedAt,
		"an exact replay must not fabricate activity")

	_, err = st.RequestDispatch(ctx, DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.one",
		HostSessionID: "different-host", WorkerID: "worker-7",
	})
	require.ErrorContains(t, err, "host_session_id conflicts")
	unchanged, err := st.GetDispatchLifecycle(ctx, prepared.ID)
	require.NoError(t, err)
	assert.Equal(t, "host-1", unchanged.HostSessionID)
	assert.Equal(t, requested.UpdatedAt, unchanged.UpdatedAt)

	acknowledged, err := st.AcknowledgeDispatch(ctx, DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.one",
		HostSessionID: "host-1", WorkerID: "worker-7",
	})
	require.NoError(t, err)
	require.NotNil(t, acknowledged.WorkerAcknowledgedAt)
	assert.Equal(t, "worker-7", acknowledged.WorkerID)

	ackReplay, err := st.AcknowledgeDispatch(ctx, DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.one",
		HostSessionID: "host-1", WorkerID: "worker-7",
	})
	require.NoError(t, err)
	assert.Equal(t, acknowledged.WorkerAcknowledgedAt, ackReplay.WorkerAcknowledgedAt)
	assert.Equal(t, acknowledged.UpdatedAt, ackReplay.UpdatedAt,
		"an acknowledgement replay must not fabricate activity")

	_, err = st.AcknowledgeDispatch(ctx, DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: "resource.one",
		HostSessionID: "host-1", WorkerID: "worker-8",
	})
	require.ErrorContains(t, err, "worker_id conflicts")
	unchanged, err = st.GetDispatchLifecycle(ctx, prepared.ID)
	require.NoError(t, err)
	assert.Equal(t, "worker-7", unchanged.WorkerID)
	assert.Equal(t, acknowledged.UpdatedAt, unchanged.UpdatedAt)

	listed, err := st.ListDispatchLifecyclesBySession(ctx, "session-1")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, acknowledged.ID, listed[0].ID)
}

func TestExplicitDispatchRequiresStableWorkerIdentityAndRejectsWorkerOverlap(t *testing.T) {
	st := newContextManifestStore(t, "resource.one")
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.two", State: "pending", MaxRetries: 3,
	}))
	ctx := context.Background()
	firstPrepared := prepareStoredDispatchContext(t, st, "resource.one", "attempt-first", "manifest-first")
	secondPrepared := prepareStoredDispatchContext(t, st, "resource.two", "attempt-second", "manifest-second")

	_, err := st.RequestDispatch(ctx, DispatchRequest{
		ID: firstPrepared.ID, SessionID: "session-1", ResourceID: "resource.one", HostSessionID: "host-1",
	})
	require.ErrorContains(t, err, "host_session_id and worker_id are required")

	first, err := st.RequestDispatch(ctx, DispatchRequest{
		ID: firstPrepared.ID, SessionID: "session-1", ResourceID: "resource.one",
		HostSessionID: "host-1", WorkerID: "worker-1",
	})
	require.NoError(t, err)

	_, err = st.RequestDispatch(ctx, DispatchRequest{
		ID: secondPrepared.ID, SessionID: "session-1", ResourceID: "resource.two",
		HostSessionID: "host-1", WorkerID: "worker-1",
	})
	require.ErrorContains(t, err, "already has a nonterminal assignment")
	overlapped, err := st.GetDispatchLifecycle(ctx, secondPrepared.ID)
	require.NoError(t, err)
	require.Nil(t, overlapped.DispatchRequestedAt, "failed assignment must roll back its lifecycle timestamp")
	unclaimed, err := st.GetSessionResource("session-1", "resource.two")
	require.NoError(t, err)
	require.Equal(t, "pending", unclaimed.State)
	require.Empty(t, unclaimed.JobID)

	// Once the first assignment is terminal, the same stable worker identity is
	// deliberately reusable for the next resource.
	_, err = st.sqlDB.ExecContext(ctx,
		`UPDATE dispatch_lifecycles SET candidate_accepted_at = ? WHERE id = ?`,
		now(), first.ID,
	)
	require.NoError(t, err)
	second, err := st.RequestDispatch(ctx, DispatchRequest{
		ID: secondPrepared.ID, SessionID: "session-1", ResourceID: "resource.two",
		HostSessionID: "host-1", WorkerID: "worker-1",
	})
	require.NoError(t, err)
	require.Equal(t, "worker-1", second.WorkerID)

	_, err = st.AcknowledgeDispatch(ctx, DispatchRequest{
		ID: second.ID, SessionID: "session-1", ResourceID: "resource.two",
		HostSessionID: "", WorkerID: "worker-1",
	})
	require.ErrorContains(t, err, "host_session_id and worker_id are required")
}

func TestSkippedAssignmentReleasesWarmWorkerWithoutCandidate(t *testing.T) {
	st := newContextManifestStore(t, "resource.one")
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.two",
		State: "pending", MaxRetries: 3,
	}))
	ctx := context.Background()
	firstPrepared := prepareStoredDispatchContext(t, st, "resource.one", "attempt-skipped", "manifest-skipped")
	secondPrepared := prepareStoredDispatchContext(t, st, "resource.two", "attempt-after-skip", "manifest-after-skip")
	_, err := st.RequestDispatch(ctx, DispatchRequest{
		ID: firstPrepared.ID, SessionID: "session-1", ResourceID: "resource.one",
		HostSessionID: "host-1", WorkerID: "worker-1",
	})
	require.NoError(t, err)
	require.NoError(t, st.UpdateSessionResourceState(
		"session-1", "resource.one", "skipped", "human skip", "", 0, "",
	))

	second, err := st.RequestDispatch(ctx, DispatchRequest{
		ID: secondPrepared.ID, SessionID: "session-1", ResourceID: "resource.two",
		HostSessionID: "host-1", WorkerID: "worker-1",
	})
	require.NoError(t, err)
	require.Equal(t, "worker-1", second.WorkerID)
}

func TestContextRequestWithoutHostDispatchDoesNotFabricateAcknowledgement(t *testing.T) {
	st := testStore(t)
	require.NoError(t, st.CreateSession(Session{
		ID: "session-legacy", PlanJSON: "[]", WavesJSON: "[]", HashesJSON: "{}",
	}))
	lifecycle, err := st.RequestContext(context.Background(), DispatchRequest{
		SessionID: "session-legacy", ResourceID: "resource.legacy",
	})
	require.NoError(t, err)
	assert.Nil(t, lifecycle.DispatchRequestedAt)
	assert.Nil(t, lifecycle.WorkerAcknowledgedAt)
	require.NotNil(t, lifecycle.ContextRequestedAt)
}

func TestVacuumCompactsDispatchLifecycleBeforeContextAttempt(t *testing.T) {
	st := newContextManifestStore(t, "resource.dispatch-vacuum")
	ctx := context.Background()
	lifecycle, err := st.RequestContext(ctx, DispatchRequest{
		SessionID: "session-1", ResourceID: "resource.dispatch-vacuum",
	})
	require.NoError(t, err)
	_, err = st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest:            testContextManifest("attempt-dispatch-vacuum", "manifest-dispatch-vacuum", "resource.dispatch-vacuum"),
		DispatchLifecycleID: lifecycle.ID,
	})
	require.NoError(t, err)

	_, err = st.Vacuum(time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = st.GetDispatchLifecycle(ctx, lifecycle.ID)
	require.Error(t, err)
	_, err = st.GetContextManifest(ctx, "manifest-dispatch-vacuum")
	require.Error(t, err)
}

func prepareStoredDispatchContext(
	t *testing.T,
	st *Store,
	resourceID, attemptID, manifestID string,
) *DispatchLifecycle {
	t.Helper()
	ctx := context.Background()
	lifecycle, err := st.RequestContext(ctx, DispatchRequest{
		SessionID: "session-1", ResourceID: resourceID,
	})
	require.NoError(t, err)
	_, err = st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest:            testContextManifest(attemptID, manifestID, resourceID),
		DispatchLifecycleID: lifecycle.ID,
	})
	require.NoError(t, err)
	prepared, err := st.GetDispatchLifecycle(ctx, lifecycle.ID)
	require.NoError(t, err)
	return prepared
}
