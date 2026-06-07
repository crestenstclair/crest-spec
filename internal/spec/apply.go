package spec

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/store"
)

type ApplyResult struct {
	SessionID          string
	ApplyID            string
	Committed          int
	Skipped            int
	Errored            int
	DestroyedResources []DestroyedResource
	ResourceResults    []ResourceApplyResult
}

type ResourceApplyResult struct {
	ResourceID string
	Outcome    string
	Attempts   int
	Error      string
	Files      []string
}

func (s *Spec) Apply(ctx context.Context, opts BeginOpts) (*ApplyResult, error) {
	beginResult, err := s.Begin(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}

	if beginResult.SessionID == "" {
		return &ApplyResult{}, nil
	}

	result := &ApplyResult{
		SessionID:          beginResult.SessionID,
		ApplyID:            beginResult.ApplyID,
		DestroyedResources: beginResult.DestroyedResources,
	}

	for {
		nextResult, err := s.Next(ctx, beginResult.SessionID)
		if err != nil {
			return nil, fmt.Errorf("next: %w", err)
		}
		if nextResult.Done {
			break
		}

		for _, rs := range nextResult.Resources {
			if rs.State.IsTerminal() {
				continue
			}

			resourceResult := s.applyResource(ctx, beginResult.SessionID, beginResult.ApplyID, rs.ResourceID)
			result.ResourceResults = append(result.ResourceResults, resourceResult)

			switch resourceResult.Outcome {
			case "committed":
				result.Committed++
			case "skipped":
				result.Skipped++
			default:
				result.Errored++
			}
		}

		s.AdvanceWave(ctx, beginResult.SessionID)
	}

	s.Finish(ctx, beginResult.SessionID, false)

	return result, nil
}

func (s *Spec) applyResource(ctx context.Context, sessionID, applyID, resourceID string) ResourceApplyResult {
	ctxResult, err := s.Context(ctx, sessionID, resourceID)
	if err != nil {
		s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateErrored), err.Error(), "", 0, "")
		return ResourceApplyResult{ResourceID: resourceID, Outcome: "errored", Error: err.Error()}
	}

	loopResult, err := runConstraintLoop(ctx, s.engine, LoopOpts{
		SystemPrompt: ctxResult.SystemPrompt,
		Prompt:       ctxResult.Prompt,
		Model:        s.cfg.GenerateModel,
		MaxRetries:   s.cfg.MaxRetries,
		ReviewLevel:  "light",
		Cwd:          s.cfg.SpecDir,
	})
	if err != nil {
		s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateErrored), err.Error(), "", 1, "")
		return ResourceApplyResult{ResourceID: resourceID, Outcome: "errored", Attempts: 1, Error: err.Error()}
	}

	if loopResult.Outcome == "rejected" {
		s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateRejected), loopResult.RejectionReason, "", loopResult.Attempts, "")
		return ResourceApplyResult{ResourceID: resourceID, Outcome: "rejected", Attempts: loopResult.Attempts, Error: loopResult.RejectionReason}
	}

	files := make([]CommitFile, len(loopResult.Files))
	var filePaths []string
	for i, block := range loopResult.Files {
		files[i] = CommitFile{Path: block.Path, Content: block.Content}
		filePaths = append(filePaths, block.Path)
	}

	// Record generation
	genID := uuid.NewString()
	s.store.CreateGeneration(store.Generation{
		ID:         genID,
		ApplyID:    applyID,
		ResourceID: resourceID,
		Model:      s.cfg.GenerateModel,
		DurationMS: time.Since(time.Now()).Milliseconds(),
	})

	commitResult, err := s.Commit(ctx, sessionID, resourceID, files, "")
	if err != nil {
		s.store.UpdateSessionResourceState(sessionID, resourceID, string(StateErrored), err.Error(), "", loopResult.Attempts, "")
		return ResourceApplyResult{ResourceID: resourceID, Outcome: "errored", Attempts: loopResult.Attempts, Error: err.Error()}
	}

	if !commitResult.Committed {
		var msgs []string
		for _, v := range commitResult.Validations {
			if !v.Passed {
				msgs = append(msgs, v.Message)
			}
		}
		errMsg := fmt.Sprintf("validation failed: %s", msgs)
		return ResourceApplyResult{ResourceID: resourceID, Outcome: "rejected", Attempts: loopResult.Attempts, Error: errMsg}
	}

	return ResourceApplyResult{ResourceID: resourceID, Outcome: "committed", Attempts: loopResult.Attempts, Files: filePaths}
}
