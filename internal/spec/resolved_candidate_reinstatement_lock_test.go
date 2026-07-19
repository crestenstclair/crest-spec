package spec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
	projectworkspace "github.com/crestenstclair/crest-spec/internal/workspace"
)

// reinstatementBarrierStore pauses after Spec has verified candidate ownership
// and repository bytes, but before the durable reinstatement transaction. It
// makes the otherwise tiny TOCTOU window deterministic.
type reinstatementBarrierStore struct {
	specStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *reinstatementBarrierStore) ReinstateAcceptedSessionResource(
	ctx context.Context,
	input store.AcceptedCandidateReinstatement,
) (bool, error) {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	return s.specStore.ReinstateAcceptedSessionResource(ctx, input)
}

// A second process first probes the project lock with a bounded wait. Once it
// proves the lock is held, it waits normally and publishes both a repository
// mutation and a new shared-owner row while holding that same lock.
func TestResolvedCandidateReinstatementMutationWorker(t *testing.T) {
	if os.Getenv("CREST_SPEC_REINSTATEMENT_MUTATION_HELPER") != "1" {
		return
	}
	root := os.Getenv("CREST_SPEC_REINSTATEMENT_ROOT")
	statePath := os.Getenv("CREST_SPEC_REINSTATEMENT_STATE")
	targetPath := os.Getenv("CREST_SPEC_REINSTATEMENT_TARGET")
	repositoryPath := os.Getenv("CREST_SPEC_REINSTATEMENT_REPOSITORY_PATH")
	resourceID := os.Getenv("CREST_SPEC_REINSTATEMENT_OTHER_RESOURCE")
	mutated := []byte("package generated\n\nconst Tone = 880\n")

	_, err := fmt.Fprintln(os.Stdout, "ready")
	require.NoError(t, err)
	probeCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	release, err := projectworkspace.FromRoot(root).AcquireCommit(probeCtx)
	cancel()
	if err == nil {
		release()
		require.FailNow(t, "reinstatement did not hold the cross-process project commit lock")
	}
	require.True(t, errors.Is(err, context.DeadlineExceeded), err)
	_, err = fmt.Fprintln(os.Stdout, "blocked")
	require.NoError(t, err)

	release, err = projectworkspace.FromRoot(root).AcquireCommit(context.Background())
	require.NoError(t, err)
	defer release()
	require.NoError(t, os.WriteFile(targetPath, mutated, 0o644))
	st, err := store.New(statePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, st.Close()) }()
	require.NoError(t, st.SetGeneratedFile(store.GeneratedFile{
		Path: repositoryPath, ResourceID: resourceID,
		ContentHash: contextmanifest.Hash(string(mutated)), Model: "concurrent-worker",
		CreatedAt: time.Now().UTC(),
	}))
	_, err = fmt.Fprintln(os.Stdout, "mutated")
	require.NoError(t, err)
}

func TestResolvedCandidateReinstatementSerializesCrossProcessWorkspaceAndOwnershipMutation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	stateDir := filepath.Join(root, ".crest-spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"), []byte(withTestIntent(commitTestSpecCUE)), 0o644,
	))

	statePath := filepath.Join(stateDir, "state.db")
	cfg := &config.Config{SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3}
	st, err := store.New(statePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	initial := New(st, OSFileSystem{}, cfg)
	begin, err := initial.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.Len(t, begin.Plan, 1)
	resourceID := begin.Plan[0].ResourceID
	const repositoryPath = "generated/tone.go"
	const acceptedContent = "package generated\n\nconst Tone = 440\n"
	accepted, err := commitTestAttempt(t, initial, ctx, begin.SessionID, resourceID, []CommitFile{{
		Path: repositoryPath, Content: acceptedContent,
	}}, "accepted candidate", "test-model")
	require.NoError(t, err)
	require.True(t, accepted.Committed)

	routed, err := st.RouteClosureValidationFailures(ctx, store.ClosureValidationFailureRoutingInput{
		SessionID: begin.SessionID, ResourceIDs: []string{resourceID},
		Reason:   "diagnostic later proven to belong to another owner",
		Category: execution.FailureIntegrationError, MaxRepairRetries: 2,
	})
	require.NoError(t, err)
	require.Equal(t, []string{resourceID}, routed.ReopenedResources)
	require.Len(t, routed.Failures, 1)
	require.NoError(t, initial.ResolveFailure(
		ctx, routed.Failures[0].ID, "triage proved this accepted owner was innocent", "",
	))
	require.NoError(t, initial.Resolve(
		ctx, begin.SessionID, resourceID, "preserve the exact accepted candidate", "",
	))

	const otherResourceID = "valueObject.Audio.ConcurrentOwner"
	require.NoError(t, st.SetResource(store.Resource{
		ID: otherResourceID, Kind: "valueObject", ContextName: "Audio",
	}))
	barrier := &reinstatementBarrierStore{
		specStore: st, entered: make(chan struct{}), release: make(chan struct{}),
	}
	restarted := New(barrier, OSFileSystem{}, cfg)
	type contextResult struct {
		result *ContextResult
		err    error
	}
	contextDone := make(chan contextResult, 1)
	go func() {
		result, contextErr := restarted.Context(ctx, begin.SessionID, resourceID)
		contextDone <- contextResult{result: result, err: contextErr}
	}()
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reinstatement did not reach the post-verification transaction barrier")
	}

	targetPath := filepath.Join(root, repositoryPath)
	command := exec.Command(os.Args[0], "-test.run=^TestResolvedCandidateReinstatementMutationWorker$")
	command.Env = append(os.Environ(),
		"CREST_SPEC_REINSTATEMENT_MUTATION_HELPER=1",
		"CREST_SPEC_REINSTATEMENT_ROOT="+root,
		"CREST_SPEC_REINSTATEMENT_STATE="+statePath,
		"CREST_SPEC_REINSTATEMENT_TARGET="+targetPath,
		"CREST_SPEC_REINSTATEMENT_REPOSITORY_PATH="+repositoryPath,
		"CREST_SPEC_REINSTATEMENT_OTHER_RESOURCE="+otherResourceID,
	)
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	var stderr strings.Builder
	command.Stderr = &stderr
	require.NoError(t, command.Start())
	processWaited := false
	t.Cleanup(func() {
		if processWaited {
			return
		}
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	scanner := bufio.NewScanner(stdout)
	require.True(t, scanner.Scan(), "mutation worker did not start: %s", stderr.String())
	require.Equal(t, "ready", scanner.Text())
	require.True(t, scanner.Scan(), "mutation worker did not complete its lock probe: %s", stderr.String())
	require.Equal(t, "blocked", scanner.Text(),
		"a concurrent process must not reach workspace or ownership mutation before reinstatement commits")
	require.Equal(t, acceptedContent, string(mustReadFile(t, targetPath)))
	require.Empty(t, mustGeneratedFiles(t, st, otherResourceID))

	close(barrier.release)
	result := <-contextDone
	require.NoError(t, result.err)
	require.NotNil(t, result.result)
	require.True(t, result.result.NoDispatch)
	require.Equal(t, accepted.CandidateID, result.result.ReinstatedCandidateID)

	require.True(t, scanner.Scan(), "mutation worker did not publish after reinstatement: %s", stderr.String())
	require.Equal(t, "mutated", scanner.Text())
	require.NoError(t, command.Wait(), stderr.String())
	processWaited = true
	require.NoError(t, scanner.Err())
	require.Equal(t, "package generated\n\nconst Tone = 880\n", string(mustReadFile(t, targetPath)))
	require.Len(t, mustGeneratedFiles(t, st, otherResourceID), 1)
	reinstated, err := st.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), reinstated.State)
	require.Equal(t, "integration_pending", reinstated.Phase)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return content
}

func mustGeneratedFiles(t *testing.T, st *store.Store, resourceID string) []store.GeneratedFile {
	t.Helper()
	files, err := st.GetGeneratedFiles(resourceID)
	require.NoError(t, err)
	return files
}
