package spec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/engine"
)

type mockEngine struct {
	generateFn func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error)
	reviewFn   func(ctx context.Context, opts engine.ReviewOpts) (*agent.RunResult, error)
}

func (m *mockEngine) Generate(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error) {
	if m.generateFn != nil {
		return m.generateFn(ctx, opts)
	}
	return &agent.RunResult{Output: ""}, nil
}

func (m *mockEngine) Review(ctx context.Context, opts engine.ReviewOpts) (*agent.RunResult, error) {
	if m.reviewFn != nil {
		return m.reviewFn(ctx, opts)
	}
	return &agent.RunResult{Output: "PASS"}, nil
}

func (m *mockEngine) CodeReview(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error) {
	return &agent.RunResult{Output: "PASS: no issues found"}, nil
}

func (m *mockEngine) Bugbot(ctx context.Context, opts engine.BugbotOpts) (*agent.RunResult, error) {
	return &agent.RunResult{Output: "No bugs found"}, nil
}

func TestConstraintLoop_PassOnFirstTry(t *testing.T) {
	eng := &mockEngine{
		generateFn: func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error) {
			return &agent.RunResult{
				Output: "```go\n// path: src/voice.go\npackage voice\n```\n",
			}, nil
		},
	}

	result, err := runConstraintLoop(context.Background(), eng, LoopOpts{
		Prompt:      "generate voice",
		Model:       "test-model",
		MaxRetries:  3,
		ReviewLevel: "skip",
	})

	require.NoError(t, err)
	assert.Equal(t, "accepted", result.Outcome)
	assert.Equal(t, 1, result.Attempts)
	require.Len(t, result.Files, 1)
	assert.Equal(t, "src/voice.go", result.Files[0].Path)
}

func TestConstraintLoop_RetryOnParseFailure(t *testing.T) {
	calls := 0
	eng := &mockEngine{
		generateFn: func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error) {
			calls++
			if calls == 1 {
				return &agent.RunResult{Output: "I can't generate that"}, nil
			}
			return &agent.RunResult{
				Output: "```go\n// path: src/voice.go\npackage voice\n```\n",
			}, nil
		},
	}

	result, err := runConstraintLoop(context.Background(), eng, LoopOpts{
		Prompt:      "generate voice",
		Model:       "test-model",
		MaxRetries:  3,
		ReviewLevel: "skip",
	})

	require.NoError(t, err)
	assert.Equal(t, "accepted", result.Outcome)
	assert.Equal(t, 2, result.Attempts)
}

func TestConstraintLoop_ExhaustedRetries(t *testing.T) {
	eng := &mockEngine{
		generateFn: func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "I can't do this"}, nil
		},
	}

	result, err := runConstraintLoop(context.Background(), eng, LoopOpts{
		Prompt:      "generate voice",
		Model:       "test-model",
		MaxRetries:  2,
		ReviewLevel: "skip",
	})

	require.NoError(t, err)
	assert.Equal(t, "rejected", result.Outcome)
	assert.Equal(t, 3, result.Attempts) // initial + 2 retries
}
