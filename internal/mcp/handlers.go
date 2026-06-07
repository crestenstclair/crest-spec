package mcp

import (
	"context"
	"encoding/json"

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
			"instructions": `crest-spec is a declarative code generation system. YOU are the orchestrator.

## Spec workflow — agent-driven generation

To generate code from a spec, drive this pipeline yourself. Do NOT use spec_apply — it is unattended mode and gives you no control.

1. spec_plan          → see what needs generating
2. spec_begin         → start a session, get session_id
3. spec_next          → get the next wave of resources (respects dependency order)
4. For each resource in the wave:
   a. spec_context    → get the scoped prompt + system_prompt for this resource
   b. run_prompt      → dispatch the prompt to a Claude sub-agent (async, returns job_id)
   c. poll_result     → retrieve the generated output when ready
   d. Parse the output: extract code blocks with "// path:" annotations
   e. spec_commit     → commit the parsed files for this resource
   f. If generation fails or output is bad: fix the prompt and retry, or spec_skip
5. Repeat step 3 until spec_next returns done=true
6. spec_finish        → finalize the session

You can run multiple run_prompt calls in parallel for resources within the same wave.
Review the generated code before committing — you are the quality gate.

## Other tools
- run_prompt: ad-hoc generation (not tied to a spec session)
- code_review: multi-model code review
- bugbot: lightweight bug scan
- poll_result: check async job status (use with run_prompt, code_review, bugbot)
- cancel_job: kill a running async job`,
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

	var progressToken string
	if tcp.Meta != nil {
		progressToken = tcp.Meta.ProgressToken
	}

	result := handler(ctx, tcp.Arguments, progressToken)
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

	case "orchestrator_instructions":
		instructions := "You are a dispatcher, not a code generator. Do not write code yourself.\nFor each resource: call spec/context to get its prompt, then call run_prompt with that prompt."
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
			"messages": []map[string]string{{"role": "user", "content": instructions}},
		}}

	default:
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "unknown prompt: " + p.Name}}
	}
}
