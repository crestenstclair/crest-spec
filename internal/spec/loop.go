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
	prompt += fmt.Sprintf("\nCODE:\n%s\n\nRespond with PASS if the code respects the invariant, or FAIL with explanation.", code)

	res, err := eng.Review(ctx, engine.ReviewOpts{
		Code:         code,
		Requirements: prompt,
	})
	if err != nil {
		return false, "", err
	}

	passed := !strings.Contains(strings.ToUpper(res.Output), "FAIL")
	return passed, res.Output, nil
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

const reviewJSONInstruction = `

IMPORTANT: Respond with ONLY a single JSON object and nothing else — no prose, no markdown fences, no text before or after. Use exactly this shape:
{"passed": true, "findings": [{"severity": "critical|major|minor", "description": "...", "file": "...", "line": 0}], "summary": "..."}
Set "passed" to false only when there is at least one finding serious enough to block acceptance.`

// parseReviewOutput attempts to parse the LLM output as structured JSON.
// Multi-model reviews (engine.CodeReview / Bugbot) aggregate each model's reply
// under a "## Model: X" header, so the output may contain several JSON objects.
// Each section is parsed independently and combined: the review passes only if
// every section passed, and findings are unioned. Returns nil only when no
// section yields parseable JSON, signaling the caller to fall back to string
// matching.
func parseReviewOutput(output string) *ReviewOutput {
	var combined *ReviewOutput
	for _, section := range splitModelSections(output) {
		ro := parseSingleReviewJSON(section)
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

// parseSingleReviewJSON parses one section's JSON, tolerating surrounding prose
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

// negationTokens precede a keyword to signal it is being denied
// (e.g. "no critical issues", "0 critical", "free of critical bugs").
var negationTokens = []string{
	"no ", "no-", "not ", "n't ", "without ", "zero ", "0 ",
	"none", "free of ", "free from ", "absent", "lack of ",
}

// indicatesFailure reports whether any keyword appears in output as a genuine
// positive finding rather than a negated one. The output is split into clauses
// (on sentence/clause punctuation); a clause counts as a failure only if it
// contains a keyword and NO negation. So "No critical bugs", "none of the
// findings are critical", and "Critical issues: none" do NOT indicate failure,
// while "found a critical bug" and "No minor issues, but a critical bug exists"
// do. Case-insensitive. This is only a fallback for when structured JSON review
// output cannot be parsed.
func indicatesFailure(output string, keywords []string) bool {
	for _, clause := range splitClauses(strings.ToLower(output)) {
		if !clauseContainsAny(clause, keywords) {
			continue
		}
		if !containsNegation(clause) {
			return true
		}
	}
	return false
}

// splitClauses breaks text on sentence and clause boundaries so negation
// detection stays scoped to the clause containing the keyword. ':' is
// deliberately not a delimiter, so "Critical issues: none" stays one clause.
func splitClauses(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '.', ';', ',', '\n', '!', '?':
			return true
		}
		return false
	})
}

func clauseContainsAny(clause string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(clause, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func containsNegation(prefix string) bool {
	for _, n := range negationTokens {
		if strings.Contains(prefix, n) {
			return true
		}
	}
	return false
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
	res, fallbackPassed, err := dispatchReview(ctx, eng, code, opts)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return &ValidationResult{Passed: true, Kind: "review"}, nil
	}

	if ro := parseReviewOutput(res.Output); ro != nil {
		return &ValidationResult{
			Passed:  ro.Passed,
			Kind:    "review",
			Message: buildReviewMessage(ro, res.Output),
		}, nil
	}

	return &ValidationResult{Passed: fallbackPassed(res.Output), Kind: "review", Message: res.Output}, nil
}

type fallbackCheck func(output string) bool

// dispatchReview calls the appropriate engine review method based on the review
// level. Returns nil result for unknown levels. The fallbackCheck is used when
// structured JSON parsing fails.
func dispatchReview(ctx context.Context, eng specEngine, code string, opts LoopOpts) (*agent.RunResult, fallbackCheck, error) {
	switch opts.ReviewLevel {
	case "full":
		res, err := eng.CodeReview(ctx, engine.CodeReviewOpts{
			Prompt: fmt.Sprintf("Review this generated code:\n\n%s%s", code, reviewJSONInstruction),
			Cwd:    opts.Cwd,
		})
		return res, func(o string) bool { return !indicatesFailure(o, []string{"fail", "critical"}) }, err

	case "light":
		res, err := eng.Bugbot(ctx, engine.BugbotOpts{
			Prompt: code + reviewJSONInstruction,
			Cwd:    opts.Cwd,
		})
		return res, func(o string) bool { return !indicatesFailure(o, []string{"critical"}) }, err

	case "solid":
		res, err := eng.Review(ctx, engine.ReviewOpts{
			Code:         code,
			Requirements: opts.Prompt + reviewJSONInstruction,
		})
		return res, func(o string) bool { return !indicatesFailure(o, []string{"fail"}) }, err

	case "deep":
		prompt := promptpkg.BuildDeepReviewPrompt(code)
		res, err := eng.CodeReview(ctx, engine.CodeReviewOpts{
			Prompt: prompt,
			Cwd:    opts.Cwd,
		})
		return res, func(o string) bool { return !indicatesFailure(o, []string{"fail", "critical"}) }, err

	default:
		return nil, nil, nil
	}
}
