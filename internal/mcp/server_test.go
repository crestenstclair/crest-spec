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
	executionpkg "github.com/crestenstclair/crest-spec/internal/execution"
	impactpkg "github.com/crestenstclair/crest-spec/internal/impact"
	observationpkg "github.com/crestenstclair/crest-spec/internal/observability"
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
	specHandler
	lastFiles          []specmod.CommitFile
	lastNotes          string
	lastModel          string
	lastAttempt        string
	lastExecutionStart specmod.ExecutionStartOptions
	lastVerifySession  string
	lastVerifyWave     int
	lastWitnessID      string
	lastWitnessCheckID string
	lastWitnessSession string
	lastContext        specmod.ContextOptions
	evaluationRuns     []storemod.EvaluationRun
	lastEvalRunID      string
	lastEvalOwner      string
	lastEvalSplit      string
	lastEvalLease      time.Duration
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
func (f *fakeSpec) PrepareContext(ctx context.Context, opts specmod.ContextOptions) (*specmod.ContextResult, error) {
	f.lastContext = opts
	return &specmod.ContextResult{AttemptID: "attempt-test", ContextManifestID: "manifest-test"}, nil
}
func (f *fakeSpec) InspectContext(ctx context.Context, manifestID, attemptID string) (*storemod.ContextManifest, error) {
	return &storemod.ContextManifest{ID: manifestID}, nil
}
func (f *fakeSpec) ListContextAttempts(ctx context.Context, limit int) ([]storemod.ContextManifestSummary, error) {
	return []storemod.ContextManifestSummary{{ID: "manifest-test"}}, nil
}
func (f *fakeSpec) CompareContexts(ctx context.Context, leftManifestID, rightManifestID string) (*specmod.ContextManifestComparison, error) {
	return &specmod.ContextManifestComparison{LeftManifestID: leftManifestID, RightManifestID: rightManifestID}, nil
}
func (f *fakeSpec) StartExecution(ctx context.Context, opts specmod.ExecutionStartOptions) (*storemod.ExecutionManifest, error) {
	f.lastExecutionStart = opts
	return &storemod.ExecutionManifest{ID: "execution-test", AttemptID: opts.AttemptID}, nil
}
func (f *fakeSpec) ReportExecution(ctx context.Context, opts specmod.ExecutionReportOptions) (*storemod.ExecutionManifest, error) {
	return &storemod.ExecutionManifest{AttemptID: opts.AttemptID, Status: opts.Status}, nil
}
func (f *fakeSpec) ExecutionRoles() []executionpkg.RolePolicy { return executionpkg.Roles() }
func (f *fakeSpec) InspectExecution(ctx context.Context, id, attemptID string) (*storemod.ExecutionManifest, error) {
	return &storemod.ExecutionManifest{ID: id, AttemptID: attemptID}, nil
}
func (f *fakeSpec) ListExecutions(ctx context.Context, limit int) ([]storemod.ExecutionManifest, error) {
	return nil, nil
}
func (f *fakeSpec) RecoverExecutions(ctx context.Context, before time.Time) (int, error) {
	return 0, nil
}
func (f *fakeSpec) ListFailures(ctx context.Context, attemptID string, limit int) ([]storemod.FailureClassification, error) {
	return nil, nil
}
func (f *fakeSpec) ClassifyFailure(ctx context.Context, opts specmod.FailureClassifyOptions) (*storemod.FailureClassification, error) {
	return &storemod.FailureClassification{AttemptID: opts.AttemptID, Category: opts.Category, Origin: opts.Origin}, nil
}
func (f *fakeSpec) ResolveFailure(ctx context.Context, id, resolution, resolvedByAttempt string) error {
	return nil
}
func (f *fakeSpec) ListHandoffs(ctx context.Context, attemptID string, limit int) ([]storemod.AttemptHandoff, error) {
	return nil, nil
}
func (f *fakeSpec) CommitAttempt(ctx context.Context, attemptID string, files []specmod.CommitFile, notes string, metadata specmod.CommitMetadata) (*specmod.CommitResult, error) {
	f.lastAttempt = attemptID
	f.lastFiles = files
	f.lastNotes = notes
	return &specmod.CommitResult{AttemptID: attemptID}, nil
}
func (f *fakeSpec) Commit(ctx context.Context, sessionID, resourceID string, files []specmod.CommitFile, notes string, model string) (*specmod.CommitResult, error) {
	f.lastFiles = files
	f.lastNotes = notes
	f.lastModel = model
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
func (f *fakeSpec) ProjectOverview(ctx context.Context) (*specmod.ProjectOverviewResult, error) {
	return &specmod.ProjectOverviewResult{}, nil
}
func (f *fakeSpec) ObserveProject(context.Context) (*observationpkg.ProjectOverview, error) {
	return &observationpkg.ProjectOverview{Version: observationpkg.APIVersion, Project: observationpkg.Project{Name: "fixture"}}, nil
}
func (f *fakeSpec) ObserveGoals(_ context.Context, request observationpkg.PageRequest) (*observationpkg.Page[observationpkg.GoalSummary], error) {
	return &observationpkg.Page[observationpkg.GoalSummary]{Version: observationpkg.APIVersion, Items: []observationpkg.GoalSummary{{ID: "goal.fixture"}}, Page: observationpkg.PageInfo{Limit: request.Limit}}, nil
}
func (f *fakeSpec) ObserveGoal(_ context.Context, id string) (*observationpkg.GoalDetail, error) {
	return &observationpkg.GoalDetail{Version: observationpkg.APIVersion, Goal: observationpkg.GoalSummary{ID: id}}, nil
}
func (f *fakeSpec) ObserveCapabilities(_ context.Context, request observationpkg.PageRequest) (*observationpkg.Page[observationpkg.CapabilitySummary], error) {
	return &observationpkg.Page[observationpkg.CapabilitySummary]{Version: observationpkg.APIVersion, Items: []observationpkg.CapabilitySummary{{ID: "capability.fixture"}}, Page: observationpkg.PageInfo{Limit: request.Limit}}, nil
}
func (f *fakeSpec) ObserveCapability(_ context.Context, id string) (*observationpkg.CapabilityDetail, error) {
	return &observationpkg.CapabilityDetail{Version: observationpkg.APIVersion, Capability: observationpkg.CapabilitySummary{ID: id}}, nil
}
func (f *fakeSpec) ObserveResources(_ context.Context, request observationpkg.PageRequest) (*observationpkg.Page[observationpkg.ResourceSummary], error) {
	return &observationpkg.Page[observationpkg.ResourceSummary]{Version: observationpkg.APIVersion, Items: []observationpkg.ResourceSummary{{ID: "resource.fixture"}}, Page: observationpkg.PageInfo{Limit: request.Limit}}, nil
}
func (f *fakeSpec) ObserveResource(_ context.Context, id string) (*observationpkg.ResourceDetail, error) {
	return &observationpkg.ResourceDetail{Version: observationpkg.APIVersion, Resource: observationpkg.ResourceSummary{ID: id}}, nil
}
func (f *fakeSpec) ObservePlan(context.Context) (*observationpkg.PlanView, error) {
	return &observationpkg.PlanView{Version: observationpkg.APIVersion, ProjectName: "fixture", Operations: []observationpkg.PlanOperation{{ID: "modify:fixture"}}}, nil
}
func (f *fakeSpec) ObserveAttempt(_ context.Context, id string) (*observationpkg.AttemptDetail, error) {
	return &observationpkg.AttemptDetail{Version: observationpkg.APIVersion, Attempt: observationpkg.AttemptSummary{ID: id}}, nil
}
func (f *fakeSpec) CompareAttempts(_ context.Context, left, right string) (*observationpkg.AttemptComparison, error) {
	return &observationpkg.AttemptComparison{Version: observationpkg.APIVersion, Left: observationpkg.AttemptDetail{Attempt: observationpkg.AttemptSummary{ID: left}}, Right: observationpkg.AttemptDetail{Attempt: observationpkg.AttemptSummary{ID: right}}}, nil
}
func (f *fakeSpec) ObserveFailures(_ context.Context, request observationpkg.PageRequest) (*observationpkg.Page[observationpkg.FailureSummary], error) {
	return &observationpkg.Page[observationpkg.FailureSummary]{Version: observationpkg.APIVersion, Items: []observationpkg.FailureSummary{{ID: "failure.fixture", Category: request.Kind}}, Page: observationpkg.PageInfo{Limit: request.Limit}}, nil
}
func (f *fakeSpec) ObserveFailure(_ context.Context, id string) (*observationpkg.FailureDetail, error) {
	return &observationpkg.FailureDetail{Version: observationpkg.APIVersion, Failure: observationpkg.FailureSummary{ID: id}}, nil
}
func (f *fakeSpec) Impact(ctx context.Context, resourceID string) (*impactpkg.Result, error) {
	return &impactpkg.Result{SourceResource: resourceID}, nil
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
func (f *fakeSpec) VerifyWave(ctx context.Context, sessionID string, waveIndex int) *specmod.WaveVerifyResult {
	f.lastVerifySession = sessionID
	f.lastVerifyWave = waveIndex
	return &specmod.WaveVerifyResult{WaveIndex: waveIndex, Passed: true}
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
func (f *fakeSpec) GetEvaluationRun(_ context.Context, id string) (*storemod.EvaluationRun, error) {
	for _, run := range f.evaluationRuns {
		if run.ID == id {
			return &run, nil
		}
	}
	return &storemod.EvaluationRun{ID: id}, nil
}
func (f *fakeSpec) ListEvaluationRuns(_ context.Context, _ int) ([]storemod.EvaluationRun, error) {
	return f.evaluationRuns, nil
}
func (f *fakeSpec) ClaimEvaluationAssignment(_ context.Context, runID, owner, split string, lease time.Duration) (*storemod.EvaluationAssignmentClaim, error) {
	f.lastEvalRunID, f.lastEvalOwner, f.lastEvalSplit, f.lastEvalLease = runID, owner, split, lease
	return &storemod.EvaluationAssignmentClaim{Assignment: storemod.EvaluationAssignment{ID: "assignment-1", RunID: runID}}, nil
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
func (f *fakeSpec) DesignCommit(ctx context.Context, contextName, contractJSON string) error {
	return nil
}
func (f *fakeSpec) TasksCommit(ctx context.Context, resourceID string, tasks []specmod.TaskInput) error {
	return nil
}
func (f *fakeSpec) VerifyWitness(ctx context.Context, witnessID, checkID, sessionID string) (specmod.VerifyResult, error) {
	f.lastWitnessID, f.lastWitnessCheckID, f.lastWitnessSession = witnessID, checkID, sessionID
	return specmod.VerifyResult{WitnessID: witnessID, Classification: "passed", Passed: true}, nil
}
func (f *fakeSpec) ListValidationRuns(ctx context.Context, limit int) ([]storemod.ValidationRun, error) {
	return nil, nil
}
func (f *fakeSpec) GetValidationRun(ctx context.Context, runID string) (*storemod.ValidationRun, error) {
	return nil, nil
}
func (f *fakeSpec) ListVerificationDefinitions(ctx context.Context, projectName string) ([]storemod.VerificationDefinition, error) {
	return nil, nil
}
func (f *fakeSpec) Graduate(ctx context.Context, checkID string) (specmod.GraduateResult, error) {
	return specmod.GraduateResult{}, nil
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
	experimental, ok := caps["experimental"].(map[string]any)
	require.True(t, ok)
	protocol, ok := experimental["crestExecution"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, executionpkg.ProtocolVersion, protocol["protocolVersion"])
	assert.Equal(t, executionpkg.RolePolicyVersion, protocol["rolePolicyVersion"])
	assert.Equal(t, false, protocol["legacyCommit"])
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

	// New tools.
	assert.True(t, names["spec/record_learnings"])
	assert.True(t, names["spec/context_attempts"])
	assert.True(t, names["spec/context_inspect"])
	assert.True(t, names["spec/context_compare"])
	assert.True(t, names["spec/attempt_inspect"])
	assert.True(t, names["spec/attempt_compare"])
	assert.True(t, names["spec/capabilities"])
	assert.True(t, names["spec/execution_start"])
	assert.True(t, names["spec/execution_report"])
	assert.True(t, names["spec/execution_roles"])
	assert.True(t, names["spec/execution_inspect"])
	assert.True(t, names["spec/failures"])
	assert.True(t, names["spec/verify"])
	assert.True(t, names["spec/verifications"])
	assert.True(t, names["spec/verification_inspect"])
	assert.True(t, names["spec/verification_definitions"])
	assert.True(t, names["spec/failure_classify"])
	assert.True(t, names["spec/failure_resolve"])
	assert.True(t, names["spec/handoffs"])
	assert.True(t, names["spec/evaluation_cases"])
	assert.True(t, names["spec/evaluation_runs"])
	assert.True(t, names["spec/evaluation_assignment_claim"])
	assert.True(t, names["spec/evaluation_promotions"])

	// Spot-check kept spec tools.
	for _, kept := range []string{
		"spec/plan", "spec/begin", "spec/next", "spec/context",
		"spec/commit", "spec/finish", "spec/evolve", "spec/sql",
		"spec/project_overview",
		"spec/goals", "spec/capabilities", "spec/resources", "spec/plan_inspect",
		"spec/impact",
		"spec/unlock", "spec/apply_amendments", "spec/list_amendments",
		"spec/graduate_amendment",
	} {
		assert.True(t, names[kept], "tool %q should be registered", kept)
	}
}

func TestTypedObservationToolsShareDashboardContracts(t *testing.T) {
	srv := New(&fakeSpec{}, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	project := srv.toolFns["spec/project_overview"](context.Background(), json.RawMessage(`{}`))
	require.False(t, project.IsError)
	assert.Contains(t, project.Content[0].Text, `"version":"v1"`)
	assert.Contains(t, project.Content[0].Text, `"name":"fixture"`)

	goals := srv.toolFns["spec/goals"](context.Background(), json.RawMessage(`{"limit":20,"status":"blocked"}`))
	require.False(t, goals.IsError)
	assert.Contains(t, goals.Content[0].Text, `"id":"goal.fixture"`)

	resource := srv.toolFns["spec/resources"](context.Background(), json.RawMessage(`{"id":"aggregate.Fixture"}`))
	require.False(t, resource.IsError)
	assert.Contains(t, resource.Content[0].Text, `"id":"aggregate.Fixture"`)

	comparison := srv.toolFns["spec/attempt_compare"](context.Background(), json.RawMessage(`{"left_attempt_id":"left","right_attempt_id":"right"}`))
	require.False(t, comparison.IsError)
	assert.Contains(t, comparison.Content[0].Text, `"id":"left"`)
	assert.Contains(t, comparison.Content[0].Text, `"id":"right"`)
}

func TestCommitToolForwardsArgs(t *testing.T) {
	fake := &fakeSpec{}
	srv := New(fake, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	args := json.RawMessage(`{"attempt_id":"attempt-1","files":[{"path":"a.go","content":"x"}],"notes":"kept small"}`)
	srv.toolFns["spec/commit"](context.Background(), args)
	assert.Equal(t, "attempt-1", fake.lastAttempt)
	assert.Equal(t, "kept small", fake.lastNotes)
	require.Len(t, fake.lastFiles, 1)
	assert.Equal(t, "a.go", fake.lastFiles[0].Path)
}

func TestExecutionStartToolForwardsGovernedInputs(t *testing.T) {
	fake := &fakeSpec{}
	srv := New(fake, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	args := json.RawMessage(`{"attempt_id":"attempt-1","protocol_version":"crest-execution-v1","idempotency_key":"request-1","context_hash":"context-hash","role":"resource_implementer","host_name":"claude-code","model":"claude-sonnet","template_hashes":{"system":"template-hash"},"tools":[{"name":"filesystem","permission":"project"}]}`)
	result := srv.toolFns["spec/execution_start"](context.Background(), args)
	require.False(t, result.IsError)
	require.Equal(t, "attempt-1", fake.lastExecutionStart.AttemptID)
	require.Equal(t, "crest-execution-v1", fake.lastExecutionStart.ProtocolVersion)
	require.Equal(t, "resource_implementer", fake.lastExecutionStart.Role)
	require.Equal(t, "filesystem", fake.lastExecutionStart.Tools[0].Name)
}

func TestEvaluationToolsExposeRunsAndForwardLeaseClaims(t *testing.T) {
	fake := &fakeSpec{evaluationRuns: []storemod.EvaluationRun{{ID: "run-1", Name: "selector comparison", Status: "running"}}}
	srv := New(fake, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	listed := srv.toolFns["spec/evaluation_runs"](context.Background(), json.RawMessage(`{"limit":10}`))
	require.False(t, listed.IsError)
	assert.Contains(t, listed.Content[0].Text, `"id":"run-1"`)

	claimed := srv.toolFns["spec/evaluation_assignment_claim"](context.Background(), json.RawMessage(`{"run_id":"run-1","owner":"host-a","split":"held_out","lease_seconds":90}`))
	require.False(t, claimed.IsError)
	assert.Equal(t, "run-1", fake.lastEvalRunID)
	assert.Equal(t, "host-a", fake.lastEvalOwner)
	assert.Equal(t, "held_out", fake.lastEvalSplit)
	assert.Equal(t, 90*time.Second, fake.lastEvalLease)
	assert.Contains(t, claimed.Content[0].Text, `"assignment":{"id":"assignment-1"`)
}

func TestFailureClassifyToolReturnsAppendOnlyClassification(t *testing.T) {
	fake := &fakeSpec{}
	srv := New(fake, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	args := json.RawMessage(`{"attempt_id":"attempt-1","category":"missing_context","origin":"host","confidence":0.8,"evidence_source":"triage"}`)
	result := srv.toolFns["spec/failure_classify"](context.Background(), args)
	require.False(t, result.IsError)
	require.Contains(t, result.Content[0].Text, `"Category":"missing_context"`)
}

func TestContextToolCreatesAttemptWithSelectionOptions(t *testing.T) {
	fake := &fakeSpec{}
	srv := New(fake, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	args := json.RawMessage(`{"session_id":"s","resource_id":"r","role":"minimal_diff_repair","budget_tokens":8192,"parent_attempt_id":"parent-1"}`)
	result := srv.toolFns["spec/context"](context.Background(), args)
	require.False(t, result.IsError)
	require.Equal(t, specmod.ContextOptions{
		SessionID: "s", ResourceID: "r", Role: "minimal_diff_repair",
		BudgetTokens: 8192, ParentAttemptID: "parent-1",
	}, fake.lastContext)
	require.Contains(t, result.Content[0].Text, `"AttemptID":"attempt-test"`)
}

func TestVerifyWaveToolForwardsArgs(t *testing.T) {
	fake := &fakeSpec{}
	srv := New(fake, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	args := json.RawMessage(`{"session_id":"sess-1","wave_index":2}`)
	res := srv.toolFns["spec/verify_wave"](context.Background(), args)
	assert.Equal(t, "sess-1", fake.lastVerifySession)
	assert.Equal(t, 2, fake.lastVerifyWave)
	require.False(t, res.IsError)
	assert.Contains(t, res.Content[0].Text, `"Passed":true`)
	assert.Contains(t, res.Content[0].Text, `"WaveIndex":2`)
}

func TestVerifyToolAcceptsOnlyDeclaredWitnessReferences(t *testing.T) {
	fake := &fakeSpec{}
	srv := New(fake, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	result := srv.toolFns["spec/verify"](context.Background(), json.RawMessage(`{"witness_id":"witness.signal","check_id":"check-1","session_id":"session-1","real_observation":{"forged":true}}`))
	require.False(t, result.IsError)
	assert.Equal(t, "witness.signal", fake.lastWitnessID)
	assert.Equal(t, "check-1", fake.lastWitnessCheckID)
	assert.Equal(t, "session-1", fake.lastWitnessSession)
	assert.Contains(t, result.Content[0].Text, `"classification":"passed"`)

	resp := sendRequest(t, srv, nil, "tools/list", 1, nil)
	require.Nil(t, resp.Error)
	tools := resp.Result.(map[string]any)["tools"].([]toolDef)
	for _, definition := range tools {
		if definition.Name != "spec/verify" {
			continue
		}
		assert.Contains(t, string(definition.InputSchema), `"witness_id"`)
		assert.NotContains(t, string(definition.InputSchema), "real_observation")
		assert.NotContains(t, string(definition.InputSchema), "stub_observation")
	}
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
