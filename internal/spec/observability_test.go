package spec

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func TestSummarizeTimingUsesWallUnionEffortAndUsefulConcurrency(t *testing.T) {
	start := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	intervals := []workInterval{
		{startedAt: start.Add(10 * time.Millisecond), endedAt: start.Add(50 * time.Millisecond)},
		{startedAt: start.Add(30 * time.Millisecond), endedAt: start.Add(70 * time.Millisecond)},
		{startedAt: start.Add(80 * time.Millisecond), endedAt: start.Add(90 * time.Millisecond)},
	}

	metrics := summarizeTiming(intervals, start, start.Add(100*time.Millisecond))

	assert.Equal(t, int64(100), metrics.ElapsedMS)
	assert.Equal(t, int64(70), metrics.ActiveWallMS)
	assert.Equal(t, int64(90), metrics.SummedEffortMS)
	assert.Equal(t, int64(30), metrics.IdleMS)
	assert.Equal(t, 2, metrics.MaxUsefulConcurrency)
}

func TestSummarizeTimingDoesNotCountAdjacentWorkAsConcurrent(t *testing.T) {
	start := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	metrics := summarizeTiming([]workInterval{
		{startedAt: start, endedAt: start.Add(10 * time.Millisecond)},
		{startedAt: start.Add(10 * time.Millisecond), endedAt: start.Add(20 * time.Millisecond)},
	}, start, start.Add(20*time.Millisecond))

	assert.Equal(t, 1, metrics.MaxUsefulConcurrency)
	assert.Equal(t, int64(20), metrics.ActiveWallMS)
}

func TestSummarizeTimingCountsAliasedPhysicalValidationOnce(t *testing.T) {
	start := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	metrics := summarizeTiming([]workInterval{
		{category: "validation", identity: "physical-command", startedAt: start, endedAt: start.Add(40 * time.Millisecond)},
		{category: "validation", identity: "physical-command", startedAt: start, endedAt: start.Add(40 * time.Millisecond)},
	}, start, start.Add(40*time.Millisecond))

	assert.Equal(t, int64(40), metrics.ActiveWallMS)
	assert.Equal(t, int64(40), metrics.SummedEffortMS)
	assert.Equal(t, 1, metrics.MaxUsefulConcurrency)
}

func TestValidationResourceOwnersIncludeAttemptAndDefinitionTargets(t *testing.T) {
	run := store.ValidationRun{
		AttemptID: "attempt-a",
		Targets: []store.IntentVerificationTarget{
			{Kind: "resource", ID: "resource.b"},
			{Kind: "goal", ID: "goal.ignored"},
			{Kind: "resource", ID: "resource.outside-session"},
		},
	}
	owners := validationResourceOwners(
		run,
		map[string]string{"attempt-a": "resource.a"},
		map[string]bool{"resource.a": true, "resource.b": true},
	)

	assert.Equal(t, []string{"resource.a", "resource.b"}, owners)
}

func TestWaveTimingCountsSharedPreparationAndAliasedValidationOnce(t *testing.T) {
	start := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	shared := workInterval{
		category: "shared_context_preparation", identity: "batch-1",
		startedAt: start, endedAt: start.Add(10 * time.Millisecond),
	}
	validation := workInterval{
		category: "validation", identity: "physical-command",
		startedAt: start.Add(10 * time.Millisecond), endedAt: start.Add(30 * time.Millisecond),
	}
	timing := sessionTiming{
		batchByWave: map[int][]workInterval{0: {shared}},
		byResource: map[string][]workInterval{
			"resource.a": {validation},
			"resource.b": {validation},
		},
		attributedByResource: map[string][]workInterval{
			"resource.a": {validation},
			"resource.b": {validation},
		},
		missingAckByResource: map[string]int{},
	}

	summaries := buildWaveSummaries(
		[][]string{{"resource.a", "resource.b"}},
		[]store.SessionResource{
			{ResourceID: "resource.a", State: string(StateCommitted)},
			{ResourceID: "resource.b", State: string(StateCommitted)},
		},
		timing,
		start.Add(30*time.Millisecond),
	)

	require.Len(t, summaries, 1)
	assert.Equal(t, int64(30), summaries[0].SummedEffortMS)
	assert.Equal(t, 1, summaries[0].MaxUsefulConcurrency)
	assert.Equal(t, int64(10), summaries[0].Timing.SharedContextPreparationMS)
	assert.Equal(t, int64(20), summaries[0].Timing.ValidationMS)
}

func TestSummarizeWorkerPoolCountsOnlyExplicitAssignmentsAndReuse(t *testing.T) {
	requested := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	metrics := summarizeWorkerPool([]store.DispatchLifecycle{
		{ID: "one", HostSessionID: "host-1", WorkerID: "worker-a", DispatchRequestedAt: &requested},
		{ID: "two", HostSessionID: "host-1", WorkerID: "worker-a", DispatchRequestedAt: &requested},
		{ID: "three", HostSessionID: "host-1", WorkerID: "worker-b", DispatchRequestedAt: &requested},
		{ID: "legacy-implicit"},
	})

	assert.Equal(t, 2, metrics.UniqueWorkers)
	assert.Equal(t, 1, metrics.ReusedWorkers)
	assert.Equal(t, 3, metrics.Assignments)
	assert.Equal(t, 2, metrics.MaxAssignmentsPerWorker)
}

func TestPreparedWaveRejectsOverlappingAssignmentsForOneWorker(t *testing.T) {
	const twoResources = `package crestsynth

project: name: "worker-pool"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "represents tone"}]
}
project: contexts: Audio: valueObjects: Gain: {
	from: "f64"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "represents gain"}]
}
`
	s, state := newTestSpecWithSessionCUE(t, twoResources)
	ctx := context.Background()
	batch, err := s.PrepareWaveContexts(ctx, WaveContextOptions{SessionID: state.sessionID, Limit: 2})
	require.NoError(t, err)
	require.Len(t, batch.Contexts, 2)
	require.NotNil(t, batch.Contexts[0].Context)
	require.NotNil(t, batch.Contexts[1].Context)

	_, err = s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: batch.Contexts[0].Context.DispatchID,
		SessionID: state.sessionID, ResourceID: batch.Contexts[0].ResourceID,
		HostSessionID: "host-run", WorkerID: "worker-1",
	})
	require.NoError(t, err)

	_, err = s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: batch.Contexts[1].Context.DispatchID,
		SessionID: state.sessionID, ResourceID: batch.Contexts[1].ResourceID,
		HostSessionID: "host-run", WorkerID: "worker-1",
	})
	require.ErrorContains(t, err, "already has a nonterminal assignment")

	status, err := s.SessionStatus(ctx, state.sessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, status.WorkerPool.UniqueWorkers)
	assert.Equal(t, 1, status.WorkerPool.Assignments,
		"a rejected scheduling conflict is not a worker assignment")
}

func TestSessionStatusReportsPersistedActiveAttemptNotProcessCount(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, state.sessionID, state.resourceID,
		"observability-host-session", "observability-worker",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "observability-test", Provider: "test", Model: "test-model",
		HostSessionID:  "observability-host-session",
		Tools:          []store.ExecutionTool{{Name: "filesystem", Permission: "test-project"}},
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)

	status, err := s.SessionStatus(ctx, state.sessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, status.ActiveAttempts)
	assert.Equal(t, status.ActiveAttempts, status.Concurrency.Active)
	assert.Equal(t, 1, status.MaxUsefulConcurrency)
	assert.Equal(t, status.MaxUsefulConcurrency, status.Concurrency.Max)
	assert.Zero(t, status.QueuedResources, "the actively executing resource is not also queued")
	assert.NotEmpty(t, status.LastActivityAt)
	assert.GreaterOrEqual(t, status.ElapsedMS, int64(0))

	wave, err := s.WaveStatus(ctx, state.sessionID, 0)
	require.NoError(t, err)
	require.Len(t, wave.Resources, 1)
	assert.NotEmpty(t, wave.Resources[0].StartedAt)
	assert.GreaterOrEqual(t, wave.Resources[0].ElapsedMS, int64(0))
}

func TestSessionStatusReportsCompletedResourceExecutionDuration(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	_, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID,
		[]CommitFile{{Path: "observability.go", Content: "package observed\n"}}, "", "test-model")
	require.NoError(t, err)

	status, err := s.SessionStatus(ctx, state.sessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, status.CompletedResources)
	assert.Zero(t, status.ActiveAttempts)
	assert.GreaterOrEqual(t, status.SummedEffortMS, int64(0))

	wave, err := s.WaveStatus(ctx, state.sessionID, 0)
	require.NoError(t, err)
	require.Len(t, wave.Resources, 1)
	detail := wave.Resources[0]
	assert.NotEmpty(t, detail.StartedAt)
	assert.NotEmpty(t, detail.CompletedAt)
	assert.GreaterOrEqual(t, detail.DurationMS, int64(0))
}

func TestDispatchLifecycleRecordsExplicitBoundariesAndTiming(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	requested, err := s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: prepared.DispatchID,
		SessionID: state.sessionID, ResourceID: state.resourceID,
		HostSessionID: "host-session", WorkerID: "worker-1",
	})
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	acknowledged, err := s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "worker_acknowledged", DispatchID: requested.ID,
		SessionID: state.sessionID, ResourceID: state.resourceID,
		HostSessionID: "host-session", WorkerID: "worker-1",
	})
	require.NoError(t, err)
	require.NotNil(t, acknowledged.WorkerAcknowledgedAt)
	require.Equal(t, requested.ID, prepared.DispatchID)

	lifecycle, err := s.store.GetDispatchLifecycle(ctx, requested.ID)
	require.NoError(t, err)
	require.Equal(t, prepared.AttemptID, lifecycle.AttemptID)
	require.NotNil(t, lifecycle.DispatchRequestedAt)
	require.NotNil(t, lifecycle.WorkerAcknowledgedAt)
	require.NotNil(t, lifecycle.ContextRequestedAt)
	require.NotNil(t, lifecycle.ContextPreparedAt)
	require.Nil(t, lifecycle.ExecutionRegisteredAt)

	time.Sleep(2 * time.Millisecond)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "observability-test", Provider: "test", Model: "test-model",
		HostSessionID:  "host-session",
		Tools:          []store.ExecutionTool{{Name: "filesystem", Permission: "test-project"}},
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = s.CommitAttempt(ctx, prepared.AttemptID,
		[]CommitFile{{Path: "dispatch-observed.go", Content: "package observed\n"}}, "", CommitMetadata{})
	require.NoError(t, err)

	lifecycle, err = s.store.GetDispatchLifecycle(ctx, requested.ID)
	require.NoError(t, err)
	require.NotNil(t, lifecycle.ExecutionRegisteredAt)
	require.NotNil(t, lifecycle.CandidateSubmittedAt)
	require.NotNil(t, lifecycle.CandidateAcceptedAt)
	require.Nil(t, lifecycle.CandidateRejectedAt)

	status, err := s.SessionStatus(ctx, state.sessionID)
	require.NoError(t, err)
	assert.Greater(t, status.Timing.DispatchToAcknowledgedMS, int64(0))
	assert.GreaterOrEqual(t, status.Timing.ContextPreparationMS, int64(0))
	assert.Greater(t, status.Timing.PreparedToDispatchMS, int64(0))
	assert.Greater(t, status.Timing.AcknowledgedToExecutionMS, int64(0))
	assert.Greater(t, status.Timing.ModelActiveMS, int64(0))
	assert.Zero(t, status.Timing.MissingAcknowledgements)
	assert.Equal(t, 1, status.WorkerPool.UniqueWorkers)
	assert.Equal(t, 1, status.WorkerPool.Assignments)
	assert.Zero(t, status.WorkerPool.ReusedWorkers)
}

func TestMissingWorkerAcknowledgementRemainsExplicitlyUnattributed(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	requested, err := s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: prepared.DispatchID,
		SessionID: state.sessionID, ResourceID: state.resourceID,
		HostSessionID: "host-without-ack", WorkerID: "worker-without-ack",
	})
	require.NoError(t, err)
	require.Equal(t, prepared.DispatchID, requested.ID)

	status, err := s.SessionStatus(ctx, state.sessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, status.Timing.MissingAcknowledgements)
	assert.Zero(t, status.Timing.DispatchToAcknowledgedMS)
	assert.Zero(t, status.Timing.AcknowledgedToExecutionMS)
	assert.Greater(t, status.Timing.PreparedToDispatchMS, int64(0))
	assert.GreaterOrEqual(t, status.Timing.UnattributedWallMS, int64(2))
}

func TestWaveSummariesExcludeUnplannedRegistryResources(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	timing := sessionTiming{
		byResource:           map[string][]workInterval{},
		attributedByResource: map[string][]workInterval{},
		missingAckByResource: map[string]int{},
	}
	summaries := buildWaveSummaries(
		[][]string{{"resource.action", "resource.unchanged"}},
		[]store.SessionResource{{ResourceID: "resource.action", State: string(StatePending)}},
		timing,
		now,
	)
	require.Len(t, summaries, 1)
	assert.Equal(t, 1, summaries[0].Total)
	assert.Equal(t, 1, summaries[0].Pending)
}

func TestEveryInactiveDispatchableRepairStateCountsAsQueued(t *testing.T) {
	for _, state := range []ResourceState{
		StatePending, StateRejected, StateErrored, StateTimedOut, StateBlocked,
	} {
		t.Run(string(state), func(t *testing.T) {
			assert.True(t, resourceIsQueued(state, false))
			assert.False(t, resourceIsQueued(state, true))
		})
	}
}

func TestLifecycleIntervalsUseTerminalCompletionWithoutDispositionStamp(t *testing.T) {
	registered := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	submitted := registered.Add(40 * time.Millisecond)
	completed := submitted.Add(25 * time.Millisecond)
	manifest := &store.ExecutionManifest{Status: "cancelled", CompletedAt: &completed}

	withoutCandidate := lifecycleWorkIntervals(store.DispatchLifecycle{
		ResourceID: "resource.cancelled", ExecutionRegisteredAt: &registered,
	}, manifest, completed.Add(time.Second))
	require.Len(t, withoutCandidate, 1)
	assert.Equal(t, "model_active", withoutCandidate[0].category)
	assert.Equal(t, completed, withoutCandidate[0].endedAt)

	withCandidate := lifecycleWorkIntervals(store.DispatchLifecycle{
		ResourceID: "resource.cancelled", ExecutionRegisteredAt: &registered,
		CandidateSubmittedAt: &submitted,
	}, manifest, completed.Add(time.Second))
	require.Len(t, withCandidate, 2)
	assert.Equal(t, submitted, withCandidate[0].endedAt)
	assert.Equal(t, "submission_commit", withCandidate[1].category)
	assert.Equal(t, completed, withCandidate[1].endedAt)
}

func TestAbandonClosesObservedModelIntervalAtExecutionCompletion(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, state.sessionID, state.resourceID,
		"abandon-host-session", "abandon-worker",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "abandon-observability-test", Model: "test-model",
		HostSessionID:  "abandon-host-session",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = s.Abandon(ctx, state.sessionID, "host run interrupted", false)
	require.NoError(t, err)

	manifest, err := s.store.GetExecutionManifestByAttempt(ctx, prepared.AttemptID)
	require.NoError(t, err)
	require.NotNil(t, manifest.CompletedAt)
	status, err := s.SessionStatus(ctx, state.sessionID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, status.Timing.ModelActiveMS, int64(2))
}
