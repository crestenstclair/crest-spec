package spec

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/require"
)

type blockingClosureRoutingStore struct {
	specStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingClosureRoutingStore) RouteClosureValidationFailures(
	ctx context.Context,
	input store.ClosureValidationFailureRoutingInput,
) (*store.ClosureValidationFailureRoutingResult, error) {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.specStore.RouteClosureValidationFailures(ctx, input)
}

func TestConcurrentContextBatchCannotObserveUnroutedClosureRepair(t *testing.T) {
	const failingClosure = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	validations: [{kind: "custom", command: ["false"]}]
}
`
	s, state := newTestSpecWithSessionCUE(t, failingClosure)
	ctx := context.Background()
	commit, err := commitTestAttempt(
		t, s, ctx, state.sessionID, state.resourceID,
		[]CommitFile{{Path: "tone.go", Content: "package tone\n"}}, "", "test-model",
	)
	require.NoError(t, err)
	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)

	blocking := &blockingClosureRoutingStore{
		specStore: s.store, entered: make(chan struct{}), release: make(chan struct{}),
	}
	s.store = blocking
	verificationDone := make(chan *WaveVerifyResult, 1)
	go func() {
		verificationDone <- s.VerifyWave(ctx, state.sessionID, resource.WaveIndex)
	}()
	select {
	case <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("VerifyWave did not use the atomic closure-routing transition")
	}

	contextBatchDone := make(chan *WaveContextResult, 1)
	contextBatchErrors := make(chan error, 1)
	go func() {
		batch, batchErr := s.PrepareWaveContexts(ctx, WaveContextOptions{
			SessionID: state.sessionID, Limit: 1,
		})
		contextBatchDone <- batch
		contextBatchErrors <- batchErr
	}()
	select {
	case <-contextBatchDone:
		t.Fatal("context_batch returned while closure routing was still uncommitted")
	case <-time.After(150 * time.Millisecond):
		// VerifyWave's project lock and the SQLite transaction jointly keep
		// context preparation behind the atomic publication point.
	}

	close(blocking.release)
	verified := <-verificationDone
	require.False(t, verified.Passed)
	require.Equal(t, []string{state.resourceID}, verified.ReopenedResources)

	afterRouting := <-contextBatchDone
	require.NoError(t, <-contextBatchErrors)
	require.Len(t, afterRouting.Contexts, 1)
	require.Empty(t, afterRouting.Contexts[0].Error)
	require.NotNil(t, afterRouting.Contexts[0].Context)
	require.Equal(t, string(execution.RoleIntegrationImplementer), afterRouting.Contexts[0].Context.Role)
	require.NotEmpty(t, afterRouting.Contexts[0].Context.HandoffID)

	handoffs, err := s.store.ListAttemptHandoffsBySource(ctx, commit.AttemptID)
	require.NoError(t, err)
	require.Len(t, handoffs, 1)
	require.Equal(t, "accepted", handoffs[0].Status,
		"context persistence atomically accepts the route it used")
	require.Equal(t, afterRouting.Contexts[0].Context.AttemptID, handoffs[0].TargetAttemptID)
}
