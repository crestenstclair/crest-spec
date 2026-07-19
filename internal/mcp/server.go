package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/crestenstclair/crest-spec/internal/config"
	executionpkg "github.com/crestenstclair/crest-spec/internal/execution"
	impactpkg "github.com/crestenstclair/crest-spec/internal/impact"
	observationpkg "github.com/crestenstclair/crest-spec/internal/observability"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	storemod "github.com/crestenstclair/crest-spec/internal/store"
)

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
type toolHandler func(ctx context.Context, args json.RawMessage) toolResult

// specHandler is the consumer-side surface of the Spec engine.
type specHandler interface {
	Plan(ctx context.Context) (*specmod.PlanResult, error)
	Begin(ctx context.Context, opts specmod.BeginOpts) (*specmod.BeginResult, error)
	ConfirmDestroys(ctx context.Context, sessionID string, resourceIDs []string) ([]specmod.DestroyedResource, error)
	Next(ctx context.Context, sessionID string) (*specmod.NextResult, error)
	Context(ctx context.Context, sessionID, resourceID string) (*specmod.ContextResult, error)
	PrepareContext(ctx context.Context, opts specmod.ContextOptions) (*specmod.ContextResult, error)
	PrepareWaveContexts(ctx context.Context, opts specmod.WaveContextOptions) (*specmod.WaveContextResult, error)
	ReportDispatchEvent(ctx context.Context, opts specmod.DispatchEventOptions) (*storemod.DispatchLifecycle, error)
	InspectContext(ctx context.Context, manifestID, attemptID string) (*storemod.ContextManifest, error)
	ListContextAttempts(ctx context.Context, limit int) ([]storemod.ContextManifestSummary, error)
	CompareContexts(ctx context.Context, leftManifestID, rightManifestID string) (*specmod.ContextManifestComparison, error)
	StartExecution(ctx context.Context, opts specmod.ExecutionStartOptions) (*storemod.ExecutionManifest, error)
	ReportExecution(ctx context.Context, opts specmod.ExecutionReportOptions) (*storemod.ExecutionManifest, error)
	ExecutionRoles() []executionpkg.RolePolicy
	InspectExecution(ctx context.Context, id, attemptID string) (*storemod.ExecutionManifest, error)
	ListExecutions(ctx context.Context, limit int) ([]storemod.ExecutionManifest, error)
	RecoverWorkspaceCommits(ctx context.Context) (int, error)
	RecoverExecutions(ctx context.Context, dispatchBefore, executionBefore time.Time) (int, error)
	ListFailures(ctx context.Context, attemptID string, limit int) ([]storemod.FailureClassification, error)
	ClassifyFailure(ctx context.Context, opts specmod.FailureClassifyOptions) (*storemod.FailureClassification, error)
	ResolveFailure(ctx context.Context, id, resolution, resolvedByAttempt string) error
	ListHandoffs(ctx context.Context, attemptID string, limit int) ([]storemod.AttemptHandoff, error)
	CommitAttempt(ctx context.Context, attemptID string, files []specmod.CommitFile, notes string, metadata specmod.CommitMetadata) (*specmod.CommitResult, error)
	Finish(ctx context.Context, sessionID string, force bool) (*specmod.FinishResult, error)
	Abandon(ctx context.Context, sessionID, reason string, invalidateCommitted bool) (*specmod.AbandonResult, error)
	Resolve(ctx context.Context, sessionID, resourceID, answer string, model string) error
	Note(ctx context.Context, sessionID, resourceID, content string) error
	Amend(ctx context.Context, sessionID, resourceID string) error
	Skip(ctx context.Context, sessionID, resourceID, reason string) error
	Status(ctx context.Context) (*specmod.StatusResult, error)
	ProjectOverview(ctx context.Context) (*specmod.ProjectOverviewResult, error)
	ObserveProject(ctx context.Context) (*observationpkg.ProjectOverview, error)
	ObserveGoals(ctx context.Context, request observationpkg.PageRequest) (*observationpkg.Page[observationpkg.GoalSummary], error)
	ObserveGoal(ctx context.Context, id string) (*observationpkg.GoalDetail, error)
	ObserveCapabilities(ctx context.Context, request observationpkg.PageRequest) (*observationpkg.Page[observationpkg.CapabilitySummary], error)
	ObserveCapability(ctx context.Context, id string) (*observationpkg.CapabilityDetail, error)
	ObserveResources(ctx context.Context, request observationpkg.PageRequest) (*observationpkg.Page[observationpkg.ResourceSummary], error)
	ObserveResource(ctx context.Context, id string) (*observationpkg.ResourceDetail, error)
	ObservePlan(ctx context.Context) (*observationpkg.PlanView, error)
	ObserveAttempt(ctx context.Context, id string) (*observationpkg.AttemptDetail, error)
	CompareAttempts(ctx context.Context, left, right string) (*observationpkg.AttemptComparison, error)
	ObserveFailures(ctx context.Context, request observationpkg.PageRequest) (*observationpkg.Page[observationpkg.FailureSummary], error)
	ObserveFailure(ctx context.Context, id string) (*observationpkg.FailureDetail, error)
	Impact(ctx context.Context, resourceID string) (*impactpkg.Result, error)
	Log(ctx context.Context, limit int) ([]storemod.Apply, error)
	History(ctx context.Context, resourceID string, limit int) ([]storemod.Generation, error)
	GraphInfo(ctx context.Context) (*specmod.GraphResult, error)
	Validate(ctx context.Context) (*specmod.ValidateResult, error)
	ValidateResource(ctx context.Context, resourceID string) (*specmod.ValidateResourceResult, error)
	Inspect(ctx context.Context, resourceID string) (*specmod.InspectResult, error)
	Unlock(ctx context.Context) error
	DiffApplies(ctx context.Context, applyIDA, applyIDB string) (*specmod.DiffResult, error)
	Vacuum(ctx context.Context, before time.Time) (int, error)
	ReadOnlyQuery(ctx context.Context, query string) ([]map[string]interface{}, error)
	RemoveResource(ctx context.Context, resourceID string) error
	Import(ctx context.Context, opts specmod.ImportOpts) (*specmod.ImportResult, error)
	Prompt(ctx context.Context, resourceID string) (*specmod.PromptResult, error)
	Bootstrap(ctx context.Context, opts specmod.BootstrapOpts) (*specmod.BootstrapResult, error)
	SessionStatus(ctx context.Context, sessionID string) (*specmod.SessionStatusResult, error)
	WaveStatus(ctx context.Context, sessionID string, waveIndex int) (*specmod.WaveStatusResult, error)
	VerifyWave(ctx context.Context, sessionID string, waveIndex int) *specmod.WaveVerifyResult
	EvolvePrompt(ctx context.Context, sessionID string) (string, error)
	RecordLearnings(ctx context.Context, sessionID, output string) (int, error)
	ListLearnings(status string) ([]storemod.Learning, error)
	PromoteLearnings(ctx context.Context, lang string, minConfidence float64, minTimesApplied int, apply bool, templatePath string) (specmod.PromoteResult, error)
	CreateHistoricalEvaluationCase(ctx context.Context, attemptID, projectName string) (*storemod.EvaluationCaseAssessment, error)
	CreateCuratedEvaluationCase(ctx context.Context, input storemod.CuratedEvaluationCase) (*storemod.EvaluationCase, error)
	GetEvaluationCase(ctx context.Context, id string) (*storemod.EvaluationCase, error)
	ListEvaluationCases(ctx context.Context, limit int) ([]storemod.EvaluationCase, error)
	CreateEvaluationDataset(ctx context.Context, name, description string) (*storemod.EvaluationDataset, error)
	AddEvaluationDatasetCase(ctx context.Context, datasetID, caseID, split string) error
	SealEvaluationDataset(ctx context.Context, datasetID string) (*storemod.EvaluationDataset, error)
	GetEvaluationDataset(ctx context.Context, id string) (*storemod.EvaluationDataset, error)
	ListEvaluationDatasets(ctx context.Context, limit int) ([]storemod.EvaluationDataset, error)
	CreateEvaluationConfiguration(ctx context.Context, input storemod.EvaluationConfiguration) (*storemod.EvaluationConfiguration, error)
	GetEvaluationConfiguration(ctx context.Context, id string) (*storemod.EvaluationConfiguration, error)
	ListEvaluationConfigurations(ctx context.Context, limit int) ([]storemod.EvaluationConfiguration, error)
	CreateEvaluationRun(ctx context.Context, datasetID, name string, variants []storemod.EvaluationRunVariantInput, policy storemod.EvaluationMetricPolicy, minimumSampleSize int, practicalSignificance float64, requireHeldOut bool) (*storemod.EvaluationRun, error)
	GetEvaluationRun(ctx context.Context, id string) (*storemod.EvaluationRun, error)
	ListEvaluationRuns(ctx context.Context, limit int) ([]storemod.EvaluationRun, error)
	ClaimEvaluationAssignment(ctx context.Context, runID, owner, split string, leaseDuration time.Duration) (*storemod.EvaluationAssignmentClaim, error)
	HeartbeatEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token string, extension time.Duration) (time.Time, error)
	ReleaseEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token string) error
	CancelEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token, reason string) (*storemod.EvaluationAssignment, error)
	SubmitEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token string, result storemod.EvaluationAssignmentResult) (*storemod.EvaluationAssignment, error)
	FinalizeEvaluationRun(ctx context.Context, runID string) (*storemod.EvaluationRun, error)
	GetEvaluationPromotion(ctx context.Context, id string) (*storemod.EvaluationPromotionProposal, error)
	ListEvaluationPromotions(ctx context.Context, status string, limit int) ([]storemod.EvaluationPromotionProposal, error)
	ProposeLearningPromotion(ctx context.Context, lang string, minConfidence float64, minTimesApplied int, templatePath, runID, variantName string) (specmod.LearningPromotionResult, error)
	DecideLearningPromotion(ctx context.Context, proposalID, decision, actor, reason string) (*storemod.EvaluationPromotionProposal, error)
	ApplyLearningPromotion(ctx context.Context, proposalID, actor, reason string) (*storemod.EvaluationPromotionProposal, error)
	ApplyAmendments(ctx context.Context, resourceID string, proposals []specmod.ProposedAmendment, apply bool) (*specmod.AmendmentApplyResult, error)
	ListAmendments(ctx context.Context, resourceID, state string) ([]storemod.Amendment, error)
	GraduateAmendment(ctx context.Context, resourceID, name string, apply bool) (*specmod.GraduationResult, error)
	// Behavioral pipeline
	DesignCommit(ctx context.Context, contextName, contractJSON string) error
	TasksCommit(ctx context.Context, resourceID string, tasks []specmod.TaskInput) error
	VerifyWitness(ctx context.Context, witnessID, checkID, sessionID string) (specmod.VerifyResult, error)
	ListValidationRuns(ctx context.Context, limit int) ([]storemod.ValidationRun, error)
	GetValidationRun(ctx context.Context, runID string) (*storemod.ValidationRun, error)
	ListVerificationDefinitions(ctx context.Context, projectName string) ([]storemod.VerificationDefinition, error)
	Graduate(ctx context.Context, checkID string) (specmod.GraduateResult, error)
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Server is the MCP JSON-RPC server.
type Server struct {
	spec      specHandler
	stdin     io.Reader
	stdout    io.Writer
	log       zerolog.Logger
	cfg       *config.Config
	metrics   *Metrics
	asyncWg   sync.WaitGroup
	outMu     sync.Mutex
	tools     []toolDef
	dispatch  map[string]handlerFunc
	toolFns   map[string]toolHandler
	startTime time.Time
	build     processBuildIdentity

	startupRecoveryOnce sync.Once
	recoveryAttemptMu   sync.Mutex
	recoveryMu          sync.RWMutex
	recoveryBlocker     *RecoveryBlocker
}

// RecoveryBlocker is a fail-closed lifecycle-recovery fault that still leaves
// the MCP diagnostic surface available. In particular, workspace recovery may
// discover bytes written by neither side of an interrupted commit. CrestSpec
// must never overwrite that third state, but exiting before initialize would
// also make the fault impossible to inspect through the framework.
type RecoveryBlocker struct {
	Active           bool   `json:"active"`
	Kind             string `json:"kind"`
	Stage            string `json:"stage"`
	Message          string `json:"message"`
	CorrectiveAction string `json:"corrective_action"`
	ObservedAt       string `json:"observed_at"`
}

// New creates a new MCP Server.
func New(
	spec specHandler,
	stdin io.Reader,
	stdout io.Writer,
	log zerolog.Logger,
	cfg *config.Config,
) *Server {
	s := &Server{
		spec:      spec,
		stdin:     stdin,
		stdout:    stdout,
		log:       log,
		cfg:       cfg,
		metrics:   NewMetrics(),
		toolFns:   make(map[string]toolHandler),
		startTime: time.Now(),
		build:     currentProcessBuildIdentity(),
	}

	s.registerTools()

	return s
}

// Run starts the stdio transport. It blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	s.prepareStartupRecovery(ctx)
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

// prepareStartupRecovery runs before request serving in the stdio transport
// and is also called lazily by initialize/tool dispatch for HTTP safety. A
// recovery conflict becomes a structured blocker instead of terminating the
// transport before diagnostics are available.
func (s *Server) prepareStartupRecovery(ctx context.Context) {
	s.startupRecoveryOnce.Do(func() {
		_ = s.recoverLifecycle(ctx)
	})
}

func (s *Server) recoverLifecycle(ctx context.Context) error {
	if s.spec == nil {
		s.clearRecoveryBlocker()
		return nil
	}
	s.recoveryAttemptMu.Lock()
	defer s.recoveryAttemptMu.Unlock()

	recovered, err := s.spec.RecoverWorkspaceCommits(ctx)
	if err != nil {
		return s.blockRecovery("workspace_commit_recovery", err)
	}
	if recovered > 0 {
		s.log.Info().Int("count", recovered).Msg("recovered interrupted workspace commits")
	}
	if s.cfg != nil {
		dispatchBefore, executionBefore := s.executionRecoveryCutoffs()
		recovered, err = s.spec.RecoverExecutions(ctx, dispatchBefore, executionBefore)
		if err != nil {
			return s.blockRecovery("execution_lifecycle_recovery", err)
		}
		if recovered > 0 {
			s.log.Info().Int("count", recovered).Msg("recovered abandoned executions")
		}
	}
	s.clearRecoveryBlocker()
	return nil
}

// ensureWorkspaceRecovery is the mutation/scheduling preflight. It catches a
// journal that became pending after process startup (for example when an
// editor raced a commit) before any unrelated state-changing tool can run.
func (s *Server) ensureWorkspaceRecovery(ctx context.Context) error {
	if s.spec == nil {
		return nil
	}
	if s.recoveryBlockerSnapshot() != nil {
		return s.recoverLifecycle(ctx)
	}
	s.recoveryAttemptMu.Lock()
	defer s.recoveryAttemptMu.Unlock()
	recovered, err := s.spec.RecoverWorkspaceCommits(ctx)
	if err != nil {
		return s.blockRecovery("workspace_commit_recovery", err)
	}
	if recovered > 0 {
		s.log.Info().Int("count", recovered).Msg("recovered interrupted workspace commits")
	}
	return nil
}

func (s *Server) executionRecoveryCutoffs() (time.Time, time.Time) {
	if s.cfg == nil {
		return time.Time{}, time.Time{}
	}
	now := time.Now().UTC()
	var dispatchBefore, executionBefore time.Time
	if timeout := s.cfg.EffectiveDispatchTimeout(); timeout > 0 {
		dispatchBefore = now.Add(-timeout)
	}
	if s.cfg.ExecutionTimeout > 0 {
		executionBefore = now.Add(-s.cfg.ExecutionTimeout)
	}
	return dispatchBefore, executionBefore
}

func (s *Server) blockRecovery(stage string, err error) error {
	wrapped := fmt.Errorf("%s: %w", stage, err)
	blocker := &RecoveryBlocker{
		Active: true, Kind: "recovery_blocked", Stage: stage, Message: err.Error(),
		CorrectiveAction: "inspect the pending workspace commit and reconcile the divergent file; then retry a state-changing spec tool or restart CrestSpec (third-party bytes will never be overwritten automatically)",
		ObservedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}
	s.recoveryMu.Lock()
	s.recoveryBlocker = blocker
	s.recoveryMu.Unlock()
	s.log.Error().Err(wrapped).Msg("lifecycle recovery blocked; diagnostic MCP surface remains available")
	return wrapped
}

func (s *Server) clearRecoveryBlocker() {
	s.recoveryMu.Lock()
	s.recoveryBlocker = nil
	s.recoveryMu.Unlock()
}

func (s *Server) recoveryBlockerSnapshot() *RecoveryBlocker {
	s.recoveryMu.RLock()
	defer s.recoveryMu.RUnlock()
	if s.recoveryBlocker == nil {
		return nil
	}
	copy := *s.recoveryBlocker
	return &copy
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

	// Notifications (no ID) -- fire and forget, no response per JSON-RPC spec
	if req.ID == nil {
		handler(ctx, nil, req.Params)
		return
	}

	s.asyncWg.Add(1)
	go func() {
		defer s.asyncWg.Done()
		resp := handler(ctx, req.ID, req.Params)
		s.writeResponse(resp)
	}()
}

// shutdown waits for in-flight requests to finish.
func (s *Server) shutdown() {
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
	if _, err := s.stdout.Write(data); err != nil {
		s.log.Error().Err(err).Msg("write response failed")
	}
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
