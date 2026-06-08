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
	enginemod "github.com/crestenstclair/crest-spec/internal/engine"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	storemod "github.com/crestenstclair/crest-spec/internal/store"
)

// ---------------------------------------------------------------------------
// Package-private interfaces
// ---------------------------------------------------------------------------

// engine is the consumer-side surface of the Engine.
type engine interface {
	Generate(ctx context.Context, opts enginemod.GenerateOpts) (*agent.RunResult, error)
	Review(ctx context.Context, opts enginemod.ReviewOpts) (*agent.RunResult, error)
	CodeReview(ctx context.Context, opts enginemod.CodeReviewOpts) (*agent.RunResult, error)
	Bugbot(ctx context.Context, opts enginemod.BugbotOpts) (*agent.RunResult, error)
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
	UpdateJobProgress(id, progressJSON string) error
	CleanupOrphans(aliveFn func(int) bool) (int, error)
	CreateGeneration(g storemod.Generation) error
	UpdateGeneration(id, outputText, outcome, rejectionReason string, durationMS, inputTokens, outputTokens int64, costUSD float64) error
	GetActiveSession() (*storemod.Session, error)
	UpdateSessionResourceState(sessionID, resourceID, state, lastError, lastOutput string, attempts int, jobID string) error
	GetSessionResource(sessionID, resourceID string) (*storemod.SessionResource, error)
	CreateAgentEvent(e storemod.AgentEvent) error
	ListAgentEventsByResource(resourceID string) ([]storemod.AgentEvent, error)
	ListRecentAgentEvents(limit int) ([]storemod.AgentEvent, error)
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
	ProgressToken json.RawMessage `json:"progressToken,omitempty"`
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

// specHandler is the consumer-side surface of the Spec engine.
type specHandler interface {
	Plan(ctx context.Context) (*specmod.PlanResult, error)
	Apply(ctx context.Context, opts specmod.BeginOpts) (*specmod.ApplyResult, error)
	Begin(ctx context.Context, opts specmod.BeginOpts) (*specmod.BeginResult, error)
	ConfirmDestroys(ctx context.Context, sessionID string, resourceIDs []string) ([]specmod.DestroyedResource, error)
	Next(ctx context.Context, sessionID string) (*specmod.NextResult, error)
	Context(ctx context.Context, sessionID, resourceID string) (*specmod.ContextResult, error)
	Commit(ctx context.Context, sessionID, resourceID string, files []specmod.CommitFile, notes string) (*specmod.CommitResult, error)
	Finish(ctx context.Context, sessionID string, force bool) (*specmod.FinishResult, error)
	AdvanceWave(ctx context.Context, sessionID string) error
	Resolve(ctx context.Context, sessionID, resourceID, answer string, model string) error
	Note(ctx context.Context, sessionID, resourceID, content string) error
	Amend(ctx context.Context, sessionID, resourceID string) error
	Skip(ctx context.Context, sessionID, resourceID, reason string) error
	Status(ctx context.Context) (*specmod.StatusResult, error)
	Log(ctx context.Context, limit int) ([]storemod.Apply, error)
	History(ctx context.Context, resourceID string, limit int) ([]storemod.Generation, error)
	GraphInfo(ctx context.Context) (*specmod.GraphResult, error)
	Validate(ctx context.Context) (*specmod.ValidateResult, error)
	ValidateResource(ctx context.Context, resourceID string) (*specmod.ValidateResourceResult, error)
	Inspect(ctx context.Context, resourceID string) (*specmod.InspectResult, error)
	DriftAction(ctx context.Context, action, resourceID string) error
	Unlock(ctx context.Context) error
	DiffApplies(ctx context.Context, applyIDA, applyIDB string) (*specmod.DiffResult, error)
	Vacuum(ctx context.Context, before time.Time) (int, error)
	ReadOnlyQuery(ctx context.Context, query string) ([]map[string]interface{}, error)
	RemoveResource(ctx context.Context, resourceID string) error
	Import(ctx context.Context, opts specmod.ImportOpts) (*specmod.ImportResult, error)
	Prompt(ctx context.Context, resourceID string) (*specmod.PromptResult, error)
	Dispatch(ctx context.Context, opts specmod.DispatchOpts) (*specmod.DispatchResult, error)
	RunWave(ctx context.Context, opts specmod.RunWaveOpts) (*specmod.RunWaveResult, error)
	Bootstrap(ctx context.Context, opts specmod.BootstrapOpts) (*specmod.BootstrapResult, error)
	DeepReview(ctx context.Context, opts specmod.DeepReviewOpts) (*specmod.DeepReviewResult, error)
	SessionStatus(ctx context.Context, sessionID string) (*specmod.SessionStatusResult, error)
	WaveStatus(ctx context.Context, sessionID string, waveIndex int) (*specmod.WaveStatusResult, error)
	Evolve(ctx context.Context, sessionID string) (int, error)
	ListLearnings(status string) ([]storemod.Learning, error)
	PromoteLearnings(ctx context.Context, lang string, minConfidence float64, minTimesApplied int, apply bool, templatePath string) (specmod.PromoteResult, error)
	ProposeAmendments(ctx context.Context, sessionID, resourceID string) ([]specmod.ProposedAmendment, error)
	ApplyAmendments(ctx context.Context, resourceID string, proposals []specmod.ProposedAmendment, apply bool) (*specmod.AmendmentApplyResult, error)
	ListAmendments(ctx context.Context, resourceID, state string) ([]storemod.Amendment, error)
	GraduateAmendment(ctx context.Context, resourceID, name string, apply bool) (*specmod.GraduationResult, error)
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Server is the MCP JSON-RPC server.
type Server struct {
	spec      specHandler
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
	spec specHandler,
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
		spec:      spec,
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
	if _, err := s.stdout.Write(data); err != nil {
		s.log.Error().Err(err).Msg("write response failed")
	}
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
	if _, err := s.stdout.Write(data); err != nil {
		s.log.Error().Err(err).Msg("write notification failed")
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

// runAsync creates a job, launches a background goroutine, and returns immediately
// with a job_id. The goroutine updates the store when the job completes/fails/cancels.
// The callback receives the job context and the job ID so callers can write
// incremental progress to the job record.
func (s *Server) runAsync(
	toolName string,
	fn func(ctx context.Context, jobID string) (string, error),
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
		result, err := fn(jobCtx, id)
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

	return jsonResult(map[string]string{"job_id": id})
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
