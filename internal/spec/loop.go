package spec

import (
	"context"
	"fmt"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
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
	SystemPrompt     string
	Prompt           string
	Model            string
	MaxRetries       int
	ReviewLevel      string
	Cwd              string
	Validations      []cuepkg.Validation
	Invariants       []cuepkg.Invariant
	TypeCheckCommand string
	TestCommand      string
}

func runConstraintLoop(ctx context.Context, eng specEngine, opts LoopOpts) (*LoopResult, error) {
	maxAttempts := opts.MaxRetries + 1
	var lastOutput string
	var lastError string

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		genPrompt := opts.Prompt
		if attempt > 1 && lastError != "" {
			genPrompt = promptpkg.BuildFixPrompt(opts.Prompt, lastOutput, lastError)
		}

		blocks, output, err := generate(ctx, eng, genPrompt, opts)
		if err != nil {
			return nil, fmt.Errorf("generate attempt %d: %w", attempt, err)
		}
		lastOutput = output

		if blocks == nil {
			lastError = "parse error: no code blocks found in output"
			continue
		}

		if err := runValidationStep(ctx, opts, &lastError); err != nil {
			continue
		}

		if err := runInvariantStep(ctx, eng, blocks, opts.Invariants, &lastError); err != nil {
			continue
		}

		if err := runReviewStep(ctx, eng, output, opts, &lastError); err != nil {
			continue
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

func generate(ctx context.Context, eng specEngine, prompt string, opts LoopOpts) ([]CodeBlock, string, error) {
	res, err := eng.Generate(ctx, engine.GenerateOpts{
		Prompt:             prompt,
		Model:              opts.Model,
		AppendSystemPrompt: opts.SystemPrompt,
	})
	if err != nil {
		return nil, "", err
	}

	blocks, parseErr := ParseCodeBlocks(res.Output)
	if parseErr != nil {
		return nil, res.Output, nil
	}

	return blocks, res.Output, nil
}

func runValidationStep(ctx context.Context, opts LoopOpts, lastError *string) error {
	validations := opts.Validations

	if len(validations) == 0 {
		validations = fallbackValidations(opts.TypeCheckCommand, opts.TestCommand)
	}

	if len(validations) == 0 {
		return nil
	}

	results, err := RunValidations(ctx, validations, opts.Cwd)
	if err != nil {
		*lastError = fmt.Sprintf("validation error: %s", err.Error())
		return err
	}

	for _, r := range results {
		if !r.Passed {
			*lastError = fmt.Sprintf("validation failed (%s): %s", r.Kind, r.Message)
			return fmt.Errorf("failed")
		}
	}

	return nil
}

func fallbackValidations(typeCheckCmd, testCmd string) []cuepkg.Validation {
	var validations []cuepkg.Validation
	if typeCheckCmd != "" {
		validations = append(validations, cuepkg.Validation{
			Kind:    "compiles",
			Command: []string{"sh", "-c", typeCheckCmd},
		})
	}
	if testCmd != "" {
		validations = append(validations, cuepkg.Validation{
			Kind:    "test",
			Command: []string{"sh", "-c", testCmd},
		})
	}
	return validations
}

func runInvariantStep(ctx context.Context, eng specEngine, blocks []CodeBlock, invariants []cuepkg.Invariant, lastError *string) error {
	if len(invariants) == 0 {
		return nil
	}

	var codeBuilder string
	for _, b := range blocks {
		codeBuilder += fmt.Sprintf("// path: %s\n%s\n\n", b.Path, b.Content)
	}

	for _, inv := range invariants {
		prompt := fmt.Sprintf(
			"Check if this code violates the following invariant:\n\nINVARIANT: %s\n",
			inv.Text,
		)
		if inv.Meta.Rationale != "" {
			prompt += fmt.Sprintf("RATIONALE: %s\n", inv.Meta.Rationale)
		}
		prompt += fmt.Sprintf("\nCODE:\n%s\n\nRespond with PASS if the code respects the invariant, or FAIL with explanation.", codeBuilder)

		res, err := eng.Review(ctx, engine.ReviewOpts{
			Code:         codeBuilder,
			Requirements: prompt,
		})
		if err != nil {
			continue
		}

		if strings.Contains(strings.ToUpper(res.Output), "FAIL") {
			*lastError = fmt.Sprintf("invariant violated: %s\n%s", inv.Text, res.Output)
			return fmt.Errorf("failed")
		}
	}

	return nil
}

func runReviewStep(ctx context.Context, eng specEngine, output string, opts LoopOpts, lastError *string) error {
	if opts.ReviewLevel == "" || opts.ReviewLevel == "skip" {
		return nil
	}

	result, err := runReview(ctx, eng, output, opts)
	if err != nil {
		return fmt.Errorf("review: %w", err)
	}

	if !result.Passed {
		*lastError = fmt.Sprintf("review failed: %s", result.Message)
		return fmt.Errorf("failed")
	}

	return nil
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
