package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	specmod "github.com/crestenstclair/crest-spec/internal/spec"
)

// registerTools populates s.tools, s.dispatch, and s.toolFns.
func (s *Server) registerTools() {
	s.dispatch = map[string]handlerFunc{
		"initialize":                s.handleInitialize,
		"notifications/initialized": s.handleInitialized,
		"tools/list":                s.handleToolsList,
		"tools/call":                s.handleToolCall,
		"resources/list":            s.handleResourcesList,
		"resources/read":            s.handleResourcesRead,
		"prompts/list":              s.handlePromptsList,
		"prompts/get":               s.handlePromptsGet,
	}
	s.registerInfoTools()
	if s.spec != nil {
		s.registerSpecLifecycleTools()
		s.registerSpecQueryTools()
	} else {
		s.registerSpecStubs()
	}
}

// registerInfoTools adds informational tools (live_metrics, about).
func (s *Server) registerInfoTools() {
	s.addTool(toolDef{
		Name:        "about",
		Description: "Show system info and the spec workflow guide. Call this first to understand how to use the tools.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, s.handleAbout)

	s.addTool(toolDef{
		Name:        "live_metrics",
		Description: "Self-monitoring snapshot: uptime, call counts, error rates, per-tool stats.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage) toolResult {
		snap := s.metrics.Snapshot()
		return jsonResult(snap)
	})
}

func (s *Server) handleAbout(_ context.Context, _ json.RawMessage) toolResult {
	return textResult(`crest-spec — declarative code generation MCP server (state engine only).

This server plans, tracks, validates, and records. It does not run LLMs.
Claude Code orchestrates generation natively. Call spec/begin and follow
the returned Instructions, or read the server instructions from initialize.`)
}

// registerSpecStubs adds placeholder stubs when no spec handler is provided.
func (s *Server) registerSpecStubs() {
	stubs := []toolDef{
		{Name: "spec/plan", Description: "Show what would change (dry run)", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"filter":{"type":"string","description":"Resource filter pattern"}}}`)},
		{Name: "spec/validate", Description: "Check structural invariants", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`)},
		{Name: "spec/begin", Description: "Step 1: Start a generation session.", InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"Target resource filter"},"force":{"type":"boolean","description":"Force regeneration"},"model":{"type":"string","description":"Model override"}}}`)},
		{Name: "spec/confirm_destroys", Description: "Confirm and execute pending resource destroys.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_ids":{"type":"array","items":{"type":"string"},"description":"Resource IDs to confirm for deletion"}},"required":["session_id","resource_ids"]}`)},
		{Name: "spec/next", Description: "Step 2: Get next wave of resources.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`)},
		{Name: "spec/context", Description: "Step 3: Get the generation prompt for a resource.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["session_id","resource_id"]}`)},
		{Name: "spec/validate-resource", Description: "Run invariant checks for a resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`)},
		{Name: "spec/note", Description: "Save a design decision note", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"content":{"type":"string","description":"Note content"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id","content"]}`)},
		{Name: "spec/commit", Description: "Commit generated files for a resource. The server writes the files, runs the resource's mechanical validations, and enforces the supplied invariant verdicts — any failure rejects the commit. Pass invariant_checks judged against the invariants returned by spec/context.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}},"notes":{"type":"string","description":"Design decision notes"},"model":{"type":"string","description":"Model that generated the files (recorded in state)"},"invariant_checks":{"type":"array","description":"Orchestrator-judged verdicts for the project invariants returned by spec/context. A failed verdict rejects the commit.","items":{"type":"object","properties":{"invariant":{"type":"string"},"passed":{"type":"boolean"},"summary":{"type":"string"}},"required":["invariant","passed"]}}},"required":["session_id","resource_id"]}`)},
		{Name: "spec/resolve", Description: "Provide guidance for blocked resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"guidance":{"type":"string","description":"Resolution guidance"},"model":{"type":"string","description":"Model override"}},"required":["resource_id","guidance"]}`)},
		{Name: "spec/amend", Description: "Signal spec update for resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`)},
		{Name: "spec/skip", Description: "Skip a resource.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"reason":{"type":"string","description":"Reason for skipping"}},"required":["resource_id"]}`)},
		{Name: "spec/finish", Description: "Step 6: Finalize the session.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"force":{"type":"boolean","description":"Force finish even with incomplete resources"}},"required":["session_id"]}`)},
		{Name: "spec/status", Description: "Session-level status overview", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID (optional)"}}}`)},
		{Name: "spec/wave_status", Description: "Detailed wave-level view", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"wave_index":{"type":"integer","description":"Wave index"}},"required":["session_id","wave_index"]}`)},
		{Name: "spec/log", Description: "List past applies", InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max entries to return"}}}`)},
		{Name: "spec/history", Description: "Show generation history for resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"limit":{"type":"integer","description":"Max entries to return"}},"required":["resource_id"]}`)},
		{Name: "spec/graph", Description: "Return dependency graph", InputSchema: json.RawMessage(`{"type":"object","properties":{"format":{"type":"string","description":"Output format (json, dot)"}}}`)},
		{Name: "spec/diff", Description: "Reconstruct state delta between applies", InputSchema: json.RawMessage(`{"type":"object","properties":{"apply_id_a":{"type":"string","description":"First apply ID"},"apply_id_b":{"type":"string","description":"Second apply ID"}}}`)},
		{Name: "spec/state", Description: "Inspect/modify state tracking", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"action":{"type":"string","description":"Action: list or rm"}}}`)},
		{Name: "spec/vacuum", Description: "Compact old history", InputSchema: json.RawMessage(`{"type":"object","properties":{"older_than":{"type":"string","description":"Age threshold (e.g. 30d)"}}}`)},
		{Name: "spec/sql", Description: "Read-only SQLite shell", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"SQL query to execute"}},"required":["query"]}`)},
		{Name: "spec/unlock", Description: "Force-clear stale lock", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "spec/mode", Description: "Show the current mode (environment)", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "spec/inspect", Description: "Full debug view of a resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`)},
		{Name: "spec/import", Description: "Scan directory and generate skeleton CUE spec", InputSchema: json.RawMessage(`{"type":"object","properties":{"directory":{"type":"string","description":"Directory to scan"}},"required":["directory"]}`)},
		{Name: "spec/prompt", Description: "Build and return the prompt for a resource without dispatching", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`)},
		{Name: "spec/record_learnings", Description: "Persist learnings distilled by a reflection run.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session the reflection belongs to (provides apply provenance)"},"output":{"type":"string","description":"Raw reflection LLM output"}},"required":["session_id","output"]}`)},
		{Name: "spec/bootstrap", Description: "Check environment and set up crest-spec", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Override spec directory location"}}}`)},
		{Name: "spec/apply_amendments", Description: "Human-gated write-back of approved amendments into the CUE spec.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string"},"proposals":{"type":"array","items":{"type":"object"}},"apply":{"type":"boolean"}},"required":["resource_id","proposals"]}`)},
		{Name: "spec/list_amendments", Description: "List materialized amendments, optionally filtered by resource_id and/or state.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string"},"state":{"type":"string"}}}`)},
		{Name: "spec/graduate_amendment", Description: "Human-gated: fold a VERIFIED amendment's intent into the resource's canonical invariants.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string"},"name":{"type":"string"},"apply":{"type":"boolean"}},"required":["resource_id","name"]}`)},
		{Name: "spec/evolve", Description: "Build the reflection prompt from a session's failure history. Run the returned prompt with an LLM (sonnet), then submit the raw output to spec/record_learnings. Returns an empty prompt when there is nothing to learn from.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID to reflect over"}},"required":["session_id"]}`)},
		{Name: "spec/learnings", Description: "List craft-level learnings extracted by reflection. Filter by status (active, retired, promoted); defaults to active. Returns id, scope, text, confidence, status, and times_applied.", InputSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","description":"Learning status filter (default: active)"}}}`)},
		{Name: "spec/promote_learnings", Description: "Human-gated promotion of active learnings into the per-language learned prompt template. Selects learnings above thresholds (default confidence >= 0.8, times_applied >= 3) and returns the proposed markdown block. With apply=false (default) it writes nothing — review the block, then re-invoke with apply=true to append it to the template and mark those learnings promoted.", InputSchema: json.RawMessage(`{"type":"object","properties":{"lang":{"type":"string","description":"Language scope (default: rust). Selects learnings whose scope_lang is empty or matches."},"min_confidence":{"type":"number","description":"Minimum confidence threshold (default: 0.8)"},"min_times_applied":{"type":"integer","description":"Minimum times_applied threshold (default: 3)"},"apply":{"type":"boolean","description":"When true, writes the block to the template and marks learnings promoted. Default false (preview only)."},"template_path":{"type":"string","description":"Override the target template path (default: internal/prompt/templates/learned/<lang>.md)"}}}`)},
	}

	for _, def := range stubs {
		s.addTool(def, func(ctx context.Context, args json.RawMessage) toolResult {
			return textResult("not implemented yet -- available in a future release")
		})
	}
}

// ---------------------------------------------------------------------------
// Generic spec tool helpers — eliminate boilerplate across 27 handlers
// ---------------------------------------------------------------------------

// specTool creates a toolHandler that unmarshals args into A, calls fn, and
// returns jsonResult on success or errorResult (prefixed with label) on failure.
func specTool[A any](label string, fn func(ctx context.Context, args A) (any, error)) toolHandler {
	return func(ctx context.Context, raw json.RawMessage) toolResult {
		var a A
		json.Unmarshal(raw, &a)
		result, err := fn(ctx, a)
		if err != nil {
			return errorResult(fmt.Sprintf("%s: %v", label, err))
		}
		return jsonResult(result)
	}
}

// specToolErr is like specTool but for methods that return only an error.
// On success it returns jsonResult(confirmValue).
func specToolErr[A any](label string, confirmValue any, fn func(ctx context.Context, args A) error) toolHandler {
	return func(ctx context.Context, raw json.RawMessage) toolResult {
		var a A
		json.Unmarshal(raw, &a)
		if err := fn(ctx, a); err != nil {
			return errorResult(fmt.Sprintf("%s: %v", label, err))
		}
		return jsonResult(confirmValue)
	}
}

// specToolStrict is like specTool but fails on malformed JSON args.
func specToolStrict[A any](label string, fn func(ctx context.Context, args A) (any, error)) toolHandler {
	return func(ctx context.Context, raw json.RawMessage) toolResult {
		var a A
		if err := json.Unmarshal(raw, &a); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		result, err := fn(ctx, a)
		if err != nil {
			return errorResult(fmt.Sprintf("%s: %v", label, err))
		}
		return jsonResult(result)
	}
}

// ---------------------------------------------------------------------------
// Arg structs for spec tools — shared between registration and handlers
// ---------------------------------------------------------------------------

type specBeginArgs struct {
	Target string `json:"target"`
	Force  bool   `json:"force"`
	Model  string `json:"model"`
}

type specSessionArgs struct {
	SessionID string `json:"session_id"`
}

type specSessionResourceArgs struct {
	SessionID  string `json:"session_id"`
	ResourceID string `json:"resource_id"`
}

type specConfirmDestroysArgs struct {
	SessionID   string   `json:"session_id"`
	ResourceIDs []string `json:"resource_ids"`
}

type specResourceArgs struct {
	ResourceID string `json:"resource_id"`
}

type specResourceLimitArgs struct {
	ResourceID string `json:"resource_id"`
	Limit      int    `json:"limit"`
}

type specLimitArgs struct {
	Limit int `json:"limit"`
}

type specNoteArgs struct {
	ResourceID string `json:"resource_id"`
	Content    string `json:"content"`
	SessionID  string `json:"session_id"`
}

type specCommitArgs struct {
	SessionID  string `json:"session_id"`
	ResourceID string `json:"resource_id"`
	Files      []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
	Notes           string                        `json:"notes"`
	Model           string                        `json:"model"`
	InvariantChecks []specmod.InvariantCheckInput `json:"invariant_checks"`
}

type specResolveArgs struct {
	SessionID  string `json:"session_id"`
	ResourceID string `json:"resource_id"`
	Guidance   string `json:"guidance"`
	Model      string `json:"model"`
}

type specSkipArgs struct {
	SessionID  string `json:"session_id"`
	ResourceID string `json:"resource_id"`
	Reason     string `json:"reason"`
}

type specFinishArgs struct {
	SessionID string `json:"session_id"`
	Force     bool   `json:"force"`
}

type specEvolveArgs struct {
	SessionID string `json:"session_id"`
}

type specRecordLearningsArgs struct {
	SessionID string `json:"session_id"`
	Output    string `json:"output"`
}

type specLearningsArgs struct {
	Status string `json:"status"`
}

type specPromoteLearningsArgs struct {
	Lang            string  `json:"lang"`
	MinConfidence   float64 `json:"min_confidence"`
	MinTimesApplied int     `json:"min_times_applied"`
	Apply           bool    `json:"apply"`
	TemplatePath    string  `json:"template_path"`
}

type specDiffArgs struct {
	ApplyIDA string `json:"apply_id_a"`
	ApplyIDB string `json:"apply_id_b"`
}

type specStateArgs struct {
	ResourceID string `json:"resource_id"`
	Action     string `json:"action"`
}

type specVacuumArgs struct {
	OlderThan string `json:"older_than"`
}

type specSQLArgs struct {
	Query string `json:"query"`
}

type specImportArgs struct {
	Directory string `json:"directory"`
	Language  string `json:"language"`
	Output    string `json:"output"`
	DryRun    bool   `json:"dry_run"`
}

type specBootstrapArgs struct {
	SpecDir string `json:"spec_dir"`
}

type specWaveStatusArgs struct {
	SessionID string `json:"session_id"`
	WaveIndex int    `json:"wave_index"`
}

type specApplyAmendmentsArgs struct {
	ResourceID string                      `json:"resource_id"`
	Proposals  []specmod.ProposedAmendment `json:"proposals"`
	Apply      bool                        `json:"apply"`
}

type specListAmendmentsArgs struct {
	ResourceID string `json:"resource_id"`
	State      string `json:"state"`
}

type specGraduateAmendmentArgs struct {
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
	Apply      bool   `json:"apply"`
}

// ---------------------------------------------------------------------------
// registerSpecTools — compact registration table
// ---------------------------------------------------------------------------

// registerSpecLifecycleTools adds spec session lifecycle tools (plan through finish).
func (s *Server) registerSpecLifecycleTools() {
	s.addTool(toolDef{
		Name: "spec/plan", Description: "Show what would change (dry run)",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"filter":{"type":"string","description":"Resource filter pattern"}}}`),
	}, s.handleSpecPlan)

	s.addTool(toolDef{
		Name: "spec/validate", Description: "Check structural invariants",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`),
	}, s.handleSpecValidate)

	s.addTool(toolDef{
		Name: "spec/begin", Description: "Step 1: Start a generation session. Returns session_id and plan. Then call spec_next to get resources.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"Target resource filter"},"force":{"type":"boolean","description":"Force regeneration"},"model":{"type":"string","description":"Model override"}}}`),
	}, specTool("begin", func(ctx context.Context, a specBeginArgs) (any, error) {
		return s.spec.Begin(ctx, specmod.BeginOpts{Target: a.Target, Force: a.Force, Model: a.Model})
	}))

	s.addTool(toolDef{
		Name: "spec/confirm_destroys", Description: "Confirm and execute pending resource destroys. Call after spec/begin when PendingDestroys is non-empty. Only confirmed resource IDs are deleted.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_ids":{"type":"array","items":{"type":"string"},"description":"Resource IDs to confirm for deletion"}},"required":["session_id","resource_ids"]}`),
	}, specTool("confirm_destroys", func(ctx context.Context, a specConfirmDestroysArgs) (any, error) {
		return s.spec.ConfirmDestroys(ctx, a.SessionID, a.ResourceIDs)
	}))

	s.addTool(toolDef{
		Name: "spec/next", Description: "Step 2: Get next wave of resources to generate (respects dependency order). Returns done=true when all waves complete.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
	}, specTool("next", func(ctx context.Context, a specSessionArgs) (any, error) {
		return s.spec.Next(ctx, a.SessionID)
	}))

	s.addTool(toolDef{
		Name: "spec/context", Description: "Get the generation prompt for a resource. Returns system_prompt, prompt, and the project invariants to judge at commit time. Give the prompts to a generation sub-agent, then commit via spec/commit.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["session_id","resource_id"]}`),
	}, specTool("context", func(ctx context.Context, a specSessionResourceArgs) (any, error) {
		return s.spec.Context(ctx, a.SessionID, a.ResourceID)
	}))

	s.addTool(toolDef{
		Name: "spec/validate-resource", Description: "Run invariant checks for a resource",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`),
	}, specTool("validate-resource", func(ctx context.Context, a specResourceArgs) (any, error) {
		return s.spec.ValidateResource(ctx, a.ResourceID)
	}))

	s.addTool(toolDef{
		Name: "spec/note", Description: "Save a design decision note",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"content":{"type":"string","description":"Note content"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id","content"]}`),
	}, specToolErr("note", map[string]bool{"saved": true}, func(ctx context.Context, a specNoteArgs) error {
		return s.spec.Note(ctx, a.SessionID, a.ResourceID, a.Content)
	}))

	s.addTool(toolDef{
		Name: "spec/commit", Description: "Commit generated files for a resource. The server writes the files, runs the resource's mechanical validations, and enforces the supplied invariant verdicts — any failure rejects the commit. Pass invariant_checks judged against the invariants returned by spec/context.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}},"notes":{"type":"string","description":"Design decision notes"},"model":{"type":"string","description":"Model that generated the files (recorded in state)"},"invariant_checks":{"type":"array","description":"Orchestrator-judged verdicts for the project invariants returned by spec/context. A failed verdict rejects the commit.","items":{"type":"object","properties":{"invariant":{"type":"string"},"passed":{"type":"boolean"},"summary":{"type":"string"}},"required":["invariant","passed"]}}},"required":["session_id","resource_id"]}`),
	}, s.handleSpecCommit)

	s.addTool(toolDef{
		Name: "spec/resolve", Description: "Provide guidance for blocked resource",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"guidance":{"type":"string","description":"Resolution guidance"},"model":{"type":"string","description":"Model override"}},"required":["resource_id","guidance"]}`),
	}, specToolErr("resolve", map[string]bool{"resolved": true}, func(ctx context.Context, a specResolveArgs) error {
		return s.spec.Resolve(ctx, a.SessionID, a.ResourceID, a.Guidance, a.Model)
	}))

	s.addTool(toolDef{
		Name: "spec/amend", Description: "Signal spec update for resource",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`),
	}, specToolErr("amend", map[string]bool{"amended": true}, func(ctx context.Context, a specSessionResourceArgs) error {
		return s.spec.Amend(ctx, a.SessionID, a.ResourceID)
	}))

	s.addTool(toolDef{
		Name: "spec/skip", Description: "Skip a resource that failed generation (allows the wave to advance).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"reason":{"type":"string","description":"Reason for skipping"}},"required":["resource_id"]}`),
	}, specToolErr("skip", map[string]bool{"skipped": true}, func(ctx context.Context, a specSkipArgs) error {
		return s.spec.Skip(ctx, a.SessionID, a.ResourceID, a.Reason)
	}))

	s.addTool(toolDef{
		Name: "spec/finish", Description: "Step 6: Finalize the session and release the lock. Call after all waves are done.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"force":{"type":"boolean","description":"Force finish even with incomplete resources"}},"required":["session_id"]}`),
	}, specTool("finish", func(ctx context.Context, a specFinishArgs) (any, error) {
		return s.spec.Finish(ctx, a.SessionID, a.Force)
	}))

	s.addTool(toolDef{
		Name: "spec/evolve", Description: "Build the reflection prompt from a session's failure history. Run the returned prompt with an LLM (sonnet), then submit the raw output to spec/record_learnings. Returns an empty prompt when there is nothing to learn from.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID to reflect over"}},"required":["session_id"]}`),
	}, specTool("evolve", func(ctx context.Context, a specEvolveArgs) (any, error) {
		prompt, err := s.spec.EvolvePrompt(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"reflection_prompt": prompt}, nil
	}))

	s.addTool(toolDef{
		Name: "spec/record_learnings", Description: "Persist learnings distilled by a reflection run. Pass the session_id and the raw LLM output from the spec/evolve reflection prompt (the learnings marker block).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session the reflection belongs to (provides apply provenance)"},"output":{"type":"string","description":"Raw reflection LLM output"}},"required":["session_id","output"]}`),
	}, specTool("record_learnings", func(ctx context.Context, a specRecordLearningsArgs) (any, error) {
		added, err := s.spec.RecordLearnings(ctx, a.SessionID, a.Output)
		if err != nil {
			return nil, err
		}
		return map[string]int{"learnings_added": added}, nil
	}))

	s.addTool(toolDef{
		Name: "spec/learnings", Description: "List craft-level learnings extracted by reflection. Filter by status (active, retired, promoted); defaults to active. Returns id, scope, text, confidence, status, and times_applied.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","description":"Learning status filter (default: active)"}}}`),
	}, specTool("learnings", func(ctx context.Context, a specLearningsArgs) (any, error) {
		learnings, err := s.spec.ListLearnings(a.Status)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, len(learnings))
		for i, l := range learnings {
			out[i] = map[string]any{
				"id":            l.ID,
				"scope":         map[string]string{"lang": l.ScopeLang, "kind": l.ScopeKind},
				"text":          l.Text,
				"confidence":    l.Confidence,
				"status":        l.Status,
				"times_applied": l.TimesApplied,
			}
		}
		return out, nil
	}))

	s.addTool(toolDef{
		Name: "spec/promote_learnings", Description: "Human-gated promotion of active learnings into the per-language learned prompt template. Selects learnings above thresholds (default confidence >= 0.8, times_applied >= 3) and returns the proposed markdown block. With apply=false (default) it writes nothing — review the block, then re-invoke with apply=true to append it to the template and mark those learnings promoted.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"lang":{"type":"string","description":"Language scope (default: rust). Selects learnings whose scope_lang is empty or matches."},"min_confidence":{"type":"number","description":"Minimum confidence threshold (default: 0.8)"},"min_times_applied":{"type":"integer","description":"Minimum times_applied threshold (default: 3)"},"apply":{"type":"boolean","description":"When true, writes the block to the template and marks learnings promoted. Default false (preview only)."},"template_path":{"type":"string","description":"Override the target template path (default: internal/prompt/templates/learned/<lang>.md)"}}}`),
	}, specTool("promote_learnings", func(ctx context.Context, a specPromoteLearningsArgs) (any, error) {
		res, err := s.spec.PromoteLearnings(ctx, a.Lang, a.MinConfidence, a.MinTimesApplied, a.Apply, a.TemplatePath)
		if err != nil {
			return nil, err
		}
		learnings := make([]map[string]any, len(res.Promotable))
		for i, l := range res.Promotable {
			learnings[i] = map[string]any{
				"id":            l.ID,
				"text":          l.Text,
				"confidence":    l.Confidence,
				"times_applied": l.TimesApplied,
			}
		}
		return map[string]any{
			"promotable_count": len(res.Promotable),
			"target_path":      res.TargetPath,
			"applied":          res.Applied,
			"markdown_block":   res.MarkdownBlock,
			"learnings":        learnings,
		}, nil
	}))
}

// registerSpecQueryTools adds spec query and admin tools (status through prompt).
func (s *Server) registerSpecQueryTools() {
	s.addTool(toolDef{
		Name: "spec/status", Description: "Session-level status overview. Without session_id: shows general state. With session_id: shows wave progress, per-wave resource counts (committed/rejected/errored/pending).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID (optional — omit for general status)"}}}`),
	}, s.handleSpecStatus)

	s.addTool(toolDef{
		Name: "spec/wave_status", Description: "Detailed wave-level view: per-resource state, attempts, retries, errors. Use to inspect progress within a specific wave.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"wave_index":{"type":"integer","description":"Wave index (0-based)"}},"required":["session_id","wave_index"]}`),
	}, specTool("wave_status", func(ctx context.Context, a specWaveStatusArgs) (any, error) {
		return s.spec.WaveStatus(ctx, a.SessionID, a.WaveIndex)
	}))

	s.addTool(toolDef{
		Name: "spec/log", Description: "List past applies",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max entries to return"}}}`),
	}, specTool("log", func(ctx context.Context, a specLimitArgs) (any, error) {
		return s.spec.Log(ctx, a.Limit)
	}))

	s.addTool(toolDef{
		Name: "spec/history", Description: "Show generation history for resource",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"limit":{"type":"integer","description":"Max entries to return"}},"required":["resource_id"]}`),
	}, specTool("history", func(ctx context.Context, a specResourceLimitArgs) (any, error) {
		return s.spec.History(ctx, a.ResourceID, a.Limit)
	}))

	s.addTool(toolDef{
		Name: "spec/graph", Description: "Return dependency graph",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"format":{"type":"string","description":"Output format (json, dot)"}}}`),
	}, s.handleSpecGraph)

	s.addTool(toolDef{
		Name: "spec/diff", Description: "Reconstruct state delta between applies",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"apply_id_a":{"type":"string","description":"First apply ID"},"apply_id_b":{"type":"string","description":"Second apply ID"}}}`),
	}, s.handleSpecDiff)

	s.addTool(toolDef{
		Name: "spec/state", Description: "Inspect/modify state tracking",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"action":{"type":"string","description":"Action: list or rm"}}}`),
	}, s.handleSpecState)

	s.addTool(toolDef{
		Name: "spec/vacuum", Description: "Compact old history",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"older_than":{"type":"string","description":"Age threshold (e.g. 30d, 7d, 24h)"}}}`),
	}, s.handleSpecVacuum)

	s.addTool(toolDef{
		Name: "spec/sql", Description: "Read-only SQLite shell",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"SQL query to execute"}},"required":["query"]}`),
	}, s.handleSpecSQL)

	s.addTool(toolDef{
		Name: "spec/inspect", Description: "Full debug view of a resource: effective prompt, hash breakdown, dependency chain, generated files, wave assignment.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`),
	}, specTool("inspect", func(ctx context.Context, a specResourceArgs) (any, error) {
		return s.spec.Inspect(ctx, a.ResourceID)
	}))

	s.addTool(toolDef{
		Name: "spec/unlock", Description: "Force-clear stale lock",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, s.handleSpecUnlock)

	s.addTool(toolDef{
		Name: "spec/mode", Description: "Show the current mode (environment). Different modes produce different hashes, triggering regeneration.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(_ context.Context, _ json.RawMessage) toolResult {
		return jsonResult(map[string]string{"mode": s.cfg.Mode})
	})

	s.addTool(toolDef{
		Name: "spec/import", Description: "Scan a directory of source files and generate a skeleton CUE spec. Heuristic-based classification by filename — no LLM calls.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"directory":{"type":"string","description":"Directory to scan for source files"},"language":{"type":"string","description":"Language hint (go, rust, typescript, python)"},"output":{"type":"string","description":"Output file path (default: spec/imported.cue)"},"dry_run":{"type":"boolean","description":"If true, return CUE output without writing to disk"}},"required":["directory"]}`),
	}, specToolStrict("import", func(ctx context.Context, a specImportArgs) (any, error) {
		return s.spec.Import(ctx, specmod.ImportOpts{
			Directory:  a.Directory,
			Language:   a.Language,
			OutputFile: a.Output,
			DryRun:     a.DryRun,
		})
	}))

	s.addTool(toolDef{
		Name: "spec/prompt", Description: "Build and return the full prompt for a resource WITHOUT dispatching to an LLM. Useful for reviewing what the model would see.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`),
	}, specTool("prompt", func(ctx context.Context, a specResourceArgs) (any, error) {
		return s.spec.Prompt(ctx, a.ResourceID)
	}))

	s.addTool(toolDef{
		Name: "spec/bootstrap", Description: "Check environment and set up crest-spec: spec directory, database, Claude CLI, MCP config. Idempotent — safe to run multiple times.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Override spec directory location"}}}`),
	}, specTool("bootstrap", func(ctx context.Context, a specBootstrapArgs) (any, error) {
		return s.spec.Bootstrap(ctx, specmod.BootstrapOpts{SpecDir: a.SpecDir})
	}))

	s.addTool(toolDef{
		Name:        "spec/apply_amendments",
		Description: "Human-gated write-back: writes approved amendments into the CUE spec as an override file. apply=false (default) returns the CUE diff for review and writes nothing; apply=true writes it. After approval, normal plan/begin re-renders the resource in UPDATE mode.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string"},"proposals":{"type":"array","items":{"type":"object"}},"apply":{"type":"boolean"}},"required":["resource_id","proposals"]}`),
	}, specTool("apply_amendments", func(ctx context.Context, a specApplyAmendmentsArgs) (any, error) {
		return s.spec.ApplyAmendments(ctx, a.ResourceID, a.Proposals, a.Apply)
	}))

	s.addTool(toolDef{
		Name:        "spec/list_amendments",
		Description: "List materialized amendments, optionally filtered by resource_id and/or state (PENDING|APPLIED|VERIFIED|GRADUATED|FAILED).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string"},"state":{"type":"string"}}}`),
	}, specTool("list_amendments", func(ctx context.Context, a specListAmendmentsArgs) (any, error) {
		return s.spec.ListAmendments(ctx, a.ResourceID, a.State)
	}))

	s.addTool(toolDef{
		Name:        "spec/graduate_amendment",
		Description: "Human-gated: fold a VERIFIED amendment's intent into the resource's canonical invariants and remove the amendment. apply=false returns the CUE diff; apply=true writes it. Run a force clean regen afterward to confirm the intent survives without the amendment.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string"},"name":{"type":"string"},"apply":{"type":"boolean"}},"required":["resource_id","name"]}`),
	}, specTool("graduate_amendment", func(ctx context.Context, a specGraduateAmendmentArgs) (any, error) {
		return s.spec.GraduateAmendment(ctx, a.ResourceID, a.Name, a.Apply)
	}))
}

// ---------------------------------------------------------------------------
// Extracted spec tool handlers — tools with custom logic beyond unmarshal+call
// ---------------------------------------------------------------------------

func (s *Server) handleSpecPlan(ctx context.Context, _ json.RawMessage) toolResult {
	result, err := s.spec.Plan(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("plan: %v", err))
	}
	return jsonResult(result.Actions)
}

func (s *Server) handleSpecValidate(ctx context.Context, _ json.RawMessage) toolResult {
	result, err := s.spec.Validate(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("validate: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecCommit(ctx context.Context, args json.RawMessage) toolResult {
	var p specCommitArgs
	json.Unmarshal(args, &p)
	files := make([]specmod.CommitFile, len(p.Files))
	for i, f := range p.Files {
		files[i] = specmod.CommitFile{Path: f.Path, Content: f.Content}
	}
	result, err := s.spec.Commit(ctx, p.SessionID, p.ResourceID, files, p.Notes, p.InvariantChecks, p.Model)
	if err != nil {
		return errorResult(fmt.Sprintf("commit: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecStatus(ctx context.Context, args json.RawMessage) toolResult {
	var p specSessionArgs
	json.Unmarshal(args, &p)

	if p.SessionID != "" {
		result, err := s.spec.SessionStatus(ctx, p.SessionID)
		if err != nil {
			return errorResult(fmt.Sprintf("session status: %v", err))
		}
		return jsonResult(result)
	}

	result, err := s.spec.Status(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("status: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecGraph(ctx context.Context, _ json.RawMessage) toolResult {
	result, err := s.spec.GraphInfo(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("graph: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecDiff(ctx context.Context, args json.RawMessage) toolResult {
	var p specDiffArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult("invalid arguments: " + err.Error())
	}
	if p.ApplyIDA == "" || p.ApplyIDB == "" {
		return errorResult("both apply_id_a and apply_id_b are required")
	}
	result, err := s.spec.DiffApplies(ctx, p.ApplyIDA, p.ApplyIDB)
	if err != nil {
		return errorResult(fmt.Sprintf("diff: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecState(ctx context.Context, args json.RawMessage) toolResult {
	var p specStateArgs
	json.Unmarshal(args, &p)

	if p.Action == "rm" {
		if p.ResourceID == "" {
			return errorResult("resource_id is required for rm action")
		}
		if err := s.spec.RemoveResource(ctx, p.ResourceID); err != nil {
			return errorResult(fmt.Sprintf("remove resource: %v", err))
		}
		return jsonResult(map[string]any{"removed": p.ResourceID})
	}

	// "list" or empty — return full status with resource hashes
	result, err := s.spec.Status(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("state: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecVacuum(ctx context.Context, args json.RawMessage) toolResult {
	var p specVacuumArgs
	json.Unmarshal(args, &p)
	if p.OlderThan == "" {
		p.OlderThan = "30d"
	}
	dur, err := parseDuration(p.OlderThan)
	if err != nil {
		return errorResult(fmt.Sprintf("invalid older_than: %v", err))
	}
	before := time.Now().Add(-dur)
	deleted, err := s.spec.Vacuum(ctx, before)
	if err != nil {
		return errorResult(fmt.Sprintf("vacuum: %v", err))
	}
	return jsonResult(map[string]any{
		"deleted":    deleted,
		"older_than": p.OlderThan,
		"before":     before.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleSpecSQL(ctx context.Context, args json.RawMessage) toolResult {
	var p specSQLArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult("invalid arguments: " + err.Error())
	}
	trimmed := strings.TrimSpace(p.Query)
	if len(trimmed) < 6 || !strings.EqualFold(trimmed[:6], "SELECT") {
		return errorResult("only SELECT queries are allowed")
	}
	rows, err := s.spec.ReadOnlyQuery(ctx, p.Query)
	if err != nil {
		return errorResult(fmt.Sprintf("sql: %v", err))
	}
	return jsonResult(rows)
}

func (s *Server) handleSpecUnlock(ctx context.Context, _ json.RawMessage) toolResult {
	if err := s.spec.Unlock(ctx); err != nil {
		return errorResult(fmt.Sprintf("unlock: %v", err))
	}
	return jsonResult(map[string]bool{"unlocked": true})
}

// addTool registers a tool definition and its handler.
func (s *Server) addTool(def toolDef, handler toolHandler) {
	s.tools = append(s.tools, def)
	s.toolFns[def.Name] = handler
}

// parseDuration parses duration strings like "30d", "7d", "24h", "2h30m".
// It extends time.ParseDuration to support "d" (days) as a suffix.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	// Check for day suffix: e.g. "30d"
	if strings.HasSuffix(s, "d") {
		numStr := s[:len(s)-1]
		days, err := strconv.Atoi(numStr)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	// Fall back to standard Go duration parsing (supports h, m, s, etc.)
	return time.ParseDuration(s)
}
