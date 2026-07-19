package spec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

const (
	workspaceOriginalExisting  = "package src\n\nconst Value = \"original\"\n"
	workspaceCandidateExisting = "package src\n\nconst Value = \"candidate\"\n"
	workspaceCandidateNew      = "package src\n\nconst Created = true\n"
)

type workspaceCommitBoundaryFixture struct {
	ctx          context.Context
	root         string
	specDir      string
	statePath    string
	existingPath string
	newPath      string
	sessionID    string
	resourceID   string
	attemptID    string
	executionID  string
	initial      *Spec
	initialStore *store.Store
}

func newWorkspaceCommitBoundaryFixture(t *testing.T) *workspaceCommitBoundaryFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".crest-spec"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"), []byte(withTestIntent(commitTestSpecCUE)), 0o644,
	))
	existingPath := filepath.Join(root, "src", "first.go")

	statePath := filepath.Join(root, ".crest-spec", "state.db")
	st, err := store.New(statePath)
	require.NoError(t, err)
	sp := New(st, OSFileSystem{}, &config.Config{
		SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3,
	})
	begin, err := sp.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.Len(t, begin.Plan, 1)
	resourceID := begin.Plan[0].ResourceID
	baseline, err := commitTestAttempt(t, sp, ctx, begin.SessionID, resourceID, []CommitFile{{
		Path: "src/first.go", Content: workspaceOriginalExisting,
	}}, "", "test-model")
	require.NoError(t, err)
	require.True(t, baseline.Committed)
	require.NoError(t, os.Chmod(existingPath, 0o640))
	_, err = st.ReopenSessionResources(
		ctx, begin.SessionID, []string{resourceID}, "exercise interrupted minimal-diff commit",
	)
	require.NoError(t, err)
	prepared, err := sp.PrepareContext(ctx, ContextOptions{
		SessionID: begin.SessionID, ResourceID: resourceID,
	})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, sp, ctx, prepared, begin.SessionID, resourceID, "boundary-host", "boundary-worker",
	)
	started, err := sp.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "boundary-test", HostSessionID: lifecycle.HostSessionID,
		Model: "test-model", TemplateHashes: prepared.TemplateHashes,
		SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)
	return &workspaceCommitBoundaryFixture{
		ctx: ctx, root: root, specDir: specDir, statePath: statePath,
		existingPath: existingPath, newPath: filepath.Join(root, "src", "second.go"),
		sessionID: begin.SessionID, resourceID: resourceID,
		attemptID: prepared.AttemptID, executionID: started.ID,
		initial: sp, initialStore: st,
	}
}

func (fixture *workspaceCommitBoundaryFixture) candidateFiles() []CommitFile {
	return []CommitFile{
		{Path: "src/first.go", Content: workspaceCandidateExisting},
		{Path: "src/second.go", Content: workspaceCandidateNew},
	}
}

func (fixture *workspaceCommitBoundaryFixture) restart(t *testing.T) (*Spec, *store.Store) {
	t.Helper()
	require.NoError(t, fixture.initialStore.Close())
	st, err := store.New(fixture.statePath)
	require.NoError(t, err)
	sp := New(st, OSFileSystem{}, &config.Config{
		SpecDir: fixture.specDir, GenerateModel: "test-model", MaxRetries: 3,
	})
	return sp, st
}

func requireCommitProcessCrash(t *testing.T, run func()) {
	t.Helper()
	defer func() { require.Equal(t, errSimulatedCommitProcessCrash, recover()) }()
	run()
}

func crashAtWorkspaceCommitStage(spec *Spec, target string) {
	spec.workspaceCommitFailpoint = func(stage string) {
		if stage == target {
			panic(errSimulatedCommitProcessCrash)
		}
	}
}

type blockAfterAtomicPublishFS struct {
	OSFileSystem
	target    string
	published chan struct{}
	release   <-chan struct{}
	once      sync.Once
}

func (files *blockAfterAtomicPublishFS) WriteFileAtomic(
	path string,
	data []byte,
	mode os.FileMode,
) error {
	if err := files.OSFileSystem.WriteFileAtomic(path, data, mode); err != nil {
		return err
	}
	if path == files.target {
		files.once.Do(func() { close(files.published) })
		<-files.release
	}
	return nil
}

func TestWorkspaceCommitCrashAfterJournalBeforeCandidateSubmissionRecoversImmediately(t *testing.T) {
	fixture := newWorkspaceCommitBoundaryFixture(t)
	crashAtWorkspaceCommitStage(fixture.initial, "after_journal_prepared")
	requireCommitProcessCrash(t, func() {
		_, err := fixture.initial.CommitAttempt(
			fixture.ctx, fixture.attemptID, fixture.candidateFiles(), "", CommitMetadata{},
		)
		if err != nil {
			panic(err)
		}
	})

	manifest, err := fixture.initialStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusExecuting), manifest.Status)
	_, err = fixture.initialStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.Error(t, err, "crash boundary must precede candidate submission")
	pending, err := fixture.initialStore.ListPendingWorkspaceCommitJournals(fixture.ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Empty(t, pending[0].CandidateID)
	require.Equal(t, store.WorkspaceCommitPrepared, pending[0].Status)
	require.FileExists(t, fixture.existingPath)
	require.NoFileExists(t, fixture.newPath)

	restarted, restartedStore := fixture.restart(t)
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	next, err := restarted.Next(fixture.ctx, fixture.sessionID)
	require.NoError(t, err)
	require.Len(t, next.Resources, 1)
	require.Equal(t, StateErrored, next.Resources[0].State)
	manifest, err = restartedStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusFailed), manifest.Status)
	journal, err := restartedStore.GetWorkspaceCommitJournal(fixture.ctx, pending[0].ID)
	require.NoError(t, err)
	require.Equal(t, store.WorkspaceCommitRolledBack, journal.Status)
	contents, err := os.ReadFile(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, workspaceOriginalExisting, string(contents))
	require.NoFileExists(t, fixture.newPath)
}

func TestWorkspaceCommitCrashAfterCandidateSubmissionBeforeJournalBindRejectsUnboundCandidate(t *testing.T) {
	fixture := newWorkspaceCommitBoundaryFixture(t)
	crashAtWorkspaceCommitStage(fixture.initial, "after_candidate_submitted")
	requireCommitProcessCrash(t, func() {
		_, err := fixture.initial.CommitAttempt(
			fixture.ctx, fixture.attemptID, fixture.candidateFiles(), "", CommitMetadata{},
		)
		if err != nil {
			panic(err)
		}
	})

	manifest, err := fixture.initialStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusCandidateSubmitted), manifest.Status)
	candidate, err := fixture.initialStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.NoError(t, err)
	require.Equal(t, "submitted", candidate.Status)
	pending, err := fixture.initialStore.ListPendingWorkspaceCommitJournals(fixture.ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Empty(t, pending[0].CandidateID,
		"failpoint must be after candidate commit and before journal bind")
	contents, err := os.ReadFile(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, workspaceOriginalExisting, string(contents))
	info, err := os.Stat(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	require.NoFileExists(t, fixture.newPath)

	restarted, restartedStore := fixture.restart(t)
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	next, err := restarted.Next(fixture.ctx, fixture.sessionID)
	require.NoError(t, err)
	require.Len(t, next.Resources, 1)
	require.Equal(t, StateErrored, next.Resources[0].State)
	manifest, err = restartedStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusFailed), manifest.Status)
	candidate, err = restartedStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.NoError(t, err)
	require.Equal(t, "rejected", candidate.Status)
	journal, err := restartedStore.GetWorkspaceCommitJournal(fixture.ctx, pending[0].ID)
	require.NoError(t, err)
	require.Equal(t, store.WorkspaceCommitRolledBack, journal.Status)
	contents, err = os.ReadFile(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, workspaceOriginalExisting, string(contents))
	info, err = os.Stat(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	require.NoFileExists(t, fixture.newPath)
}

func TestWorkspaceCommitCrashAfterAllWritesBeforeValidationRestoresOriginalTree(t *testing.T) {
	fixture := newWorkspaceCommitBoundaryFixture(t)
	crashAtWorkspaceCommitStage(fixture.initial, "after_workspace_writes")
	requireCommitProcessCrash(t, func() {
		_, err := fixture.initial.CommitAttempt(
			fixture.ctx, fixture.attemptID, fixture.candidateFiles(), "", CommitMetadata{},
		)
		if err != nil {
			panic(err)
		}
	})

	existing, err := os.ReadFile(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, workspaceCandidateExisting, string(existing))
	created, err := os.ReadFile(fixture.newPath)
	require.NoError(t, err)
	require.Equal(t, workspaceCandidateNew, string(created))
	manifest, err := fixture.initialStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusCandidateSubmitted), manifest.Status)
	pending, err := fixture.initialStore.ListPendingWorkspaceCommitJournals(fixture.ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, store.WorkspaceCommitApplied, pending[0].Status)

	restarted, restartedStore := fixture.restart(t)
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	_, err = restarted.Next(fixture.ctx, fixture.sessionID)
	require.NoError(t, err)
	restored, err := os.ReadFile(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, workspaceOriginalExisting, string(restored))
	info, err := os.Stat(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	require.NoFileExists(t, fixture.newPath)
	manifest, err = restartedStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusFailed), manifest.Status)
	candidate, err := restartedStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.NoError(t, err)
	require.Equal(t, "rejected", candidate.Status)
	journal, err := restartedStore.GetWorkspaceCommitJournal(fixture.ctx, pending[0].ID)
	require.NoError(t, err)
	require.Equal(t, store.WorkspaceCommitRolledBack, journal.Status)
}

func TestWorkspaceCommitCrashAfterValidatingTransitionRestoresAndRejectsCandidate(t *testing.T) {
	fixture := newWorkspaceCommitBoundaryFixture(t)
	crashAtWorkspaceCommitStage(fixture.initial, "after_validating_transition")
	requireCommitProcessCrash(t, func() {
		_, err := fixture.initial.CommitAttempt(
			fixture.ctx, fixture.attemptID, fixture.candidateFiles(), "", CommitMetadata{},
		)
		if err != nil {
			panic(err)
		}
	})
	manifest, err := fixture.initialStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusValidating), manifest.Status)
	pending, err := fixture.initialStore.ListPendingWorkspaceCommitJournals(fixture.ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, store.WorkspaceCommitApplied, pending[0].Status)

	restarted, restartedStore := fixture.restart(t)
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	next, err := restarted.Next(fixture.ctx, fixture.sessionID)
	require.NoError(t, err)
	require.Len(t, next.Resources, 1)
	require.Equal(t, StateErrored, next.Resources[0].State)
	contents, err := os.ReadFile(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, workspaceOriginalExisting, string(contents))
	info, err := os.Stat(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	require.NoFileExists(t, fixture.newPath)
	manifest, err = restartedStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusFailed), manifest.Status)
	candidate, err := restartedStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.NoError(t, err)
	require.Equal(t, "rejected", candidate.Status)
	journal, err := restartedStore.GetWorkspaceCommitJournal(fixture.ctx, pending[0].ID)
	require.NoError(t, err)
	require.Equal(t, store.WorkspaceCommitRolledBack, journal.Status)
}

func TestWorkspaceCommitCrashAfterCandidateAcceptanceRollsForwardAndNeverRollsBackDB(t *testing.T) {
	fixture := newWorkspaceCommitBoundaryFixture(t)
	crashAtWorkspaceCommitStage(fixture.initial, "after_finalize_candidate")
	requireCommitProcessCrash(t, func() {
		_, err := fixture.initial.CommitAttempt(
			fixture.ctx, fixture.attemptID, fixture.candidateFiles(), "", CommitMetadata{},
		)
		if err != nil {
			panic(err)
		}
	})

	manifest, err := fixture.initialStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusAccepted), manifest.Status)
	candidate, err := fixture.initialStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.NoError(t, err)
	require.Equal(t, "accepted", candidate.Status)
	pending, err := fixture.initialStore.ListPendingWorkspaceCommitJournals(fixture.ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)

	// Model a host crash where directory/file persistence exposes the old legal
	// version on restart. Accepted SQLite provenance is authoritative, so the
	// pending journal must roll forward from original to candidate bytes.
	require.NoError(t, (OSFileSystem{}).WriteFileAtomic(
		fixture.existingPath, []byte(workspaceOriginalExisting), 0o640,
	))
	require.NoError(t, os.Remove(fixture.newPath))
	restarted, restartedStore := fixture.restart(t)
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	next, err := restarted.Next(fixture.ctx, fixture.sessionID)
	require.NoError(t, err)
	require.Empty(t, next.Resources, "accepted execution must never be redispatched by journal recovery")

	existing, err := os.ReadFile(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, workspaceCandidateExisting, string(existing))
	existingInfo, err := os.Stat(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), existingInfo.Mode().Perm())
	created, err := os.ReadFile(fixture.newPath)
	require.NoError(t, err)
	require.Equal(t, workspaceCandidateNew, string(created))
	createdInfo, err := os.Stat(fixture.newPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), createdInfo.Mode().Perm())
	manifest, err = restartedStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusAccepted), manifest.Status)
	candidate, err = restartedStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.NoError(t, err)
	require.Equal(t, "accepted", candidate.Status)
	journal, err := restartedStore.GetWorkspaceCommitJournal(fixture.ctx, pending[0].ID)
	require.NoError(t, err)
	require.Equal(t, store.WorkspaceCommitCommitted, journal.Status)
	owned, err := restartedStore.GetGeneratedFiles(fixture.resourceID)
	require.NoError(t, err)
	require.Len(t, owned, 2)
	recoveredAgain, err := restarted.RecoverWorkspaceCommits(fixture.ctx)
	require.NoError(t, err)
	require.Zero(t, recoveredAgain)
}

func TestWorkspaceCommitPostFinalizeVerificationNeverReportsDivergentAcceptance(t *testing.T) {
	fixture := newWorkspaceCommitBoundaryFixture(t)
	const thirdParty = "package src\n\nconst Value = \"changed after SQLite acceptance\"\n"
	fixture.initial.workspaceCommitFailpoint = func(stage string) {
		if stage == "after_finalize_candidate" {
			require.NoError(t, (OSFileSystem{}).WriteFileAtomic(
				fixture.existingPath, []byte(thirdParty), 0o600,
			))
		}
	}
	result, err := fixture.initial.CommitAttempt(
		fixture.ctx, fixture.attemptID, fixture.candidateFiles(), "", CommitMetadata{},
	)
	require.Nil(t, result, "divergent accepted bytes must never be reported as a successful commit")
	require.ErrorContains(t, err, "accepted candidate workspace verification after finalization")
	require.ErrorContains(t, err, "refusing to overwrite third-party state")
	contents, err := os.ReadFile(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, thirdParty, string(contents))
	info, err := os.Stat(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	manifest, err := fixture.initialStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusAccepted), manifest.Status)
	candidate, err := fixture.initialStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.NoError(t, err)
	require.Equal(t, "accepted", candidate.Status)
	pending, err := fixture.initialStore.ListPendingWorkspaceCommitJournals(fixture.ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, store.WorkspaceCommitApplied, pending[0].Status,
		"post-finalize mismatch must leave durable recovery work visible")

	restarted, restartedStore := fixture.restart(t)
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	_, err = restarted.Next(fixture.ctx, fixture.sessionID)
	require.ErrorContains(t, err, "refusing to overwrite third-party state",
		"restart must surface the pending accepted-workspace blocker")
	contents, err = os.ReadFile(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, thirdParty, string(contents),
		"restart recovery must not overwrite the third-party value")
	manifest, err = restartedStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusAccepted), manifest.Status)
	journal, err := restartedStore.GetWorkspaceCommitJournal(fixture.ctx, pending[0].ID)
	require.NoError(t, err)
	require.Equal(t, store.WorkspaceCommitApplied, journal.Status,
		"failed roll-forward CAS must never complete the journal")
}

func TestWorkspaceCommitRecoveryRefusesThirdPartyDivergence(t *testing.T) {
	fixture := newWorkspaceCommitBoundaryFixture(t)
	crashingFS := &crashAfterFirstWorkspaceWriteFS{crashAfter: 1}
	fixture.initial.fs = crashingFS
	requireCommitProcessCrash(t, func() {
		_, err := fixture.initial.CommitAttempt(
			fixture.ctx, fixture.attemptID, fixture.candidateFiles(), "", CommitMetadata{},
		)
		if err != nil {
			panic(err)
		}
	})
	const thirdParty = "package src\n\nconst Value = \"manual edit\"\n"
	require.NoError(t, (OSFileSystem{}).WriteFileAtomic(fixture.existingPath, []byte(thirdParty), 0o600))

	restarted, restartedStore := fixture.restart(t)
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	_, err := restarted.Next(fixture.ctx, fixture.sessionID)
	require.ErrorContains(t, err, "refusing to overwrite third-party state")
	contents, readErr := os.ReadFile(fixture.existingPath)
	require.NoError(t, readErr)
	require.Equal(t, thirdParty, string(contents))
	info, statErr := os.Stat(fixture.existingPath)
	require.NoError(t, statErr)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	manifest, loadErr := restartedStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, loadErr)
	require.Equal(t, string(execution.StatusCandidateSubmitted), manifest.Status)
	candidate, loadErr := restartedStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.NoError(t, loadErr)
	require.Equal(t, "submitted", candidate.Status)
	pending, loadErr := restartedStore.ListPendingWorkspaceCommitJournals(fixture.ctx)
	require.NoError(t, loadErr)
	require.Len(t, pending, 1)
	owned, loadErr := restartedStore.GetGeneratedFiles(fixture.resourceID)
	require.NoError(t, loadErr)
	require.Len(t, owned, 1)
	require.Equal(t, "src/first.go", owned[0].Path)
	require.Equal(t, hashWorkspaceContent([]byte(workspaceOriginalExisting)), owned[0].ContentHash)
}

func TestWorkspaceCommitRechecksPublishedCandidateBeforeAcceptance(t *testing.T) {
	fixture := newWorkspaceCommitBoundaryFixture(t)
	published := make(chan struct{})
	release := make(chan struct{})
	fixture.initial.fs = &blockAfterAtomicPublishFS{
		target: fixture.existingPath, published: published, release: release,
	}
	type commitOutcome struct {
		result *CommitResult
		err    error
	}
	outcome := make(chan commitOutcome, 1)
	go func() {
		result, err := fixture.initial.CommitAttempt(
			fixture.ctx, fixture.attemptID, fixture.candidateFiles(), "", CommitMetadata{},
		)
		outcome <- commitOutcome{result: result, err: err}
	}()
	<-published
	const thirdParty = "package src\n\nconst Value = \"changed after publish\"\n"
	require.NoError(t, (OSFileSystem{}).WriteFileAtomic(fixture.existingPath, []byte(thirdParty), 0o600))
	close(release)
	finished := <-outcome
	require.Nil(t, finished.result)
	require.ErrorContains(t, finished.err, "candidate workspace verification before finalization")
	require.ErrorContains(t, finished.err, "refusing to overwrite third-party state")

	contents, err := os.ReadFile(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, thirdParty, string(contents),
		"failed verification must never overwrite the external edit")
	info, err := os.Stat(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	manifest, err := fixture.initialStore.GetExecutionManifest(fixture.ctx, fixture.executionID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusValidating), manifest.Status)
	candidate, err := fixture.initialStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.NoError(t, err)
	require.Equal(t, "submitted", candidate.Status)
	owned, err := fixture.initialStore.GetGeneratedFiles(fixture.resourceID)
	require.NoError(t, err)
	require.Len(t, owned, 1)
	require.Equal(t, hashWorkspaceContent([]byte(workspaceOriginalExisting)), owned[0].ContentHash,
		"the unverified candidate must not replace accepted ownership provenance")
	pending, err := fixture.initialStore.ListPendingWorkspaceCommitJournals(fixture.ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, store.WorkspaceCommitApplied, pending[0].Status)
}

func TestWorkspaceCommitRejectsSymlinkTargetBeforeCandidatePersistence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires POSIX link semantics")
	}
	fixture := newWorkspaceCommitBoundaryFixture(t)
	targetPath := filepath.Join(fixture.root, "src", "symlink-target.go")
	require.NoError(t, os.WriteFile(targetPath, []byte(workspaceOriginalExisting), 0o640))
	require.NoError(t, os.Remove(fixture.existingPath))
	require.NoError(t, os.Symlink(filepath.Base(targetPath), fixture.existingPath))

	result, err := fixture.initial.CommitAttempt(
		fixture.ctx, fixture.attemptID, fixture.candidateFiles(), "", CommitMetadata{},
	)
	require.Nil(t, result)
	require.ErrorContains(t, err, "contains symlink component")
	linkInfo, err := os.Lstat(fixture.existingPath)
	require.NoError(t, err)
	require.NotZero(t, linkInfo.Mode()&os.ModeSymlink,
		"governed rejection must preserve the link inode")
	linkTarget, err := os.Readlink(fixture.existingPath)
	require.NoError(t, err)
	require.Equal(t, filepath.Base(targetPath), linkTarget)
	targetBytes, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	require.Equal(t, workspaceOriginalExisting, string(targetBytes),
		"governed rejection must not mutate the symlink target")
	require.NoFileExists(t, fixture.newPath)
	_, err = fixture.initialStore.GetCandidateSetByAttempt(fixture.ctx, fixture.attemptID)
	require.Error(t, err, "unsafe topology must fail before candidate persistence")
	pending, err := fixture.initialStore.ListPendingWorkspaceCommitJournals(fixture.ctx)
	require.NoError(t, err)
	require.Empty(t, pending, "unsafe topology must fail before workspace journal persistence")
}
