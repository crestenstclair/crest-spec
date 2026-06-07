package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	enginemod "github.com/crestenstclair/crest-spec/internal/engine"
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

	// ----- Spec tools -----

	if s.spec != nil {
		s.registerSpecTools()
	} else {
		// Fallback stubs when no spec handler is provided (e.g. tests)
		specStubs := []toolDef{
			{Name: "spec/plan", Description: "Show what would change (dry run)", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"filter":{"type":"string","description":"Resource filter pattern"}}}`)},
			{Name: "spec/apply", Description: "Execute the plan (async)", InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"Target resource filter"},"force":{"type":"boolean","description":"Force regeneration"}}}`)},
			{Name: "spec/validate", Description: "Check structural invariants", InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`)},
			{Name: "spec/begin", Description: "Start interactive agent session", InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"Target resource filter"},"force":{"type":"boolean","description":"Force regeneration"},"model":{"type":"string","description":"Model override"}}}`)},
			{Name: "spec/next", Description: "Get next wave of uncommitted resources", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`)},
			{Name: "spec/context", Description: "Get scoped prompt for a resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["session_id","resource_id"]}`)},
			{Name: "spec/validate-resource", Description: "Run invariant checks for a resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`)},
			{Name: "spec/note", Description: "Save a design decision note", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"content":{"type":"string","description":"Note content"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id","content"]}`)},
			{Name: "spec/commit", Description: "Record a resource as complete", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}},"notes":{"type":"string","description":"Design decision notes"}},"required":["session_id","resource_id"]}`)},
			{Name: "spec/resolve", Description: "Provide guidance for blocked resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"guidance":{"type":"string","description":"Resolution guidance"},"model":{"type":"string","description":"Model override"}},"required":["resource_id","guidance"]}`)},
			{Name: "spec/amend", Description: "Signal spec update for resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`)},
			{Name: "spec/skip", Description: "Skip a failed resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"reason":{"type":"string","description":"Reason for skipping"}},"required":["resource_id"]}`)},
			{Name: "spec/finish", Description: "Finalize session, release lock", InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"force":{"type":"boolean","description":"Force finish even with incomplete resources"}},"required":["session_id"]}`)},
			{Name: "spec/status", Description: "Show current state", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
			{Name: "spec/log", Description: "List past applies", InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max entries to return"}}}`)},
			{Name: "spec/history", Description: "Show generation history for resource", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"limit":{"type":"integer","description":"Max entries to return"}},"required":["resource_id"]}`)},
			{Name: "spec/graph", Description: "Return dependency graph", InputSchema: json.RawMessage(`{"type":"object","properties":{"format":{"type":"string","description":"Output format (json, dot)"}}}`)},
			{Name: "spec/diff", Description: "Reconstruct state delta between applies", InputSchema: json.RawMessage(`{"type":"object","properties":{"apply_id_a":{"type":"string","description":"First apply ID"},"apply_id_b":{"type":"string","description":"Second apply ID"}}}`)},
			{Name: "spec/state", Description: "Inspect/modify state tracking", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"action":{"type":"string","description":"Action: get, set, clear"}}}`)},
			{Name: "spec/drift", Description: "Handle drifted resources", InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","description":"accept or revert"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["action","resource_id"]}`)},
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
}

// registerSpecTools adds all spec/* tool handlers backed by the spec engine.
func (s *Server) registerSpecTools() {
	// spec/plan
	s.addTool(toolDef{
		Name: "spec/plan", Description: "Show what would change (dry run)",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"},"filter":{"type":"string","description":"Resource filter pattern"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		result, err := s.spec.Plan(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("plan: %v", err))
		}
		return jsonResult(result.Actions)
	})

	// spec/apply
	s.addTool(toolDef{
		Name: "spec/apply", Description: "Execute the plan (async)",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"Target resource filter"},"force":{"type":"boolean","description":"Force regeneration"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		return s.runAsync("spec/apply", func(ctx context.Context) (string, error) {
			result, err := s.spec.Begin(ctx, specmod.BeginOpts{})
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(result)
			return string(b), nil
		}, progressToken)
	})

	// spec/validate
	s.addTool(toolDef{
		Name: "spec/validate", Description: "Check structural invariants",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"spec_dir":{"type":"string","description":"Spec directory path"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		result, err := s.spec.Validate(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("validate: %v", err))
		}
		return jsonResult(result)
	})

	// spec/begin
	s.addTool(toolDef{
		Name: "spec/begin", Description: "Start interactive agent session",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"Target resource filter"},"force":{"type":"boolean","description":"Force regeneration"},"model":{"type":"string","description":"Model override"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Target string `json:"target"`
			Force  bool   `json:"force"`
			Model  string `json:"model"`
		}
		json.Unmarshal(args, &p)
		result, err := s.spec.Begin(ctx, specmod.BeginOpts{Target: p.Target, Force: p.Force, Model: p.Model})
		if err != nil {
			return errorResult(fmt.Sprintf("begin: %v", err))
		}
		return jsonResult(result)
	})

	// spec/next
	s.addTool(toolDef{
		Name: "spec/next", Description: "Get next wave of uncommitted resources",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct{ SessionID string `json:"session_id"` }
		json.Unmarshal(args, &p)
		result, err := s.spec.Next(ctx, p.SessionID)
		if err != nil {
			return errorResult(fmt.Sprintf("next: %v", err))
		}
		return jsonResult(result)
	})

	// spec/context
	s.addTool(toolDef{
		Name: "spec/context", Description: "Get scoped prompt for a resource",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["session_id","resource_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			SessionID  string `json:"session_id"`
			ResourceID string `json:"resource_id"`
		}
		json.Unmarshal(args, &p)
		result, err := s.spec.Context(ctx, p.SessionID, p.ResourceID)
		if err != nil {
			return errorResult(fmt.Sprintf("context: %v", err))
		}
		return jsonResult(result)
	})

	// spec/validate-resource
	s.addTool(toolDef{
		Name: "spec/validate-resource", Description: "Run invariant checks for a resource",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct{ ResourceID string `json:"resource_id"` }
		json.Unmarshal(args, &p)
		result, err := s.spec.ValidateResource(ctx, p.ResourceID)
		if err != nil {
			return errorResult(fmt.Sprintf("validate-resource: %v", err))
		}
		return jsonResult(result)
	})

	// spec/note
	s.addTool(toolDef{
		Name: "spec/note", Description: "Save a design decision note",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"content":{"type":"string","description":"Note content"},"session_id":{"type":"string","description":"Session ID"}},"required":["resource_id","content"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			ResourceID string `json:"resource_id"`
			Content    string `json:"content"`
			SessionID  string `json:"session_id"`
		}
		json.Unmarshal(args, &p)
		if err := s.spec.Resolve(ctx, p.SessionID, p.ResourceID, p.Content, ""); err != nil {
			return errorResult(fmt.Sprintf("note: %v", err))
		}
		return jsonResult(map[string]bool{"saved": true})
	})

	// spec/commit
	s.addTool(toolDef{
		Name: "spec/commit", Description: "Record a resource as complete",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}},"notes":{"type":"string","description":"Design decision notes"}},"required":["session_id","resource_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			SessionID  string `json:"session_id"`
			ResourceID string `json:"resource_id"`
			Files      []struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			} `json:"files"`
			Notes string `json:"notes"`
		}
		json.Unmarshal(args, &p)
		var files []specmod.CommitFile
		for _, f := range p.Files {
			files = append(files, specmod.CommitFile{Path: f.Path, Content: f.Content})
		}
		if err := s.spec.Commit(ctx, p.SessionID, p.ResourceID, files, p.Notes); err != nil {
			return errorResult(fmt.Sprintf("commit: %v", err))
		}
		return jsonResult(map[string]bool{"committed": true})
	})

	// spec/resolve
	s.addTool(toolDef{
		Name: "spec/resolve", Description: "Provide guidance for blocked resource",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"guidance":{"type":"string","description":"Resolution guidance"},"model":{"type":"string","description":"Model override"}},"required":["resource_id","guidance"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			SessionID  string `json:"session_id"`
			ResourceID string `json:"resource_id"`
			Guidance   string `json:"guidance"`
			Model      string `json:"model"`
		}
		json.Unmarshal(args, &p)
		if err := s.spec.Resolve(ctx, p.SessionID, p.ResourceID, p.Guidance, p.Model); err != nil {
			return errorResult(fmt.Sprintf("resolve: %v", err))
		}
		return jsonResult(map[string]bool{"resolved": true})
	})

	// spec/amend
	s.addTool(toolDef{
		Name: "spec/amend", Description: "Signal spec update for resource",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["resource_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			SessionID  string `json:"session_id"`
			ResourceID string `json:"resource_id"`
		}
		json.Unmarshal(args, &p)
		if err := s.spec.Amend(ctx, p.SessionID, p.ResourceID); err != nil {
			return errorResult(fmt.Sprintf("amend: %v", err))
		}
		return jsonResult(map[string]bool{"amended": true})
	})

	// spec/skip
	s.addTool(toolDef{
		Name: "spec/skip", Description: "Skip a failed resource",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"resource_id":{"type":"string","description":"Resource identifier"},"reason":{"type":"string","description":"Reason for skipping"}},"required":["resource_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			SessionID  string `json:"session_id"`
			ResourceID string `json:"resource_id"`
			Reason     string `json:"reason"`
		}
		json.Unmarshal(args, &p)
		if err := s.spec.Skip(ctx, p.SessionID, p.ResourceID, p.Reason); err != nil {
			return errorResult(fmt.Sprintf("skip: %v", err))
		}
		return jsonResult(map[string]bool{"skipped": true})
	})

	// spec/finish
	s.addTool(toolDef{
		Name: "spec/finish", Description: "Finalize session, release lock",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"},"force":{"type":"boolean","description":"Force finish even with incomplete resources"}},"required":["session_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			SessionID string `json:"session_id"`
			Force     bool   `json:"force"`
		}
		json.Unmarshal(args, &p)
		result, err := s.spec.Finish(ctx, p.SessionID, p.Force)
		if err != nil {
			return errorResult(fmt.Sprintf("finish: %v", err))
		}
		return jsonResult(result)
	})

	// spec/status
	s.addTool(toolDef{
		Name: "spec/status", Description: "Show current state",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		result, err := s.spec.Status(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("status: %v", err))
		}
		return jsonResult(result)
	})

	// spec/log
	s.addTool(toolDef{
		Name: "spec/log", Description: "List past applies",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max entries to return"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct{ Limit int `json:"limit"` }
		json.Unmarshal(args, &p)
		result, err := s.spec.Log(ctx, p.Limit)
		if err != nil {
			return errorResult(fmt.Sprintf("log: %v", err))
		}
		return jsonResult(result)
	})

	// spec/history
	s.addTool(toolDef{
		Name: "spec/history", Description: "Show generation history for resource",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"limit":{"type":"integer","description":"Max entries to return"}},"required":["resource_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			ResourceID string `json:"resource_id"`
			Limit      int    `json:"limit"`
		}
		json.Unmarshal(args, &p)
		result, err := s.spec.History(ctx, p.ResourceID, p.Limit)
		if err != nil {
			return errorResult(fmt.Sprintf("history: %v", err))
		}
		return jsonResult(result)
	})

	// spec/graph
	s.addTool(toolDef{
		Name: "spec/graph", Description: "Return dependency graph",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"format":{"type":"string","description":"Output format (json, dot)"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		result, err := s.spec.GraphInfo(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("graph: %v", err))
		}
		return jsonResult(result)
	})

	// spec/diff
	s.addTool(toolDef{
		Name: "spec/diff", Description: "Reconstruct state delta between applies",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"apply_id_a":{"type":"string","description":"First apply ID"},"apply_id_b":{"type":"string","description":"Second apply ID"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		return textResult("diff not yet implemented")
	})

	// spec/state
	s.addTool(toolDef{
		Name: "spec/state", Description: "Inspect/modify state tracking",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","description":"Resource identifier"},"action":{"type":"string","description":"Action: get, set, clear"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		result, err := s.spec.Status(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("state: %v", err))
		}
		return jsonResult(result)
	})

	// spec/drift
	s.addTool(toolDef{
		Name: "spec/drift", Description: "Handle drifted resources",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","description":"accept or revert"},"resource_id":{"type":"string","description":"Resource identifier"}},"required":["action","resource_id"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		var p struct {
			Action     string `json:"action"`
			ResourceID string `json:"resource_id"`
		}
		json.Unmarshal(args, &p)
		if err := s.spec.DriftAction(ctx, p.Action, p.ResourceID); err != nil {
			return errorResult(fmt.Sprintf("drift: %v", err))
		}
		return jsonResult(map[string]bool{"ok": true})
	})

	// spec/vacuum
	s.addTool(toolDef{
		Name: "spec/vacuum", Description: "Compact old history",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"older_than":{"type":"string","description":"Age threshold (e.g. 30d)"}}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		return textResult("vacuum not yet implemented")
	})

	// spec/sql
	s.addTool(toolDef{
		Name: "spec/sql", Description: "Read-only SQLite shell",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"SQL query to execute"}},"required":["query"]}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		return textResult("sql not yet implemented")
	})

	// spec/unlock
	s.addTool(toolDef{
		Name: "spec/unlock", Description: "Force-clear stale lock",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
		if err := s.spec.Unlock(ctx); err != nil {
			return errorResult(fmt.Sprintf("unlock: %v", err))
		}
		return jsonResult(map[string]bool{"unlocked": true})
	})
}

// addTool registers a tool definition and its handler.
func (s *Server) addTool(def toolDef, handler toolHandler) {
	s.tools = append(s.tools, def)
	s.toolFns[def.Name] = handler
}
