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
	enginemod "github.com/crestenstclair/crest-spec/internal/engine"
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

func (f *fakeEngine) Generate(ctx context.Context, opts enginemod.GenerateOpts) (*agent.RunResult, error) {
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

func (f *fakeEngine) Review(ctx context.Context, opts enginemod.ReviewOpts) (*agent.RunResult, error) {
	return &agent.RunResult{Output: "PASS"}, nil
}

func (f *fakeEngine) CodeReview(ctx context.Context, opts enginemod.CodeReviewOpts) (*agent.RunResult, error) {
	f.mu.Lock()
	f.codeReviewCalls++
	f.mu.Unlock()
	if f.codeReviewErr != nil {
		return &agent.RunResult{Output: f.codeReviewOut}, f.codeReviewErr
	}
	out := f.codeReviewOut
	if out == "" {
		out = "code review output"
	}
	return &agent.RunResult{Output: out}, nil
}

func (f *fakeEngine) Bugbot(ctx context.Context, opts enginemod.BugbotOpts) (*agent.RunResult, error) {
	f.mu.Lock()
	f.bugbotCalls++
	f.mu.Unlock()
	if f.bugbotErr != nil {
		return &agent.RunResult{Output: f.bugbotOut}, f.bugbotErr
	}
	out := f.bugbotOut
	if out == "" {
		out = "bugbot output"
	}
	return &agent.RunResult{Output: out}, nil
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

func (f *fakeStore) CreateGeneration(g storemod.Generation) error { return nil }
func (f *fakeStore) UpdateGeneration(id, outputText, outcome, rejectionReason string, durationMS, inputTokens, outputTokens int64, costUSD float64) error {
	return nil
}
func (f *fakeStore) GetActiveSession() (*storemod.Session, error) { return nil, nil }
func (f *fakeStore) UpdateSessionResourceState(sessionID, resourceID, state, lastError, lastOutput string, attempts int, jobID string) error {
	return nil
}
func (f *fakeStore) GetSessionResource(sessionID, resourceID string) (*storemod.SessionResource, error) {
	return nil, nil
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
	srv := New(nil, eng, st, noRecursionTree{}, strings.NewReader(""), &stdout, log, cfg)
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
	srv := New(nil, &fakeEngine{}, newFakeStore(), noRecursionTree{}, stdin, &stdout, log, cfg)

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
	srv := New(nil, &fakeEngine{}, newFakeStore(), noRecursionTree{}, stdin, &stdout, log, cfg)

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
	resources, ok := result["resources"].([]map[string]string)
	require.True(t, ok)
	assert.NotEmpty(t, resources)
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
	prompts, ok := result["prompts"].([]map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, prompts)
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
	srv := New(nil, &fakeEngine{}, newFakeStore(), pt, strings.NewReader(""), &stdout, log, cfg)

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
