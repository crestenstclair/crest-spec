package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	enginemod "github.com/crestenstclair/crest-spec/internal/engine"
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

	// ----- Engine tools (fully implemented) -----

	s.addTool(toolDef{
		Name:        "run_prompt",
		Description: "Run a prompt via the claude CLI sub-agent. Returns a job ID immediately; use poll_result to retrieve the output.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"The prompt to send"},"system_prompt":{"type":"string","description":"System prompt appended to the agent"},"model":{"type":"string","description":"Model override (default: generate model from config)"}},"required":["prompt"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Prompt       string `json:"prompt"`
			SystemPrompt string `json:"system_prompt"`
			Model        string `json:"model"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		return s.runAsync("run_prompt", func(ctx context.Context) (string, error) {
			res, err := s.eng.Generate(ctx, enginemod.GenerateOpts{
				Prompt:             p.Prompt,
				Model:              p.Model,
				AppendSystemPrompt: p.SystemPrompt,
			})
			if err != nil {
				return "", err
			}
			return res.Output, nil
		}, progressToken)
	})

	s.addTool(toolDef{
		Name:        "code_review",
		Description: "Multi-model code review. Fans out across models and aggregates findings per model.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory for the review"},"models":{"type":"array","items":{"type":"string"},"description":"Models to use (default: opus, sonnet, haiku)"},"prompt":{"type":"string","description":"Review instructions or focus areas"}},"required":["prompt"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Cwd    string   `json:"cwd"`
			Models []string `json:"models"`
			Prompt string   `json:"prompt"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		return s.runAsync("code_review", func(ctx context.Context) (string, error) {
			res, err := s.eng.CodeReview(ctx, enginemod.CodeReviewOpts{
				Cwd:    p.Cwd,
				Models: p.Models,
				Prompt: p.Prompt,
			})
			if err != nil {
				return "", err
			}
			return res.Output, nil
		}, progressToken)
	})

	s.addTool(toolDef{
		Name:        "bugbot",
		Description: "Lightweight severity-ranked bug scan.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory for the scan"},"models":{"type":"array","items":{"type":"string"},"description":"Models to use (default: haiku)"},"prompt":{"type":"string","description":"Scan focus or file list"}},"required":["prompt"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Cwd    string   `json:"cwd"`
			Models []string `json:"models"`
			Prompt string   `json:"prompt"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid arguments: " + err.Error())
		}
		return s.runAsync("bugbot", func(ctx context.Context) (string, error) {
			res, err := s.eng.Bugbot(ctx, enginemod.BugbotOpts{
				Cwd:    p.Cwd,
				Models: p.Models,
				Prompt: p.Prompt,
			})
			if err != nil {
				return "", err
			}
			return res.Output, nil
		}, progressToken)
	})

	s.addTool(toolDef{
		Name:        "poll_result",
		Description: "Check a job's status. Optionally consume (delete) the result.",
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
			"status": job.Status,
			"result": job.Result,
			"error":  job.Error,
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

		// Check if job exists but already finished
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
		Description: "Show claude CLI version and auth status.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		about, aboutErr := s.eng.About(ctx)
		status, statusErr := s.eng.Status(ctx)
		if aboutErr != nil {
			return errorResult(fmt.Sprintf("about: %v", aboutErr))
		}
		if statusErr != nil {
			return errorResult(fmt.Sprintf("status: %v", statusErr))
		}
		return textResult(fmt.Sprintf("Version: %s\nAuth: %s", about, status))
	})

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

	// ----- Spec tool stubs (registered now, implemented in SP3-SP5) -----

	specStubs := []toolDef{
		{Name: "spec/plan", Description: "Show what would change (dry run)", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"filter":{"type":"string","description":"Resource filter pattern"}}}`)},
		{Name: "spec/apply", Description: "Execute the plan (async)", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"filter":{"type":"string","description":"Resource filter pattern"},"dry_run":{"type":"boolean","description":"Preview without executing"}}}`)},
		{Name: "spec/validate", Description: "Check structural invariants", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`)},
		{Name: "spec/begin", Description: "Start interactive agent session", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`)},
		{Name: "spec/next", Description: "Get next wave of uncommitted resources", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}}}`)},
		{Name: "spec/context", Description: "Get scoped prompt for a resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"session_id":{"type":"string","description":"Session ID"}}}`)},
		{Name: "spec/validate-resource", Description: "Run invariant checks for a resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"session_id":{"type":"string","description":"Session ID"}}}`)},
		{Name: "spec/note", Description: "Save a design decision note", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"content":{"type":"string","description":"Note content"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id","content"]}`)},
		{Name: "spec/commit", Description: "Record a resource as complete", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id"]}`)},
		{Name: "spec/resolve", Description: "Provide guidance for blocked resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"guidance":{"type":"string","description":"Resolution guidance"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id","guidance"]}`)},
		{Name: "spec/amend", Description: "Signal spec update for resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id"]}`)},
		{Name: "spec/skip", Description: "Skip a failed resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"reason":{"type":"string","description":"Reason for skipping"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id"]}`)},
		{Name: "spec/finish", Description: "Finalize session, release lock", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}}}`)},
		{Name: "spec/status", Description: "Show current state", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`)},
		{Name: "spec/log", Description: "List past applies", InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max entries to return"}}}`)},
		{Name: "spec/history", Description: "Show generation history for resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"limit":{"type":"integer","description":"Max entries to return"}},"required":["resource_id"]}`)},
		{Name: "spec/graph", Description: "Return dependency graph", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"format":{"type":"string","description":"Output format (json, dot)"}}}`)},
		{Name: "spec/diff", Description: "Reconstruct state delta between applies", InputSchema: json.RawMessage(`{"type":"object","properties":{"apply_id_a":{"type":"string","description":"First apply ID"},"apply_id_b":{"type":"string","description":"Second apply ID"}}}`)},
		{Name: "spec/state", Description: "Inspect/modify state tracking", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"action":{"type":"string","description":"Action: get, set, clear"}}}`)},
		{Name: "spec/drift", Description: "Handle drifted resources", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`)},
		{Name: "spec/vacuum", Description: "Compact old history", InputSchema: json.RawMessage(`{"type":"object","properties":{"older_than":{"type":"string","description":"Age threshold (e.g. 30d)"}}}`)},
		{Name: "spec/sql", Description: "Read-only SQLite shell", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"SQL query to execute"}},"required":["query"]}`)},
		{Name: "spec/unlock", Description: "Force-clear stale lock", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
	}

	for _, def := range specStubs {
		s.addTool(def, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
			return textResult("not implemented yet -- available in a future release")
		})
	}
}

// addTool registers a tool definition and its handler.
func (s *Server) addTool(def toolDef, handler toolHandler) {
	s.tools = append(s.tools, def)
	s.toolFns[def.Name] = handler
}
