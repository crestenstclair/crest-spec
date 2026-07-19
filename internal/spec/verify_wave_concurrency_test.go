package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// TestConcurrentVerifyWaveRunsOneCanonicalFailingClosure is deliberately a
// persistent, two-Spec test. MCP hosts can overlap during a restart, so an
// in-memory mutex on one Spec would not protect the durable session or prevent
// the same expensive closure command, reopen, failure classification, and
// repair handoff from being emitted twice.
func TestConcurrentVerifyWaveRunsOneCanonicalFailingClosure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	stateDir := filepath.Join(root, ".crest-spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.MkdirAll(stateDir, 0o755))

	const validationScript = `#!/bin/sh
printf 'run\n' >> validation-invocations.log
touch validation-entered
while [ ! -f validation-release ]; do sleep 0.01; done
printf 'src/provider.go src/consumer.go\n' >&2
exit 1
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "verify-wave.sh"), []byte(validationScript), 0o755))
	const failingClosure = `
project: validations: [{
	kind: "custom"
	command: ["sh", "verify-wave.sh"]
	resources: ["valueObject.Audio.Tone", "domainService.Audio.Mixer"]
}]
`
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"),
		[]byte(withTestIntent(twoWaveSpecCUE+failingClosure)),
		0o644,
	))

	statePath := filepath.Join(stateDir, "state.db")
	firstStore, err := store.New(statePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, firstStore.Close()) })
	cfg := &config.Config{
		SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3, WaveMaxRetries: 2,
		DispatchTimeout: 2 * time.Minute, ExecutionTimeout: 30 * time.Minute,
	}
	firstSpec := New(firstStore, OSFileSystem{}, cfg)
	begin, err := firstSpec.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	providerID := "valueObject.Audio.Tone"
	consumerID := "domainService.Audio.Mixer"
	commitValidationResource(t, firstSpec, begin.SessionID, providerID, "provider.go")
	commitValidationResource(t, firstSpec, begin.SessionID, consumerID, "consumer.go")

	beforeAttempts, err := firstStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, beforeAttempts, 2)
	beforeRetryCounts := make(map[string]int, 2)
	beforeResourceAttempts := make(map[string]int, 2)
	acceptedAttempts := make(map[string]string, 2)
	for _, attempt := range beforeAttempts {
		beforeRetryCounts[attempt.ResourceID] = attempt.RetryNumber
		require.Equal(t, "accepted", attempt.Status)
		acceptedAttempts[attempt.ResourceID] = attempt.ID
		resource, resourceErr := firstStore.GetSessionResource(begin.SessionID, attempt.ResourceID)
		require.NoError(t, resourceErr)
		beforeResourceAttempts[attempt.ResourceID] = resource.Attempts
	}

	secondStore, err := store.New(statePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, secondStore.Close()) })
	secondSpec := New(secondStore, OSFileSystem{}, cfg)

	results := make(chan *WaveVerifyResult, 2)
	go func() { results <- firstSpec.VerifyWave(ctx, begin.SessionID, 1) }()
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(filepath.Join(root, "validation-entered"))
		return statErr == nil
	}, 5*time.Second, 10*time.Millisecond, "first closure command never started")
	go func() { results <- secondSpec.VerifyWave(ctx, begin.SessionID, 1) }()

	// Leave enough overlap for an unserialized second host to enter the command
	// before releasing the first. The durable assertion below, rather than this
	// timing window, decides correctness.
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(root, "validation-release"), []byte("release\n"), 0o644))
	firstResult := <-results
	secondResult := <-results

	canonicalResults := 0
	reopenResults := 0
	deferredResults := 0
	for _, result := range []*WaveVerifyResult{firstResult, secondResult} {
		require.Equal(t, 1, result.RetryCount,
			"both the canonical verifier and its follower must observe one durable closure-repair cycle")
		require.Equal(t, 2, result.MaxRetries)
		if result.IntegrationValidated {
			canonicalResults++
			require.False(t, result.Passed)
			require.ElementsMatch(t, []string{providerID, consumerID}, result.ReopenedResources)
		}
		if len(result.ReopenedResources) > 0 {
			reopenResults++
		}
		if result.Deferred {
			deferredResults++
		}
	}
	require.Equal(t, 1, canonicalResults, "only one caller may execute the closure gate")
	require.Equal(t, 1, reopenResults, "only the canonical outcome may reopen owners")
	require.Equal(t, 1, deferredResults, "the follower must observe the canonical reopen")

	invocations, err := os.ReadFile(filepath.Join(root, "validation-invocations.log"))
	require.NoError(t, err)
	require.Equal(t, "run\n", string(invocations), "the closure command must execute exactly once")
	runs, err := firstStore.ListValidationRuns(ctx, 20)
	require.NoError(t, err)
	require.Len(t, runs, 1, "the persistent session must have one canonical validation run")
	require.Equal(t, begin.SessionID, runs[0].SessionID)
	require.Equal(t, "failed", runs[0].Classification)

	afterAttempts, err := firstStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, afterAttempts, len(beforeAttempts), "verification must not create repair attempts itself")
	for _, attempt := range afterAttempts {
		require.Equal(t, beforeRetryCounts[attempt.ResourceID], attempt.RetryNumber,
			"duplicate verification must not consume retry budget")
	}
	for _, resourceID := range []string{providerID, consumerID} {
		resource, resourceErr := firstStore.GetSessionResource(begin.SessionID, resourceID)
		require.NoError(t, resourceErr)
		require.Equal(t, "pending", resource.State)
		require.Equal(t, "integration_failed", resource.Phase)
		require.Positive(t, beforeResourceAttempts[resourceID])
		require.Zero(t, resource.Attempts,
			"one canonical reopen starts a fresh repair epoch and the duplicate must not charge it")

		attemptID := acceptedAttempts[resourceID]
		failures, failureErr := firstStore.ListFailureClassificationsByAttempt(ctx, attemptID)
		require.NoError(t, failureErr)
		require.Len(t, failures, 1, "one closure observation must create one failure classification per owner")
		require.Equal(t, "affected_closure_validation", failures[0].EvidenceSource)
		handoffs, handoffErr := firstStore.ListAttemptHandoffsBySource(ctx, attemptID)
		require.NoError(t, handoffErr)
		require.Len(t, handoffs, 1, "one closure observation must create one repair handoff per owner")
		require.Equal(t, failures[0].ID, handoffs[0].FailureID)
	}
}
