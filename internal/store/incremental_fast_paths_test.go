package store

import (
	"context"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/stretchr/testify/require"
)

func TestCandidateReuseRequiresExactImmutableInputsAndRecordsZeroExecutionProvenance(t *testing.T) {
	st := newContextManifestStore(t, "resource.synth")
	ctx := context.Background()
	prepareAndClaimTestContext(t, st, testContextManifest("attempt-1", "manifest-1", "resource.synth"))
	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "synth.go", Content: "package synth\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{ExecutionID: started.ID, ToStatus: execution.StatusValidating})
	require.NoError(t, err)
	_, err = st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: started.ID, Accepted: true, Reason: "accepted before interruption",
		Resource: &Resource{
			ID: "resource.synth", Kind: "asset", DeclarationHash: "decl", EffectiveHash: "effective",
			GenerationInputHash: "generation-v1", ExportedContractHash: "export-v1",
			DependencyContractHash: "dependencies-v1", Model: "model",
		},
		Files: []GeneratedFile{{
			Path: "synth.go", ResourceID: "resource.synth", ContentHash: candidate.Files[0].ContentHash,
			PromptHash: started.ContextHash, Model: "model",
		}},
		Attempts: 1,
	})
	require.NoError(t, err)

	_, err = st.FindReusableAcceptedCandidate(ctx, "resource.synth", "generation-v2", "dependencies-v1")
	require.Error(t, err, "different generation inputs must miss")
	_, err = st.FindReusableAcceptedCandidate(ctx, "resource.synth", "generation-v1", "dependencies-v2")
	require.Error(t, err, "different dependency contracts must miss")
	_, err = st.FindReusableAcceptedCandidate(ctx, "resource.synth", "generation-v1", "dependencies-v1")
	require.Error(t, err, "an active source session is never reusable")

	require.NoError(t, st.UpdateSession("session-1", "aborted", 0))
	projected, err := st.GetResource("resource.synth")
	require.NoError(t, err)
	projected.GenerationInputHash = "generation-v2"
	require.NoError(t, st.SetResource(*projected))
	_, err = st.FindReusableAcceptedCandidate(ctx, "resource.synth", "generation-v2", "dependencies-v1")
	require.Error(t, err,
		"migrating the resource projection must not rewrite immutable v1/v2 candidate provenance; reuse waits for a v3 acceptance")
	reusable, err := st.FindReusableAcceptedCandidate(ctx, "resource.synth", "generation-v1", "dependencies-v1")
	require.NoError(t, err)
	require.Equal(t, candidate.ID, reusable.ID)
	require.Equal(t, "generation-v1", reusable.GenerationInputHash)
	require.Equal(t, "dependencies-v1", reusable.DependencyContractHash)

	require.NoError(t, st.CreateApply("apply-2", "spec-2"))
	require.NoError(t, st.CreateSession(Session{ID: "session-2", ApplyID: "apply-2", PlanJSON: "[]", WavesJSON: "[]", HashesJSON: "{}"}))
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-2", ResourceID: "resource.synth", State: "pending",
		ExecutionStrategy: "reuse_candidate", SourceCandidateID: candidate.ID, MaxRetries: 3,
	}))
	reuse, err := st.ApplyCandidateReuse(ctx, CandidateReuseInput{
		SessionID: "session-2", ResourceID: "resource.synth", SourceCandidateID: candidate.ID,
		CandidateHash: candidate.CandidateHash, GenerationInputHash: "generation-v1",
		DependencyContractHash: "dependencies-v1",
	})
	require.NoError(t, err)
	require.Equal(t, candidate.ID, reuse.SourceCandidateID)
	resource, err := st.GetSessionResource("session-2", "resource.synth")
	require.NoError(t, err)
	require.Equal(t, "committed", resource.State)
	require.Equal(t, "integration_pending", resource.Phase)
	require.Zero(t, resource.Attempts)
	attempts, err := st.ListGenerationAttemptsBySession(ctx, "session-2")
	require.NoError(t, err)
	require.Empty(t, attempts, "cache reuse must not fabricate model execution")
	storedReuse, err := st.GetCandidateReuse(ctx, "session-2", "resource.synth")
	require.NoError(t, err)
	require.Equal(t, reuse.ID, storedReuse.ID)
}
