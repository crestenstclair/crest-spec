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
		s.store.CreateGeneration(store.Generation{
			ID:              uuid.NewString(),
			ApplyID:         applyID,
			ResourceID:      resourceID,
			PromptText:      rec.Prompt,
			PromptHash:      promptHash(rec.Prompt),
			OutputText:      rec.Output,
			Model:           model,
			Outcome:         rec.Outcome,
			RejectionReason: rec.Error,
			RetryCount:      rec.Attempt,
			DurationMS:      rec.DurationMS,
		})
	}
}

type DispatchOpts struct {
	SessionID  string
	ResourceID string
	Model      string
}

type DispatchResult struct {
	ResourceID  string             `json:"resource_id"`
	Status      string             `json:"status"`
	Files       []CommitFile       `json:"files,omitempty"`
	Validations []ValidationResult `json:"validations,omitempty"`
	Error       string             `json:"error,omitempty"`
	Attempts    int                `json:"attempts"`
	DurationMS  int64              `json:"duration_ms"`
}

type RunWaveOpts struct {
	SessionID      string
	Model          string
	ModelOverrides map[string]string
}

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

	return s.dispatchResource(ctx, opts.SessionID, sess.ApplyID, opts.ResourceID, model), nil
}

func (s *Spec) dispatchResource(ctx context.Context, sessionID, applyID, resourceID, model string) *DispatchResult {
	startTime := time.Now()

	ctxResult, err := s.Context(ctx, sessionID, resourceID)
	if err != nil {
		s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateErrored), err.Error(), "", 0, "")
		return &DispatchResult{
			ResourceID: resourceID, Status: "errored", Error: err.Error(),
			DurationMS: time.Since(startTime).Milliseconds(),
		}
	}

	planResult, _ := s.Plan(ctx)
	loopOpts := s.buildLoopOpts(ctxResult, applyID, resourceID, model, planResult)

	loopResult, err := runConstraintLoop(ctx, s.engine, loopOpts)
	if err != nil {
		s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateErrored), err.Error(), "", 1, "")
		return &DispatchResult{
			ResourceID: resourceID, Status: "errored", Error: err.Error(),
			Attempts: 1, DurationMS: time.Since(startTime).Milliseconds(),
		}
	}

	s.recordAttempts(applyID, resourceID, model, loopResult.AttemptRecords)

	if loopResult.Outcome == "rejected" {
		s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateRejected), loopResult.RejectionReason, "", loopResult.Attempts, "")
		return &DispatchResult{
			ResourceID: resourceID, Status: "rejected", Error: loopResult.RejectionReason,
			Attempts: loopResult.Attempts, DurationMS: time.Since(startTime).Milliseconds(),
		}
	}

	files := blocksToCommitFiles(loopResult.Files)

	commitResult, err := s.Commit(ctx, sessionID, resourceID, files, "")
	if err != nil {
		s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateErrored), err.Error(), "", loopResult.Attempts, "")
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

	return &DispatchResult{
		ResourceID: resourceID, Status: "committed", Files: files,
		Validations: commitResult.Validations, Attempts: loopResult.Attempts,
		DurationMS: time.Since(startTime).Milliseconds(),
	}
}

func (s *Spec) buildLoopOpts(ctxResult *ContextResult, applyID, resourceID, model string, planResult *PlanResult) LoopOpts {
	opts := LoopOpts{
		SystemPrompt:     ctxResult.SystemPrompt,
		Prompt:           ctxResult.Prompt,
		Model:            model,
		MaxRetries:       s.cfg.MaxRetries,
		ReviewLevel:      "light",
		Cwd:              s.cfg.SpecDir,
		TypeCheckCommand: s.cfg.TypeCheckCommand,
		TestCommand:      s.cfg.TestCommand,
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
	var wg sync.WaitGroup

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

			dr := s.dispatchResource(ctx, opts.SessionID, applyID, resourceID, model)

			mu.Lock()
			results = append(results, *dr)
			mu.Unlock()
		}(rs.ResourceID)
	}

	wg.Wait()
	return results
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
