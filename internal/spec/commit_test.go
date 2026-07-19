package spec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// commitTestSpecCUE declares one value object with no resource-level
// validation, so a commit against it exercises the no-gate path.
const commitTestSpecCUE = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "represents the valid result under test"}]
}
project: invariants: [{text: "no global state"}]
`

// testIntentCUE is shared by focused spec-engine tests whose resource shape is
// intentionally tiny. Production and fixture specs carry domain-specific
// intent; these tests need one valid goal without obscuring the behavior under
// test.
const testIntentCUE = `
project: mission: "Generate and verify the resource under test"
project: actors: developer: {description: "the developer running the test"}
project: goals: test_behavior: {description: "the declared test behavior works", priority: "required", actors: ["actor.developer"], capabilities: ["capability.exercise_resource"], requirements: ["requirement.valid_result"]}
project: capabilities: exercise_resource: {description: "exercise the resource under test", goals: ["goal.test_behavior"], acceptance: successful_result: {description: "the resource produces the expected result", actor: "actor.developer", evidence: ["evidence.test_result"]}}
project: requirements: valid_result: {kind: "functional", description: "the resource produces a valid result", goals: ["goal.test_behavior"], capabilities: ["capability.exercise_resource"]}
project: evidence: test_result: {kind: "validation", description: "the focused test validation"}
project: completion: requiredGoals: ["goal.test_behavior"]
`

func withTestIntent(specCUE string) string {
	return specCUE + "\n" + testIntentCUE
}

// commitTestSession bundles the IDs the commit-contract tests assert against.
type commitTestSession struct {
	sessionID  string
	resourceID string
}

// newTestSpecWithSession builds a Spec over a real store + minimal spec and
// starts a session, returning the session and a generatable resource ID.
func newTestSpecWithSession(t *testing.T) (*Spec, commitTestSession) {
	return newTestSpecWithSessionCUE(t, commitTestSpecCUE)
}

func newTestSpecWithSessionCUE(t *testing.T, specCUE string) (*Spec, commitTestSession) {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spec.cue"), []byte(withTestIntent(specCUE)), 0o644))

	cfg := &config.Config{SpecDir: dir, GenerateModel: "claude-sonnet-4-6", MaxRetries: 3}
	s := New(st, OSFileSystem{}, cfg)

	ctx := context.Background()
	begin, err := s.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.NotEmpty(t, begin.SessionID, "expected a session to start")

	next, err := s.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.False(t, next.Done, "expected at least one wave of work")
	require.NotEmpty(t, next.Resources, "expected a generatable resource")

	return s, commitTestSession{sessionID: begin.SessionID, resourceID: next.Resources[0].ResourceID}
}

// A validation that cannot launch remains fail-closed at the scheduled batch
// gate, but it is an infrastructure blocker rather than evidence that the
// accepted source candidate needs another model execution.
func TestBatchValidationKeepsCandidateWhenValidationCannotRun(t *testing.T) {
	const cueWithBrokenValidation = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	validations: [{kind: "custom", command: ["/nonexistent-crest-validator-xyz"]}]
}
`
	s, st := newTestSpecWithSessionCUE(t, cueWithBrokenValidation)
	res, err := commitTestAttempt(t, s, context.Background(), st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "",
		"claude-sonnet-4-6")
	require.NoError(t, err)
	require.True(t, res.Committed)
	require.True(t, res.ValidationDeferred)
	sr, err := s.store.GetSessionResource(st.sessionID, st.resourceID)
	require.NoError(t, err)
	acceptedAttempts := sr.Attempts
	verified := s.VerifyWave(context.Background(), st.sessionID, sr.WaveIndex)
	require.False(t, verified.Passed, "an unrunnable scheduled gate must fail closed")
	require.NotEmpty(t, verified.Errors)
	require.Contains(t, verified.Errors[0].Message, "could not run")
	require.Equal(t, "validation_infrastructure", verified.Errors[0].Kind)
	require.Empty(t, verified.ReopenedResources)
	preserved, err := s.store.GetSessionResource(st.sessionID, st.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), preserved.State)
	require.Equal(t, "integration_failed", preserved.Phase)
	require.Equal(t, acceptedAttempts, preserved.Attempts)
	next, err := s.Next(context.Background(), st.sessionID)
	require.NoError(t, err)
	require.True(t, next.ValidationReady)
	require.Empty(t, next.Resources)
}

func TestCommitRecordsGenerationOutcome(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	files := []CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}
	result, err := commitTestAttempt(t, s, context.Background(), st.sessionID, st.resourceID,
		files, "", "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	replayed, err := s.CommitAttempt(context.Background(), result.AttemptID, files, "", CommitMetadata{})
	require.NoError(t, err)
	require.True(t, replayed.Committed)
	require.Equal(t, result.CandidateID, replayed.CandidateID)
	gens, _ := s.store.ListGenerations(st.resourceID, 10)
	if len(gens) == 0 {
		t.Fatal("expected a generation record from commit")
	}
	// Outcome must round-trip through the DB CHECK constraint
	// (outcome IN ('accepted','rejected')) — store errors are swallowed by
	// convention, so a bad value silently leaves outcome NULL.
	require.Equal(t, "accepted", gens[0].Outcome, "committed generation must record outcome 'accepted'")
}

func TestConcurrentCommitReplayConvergesOnOneCandidate(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{SessionID: state.sessionID, ResourceID: state.resourceID})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, state.sessionID, state.resourceID, "", "",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostSessionID: lifecycle.HostSessionID, Model: "test-model", TemplateHashes: prepared.TemplateHashes,
		SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)
	files := []CommitFile{{Path: "generated/concurrent.go", Content: "package generated\n"}}

	start := make(chan struct{})
	results := make(chan *CommitResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, commitErr := s.CommitAttempt(ctx, prepared.AttemptID, files, "", CommitMetadata{})
			results <- result
			errs <- commitErr
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for commitErr := range errs {
		require.NoError(t, commitErr)
	}

	var candidateID string
	for result := range results {
		require.NotNil(t, result)
		require.True(t, result.Committed)
		if candidateID == "" {
			candidateID = result.CandidateID
		}
		require.Equal(t, candidateID, result.CandidateID)
	}
}

func TestCommitRejectsCUEChangedAfterContextBeforeWritingCandidate(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, state.sessionID, state.resourceID, "", "",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostSessionID: lifecycle.HostSessionID, Model: "test-model", TemplateHashes: prepared.TemplateHashes,
		SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)

	specPath := filepath.Join(s.cfg.SpecDir, "spec.cue")
	contents, err := os.ReadFile(specPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		specPath,
		[]byte(strings.Replace(string(contents), `from: "f64"`, `from: "string"`, 1)),
		0o644,
	))

	outputPath := "generated/stale.go"
	result, err := s.CommitAttempt(ctx, prepared.AttemptID, []CommitFile{{
		Path: outputPath, Content: "package generated\n",
	}}, "", CommitMetadata{})
	require.Nil(t, result)
	require.ErrorContains(t, err, "plan inputs changed after context preparation")
	require.ErrorContains(t, err, "rebuild_context")
	_, statErr := os.Stat(filepath.Join(filepath.Dir(s.cfg.SpecDir), outputPath))
	require.ErrorIs(t, statErr, os.ErrNotExist, "stale context must not write workspace files")
	_, candidateErr := s.store.GetCandidateSetByAttempt(ctx, prepared.AttemptID)
	require.Error(t, candidateErr, "stale context must fail before candidate submission")
}

func TestCommitRejectsRepositorySourceChangedAfterContext(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	outputPath := filepath.Join("generated", uuid.NewString()+".go")
	projectPath := filepath.Join(filepath.Dir(s.cfg.SpecDir), outputPath)
	_, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID, []CommitFile{{
		Path: outputPath, Content: "package generated\n\nconst Value = 1\n",
	}}, "", "test-model")
	require.NoError(t, err)
	_, err = s.store.ReopenSessionResources(ctx, state.sessionID, []string{state.resourceID}, "repair requested")
	require.NoError(t, err)

	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, state.sessionID, state.resourceID, "", "",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostSessionID: lifecycle.HostSessionID, Model: "test-model", TemplateHashes: prepared.TemplateHashes,
		SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)
	manualEdit := "package generated\n\nconst Value = 2\n"
	require.NoError(t, os.WriteFile(projectPath, []byte(manualEdit), 0o644))

	result, err := s.CommitAttempt(ctx, prepared.AttemptID, []CommitFile{{
		Path: outputPath, Content: "package generated\n\nconst Value = 3\n",
	}}, "", CommitMetadata{})
	require.Nil(t, result)
	require.ErrorContains(t, err, "repository context changed after preparation")
	require.ErrorContains(t, err, "rebuild_context")
	contents, readErr := os.ReadFile(projectPath)
	require.NoError(t, readErr)
	require.Equal(t, manualEdit, string(contents), "stale worker must not overwrite the newer repository edit")
}

func TestValidateSelectedRepositorySourcesUsesExactModuleConfigurationBytes(t *testing.T) {
	root := t.TempDir()
	s := &Spec{fs: OSFileSystem{}, cfg: &config.Config{SpecDir: filepath.Join(root, "spec")}}
	const rawManifest = "\n[package]\nname = \"crest-synth\"\n\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte(rawManifest), 0o644))
	manifest := &store.ContextManifest{Sections: []store.ContextManifestSection{{
		Kind: "module_configuration", SourceKind: "repository_file", SourcePath: "Cargo.toml",
		Decision: string(contextmanifest.Included), OriginalHash: contextmanifest.Hash(rawManifest), Content: rawManifest,
	}}}

	require.NoError(t, s.validateSelectedRepositorySources(manifest))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte(strings.TrimSpace(rawManifest)), 0o644))
	require.ErrorContains(t, s.validateSelectedRepositorySources(manifest), "no longer matches the repository source")
}

func TestValidateSelectedRepositorySourcesResumesLegacyNestedOwnedCodeContext(t *testing.T) {
	for _, test := range []struct {
		name         string
		innerChanges string
		outerChanges string
	}{
		{name: "outer changes", outerChanges: "remove the accidental envelope"},
		{name: "inner changes", innerChanges: "the envelope previously carried this repair"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			s := &Spec{fs: OSFileSystem{}, cfg: &config.Config{SpecDir: filepath.Join(root, "spec")}}
			rawRepositoryBytes := updateModeContent("src/lib.rs", "pub fn render() {}\n", test.innerChanges)
			legacyPresentation := updateModeContent("src/lib.rs", rawRepositoryBytes, test.outerChanges)
			require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "src", "lib.rs"), []byte(rawRepositoryBytes), 0o644))
			manifest := &store.ContextManifest{Sections: []store.ContextManifestSection{{
				Kind: "owned_code", SourceKind: "repository_file", SourcePath: "src/lib.rs",
				Decision: string(contextmanifest.Included), OriginalHash: contextmanifest.Hash(legacyPresentation), Content: legacyPresentation,
			}}}

			require.NoError(t, s.validateSelectedRepositorySources(manifest),
				"legacy active attempts must compare the exact nested raw bytes rather than an inner fence")
		})
	}
}

func TestCommitRejectsUpdateModeEnvelopeBeforeCandidatePersistence(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{SessionID: state.sessionID, ResourceID: state.resourceID})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, state.sessionID, state.resourceID, "", "",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostSessionID: lifecycle.HostSessionID, Model: "test-model", TemplateHashes: prepared.TemplateHashes,
		SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)
	const outputPath = "generated/enveloped.go"

	result, err := s.CommitAttempt(ctx, prepared.AttemptID, []CommitFile{{
		Path: outputPath, Content: updateModeContent(outputPath, "package generated\n", ""),
	}}, "", CommitMetadata{})
	require.Nil(t, result)
	require.ErrorContains(t, err, "submit only exact raw repository bytes")
	_, candidateErr := s.store.GetCandidateSetByAttempt(ctx, prepared.AttemptID)
	require.Error(t, candidateErr, "presentation envelopes must never enter candidate storage")
	_, statErr := os.Stat(filepath.Join(filepath.Dir(s.cfg.SpecDir), outputPath))
	require.ErrorIs(t, statErr, os.ErrNotExist, "presentation envelopes must never enter the workspace")
}

func TestCommitRejectsClaimOfExistingUpdateModeEnvelopeBeforeCandidatePersistence(t *testing.T) {
	rustSpec := strings.Replace(commitTestSpecCUE, `language: "go"`, `language: "rust"`, 1)
	s, state := newTestSpecWithSessionCUE(t, rustSpec)
	ctx := context.Background()
	const outputPath = "src/mod.rs"
	projectPath := filepath.Join(filepath.Dir(s.cfg.SpecDir), outputPath)
	envelope := updateModeContent(outputPath, "package generated\n", "")
	require.NoError(t, os.MkdirAll(filepath.Dir(projectPath), 0o755))
	require.NoError(t, os.WriteFile(projectPath, []byte(envelope), 0o644))
	prepared, err := s.PrepareContext(ctx, ContextOptions{SessionID: state.sessionID, ResourceID: state.resourceID})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, state.sessionID, state.resourceID, "", "",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostSessionID: lifecycle.HostSessionID, Model: "test-model", TemplateHashes: prepared.TemplateHashes,
		SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)

	result, err := s.CommitAttempt(ctx, prepared.AttemptID, []CommitFile{{Path: outputPath}}, "", CommitMetadata{})
	require.Nil(t, result)
	require.ErrorContains(t, err, "submit only exact raw repository bytes")
	_, candidateErr := s.store.GetCandidateSetByAttempt(ctx, prepared.AttemptID)
	require.Error(t, candidateErr, "claiming a corrupt file must not copy its envelope into candidate storage")
	content, readErr := os.ReadFile(projectPath)
	require.NoError(t, readErr)
	require.Equal(t, envelope, string(content), "governance rejection must leave the existing file untouched for repair")
}

func TestCommitRejectsRustfmtPathBannerBatchAtomicallyAndAllowsCleanResubmission(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{SessionID: state.sessionID, ResourceID: state.resourceID})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, state.sessionID, state.resourceID, "", "",
	)
	started, err := s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostSessionID: lifecycle.HostSessionID, Model: "test-model", TemplateHashes: prepared.TemplateHashes,
		SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)

	const (
		firstPath  = "generated/first.go"
		secondPath = "generated/second.go"
		firstRaw   = "package generated\n\nconst First = true\n"
		secondRaw  = "package generated\n\nconst Second = true"
	)
	projectRoot := filepath.Dir(s.cfg.SpecDir)
	malformedSecond := filepath.Join(projectRoot, filepath.FromSlash(secondPath)) + ":\n\n" + secondRaw

	result, err := s.CommitAttempt(ctx, prepared.AttemptID, []CommitFile{
		{Path: firstPath, Content: firstRaw},
		{Path: secondPath, Content: malformedSecond},
	}, "", CommitMetadata{})
	require.Nil(t, result)
	require.ErrorContains(t, err, "absolute-path formatter banner")
	require.ErrorContains(t, err, "submit only exact raw repository bytes")
	_, candidateErr := s.store.GetCandidateSetByAttempt(ctx, prepared.AttemptID)
	require.Error(t, candidateErr, "one malformed file must reject the complete immutable candidate")
	pendingJournals, journalErr := s.store.ListPendingWorkspaceCommitJournals(ctx)
	require.NoError(t, journalErr)
	require.Empty(t, pendingJournals, "candidate bytes must be validated before journal preparation")
	for _, path := range []string{firstPath, secondPath} {
		_, statErr := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(path)))
		require.ErrorIs(t, statErr, os.ErrNotExist, "no file in a rejected batch may be materialized")
	}
	stillExecuting, err := s.store.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusExecuting), stillExecuting.Status)

	result, err = s.CommitAttempt(ctx, prepared.AttemptID, []CommitFile{
		{Path: firstPath, Content: firstRaw},
		{Path: secondPath, Content: secondRaw},
	}, "", CommitMetadata{})
	require.NoError(t, err, "presentation rejection must not consume the attempt")
	require.True(t, result.Committed)
	for path, expected := range map[string]string{firstPath: firstRaw, secondPath: secondRaw} {
		content, readErr := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(path)))
		require.NoError(t, readErr)
		require.Equal(t, expected, string(content), "clean resubmission must preserve exact raw bytes")
	}
}

func TestWriteWorkspaceCommitCandidateRejectsMalformedBatchBeforeFirstWrite(t *testing.T) {
	root := t.TempDir()
	s := &Spec{fs: OSFileSystem{}, cfg: &config.Config{SpecDir: filepath.Join(root, "spec")}}
	const firstRaw = "package generated\n"
	malformed := filepath.Join(root, "src", "second.go") + ":\n\npackage generated\n"
	journal := &store.WorkspaceCommitJournal{Files: []store.WorkspaceCommitJournalFile{
		{Path: "src/first.go", CandidateContent: []byte(firstRaw), CandidateMode: 0o644, WriteIntent: "create"},
		{Path: "src/second.go", CandidateContent: []byte(malformed), CandidateMode: 0o644, WriteIntent: "create"},
	}}

	err := s.writeWorkspaceCommitCandidate(journal)
	require.ErrorContains(t, err, "absolute-path formatter banner")
	for _, path := range []string{"src/first.go", "src/second.go"} {
		_, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		require.ErrorIs(t, statErr, os.ErrNotExist, "all candidate files must be validated before the first workspace write")
	}

	err = s.applyAcceptedWorkspaceJournal(journal)
	require.ErrorContains(t, err, "absolute-path formatter banner")
	for _, path := range []string{"src/first.go", "src/second.go"} {
		_, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		require.ErrorIs(t, statErr, os.ErrNotExist, "recovery must not roll forward a legacy malformed accepted journal")
	}
}

func TestStartExecutionAcceptsUnmodifiedRawRustModuleContexts(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	rustCUE := strings.Replace(withTestIntent(commitTestSpecCUE), `language: "go"`, `language: "rust"`, 1)
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "spec.cue"), []byte(rustCUE), 0o644))
	const (
		rawManifest = "\n[package]\nname = \"raw-context-test\"\nversion = \"0.1.0\"\n\n"
		rawModule   = "\npub mod tone;\n\n"
	)
	require.NoError(t, os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte(rawManifest), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "mod.rs"), []byte(rawModule), 0o644))
	s := New(st, OSFileSystem{}, &config.Config{SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3})
	begin, err := s.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	next, err := s.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.NotEmpty(t, next.Resources)
	resourceID := next.Resources[0].ResourceID
	_, err = commitTestAttempt(t, s, ctx, begin.SessionID, resourceID, []CommitFile{{
		Path: filepath.Join("src", "mod.rs"), Content: rawModule,
	}}, "", "test-model")
	require.NoError(t, err)
	_, err = s.store.ReopenSessionResources(ctx, begin.SessionID, []string{resourceID}, "exercise owned-code provenance")
	require.NoError(t, err)
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: begin.SessionID, ResourceID: resourceID,
	})
	require.NoError(t, err)

	sections := make(map[string]contextmanifest.Section)
	for _, section := range prepared.Sections {
		if section.Kind == "module_configuration" || section.Kind == "owned_code" {
			sections[section.Kind+"\x00"+section.SourcePath] = section
		}
	}
	manifestSection := sections["module_configuration\x00Cargo.toml"]
	moduleSection := sections["module_configuration\x00"+filepath.Join("src", "mod.rs")]
	ownedSection := sections["owned_code\x00"+filepath.Join("src", "mod.rs")]
	require.Equal(t, rawManifest, manifestSection.Content)
	require.Equal(t, contextmanifest.Hash(rawManifest), manifestSection.OriginalHash)
	require.Equal(t, rawModule, moduleSection.Content)
	require.Equal(t, contextmanifest.Hash(rawModule), moduleSection.OriginalHash)
	require.Contains(t, ownedSection.Content, "UPDATE MODE")
	require.Equal(t, contextmanifest.Hash(rawModule), ownedSection.OriginalHash,
		"owned-code provenance and module provenance must hash the same raw repository bytes")
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, begin.SessionID, resourceID, "", "",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostSessionID: lifecycle.HostSessionID, Model: "test-model", TemplateHashes: prepared.TemplateHashes,
		SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)
}

type commitSessionOverrideStore struct {
	specStore
	mutate func(*store.Session)
}

func (s *commitSessionOverrideStore) GetSession(id string) (*store.Session, error) {
	session, err := s.specStore.GetSession(id)
	if err != nil {
		return nil, err
	}
	copy := *session
	s.mutate(&copy)
	return &copy, nil
}

func TestCommitFailsClosedOnInvalidCanonicalSessionState(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*store.Session)
		wantErr string
	}{
		{
			name: "planned action is missing",
			mutate: func(session *store.Session) {
				session.PlanJSON = "[]"
			},
			wantErr: "persisted plan operation is unavailable",
		},
		{
			name: "effective hashes are malformed",
			mutate: func(session *store.Session) {
				session.HashesJSON = "{"
			},
			wantErr: "decode session hashes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, state := newTestSpecWithSession(t)
			ctx := context.Background()
			prepared, err := s.PrepareContext(ctx, ContextOptions{
				SessionID: state.sessionID, ResourceID: state.resourceID,
			})
			require.NoError(t, err)
			lifecycle := dispatchAndAcknowledgePreparedContext(
				t, s, ctx, prepared, state.sessionID, state.resourceID, "", "",
			)
			_, err = s.StartExecution(ctx, ExecutionStartOptions{
				AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
				IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
				HostName: "spec-test", HostSessionID: lifecycle.HostSessionID, Model: "test-model", TemplateHashes: prepared.TemplateHashes,
				SystemInstructions: prepared.SystemPrompt,
			})
			require.NoError(t, err)

			canonical := s.store
			s.store = &commitSessionOverrideStore{specStore: canonical, mutate: tt.mutate}
			result, err := s.CommitAttempt(ctx, prepared.AttemptID, []CommitFile{{
				Path: "generated/canonical.go", Content: "package generated\n",
			}}, "", CommitMetadata{})
			require.ErrorContains(t, err, tt.wantErr)
			require.Nil(t, result)

			manifest, err := canonical.GetExecutionManifestByAttempt(ctx, prepared.AttemptID)
			require.NoError(t, err)
			require.Equal(t, string(execution.StatusFailed), manifest.Status)
			candidate, candidateErr := canonical.GetCandidateSetByAttempt(ctx, prepared.AttemptID)
			if tt.name == "planned action is missing" {
				require.Error(t, candidateErr, "invalid plan identity must fail before candidate submission")
			} else {
				require.NoError(t, candidateErr)
				require.Equal(t, "rejected", candidate.Status,
					"invalid canonical hash state must reject the staged candidate")
			}
		})
	}
}

func TestDeferredValidationPreservesAcceptedCandidateAndReopensResource(t *testing.T) {
	const cueWithFailingValidation = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	validations: [{kind: "custom", command: ["false"]}]
}
`
	s, st := newTestSpecWithSessionCUE(t, cueWithFailingValidation)
	commit, err := commitTestAttempt(t, s, context.Background(), st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "",
		"claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	sr, err := s.store.GetSessionResource(st.sessionID, st.resourceID)
	require.NoError(t, err)
	verified := s.VerifyWave(context.Background(), st.sessionID, sr.WaveIndex)
	require.False(t, verified.Passed)
	require.Equal(t, []string{st.resourceID}, verified.ReopenedResources)
	gens, _ := s.store.ListGenerations(st.resourceID, 10)
	if len(gens) == 0 {
		t.Fatal("expected a generation record from staged commit")
	}
	require.True(t, commit.ValidationDeferred)
	require.Equal(t, "accepted", gens[0].Outcome, "immutable candidate acceptance is preserved while its resource is reopened")
}
