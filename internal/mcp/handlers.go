package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
)

// handleInitialize returns the MCP protocol initialization response.
func (s *Server) handleInitialize(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
				"prompts":   map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "crest-spec",
				"version": "0.1.0",
			},
			"instructions": `crest-spec is a declarative code generation system. The MCP server is a pure
spec state engine — it never calls an LLM and never spawns subprocesses.
YOU (Claude Code) are the orchestrator and run all generation with your own
sub-agents/workflows. Default generation model: sonnet.

## Spec workflow — native orchestration

1. spec/plan            → see what needs generating
2. spec/begin           → start a session (returns session_id, plan, waves, pending destroys)
3. spec/confirm_destroys → if PendingDestroys is non-empty, confirm deletions
4. spec/next            → next wave of resources (dependency order)
5. For each resource in the wave (parallelize across the wave):
   a. spec/context      → scoped prompt + system_prompt + project invariants
   b. Generate with a sub-agent (sonnet) using that prompt
   c. Judge each returned invariant against the generated files (pass/fail + summary)
   d. spec/commit       → files + notes + model + invariant_checks
                          The server writes files and runs the resource's
                          mechanical validations (compile/test/custom).
                          A failed validation or a failed invariant verdict
                          rejects the commit.
   e. If Committed=false: call spec/context again (it now includes the
      failure), regenerate, re-commit — up to max_retries. Then
      spec/resolve (guidance) or spec/skip.
6. Repeat from step 4 until spec/next returns done=true
7. spec/finish          → finalize; if reflection_prompt is non-empty, run it
                          with a sub-agent and submit via spec/record_learnings

The core loop is generate → commit → validate → retry-with-feedback.
Never write implementation code in the orchestrator context — every file
comes from a sub-agent. You are the quality gate: review before committing.

## Self-improvement
- spec/evolve + spec/record_learnings: distill learnings from failures
- spec/learnings + spec/promote_learnings: inspect and promote learnings`,
		},
	}
}

// handleInitialized is a no-op acknowledgment.
func (s *Server) handleInitialized(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{},
	}
}

// handleToolsList returns all registered tool definitions.
func (s *Server) handleToolsList(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{"tools": s.tools},
	}
}

// handleToolCall dispatches a tool call to its handler.
func (s *Server) handleToolCall(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	var tcp toolCallParams
	if err := json.Unmarshal(params, &tcp); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32602, Message: "Invalid params: " + err.Error()},
		}
	}

	handler, ok := s.toolFns[tcp.Name]
	if !ok {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result:  errorResult("unknown tool: " + tcp.Name),
		}
	}

	start := time.Now()
	result := handler(ctx, tcp.Arguments)
	var recordErr error
	if result.IsError {
		recordErr = errors.New("tool error")
	}
	s.metrics.Record(tcp.Name, time.Since(start), recordErr)
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

// handleResourcesList returns the list of available MCP resources.
func (s *Server) handleResourcesList(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	resources := []map[string]string{
		{"uri": "crest-spec://plan", "name": "Current Plan", "mimeType": "application/json"},
		{"uri": "crest-spec://state", "name": "Spec State", "mimeType": "application/json"},
		{"uri": "crest-spec://graph", "name": "Dependency Graph", "mimeType": "application/json"},
		{"uri": "crest-spec://session", "name": "Active Session", "mimeType": "application/json"},
		{"uri": "crest-spec://metrics", "name": "Server Metrics", "mimeType": "application/json"},
	}
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{"resources": resources},
	}
}

// handleResourcesRead reads a specific MCP resource by URI.
func (s *Server) handleResourcesRead(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: err.Error()}}
	}

	if s.spec == nil {
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32603, Message: "spec engine not initialized"}}
	}

	var content any
	var readErr error

	switch p.URI {
	case "crest-spec://plan":
		result, err := s.spec.Plan(ctx)
		if err != nil {
			readErr = err
		} else {
			content = result.Actions
		}
	case "crest-spec://state":
		result, err := s.spec.Status(ctx)
		if err != nil {
			readErr = err
		} else {
			content = result
		}
	case "crest-spec://graph":
		result, err := s.spec.GraphInfo(ctx)
		if err != nil {
			readErr = err
		} else {
			content = result
		}
	case "crest-spec://session":
		result, err := s.spec.Status(ctx)
		if err != nil {
			readErr = err
		} else {
			content = result.Session
		}
	case "crest-spec://metrics":
		content = s.metrics.Snapshot()
	default:
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "unknown resource: " + p.URI}}
	}

	if readErr != nil {
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32603, Message: readErr.Error()}}
	}

	b, _ := json.Marshal(content)
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"contents": []map[string]any{
				{"uri": p.URI, "mimeType": "application/json", "text": string(b)},
			},
		},
	}
}

// handlePromptsList returns the list of available MCP prompts.
func (s *Server) handlePromptsList(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	prompts := []map[string]any{
		{"name": "system_prompt", "description": "The system prompt for sub-agents"},
		{"name": "resource_prompt", "description": "Full resource prompt for a specific resource", "arguments": []map[string]string{{"name": "resource_id", "description": "Resource identifier", "required": "true"}}},
		{"name": "orchestrator_instructions", "description": "Orchestrator protocol instructions"},
	}
	return jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{"prompts": prompts}}
}

// handlePromptsGet retrieves a specific MCP prompt by name.
func (s *Server) handlePromptsGet(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: err.Error()}}
	}

	if s.spec == nil {
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32603, Message: "spec engine not initialized"}}
	}

	switch p.Name {
	case "system_prompt":
		result, err := s.spec.Plan(ctx)
		if err != nil {
			return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32603, Message: err.Error()}}
		}
		prompt := promptpkg.BuildSystemPrompt(result.Registry.Project)
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
			"messages": []map[string]string{{"role": "user", "content": prompt}},
		}}

	case "resource_prompt":
		resourceID := p.Arguments["resource_id"]
		if resourceID == "" {
			return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "resource_id argument required"}}
		}
		result, err := s.spec.Plan(ctx)
		if err != nil {
			return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32603, Message: err.Error()}}
		}
		resource, ok := result.Registry.Resources[resourceID]
		if !ok {
			return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "resource not found: " + resourceID}}
		}
		sysPrompt := promptpkg.BuildSystemPrompt(result.Registry.Project)
		resPrompt := promptpkg.BuildResourcePrompt(resource, result.Registry)
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
			"messages": []map[string]string{
				{"role": "user", "content": sysPrompt + "\n\n" + resPrompt},
			},
		}}

	case "orchestrator_instructions":
		instructions := "You are the orchestrator. For each resource: spec/context → generate with a sub-agent (sonnet) → judge the returned invariants → spec/commit with files + invariant_checks. On Committed=false, re-call spec/context (it includes the failure) and retry."
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
			"messages": []map[string]string{{"role": "user", "content": instructions}},
		}}

	default:
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "unknown prompt: " + p.Name}}
	}
}
