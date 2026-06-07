package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
)

// fakeRunner records all RunPrompt calls and allows configurable return
// behaviour per method.
type fakeRunner struct {
	mu             sync.Mutex
	runPromptCalls []agent.RunOpts
	runPromptFn    func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error)
	modelsFn       func(ctx context.Context) (string, error)
	aboutFn        func(ctx context.Context) (string, error)
	statusFn       func(ctx context.Context) (string, error)
}

func (f *fakeRunner) RunPrompt(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
	f.mu.Lock()
	f.runPromptCalls = append(f.runPromptCalls, opts)
	f.mu.Unlock()
	if f.runPromptFn != nil {
		return f.runPromptFn(ctx, opts)
	}
	return &agent.RunResult{Output: "ok"}, nil
}

func (f *fakeRunner) Models(ctx context.Context) (string, error) {
	if f.modelsFn != nil {
		return f.modelsFn(ctx)
	}
	return "model-list", nil
}

func (f *fakeRunner) About(ctx context.Context) (string, error) {
	if f.aboutFn != nil {
		return f.aboutFn(ctx)
	}
	return "about-info", nil
}

func (f *fakeRunner) Status(ctx context.Context) (string, error) {
	if f.statusFn != nil {
		return f.statusFn(ctx)
	}
	return "status-info", nil
}

func (f *fakeRunner) calls() []agent.RunOpts {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]agent.RunOpts, len(f.runPromptCalls))
	copy(cp, f.runPromptCalls)
	return cp
}

func defaultCfg() *config.Config {
	return &config.Config{
		MaxConcurrency: 5,
		GenerateModel:  "gen-model",
		VerifyModel:    "verify-model",
	}
}

func newEngine(r *fakeRunner) *Engine {
	return New(r, defaultCfg())
}

// ---------- Generate tests ----------

func TestGenerate_DisallowedTools(t *testing.T) {
	r := &fakeRunner{}
	e := newEngine(r)

	_, err := e.Generate(context.Background(), GenerateOpts{Prompt: "hello"})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)

	// Sub-agents now run with full tool access so they can read existing files,
	// run the build, and self-verify their output. Nothing is disallowed.
	assert.Empty(t, calls[0].DisallowedTools)
}

func TestGenerate_NoSessionPersistence(t *testing.T) {
	r := &fakeRunner{}
	e := newEngine(r)

	_, err := e.Generate(context.Background(), GenerateOpts{Prompt: "hello"})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)
	assert.True(t, calls[0].NoSessionPersistence)
}

func TestGenerate_SystemPromptPassedThrough(t *testing.T) {
	r := &fakeRunner{}
	e := newEngine(r)

	_, err := e.Generate(context.Background(), GenerateOpts{
		Prompt:             "hello",
		AppendSystemPrompt: "custom-system-prompt",
	})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "custom-system-prompt", calls[0].AppendSystemPrompt)
}

func TestGenerate_ModelDefault(t *testing.T) {
	r := &fakeRunner{}
	e := newEngine(r)

	_, err := e.Generate(context.Background(), GenerateOpts{Prompt: "hello"})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "gen-model", calls[0].Model)
}

func TestGenerate_ModelOverride(t *testing.T) {
	r := &fakeRunner{}
	e := newEngine(r)

	_, err := e.Generate(context.Background(), GenerateOpts{
		Prompt: "hello",
		Model:  "custom-model",
	})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "custom-model", calls[0].Model)
}

func TestGenerate_ContextCancellation(t *testing.T) {
	cfg := &config.Config{MaxConcurrency: 1, GenerateModel: "gen-model"}
	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			// Block until context is cancelled.
			<-ctx.Done()
			return &agent.RunResult{Output: "cancelled"}, ctx.Err()
		},
	}
	e := New(r, cfg)

	// Fill the semaphore with a blocking call.
	go func() {
		e.Generate(context.Background(), GenerateOpts{Prompt: "blocker"}) //nolint:errcheck
	}()
	// Give the goroutine time to acquire the semaphore.
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := e.Generate(ctx, GenerateOpts{Prompt: "should-fail"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestGenerate_RunnerError(t *testing.T) {
	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "partial"}, fmt.Errorf("runner broke")
		},
	}
	e := newEngine(r)

	result, err := e.Generate(context.Background(), GenerateOpts{Prompt: "hello"})
	require.Error(t, err)
	assert.Equal(t, "partial", result.Output)
	assert.Contains(t, err.Error(), "runner broke")
}

// ---------- Review tests ----------

func TestReview_PromptIncludesCodeAndRequirements(t *testing.T) {
	r := &fakeRunner{}
	e := newEngine(r)

	_, err := e.Review(context.Background(), ReviewOpts{
		Code:         "func main() {}",
		Requirements: "must compile",
	})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)

	prompt := calls[0].Prompt
	assert.Contains(t, prompt, "func main() {}")
	assert.Contains(t, prompt, "must compile")
	assert.Contains(t, prompt, "PASS")
	assert.Contains(t, prompt, "FAIL")
}

func TestReview_ModelDefault(t *testing.T) {
	r := &fakeRunner{}
	e := newEngine(r)

	_, err := e.Review(context.Background(), ReviewOpts{
		Code:         "code",
		Requirements: "reqs",
	})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "verify-model", calls[0].Model)
}

func TestReview_DisallowedTools(t *testing.T) {
	r := &fakeRunner{}
	e := newEngine(r)

	_, err := e.Review(context.Background(), ReviewOpts{
		Code:         "code",
		Requirements: "reqs",
	})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)

	// Review sub-agents also run with full tool access (see Generate above).
	assert.Empty(t, calls[0].DisallowedTools)
}

// ---------- CodeReview tests ----------

func TestCodeReview_DefaultModels(t *testing.T) {
	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "review for " + opts.Model}, nil
		},
	}
	e := newEngine(r)

	_, err := e.CodeReview(context.Background(), CodeReviewOpts{Prompt: "review this"})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 2)

	models := make(map[string]bool)
	for _, c := range calls {
		models[c.Model] = true
	}
	assert.True(t, models["claude-opus-4-6"])
	assert.True(t, models["claude-sonnet-4-6"])
}

func TestCodeReview_ExplicitModels(t *testing.T) {
	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "review"}, nil
		},
	}
	e := newEngine(r)

	_, err := e.CodeReview(context.Background(), CodeReviewOpts{
		Prompt: "review this",
		Models: []string{"custom-model"},
	})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "custom-model", calls[0].Model)
}

func TestCodeReview_AggregatesResults(t *testing.T) {
	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "output-from-" + opts.Model}, nil
		},
	}
	e := newEngine(r)

	result, err := e.CodeReview(context.Background(), CodeReviewOpts{
		Prompt: "review",
		Models: []string{"model-a", "model-b"},
	})
	require.NoError(t, err)

	assert.Contains(t, result.Output, "## Model: model-a")
	assert.Contains(t, result.Output, "## Model: model-b")
	assert.Contains(t, result.Output, "output-from-model-a")
	assert.Contains(t, result.Output, "output-from-model-b")
}

func TestCodeReview_PartialFailure(t *testing.T) {
	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			if opts.Model == "bad-model" {
				return nil, fmt.Errorf("model failed")
			}
			return &agent.RunResult{Output: "ok"}, nil
		},
	}
	e := newEngine(r)

	_, err := e.CodeReview(context.Background(), CodeReviewOpts{
		Prompt: "review",
		Models: []string{"good-model", "bad-model"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad-model")
}

func TestCodeReview_SetsCwd(t *testing.T) {
	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "ok"}, nil
		},
	}
	e := newEngine(r)

	_, err := e.CodeReview(context.Background(), CodeReviewOpts{
		Prompt: "review",
		Models: []string{"m1"},
		Cwd:    "/some/path",
	})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "/some/path", calls[0].Cwd)
}

// ---------- Bugbot tests ----------

func TestBugbot_DefaultModels(t *testing.T) {
	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "bugs"}, nil
		},
	}
	e := newEngine(r)

	_, err := e.Bugbot(context.Background(), BugbotOpts{Prompt: "find bugs"})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "claude-sonnet-4-6", calls[0].Model)
}

func TestBugbot_AggregatesResults(t *testing.T) {
	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "bugs-from-" + opts.Model}, nil
		},
	}
	e := newEngine(r)

	result, err := e.Bugbot(context.Background(), BugbotOpts{
		Prompt: "find bugs",
		Models: []string{"model-x", "model-y"},
	})
	require.NoError(t, err)

	assert.Contains(t, result.Output, "## Model: model-x")
	assert.Contains(t, result.Output, "## Model: model-y")
	assert.Contains(t, result.Output, "bugs-from-model-x")
	assert.Contains(t, result.Output, "bugs-from-model-y")
}

// ---------- Pass-through tests ----------

func TestModels_PassThrough(t *testing.T) {
	r := &fakeRunner{
		modelsFn: func(ctx context.Context) (string, error) {
			return "model-list-output", nil
		},
	}
	e := newEngine(r)

	result, err := e.Models(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "model-list-output", result)
}

func TestAbout_PassThrough(t *testing.T) {
	r := &fakeRunner{
		aboutFn: func(ctx context.Context) (string, error) {
			return "about-output", nil
		},
	}
	e := newEngine(r)

	result, err := e.About(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "about-output", result)
}

func TestStatus_PassThrough(t *testing.T) {
	r := &fakeRunner{
		statusFn: func(ctx context.Context) (string, error) {
			return "status-output", nil
		},
	}
	e := newEngine(r)

	result, err := e.Status(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "status-output", result)
}

// ---------- Semaphore test ----------

func TestSemaphore_LimitsConcurrency(t *testing.T) {
	cfg := &config.Config{MaxConcurrency: 1, GenerateModel: "gen-model"}

	var running atomic.Int32
	var maxRunning atomic.Int32

	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			cur := running.Add(1)
			defer running.Add(-1)

			// Track peak concurrency.
			for {
				old := maxRunning.Load()
				if cur <= old || maxRunning.CompareAndSwap(old, cur) {
					break
				}
			}

			time.Sleep(50 * time.Millisecond)
			return &agent.RunResult{Output: "ok"}, nil
		},
	}
	e := New(r, cfg)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.Generate(context.Background(), GenerateOpts{Prompt: "hello"})
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), maxRunning.Load(), "expected max concurrency of 1")

	calls := r.calls()
	assert.Len(t, calls, 3, "all 3 calls should have completed")
}

// ---------- Bugbot severity template test ----------

func TestBugbot_PromptWrappedInSeverityTemplate(t *testing.T) {
	r := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "ok"}, nil
		},
	}
	e := newEngine(r)

	_, err := e.Bugbot(context.Background(), BugbotOpts{
		Prompt: "my code here",
		Models: []string{"m1"},
	})
	require.NoError(t, err)

	calls := r.calls()
	require.Len(t, calls, 1)
	prompt := calls[0].Prompt
	assert.True(t, strings.Contains(prompt, "severity"), "prompt should mention severity")
	assert.True(t, strings.Contains(prompt, "my code here"), "prompt should contain original prompt")
}
