package store

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/execution"
)

func TestValidateCandidateRepositoryBytesRejectsPresentationMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		content string
		kind    string
	}{
		{
			name:    "file-mode rustfmt absolute path banner",
			path:    "src/control/event_log.rs",
			content: "/Users/example/workspace/crest-synth/src/control/event_log.rs:\n\nuse crate::control::AppEvent;\n",
			kind:    "absolute-path formatter banner",
		},
		{
			name:    "Windows absolute path banner",
			path:    "src/control/event_log.rs",
			content: "C:\\workspace\\crest-synth\\src\\control\\event_log.rs:\r\n\r\nuse crate::control::AppEvent;\r\n",
			kind:    "absolute-path formatter banner",
		},
		{
			name: "UPDATE MODE envelope",
			path: "src/control/event_log.rs",
			content: "UPDATE MODE: preserve the existing implementation and make the smallest change that satisfies the task.\n\n" +
				"File: src/control/event_log.rs\n\n```\nuse crate::control::AppEvent;\n\n```\n",
			kind: "UPDATE MODE envelope",
		},
		{
			name:    "File envelope",
			path:    "src/control/event_log.rs",
			content: "File: src/control/event_log.rs\n\n```rust\nuse crate::control::AppEvent;\n```\n",
			kind:    "File envelope",
		},
		{
			name:    "code fence envelope",
			path:    "src/control/event_log.rs",
			content: "```rust\nuse crate::control::AppEvent;\n```\n",
			kind:    "code-fence envelope",
		},
		{
			name:    "code fence with trailing model commentary",
			path:    "src/control/event_log.rs",
			content: "```rust\nuse crate::control::AppEvent;\n```\nThe requested change is complete.\n",
			kind:    "code-fence envelope",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateCandidateRepositoryBytes(test.path, []byte(test.content))
			require.ErrorContains(t, err, "contains presentation metadata")
			require.ErrorContains(t, err, test.kind)
			require.ErrorContains(t, err, "submit only exact raw repository bytes")
		})
	}
}

func TestValidateCandidateRepositoryBytesPreservesLegitimateRawBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		content string
	}{
		{
			name:    "stdin-mode rustfmt source without final newline",
			path:    "src/control/event_log.rs",
			content: "use crate::control::AppEvent;\n\npub fn record(event: AppEvent) {}",
		},
		{
			name:    "Rust label",
			path:    "src/control/event_log.rs",
			content: "'retry: loop { break 'retry; }\n",
		},
		{
			name:    "absolute banner for another candidate is ordinary content",
			path:    "src/control/event_log.rs",
			content: "/Users/example/workspace/crest-synth/src/control/state_projector.rs:\n\nuse crate::control::AppEvent;\n",
		},
		{
			name:    "same absolute path without formatter blank line",
			path:    "src/control/event_log.rs",
			content: "/Users/example/workspace/crest-synth/src/control/event_log.rs:\nuse crate::control::AppEvent;\n",
		},
		{
			name:    "Markdown code fence is repository content",
			path:    "docs/example.md",
			content: "```rust\nfn example() {}\n```\n",
		},
		{
			name:    "Markdown prose may use UPDATE MODE wording",
			path:    "docs/update-mode.md",
			content: "UPDATE MODE: this section documents the worker protocol.\n\nNo file envelope follows.\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, ValidateCandidateRepositoryBytes(test.path, []byte(test.content)))
		})
	}
}

func TestSubmitCandidateRejectsPresentationBytesWithoutConsumingAttempt(t *testing.T) {
	st := newContextManifestStore(t, "resource.presentation-guard")
	ctx := t.Context()
	prepareAndClaimTestContext(t, st, testContextManifest(
		"attempt-1", "manifest-1", "resource.presentation-guard",
	))
	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)

	const path = "src/control/event_log.rs"
	malformed := "/Users/example/workspace/crest-synth/" + path + ":\n\nuse crate::control::AppEvent;\n"
	_, err = st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: path, Content: malformed, WriteIntent: "modify",
	}})
	require.ErrorContains(t, err, "absolute-path formatter banner")
	_, err = st.GetCandidateSetByAttempt(ctx, "attempt-1")
	require.Error(t, err, "rejected presentation bytes must not enter candidate storage")
	stillExecuting, err := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	require.Equal(t, "executing", stillExecuting.Status)
	contextManifest, err := st.GetContextManifestByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, "context_prepared", contextManifest.Attempt.Status)

	raw := "use crate::control::AppEvent;\n\npub fn record(event: AppEvent) {}"
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: path, Content: raw, WriteIntent: "modify",
	}})
	require.NoError(t, err, "the same execution must be immediately resubmittable")
	require.Equal(t, raw, candidate.Files[0].Content, "successful validation must preserve exact repository bytes")
	require.Equal(t, contextmanifest.Hash(raw), candidate.Files[0].ContentHash)
	require.Equal(t, len([]byte(raw)), candidate.Files[0].ByteSize)
}

func TestPrepareWorkspaceCommitJournalRejectsPresentationBatchAtomically(t *testing.T) {
	st := newContextManifestStore(t, "resource.presentation-journal")
	ctx := t.Context()
	prepareAndClaimTestContext(t, st, testContextManifest(
		"attempt-1", "manifest-1", "resource.presentation-journal",
	))
	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)

	const path = "src/control/event_log.rs"
	malformed := []byte("/Users/example/workspace/crest-synth/" + path + ":\n\nuse crate::control::AppEvent;\n")
	journal, err := st.PrepareWorkspaceCommitJournal(ctx, WorkspaceCommitJournalInput{
		ExecutionID: started.ID,
		ProjectRoot: "/Users/example/workspace/crest-synth",
		Files: []WorkspaceCommitJournalFile{
			{
				Path: "src/control/state.rs", CandidateContent: []byte("pub struct State;\n"),
				CandidateContentHash: hashWorkspaceBytes([]byte("pub struct State;\n")), CandidateMode: 0o644,
				WriteIntent: "create", Ordinal: 0,
			},
			{
				Path: path, CandidateContent: malformed, CandidateContentHash: hashWorkspaceBytes(malformed),
				CandidateMode: 0o644, WriteIntent: "create", Ordinal: 1,
			},
		},
	})
	require.Nil(t, journal)
	require.ErrorContains(t, err, "absolute-path formatter banner")
	pending, listErr := st.ListPendingWorkspaceCommitJournals(ctx)
	require.NoError(t, listErr)
	require.Empty(t, pending, "journal validation must roll back the entire batch")
	manifest, manifestErr := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, manifestErr)
	require.Equal(t, string(execution.StatusExecuting), manifest.Status)
}

func TestFinalizeCandidateRefusesLegacyStoredPresentationBytes(t *testing.T) {
	st := newContextManifestStore(t, "resource.presentation-finalize")
	ctx := t.Context()
	prepareAndClaimTestContext(t, st, testContextManifest(
		"attempt-1", "manifest-1", "resource.presentation-finalize",
	))
	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)
	const path = "src/control/event_log.rs"
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: path, Content: "use crate::control::AppEvent;\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID, ToStatus: execution.StatusValidating,
	})
	require.NoError(t, err)

	malformed := "/Users/example/workspace/crest-synth/" + path + ":\n\nuse crate::control::AppEvent;\n"
	malformedHash := contextmanifest.Hash(malformed)
	_, err = st.sqlDB.ExecContext(ctx, `
		INSERT INTO content_blobs (hash, content, byte_size, created_at)
		VALUES (?, ?, ?, ?)`, malformedHash, []byte(malformed), len([]byte(malformed)), now())
	require.NoError(t, err)
	_, err = st.sqlDB.ExecContext(ctx, `
		UPDATE candidate_files
		SET content_blob = ?, content_hash = ?, byte_size = ?
		WHERE candidate_id = ?`, malformedHash, malformedHash, len([]byte(malformed)), candidate.ID)
	require.NoError(t, err)

	accepted, err := st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: started.ID,
		Accepted:    true,
		Resource: &Resource{
			ID: "resource.presentation-finalize", Kind: "asset", DeclarationHash: "declaration",
			EffectiveHash: "effective", GenerationInputHash: "generation", DependencyContractHash: "dependencies",
		},
		Files: []GeneratedFile{{
			Path: path, ResourceID: "resource.presentation-finalize", ContentHash: malformedHash,
		}},
	})
	require.Nil(t, accepted)
	require.ErrorContains(t, err, "absolute-path formatter banner")
	manifest, manifestErr := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, manifestErr)
	require.Equal(t, string(execution.StatusValidating), manifest.Status, "acceptance guard must fail before terminal projections")
	persisted, candidateErr := st.GetCandidateSetByAttempt(ctx, "attempt-1")
	require.NoError(t, candidateErr)
	require.Equal(t, "submitted", persisted.Status)
	require.Equal(t, malformed, persisted.Files[0].Content, "legacy corrupt content must remain inspectable for governed rejection")
}

func TestTransitionExecutionRefusesLegacyStoredPresentationBytes(t *testing.T) {
	st := newContextManifestStore(t, "resource.presentation-transition")
	ctx := t.Context()
	prepareAndClaimTestContext(t, st, testContextManifest(
		"attempt-1", "manifest-1", "resource.presentation-transition",
	))
	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)
	const path = "src/control/event_log.rs"
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: path, Content: "use crate::control::AppEvent;\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID, ToStatus: execution.StatusValidating,
	})
	require.NoError(t, err)

	malformed := "/Users/example/workspace/crest-synth/" + path + ":\n\nuse crate::control::AppEvent;\n"
	malformedHash := contextmanifest.Hash(malformed)
	_, err = st.sqlDB.ExecContext(ctx, `
		INSERT INTO content_blobs (hash, content, byte_size, created_at)
		VALUES (?, ?, ?, ?)`, malformedHash, []byte(malformed), len([]byte(malformed)), now())
	require.NoError(t, err)
	_, err = st.sqlDB.ExecContext(ctx, `
		UPDATE candidate_files
		SET content_blob = ?, content_hash = ?, byte_size = ?
		WHERE candidate_id = ?`, malformedHash, malformedHash, len([]byte(malformed)), candidate.ID)
	require.NoError(t, err)

	accepted, err := st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID, ToStatus: execution.StatusAccepted,
	})
	require.Nil(t, accepted)
	require.ErrorContains(t, err, "absolute-path formatter banner")
	manifest, manifestErr := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, manifestErr)
	require.Equal(t, string(execution.StatusValidating), manifest.Status, "direct acceptance must fail before terminal projections")
	persisted, candidateErr := st.GetCandidateSetByAttempt(ctx, "attempt-1")
	require.NoError(t, candidateErr)
	require.Equal(t, "submitted", persisted.Status)
}
