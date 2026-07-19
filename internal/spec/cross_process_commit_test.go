package spec

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

const overlappingCommitSpecCUE = `package crestsynth

project: name: "overlapping-commit"
project: meta: language: "rust"
project: contexts: Control: purpose: "exercise overlapping module commits"
project: contexts: Control: valueObjects: First: {
	from: "first"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "adds the first module"}]
}
project: contexts: Control: valueObjects: Second: {
	from: "second"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "adds the second module"}]
}
project: invariants: [{text: "module configuration stays internally consistent"}]
`

// blockingCommitFS stops exactly one target write after CommitAttempt has
// reloaded source provenance and acquired both commit coordinators.
type blockingCommitFS struct {
	OSFileSystem
	target  string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *blockingCommitFS) WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	if filepath.Clean(path) == filepath.Clean(f.target) {
		f.once.Do(func() {
			close(f.entered)
			<-f.release
		})
	}
	return f.OSFileSystem.WriteFileAtomic(path, data, perm)
}

type concurrentCommitResult struct {
	result *CommitResult
	err    error
}

type subprocessCommitMessage struct {
	Event       string `json:"event"`
	AttemptID   string `json:"attempt_id,omitempty"`
	Committed   bool   `json:"committed,omitempty"`
	CandidateID string `json:"candidate_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

type subprocessCommitFS struct {
	OSFileSystem
	target  string
	role    string
	input   *bufio.Reader
	output  *json.Encoder
	entered bool
}

func (f *subprocessCommitFS) WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	if filepath.Clean(path) == filepath.Clean(f.target) && !f.entered {
		f.entered = true
		if err := f.output.Encode(subprocessCommitMessage{Event: "write_entered"}); err != nil {
			return fmt.Errorf("signal workspace write: %w", err)
		}
		if f.role == "first" {
			command, err := f.input.ReadString('\n')
			if err != nil {
				return fmt.Errorf("wait for workspace write release: %w", err)
			}
			if strings.TrimSpace(command) != "release" {
				return fmt.Errorf("unexpected workspace write command %q", strings.TrimSpace(command))
			}
		}
	}
	return f.OSFileSystem.WriteFileAtomic(path, data, perm)
}

type commitWorkerProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	decoder *json.Decoder
	stderr  strings.Builder
	waited  bool
}

func startCommitWorkerProcess(
	t *testing.T,
	role, root, sessionID, resourceID string,
) *commitWorkerProcess {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestCommitAttemptSubprocessWorker$")
	command.Env = append(os.Environ(),
		"CREST_SPEC_COMMIT_PROCESS_HELPER="+role,
		"CREST_SPEC_COMMIT_PROCESS_ROOT="+root,
		"CREST_SPEC_COMMIT_PROCESS_SESSION="+sessionID,
		"CREST_SPEC_COMMIT_PROCESS_RESOURCE="+resourceID,
	)
	input, err := command.StdinPipe()
	require.NoError(t, err)
	output, err := command.StdoutPipe()
	require.NoError(t, err)
	worker := &commitWorkerProcess{command: command, input: input, decoder: json.NewDecoder(output)}
	command.Stderr = &worker.stderr
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		if worker.waited {
			return
		}
		_ = worker.input.Close()
		_ = worker.command.Process.Kill()
		_ = worker.command.Wait()
	})
	return worker
}

func (w *commitWorkerProcess) readMessage(t *testing.T) subprocessCommitMessage {
	t.Helper()
	var message subprocessCommitMessage
	require.NoError(t, w.decoder.Decode(&message), "worker protocol failed: %s", w.stderr.String())
	return message
}

func (w *commitWorkerProcess) writeCommand(t *testing.T, command string) {
	t.Helper()
	_, err := fmt.Fprintln(w.input, command)
	require.NoError(t, err)
}

func (w *commitWorkerProcess) wait(t *testing.T) {
	t.Helper()
	require.NoError(t, w.input.Close())
	require.NoError(t, w.command.Wait(), w.stderr.String())
	w.waited = true
}

func TestUnrepresentedCandidatePathMustRemainAbsentAndUnowned(t *testing.T) {
	tests := []struct {
		name          string
		createPath    bool
		ownerResource string
		wantError     string
	}{
		{name: "absent and unowned"},
		{name: "existing and unowned", createPath: true, wantError: "but now exists"},
		{name: "absent and owned by target", ownerResource: "resource.target", wantError: "but is already owned"},
		{name: "absent and owned by another resource", ownerResource: "resource.other", wantError: "but is already owned"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "spec"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
			if test.createPath {
				require.NoError(t, os.WriteFile(filepath.Join(root, "src", "new.rs"), []byte("existing\n"), 0o644))
			}
			st, err := store.New(":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, st.Close()) })
			require.NoError(t, st.SetResource(store.Resource{ID: "resource.target", Kind: "valueObject"}))
			if test.ownerResource != "" {
				if test.ownerResource != "resource.target" {
					require.NoError(t, st.SetResource(store.Resource{ID: test.ownerResource, Kind: "valueObject"}))
				}
				require.NoError(t, st.SetGeneratedFile(store.GeneratedFile{
					Path: "src/new.rs", ResourceID: test.ownerResource, ContentHash: "prior",
				}))
			}
			s := New(st, OSFileSystem{}, &config.Config{SpecDir: filepath.Join(root, "spec")})
			err = s.validateUnrepresentedCandidatePaths(
				&store.ContextManifest{},
				"resource.target",
				[]CommitFile{{Path: "src/new.rs", Content: "candidate\n"}},
			)
			if test.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, "candidate path was absent from the prepared context")
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestRealProcessesSerializeCommitAttemptAndRejectStaleSecondCreator(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	stateDir := filepath.Join(root, ".crest-spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"),
		[]byte(withTestIntent(overlappingCommitSpecCUE)),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "Cargo.toml"),
		[]byte("[package]\nname = \"overlapping-commit\"\nversion = \"0.1.0\"\n"),
		0o644,
	))
	targetPath := filepath.Join(root, "src", "real_process_shared.rs")
	statePath := filepath.Join(stateDir, "state.db")
	initialStore, err := store.New(statePath)
	require.NoError(t, err)
	initialSpec := New(initialStore, OSFileSystem{}, &config.Config{
		SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3,
		DispatchTimeout: 2 * time.Minute, ExecutionTimeout: 30 * time.Minute,
	})
	begin, err := initialSpec.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	next, err := initialSpec.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, next.Resources, 2)
	firstResource := next.Resources[0].ResourceID
	secondResource := next.Resources[1].ResourceID
	require.NoError(t, initialStore.Close())

	firstWorker := startCommitWorkerProcess(t, "first", root, begin.SessionID, firstResource)
	firstPrepared := firstWorker.readMessage(t)
	require.Equal(t, "prepared", firstPrepared.Event)
	require.NotEmpty(t, firstPrepared.AttemptID)
	secondWorker := startCommitWorkerProcess(t, "second", root, begin.SessionID, secondResource)
	secondPrepared := secondWorker.readMessage(t)
	require.Equal(t, "prepared", secondPrepared.Event)
	require.NotEmpty(t, secondPrepared.AttemptID)
	require.NoFileExists(t, targetPath)

	firstWorker.writeCommand(t, "commit")
	firstWrite := firstWorker.readMessage(t)
	require.Equal(t, "write_entered", firstWrite.Event, "first process must hold the OS project lock at its workspace write")
	secondWorker.writeCommand(t, "commit")
	secondMessage := make(chan subprocessCommitMessage, 1)
	go func() {
		var message subprocessCommitMessage
		if decodeErr := secondWorker.decoder.Decode(&message); decodeErr != nil {
			message = subprocessCommitMessage{Event: "protocol_error", Error: decodeErr.Error()}
		}
		secondMessage <- message
	}()
	select {
	case premature := <-secondMessage:
		t.Fatalf("second process was not blocked by the first process's OS project lock: %+v", premature)
	case <-time.After(150 * time.Millisecond):
	}

	firstWorker.writeCommand(t, "release")
	firstCompleted := firstWorker.readMessage(t)
	require.Equal(t, "completed", firstCompleted.Event)
	require.True(t, firstCompleted.Committed)
	require.Empty(t, firstCompleted.Error)
	firstWorker.wait(t)

	secondCompleted := <-secondMessage
	require.Equal(t, "completed", secondCompleted.Event, "stale second process must fail before entering a workspace write")
	require.False(t, secondCompleted.Committed)
	require.Contains(t, secondCompleted.Error, "candidate path was absent from the prepared context")
	require.Contains(t, secondCompleted.Error, "corrective_action=rebuild_context")
	secondWorker.wait(t)

	workspaceBytes, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	require.Equal(t, "pub struct FirstProcess;\n", string(workspaceBytes))
	verificationStore, err := store.New(statePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, verificationStore.Close()) })
	firstCandidate, err := verificationStore.GetCandidateSetByAttempt(ctx, firstPrepared.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "accepted", firstCandidate.Status)
	require.Equal(t, firstCompleted.CandidateID, firstCandidate.ID)
	_, err = verificationStore.GetCandidateSetByAttempt(ctx, secondPrepared.AttemptID)
	require.Error(t, err, "stale real-process candidate must fail before candidate persistence")
	secondExecution, err := verificationStore.GetExecutionManifestByAttempt(ctx, secondPrepared.AttemptID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusFailed), secondExecution.Status)
	secondState, err := verificationStore.GetSessionResource(begin.SessionID, secondResource)
	require.NoError(t, err)
	require.Equal(t, string(StateErrored), secondState.State)
	require.Zero(t, secondState.Attempts)
}

func TestCommitAttemptSubprocessWorker(t *testing.T) {
	role := os.Getenv("CREST_SPEC_COMMIT_PROCESS_HELPER")
	if role == "" {
		return
	}
	require.Contains(t, []string{"first", "second"}, role)
	root := os.Getenv("CREST_SPEC_COMMIT_PROCESS_ROOT")
	sessionID := os.Getenv("CREST_SPEC_COMMIT_PROCESS_SESSION")
	resourceID := os.Getenv("CREST_SPEC_COMMIT_PROCESS_RESOURCE")
	require.NotEmpty(t, root)
	require.NotEmpty(t, sessionID)
	require.NotEmpty(t, resourceID)
	input := bufio.NewReader(os.Stdin)
	output := json.NewEncoder(os.Stdout)
	targetPath := filepath.Join(root, "src", "real_process_shared.rs")
	workerFS := &subprocessCommitFS{
		target: targetPath, role: role, input: input, output: output,
	}
	st, err := store.New(filepath.Join(root, ".crest-spec", "state.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, st.Close()) }()
	s := New(st, workerFS, &config.Config{
		SpecDir: filepath.Join(root, "spec"), GenerateModel: "test-model", MaxRetries: 3,
		DispatchTimeout: 2 * time.Minute, ExecutionTimeout: 30 * time.Minute,
	})
	ctx := context.Background()
	prepared := prepareStartedOverlappingCommit(
		t, s, ctx, sessionID, resourceID, role+"-process-host", role+"-process-worker",
	)
	requireContextDidNotCapturePath(t, prepared, "src/real_process_shared.rs")
	require.NoError(t, output.Encode(subprocessCommitMessage{
		Event: "prepared", AttemptID: prepared.AttemptID,
	}))
	command, err := input.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "commit", strings.TrimSpace(command))
	content := "pub struct FirstProcess;\n"
	if role == "second" {
		content = "pub struct SecondProcess;\n"
	}
	result, commitErr := s.CommitAttempt(ctx, prepared.AttemptID, []CommitFile{{
		Path: "src/real_process_shared.rs", Content: content,
	}}, role+" real-process creator", CommitMetadata{})
	message := subprocessCommitMessage{Event: "completed"}
	if result != nil {
		message.Committed = result.Committed
		message.CandidateID = result.CandidateID
	}
	if commitErr != nil {
		message.Error = commitErr.Error()
	}
	require.NoError(t, output.Encode(message))
}

func TestIndependentSpecsSerializeOverlappingCommitAndRejectStaleSecondWriter(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	stateDir := filepath.Join(root, ".crest-spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"),
		[]byte(withTestIntent(overlappingCommitSpecCUE)),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "Cargo.toml"),
		[]byte("[package]\nname = \"overlapping-commit\"\nversion = \"0.1.0\"\n"),
		0o644,
	))
	const originalModule = "pub mod existing;\n"
	targetPath := filepath.Join(root, "src", "mod.rs")
	require.NoError(t, os.WriteFile(targetPath, []byte(originalModule), 0o644))

	statePath := filepath.Join(stateDir, "state.db")
	firstStore, err := store.New(statePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, firstStore.Close()) })
	firstFS := &blockingCommitFS{
		target: targetPath, entered: make(chan struct{}), release: make(chan struct{}),
	}
	cfg := &config.Config{
		SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3,
		DispatchTimeout: 2 * time.Minute, ExecutionTimeout: 30 * time.Minute,
	}
	firstSpec := New(firstStore, firstFS, cfg)
	begin, err := firstSpec.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.Len(t, begin.Plan, 2)
	require.Len(t, begin.Waves, 1)

	secondStore, err := store.New(statePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, secondStore.Close()) })
	secondFS := &blockingCommitFS{
		target: targetPath, entered: make(chan struct{}), release: make(chan struct{}),
	}
	secondSpec := New(secondStore, secondFS, cfg)

	next, err := firstSpec.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, next.Resources, 2)
	firstResource := next.Resources[0].ResourceID
	secondResource := next.Resources[1].ResourceID
	firstContext := prepareStartedOverlappingCommit(
		t, firstSpec, ctx, begin.SessionID, firstResource, "first-host", "first-worker",
	)
	secondContext := prepareStartedOverlappingCommit(
		t, secondSpec, ctx, begin.SessionID, secondResource, "second-host", "second-worker",
	)
	requireContextCapturedRawFile(t, firstContext, "src/mod.rs", originalModule)
	requireContextCapturedRawFile(t, secondContext, "src/mod.rs", originalModule)

	const firstModule = "pub mod existing;\npub mod first;\n"
	const secondModule = "pub mod existing;\npub mod second;\n"
	firstDone := make(chan concurrentCommitResult, 1)
	go func() {
		result, commitErr := firstSpec.CommitAttempt(ctx, firstContext.AttemptID, []CommitFile{{
			Path: "src/mod.rs", Content: firstModule,
		}}, "first overlapping candidate", CommitMetadata{})
		firstDone <- concurrentCommitResult{result: result, err: commitErr}
	}()
	select {
	case <-firstFS.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first commit never reached the coordinated workspace write")
	}

	secondDone := make(chan concurrentCommitResult, 1)
	go func() {
		result, commitErr := secondSpec.CommitAttempt(ctx, secondContext.AttemptID, []CommitFile{{
			Path: "src/mod.rs", Content: secondModule,
		}}, "stale overlapping candidate", CommitMetadata{})
		secondDone <- concurrentCommitResult{result: result, err: commitErr}
	}()

	select {
	case <-secondFS.entered:
		close(secondFS.release)
		close(firstFS.release)
		t.Fatal("independent Spec instances reached the same workspace write concurrently")
	case premature := <-secondDone:
		close(firstFS.release)
		t.Fatalf("second commit returned before the first critical section completed: result=%+v err=%v", premature.result, premature.err)
	case <-time.After(100 * time.Millisecond):
		// The second Spec is blocked by the project-scoped coordinator.
	}

	close(firstFS.release)
	first := <-firstDone
	require.NoError(t, first.err)
	require.NotNil(t, first.result)
	require.True(t, first.result.Committed)

	var second concurrentCommitResult
	select {
	case second = <-secondDone:
	case <-secondFS.entered:
		close(secondFS.release)
		second = <-secondDone
		t.Fatalf("stale second writer reached a workspace write: result=%+v err=%v", second.result, second.err)
	}
	require.Nil(t, second.result)
	require.ErrorContains(t, second.err, "repository context changed after preparation")
	require.ErrorContains(t, second.err, "corrective_action=rebuild_context")
	select {
	case <-secondFS.entered:
		t.Fatal("stale second candidate must be rejected before any workspace write")
	default:
	}

	workspaceBytes, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	require.Equal(t, firstModule, string(workspaceBytes))
	firstCandidate, err := firstStore.GetCandidateSetByAttempt(ctx, firstContext.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "accepted", firstCandidate.Status)
	require.Len(t, firstCandidate.Files, 1)
	require.Equal(t, contextmanifest.Hash(firstModule), firstCandidate.Files[0].ContentHash)
	_, err = secondStore.GetCandidateSetByAttempt(ctx, secondContext.AttemptID)
	require.Error(t, err, "stale second writer must fail before candidate persistence")
	secondExecution, err := secondStore.GetExecutionManifestByAttempt(ctx, secondContext.AttemptID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusFailed), secondExecution.Status)
	firstState, err := firstStore.GetSessionResource(begin.SessionID, firstResource)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), firstState.State)
	secondState, err := secondStore.GetSessionResource(begin.SessionID, secondResource)
	require.NoError(t, err)
	require.Equal(t, string(StateErrored), secondState.State)
	require.Zero(t, secondState.Attempts, "stale-context rejection is retry-exempt")
}

func TestIndependentSpecsSerializeCreationAndRejectStaleSecondCreator(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	stateDir := filepath.Join(root, ".crest-spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"),
		[]byte(withTestIntent(overlappingCommitSpecCUE)),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "Cargo.toml"),
		[]byte("[package]\nname = \"overlapping-commit\"\nversion = \"0.1.0\"\n"),
		0o644,
	))

	targetPath := filepath.Join(root, "src", "shared.rs")
	statePath := filepath.Join(stateDir, "state.db")
	firstStore, err := store.New(statePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, firstStore.Close()) })
	firstFS := &blockingCommitFS{
		target: targetPath, entered: make(chan struct{}), release: make(chan struct{}),
	}
	cfg := &config.Config{
		SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3,
		DispatchTimeout: 2 * time.Minute, ExecutionTimeout: 30 * time.Minute,
	}
	firstSpec := New(firstStore, firstFS, cfg)
	begin, err := firstSpec.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.Len(t, begin.Plan, 2)
	require.Len(t, begin.Waves, 1)

	secondStore, err := store.New(statePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, secondStore.Close()) })
	secondFS := &blockingCommitFS{
		target: targetPath, entered: make(chan struct{}), release: make(chan struct{}),
	}
	secondSpec := New(secondStore, secondFS, cfg)

	next, err := firstSpec.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, next.Resources, 2)
	firstResource := next.Resources[0].ResourceID
	secondResource := next.Resources[1].ResourceID
	firstContext := prepareStartedOverlappingCommit(
		t, firstSpec, ctx, begin.SessionID, firstResource, "first-create-host", "first-create-worker",
	)
	secondContext := prepareStartedOverlappingCommit(
		t, secondSpec, ctx, begin.SessionID, secondResource, "second-create-host", "second-create-worker",
	)
	requireContextDidNotCapturePath(t, firstContext, "src/shared.rs")
	requireContextDidNotCapturePath(t, secondContext, "src/shared.rs")
	require.NoFileExists(t, targetPath)

	const firstContent = "pub struct First;\n"
	const secondContent = "pub struct Second;\n"
	firstDone := make(chan concurrentCommitResult, 1)
	go func() {
		result, commitErr := firstSpec.CommitAttempt(ctx, firstContext.AttemptID, []CommitFile{{
			Path: "src/shared.rs", Content: firstContent,
		}}, "first creator", CommitMetadata{})
		firstDone <- concurrentCommitResult{result: result, err: commitErr}
	}()
	select {
	case <-firstFS.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first creator never reached the coordinated workspace write")
	}

	secondDone := make(chan concurrentCommitResult, 1)
	go func() {
		result, commitErr := secondSpec.CommitAttempt(ctx, secondContext.AttemptID, []CommitFile{{
			Path: "src/shared.rs", Content: secondContent,
		}}, "stale second creator", CommitMetadata{})
		secondDone <- concurrentCommitResult{result: result, err: commitErr}
	}()
	select {
	case <-secondFS.entered:
		close(secondFS.release)
		close(firstFS.release)
		t.Fatal("independent creators reached the same absent path concurrently")
	case premature := <-secondDone:
		close(firstFS.release)
		t.Fatalf("second creator returned before the first critical section completed: result=%+v err=%v", premature.result, premature.err)
	case <-time.After(100 * time.Millisecond):
	}

	close(firstFS.release)
	first := <-firstDone
	require.NoError(t, first.err)
	require.NotNil(t, first.result)
	require.True(t, first.result.Committed, "an absent and unowned candidate path must remain creatable")

	var second concurrentCommitResult
	select {
	case second = <-secondDone:
	case <-secondFS.entered:
		close(secondFS.release)
		second = <-secondDone
		t.Fatalf("stale second creator reached a workspace write: result=%+v err=%v", second.result, second.err)
	}
	require.Nil(t, second.result)
	require.ErrorContains(t, second.err, "candidate path was absent from the prepared context")
	require.ErrorContains(t, second.err, "corrective_action=rebuild_context")
	select {
	case <-secondFS.entered:
		t.Fatal("stale second creator must be rejected before any workspace write")
	default:
	}

	workspaceBytes, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	require.Equal(t, firstContent, string(workspaceBytes))
	firstCandidate, err := firstStore.GetCandidateSetByAttempt(ctx, firstContext.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "accepted", firstCandidate.Status)
	_, err = secondStore.GetCandidateSetByAttempt(ctx, secondContext.AttemptID)
	require.Error(t, err, "stale second creator must fail before candidate persistence")
	owners, err := firstStore.GetGeneratedFiles(firstResource)
	require.NoError(t, err)
	require.Len(t, owners, 1)
	require.Equal(t, "src/shared.rs", owners[0].Path)
}

func prepareStartedOverlappingCommit(
	t *testing.T,
	s *Spec,
	ctx context.Context,
	sessionID, resourceID, hostID, workerID string,
) *ContextResult {
	t.Helper()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: sessionID, ResourceID: resourceID,
	})
	require.NoError(t, err)
	dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, sessionID, resourceID, hostID, workerID,
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: hostID, HostSessionID: hostID, Model: "test-model",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)
	return prepared
}

func requireContextCapturedRawFile(t *testing.T, prepared *ContextResult, path, content string) {
	t.Helper()
	for _, section := range prepared.Sections {
		if filepath.ToSlash(section.SourcePath) != filepath.ToSlash(path) || section.SourceKind != "repository_file" {
			continue
		}
		require.Equal(t, contextmanifest.Hash(content), section.OriginalHash)
		return
	}
	available := make([]string, 0, len(prepared.Sections))
	for _, section := range prepared.Sections {
		if section.SourcePath != "" {
			available = append(available, section.SourcePath)
		}
	}
	require.Failf(t, "raw repository source was not captured", "missing %s in %s", path, strings.Join(available, ", "))
}

func requireContextDidNotCapturePath(t *testing.T, prepared *ContextResult, path string) {
	t.Helper()
	for _, section := range prepared.Sections {
		require.NotEqual(t, filepath.ToSlash(path), filepath.ToSlash(section.SourcePath))
	}
}
