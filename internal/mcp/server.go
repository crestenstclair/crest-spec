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
	Commit(ctx context.Context, sessionID, resourceID string, files []specmod.CommitFile, notes string, invariantChecks []specmod.InvariantCheckInput, model string) (*specmod.CommitResult, error)
	Finish(ctx context.Context, sessionID string, force bool) (*specmod.FinishResult, error)
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
	}

	s.registerTools()

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
