package spec

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func commitTestAttempt(
	t *testing.T,
	s *Spec,
	ctx context.Context,
	sessionID, resourceID string,
	files []CommitFile,
	notes, model string,
) (*CommitResult, error) {
	t.Helper()
	prepared, err := s.PrepareContext(ctx, ContextOptions{SessionID: sessionID, ResourceID: resourceID})
	require.NoError(t, err)
	if model == "" {
		model = "test-model"
	}
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostVersion: "1", Provider: "test", Model: model,
		Tools:          []store.ExecutionTool{{Name: "filesystem", Permission: "test-project"}},
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)
	return s.CommitAttempt(ctx, prepared.AttemptID, files, notes, CommitMetadata{})
}
