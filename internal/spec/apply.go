package spec

import (
	"context"
	"fmt"
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

	// Unattended apply auto-confirms all pending destroys.
	var destroyed []DestroyedResource
	if len(beginResult.PendingDestroys) > 0 {
		var ids []string
		for _, d := range beginResult.PendingDestroys {
			ids = append(ids, d.ResourceID)
		}
		destroyed, _ = s.ConfirmDestroys(ctx, beginResult.SessionID, ids)
	}

	result := &ApplyResult{
		SessionID:          beginResult.SessionID,
		ApplyID:            beginResult.ApplyID,
		DestroyedResources: destroyed,
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
	dr := s.dispatchResource(ctx, sessionID, applyID, resourceID, s.cfg.GenerateModel, nil)

	var filePaths []string
	for _, f := range dr.Files {
		filePaths = append(filePaths, f.Path)
	}

	return ResourceApplyResult{
		ResourceID: resourceID,
		Outcome:    dr.Status,
		Attempts:   dr.Attempts,
		Error:      dr.Error,
		Files:      filePaths,
	}
}
