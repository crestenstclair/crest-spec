# crest-spec SP2: Engine + MCP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the engine (concurrency-controlled sub-agent dispatch) and MCP server (JSON-RPC with stdio + HTTP transports, tool definitions, async job model, metrics, recursion guard).

**Architecture:** Engine wraps the agent with Generate/Review/CodeReview/Bugbot behind a concurrency semaphore. MCP server exposes these as JSON-RPC tools over stdio and Streamable HTTP, with async job lifecycle, per-tool metrics, and recursion detection.

**Tech Stack:** Go, zerolog, modernc.org/sqlite, golang.org/x/sync/errgroup, google/uuid

**Spec:** `docs/superpowers/specs/2026-06-06-crest-spec-sp2-engine-mcp-design.md`

---

## File Map

| File | Responsibility |
|------|---------------|
| `internal/engine/engine.go` | Engine struct, runner interface, semaphore, Generate, Review, CodeReview, Bugbot, pass-throughs |
| `internal/engine/engine_test.go` | Engine tests with manual fake runner |
| `internal/mcp/metrics.go` | Per-tool metrics with atomic counters |
| `internal/mcp/metrics_test.go` | Metrics tests |
| `internal/mcp/process.go` | OSProcessTree, processTree interface |
| `internal/mcp/recursion.go` | DetectRecursion function |
| `internal/mcp/recursion_test.go` | Recursion guard tests with fake process tree |
| `internal/mcp/server.go` | Server struct, JSON-RPC types, Run (stdio), ServeHTTP, runAsync, writeResponse |
| `internal/mcp/tools.go` | registerTools, engine tool handlers, spec tool stubs |
| `internal/mcp/handlers.go` | handleInitialize, handleToolsList, handleToolCall, etc. |
| `internal/mcp/server_test.go` | MCP server tests with manual fakes |
| `cmd/crest-spec/main.go` | Updated wiring with engine + MCP server + HTTP transport |

---

### Task 1: Engine Package -- Core Operations

**Files:**
- Create: `internal/engine/engine.go`

- [ ] **Step 1: Create engine directory**

```bash
mkdir -p /Users/crestenstclair/workspace/claude-mcp-server/internal/engine
```

- [ ] **Step 2: Write engine.go with runner interface, Engine struct, and core operations**

```go
package engine

import (
	"context"
	"fmt"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
)

// runner is the package-private interface that agent.Agent satisfies.
type runner interface {
	RunPrompt(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error)
	Models(ctx context.Context) (string, error)
	About(ctx context.Context) (string, error)
	Status(ctx context.Context) (string, error)
}

// engineStore is a placeholder for future SP5 expansion.
type engineStore interface{}

// Engine wraps the runner with higher-level operations and a concurrency semaphore.
type Engine struct {
	r     runner
	store engineStore
	cfg   *config.Config
	sem   chan struct{}
}

// New creates an Engine with a semaphore sized to cfg.MaxConcurrency.
func New(r runner, s engineStore, cfg *config.Config) *Engine {
	return &Engine{
		r:     r,
		store: s,
		cfg:   cfg,
		sem:   make(chan struct{}, cfg.MaxConcurrency),
	}
}

// acquire blocks until a semaphore slot is available or ctx is cancelled.
func (e *Engine) acquire(ctx context.Context) error {
	select {
	case e.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release returns a semaphore slot.
func (e *Engine) release() {
	<-e.sem
}

// Generate runs a constrained code generation prompt with no tool access.
func (e *Engine) Generate(ctx context.Context, prompt, systemPrompt, model string) (*agent.RunResult, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()

	if model == "" {
		model = e.cfg.GenerateModel
	}

	opts := agent.RunOpts{
		Prompt: prompt,
		Model:  model,
		DisallowedTools: []string{
			"Bash", "Read", "Edit", "Write",
			"Glob", "Grep", "WebFetch", "WebSearch",
		},
		NoSessionPersistence: true,
		AppendSystemPrompt:   systemPrompt,
	}

	return e.r.RunPrompt(ctx, opts)
}

// Review runs an LLM verification pass checking code against requirements.
func (e *Engine) Review(ctx context.Context, code, requirements, model string) (*agent.RunResult, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()

	if model == "" {
		model = e.cfg.VerifyModel
	}

	reviewPrompt := fmt.Sprintf(`Review the following code against the stated requirements.

Check for:
- SOLID principles (SRP, OCP, LSP, ISP, DIP)
- Correct folder structure and package organization
- Dependency injection (no class instantiates its own dependencies)
- Interfaces for testability
- Test coverage for key paths
- All declared invariants and constraints

## Requirements

%s

## Code

%s

Reply with PASS if the code meets all requirements, or FAIL followed by specific issues.`, requirements, code)

	opts := agent.RunOpts{
		Prompt: reviewPrompt,
		Model:  model,
		DisallowedTools: []string{
			"Bash", "Read", "Edit", "Write",
			"Glob", "Grep", "WebFetch", "WebSearch",
		},
		NoSessionPersistence: true,
	}

	return e.r.RunPrompt(ctx, opts)
}

// Models returns the list of available models (pass-through, no semaphore).
func (e *Engine) Models(ctx context.Context) (string, error) {
	return e.r.Models(ctx)
}

// About returns the claude CLI version info (pass-through, no semaphore).
func (e *Engine) About(ctx context.Context) (string, error) {
	return e.r.About(ctx)
}

// Status returns the claude auth status (pass-through, no semaphore).
func (e *Engine) Status(ctx context.Context) (string, error) {
	return e.r.Status(ctx)
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/engine/`
Expected: No output (success).

- [ ] **Step 4: Commit**

```bash
git add internal/engine/engine.go
git commit -m "feat(engine): add Engine struct with Generate, Review, and pass-throughs"
```

---

### Task 2: Engine Package -- Fan-Out Operations + Tests

**Files:**
- Modify: `internal/engine/engine.go`
- Create: `internal/engine/engine_test.go`

- [ ] **Step 1: Add golang.org/x/sync dependency**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go get golang.org/x/sync
```

- [ ] **Step 2: Add CodeReview and Bugbot to engine.go**

Append the following to `internal/engine/engine.go` (add `"strings"`, `"sync"`, and `"golang.org/x/sync/errgroup"` to the import block):

```go
// Add to imports:
// "strings"
// "sync"
// "golang.org/x/sync/errgroup"

// CodeReview fans out a code review across multiple models and aggregates results.
func (e *Engine) CodeReview(ctx context.Context, cwd string, models []string, prompt string) (string, error) {
	if len(models) == 0 {
		models = []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-3-5"}
	}

	type modelResult struct {
		model  string
		output string
	}

	var (
		mu      sync.Mutex
		results []modelResult
	)

	g, gctx := errgroup.WithContext(ctx)

	for _, m := range models {
		m := m
		g.Go(func() error {
			if err := e.acquire(gctx); err != nil {
				return err
			}
			defer e.release()

			opts := agent.RunOpts{
				Prompt:               prompt,
				Model:                m,
				Cwd:                  cwd,
				NoSessionPersistence: true,
			}

			res, err := e.r.RunPrompt(gctx, opts)
			if err != nil {
				return fmt.Errorf("model %s: %w", m, err)
			}

			mu.Lock()
			results = append(results, modelResult{model: m, output: res.Output})
			mu.Unlock()
			return nil
		})
	}

	err := g.Wait()

	var sb strings.Builder
	mu.Lock()
	for _, r := range results {
		sb.WriteString("## Model: ")
		sb.WriteString(r.model)
		sb.WriteString("\n\n")
		sb.WriteString(r.output)
		sb.WriteString("\n\n")
	}
	mu.Unlock()

	return sb.String(), err
}

// Bugbot fans out a lightweight severity-ranked bug scan across models.
func (e *Engine) Bugbot(ctx context.Context, cwd string, models []string, prompt string) (string, error) {
	if len(models) == 0 {
		models = []string{"claude-haiku-3-5"}
	}

	bugbotPrompt := fmt.Sprintf(`Scan the following codebase for bugs. For each bug found:
1. State the file and line (if known)
2. Rank severity: critical / high / medium / low
3. Provide a one-line remedy

Focus: %s`, prompt)

	type modelResult struct {
		model  string
		output string
	}

	var (
		mu      sync.Mutex
		results []modelResult
	)

	g, gctx := errgroup.WithContext(ctx)

	for _, m := range models {
		m := m
		g.Go(func() error {
			if err := e.acquire(gctx); err != nil {
				return err
			}
			defer e.release()

			opts := agent.RunOpts{
				Prompt:               bugbotPrompt,
				Model:                m,
				Cwd:                  cwd,
				NoSessionPersistence: true,
			}

			res, err := e.r.RunPrompt(gctx, opts)
			if err != nil {
				return fmt.Errorf("model %s: %w", m, err)
			}

			mu.Lock()
			results = append(results, modelResult{model: m, output: res.Output})
			mu.Unlock()
			return nil
		})
	}

	err := g.Wait()

	var sb strings.Builder
	mu.Lock()
	for _, r := range results {
		sb.WriteString("## Model: ")
		sb.WriteString(r.model)
		sb.WriteString("\n\n")
		sb.WriteString(r.output)
		sb.WriteString("\n\n")
	}
	mu.Unlock()

	return sb.String(), err
}
```

The full import block for `engine.go` after this step should be:

```go
import (
	"context"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
)
```

- [ ] **Step 3: Write engine_test.go with manual fake runner**

```go
package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
)

// fakeRunner is a manual test double for the runner interface.
type fakeRunner struct {
	runPromptCalls []agent.RunOpts
	runPromptFn    func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error)
	modelsFn       func(ctx context.Context) (string, error)
	aboutFn        func(ctx context.Context) (string, error)
	statusFn       func(ctx context.Context) (string, error)
}

func (f *fakeRunner) RunPrompt(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
	f.runPromptCalls = append(f.runPromptCalls, opts)
	if f.runPromptFn != nil {
		return f.runPromptFn(ctx, opts)
	}
	return &agent.RunResult{Output: "fake output"}, nil
}

func (f *fakeRunner) Models(ctx context.Context) (string, error) {
	if f.modelsFn != nil {
		return f.modelsFn(ctx)
	}
	return "claude-opus-4-6, claude-sonnet-4-6", nil
}

func (f *fakeRunner) About(ctx context.Context) (string, error) {
	if f.aboutFn != nil {
		return f.aboutFn(ctx)
	}
	return "claude-code v1.0.0", nil
}

func (f *fakeRunner) Status(ctx context.Context) (string, error) {
	if f.statusFn != nil {
		return f.statusFn(ctx)
	}
	return "Authenticated", nil
}

func testConfig() *config.Config {
	return &config.Config{
		MaxConcurrency: 5,
		GenerateModel:  "claude-sonnet-4-6",
		VerifyModel:    "claude-sonnet-4-6",
	}
}

// ---------------------------------------------------------------------------
// Generate tests
// ---------------------------------------------------------------------------

func TestGenerate_DisallowedTools(t *testing.T) {
	fr := &fakeRunner{}
	eng := New(fr, nil, testConfig())

	_, err := eng.Generate(context.Background(), "say hello", "", "")
	require.NoError(t, err)

	require.Len(t, fr.runPromptCalls, 1)
	opts := fr.runPromptCalls[0]
	expected := []string{"Bash", "Read", "Edit", "Write", "Glob", "Grep", "WebFetch", "WebSearch"}
	assert.Equal(t, expected, opts.DisallowedTools)
}

func TestGenerate_NoSessionPersistence(t *testing.T) {
	fr := &fakeRunner{}
	eng := New(fr, nil, testConfig())

	_, err := eng.Generate(context.Background(), "say hello", "", "")
	require.NoError(t, err)

	assert.True(t, fr.runPromptCalls[0].NoSessionPersistence)
}

func TestGenerate_SystemPromptPassedThrough(t *testing.T) {
	fr := &fakeRunner{}
	eng := New(fr, nil, testConfig())

	_, err := eng.Generate(context.Background(), "prompt", "you are a bot", "")
	require.NoError(t, err)

	assert.Equal(t, "you are a bot", fr.runPromptCalls[0].AppendSystemPrompt)
}

func TestGenerate_ModelDefault(t *testing.T) {
	fr := &fakeRunner{}
	cfg := testConfig()
	cfg.GenerateModel = "claude-haiku-3-5"
	eng := New(fr, nil, cfg)

	_, err := eng.Generate(context.Background(), "prompt", "", "")
	require.NoError(t, err)

	assert.Equal(t, "claude-haiku-3-5", fr.runPromptCalls[0].Model)
}

func TestGenerate_ModelOverride(t *testing.T) {
	fr := &fakeRunner{}
	eng := New(fr, nil, testConfig())

	_, err := eng.Generate(context.Background(), "prompt", "", "claude-opus-4-6")
	require.NoError(t, err)

	assert.Equal(t, "claude-opus-4-6", fr.runPromptCalls[0].Model)
}

func TestGenerate_ContextCancellation(t *testing.T) {
	fr := &fakeRunner{}
	cfg := testConfig()
	cfg.MaxConcurrency = 1
	eng := New(fr, nil, cfg)

	// Fill the semaphore
	eng.sem <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := eng.Generate(ctx, "prompt", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	// Clean up semaphore
	<-eng.sem
}

func TestGenerate_RunnerError(t *testing.T) {
	fr := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "partial"}, errors.New("runner failed")
		},
	}
	eng := New(fr, nil, testConfig())

	result, err := eng.Generate(context.Background(), "prompt", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runner failed")
	assert.Equal(t, "partial", result.Output)
}

// ---------------------------------------------------------------------------
// Review tests
// ---------------------------------------------------------------------------

func TestReview_PromptIncludesCodeAndRequirements(t *testing.T) {
	fr := &fakeRunner{}
	eng := New(fr, nil, testConfig())

	_, err := eng.Review(context.Background(), "func main() {}", "must compile", "")
	require.NoError(t, err)

	require.Len(t, fr.runPromptCalls, 1)
	assert.Contains(t, fr.runPromptCalls[0].Prompt, "func main() {}")
	assert.Contains(t, fr.runPromptCalls[0].Prompt, "must compile")
	assert.Contains(t, fr.runPromptCalls[0].Prompt, "PASS")
	assert.Contains(t, fr.runPromptCalls[0].Prompt, "FAIL")
}

func TestReview_ModelDefault(t *testing.T) {
	fr := &fakeRunner{}
	cfg := testConfig()
	cfg.VerifyModel = "claude-opus-4-6"
	eng := New(fr, nil, cfg)

	_, err := eng.Review(context.Background(), "code", "reqs", "")
	require.NoError(t, err)

	assert.Equal(t, "claude-opus-4-6", fr.runPromptCalls[0].Model)
}

func TestReview_DisallowedTools(t *testing.T) {
	fr := &fakeRunner{}
	eng := New(fr, nil, testConfig())

	_, err := eng.Review(context.Background(), "code", "reqs", "")
	require.NoError(t, err)

	expected := []string{"Bash", "Read", "Edit", "Write", "Glob", "Grep", "WebFetch", "WebSearch"}
	assert.Equal(t, expected, fr.runPromptCalls[0].DisallowedTools)
}

// ---------------------------------------------------------------------------
// CodeReview tests
// ---------------------------------------------------------------------------

func TestCodeReview_DefaultModels(t *testing.T) {
	fr := &fakeRunner{}
	eng := New(fr, nil, testConfig())

	_, err := eng.CodeReview(context.Background(), "/tmp", nil, "review this")
	require.NoError(t, err)

	assert.Len(t, fr.runPromptCalls, 3)
	models := make(map[string]bool)
	for _, c := range fr.runPromptCalls {
		models[c.Model] = true
	}
	assert.True(t, models["claude-opus-4-6"])
	assert.True(t, models["claude-sonnet-4-6"])
	assert.True(t, models["claude-haiku-3-5"])
}

func TestCodeReview_ExplicitModels(t *testing.T) {
	fr := &fakeRunner{}
	eng := New(fr, nil, testConfig())

	_, err := eng.CodeReview(context.Background(), "/tmp", []string{"claude-opus-4-6"}, "review")
	require.NoError(t, err)

	assert.Len(t, fr.runPromptCalls, 1)
	assert.Equal(t, "claude-opus-4-6", fr.runPromptCalls[0].Model)
}

func TestCodeReview_AggregatesResults(t *testing.T) {
	fr := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "findings for " + opts.Model}, nil
		},
	}
	eng := New(fr, nil, testConfig())

	result, err := eng.CodeReview(context.Background(), "/tmp", []string{"model-a", "model-b"}, "review")
	require.NoError(t, err)

	assert.Contains(t, result, "## Model: model-a")
	assert.Contains(t, result, "## Model: model-b")
	assert.Contains(t, result, "findings for model-a")
	assert.Contains(t, result, "findings for model-b")
}

func TestCodeReview_PartialFailure(t *testing.T) {
	callCount := int32(0)
	fr := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			n := atomic.AddInt32(&callCount, 1)
			if n == 1 {
				return nil, errors.New("model exploded")
			}
			return &agent.RunResult{Output: "ok from " + opts.Model}, nil
		},
	}
	eng := New(fr, nil, testConfig())

	// errgroup cancels remaining goroutines on first error, so we may get
	// partial results or none. The important thing is we get an error back.
	_, err := eng.CodeReview(context.Background(), "/tmp", []string{"bad-model", "good-model"}, "review")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model exploded")
}

func TestCodeReview_SetsCwd(t *testing.T) {
	fr := &fakeRunner{}
	eng := New(fr, nil, testConfig())

	_, err := eng.CodeReview(context.Background(), "/my/project", []string{"m1"}, "review")
	require.NoError(t, err)

	assert.Equal(t, "/my/project", fr.runPromptCalls[0].Cwd)
}

// ---------------------------------------------------------------------------
// Bugbot tests
// ---------------------------------------------------------------------------

func TestBugbot_DefaultModels(t *testing.T) {
	fr := &fakeRunner{}
	eng := New(fr, nil, testConfig())

	_, err := eng.Bugbot(context.Background(), "/tmp", nil, "scan")
	require.NoError(t, err)

	assert.Len(t, fr.runPromptCalls, 1)
	assert.Equal(t, "claude-haiku-3-5", fr.runPromptCalls[0].Model)
}

func TestBugbot_AggregatesResults(t *testing.T) {
	fr := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "bugs from " + opts.Model}, nil
		},
	}
	eng := New(fr, nil, testConfig())

	result, err := eng.Bugbot(context.Background(), "/tmp", []string{"m1"}, "scan")
	require.NoError(t, err)

	assert.Contains(t, result, "## Model: m1")
	assert.Contains(t, result, "bugs from m1")
}

// ---------------------------------------------------------------------------
// Pass-through tests
// ---------------------------------------------------------------------------

func TestModels_PassThrough(t *testing.T) {
	fr := &fakeRunner{
		modelsFn: func(ctx context.Context) (string, error) {
			return "model-list", nil
		},
	}
	eng := New(fr, nil, testConfig())

	out, err := eng.Models(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "model-list", out)
}

func TestAbout_PassThrough(t *testing.T) {
	fr := &fakeRunner{
		aboutFn: func(ctx context.Context) (string, error) {
			return "v1.0.0", nil
		},
	}
	eng := New(fr, nil, testConfig())

	out, err := eng.About(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", out)
}

func TestStatus_PassThrough(t *testing.T) {
	fr := &fakeRunner{
		statusFn: func(ctx context.Context) (string, error) {
			return "authenticated", nil
		},
	}
	eng := New(fr, nil, testConfig())

	out, err := eng.Status(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "authenticated", out)
}

// ---------------------------------------------------------------------------
// Semaphore tests
// ---------------------------------------------------------------------------

func TestSemaphore_LimitsConcurrency(t *testing.T) {
	var running atomic.Int32
	var maxSeen atomic.Int32

	fr := &fakeRunner{
		runPromptFn: func(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error) {
			n := running.Add(1)
			for {
				old := maxSeen.Load()
				if int32(n) <= old || maxSeen.CompareAndSwap(old, int32(n)) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			running.Add(-1)
			return &agent.RunResult{Output: "ok"}, nil
		},
	}

	cfg := testConfig()
	cfg.MaxConcurrency = 1
	eng := New(fr, nil, cfg)

	ctx := context.Background()
	done := make(chan struct{}, 2)

	go func() {
		eng.Generate(ctx, "p1", "", "")
		done <- struct{}{}
	}()
	go func() {
		eng.Generate(ctx, "p2", "", "")
		done <- struct{}{}
	}()

	<-done
	<-done

	assert.Equal(t, int32(1), maxSeen.Load(), "at most 1 operation should run at a time")
}
```

- [ ] **Step 4: Run engine tests**

Run: `go test ./internal/engine/ -v -race`
Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/engine.go internal/engine/engine_test.go go.mod go.sum
git commit -m "feat(engine): add CodeReview, Bugbot fan-out and comprehensive tests"
```

---

### Task 3: MCP Metrics

**Files:**
- Create: `internal/mcp/metrics.go`
- Create: `internal/mcp/metrics_test.go`

- [ ] **Step 1: Create mcp directory**

```bash
mkdir -p /Users/crestenstclair/workspace/claude-mcp-server/internal/mcp
```

- [ ] **Step 2: Write metrics.go**

```go
package mcp

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// toolMetric tracks per-tool statistics using atomic counters.
type toolMetric struct {
	Calls   atomic.Int64
	Errors  atomic.Int64
	TotalNs atomic.Int64
	MinNs   atomic.Int64
	MaxNs   atomic.Int64
}

// ToolMetricSnapshot is a point-in-time snapshot of a single tool's metrics.
type ToolMetricSnapshot struct {
	Calls  int64   `json:"calls"`
	Errors int64   `json:"errors"`
	AvgMs  float64 `json:"avg_ms"`
	MinMs  float64 `json:"min_ms"`
	MaxMs  float64 `json:"max_ms"`
}

// MetricsSnapshot is a point-in-time snapshot of all metrics.
type MetricsSnapshot struct {
	UptimeSeconds float64                       `json:"uptime_seconds"`
	TotalCalls    int64                         `json:"total_calls"`
	TotalErrors   int64                         `json:"total_errors"`
	Tools         map[string]ToolMetricSnapshot `json:"tools"`
}

// Metrics tracks per-tool call statistics.
type Metrics struct {
	mu        sync.RWMutex
	tools     map[string]*toolMetric
	startTime time.Time
}

// NewMetrics creates a new Metrics tracker.
func NewMetrics() *Metrics {
	return &Metrics{
		tools:     make(map[string]*toolMetric),
		startTime: time.Now(),
	}
}

// getOrCreate returns the toolMetric for the given tool, creating it if needed.
func (m *Metrics) getOrCreate(tool string) *toolMetric {
	m.mu.RLock()
	tm, ok := m.tools[tool]
	m.mu.RUnlock()
	if ok {
		return tm
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock.
	if tm, ok = m.tools[tool]; ok {
		return tm
	}

	tm = &toolMetric{}
	tm.MinNs.Store(math.MaxInt64)
	tm.MaxNs.Store(0)
	m.tools[tool] = tm
	return tm
}

// Record records a tool invocation.
func (m *Metrics) Record(tool string, elapsed time.Duration, err error) {
	tm := m.getOrCreate(tool)
	ns := elapsed.Nanoseconds()

	tm.Calls.Add(1)
	if err != nil {
		tm.Errors.Add(1)
	}
	tm.TotalNs.Add(ns)

	// CAS loop for MinNs
	for {
		old := tm.MinNs.Load()
		if ns >= old || tm.MinNs.CompareAndSwap(old, ns) {
			break
		}
	}

	// CAS loop for MaxNs
	for {
		old := tm.MaxNs.Load()
		if ns <= old || tm.MaxNs.CompareAndSwap(old, ns) {
			break
		}
	}
}

// Snapshot returns a point-in-time snapshot of all metrics.
func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := MetricsSnapshot{
		UptimeSeconds: time.Since(m.startTime).Seconds(),
		Tools:         make(map[string]ToolMetricSnapshot, len(m.tools)),
	}

	for name, tm := range m.tools {
		calls := tm.Calls.Load()
		errs := tm.Errors.Load()
		totalNs := tm.TotalNs.Load()
		minNs := tm.MinNs.Load()
		maxNs := tm.MaxNs.Load()

		snap.TotalCalls += calls
		snap.TotalErrors += errs

		ts := ToolMetricSnapshot{
			Calls:  calls,
			Errors: errs,
		}

		if calls > 0 {
			ts.AvgMs = float64(totalNs) / float64(calls) / 1e6
			ts.MinMs = float64(minNs) / 1e6
			ts.MaxMs = float64(maxNs) / 1e6
		}

		snap.Tools[name] = ts
	}

	return snap
}
```

- [ ] **Step 3: Write metrics_test.go**

```go
package mcp

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetrics_Record_UpdatesCounters(t *testing.T) {
	m := NewMetrics()

	m.Record("run_prompt", 100*time.Millisecond, nil)
	m.Record("run_prompt", 200*time.Millisecond, nil)
	m.Record("run_prompt", 300*time.Millisecond, errors.New("fail"))

	snap := m.Snapshot()
	require.Contains(t, snap.Tools, "run_prompt")

	ts := snap.Tools["run_prompt"]
	assert.Equal(t, int64(3), ts.Calls)
	assert.Equal(t, int64(1), ts.Errors)
	assert.InDelta(t, 200.0, ts.AvgMs, 1.0)
	assert.InDelta(t, 100.0, ts.MinMs, 1.0)
	assert.InDelta(t, 300.0, ts.MaxMs, 1.0)
}

func TestMetrics_Snapshot_Totals(t *testing.T) {
	m := NewMetrics()

	m.Record("tool_a", 10*time.Millisecond, nil)
	m.Record("tool_b", 20*time.Millisecond, errors.New("err"))

	snap := m.Snapshot()
	assert.Equal(t, int64(2), snap.TotalCalls)
	assert.Equal(t, int64(1), snap.TotalErrors)
	assert.Greater(t, snap.UptimeSeconds, 0.0)
}

func TestMetrics_Snapshot_ZeroCalls(t *testing.T) {
	m := NewMetrics()

	snap := m.Snapshot()
	assert.Equal(t, int64(0), snap.TotalCalls)
	assert.Equal(t, int64(0), snap.TotalErrors)
	assert.Empty(t, snap.Tools)
}

func TestMetrics_ConcurrentRecord(t *testing.T) {
	m := NewMetrics()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%10 == 0 {
				err = errors.New("err")
			}
			m.Record("concurrent_tool", time.Duration(i)*time.Millisecond, err)
		}(i)
	}
	wg.Wait()

	snap := m.Snapshot()
	assert.Equal(t, int64(100), snap.Tools["concurrent_tool"].Calls)
	assert.Equal(t, int64(10), snap.Tools["concurrent_tool"].Errors)
}

func TestMetrics_MultipleTools(t *testing.T) {
	m := NewMetrics()

	m.Record("tool_a", 10*time.Millisecond, nil)
	m.Record("tool_b", 20*time.Millisecond, nil)
	m.Record("tool_c", 30*time.Millisecond, nil)

	snap := m.Snapshot()
	assert.Len(t, snap.Tools, 3)
	assert.Contains(t, snap.Tools, "tool_a")
	assert.Contains(t, snap.Tools, "tool_b")
	assert.Contains(t, snap.Tools, "tool_c")
}
```

- [ ] **Step 4: Run metrics tests**

Run: `go test ./internal/mcp/ -v -race -run TestMetrics`
Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/metrics.go internal/mcp/metrics_test.go
git commit -m "feat(mcp): add per-tool metrics with atomic counters"
```

---

### Task 4: Process Tree + Recursion Guard

**Files:**
- Create: `internal/mcp/process.go`
- Create: `internal/mcp/recursion.go`
- Create: `internal/mcp/recursion_test.go`

- [ ] **Step 1: Write process.go**

```go
package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// processTree abstracts process tree traversal for recursion detection.
type processTree interface {
	ParentProcess(pid int) (name string, ppid int, err error)
	SelfPID() int
}

// OSProcessTree is the real implementation that queries the OS process table.
type OSProcessTree struct{}

// SelfPID returns the current process ID.
func (OSProcessTree) SelfPID() int {
	return os.Getpid()
}

// ParentProcess returns the command name and parent PID of the given process.
func (OSProcessTree) ParentProcess(pid int) (string, int, error) {
	out, err := exec.Command("ps", "-o", "comm=,ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", 0, fmt.Errorf("ps for pid %d: %w", pid, err)
	}

	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", 0, fmt.Errorf("no output from ps for pid %d", pid)
	}

	// The output format is "COMMAND PPID" -- split on last whitespace
	// since command name could contain spaces.
	idx := strings.LastIndexByte(line, ' ')
	if idx < 0 {
		return "", 0, fmt.Errorf("unexpected ps output format: %q", line)
	}

	name := strings.TrimSpace(line[:idx])
	ppidStr := strings.TrimSpace(line[idx+1:])
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil {
		return "", 0, fmt.Errorf("parse ppid %q: %w", ppidStr, err)
	}

	return name, ppid, nil
}
```

- [ ] **Step 2: Write recursion.go**

```go
package mcp

import (
	"path/filepath"
	"strings"
)

// DetectRecursion walks up the process tree looking for evidence that
// crest-spec is being invoked recursively by a claude subprocess.
// Returns true if more than one "claude" ancestor (excluding crest-spec
// and mcp processes) is found.
func DetectRecursion(pt processTree) bool {
	pid := pt.SelfPID()
	visited := make(map[int]bool)
	claudeCount := 0

	for pid > 1 {
		if visited[pid] {
			break
		}
		visited[pid] = true

		name, ppid, err := pt.ParentProcess(pid)
		if err != nil {
			break
		}

		base := strings.ToLower(filepath.Base(name))
		if strings.Contains(base, "claude") &&
			!strings.Contains(base, "crest-spec") &&
			!strings.Contains(base, "mcp") {
			claudeCount++
		}

		if claudeCount > 1 {
			return true
		}

		pid = ppid
	}

	return false
}
```

- [ ] **Step 3: Write recursion_test.go**

```go
package mcp

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeProcess represents a process in the fake tree.
type fakeProcess struct {
	name string
	ppid int
}

// fakeProcessTree implements processTree for testing.
type fakeProcessTree struct {
	selfPID   int
	processes map[int]fakeProcess
}

func (f *fakeProcessTree) SelfPID() int {
	return f.selfPID
}

func (f *fakeProcessTree) ParentProcess(pid int) (string, int, error) {
	p, ok := f.processes[pid]
	if !ok {
		return "", 0, fmt.Errorf("process %d not found", pid)
	}
	return p.name, p.ppid, nil
}

func TestDetectRecursion_NoRecursion(t *testing.T) {
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 50},
			50:  {name: "claude", ppid: 10},
			10:  {name: "zsh", ppid: 1},
		},
	}

	assert.False(t, DetectRecursion(pt))
}

func TestDetectRecursion_RecursionDetected(t *testing.T) {
	// Two claude ancestors means recursion.
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 90},
			90:  {name: "claude", ppid: 80},
			80:  {name: "node", ppid: 70},
			70:  {name: "claude", ppid: 60},
			60:  {name: "zsh", ppid: 1},
		},
	}

	assert.True(t, DetectRecursion(pt))
}

func TestDetectRecursion_CrestSpecAncestorNotCounted(t *testing.T) {
	// crest-spec in ancestor chain should NOT be counted as claude.
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 90},
			90:  {name: "claude", ppid: 80},
			80:  {name: "crest-spec", ppid: 70},
			70:  {name: "zsh", ppid: 1},
		},
	}

	assert.False(t, DetectRecursion(pt))
}

func TestDetectRecursion_MCPAncestorNotCounted(t *testing.T) {
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 90},
			90:  {name: "claude", ppid: 80},
			80:  {name: "claude-mcp-bridge", ppid: 70},
			70:  {name: "zsh", ppid: 1},
		},
	}

	assert.False(t, DetectRecursion(pt))
}

func TestDetectRecursion_SelfReferentialPID(t *testing.T) {
	// Process tree loops back to itself -- should not infinite loop.
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 90},
			90:  {name: "claude", ppid: 90}, // self-referential
		},
	}

	assert.False(t, DetectRecursion(pt))
}

func TestDetectRecursion_ProcessNotFound(t *testing.T) {
	// Parent process disappears mid-walk.
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 99},
			// 99 does not exist
		},
	}

	assert.False(t, DetectRecursion(pt))
}

func TestDetectRecursion_FullPathNames(t *testing.T) {
	// Process names can be full paths.
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "/usr/local/bin/crest-spec", ppid: 90},
			90:  {name: "/usr/local/bin/claude", ppid: 80},
			80:  {name: "/usr/local/bin/claude", ppid: 70},
			70:  {name: "/bin/zsh", ppid: 1},
		},
	}

	assert.True(t, DetectRecursion(pt))
}
```

- [ ] **Step 4: Run recursion tests**

Run: `go test ./internal/mcp/ -v -race -run TestDetectRecursion`
Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/process.go internal/mcp/recursion.go internal/mcp/recursion_test.go
git commit -m "feat(mcp): add process tree and recursion guard"
```

---

### Task 5: MCP Server Core + JSON-RPC Types

**Files:**
- Create: `internal/mcp/server.go`

- [ ] **Step 1: Write server.go with types, Server struct, New, Run, ServeHTTP, runAsync, writeResponse**

```go
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
	storemod "github.com/crestenstclair/crest-spec/internal/store"
)

// ---------------------------------------------------------------------------
// Package-private interfaces
// ---------------------------------------------------------------------------

// engine is the consumer-side surface of the Engine.
type engine interface {
	Generate(ctx context.Context, prompt, systemPrompt, model string) (*agent.RunResult, error)
	Review(ctx context.Context, code, requirements, model string) (*agent.RunResult, error)
	CodeReview(ctx context.Context, cwd string, models []string, prompt string) (string, error)
	Bugbot(ctx context.Context, cwd string, models []string, prompt string) (string, error)
	Models(ctx context.Context) (string, error)
	About(ctx context.Context) (string, error)
	Status(ctx context.Context) (string, error)
}

// store is the consumer-side surface of the Store for job operations.
type store interface {
	CreateJob(id, tool string, pid int) error
	CompleteJob(id, result string) error
	FailJob(id string, jobErr error) error
	CancelJob(id string) error
	GetJob(id string) (*storemod.Job, error)
	ListJobs(limit int) ([]storemod.Job, error)
	DeleteJob(id string) error
	CleanupOrphans(aliveFn func(int) bool) (int, error)
}

// ---------------------------------------------------------------------------
// JSON-RPC types
// ---------------------------------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type handlerFunc func(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse

// ---------------------------------------------------------------------------
// Tool types
// ---------------------------------------------------------------------------

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      *toolCallMeta   `json:"_meta,omitempty"`
}

type toolCallMeta struct {
	ProgressToken string `json:"progressToken,omitempty"`
}

type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolHandler is a function that handles a specific tool call.
type toolHandler func(ctx context.Context, args json.RawMessage, progressToken string) toolResult

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Server is the MCP JSON-RPC server.
type Server struct {
	eng       engine
	store     store
	pt        processTree
	stdin     io.Reader
	stdout    io.Writer
	log       zerolog.Logger
	cfg       *config.Config
	metrics   *Metrics
	cancels   map[string]context.CancelFunc
	cancelsMu sync.Mutex
	asyncWg   sync.WaitGroup
	bgCtx     context.Context
	bgCancel  context.CancelFunc
	outMu     sync.Mutex
	tools     []toolDef
	dispatch  map[string]handlerFunc
	toolFns   map[string]toolHandler
	startTime time.Time
	recursion bool
}

// New creates a new MCP Server.
func New(
	eng engine,
	st store,
	pt processTree,
	stdin io.Reader,
	stdout io.Writer,
	log zerolog.Logger,
	cfg *config.Config,
) *Server {
	bgCtx, bgCancel := context.WithCancel(context.Background())

	s := &Server{
		eng:       eng,
		store:     st,
		pt:        pt,
		stdin:     stdin,
		stdout:    stdout,
		log:       log,
		cfg:       cfg,
		metrics:   NewMetrics(),
		cancels:   make(map[string]context.CancelFunc),
		bgCtx:     bgCtx,
		bgCancel:  bgCancel,
		toolFns:   make(map[string]toolHandler),
		startTime: time.Now(),
	}

	s.recursion = DetectRecursion(pt)
	s.registerTools()

	if s.recursion {
		s.log.Warn().Msg("recursion detected: all tools disabled")
		s.tools = []toolDef{
			{
				Name:        "recursion_detected",
				Description: "This server has detected it is being called recursively. All tools are disabled.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			},
		}
		s.toolFns = map[string]toolHandler{
			"recursion_detected": func(ctx context.Context, args json.RawMessage, pt string) toolResult {
				return errorResult("recursion detected: crest-spec tools are disabled to prevent infinite loops")
			},
		}
	}

	return s
}

// Run starts the stdio transport. It blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(s.stdin)
	scanner.Buffer(make([]byte, 0, 10<<20), 10<<20)

	lines := make(chan string, 64)

	// Reader goroutine
	go func() {
		defer close(lines)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				// stdin closed
				s.shutdown()
				return nil
			}
			s.handleLine(ctx, line)

		case <-ctx.Done():
			s.shutdown()
			return nil
		}
	}
}

// handleLine parses and dispatches a single JSON-RPC line.
func (s *Server) handleLine(ctx context.Context, line string) {
	var req jsonRPCRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		s.writeResponse(jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &rpcError{Code: -32700, Message: "Parse error: " + err.Error()},
		})
		return
	}

	handler, ok := s.dispatch[req.Method]
	if !ok {
		s.writeResponse(jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "Method not found: " + req.Method},
		})
		return
	}

	// Notifications (no ID) -- fire and forget
	if req.ID == nil && req.Method == "notifications/initialized" {
		handler(ctx, req.ID, req.Params)
		return
	}

	s.asyncWg.Add(1)
	go func() {
		defer s.asyncWg.Done()
		resp := handler(ctx, req.ID, req.Params)
		s.writeResponse(resp)
	}()
}

// shutdown cancels background context and waits for in-flight jobs.
func (s *Server) shutdown() {
	s.bgCancel()

	done := make(chan struct{})
	go func() {
		s.asyncWg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		s.log.Warn().Msg("shutdown: timed out waiting for async jobs")
	}
}

// writeResponse marshals and writes a JSON-RPC response to stdout.
func (s *Server) writeResponse(resp jsonRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		s.log.Error().Err(err).Msg("marshal response failed")
		return
	}
	data = append(data, '\n')

	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.stdout.Write(data)
}

// writeNotification writes a JSON-RPC notification (no ID, no response expected).
func (s *Server) writeNotification(method string, params any) {
	type notification struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}

	data, err := json.Marshal(notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		s.log.Error().Err(err).Msg("marshal notification failed")
		return
	}
	data = append(data, '\n')

	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.stdout.Write(data)
}

// ServeHTTP handles HTTP transport requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &rpcError{Code: -32700, Message: "Parse error: " + err.Error()},
		})
		return
	}

	handler, ok := s.dispatch[req.Method]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "Method not found: " + req.Method},
		})
		return
	}

	resp := handler(r.Context(), req.ID, req.Params)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// runAsync creates a job, launches a background goroutine, and returns immediately
// with a job_id. The goroutine updates the store when the job completes/fails/cancels.
func (s *Server) runAsync(
	toolName string,
	fn func(ctx context.Context) (string, error),
	progressToken string,
) toolResult {
	id := uuid.NewString()
	pid := os.Getpid()

	jobCtx, jobCancel := context.WithCancel(s.bgCtx)

	s.cancelsMu.Lock()
	s.cancels[id] = jobCancel
	s.cancelsMu.Unlock()

	if err := s.store.CreateJob(id, toolName, pid); err != nil {
		jobCancel()
		s.cancelsMu.Lock()
		delete(s.cancels, id)
		s.cancelsMu.Unlock()
		return errorResult(fmt.Sprintf("failed to create job: %v", err))
	}

	if progressToken != "" {
		s.writeNotification("notifications/progress", map[string]any{
			"progressToken": progressToken,
			"progress":      0,
			"total":         100,
			"message":       fmt.Sprintf("Job %s started (%s)", id, toolName),
		})
	}

	s.asyncWg.Add(1)
	go func() {
		defer s.asyncWg.Done()
		defer jobCancel()
		defer func() {
			s.cancelsMu.Lock()
			delete(s.cancels, id)
			s.cancelsMu.Unlock()
		}()

		start := time.Now()
		result, err := fn(jobCtx)
		elapsed := time.Since(start)

		s.metrics.Record(toolName, elapsed, err)

		if err == nil {
			if storeErr := s.store.CompleteJob(id, result); storeErr != nil {
				s.log.Error().Err(storeErr).Str("job_id", id).Msg("failed to complete job")
			}
		} else if jobCtx.Err() != nil {
			if storeErr := s.store.CancelJob(id); storeErr != nil {
				s.log.Error().Err(storeErr).Str("job_id", id).Msg("failed to cancel job")
			}
		} else {
			if storeErr := s.store.FailJob(id, err); storeErr != nil {
				s.log.Error().Err(storeErr).Str("job_id", id).Msg("failed to fail job")
			}
		}

		if progressToken != "" {
			msg := fmt.Sprintf("Job %s completed", id)
			if err != nil {
				msg = fmt.Sprintf("Job %s failed: %v", id, err)
			}
			s.writeNotification("notifications/progress", map[string]any{
				"progressToken": progressToken,
				"progress":      100,
				"total":         100,
				"message":       msg,
			})
		}
	}()

	return textResult(fmt.Sprintf(`{"job_id":"%s"}`, id))
}

// ---------------------------------------------------------------------------
// Result helpers
// ---------------------------------------------------------------------------

func textResult(text string) toolResult {
	return toolResult{
		Content: []contentBlock{{Type: "text", Text: text}},
	}
}

func errorResult(text string) toolResult {
	return toolResult{
		Content: []contentBlock{{Type: "text", Text: text}},
		IsError: true,
	}
}

func jsonResult(data any) toolResult {
	b, err := json.Marshal(data)
	if err != nil {
		return errorResult(fmt.Sprintf("marshal error: %v", err))
	}
	return textResult(string(b))
}
```

- [ ] **Step 2: Verify it compiles (will need tools.go and handlers.go stubs -- create minimal stubs)**

Create a temporary stub for `tools.go` just to confirm compilation:

```go
package mcp

// registerTools populates s.tools, s.dispatch, and s.toolFns.
// Full implementation in Task 6.
func (s *Server) registerTools() {
	s.tools = []toolDef{}
	s.dispatch = make(map[string]handlerFunc)
	s.toolFns = make(map[string]toolHandler)
}
```

Run: `go build ./internal/mcp/`
Expected: No output (success).

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/server.go internal/mcp/tools.go
git commit -m "feat(mcp): add Server struct, JSON-RPC types, stdio/HTTP transport, runAsync"
```

---

### Task 6: Tool Definitions + Handlers

**Files:**
- Replace: `internal/mcp/tools.go` (replace the stub from Task 5)
- Create: `internal/mcp/handlers.go`

- [ ] **Step 1: Write the full tools.go with registerTools, all engine tool handlers, and all spec stubs**

Replace the stub `internal/mcp/tools.go` with:

```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// registerTools populates s.tools, s.dispatch, and s.toolFns.
func (s *Server) registerTools() {
	s.dispatch = map[string]handlerFunc{
		"initialize":                s.handleInitialize,
		"notifications/initialized": s.handleInitialized,
		"tools/list":                s.handleToolsList,
		"tools/call":                s.handleToolCall,
		"resources/list":            s.handleResourcesList,
		"resources/read":            s.handleResourcesRead,
		"prompts/list":              s.handlePromptsList,
		"prompts/get":               s.handlePromptsGet,
	}

	// ----- Engine tools (fully implemented) -----

	s.addTool(toolDef{
		Name:        "run_prompt",
		Description: "Run a prompt via the claude CLI sub-agent. Returns a job ID immediately; use poll_result to retrieve the output.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"The prompt to send"},"system_prompt":{"type":"string","description":"System prompt appended to the agent"},"model":{"type":"string","description":"Model override (default: generate model from config)"}},"required":["prompt"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Prompt       string `json:"prompt"`
			SystemPrompt string `json:"system_prompt"`
			Model        string `json:"model"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		return s.runAsync("run_prompt", func(ctx context.Context) (string, error) {
			res, err := s.eng.Generate(ctx, p.Prompt, p.SystemPrompt, p.Model)
			if err != nil {
				return "", err
			}
			return res.Output, nil
		}, progressToken)
	})

	s.addTool(toolDef{
		Name:        "code_review",
		Description: "Multi-model code review. Fans out across models and aggregates findings per model.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory for the review"},"models":{"type":"array","items":{"type":"string"},"description":"Models to use (default: opus, sonnet, haiku)"},"prompt":{"type":"string","description":"Review instructions or focus areas"}},"required":["prompt"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Cwd    string   `json:"cwd"`
			Models []string `json:"models"`
			Prompt string   `json:"prompt"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		return s.runAsync("code_review", func(ctx context.Context) (string, error) {
			return s.eng.CodeReview(ctx, p.Cwd, p.Models, p.Prompt)
		}, progressToken)
	})

	s.addTool(toolDef{
		Name:        "bugbot",
		Description: "Lightweight severity-ranked bug scan.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory for the scan"},"models":{"type":"array","items":{"type":"string"},"description":"Models to use (default: haiku)"},"prompt":{"type":"string","description":"Scan focus or file list"}},"required":["prompt"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Cwd    string   `json:"cwd"`
			Models []string `json:"models"`
			Prompt string   `json:"prompt"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		return s.runAsync("bugbot", func(ctx context.Context) (string, error) {
			return s.eng.Bugbot(ctx, p.Cwd, p.Models, p.Prompt)
		}, progressToken)
	})

	s.addTool(toolDef{
		Name:        "poll_result",
		Description: "Check a job's status. Optionally consume (delete) the result.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string","description":"The job ID to poll"},"consume":{"type":"boolean","description":"If true, delete the job after reading (default: false)"}},"required":["job_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			JobID   string `json:"job_id"`
			Consume bool   `json:"consume"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}

		job, err := s.store.GetJob(p.JobID)
		if err != nil {
			return errorResult(fmt.Sprintf("job not found: %s", p.JobID))
		}

		resp := map[string]string{
			"status": job.Status,
			"result": job.Result,
			"error":  job.Error,
		}

		if p.Consume && (job.Status == "completed" || job.Status == "failed" || job.Status == "cancelled") {
			s.store.DeleteJob(p.JobID)
		}

		return jsonResult(resp)
	})

	s.addTool(toolDef{
		Name:        "cancel_job",
		Description: "Cancel a running job and kill its subprocess group.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string","description":"The job ID to cancel"}},"required":["job_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}

		s.cancelsMu.Lock()
		cancelFn, ok := s.cancels[p.JobID]
		s.cancelsMu.Unlock()

		if ok {
			cancelFn()
			return jsonResult(map[string]bool{"cancelled": true})
		}

		// Check if job exists but already finished
		job, err := s.store.GetJob(p.JobID)
		if err != nil {
			return errorResult(fmt.Sprintf("job not found: %s", p.JobID))
		}
		return textResult(fmt.Sprintf("job %s already in status: %s", p.JobID, job.Status))
	})

	s.addTool(toolDef{
		Name:        "list_jobs",
		Description: "List up to 50 recent non-deleted jobs.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max jobs to return (default: 50, max: 50)"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Limit int `json:"limit"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		if p.Limit <= 0 || p.Limit > 50 {
			p.Limit = 50
		}

		jobs, err := s.store.ListJobs(p.Limit)
		if err != nil {
			return errorResult(fmt.Sprintf("list jobs: %v", err))
		}
		return jsonResult(jobs)
	})

	s.addTool(toolDef{
		Name:        "list_models",
		Description: "List available Claude models.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		out, err := s.eng.Models(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("list models: %v", err))
		}
		return textResult(out)
	})

	s.addTool(toolDef{
		Name:        "about",
		Description: "Show claude CLI version and auth status.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		about, aboutErr := s.eng.About(ctx)
		status, statusErr := s.eng.Status(ctx)
		if aboutErr != nil {
			return errorResult(fmt.Sprintf("about: %v", aboutErr))
		}
		if statusErr != nil {
			return errorResult(fmt.Sprintf("status: %v", statusErr))
		}
		return textResult(fmt.Sprintf("Version: %s\nAuth: %s", about, status))
	})

	s.addTool(toolDef{
		Name:        "status",
		Description: "Show claude auth status.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		out, err := s.eng.Status(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("status: %v", err))
		}
		return textResult(out)
	})

	s.addTool(toolDef{
		Name:        "live_metrics",
		Description: "Self-monitoring snapshot: uptime, call counts, error rates, per-tool stats.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		snap := s.metrics.Snapshot()
		return jsonResult(snap)
	})

	// ----- Spec tool stubs (registered now, implemented in SP3-SP5) -----

	specStubs := []toolDef{
		{Name: "spec/plan", Description: "Show what would change (dry run)", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"filter":{"type":"string","description":"Resource filter pattern"}}}`),},
		{Name: "spec/apply", Description: "Execute the plan (async)", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"filter":{"type":"string","description":"Resource filter pattern"},"dry_run":{"type":"boolean","description":"Preview without executing"}}}`),},
		{Name: "spec/validate", Description: "Check structural invariants", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`),},
		{Name: "spec/begin", Description: "Start interactive agent session", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`),},
		{Name: "spec/next", Description: "Get next wave of uncommitted resources", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}}}`),},
		{Name: "spec/context", Description: "Get scoped prompt for a resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"session_id":{"type":"string","description":"Session ID"}}}`),},
		{Name: "spec/validate-resource", Description: "Run invariant checks for a resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"session_id":{"type":"string","description":"Session ID"}}}`),},
		{Name: "spec/note", Description: "Save a design decision note", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"content":{"type":"string","description":"Note content"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id","content"]}`),},
		{Name: "spec/commit", Description: "Record a resource as complete", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id"]}`),},
		{Name: "spec/resolve", Description: "Provide guidance for blocked resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"guidance":{"type":"string","description":"Resolution guidance"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id","guidance"]}`),},
		{Name: "spec/amend", Description: "Signal spec update for resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id"]}`),},
		{Name: "spec/skip", Description: "Skip a failed resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"reason":{"type":"string","description":"Reason for skipping"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id"]}`),},
		{Name: "spec/finish", Description: "Finalize session, release lock", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}}}`),},
		{Name: "spec/status", Description: "Show current state", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`),},
		{Name: "spec/log", Description: "List past applies", InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max entries to return"}}}`),},
		{Name: "spec/history", Description: "Show generation history for resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"limit":{"type":"integer","description":"Max entries to return"}},"required":["resource_id"]}`),},
		{Name: "spec/graph", Description: "Return dependency graph", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"format":{"type":"string","description":"Output format (json, dot)"}}}`),},
		{Name: "spec/diff", Description: "Reconstruct state delta between applies", InputSchema: json.RawMessage(`{"type":"object","properties":{"apply_id_a":{"type":"string","description":"First apply ID"},"apply_id_b":{"type":"string","description":"Second apply ID"}}}`),},
		{Name: "spec/state", Description: "Inspect/modify state tracking", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"action":{"type":"string","description":"Action: get, set, clear"}}}`),},
		{Name: "spec/drift", Description: "Handle drifted resources", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`),},
		{Name: "spec/vacuum", Description: "Compact old history", InputSchema: json.RawMessage(`{"type":"object","properties":{"older_than":{"type":"string","description":"Age threshold (e.g. 30d)"}}}`),},
		{Name: "spec/sql", Description: "Read-only SQLite shell", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"SQL query to execute"}},"required":["query"]}`),},
		{Name: "spec/unlock", Description: "Force-clear stale lock", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),},
	}

	for _, def := range specStubs {
		s.addTool(def, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
			return textResult("not implemented yet -- available in a future release")
		})
	}
}

// addTool registers a tool definition and its handler.
func (s *Server) addTool(def toolDef, handler toolHandler) {
	s.tools = append(s.tools, def)
	s.toolFns[def.Name] = handler
}
```

- [ ] **Step 2: Write handlers.go**

```go
package mcp

import (
	"context"
	"encoding/json"
)

// handleInitialize returns the MCP protocol initialization response.
func (s *Server) handleInitialize(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
				"prompts":   map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "crest-spec",
				"version": "0.1.0",
			},
			"instructions": "crest-spec is a declarative code generation system. Use run_prompt for ad-hoc generation, code_review for multi-model review, bugbot for bug scanning. Use poll_result to check async job results.",
		},
	}
}

// handleInitialized is a no-op acknowledgment.
func (s *Server) handleInitialized(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{},
	}
}

// handleToolsList returns all registered tool definitions.
func (s *Server) handleToolsList(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{"tools": s.tools},
	}
}

// handleToolCall dispatches a tool call to its handler.
func (s *Server) handleToolCall(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	var tcp toolCallParams
	if err := json.Unmarshal(params, &tcp); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32602, Message: "Invalid params: " + err.Error()},
		}
	}

	handler, ok := s.toolFns[tcp.Name]
	if !ok {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result:  errorResult("unknown tool: " + tcp.Name),
		}
	}

	var progressToken string
	if tcp.Meta != nil {
		progressToken = tcp.Meta.ProgressToken
	}

	result := handler(ctx, tcp.Arguments, progressToken)
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

// handleResourcesList returns an empty resource list (implemented in SP3+).
func (s *Server) handleResourcesList(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{"resources": []any{}},
	}
}

// handleResourcesRead returns an error (no resources available yet).
func (s *Server) handleResourcesRead(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: -32602, Message: "no resources available yet"},
	}
}

// handlePromptsList returns an empty prompt list (implemented in SP4+).
func (s *Server) handlePromptsList(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{"prompts": []any{}},
	}
}

// handlePromptsGet returns an error (no prompts available yet).
func (s *Server) handlePromptsGet(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: -32602, Message: "no prompts available yet"},
	}
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/mcp/`
Expected: No output (success).

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/handlers.go
git commit -m "feat(mcp): add tool definitions, handlers, and 23 spec tool stubs"
```

---

### Task 7: MCP Server Tests

**Files:**
- Create: `internal/mcp/server_test.go`

- [ ] **Step 1: Write server_test.go with manual fakes and comprehensive tests**

```go
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
	storemod "github.com/crestenstclair/crest-spec/internal/store"
)

// ---------------------------------------------------------------------------
// Fake engine
// ---------------------------------------------------------------------------

type fakeEngine struct {
	mu              sync.Mutex
	generateCalls   int
	generateResult  *agent.RunResult
	generateErr     error
	codeReviewCalls int
	codeReviewOut   string
	codeReviewErr   error
	bugbotCalls     int
	bugbotOut       string
	bugbotErr       error
	modelsOut       string
	modelsErr       error
	aboutOut        string
	aboutErr        error
	statusOut       string
	statusErr       error
	generateDelay   time.Duration
}

func (f *fakeEngine) Generate(ctx context.Context, prompt, systemPrompt, model string) (*agent.RunResult, error) {
	f.mu.Lock()
	f.generateCalls++
	f.mu.Unlock()
	if f.generateDelay > 0 {
		select {
		case <-time.After(f.generateDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.generateErr != nil {
		return f.generateResult, f.generateErr
	}
	res := f.generateResult
	if res == nil {
		res = &agent.RunResult{Output: "generated output"}
	}
	return res, nil
}

func (f *fakeEngine) Review(ctx context.Context, code, requirements, model string) (*agent.RunResult, error) {
	return &agent.RunResult{Output: "PASS"}, nil
}

func (f *fakeEngine) CodeReview(ctx context.Context, cwd string, models []string, prompt string) (string, error) {
	f.mu.Lock()
	f.codeReviewCalls++
	f.mu.Unlock()
	if f.codeReviewErr != nil {
		return f.codeReviewOut, f.codeReviewErr
	}
	out := f.codeReviewOut
	if out == "" {
		out = "code review output"
	}
	return out, nil
}

func (f *fakeEngine) Bugbot(ctx context.Context, cwd string, models []string, prompt string) (string, error) {
	f.mu.Lock()
	f.bugbotCalls++
	f.mu.Unlock()
	if f.bugbotErr != nil {
		return f.bugbotOut, f.bugbotErr
	}
	out := f.bugbotOut
	if out == "" {
		out = "bugbot output"
	}
	return out, nil
}

func (f *fakeEngine) Models(ctx context.Context) (string, error) {
	if f.modelsErr != nil {
		return "", f.modelsErr
	}
	out := f.modelsOut
	if out == "" {
		out = "claude-opus-4-6, claude-sonnet-4-6"
	}
	return out, nil
}

func (f *fakeEngine) About(ctx context.Context) (string, error) {
	if f.aboutErr != nil {
		return "", f.aboutErr
	}
	out := f.aboutOut
	if out == "" {
		out = "claude-code v1.0.0"
	}
	return out, nil
}

func (f *fakeEngine) Status(ctx context.Context) (string, error) {
	if f.statusErr != nil {
		return "", f.statusErr
	}
	out := f.statusOut
	if out == "" {
		out = "Authenticated"
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Fake store
// ---------------------------------------------------------------------------

type fakeStore struct {
	mu             sync.Mutex
	jobs           map[string]*storemod.Job
	createJobCalls int
	createJobErr   error
	completeCount  int
	failCount      int
	cancelCount    int
	deleteCount    int
}

func newFakeStore() *fakeStore {
	return &fakeStore{jobs: make(map[string]*storemod.Job)}
}

func (f *fakeStore) CreateJob(id, tool string, pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createJobCalls++
	if f.createJobErr != nil {
		return f.createJobErr
	}
	f.jobs[id] = &storemod.Job{
		ID:        id,
		Tool:      tool,
		Status:    "running",
		PID:       pid,
		StartedAt: time.Now(),
	}
	return nil
}

func (f *fakeStore) CompleteJob(id, result string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completeCount++
	if j, ok := f.jobs[id]; ok {
		j.Status = "completed"
		j.Result = result
		now := time.Now()
		j.DoneAt = &now
	}
	return nil
}

func (f *fakeStore) FailJob(id string, jobErr error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failCount++
	if j, ok := f.jobs[id]; ok {
		j.Status = "failed"
		j.Error = jobErr.Error()
		now := time.Now()
		j.DoneAt = &now
	}
	return nil
}

func (f *fakeStore) CancelJob(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelCount++
	if j, ok := f.jobs[id]; ok {
		j.Status = "cancelled"
		now := time.Now()
		j.DoneAt = &now
	}
	return nil
}

func (f *fakeStore) GetJob(id string) (*storemod.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return j, nil
}

func (f *fakeStore) ListJobs(limit int) ([]storemod.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []storemod.Job
	for _, j := range f.jobs {
		out = append(out, *j)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteJob(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCount++
	if j, ok := f.jobs[id]; ok {
		j.Status = "deleted"
	}
	return nil
}

func (f *fakeStore) CleanupOrphans(aliveFn func(int) bool) (int, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// Fake process tree (no recursion by default)
// ---------------------------------------------------------------------------

type noRecursionTree struct{}

func (noRecursionTree) SelfPID() int { return 100 }
func (noRecursionTree) ParentProcess(pid int) (string, int, error) {
	if pid == 100 {
		return "crest-spec", 50, nil
	}
	if pid == 50 {
		return "zsh", 1, nil
	}
	return "", 0, fmt.Errorf("not found")
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func testServer(eng engine, st store) (*Server, *bytes.Buffer) {
	var stdout bytes.Buffer
	log := zerolog.New(io.Discard)
	cfg := &config.Config{MaxConcurrency: 5}
	srv := New(eng, st, noRecursionTree{}, strings.NewReader(""), &stdout, log, cfg)
	return srv, &stdout
}

func sendRequest(t *testing.T, srv *Server, stdout *bytes.Buffer, method string, id any, params any) jsonRPCResponse {
	t.Helper()

	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		require.NoError(t, err)
		paramsRaw = b
	}

	handler, ok := srv.dispatch[method]
	if !ok {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32601, Message: "Method not found: " + method},
		}
	}

	return handler(context.Background(), id, paramsRaw)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestInitialize(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())
	resp := sendRequest(t, srv, nil, "initialize", 1, nil)

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "2024-11-05", result["protocolVersion"])

	serverInfo, ok := result["serverInfo"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "crest-spec", serverInfo["name"])
	assert.Equal(t, "0.1.0", serverInfo["version"])

	caps, ok := result["capabilities"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, caps, "tools")
	assert.Contains(t, caps, "resources")
	assert.Contains(t, caps, "prompts")
}

func TestToolsList_ReturnsAllTools(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())
	resp := sendRequest(t, srv, nil, "tools/list", 1, nil)

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(map[string]any)
	require.True(t, ok)
	tools, ok := result["tools"].([]toolDef)
	require.True(t, ok)

	// 10 engine tools + 23 spec stubs = 33 total
	assert.Len(t, tools, 33)

	// Check that key engine tools exist
	toolNames := make(map[string]bool)
	for _, td := range tools {
		toolNames[td.Name] = true
	}
	assert.True(t, toolNames["run_prompt"])
	assert.True(t, toolNames["code_review"])
	assert.True(t, toolNames["bugbot"])
	assert.True(t, toolNames["poll_result"])
	assert.True(t, toolNames["cancel_job"])
	assert.True(t, toolNames["list_jobs"])
	assert.True(t, toolNames["list_models"])
	assert.True(t, toolNames["about"])
	assert.True(t, toolNames["status"])
	assert.True(t, toolNames["live_metrics"])

	// Check spec stubs
	assert.True(t, toolNames["spec/plan"])
	assert.True(t, toolNames["spec/apply"])
	assert.True(t, toolNames["spec/validate"])
	assert.True(t, toolNames["spec/begin"])
	assert.True(t, toolNames["spec/sql"])
	assert.True(t, toolNames["spec/unlock"])
}

func TestRunPrompt_ReturnsJobID(t *testing.T) {
	fe := &fakeEngine{}
	fs := newFakeStore()
	srv, _ := testServer(fe, fs)

	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "run_prompt",
		Arguments: json.RawMessage(`{"prompt":"hello"}`),
	})

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(toolResult)
	require.True(t, ok)
	assert.False(t, result.IsError)
	assert.Len(t, result.Content, 1)
	assert.Contains(t, result.Content[0].Text, "job_id")

	// Wait for async goroutine to complete
	time.Sleep(100 * time.Millisecond)

	fe.mu.Lock()
	assert.Equal(t, 1, fe.generateCalls)
	fe.mu.Unlock()

	fs.mu.Lock()
	assert.Equal(t, 1, fs.completeCount)
	fs.mu.Unlock()
}

func TestRunPrompt_EngineFailure(t *testing.T) {
	fe := &fakeEngine{
		generateErr: fmt.Errorf("engine exploded"),
	}
	fs := newFakeStore()
	srv, _ := testServer(fe, fs)

	sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "run_prompt",
		Arguments: json.RawMessage(`{"prompt":"hello"}`),
	})

	// Wait for async goroutine
	time.Sleep(100 * time.Millisecond)

	fs.mu.Lock()
	assert.Equal(t, 1, fs.failCount)
	fs.mu.Unlock()
}

func TestPollResult_ExistingJob(t *testing.T) {
	fs := newFakeStore()
	fs.jobs["job-123"] = &storemod.Job{
		ID:     "job-123",
		Status: "completed",
		Result: "output data",
	}
	srv, _ := testServer(&fakeEngine{}, fs)

	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "poll_result",
		Arguments: json.RawMessage(`{"job_id":"job-123"}`),
	})

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(toolResult)
	require.True(t, ok)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "completed")
	assert.Contains(t, result.Content[0].Text, "output data")
}

func TestPollResult_MissingJob(t *testing.T) {
	fs := newFakeStore()
	srv, _ := testServer(&fakeEngine{}, fs)

	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "poll_result",
		Arguments: json.RawMessage(`{"job_id":"nonexistent"}`),
	})

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(toolResult)
	require.True(t, ok)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "not found")
}

func TestPollResult_Consume(t *testing.T) {
	fs := newFakeStore()
	fs.jobs["job-123"] = &storemod.Job{
		ID:     "job-123",
		Status: "completed",
		Result: "data",
	}
	srv, _ := testServer(&fakeEngine{}, fs)

	sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "poll_result",
		Arguments: json.RawMessage(`{"job_id":"job-123","consume":true}`),
	})

	fs.mu.Lock()
	assert.Equal(t, 1, fs.deleteCount)
	fs.mu.Unlock()
}

func TestCancelJob(t *testing.T) {
	fe := &fakeEngine{generateDelay: 5 * time.Second}
	fs := newFakeStore()
	srv, _ := testServer(fe, fs)

	// Start an async job
	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "run_prompt",
		Arguments: json.RawMessage(`{"prompt":"slow"}`),
	})

	result, ok := resp.Result.(toolResult)
	require.True(t, ok)

	// Extract job_id
	var jobResp struct{ JobID string `json:"job_id"` }
	json.Unmarshal([]byte(result.Content[0].Text), &jobResp)
	require.NotEmpty(t, jobResp.JobID)

	// Cancel it
	time.Sleep(50 * time.Millisecond) // ensure goroutine started
	cancelResp := sendRequest(t, srv, nil, "tools/call", 2, toolCallParams{
		Name:      "cancel_job",
		Arguments: json.RawMessage(fmt.Sprintf(`{"job_id":"%s"}`, jobResp.JobID)),
	})

	cancelResult, ok := cancelResp.Result.(toolResult)
	require.True(t, ok)
	assert.False(t, cancelResult.IsError)

	// Wait for async goroutine to complete
	time.Sleep(200 * time.Millisecond)

	fs.mu.Lock()
	assert.Equal(t, 1, fs.cancelCount)
	fs.mu.Unlock()
}

func TestListJobs(t *testing.T) {
	fs := newFakeStore()
	fs.jobs["j1"] = &storemod.Job{ID: "j1", Status: "running"}
	fs.jobs["j2"] = &storemod.Job{ID: "j2", Status: "completed"}
	srv, _ := testServer(&fakeEngine{}, fs)

	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "list_jobs",
		Arguments: json.RawMessage(`{}`),
	})

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(toolResult)
	require.True(t, ok)
	assert.False(t, result.IsError)
	// The output is a JSON array of jobs
	assert.Contains(t, result.Content[0].Text, "j1")
}

func TestListModels(t *testing.T) {
	fe := &fakeEngine{modelsOut: "model-a, model-b"}
	srv, _ := testServer(fe, newFakeStore())

	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "list_models",
		Arguments: json.RawMessage(`{}`),
	})

	result, ok := resp.Result.(toolResult)
	require.True(t, ok)
	assert.Contains(t, result.Content[0].Text, "model-a")
	assert.Contains(t, result.Content[0].Text, "model-b")
}

func TestAbout(t *testing.T) {
	fe := &fakeEngine{aboutOut: "v1.0.0", statusOut: "Authenticated"}
	srv, _ := testServer(fe, newFakeStore())

	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "about",
		Arguments: json.RawMessage(`{}`),
	})

	result, ok := resp.Result.(toolResult)
	require.True(t, ok)
	assert.Contains(t, result.Content[0].Text, "Version: v1.0.0")
	assert.Contains(t, result.Content[0].Text, "Auth: Authenticated")
}

func TestStatus(t *testing.T) {
	fe := &fakeEngine{statusOut: "Authenticated as user@example.com"}
	srv, _ := testServer(fe, newFakeStore())

	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "status",
		Arguments: json.RawMessage(`{}`),
	})

	result, ok := resp.Result.(toolResult)
	require.True(t, ok)
	assert.Contains(t, result.Content[0].Text, "Authenticated")
}

func TestLiveMetrics(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())

	// Record a fake metric
	srv.metrics.Record("test_tool", 100*time.Millisecond, nil)

	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "live_metrics",
		Arguments: json.RawMessage(`{}`),
	})

	result, ok := resp.Result.(toolResult)
	require.True(t, ok)
	assert.Contains(t, result.Content[0].Text, "uptime_seconds")
	assert.Contains(t, result.Content[0].Text, "test_tool")
}

func TestUnknownMethod(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())
	resp := sendRequest(t, srv, nil, "nonexistent/method", 1, nil)
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32601, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "Method not found")
}

func TestUnknownTool(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())
	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "nonexistent_tool",
		Arguments: json.RawMessage(`{}`),
	})

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(toolResult)
	require.True(t, ok)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "unknown tool")
}

func TestMalformedJSON_StdioTransport(t *testing.T) {
	stdin := strings.NewReader("this is not json\n")
	var stdout bytes.Buffer
	log := zerolog.New(io.Discard)
	cfg := &config.Config{MaxConcurrency: 5}
	srv := New(&fakeEngine{}, newFakeStore(), noRecursionTree{}, stdin, &stdout, log, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	srv.Run(ctx)

	// Should have written a parse error response
	assert.Contains(t, stdout.String(), "-32700")
	assert.Contains(t, stdout.String(), "Parse error")
}

func TestStdioTransport_InitializeFlow(t *testing.T) {
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	listReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n"
	stdin := strings.NewReader(initReq + listReq)
	var stdout bytes.Buffer
	log := zerolog.New(io.Discard)
	cfg := &config.Config{MaxConcurrency: 5}
	srv := New(&fakeEngine{}, newFakeStore(), noRecursionTree{}, stdin, &stdout, log, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	srv.Run(ctx)

	output := stdout.String()
	assert.Contains(t, output, "2024-11-05")
	assert.Contains(t, output, "crest-spec")
	assert.Contains(t, output, "run_prompt")
}

func TestHTTPTransport(t *testing.T) {
	fe := &fakeEngine{modelsOut: "model-x"}
	srv, _ := testServer(fe, newFakeStore())

	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_models","arguments":{}}}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "model-x")
}

func TestHTTPTransport_MalformedJSON(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())

	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", strings.NewReader("not json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "-32700")
}

func TestSpecToolStubs_ReturnNotImplemented(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())

	specTools := []string{
		"spec/plan", "spec/apply", "spec/validate", "spec/begin",
		"spec/next", "spec/context", "spec/validate-resource",
		"spec/note", "spec/commit", "spec/resolve", "spec/amend",
		"spec/skip", "spec/finish", "spec/status", "spec/log",
		"spec/history", "spec/graph", "spec/diff", "spec/state",
		"spec/drift", "spec/vacuum", "spec/sql", "spec/unlock",
	}

	for _, tool := range specTools {
		t.Run(tool, func(t *testing.T) {
			resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
				Name:      tool,
				Arguments: json.RawMessage(`{}`),
			})

			assert.Nil(t, resp.Error)
			result, ok := resp.Result.(toolResult)
			require.True(t, ok)
			assert.Contains(t, result.Content[0].Text, "not implemented yet")
		})
	}
}

func TestResourcesList_Empty(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())
	resp := sendRequest(t, srv, nil, "resources/list", 1, nil)

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(map[string]any)
	require.True(t, ok)
	resources, ok := result["resources"].([]any)
	require.True(t, ok)
	assert.Empty(t, resources)
}

func TestResourcesRead_Error(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())
	resp := sendRequest(t, srv, nil, "resources/read", 1, nil)

	require.NotNil(t, resp.Error)
	assert.Equal(t, -32602, resp.Error.Code)
}

func TestPromptsList_Empty(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())
	resp := sendRequest(t, srv, nil, "prompts/list", 1, nil)

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(map[string]any)
	require.True(t, ok)
	prompts, ok := result["prompts"].([]any)
	require.True(t, ok)
	assert.Empty(t, prompts)
}

func TestPromptsGet_Error(t *testing.T) {
	srv, _ := testServer(&fakeEngine{}, newFakeStore())
	resp := sendRequest(t, srv, nil, "prompts/get", 1, nil)

	require.NotNil(t, resp.Error)
	assert.Equal(t, -32602, resp.Error.Code)
}

func TestRecursionDetected_DisablesTools(t *testing.T) {
	// Fake process tree with recursion
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 90},
			90:  {name: "claude", ppid: 80},
			80:  {name: "node", ppid: 70},
			70:  {name: "claude", ppid: 60},
			60:  {name: "zsh", ppid: 1},
		},
	}

	var stdout bytes.Buffer
	log := zerolog.New(io.Discard)
	cfg := &config.Config{MaxConcurrency: 5}
	srv := New(&fakeEngine{}, newFakeStore(), pt, strings.NewReader(""), &stdout, log, cfg)

	// tools/list should return only the recursion_detected tool
	resp := sendRequest(t, srv, nil, "tools/list", 1, nil)
	result, ok := resp.Result.(map[string]any)
	require.True(t, ok)
	tools, ok := result["tools"].([]toolDef)
	require.True(t, ok)
	assert.Len(t, tools, 1)
	assert.Equal(t, "recursion_detected", tools[0].Name)

	// Calling any tool should return error
	callResp := sendRequest(t, srv, nil, "tools/call", 2, toolCallParams{
		Name:      "recursion_detected",
		Arguments: json.RawMessage(`{}`),
	})
	callResult, ok := callResp.Result.(toolResult)
	require.True(t, ok)
	assert.True(t, callResult.IsError)
	assert.Contains(t, callResult.Content[0].Text, "recursion detected")
}
```

- [ ] **Step 2: Run all MCP tests**

Run: `go test ./internal/mcp/ -v -race`
Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/server_test.go
git commit -m "test(mcp): add comprehensive MCP server tests"
```

---

### Task 8: Main.go Wiring + Final Verification

**Files:**
- Modify: `cmd/crest-spec/main.go`

- [ ] **Step 1: Update main.go to wire engine and MCP server**

Replace the contents of `cmd/crest-spec/main.go` with:

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/app"
	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/engine"
	"github.com/crestenstclair/crest-spec/internal/mcp"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	if len(os.Args) >= 4 && os.Args[1] == "check" && os.Args[2] == "job" {
		checkJob(os.Args[3])
		return
	}

	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" {
			config.Help()
			os.Exit(0)
		}
	}

	cfg, err := config.New()
	if err != nil {
		config.Help()
		panic(fmt.Sprintf("config: %v", err))
	}

	dbPath := dbPath()
	s, err := store.New(dbPath)
	if err != nil {
		panic(fmt.Sprintf("store: %v", err))
	}
	defer s.Close()

	cleaned, err := s.CleanupOrphans(processAlive)
	if err != nil {
		log.Warn().Err(err).Msg("orphan cleanup failed")
	} else if cleaned > 0 {
		log.Info().Int("cleaned", cleaned).Msg("cleaned orphaned jobs")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Step 7: Agent
	ag := agent.New(
		cfg.AgentPath,
		cfg.APIKey,
		cfg.DefaultModel,
		cfg.PermissionMode,
		cfg.Timeout,
	)

	// Step 8: Engine
	eng := engine.New(ag, nil, cfg) // nil engineStore -- not needed until SP5

	// Step 9: MCP Server
	srv := mcp.New(eng, s, mcp.OSProcessTree{}, os.Stdin, os.Stdout, log.Logger, cfg)

	// Step 10: HTTP transport (optional)
	if cfg.HTTPAddr != "" {
		httpMux := http.NewServeMux()
		httpMux.HandleFunc("POST /mcp", srv.ServeHTTP)
		httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: httpMux}
		go func() {
			log.Info().Str("addr", cfg.HTTPAddr).Msg("HTTP transport started")
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error().Err(err).Msg("HTTP server error")
			}
		}()
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			httpSrv.Shutdown(shutdownCtx)
		}()
	}

	// Run stdio transport (blocking)
	log.Info().Str("db", dbPath).Msg("crest-spec ready")
	if err := app.New(srv).Run(ctx); err != nil {
		log.Error().Err(err).Msg("server error")
	}
	log.Info().Msg("shutting down")
}

func checkJob(id string) {
	dbPath := dbPath()
	s, err := store.New(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	s.CleanupOrphans(processAlive)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	job, err := s.WaitForCompletion(ctx, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wait: %v\n", err)
		os.Exit(1)
	}

	switch job.Status {
	case "completed":
		fmt.Println(job.Result)
		s.DeleteJob(id)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "job %s: status=%s error=%s\n", id, job.Status, job.Error)
		s.DeleteJob(id)
		os.Exit(1)
	}
}

func dbPath() string {
	dir := filepath.Join(".", ".crest-spec")
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "state.db")
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}
```

- [ ] **Step 2: Verify the full project builds**

Run: `go build -o bin/crest-spec ./cmd/crest-spec`
Expected: Binary created at `bin/crest-spec`.

- [ ] **Step 3: Run the full test suite with race detector**

Run: `go test -race ./...`
Expected: All tests pass across all packages.

- [ ] **Step 4: Test help flag**

Run: `./bin/crest-spec --help; echo "exit: $?"`
Expected: Prints environment variable table, exits 0.

- [ ] **Step 5: Verify binary starts and handles stdin EOF**

Run: `echo '' | timeout 5 ./bin/crest-spec 2>/dev/null; echo "exit: $?"`
Expected: Binary starts, reads empty stdin, exits cleanly.

- [ ] **Step 6: Verify initialize + tools/list over stdio**

Run:
```bash
printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}\n{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}\n' | timeout 5 ./bin/crest-spec 2>/dev/null
```
Expected: Two JSON-RPC responses. First contains `"protocolVersion":"2024-11-05"`. Second contains all 33 tools.

- [ ] **Step 7: Commit**

```bash
git add cmd/crest-spec/main.go
git commit -m "feat: wire engine and MCP server in main.go with HTTP transport support"
```

- [ ] **Step 8: Final verification**

```bash
make test
make build
go test -race ./...
```

Expected: All tests pass, binary builds successfully.
