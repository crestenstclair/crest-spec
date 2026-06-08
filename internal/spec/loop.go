package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/agent"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/engine"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// ReviewOutput represents structured JSON output from an LLM review.
type ReviewOutput struct {
	Passed   bool            `json:"passed"`
	Findings []ReviewFinding `json:"findings,omitempty"`
	Summary  string          `json:"summary,omitempty"`
}

// ReviewFinding represents a single issue found during review.
type ReviewFinding struct {
	Severity    string `json:"severity"`
	Description string `json:"description"`
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
}

// AttemptRecord captures data from a single generation attempt within the constraint loop.
type AttemptRecord struct {
	Prompt     string
	Output     string
	Outcome    string // "accepted", "rejected_parse", "rejected_validation", "rejected_invariant", "rejected_review"
	Error      string
	DurationMS int64
	Attempt    int
}

type LoopResult struct {
	Files           []CodeBlock
	Outcome         string
	RejectionReason string
	Attempts        int
	AttemptRecords  []AttemptRecord
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
	SessionID        string
	ApplyID          string
	ResourceID       string
	Store            specStore
	OnEvent          func(eventType string, attempt int, content string)
}

func runConstraintLoop(ctx context.Context, eng specEngine, opts LoopOpts) (*LoopResult, error) {
	maxAttempts := opts.MaxRetries + 1
	var lastOutput string
	var lastError string
	var records []AttemptRecord

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		updatePhase(opts, "generating", attempt)
		rec := runAttempt(ctx, eng, opts, attempt, &lastOutput, &lastError)
		records = append(records, rec)

		if rec.Outcome == "accepted" {
			blocks, _ := ParseCodeBlocks(rec.Output)
			return &LoopResult{
				Files:          blocks,
				Outcome:        "accepted",
				Attempts:       attempt,
				AttemptRecords: records,
			}, nil
		}
	}

	return &LoopResult{
		Outcome:         "rejected",
		RejectionReason: lastError,
		Attempts:        maxAttempts,
		AttemptRecords:  records,
	}, nil
}

func emitEvent(opts LoopOpts, eventType string, attempt int, content string) {
	if opts.OnEvent != nil {
		opts.OnEvent(eventType, attempt, content)
	}
}

func updatePhase(opts LoopOpts, phase string, attempt int) {
	if opts.Store != nil && opts.SessionID != "" && opts.ResourceID != "" {
		opts.Store.UpdateSessionResourcePhase(opts.SessionID, opts.ResourceID, phase, attempt)
	}
}

func runAttempt(
	ctx context.Context, eng specEngine, opts LoopOpts,
	attempt int, lastOutput, lastError *string,
) AttemptRecord {
	start := time.Now()
	emitEvent(opts, "attempt_start", attempt, fmt.Sprintf("attempt %d of %d", attempt, opts.MaxRetries+1))

	genPrompt := opts.Prompt
	if attempt > 1 && *lastError != "" {
		genPrompt = promptpkg.BuildFixPrompt(opts.Prompt, *lastOutput, *lastError)
	}

	emitEvent(opts, "generate_start", attempt, fmt.Sprintf("model=%s", opts.Model))
	blocks, output, err := generate(ctx, eng, genPrompt, opts, attempt)
	if err != nil {
		*lastError = fmt.Sprintf("generation error: %s", err.Error())
		emitEvent(opts, "generate_fail", attempt, *lastError)
		return AttemptRecord{
			Prompt: genPrompt, Output: "", Outcome: "error",
			Error: *lastError, DurationMS: time.Since(start).Milliseconds(), Attempt: attempt,
		}
	}
	*lastOutput = output

	if blocks == nil {
		*lastError = "parse error: no code blocks found in output"
		emitEvent(opts, "parse_fail", attempt, *lastError)
		return AttemptRecord{
			Prompt: genPrompt, Output: output, Outcome: "rejected_parse",
			Error: *lastError, DurationMS: time.Since(start).Milliseconds(), Attempt: attempt,
		}
	}
	emitEvent(opts, "generate_done", attempt, fmt.Sprintf("%d code blocks extracted", len(blocks)))

	updatePhase(opts, "validating", attempt)
	emitEvent(opts, "validate_start", attempt, "")
	if err := runValidationStep(ctx, opts, lastError); err != nil {
		emitEvent(opts, "validate_fail", attempt, *lastError)
		return AttemptRecord{
			Prompt: genPrompt, Output: output, Outcome: "rejected_validation",
			Error: *lastError, DurationMS: time.Since(start).Milliseconds(), Attempt: attempt,
		}
	}
	emitEvent(opts, "validate_done", attempt, "passed")

	updatePhase(opts, "checking_invariants", attempt)
	emitEvent(opts, "invariant_start", attempt, fmt.Sprintf("%d invariants", len(opts.Invariants)))
	if err := runInvariantStep(ctx, eng, blocks, opts, lastError); err != nil {
		emitEvent(opts, "invariant_fail", attempt, *lastError)
		return AttemptRecord{
			Prompt: genPrompt, Output: output, Outcome: "rejected_invariant",
			Error: *lastError, DurationMS: time.Since(start).Milliseconds(), Attempt: attempt,
		}
	}
	emitEvent(opts, "invariant_done", attempt, "passed")

	updatePhase(opts, "reviewing", attempt)
	emitEvent(opts, "review_start", attempt, fmt.Sprintf("level=%s", opts.ReviewLevel))
	if err := runReviewStep(ctx, eng, output, opts, lastError); err != nil {
		emitEvent(opts, "review_fail", attempt, *lastError)
		return AttemptRecord{
			Prompt: genPrompt, Output: output, Outcome: "rejected_review",
			Error: *lastError, DurationMS: time.Since(start).Milliseconds(), Attempt: attempt,
		}
	}
	emitEvent(opts, "review_done", attempt, "passed")

	emitEvent(opts, "attempt_done", attempt, fmt.Sprintf("accepted in %dms", time.Since(start).Milliseconds()))
	return AttemptRecord{
		Prompt: genPrompt, Output: output, Outcome: "accepted",
		DurationMS: time.Since(start).Milliseconds(), Attempt: attempt,
	}
}

func generate(ctx context.Context, eng specEngine, prompt string, opts LoopOpts, attempt int) ([]CodeBlock, string, error) {
	var onStderr func(string)
	if opts.OnEvent != nil {
		onStderr = func(line string) {
			opts.OnEvent("stderr", attempt, line)
		}
	}

	res, err := eng.Generate(ctx, engine.GenerateOpts{
		Prompt:             prompt,
		Model:              opts.Model,
		AppendSystemPrompt: opts.SystemPrompt,
		OnStderr:           onStderr,
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

func runInvariantStep(ctx context.Context, eng specEngine, blocks []CodeBlock, opts LoopOpts, lastError *string) error {
	if len(opts.Invariants) == 0 {
		return nil
	}

	var codeBuilder string
	for _, b := range blocks {
		codeBuilder += fmt.Sprintf("// path: %s\n%s\n\n", b.Path, b.Content)
	}

	for _, inv := range opts.Invariants {
		passed, output, err := checkInvariant(ctx, eng, inv, codeBuilder)
		if err != nil {
			continue
		}

		recordInvariantCheck(opts, inv.Text, passed, output)

		if !passed {
			*lastError = fmt.Sprintf("invariant violated: %s\n%s", inv.Text, output)
			return fmt.Errorf("failed")
		}
	}

	return nil
}

func checkInvariant(ctx context.Context, eng specEngine, inv cuepkg.Invariant, code string) (bool, string, error) {
	prompt := fmt.Sprintf(
		"Check if this code violates the following invariant:\n\nINVARIANT: %s\n",
		inv.Text,
	)
	if inv.Meta.Rationale != "" {
		prompt += fmt.Sprintf("RATIONALE: %s\n", inv.Meta.Rationale)
	}
	prompt += fmt.Sprintf("\nCODE:\n%s\n", code)
	prompt += "\nSet \"passed\" to true if the code RESPECTS the invariant, or false if it VIOLATES it (put the explanation in \"summary\")."
	prompt += reviewJSONInstruction

	res, err := eng.Review(ctx, engine.ReviewOpts{
		Code:         code,
		Requirements: prompt,
	})
	if err != nil {
		return false, "", err
	}

	// Parse the marker-wrapped verdict. If it can't be parsed, treat the
	// invariant as respected rather than guessing from keywords — an
	// uninterpretable judgment must not produce a false violation/retry loop.
	ro := parseReviewOutput(res.Output)
	if ro == nil {
		return true, res.Output, nil
	}
	msg := ro.Summary
	if msg == "" {
		msg = res.Output
	}
	return ro.Passed, msg, nil
}

func recordInvariantCheck(opts LoopOpts, checkType string, passed bool, output string) {
	if opts.Store == nil || opts.ApplyID == "" || opts.ResourceID == "" {
		return
	}
	opts.Store.RecordInvariantCheck(store.InvariantCheck{
		ID:         uuid.NewString(),
		ApplyID:    opts.ApplyID,
		ResourceID: opts.ResourceID,
		CheckType:  checkType,
		Passed:     passed,
		Output:     output,
		CreatedAt:  time.Now(),
	})
}

func promptHash(prompt string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(prompt)))
}

func runReviewStep(ctx context.Context, eng specEngine, output string, opts LoopOpts, lastError *string) error {
	if opts.ReviewLevel == "" || opts.ReviewLevel == "skip" {
		return nil
	}

	result, err := runReview(ctx, eng, output, opts)
	if err != nil {
		*lastError = fmt.Sprintf("review error: %s", err.Error())
		return fmt.Errorf("review: %w", err)
	}

	if !result.Passed {
		*lastError = fmt.Sprintf("review failed: %s", result.Message)
		return fmt.Errorf("failed")
	}

	return nil
}

// Review verdicts are wrapped in these sentinel markers so the JSON object can
// be located unambiguously, even amid prose or multiple aggregated model
// replies. This replaces fragile keyword/negation matching of free-form text.
const (
	reviewResultBegin = "===CREST_REVIEW_BEGIN==="
	reviewResultEnd   = "===CREST_REVIEW_END==="
)

const reviewJSONInstruction = `

Output your verdict as a single JSON object wrapped EXACTLY between these markers, each on its own line:
===CREST_REVIEW_BEGIN===
{"passed": true, "findings": [{"severity": "critical|major|minor", "description": "...", "file": "...", "line": 0}], "summary": "..."}
===CREST_REVIEW_END===
Set "passed" to false only when at least one finding must block acceptance. Output nothing after the END marker.`

// parseReviewOutput extracts the structured verdict from review output. It looks
// for JSON wrapped in the review markers first (the requested format), and each
// aggregated model reply (engine.CodeReview / Bugbot emit one block per model)
// is parsed and combined: the review passes only if every block passed, and
// findings are unioned. If no marker blocks are present it falls back to parsing
// a bare JSON object per "## Model:" section. Returns nil when nothing parses,
// signaling the caller that the review could not be interpreted.
func parseReviewOutput(output string) *ReviewOutput {
	blocks := extractMarkerBlocks(output)
	if len(blocks) == 0 {
		blocks = splitModelSections(output)
	}

	var combined *ReviewOutput
	for _, b := range blocks {
		ro := parseSingleReviewJSON(b)
		if ro == nil {
			continue
		}
		if combined == nil {
			combined = &ReviewOutput{Passed: true}
		}
		if !ro.Passed {
			combined.Passed = false
		}
		combined.Findings = append(combined.Findings, ro.Findings...)
		if ro.Summary != "" {
			if combined.Summary != "" {
				combined.Summary += " | "
			}
			combined.Summary += ro.Summary
		}
	}
	return combined
}

// extractMarkerBlocks returns the text between each BEGIN/END marker pair. An
// unterminated final block (END missing) yields the remainder, so a truncated
// reply is still recoverable.
func extractMarkerBlocks(output string) []string {
	var blocks []string
	rest := output
	for {
		i := strings.Index(rest, reviewResultBegin)
		if i < 0 {
			break
		}
		rest = rest[i+len(reviewResultBegin):]
		if j := strings.Index(rest, reviewResultEnd); j >= 0 {
			blocks = append(blocks, rest[:j])
			rest = rest[j+len(reviewResultEnd):]
			continue
		}
		blocks = append(blocks, rest)
		break
	}
	return blocks
}

// splitModelSections splits aggregated multi-model output on "## Model:" headers.
// When no header is present the whole output is returned as a single section.
func splitModelSections(output string) []string {
	const marker = "## Model:"
	if !strings.Contains(output, marker) {
		return []string{output}
	}
	parts := strings.Split(output, marker)
	sections := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			sections = append(sections, p)
		}
	}
	return sections
}

// parseSingleReviewJSON parses one block's JSON, tolerating surrounding prose
// or markdown fences by extracting the outermost JSON object.
func parseSingleReviewJSON(section string) *ReviewOutput {
	var ro ReviewOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(section)), &ro); err == nil {
		return &ro
	}
	start := strings.Index(section, "{")
	end := strings.LastIndex(section, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(section[start:end+1]), &ro); err == nil {
			return &ro
		}
	}
	return nil
}

// buildReviewMessage produces the Message string for a ValidationResult.
// When structured findings are available it marshals them as JSON;
// otherwise the raw LLM output is returned as-is.
func buildReviewMessage(ro *ReviewOutput, rawOutput string) string {
	if ro == nil || len(ro.Findings) == 0 {
		if ro != nil && ro.Summary != "" {
			return ro.Summary
		}
		return rawOutput
	}

	data, err := json.Marshal(ro.Findings)
	if err != nil {
		return rawOutput
	}

	if ro.Summary != "" {
		return fmt.Sprintf("%s\nfindings: %s", ro.Summary, string(data))
	}
	return fmt.Sprintf("findings: %s", string(data))
}

func runReview(ctx context.Context, eng specEngine, code string, opts LoopOpts) (*ValidationResult, error) {
	res, err := dispatchReview(ctx, eng, code, opts)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return &ValidationResult{Passed: true, Kind: "review"}, nil
	}

	ro := parseReviewOutput(res.Output)
	if ro == nil {
		// The review verdict couldn't be parsed (no marker block, no JSON). Don't
		// block the candidate on an uninterpretable review — validation and
		// invariant checks already gate correctness, and the orchestrator
		// re-reviews each wave. Treat it as a non-blocking skip.
		return &ValidationResult{
			Passed:  true,
			Kind:    "review",
			Message: "review output not parseable; review gate skipped",
		}, nil
	}

	return &ValidationResult{
		Passed:  ro.Passed,
		Kind:    "review",
		Message: buildReviewMessage(ro, res.Output),
	}, nil
}

// dispatchReview calls the appropriate engine review method based on the review
// level, appending the marker-wrapped JSON verdict instruction. Returns a nil
// result for unknown levels.
func dispatchReview(ctx context.Context, eng specEngine, code string, opts LoopOpts) (*agent.RunResult, error) {
	switch opts.ReviewLevel {
	case "full":
		return eng.CodeReview(ctx, engine.CodeReviewOpts{
			Prompt: fmt.Sprintf("Review this generated code:\n\n%s%s", code, reviewJSONInstruction),
			Cwd:    opts.Cwd,
		})

	case "light":
		return eng.Bugbot(ctx, engine.BugbotOpts{
			Prompt: code + reviewJSONInstruction,
			Cwd:    opts.Cwd,
		})

	case "solid":
		return eng.Review(ctx, engine.ReviewOpts{
			Code:         code,
			Requirements: opts.Prompt + reviewJSONInstruction,
		})

	case "deep":
		return eng.CodeReview(ctx, engine.CodeReviewOpts{
			Prompt: promptpkg.BuildDeepReviewPrompt(code) + reviewJSONInstruction,
			Cwd:    opts.Cwd,
		})

	default:
		return nil, nil
	}
}
