package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	enginemod "github.com/crestenstclair/crest-spec/internal/engine"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	storemod "github.com/crestenstclair/crest-spec/internal/store"
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

	s.registerAsyncTools()
	s.registerJobTools()
	s.registerInfoTools()

	if s.spec != nil {
		s.registerSpecLifecycleTools()
		s.registerSpecDispatchTools()
		s.registerSpecQueryTools()
	} else {
		s.registerSpecStubs()
	}
}

// registerAsyncTools adds tools that dispatch work via runAsync (run_prompt, code_review, bugbot).
func (s *Server) registerAsyncTools() {
	s.addTool(toolDef{
		Name:        "run_prompt",
		Description: "Step 4: Dispatch a prompt to a Claude sub-agent. Returns job_id immediately — use poll_result to retrieve the output. In spec workflow, pass the prompt and system_prompt from spec_context here.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"The prompt to send"},"system_prompt":{"type":"string","description":"System prompt appended to the agent"},"model":{"type":"string","description":"Model override (default: generate model from config)"},"session_id":{"type":"string","description":"Session ID (optional, enables generation tracking in SQLite)"},"resource_id":{"type":"string","description":"Resource ID (optional, links generation to a resource)"}},"required":["prompt"]}`),
	}, s.handleRunPrompt)

	s.addTool(toolDef{
		Name:        "code_review",
		Description: "Multi-model code review. Fans out across models and aggregates findings per model.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory for the review"},"models":{"type":"array","items":{"type":"string"},"description":"Models to use (default: opus, sonnet)"},"prompt":{"type":"string","description":"Review instructions or focus areas"}},"required":["prompt"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Cwd    string   `json:"cwd"`
			Models []string `json:"models"`
			Prompt string   `json:"prompt"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		return s.runAsync("code_review", func(ctx context.Context, _ string) (string, error) {
			res, err := s.eng.CodeReview(ctx, enginemod.CodeReviewOpts{Cwd: p.Cwd, Models: p.Models, Prompt: p.Prompt})
			if err != nil {
				return "", err
			}
			return res.Output, nil
		}, progressToken)
	})

	s.addTool(toolDef{
		Name:        "bugbot",
		Description: "Lightweight severity-ranked bug scan.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory for the scan"},"models":{"type":"array","items":{"type":"string"},"description":"Models to use (default: sonnet)"},"prompt":{"type":"string","description":"Scan focus or file list"}},"required":["prompt"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Cwd    string   `json:"cwd"`
			Models []string `json:"models"`
			Prompt string   `json:"prompt"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		return s.runAsync("bugbot", func(ctx context.Context, _ string) (string, error) {
			res, err := s.eng.Bugbot(ctx, enginemod.BugbotOpts{Cwd: p.Cwd, Models: p.Models, Prompt: p.Prompt})
			if err != nil {
				return "", err
			}
			return res.Output, nil
		}, progressToken)
	})
}

func (s *Server) handleRunPrompt(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
	var p struct {
		Prompt       string `json:"prompt"`
		SystemPrompt string `json:"system_prompt"`
		Model        string `json:"model"`
		SessionID    string `json:"session_id"`
		ResourceID   string `json:"resource_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult("invalid arguments: " + err.Error())
	}

	var genID string
	var applyID string
	if p.SessionID != "" && p.ResourceID != "" {
		sess, _ := s.store.GetActiveSession()
		if sess != nil {
			applyID = sess.ApplyID
		}
		genID = uuid.NewString()
		promptHash := fmt.Sprintf("%x", sha256.Sum256([]byte(p.Prompt)))
		s.store.CreateGeneration(storemod.Generation{
			ID: genID, ApplyID: applyID, ResourceID: p.ResourceID,
			PromptText: p.Prompt, PromptHash: promptHash, Model: p.Model,
		})
	}

	return s.runAsync("run_prompt", func(ctx context.Context, _ string) (string, error) {
		startTime := time.Now()
		res, err := s.eng.Generate(ctx, enginemod.GenerateOpts{
			Prompt: p.Prompt, Model: p.Model, AppendSystemPrompt: p.SystemPrompt,
		})
		durationMS := time.Since(startTime).Milliseconds()

		if genID != "" {
			if err != nil {
				s.store.UpdateGeneration(genID, "", "error", err.Error(), durationMS, 0, 0, 0)
			} else {
				s.store.UpdateGeneration(genID, res.Output, "success", "", durationMS, 0, 0, 0)
			}
		}
		if err != nil {
			return "", err
		}
		return res.Output, nil
	}, progressToken)
}

// registerJobTools adds job management tools (poll_result, cancel_job, list_jobs).
func (s *Server) registerJobTools() {
	s.addTool(toolDef{
		Name:        "poll_result",
		Description: "Check an async job's status and retrieve its output. Returns status (queued/running/done/error) and output when complete. Use after run_prompt, code_review, or bugbot.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string","description":"The job ID to poll"},"consume":{"type":"boolean","description":"If true, delete the job after reading (default: false)"}},"required":["job_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			JobID   string `json:"job_id"`
			Consume bool   `json:"consume"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}

		job, err := s.store.GetJob(p.JobID)
		if err != nil {
			return errorResult(fmt.Sprintf("job not found: %s", p.JobID))
		}

		resp := map[string]string{
			"status":   job.Status,
			"result":   job.Result,
			"error":    job.Error,
			"progress": job.ProgressJSON,
		}

		if p.Consume && (job.Status == "completed" || job.Status == "failed" || job.Status == "cancelled") {
			if err := s.store.DeleteJob(p.JobID); err != nil {
				return errorResult(fmt.Sprintf("delete job: %v", err))
			}
		}

		return jsonResult(resp)
	})

	s.addTool(toolDef{
		Name:        "cancel_job",
		Description: "Cancel a running job and kill its subprocess group.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string","description":"The job ID to cancel"}},"required":["job_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}

		s.cancelsMu.Lock()
		cancelFn, ok := s.cancels[p.JobID]
		s.cancelsMu.Unlock()

		if ok {
			cancelFn()
			return jsonResult(map[string]bool{"cancelled": true})
		}

		job, err := s.store.GetJob(p.JobID)
		if err != nil {
			return errorResult(fmt.Sprintf("job not found: %s", p.JobID))
		}
		return textResult(fmt.Sprintf("job %s already in status: %s", p.JobID, job.Status))
	})

	s.addTool(toolDef{
		Name:        "list_jobs",
		Description: "List up to 50 recent non-deleted jobs.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max jobs to return (default: 50, max: 50)"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Limit int `json:"limit"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		if p.Limit <= 0 || p.Limit > 50 {
			p.Limit = 50
		}
		jobs, err := s.store.ListJobs(p.Limit)
		if err != nil {
			return errorResult(fmt.Sprintf("list jobs: %v", err))
		}
		return jsonResult(jobs)
	})
}

// registerInfoTools adds informational tools (list_models, about, status, live_metrics).
func (s *Server) registerInfoTools() {
	s.addTool(toolDef{
		Name:        "list_models",
		Description: "List available Claude models.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		out, err := s.eng.Models(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("list models: %v", err))
		}
		return textResult(out)
	})

	s.addTool(toolDef{
		Name:        "about",
		Description: "Show system info and the spec workflow guide. Call this first to understand how to use the tools.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, s.handleAbout)

	s.addTool(toolDef{
		Name:        "status",
		Description: "Show claude auth status.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		out, err := s.eng.Status(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("status: %v", err))
		}
		return textResult(out)
	})

	s.addTool(toolDef{
		Name:        "live_metrics",
		Description: "Self-monitoring snapshot: uptime, call counts, error rates, per-tool stats.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		snap := s.metrics.Snapshot()
		return jsonResult(snap)
	})
}

func (s *Server) handleAbout(ctx context.Context, _ json.RawMessage, _ string) toolResult {
	about, aboutErr := s.eng.About(ctx)
	status, statusErr := s.eng.Status(ctx)
	if aboutErr != nil {
		return errorResult(fmt.Sprintf("about: %v", aboutErr))
	}
	if statusErr != nil {
		return errorResult(fmt.Sprintf("status: %v", statusErr))
	}
	return textResult(fmt.Sprintf(`Version: %s
Auth: %s

## Spec workflow — you are the orchestrator

To generate code from a spec, drive this pipeline:

1. spec_plan       → see what needs generating
2. spec_begin      → start session (returns session_id)
3. spec_next       → get next wave of resources
4. For each resource:
   a. spec_context → get scoped prompt + system_prompt
   b. run_prompt   → dispatch to Claude sub-agent (returns job_id)
   c. poll_result  → retrieve output when ready
   d. Parse output: extract code blocks with "// path:" annotations
   e. spec_commit  → commit parsed files
   f. On failure: retry with feedback, or spec_skip
5. Repeat step 3 until done
6. spec_finish     → finalize session

Parallelize run_prompt calls within a wave. Review output before committing.
Do NOT use spec_apply — it runs unattended with no agent control.`, about, status))
}

// registerSpecStubs adds placeholder stubs when no spec handler is provided.
func (s *Server) registerSpecStubs() {
	stubs := []toolDef{
		{Name: "spec/plan", Description: "Show what would change (dry run)", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"filter":{"type":"string","description":"Resource filter pattern"}}}`)},
		{Name: "spec/apply", Description: "Unattended apply (no agent control). Prefer the manual pipeline: spec_begin → spec_next → spec_context → run_prompt → spec_commit.", InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"Target resource filter"},"force":{"type":"boolean","description":"Force regeneration"}}}`)},
		{Name: "spec/validate", Description: "Check structural invariants", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`)},
		{Name: "spec/begin", Description: "Step 1: Start a generation session.", InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"Target resource filter"},"force":{"type":"boolean","description":"Force regeneration"},"model":{"type":"string","description":"Model override"}}}`)},
		{Name: "spec/confirm_destroys", Description: "Confirm and execute pending resource destroys.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_ids":{"type":"array","items":{"type":"string"},"description":"Resource IDs to confirm for deletion"}},"required":["session_id","resource_ids"]}`)},
		{Name: "spec/next", Description: "Step 2: Get next wave of resources.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`)},
		{Name: "spec/context", Description: "Step 3: Get the generation prompt for a resource.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["session_id","resource_id"]}`)},
		{Name: "spec/validate-resource", Description: "Run invariant checks for a resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`)},
		{Name: "spec/note", Description: "Save a design decision note", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"content":{"type":"string","description":"Note content"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id","content"]}`)},
		{Name: "spec/commit", Description: "Step 5: Commit generated files for a resource.", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}},"notes":{"type":"string","description":"Design decision notes"}},"required":["session_id","resource_id"]}`)},
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
		{Name: "spec/state", Description: "Inspect/modify state tracking", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"action":{"type":"string","description":"Action: get, set, clear"}}}`)},
		{Name: "spec/drift", Description: "Handle drifted resources", InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","description":"accept or revert"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["action","resource_id"]}`)},
		{Name: "spec/vacuum", Description: "Compact old history", InputSchema: json.RawMessage(`{"type":"object","properties":{"older_than":{"type":"string","description":"Age threshold (e.g. 30d)"}}}`)},
		{Name: "spec/sql", Description: "Read-only SQLite shell", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"SQL query to execute"}},"required":["query"]}`)},
		{Name: "spec/unlock", Description: "Force-clear stale lock", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "spec/mode", Description: "Show the current mode (environment)", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "spec/inspect", Description: "Full debug view of a resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`)},
		{Name: "spec/import", Description: "Scan directory and generate skeleton CUE spec", InputSchema: json.RawMessage(`{"type":"object","properties":{"directory":{"type":"string","description":"Directory to scan"}},"required":["directory"]}`)},
		{Name: "spec/prompt", Description: "Build and return the prompt for a resource without dispatching", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`)},
		{Name: "spec/dispatch", Description: "Atomic generate-and-commit for a single resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"model":{"type":"string","description":"Model override"}},"required":["session_id","resource_id"]}`)},
		{Name: "spec/run_wave", Description: "Dispatch an entire wave of resources in parallel", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"model":{"type":"string","description":"Default model"},"model_overrides":{"type":"object","description":"Per-resource model overrides"}},"required":["session_id"]}`)},
		{Name: "spec/bootstrap", Description: "Check environment and set up crest-spec", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Override spec directory location"}}}`)},
		{Name: "spec/deep_review", Description: "Comprehensive SOLID/DI/clean code/refactoring review of generated code", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID to identify project context"},"target":{"type":"string","description":"Specific resource ID to review, or omit to review all committed resources"}},"required":["session_id"]}`)},
	}

	for _, def := range stubs {
		s.addTool(def, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
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
	return func(ctx context.Context, raw json.RawMessage, _ string) toolResult {
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
	return func(ctx context.Context, raw json.RawMessage, _ string) toolResult {
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
	return func(ctx context.Context, raw json.RawMessage, _ string) toolResult {
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
	Notes string `json:"notes"`
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

type specLearningsArgs struct {
	Status string `json:"status"`
}

type specDiffArgs struct {
	ApplyIDA string `json:"apply_id_a"`
	ApplyIDB string `json:"apply_id_b"`
}

type specStateArgs struct {
	ResourceID string `json:"resource_id"`
	Action     string `json:"action"`
}

type specDriftArgs struct {
	Action     string `json:"action"`
	ResourceID string `json:"resource_id"`
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
		Name: "spec/apply", Description: "Automated apply: runs the full constraint loop (generate → validate → retry) for all resources. Prefer the manual pipeline (begin → next → context → run_prompt → commit) for agent-controlled orchestration.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"Target resource filter"},"force":{"type":"boolean","description":"Force regeneration"},"model":{"type":"string","description":"Model override"}}}`),
	}, s.handleSpecApply)

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
		Name: "spec/context", Description: "Step 3: Get the generation prompt for a resource. Returns system_prompt and prompt — pass both to run_prompt.",
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
		Name: "spec/commit", Description: "Step 5: Commit generated files for a resource. Parse code blocks from run_prompt output (look for '// path:' annotations), then pass files here.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}},"notes":{"type":"string","description":"Design decision notes"}},"required":["session_id","resource_id"]}`),
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
		Name: "spec/evolve", Description: "On-demand reflection: distill craft-level learnings from a session's failure history (rejected generations, failed invariants, last errors). Returns the count of learnings added. Never blocks or fails a session.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID to reflect over"}},"required":["session_id"]}`),
	}, specTool("evolve", func(ctx context.Context, a specEvolveArgs) (any, error) {
		added, err := s.spec.Evolve(ctx, a.SessionID)
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
}

// registerSpecDispatchTools adds the high-level orchestrator tools (dispatch, run_wave).
func (s *Server) registerSpecDispatchTools() {
	s.addTool(toolDef{
		Name: "spec/dispatch", Description: "Atomic generate-and-commit for a single resource. Blocks until complete and returns the result inline (no polling needed). Sends progress notifications via SSE.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"model":{"type":"string","description":"Model override for this resource"}},"required":["session_id","resource_id"]}`),
	}, s.handleSpecDispatch)

	s.addTool(toolDef{
		Name: "spec/run_wave", Description: "Dispatch an entire wave of resources in parallel. Blocks until all resources complete and returns the full result inline (no polling needed). Sends per-resource progress notifications via SSE as each resource finishes.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"model":{"type":"string","description":"Default model for this wave"},"model_overrides":{"type":"object","description":"Per-resource model overrides (resource_id → model name)","additionalProperties":{"type":"string"}}},"required":["session_id"]}`),
	}, s.handleSpecRunWave)

	s.addTool(toolDef{
		Name: "spec/deep_review", Description: "Comprehensive SOLID/DI/clean code/refactoring review of generated code. Run after a full sync to identify SOLID violations, dependency injection issues, code smells, and design pattern opportunities.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID to identify project context"},"target":{"type":"string","description":"Specific resource ID to review, or omit to review all committed resources"}},"required":["session_id"]}`),
	}, s.handleSpecDeepReview)
}

func (s *Server) handleSpecDispatch(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
	var p struct {
		SessionID  string `json:"session_id"`
		ResourceID string `json:"resource_id"`
		Model      string `json:"model"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult("invalid arguments: " + err.Error())
	}

	start := time.Now()
	result, err := s.spec.Dispatch(ctx, specmod.DispatchOpts{
		SessionID:    p.SessionID,
		ResourceID:   p.ResourceID,
		Model:        p.Model,
		OnProgress:   s.progressSender(progressToken),
		OnAgentEvent: s.agentEventRecorder(),
	})
	s.metrics.Record("spec/dispatch", time.Since(start), err)
	if err != nil {
		return errorResult(fmt.Sprintf("spec/dispatch: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecRunWave(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
	var p struct {
		SessionID      string            `json:"session_id"`
		Model          string            `json:"model"`
		ModelOverrides map[string]string `json:"model_overrides"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult("invalid arguments: " + err.Error())
	}

	start := time.Now()
	result, err := s.spec.RunWave(ctx, specmod.RunWaveOpts{
		SessionID:      p.SessionID,
		Model:          p.Model,
		ModelOverrides: p.ModelOverrides,
		OnProgress:     s.progressSender(progressToken),
		OnAgentEvent:   s.agentEventRecorder(),
	})
	s.metrics.Record("spec/run_wave", time.Since(start), err)
	if err != nil {
		return errorResult(fmt.Sprintf("spec/run_wave: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecDeepReview(_ context.Context, args json.RawMessage, progressToken string) toolResult {
	var p struct {
		SessionID string `json:"session_id"`
		Target    string `json:"target"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult("invalid arguments: " + err.Error())
	}
	return s.runAsync("spec/deep_review", func(ctx context.Context, _ string) (string, error) {
		result, err := s.spec.DeepReview(ctx, specmod.DeepReviewOpts{
			SessionID: p.SessionID, Target: p.Target,
		})
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}, progressToken)
}

// agentEventRecorder returns an AgentEventFunc that writes real-time agent
// events to the store for dashboard tracing.
func (s *Server) agentEventRecorder() specmod.AgentEventFunc {
	return func(resourceID, eventType string, attempt int, content string) {
		s.store.CreateAgentEvent(storemod.AgentEvent{
			ID:         uuid.NewString(),
			ResourceID: resourceID,
			EventType:  eventType,
			Attempt:    attempt,
			Content:    content,
			CreatedAt:  time.Now(),
		})
	}
}

// progressSender returns a ProgressFunc that writes MCP progress notifications.
// Returns nil when no progressToken is provided (no-op for the caller).
func (s *Server) progressSender(progressToken string) specmod.ProgressFunc {
	if progressToken == "" {
		return nil
	}
	return func(update specmod.ProgressUpdate) {
		data, _ := json.Marshal(update)
		s.writeNotification("notifications/progress", map[string]any{
			"progressToken": progressToken,
			"progress":      update.Completed,
			"total":         update.Total,
			"message":       string(data),
		})
	}
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
		Name: "spec/drift", Description: "Handle drifted resources",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","description":"accept or revert"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["action","resource_id"]}`),
	}, specToolErr("drift", map[string]bool{"ok": true}, func(ctx context.Context, a specDriftArgs) error {
		return s.spec.DriftAction(ctx, a.Action, a.ResourceID)
	}))

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
	}, func(_ context.Context, _ json.RawMessage, _ string) toolResult {
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
}

// ---------------------------------------------------------------------------
// Extracted spec tool handlers — tools with custom logic beyond unmarshal+call
// ---------------------------------------------------------------------------

func (s *Server) handleSpecPlan(ctx context.Context, _ json.RawMessage, _ string) toolResult {
	result, err := s.spec.Plan(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("plan: %v", err))
	}
	return jsonResult(result.Actions)
}

func (s *Server) handleSpecApply(_ context.Context, args json.RawMessage, progressToken string) toolResult {
	var p specBeginArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult("invalid arguments: " + err.Error())
	}
	return s.runAsync("spec/apply", func(ctx context.Context, _ string) (string, error) {
		result, err := s.spec.Apply(ctx, specmod.BeginOpts{
			Target: p.Target, Force: p.Force, Model: p.Model,
		})
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}, progressToken)
}

func (s *Server) handleSpecValidate(ctx context.Context, _ json.RawMessage, _ string) toolResult {
	result, err := s.spec.Validate(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("validate: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecCommit(ctx context.Context, args json.RawMessage, _ string) toolResult {
	var p specCommitArgs
	json.Unmarshal(args, &p)
	files := make([]specmod.CommitFile, len(p.Files))
	for i, f := range p.Files {
		files[i] = specmod.CommitFile{Path: f.Path, Content: f.Content}
	}
	result, err := s.spec.Commit(ctx, p.SessionID, p.ResourceID, files, p.Notes)
	if err != nil {
		return errorResult(fmt.Sprintf("commit: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecStatus(ctx context.Context, args json.RawMessage, _ string) toolResult {
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

func (s *Server) handleSpecGraph(ctx context.Context, _ json.RawMessage, _ string) toolResult {
	result, err := s.spec.GraphInfo(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("graph: %v", err))
	}
	return jsonResult(result)
}

func (s *Server) handleSpecDiff(ctx context.Context, args json.RawMessage, _ string) toolResult {
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

func (s *Server) handleSpecState(ctx context.Context, args json.RawMessage, _ string) toolResult {
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

func (s *Server) handleSpecVacuum(ctx context.Context, args json.RawMessage, _ string) toolResult {
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

func (s *Server) handleSpecSQL(ctx context.Context, args json.RawMessage, _ string) toolResult {
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

func (s *Server) handleSpecUnlock(ctx context.Context, _ json.RawMessage, _ string) toolResult {
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
