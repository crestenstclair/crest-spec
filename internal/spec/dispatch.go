package spec

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/store"
)

func (s *Spec) recordAttempts(applyID, resourceID, model string, records []AttemptRecord) {
	for _, rec := range records {
		id := uuid.NewString()
		s.store.CreateGeneration(store.Generation{
			ID:         id,
			ApplyID:    applyID,
			ResourceID: resourceID,
			PromptText: rec.Prompt,
			PromptHash: promptHash(rec.Prompt),
			Model:      model,
			RetryCount: rec.Attempt,
		})
		s.store.UpdateGeneration(
			id, rec.Output, rec.Outcome, rec.Error,
			rec.DurationMS, 0, 0, 0,
		)
	}
}

// DispatchResult is the outcome of dispatching a single resource.
type DispatchResult struct {
	ResourceID  string             `json:"resource_id"`
	Status      string             `json:"status"`
	Files       []CommitFile       `json:"files,omitempty"`
	Validations []ValidationResult `json:"validations,omitempty"`
	Error       string             `json:"error,omitempty"`
	Attempts    int                `json:"attempts"`
	DurationMS  int64              `json:"duration_ms"`
}

// ProgressUpdate contains detailed progress info sent during dispatch.
type ProgressUpdate struct {
	ResourceID string `json:"resource_id"`
	State      string `json:"state"` // "generating", "committed", "rejected", "errored"
	Attempts   int    `json:"attempts"`
	Total      int    `json:"total"`
	Completed  int    `json:"completed"`
	Error      string `json:"error,omitempty"`
}

// ProgressFunc is called after each resource completes during dispatch.
// The MCP handler uses this to send progress notifications over the wire.
type ProgressFunc func(update ProgressUpdate)

// AgentEventFunc is called for each real-time event from the agent subprocess
// and constraint loop stages. The attempt number is 0 for resource-level events
// (started/completed/failed) and 1+ for per-attempt events.
type AgentEventFunc func(resourceID, eventType string, attempt int, content string)

// DispatchOpts configures a single-resource dispatch.
type DispatchOpts struct {
	SessionID    string
	ResourceID   string
	Model        string
	OnProgress   ProgressFunc
	OnAgentEvent AgentEventFunc
}

// RunWaveOpts configures a full wave dispatch.
type RunWaveOpts struct {
	SessionID      string
	Model          string
	ModelOverrides map[string]string
	OnProgress     ProgressFunc
	OnAgentEvent   AgentEventFunc
}

// RunWaveResult is the outcome of dispatching all resources in a wave.
type RunWaveResult struct {
	WaveIndex    int               `json:"wave_index"`
	Done         bool              `json:"done"`
	Committed    []DispatchResult  `json:"committed,omitempty"`
	Rejected     []DispatchResult  `json:"rejected,omitempty"`
	Errored      []DispatchResult  `json:"errored,omitempty"`
	Verification *WaveVerifyResult `json:"verification,omitempty"`
}

func (s *Spec) Dispatch(ctx context.Context, opts DispatchOpts) (*DispatchResult, error) {
	sess, err := s.store.GetSession(opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	model := opts.Model
	if model == "" {
		model = s.cfg.GenerateModel
	}

	if opts.OnProgress != nil {
		opts.OnProgress(ProgressUpdate{
			ResourceID: opts.ResourceID, State: "generating",
			Total: 1, Completed: 0,
		})
	}

	result := s.dispatchResource(ctx, opts.SessionID, sess.ApplyID, opts.ResourceID, model, opts.OnAgentEvent)

	if opts.OnProgress != nil {
		opts.OnProgress(ProgressUpdate{
			ResourceID: result.ResourceID, State: result.Status,
			Attempts: result.Attempts, Total: 1, Completed: 1,
			Error: result.Error,
		})
	}

	return result, nil
}

func (s *Spec) dispatchResource(
	ctx context.Context, sessionID, applyID, resourceID, model string,
	onAgentEvent AgentEventFunc,
) *DispatchResult {
	startTime := time.Now()

	if onAgentEvent != nil {
		onAgentEvent(resourceID, "started", 0, fmt.Sprintf("model=%s", model))
	}

	ctxResult, err := s.Context(ctx, sessionID, resourceID)
	if err != nil {
		if onAgentEvent != nil {
			onAgentEvent(resourceID, "failed", 0, err.Error())
		}
		s.store.UpdateSessionResourceState(
			sessionID, resourceID, string(StateErrored), err.Error(), "", 0, "",
		)
		return &DispatchResult{
			ResourceID: resourceID, Status: "errored", Error: err.Error(),
			DurationMS: time.Since(startTime).Milliseconds(),
		}
	}

	s.store.SetSessionResourceDispatched(sessionID, resourceID)

	planResult, _ := s.Plan(ctx)
	loopOpts := s.buildLoopOpts(ctxResult, sessionID, applyID, resourceID, model, planResult)
	if onAgentEvent != nil {
		loopOpts.OnEvent = func(eventType string, attempt int, content string) {
			onAgentEvent(resourceID, eventType, attempt, content)
		}
	}

	loopResult, err := runConstraintLoop(ctx, s.engine, loopOpts)
	if err != nil {
		if onAgentEvent != nil {
			onAgentEvent(resourceID, "failed", 0, err.Error())
		}
		s.store.UpdateSessionResourceState(
			sessionID, resourceID, string(StateErrored), err.Error(), "", 1, "",
		)
		return &DispatchResult{
			ResourceID: resourceID, Status: "errored", Error: err.Error(),
			Attempts: 1, DurationMS: time.Since(startTime).Milliseconds(),
		}
	}

	s.recordAttempts(applyID, resourceID, model, loopResult.AttemptRecords)

	if loopResult.Outcome == "rejected" {
		if onAgentEvent != nil {
			onAgentEvent(resourceID, "failed", 0, fmt.Sprintf("rejected after %d attempts: %s", loopResult.Attempts, loopResult.RejectionReason))
		}
		s.store.UpdateSessionResourceState(
			sessionID, resourceID, string(StateRejected),
			loopResult.RejectionReason, "", loopResult.Attempts, "",
		)
		return &DispatchResult{
			ResourceID: resourceID, Status: "rejected",
			Error: loopResult.RejectionReason, Attempts: loopResult.Attempts,
			DurationMS: time.Since(startTime).Milliseconds(),
		}
	}

	files := blocksToCommitFiles(loopResult.Files)

	commitResult, err := s.Commit(ctx, sessionID, resourceID, files, "")
	if err != nil {
		s.store.UpdateSessionResourceState(
			sessionID, resourceID, string(StateErrored), err.Error(),
			"", loopResult.Attempts, "",
		)
		return &DispatchResult{
			ResourceID: resourceID, Status: "errored", Error: err.Error(),
			Attempts: loopResult.Attempts, DurationMS: time.Since(startTime).Milliseconds(),
		}
	}

	if !commitResult.Committed {
		errMsg := validationErrorMessage(commitResult.Validations)
		return &DispatchResult{
			ResourceID: resourceID, Status: "rejected", Error: errMsg,
			Files: files, Validations: commitResult.Validations,
			Attempts: loopResult.Attempts, DurationMS: time.Since(startTime).Milliseconds(),
		}
	}

	if onAgentEvent != nil {
		onAgentEvent(resourceID, "completed", 0, fmt.Sprintf("%d files, %d attempts, %dms", len(files), loopResult.Attempts, time.Since(startTime).Milliseconds()))
	}

	return &DispatchResult{
		ResourceID: resourceID, Status: "committed", Files: files,
		Validations: commitResult.Validations, Attempts: loopResult.Attempts,
		DurationMS: time.Since(startTime).Milliseconds(),
	}
}

func (s *Spec) buildLoopOpts(
	ctxResult *ContextResult, sessionID, applyID, resourceID, model string,
	planResult *PlanResult,
) LoopOpts {
	opts := LoopOpts{
		SystemPrompt:     ctxResult.SystemPrompt,
		Prompt:           ctxResult.Prompt,
		Model:            model,
		MaxRetries:       s.cfg.MaxRetries,
		ReviewLevel:      "light",
		Cwd:              s.cfg.SpecDir,
		TypeCheckCommand: s.cfg.TypeCheckCommand,
		TestCommand:      s.cfg.TestCommand,
		SessionID:        sessionID,
		ApplyID:          applyID,
		ResourceID:       resourceID,
		Store:            s.store,
	}
	if planResult != nil {
		if r, ok := planResult.Registry.Resources[resourceID]; ok {
			opts.Validations = r.Validations
		}
		opts.Invariants = planResult.Registry.Project.Invariants
	}
	return opts
}

func blocksToCommitFiles(blocks []CodeBlock) []CommitFile {
	files := make([]CommitFile, len(blocks))
	for i, b := range blocks {
		files[i] = CommitFile{Path: b.Path, Content: b.Content}
	}
	return files
}

func validationErrorMessage(validations []ValidationResult) string {
	var msgs []string
	for _, v := range validations {
		if !v.Passed {
			msgs = append(msgs, v.Message)
		}
	}
	if len(msgs) == 0 {
		return "validation failed"
	}
	return fmt.Sprintf("validation failed: %s", msgs)
}

func (s *Spec) RunWave(ctx context.Context, opts RunWaveOpts) (*RunWaveResult, error) {
	nextResult, err := s.Next(ctx, opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("next: %w", err)
	}

	if nextResult.Done {
		return &RunWaveResult{WaveIndex: nextResult.WaveIndex, Done: true}, nil
	}

	sess, err := s.store.GetSession(opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	results := s.dispatchWaveResources(ctx, opts, nextResult, sess.ApplyID)

	result := classifyResults(nextResult.WaveIndex, results)

	result.Verification = s.VerifyWave(ctx, opts.SessionID, nextResult.WaveIndex)
	if !result.Verification.Passed {
		s.applyVerificationErrors(opts.SessionID, result.Verification)
	}

	s.AdvanceWave(ctx, opts.SessionID)

	return result, nil
}

func (s *Spec) dispatchWaveResources(
	ctx context.Context,
	opts RunWaveOpts,
	nextResult *NextResult,
	applyID string,
) []DispatchResult {
	var mu sync.Mutex
	var results []DispatchResult
	var completed int
	var wg sync.WaitGroup

	total := countNonTerminal(nextResult.Resources)

	for _, rs := range nextResult.Resources {
		if rs.State.IsTerminal() {
			continue
		}

		wg.Add(1)
		go func(resourceID string) {
			defer wg.Done()

			model := opts.Model
			if override, ok := opts.ModelOverrides[resourceID]; ok {
				model = override
			}
			if model == "" {
				model = s.cfg.GenerateModel
			}

			dr := s.dispatchResource(ctx, opts.SessionID, applyID, resourceID, model, opts.OnAgentEvent)

			mu.Lock()
			results = append(results, *dr)
			completed++
			done := completed
			mu.Unlock()

			if opts.OnProgress != nil {
				opts.OnProgress(ProgressUpdate{
					ResourceID: dr.ResourceID, State: dr.Status,
					Attempts: dr.Attempts, Total: total,
					Completed: done, Error: dr.Error,
				})
			}
		}(rs.ResourceID)
	}

	wg.Wait()
	return results
}

func countNonTerminal(resources []ResourceStatus) int {
	n := 0
	for _, rs := range resources {
		if !rs.State.IsTerminal() {
			n++
		}
	}
	return n
}

func classifyResults(waveIndex int, results []DispatchResult) *RunWaveResult {
	result := &RunWaveResult{WaveIndex: waveIndex}
	for _, dr := range results {
		switch dr.Status {
		case "committed":
			result.Committed = append(result.Committed, dr)
		case "rejected":
			result.Rejected = append(result.Rejected, dr)
		default:
			result.Errored = append(result.Errored, dr)
		}
	}
	return result
}

func (s *Spec) applyVerificationErrors(sessionID string, verification *WaveVerifyResult) {
	for _, we := range verification.Errors {
		if we.ResourceID == "" {
			continue
		}
		sr, _ := s.store.GetSessionResource(sessionID, we.ResourceID)
		if sr != nil {
			s.store.UpdateSessionResourceState(
				sessionID, we.ResourceID, string(StateErrored),
				we.Message, sr.LastOutput, sr.Attempts, sr.JobID,
			)
		}
	}
}
