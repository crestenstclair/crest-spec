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
	generateFn   func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error)
	reviewFn     func(ctx context.Context, opts engine.ReviewOpts) (*agent.RunResult, error)
	codeReviewFn func(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error)
	bugbotFn     func(ctx context.Context, opts engine.BugbotOpts) (*agent.RunResult, error)
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
	if m.codeReviewFn != nil {
		return m.codeReviewFn(ctx, opts)
	}
	return &agent.RunResult{Output: "PASS: no issues found"}, nil
}

func (m *mockEngine) Bugbot(ctx context.Context, opts engine.BugbotOpts) (*agent.RunResult, error) {
	if m.bugbotFn != nil {
		return m.bugbotFn(ctx, opts)
	}
	return &agent.RunResult{Output: "No bugs found"}, nil
}

func (m *mockEngine) ActiveCount() int    { return 0 }
func (m *mockEngine) MaxConcurrency() int { return 5 }

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

func TestParseReviewOutput_ValidJSON(t *testing.T) {
	input := `{"passed": false, "findings": [{"severity": "critical", "description": "nil pointer", "file": "main.go", "line": 42}], "summary": "Found issues"}`
	ro := parseReviewOutput(input)
	require.NotNil(t, ro)
	assert.False(t, ro.Passed)
	assert.Equal(t, "Found issues", ro.Summary)
	require.Len(t, ro.Findings, 1)
	assert.Equal(t, "critical", ro.Findings[0].Severity)
	assert.Equal(t, "nil pointer", ro.Findings[0].Description)
	assert.Equal(t, "main.go", ro.Findings[0].File)
	assert.Equal(t, 42, ro.Findings[0].Line)
}

func TestParseReviewOutput_WrappedInMarkdown(t *testing.T) {
	input := "Here is my review:\n```json\n{\"passed\": true, \"summary\": \"All good\"}\n```\n"
	ro := parseReviewOutput(input)
	require.NotNil(t, ro)
	assert.True(t, ro.Passed)
	assert.Equal(t, "All good", ro.Summary)
}

func TestParseReviewOutput_SurroundingProse(t *testing.T) {
	input := "After careful review, I conclude:\n{\"passed\": true, \"findings\": [], \"summary\": \"No issues\"}\nThat's my assessment."
	ro := parseReviewOutput(input)
	require.NotNil(t, ro)
	assert.True(t, ro.Passed)
}

func TestParseReviewOutput_InvalidJSON(t *testing.T) {
	input := "PASS: the code looks good, no issues found"
	ro := parseReviewOutput(input)
	assert.Nil(t, ro)
}

func TestParseReviewOutput_PassedTrue(t *testing.T) {
	input := `{"passed": true, "summary": "Looks good"}`
	ro := parseReviewOutput(input)
	require.NotNil(t, ro)
	assert.True(t, ro.Passed)
}

func TestParseReviewOutput_MultiModelAllPass(t *testing.T) {
	input := "## Model: opus\n\n{\"passed\": true, \"summary\": \"ok\"}\n\n" +
		"## Model: sonnet\n\n{\"passed\": true, \"summary\": \"fine\"}\n\n"
	ro := parseReviewOutput(input)
	require.NotNil(t, ro)
	assert.True(t, ro.Passed)
}

func TestParseReviewOutput_MultiModelOneFails(t *testing.T) {
	// One model passes, the other reports a blocking finding. The combined
	// verdict must fail, with findings unioned across sections.
	input := "## Model: opus\n\n{\"passed\": true, \"findings\": [], \"summary\": \"ok\"}\n\n" +
		"## Model: sonnet\n\n{\"passed\": false, \"findings\": [{\"severity\": \"critical\", \"description\": \"data race\"}], \"summary\": \"bug\"}\n\n"
	ro := parseReviewOutput(input)
	require.NotNil(t, ro)
	assert.False(t, ro.Passed)
	require.Len(t, ro.Findings, 1)
	assert.Equal(t, "data race", ro.Findings[0].Description)
}

func TestIndicatesFailure_NegatedKeywordsDoNotFail(t *testing.T) {
	// These are the false-positive cases that previously failed reviews even
	// though the model reported no blocking issues.
	for _, s := range []string{
		"No critical or high-severity bugs. The sharpest finding is minor.",
		"0 critical issues found.",
		"The code is free of critical defects.",
		"No failures detected; all assertions pass.",
		"none of the findings are critical",
		"Critical issues: none found",
		"no findings are critical",
	} {
		assert.False(t, indicatesFailure(s, []string{"critical", "fail"}), "should not fail on %q", s)
	}
}

func TestIndicatesFailure_GenuineFindingsFail(t *testing.T) {
	for _, s := range []string{
		"Found a critical bug: nil deref on line 12.",
		"severity: critical — unbounded allocation",
		"This test will fail because the buffer overflows.",
		"No major issues, but 1 critical finding remains.",
	} {
		assert.True(t, indicatesFailure(s, []string{"critical", "fail"}), "should fail on %q", s)
	}
}

func TestBuildReviewMessage_WithFindings(t *testing.T) {
	ro := &ReviewOutput{
		Passed:  false,
		Summary: "Issues found",
		Findings: []ReviewFinding{
			{Severity: "critical", Description: "bug"},
		},
	}
	msg := buildReviewMessage(ro, "raw output")
	assert.Contains(t, msg, "Issues found")
	assert.Contains(t, msg, "findings:")
	assert.Contains(t, msg, "critical")
}

func TestBuildReviewMessage_NoFindings(t *testing.T) {
	ro := &ReviewOutput{Passed: true, Summary: "All good"}
	msg := buildReviewMessage(ro, "raw output")
	assert.Equal(t, "All good", msg)
}

func TestBuildReviewMessage_NilFallback(t *testing.T) {
	msg := buildReviewMessage(nil, "raw output")
	assert.Equal(t, "raw output", msg)
}

func TestRunReview_FullWithJSON(t *testing.T) {
	eng := &mockEngine{}
	eng.codeReviewFn = func(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error) {
		return &agent.RunResult{Output: `{"passed": true, "summary": "No issues"}`}, nil
	}
	result, err := runReview(context.Background(), eng, "code", LoopOpts{ReviewLevel: "full"})
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, "No issues", result.Message)
}

func TestRunReview_FullFallbackStringMatch(t *testing.T) {
	eng := &mockEngine{}
	eng.codeReviewFn = func(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error) {
		return &agent.RunResult{Output: "PASS: no issues found"}, nil
	}
	result, err := runReview(context.Background(), eng, "code", LoopOpts{ReviewLevel: "full"})
	require.NoError(t, err)
	assert.True(t, result.Passed)
}

func TestRunReview_FullFallbackFail(t *testing.T) {
	eng := &mockEngine{}
	eng.codeReviewFn = func(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error) {
		return &agent.RunResult{Output: "FAIL: missing error handling"}, nil
	}
	result, err := runReview(context.Background(), eng, "code", LoopOpts{ReviewLevel: "full"})
	require.NoError(t, err)
	assert.False(t, result.Passed)
}

func TestRunReview_LightWithJSON(t *testing.T) {
	eng := &mockEngine{}
	eng.bugbotFn = func(ctx context.Context, opts engine.BugbotOpts) (*agent.RunResult, error) {
		return &agent.RunResult{Output: `{"passed": false, "findings": [{"severity": "critical", "description": "data race"}], "summary": "Bug found"}`}, nil
	}
	result, err := runReview(context.Background(), eng, "code", LoopOpts{ReviewLevel: "light"})
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.Contains(t, result.Message, "data race")
}

func TestRunReview_SolidWithJSON(t *testing.T) {
	eng := &mockEngine{}
	eng.reviewFn = func(ctx context.Context, opts engine.ReviewOpts) (*agent.RunResult, error) {
		return &agent.RunResult{Output: `{"passed": true, "summary": "Meets requirements"}`}, nil
	}
	result, err := runReview(context.Background(), eng, "code", LoopOpts{ReviewLevel: "solid", Prompt: "requirements"})
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, "Meets requirements", result.Message)
}

func TestRunReview_DeepWithJSON(t *testing.T) {
	eng := &mockEngine{}
	eng.codeReviewFn = func(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error) {
		assert.Contains(t, opts.Prompt, "SOLID Principles")
		assert.Contains(t, opts.Prompt, "Dependency Injection")
		assert.Contains(t, opts.Prompt, "Code Smells")
		return &agent.RunResult{Output: `{"passed": true, "summary": "Clean code, no issues"}`}, nil
	}
	result, err := runReview(context.Background(), eng, "code", LoopOpts{ReviewLevel: "deep"})
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, "Clean code, no issues", result.Message)
}

func TestRunReview_DeepFallbackFail(t *testing.T) {
	eng := &mockEngine{}
	eng.codeReviewFn = func(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error) {
		return &agent.RunResult{Output: "FAIL: multiple SOLID violations found"}, nil
	}
	result, err := runReview(context.Background(), eng, "code", LoopOpts{ReviewLevel: "deep"})
	require.NoError(t, err)
	assert.False(t, result.Passed)
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

func TestConstraintLoop_EmitsEvents(t *testing.T) {
	eng := &mockEngine{
		generateFn: func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error) {
			return &agent.RunResult{
				Output: "```go\n// path: src/main.go\npackage main\n```\n",
			}, nil
		},
	}

	var events []struct {
		Type    string
		Attempt int
		Content string
	}

	result, err := runConstraintLoop(context.Background(), eng, LoopOpts{
		Prompt:      "generate code",
		Model:       "test-model",
		MaxRetries:  2,
		ReviewLevel: "skip",
		OnEvent: func(eventType string, attempt int, content string) {
			events = append(events, struct {
				Type    string
				Attempt int
				Content string
			}{eventType, attempt, content})
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "accepted", result.Outcome)

	// Verify key events fired in order
	eventTypes := make([]string, len(events))
	for i, e := range events {
		eventTypes[i] = e.Type
	}
	assert.Contains(t, eventTypes, "attempt_start")
	assert.Contains(t, eventTypes, "generate_start")
	assert.Contains(t, eventTypes, "generate_done")
	assert.Contains(t, eventTypes, "validate_start")
	assert.Contains(t, eventTypes, "validate_done")
	assert.Contains(t, eventTypes, "attempt_done")

	// All events should be for attempt 1
	for _, e := range events {
		if e.Type != "stderr" {
			assert.Equal(t, 1, e.Attempt)
		}
	}
}

func TestConstraintLoop_PhaseUpdates(t *testing.T) {
	eng := &mockEngine{
		generateFn: func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error) {
			return &agent.RunResult{
				Output: "```go\n// path: src/main.go\npackage main\n```\n",
			}, nil
		},
	}

	var phases []struct {
		Phase   string
		Attempt int
	}

	mockStore := &phaseTrackingStore{}
	mockStore.phaseUpdates = &phases

	result, err := runConstraintLoop(context.Background(), eng, LoopOpts{
		Prompt:      "generate code",
		Model:       "test-model",
		MaxRetries:  2,
		ReviewLevel: "skip",
		SessionID:   "sess-1",
		ResourceID:  "res-1",
		Store:       mockStore,
	})

	require.NoError(t, err)
	assert.Equal(t, "accepted", result.Outcome)

	// Verify phase transitions happened
	phaseNames := make([]string, len(phases))
	for i, p := range phases {
		phaseNames[i] = p.Phase
	}
	assert.Equal(t, []string{"generating", "validating", "checking_invariants", "reviewing"}, phaseNames)
}

// phaseTrackingStore implements specStore, tracking UpdateSessionResourcePhase calls.
type phaseTrackingStore struct {
	stubStore
	phaseUpdates *[]struct {
		Phase   string
		Attempt int
	}
}

func (s *phaseTrackingStore) UpdateSessionResourcePhase(sessionID, resourceID, phase string, attempts int) error {
	*s.phaseUpdates = append(*s.phaseUpdates, struct {
		Phase   string
		Attempt int
	}{phase, attempts})
	return nil
}
