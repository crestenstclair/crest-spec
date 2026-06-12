package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	storemod "github.com/crestenstclair/crest-spec/internal/store"
)

// ---------------------------------------------------------------------------
// Fake spec handler
// ---------------------------------------------------------------------------

// fakeSpec is a minimal specHandler used to exercise the registered spec
// tools. Methods return zero values unless a test needs otherwise; the Commit
// method captures its arguments so tests can assert forwarding.
type fakeSpec struct {
	lastFiles           []specmod.CommitFile
	lastNotes           string
	lastModel           string
	lastInvariantChecks []specmod.InvariantCheckInput
}

func (f *fakeSpec) Plan(ctx context.Context) (*specmod.PlanResult, error) {
	return &specmod.PlanResult{}, nil
}
func (f *fakeSpec) Begin(ctx context.Context, opts specmod.BeginOpts) (*specmod.BeginResult, error) {
	return &specmod.BeginResult{}, nil
}
func (f *fakeSpec) ConfirmDestroys(ctx context.Context, sessionID string, resourceIDs []string) ([]specmod.DestroyedResource, error) {
	return nil, nil
}
func (f *fakeSpec) Next(ctx context.Context, sessionID string) (*specmod.NextResult, error) {
	return &specmod.NextResult{}, nil
}
func (f *fakeSpec) Context(ctx context.Context, sessionID, resourceID string) (*specmod.ContextResult, error) {
	return &specmod.ContextResult{}, nil
}
func (f *fakeSpec) Commit(ctx context.Context, sessionID, resourceID string, files []specmod.CommitFile, notes string, invariantChecks []specmod.InvariantCheckInput, model string) (*specmod.CommitResult, error) {
	f.lastFiles = files
	f.lastNotes = notes
	f.lastModel = model
	f.lastInvariantChecks = invariantChecks
	return &specmod.CommitResult{}, nil
}
func (f *fakeSpec) Finish(ctx context.Context, sessionID string, force bool) (*specmod.FinishResult, error) {
	return &specmod.FinishResult{}, nil
}
func (f *fakeSpec) Resolve(ctx context.Context, sessionID, resourceID, answer, model string) error {
	return nil
}
func (f *fakeSpec) Note(ctx context.Context, sessionID, resourceID, content string) error {
	return nil
}
func (f *fakeSpec) Amend(ctx context.Context, sessionID, resourceID string) error { return nil }
func (f *fakeSpec) Skip(ctx context.Context, sessionID, resourceID, reason string) error {
	return nil
}
func (f *fakeSpec) Status(ctx context.Context) (*specmod.StatusResult, error) {
	return &specmod.StatusResult{}, nil
}
func (f *fakeSpec) Log(ctx context.Context, limit int) ([]storemod.Apply, error) { return nil, nil }
func (f *fakeSpec) History(ctx context.Context, resourceID string, limit int) ([]storemod.Generation, error) {
	return nil, nil
}
func (f *fakeSpec) GraphInfo(ctx context.Context) (*specmod.GraphResult, error) {
	return &specmod.GraphResult{}, nil
}
func (f *fakeSpec) Validate(ctx context.Context) (*specmod.ValidateResult, error) {
	return &specmod.ValidateResult{}, nil
}
func (f *fakeSpec) ValidateResource(ctx context.Context, resourceID string) (*specmod.ValidateResourceResult, error) {
	return &specmod.ValidateResourceResult{}, nil
}
func (f *fakeSpec) Inspect(ctx context.Context, resourceID string) (*specmod.InspectResult, error) {
	return &specmod.InspectResult{}, nil
}
func (f *fakeSpec) Unlock(ctx context.Context) error { return nil }
func (f *fakeSpec) DiffApplies(ctx context.Context, applyIDA, applyIDB string) (*specmod.DiffResult, error) {
	return &specmod.DiffResult{}, nil
}
func (f *fakeSpec) Vacuum(ctx context.Context, before time.Time) (int, error) { return 0, nil }
func (f *fakeSpec) ReadOnlyQuery(ctx context.Context, query string) ([]map[string]interface{}, error) {
	return nil, nil
}
func (f *fakeSpec) RemoveResource(ctx context.Context, resourceID string) error { return nil }
func (f *fakeSpec) Import(ctx context.Context, opts specmod.ImportOpts) (*specmod.ImportResult, error) {
	return &specmod.ImportResult{}, nil
}
func (f *fakeSpec) Prompt(ctx context.Context, resourceID string) (*specmod.PromptResult, error) {
	return &specmod.PromptResult{}, nil
}
func (f *fakeSpec) Bootstrap(ctx context.Context, opts specmod.BootstrapOpts) (*specmod.BootstrapResult, error) {
	return &specmod.BootstrapResult{}, nil
}
func (f *fakeSpec) SessionStatus(ctx context.Context, sessionID string) (*specmod.SessionStatusResult, error) {
	return &specmod.SessionStatusResult{}, nil
}
func (f *fakeSpec) WaveStatus(ctx context.Context, sessionID string, waveIndex int) (*specmod.WaveStatusResult, error) {
	return &specmod.WaveStatusResult{}, nil
}
func (f *fakeSpec) EvolvePrompt(ctx context.Context, sessionID string) (string, error) {
	return "", nil
}
func (f *fakeSpec) RecordLearnings(ctx context.Context, sessionID, output string) (int, error) {
	return 0, nil
}
func (f *fakeSpec) ListLearnings(status string) ([]storemod.Learning, error) { return nil, nil }
func (f *fakeSpec) PromoteLearnings(ctx context.Context, lang string, minConfidence float64, minTimesApplied int, apply bool, templatePath string) (specmod.PromoteResult, error) {
	return specmod.PromoteResult{}, nil
}
func (f *fakeSpec) ApplyAmendments(ctx context.Context, resourceID string, proposals []specmod.ProposedAmendment, apply bool) (*specmod.AmendmentApplyResult, error) {
	return &specmod.AmendmentApplyResult{}, nil
}
func (f *fakeSpec) ListAmendments(ctx context.Context, resourceID, state string) ([]storemod.Amendment, error) {
	return nil, nil
}
func (f *fakeSpec) GraduateAmendment(ctx context.Context, resourceID, name string, apply bool) (*specmod.GraduationResult, error) {
	return &specmod.GraduationResult{}, nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// stubServer builds a Server with no spec handler (stub tools registered).
func stubServer() (*Server, *bytes.Buffer) {
	var stdout bytes.Buffer
	log := zerolog.New(io.Discard)
	cfg := &config.Config{}
	srv := New(nil, strings.NewReader(""), &stdout, log, cfg)
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
	srv, _ := stubServer()
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

func toolNameSet(t *testing.T, srv *Server) map[string]bool {
	t.Helper()
	resp := sendRequest(t, srv, nil, "tools/list", 1, nil)
	require.Nil(t, resp.Error)
	result, ok := resp.Result.(map[string]any)
	require.True(t, ok)
	tools, ok := result["tools"].([]toolDef)
	require.True(t, ok)
	names := make(map[string]bool)
	for _, td := range tools {
		names[td.Name] = true
	}
	return names
}

func TestToolsList_DropsRemovedTools(t *testing.T) {
	srv := New(&fakeSpec{}, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	names := toolNameSet(t, srv)

	// Deleted tools must no longer be registered.
	for _, gone := range []string{
		"run_prompt", "poll_result", "cancel_job", "list_jobs",
		"code_review", "bugbot", "list_models", "status",
		"recursion_detected",
		"spec/apply", "spec/dispatch", "spec/run_wave",
		"spec/deep_review", "spec/propose_amendments",
	} {
		assert.False(t, names[gone], "tool %q should have been removed", gone)
	}

	// Kept info tools.
	assert.True(t, names["about"])
	assert.True(t, names["live_metrics"])

	// New tool.
	assert.True(t, names["spec/record_learnings"])

	// Spot-check kept spec tools.
	for _, kept := range []string{
		"spec/plan", "spec/begin", "spec/next", "spec/context",
		"spec/commit", "spec/finish", "spec/evolve", "spec/sql",
		"spec/unlock", "spec/apply_amendments", "spec/list_amendments",
		"spec/graduate_amendment",
	} {
		assert.True(t, names[kept], "tool %q should be registered", kept)
	}
}

func TestCommitToolForwardsInvariantChecks(t *testing.T) {
	fake := &fakeSpec{}
	srv := New(fake, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	args := json.RawMessage(`{"session_id":"s","resource_id":"r","files":[{"path":"a.go","content":"x"}],"model":"claude-sonnet-4-6","invariant_checks":[{"invariant":"no globals","passed":false,"summary":"global var"}]}`)
	srv.toolFns["spec/commit"](context.Background(), args)
	if len(fake.lastInvariantChecks) != 1 || fake.lastInvariantChecks[0].Passed {
		t.Fatal("invariant_checks not forwarded to spec.Commit")
	}
	assert.Equal(t, "claude-sonnet-4-6", fake.lastModel)
	require.Len(t, fake.lastFiles, 1)
	assert.Equal(t, "a.go", fake.lastFiles[0].Path)
}

func TestAbout_Static(t *testing.T) {
	srv, _ := stubServer()
	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "about",
		Arguments: json.RawMessage(`{}`),
	})
	result, ok := resp.Result.(toolResult)
	require.True(t, ok)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content[0].Text, "state engine only")
}

func TestLiveMetrics(t *testing.T) {
	srv, _ := stubServer()

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

func TestHandleToolCall_RecordsMetrics(t *testing.T) {
	srv := New(&fakeSpec{}, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})

	// Call a known tool through the handleToolCall choke point.
	resp := sendRequest(t, srv, nil, "tools/call", 1, toolCallParams{
		Name:      "about",
		Arguments: json.RawMessage(`{}`),
	})
	require.Nil(t, resp.Error)

	snap := srv.metrics.Snapshot()
	require.Contains(t, snap.Tools, "about", "metrics should have an entry for the called tool")
	assert.Equal(t, int64(1), snap.Tools["about"].Calls)
	assert.Equal(t, int64(0), snap.Tools["about"].Errors)
	assert.Equal(t, int64(1), snap.TotalCalls)
}

func TestUnknownMethod(t *testing.T) {
	srv, _ := stubServer()
	resp := sendRequest(t, srv, nil, "nonexistent/method", 1, nil)
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32601, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "Method not found")
}

func TestUnknownTool(t *testing.T) {
	srv, _ := stubServer()
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
	cfg := &config.Config{}
	srv := New(nil, stdin, &stdout, log, cfg)

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
	cfg := &config.Config{}
	srv := New(&fakeSpec{}, stdin, &stdout, log, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	srv.Run(ctx)

	output := stdout.String()
	assert.Contains(t, output, "2024-11-05")
	assert.Contains(t, output, "crest-spec")
	assert.Contains(t, output, "spec/commit")
}

func TestHTTPTransport(t *testing.T) {
	srv := New(&fakeSpec{}, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})

	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"live_metrics","arguments":{}}}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "uptime_seconds")
}

func TestHTTPTransport_MalformedJSON(t *testing.T) {
	srv, _ := stubServer()

	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", strings.NewReader("not json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "-32700")
}

// TestSpecToolStubs_Parity asserts that the stub server (nil spec) exposes
// exactly the same tool names as the real server (fakeSpec). This guards
// against future drift: adding a real tool without a matching stub will fail
// the test immediately.
func TestSpecToolStubs_Parity(t *testing.T) {
	realSrv := New(&fakeSpec{}, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	stubSrv, _ := stubServer()

	realNames := toolNameSet(t, realSrv)
	stubNames := toolNameSet(t, stubSrv)

	for name := range realNames {
		assert.True(t, stubNames[name], "stub server is missing tool %q (add it to registerSpecStubs)", name)
	}
	for name := range stubNames {
		assert.True(t, realNames[name], "stub server has extra tool %q not present in real server", name)
	}
}

// TestSpecToolStubs_ReturnNotImplemented verifies every spec/* stub returns
// the "not implemented" sentinel text.
func TestSpecToolStubs_ReturnNotImplemented(t *testing.T) {
	srv, _ := stubServer()

	// Collect all spec/* tools from the stub server dynamically so this list
	// stays in sync with registerSpecStubs automatically.
	names := toolNameSet(t, srv)
	for tool := range names {
		if !strings.HasPrefix(tool, "spec/") {
			continue
		}
		tool := tool // capture
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
	srv, _ := stubServer()
	resp := sendRequest(t, srv, nil, "resources/list", 1, nil)

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(map[string]any)
	require.True(t, ok)
	resources, ok := result["resources"].([]map[string]string)
	require.True(t, ok)
	assert.NotEmpty(t, resources)
}

func TestResourcesRead_Error(t *testing.T) {
	srv, _ := stubServer()
	resp := sendRequest(t, srv, nil, "resources/read", 1, nil)

	require.NotNil(t, resp.Error)
	assert.Equal(t, -32602, resp.Error.Code)
}

func TestPromptsList_Empty(t *testing.T) {
	srv, _ := stubServer()
	resp := sendRequest(t, srv, nil, "prompts/list", 1, nil)

	assert.Nil(t, resp.Error)
	result, ok := resp.Result.(map[string]any)
	require.True(t, ok)
	prompts, ok := result["prompts"].([]map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, prompts)
}

func TestPromptsGet_Error(t *testing.T) {
	srv, _ := stubServer()
	resp := sendRequest(t, srv, nil, "prompts/get", 1, nil)

	require.NotNil(t, resp.Error)
	assert.Equal(t, -32602, resp.Error.Code)
}
