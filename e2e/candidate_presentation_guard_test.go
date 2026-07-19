package e2e_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// TestRealBinaryRejectsCandidatePresentationMetadataAcrossRestart exercises the
// exact compiled MCP boundary that previously accepted rustfmt --emit stdout
// output as repository source. That rustfmt mode prepends an absolute filename
// banner followed by a colon and a blank line. Presentation bytes must fail
// before candidate persistence, journal creation, or workspace materialization,
// and a fresh server process must observe the same untouched repository.
func TestRealBinaryRejectsCandidatePresentationMetadataAcrossRestart(t *testing.T) {
	binary := buildCrestSpecBinary(t, repositoryRoot(t))

	tests := []struct {
		name    string
		content func(projectRoot string) string
	}{
		{
			name: "absolute rustfmt filename banner",
			content: func(projectRoot string) string {
				absoluteSource := filepath.Join(projectRoot, "src", "lib.rs")
				return absoluteSource + ":\n\n" + rustPresentationGuardSource
			},
		},
		{
			name: "crestspec update mode envelope",
			content: func(_ string) string {
				return "UPDATE MODE: preserve the existing implementation and make the smallest change that satisfies the task.\n\n" +
					"File: src/lib.rs\n\n```\n" + rustPresentationGuardSource + "\n```\n"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projectRoot := createRustPresentationGuardProject(t)
			before := snapshotPresentationGuardRepository(t, projectRoot)
			server := startCrestSpec(t, binary, projectRoot)
			started := startPresentationGuardExecution(t, server)
			prepared := started.Prepared
			manifest := started.Manifest

			commit := callToolResult(t, server, "spec/commit", map[string]any{
				"attempt_id": prepared.AttemptID,
				"files":      []map[string]any{{"path": "src/lib.rs", "content": test.content(projectRoot)}},
				"notes":      "reproduce presentation metadata corruption",
			})
			require.True(t, commit.IsError, "presentation metadata must fail closed")
			require.Contains(t, toolResultText(commit), "contains presentation metadata")
			require.Contains(t, toolResultText(commit), "submit only exact raw repository bytes")
			assertPresentationGuardState(t, server, projectRoot, prepared.AttemptID, manifest.ID, before)

			// Lose all process-local state and force startup/recovery through the
			// same on-disk database and repository used by the rejected commit.
			server.crash(t)
			server = startCrestSpec(t, binary, projectRoot)
			callTool[specmod.SessionStatusResult](t, server, "spec/status", map[string]any{
				"session_id": started.SessionID,
			})
			assertPresentationGuardState(t, server, projectRoot, prepared.AttemptID, manifest.ID, before)

			clean := callTool[specmod.CommitResult](t, server, "spec/commit", map[string]any{
				"attempt_id": prepared.AttemptID,
				"files": []map[string]any{{
					"path": "src/lib.rs", "content": rustPresentationGuardCleanReplacement,
				}},
				"notes": "retry the same execution with exact raw repository bytes",
			})
			require.True(t, clean.Committed)
			require.Equal(t, prepared.AttemptID, clean.AttemptID)
			require.Equal(t, manifest.ID, clean.ExecutionID)
			require.NotEmpty(t, clean.CandidateID)
			assertCleanPresentationGuardCommit(t, server, projectRoot, prepared.AttemptID, manifest.ID)
			server.stop(t)
		})
	}
}

// TestRealBinaryRejectsMixedCandidateBatchAtomically proves validation happens
// across the complete file set before any member is persisted or installed.
// The clean lib.rs change must not leak when a sibling contains rustfmt's
// absolute filename banner.
func TestRealBinaryRejectsMixedCandidateBatchAtomically(t *testing.T) {
	binary := buildCrestSpecBinary(t, repositoryRoot(t))
	projectRoot := createRustPresentationGuardProject(t)
	before := snapshotPresentationGuardRepository(t, projectRoot)
	server := startCrestSpec(t, binary, projectRoot)
	started := startPresentationGuardExecution(t, server)
	malformedPath := "src/generated.rs"
	malformedAbsolutePath := filepath.Join(projectRoot, "src", "generated.rs")

	commit := callToolResult(t, server, "spec/commit", map[string]any{
		"attempt_id": started.Prepared.AttemptID,
		"files": []map[string]any{
			{"path": "src/lib.rs", "content": rustPresentationGuardCleanReplacement},
			{"path": malformedPath, "content": malformedAbsolutePath + ":\n\n" + rustPresentationGuardSource},
		},
		"notes": "one valid file must not make a malformed sibling partially durable",
	})
	require.True(t, commit.IsError, "one malformed member must reject the complete candidate set")
	require.Contains(t, toolResultText(commit), "contains presentation metadata")
	require.Contains(t, toolResultText(commit), "submit only exact raw repository bytes")
	assertPresentationGuardState(
		t, server, projectRoot, started.Prepared.AttemptID, started.Manifest.ID, before,
	)
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM generated_files WHERE path IN ('src/lib.rs','src/generated.rs')"))
	require.NoFileExists(t, filepath.Join(projectRoot, "src", "generated.rs"))

	server.crash(t)
	server = startCrestSpec(t, binary, projectRoot)
	callTool[specmod.SessionStatusResult](t, server, "spec/status", map[string]any{
		"session_id": started.SessionID,
	})
	assertPresentationGuardState(
		t, server, projectRoot, started.Prepared.AttemptID, started.Manifest.ID, before,
	)
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM generated_files WHERE path IN ('src/lib.rs','src/generated.rs')"))
	require.NoFileExists(t, filepath.Join(projectRoot, "src", "generated.rs"))
	server.stop(t)
}

type presentationGuardExecution struct {
	SessionID string
	Prepared  *specmod.ContextResult
	Manifest  store.ExecutionManifest
}

func startPresentationGuardExecution(t *testing.T, server *crestSpecProcess) presentationGuardExecution {
	t.Helper()
	validation := callTool[specmod.ValidateResult](t, server, "spec/validate", map[string]any{})
	require.True(t, validation.Valid, "fixture spec must validate: %+v", validation)
	begin := callTool[specmod.BeginResult](t, server, "spec/begin", map[string]any{})
	batch := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": begin.SessionID, "limit": 1, "budget_tokens": 32768,
	})
	require.Len(t, batch.Contexts, 1)
	item := batch.Contexts[0]
	require.Empty(t, item.Error)
	require.NotNil(t, item.Context)
	prepared := item.Context
	require.False(t, prepared.Blocked, prepared.BlockedReason)
	require.False(t, prepared.NoDispatch)
	require.NotEmpty(t, prepared.DispatchID)
	requireContextIncludesRepositoryPath(t, prepared, "src/lib.rs")

	dispatch := map[string]any{
		"dispatch_id": prepared.DispatchID, "session_id": begin.SessionID,
		"resource_id": item.ResourceID, "host_session_id": "presentation-guard-host",
		"worker_id": "presentation-guard-worker",
	}
	dispatch["event"] = "dispatch_requested"
	callTool[store.DispatchLifecycle](t, server, "spec/dispatch_event", dispatch)
	dispatch["event"] = "worker_acknowledged"
	callTool[store.DispatchLifecycle](t, server, "spec/dispatch_event", dispatch)

	manifest := callTool[store.ExecutionManifest](t, server, "spec/execution_start", map[string]any{
		"attempt_id": prepared.AttemptID, "protocol_version": execution.ProtocolVersion,
		"idempotency_key": "presentation-guard-" + prepared.AttemptID,
		"context_hash":    prepared.ContextHash, "role": prepared.Role,
		"host_name": "crest-spec-presentation-guard-e2e", "host_version": "1",
		"provider": "deterministic-fixture", "model": "deterministic-candidate-map",
		"host_session_id": "presentation-guard-host", "template_hashes": prepared.TemplateHashes,
		"system_instructions": prepared.SystemPrompt,
	})
	return presentationGuardExecution{SessionID: begin.SessionID, Prepared: prepared, Manifest: manifest}
}

const rustPresentationGuardSource = "pub fn stable_value() -> u32 {\n    7\n}\n"
const rustPresentationGuardCleanReplacement = "pub fn stable_value() -> u32 {\n    11\n}\n"

func createRustPresentationGuardProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, "Cargo.toml", "[package]\nname = \"presentation-guard\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeFixtureFile(t, root, "src/lib.rs", rustPresentationGuardSource)
	writeFixtureFile(t, root, "spec/spec.cue", strings.Replace(counterSpec, `language: "go"`, `language: "rust"`, 1))
	return root
}

func requireContextIncludesRepositoryPath(t *testing.T, prepared *specmod.ContextResult, expectedPath string) {
	t.Helper()
	for _, section := range prepared.Sections {
		if filepath.ToSlash(section.SourcePath) == filepath.ToSlash(expectedPath) &&
			(section.Decision == "included" || section.Decision == "truncated") {
			return
		}
	}
	t.Fatalf("prepared context did not include repository path %s", expectedPath)
}

func assertPresentationGuardState(
	t *testing.T,
	server *crestSpecProcess,
	projectRoot, attemptID, executionID string,
	expected []presentationGuardRepositoryFile,
) {
	t.Helper()
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM candidate_sets WHERE attempt_id = "+sqlLiteral(attemptID)),
		"presentation metadata must fail before immutable candidate persistence")
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM workspace_commit_journals WHERE execution_id = "+sqlLiteral(executionID)),
		"presentation metadata must fail before workspace journal creation")
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM generated_files WHERE path = 'src/lib.rs'"),
		"presentation metadata must not become accepted ownership")
	require.Equal(t, expected, snapshotPresentationGuardRepository(t, projectRoot),
		"presentation metadata changed repository bytes")
}

func assertCleanPresentationGuardCommit(
	t *testing.T,
	server *crestSpecProcess,
	projectRoot, attemptID, executionID string,
) {
	t.Helper()
	require.Equal(t, 1, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM candidate_sets WHERE attempt_id = "+sqlLiteral(attemptID)+" AND status = 'accepted'"))
	require.Equal(t, 1, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM workspace_commit_journals WHERE execution_id = "+sqlLiteral(executionID)+" AND status = 'committed'"))
	require.Equal(t, 1, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM generated_files WHERE path = 'src/lib.rs'"))
	contents, err := os.ReadFile(filepath.Join(projectRoot, "src", "lib.rs"))
	require.NoError(t, err)
	require.Equal(t, rustPresentationGuardCleanReplacement, string(contents),
		"the clean retry must materialize exact raw source bytes")
}

type presentationGuardRepositoryFile struct {
	Path    string
	Content string
}

func snapshotPresentationGuardRepository(t *testing.T, root string) []presentationGuardRepositoryFile {
	t.Helper()
	contentsByPath := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		first := strings.Split(filepath.ToSlash(relative), "/")[0]
		if entry.IsDir() && (first == ".crest-spec" || first == "target") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		contentsByPath[filepath.ToSlash(relative)] = string(contents)
		return nil
	})
	require.NoError(t, err)

	paths := make([]string, 0, len(contentsByPath))
	for path := range contentsByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]presentationGuardRepositoryFile, 0, len(paths))
	for _, path := range paths {
		result = append(result, presentationGuardRepositoryFile{Path: path, Content: contentsByPath[path]})
	}
	return result
}
