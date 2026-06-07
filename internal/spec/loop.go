package spec

import (
	"context"
	"fmt"
	"strings"

	"github.com/crestenstclair/crest-spec/internal/engine"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
)

type LoopResult struct {
	Files           []CodeBlock
	Outcome         string
	RejectionReason string
	Attempts        int
}

type LoopOpts struct {
	SystemPrompt string
	Prompt       string
	Model        string
	MaxRetries   int
	ReviewLevel  string
	Cwd          string
}

func runConstraintLoop(ctx context.Context, eng specEngine, opts LoopOpts) (*LoopResult, error) {
	maxAttempts := opts.MaxRetries + 1
	currentPrompt := opts.Prompt
	var lastOutput string
	var lastError string

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		genPrompt := currentPrompt
		if attempt > 1 && lastError != "" {
			genPrompt = promptpkg.BuildFixPrompt(opts.Prompt, lastOutput, lastError)
		}

		res, err := eng.Generate(ctx, engine.GenerateOpts{
			Prompt:             genPrompt,
			Model:              opts.Model,
			AppendSystemPrompt: opts.SystemPrompt,
		})
		if err != nil {
			return nil, fmt.Errorf("generate attempt %d: %w", attempt, err)
		}

		lastOutput = res.Output

		blocks, parseErr := ParseCodeBlocks(res.Output)
		if parseErr != nil {
			lastError = fmt.Sprintf("parse error: %s", parseErr.Error())
			continue
		}

		if opts.ReviewLevel != "" && opts.ReviewLevel != "skip" {
			reviewResult, reviewErr := runReview(ctx, eng, res.Output, opts)
			if reviewErr != nil {
				return nil, fmt.Errorf("review attempt %d: %w", attempt, reviewErr)
			}
			if !reviewResult.Passed {
				lastError = fmt.Sprintf("review failed: %s", reviewResult.Message)
				continue
			}
		}

		return &LoopResult{
			Files:    blocks,
			Outcome:  "accepted",
			Attempts: attempt,
		}, nil
	}

	return &LoopResult{
		Outcome:         "rejected",
		RejectionReason: lastError,
		Attempts:        maxAttempts,
	}, nil
}

// SpecEngine is the exported engine interface for CLI callers.
type SpecEngine = specEngine

// RunConstraintLoopPublic is the exported entry point for CLI callers.
func RunConstraintLoopPublic(ctx context.Context, eng SpecEngine, opts LoopOpts) (*LoopResult, error) {
	return runConstraintLoop(ctx, eng, opts)
}

func runReview(ctx context.Context, eng specEngine, code string, opts LoopOpts) (*ValidationResult, error) {
	switch opts.ReviewLevel {
	case "full":
		res, err := eng.CodeReview(ctx, engine.CodeReviewOpts{
			Prompt: fmt.Sprintf("Review this generated code:\n\n%s", code),
			Cwd:    opts.Cwd,
		})
		if err != nil {
			return nil, err
		}
		passed := !strings.Contains(strings.ToUpper(res.Output), "FAIL")
		return &ValidationResult{Passed: passed, Kind: "review", Message: res.Output}, nil

	case "light":
		res, err := eng.Bugbot(ctx, engine.BugbotOpts{
			Prompt: code,
			Cwd:    opts.Cwd,
		})
		if err != nil {
			return nil, err
		}
		passed := !strings.Contains(strings.ToLower(res.Output), "critical")
		return &ValidationResult{Passed: passed, Kind: "review", Message: res.Output}, nil

	case "solid":
		res, err := eng.Review(ctx, engine.ReviewOpts{
			Code:         code,
			Requirements: opts.Prompt,
		})
		if err != nil {
			return nil, err
		}
		passed := strings.Contains(strings.ToUpper(res.Output), "PASS")
		return &ValidationResult{Passed: passed, Kind: "review", Message: res.Output}, nil

	default:
		return &ValidationResult{Passed: true, Kind: "review"}, nil
	}
}
