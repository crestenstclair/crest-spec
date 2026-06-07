package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
)

type runner interface {
	RunPrompt(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error)
	Models(ctx context.Context) (string, error)
	About(ctx context.Context) (string, error)
	Status(ctx context.Context) (string, error)
}

type engineStore interface{}

// Engine orchestrates constrained code generation, review, and analysis
// operations using a runner (typically a Claude CLI agent) with concurrency
// limits enforced via a semaphore.
type Engine struct {
	r     runner
	store engineStore
	cfg   *config.Config
	sem   chan struct{}
}

// New creates an Engine with a concurrency-limiting semaphore sized to
// cfg.MaxConcurrency.
func New(r runner, s engineStore, cfg *config.Config) *Engine {
	return &Engine{
		r:     r,
		store: s,
		cfg:   cfg,
		sem:   make(chan struct{}, cfg.MaxConcurrency),
	}
}

func (e *Engine) acquire(ctx context.Context) error {
	select {
	case e.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) release() {
	<-e.sem
}

var disallowedTools = []string{
	"Bash",
	"Read",
	"Edit",
	"Write",
	"Glob",
	"Grep",
	"WebFetch",
	"WebSearch",
}

// GenerateOpts holds the parameters for a constrained code generation call.
type GenerateOpts struct {
	Prompt             string
	Model              string
	AppendSystemPrompt string
}

// Generate runs a constrained code generation prompt. All filesystem and web
// tools are blocked. The model defaults to cfg.GenerateModel when opts.Model
// is empty.
func (e *Engine) Generate(ctx context.Context, opts GenerateOpts) (*agent.RunResult, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()

	model := opts.Model
	if model == "" {
		model = e.cfg.GenerateModel
	}

	runOpts := agent.RunOpts{
		Prompt:               opts.Prompt,
		Model:                model,
		DisallowedTools:      disallowedTools,
		NoSessionPersistence: true,
		AppendSystemPrompt:   opts.AppendSystemPrompt,
	}

	return e.r.RunPrompt(ctx, runOpts)
}

// ReviewOpts holds the parameters for a review call.
type ReviewOpts struct {
	Code         string
	Requirements string
	Model        string
}

// Review runs an LLM verification pass on code against requirements. The
// model is instructed to respond with PASS or FAIL plus a rationale. The
// model defaults to cfg.VerifyModel when opts.Model is empty.
func (e *Engine) Review(ctx context.Context, opts ReviewOpts) (*agent.RunResult, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()

	model := opts.Model
	if model == "" {
		model = e.cfg.VerifyModel
	}

	prompt := fmt.Sprintf(`Review the following code against the requirements. Respond with PASS if the code meets all requirements, or FAIL if it does not. Include a rationale for your decision.

## Requirements

%s

## Code

%s`, opts.Requirements, opts.Code)

	runOpts := agent.RunOpts{
		Prompt:               prompt,
		Model:                model,
		DisallowedTools:      disallowedTools,
		NoSessionPersistence: true,
	}

	return e.r.RunPrompt(ctx, runOpts)
}

// CodeReviewOpts holds the parameters for a multi-model code review.
type CodeReviewOpts struct {
	Prompt string
	Models []string
	Cwd    string
}

var defaultCodeReviewModels = []string{
	"claude-opus-4-6",
	"claude-sonnet-4-6",
	"claude-haiku-3-5",
}

// CodeReview fans out a code review prompt across multiple models concurrently
// and aggregates the results into a single output with "## Model: X" section
// headers. Defaults to opus, sonnet, and haiku when opts.Models is empty.
func (e *Engine) CodeReview(ctx context.Context, opts CodeReviewOpts) (*agent.RunResult, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()

	models := opts.Models
	if len(models) == 0 {
		models = defaultCodeReviewModels
	}

	type modelResult struct {
		model  string
		output string
	}

	var mu sync.Mutex
	results := make([]modelResult, 0, len(models))

	g, gctx := errgroup.WithContext(ctx)
	for _, m := range models {
		m := m
		g.Go(func() error {
			runOpts := agent.RunOpts{
				Prompt:               opts.Prompt,
				Model:                m,
				Cwd:                  opts.Cwd,
				DisallowedTools:      disallowedTools,
				NoSessionPersistence: true,
			}
			res, err := e.r.RunPrompt(gctx, runOpts)
			if err != nil {
				return fmt.Errorf("model %s: %w", m, err)
			}
			mu.Lock()
			results = append(results, modelResult{model: m, output: res.Output})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "## Model: %s\n\n%s\n\n", r.model, r.output)
	}

	return &agent.RunResult{Output: sb.String()}, nil
}

// BugbotOpts holds the parameters for a multi-model bug analysis.
type BugbotOpts struct {
	Prompt string
	Models []string
	Cwd    string
}

var defaultBugbotModels = []string{
	"claude-haiku-3-5",
}

// Bugbot fans out a bug-analysis prompt across models using the same pattern
// as CodeReview. The prompt is wrapped in a severity-ranking template.
// Defaults to haiku when opts.Models is empty.
func (e *Engine) Bugbot(ctx context.Context, opts BugbotOpts) (*agent.RunResult, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()

	models := opts.Models
	if len(models) == 0 {
		models = defaultBugbotModels
	}

	wrappedPrompt := fmt.Sprintf(`Analyze the following code for bugs. Rank each finding by severity (critical, high, medium, low). For each bug found, include:
1. Severity level
2. Description of the bug
3. Location in the code
4. Suggested fix

%s`, opts.Prompt)

	type modelResult struct {
		model  string
		output string
	}

	var mu sync.Mutex
	results := make([]modelResult, 0, len(models))

	g, gctx := errgroup.WithContext(ctx)
	for _, m := range models {
		m := m
		g.Go(func() error {
			runOpts := agent.RunOpts{
				Prompt:               wrappedPrompt,
				Model:                m,
				Cwd:                  opts.Cwd,
				DisallowedTools:      disallowedTools,
				NoSessionPersistence: true,
			}
			res, err := e.r.RunPrompt(gctx, runOpts)
			if err != nil {
				return fmt.Errorf("model %s: %w", m, err)
			}
			mu.Lock()
			results = append(results, modelResult{model: m, output: res.Output})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "## Model: %s\n\n%s\n\n", r.model, r.output)
	}

	return &agent.RunResult{Output: sb.String()}, nil
}

// Models delegates to the runner to list available models.
func (e *Engine) Models(ctx context.Context) (string, error) {
	return e.r.Models(ctx)
}

// About delegates to the runner to return version information.
func (e *Engine) About(ctx context.Context) (string, error) {
	return e.r.About(ctx)
}

// Status delegates to the runner to return authentication status.
func (e *Engine) Status(ctx context.Context) (string, error) {
	return e.r.Status(ctx)
}
