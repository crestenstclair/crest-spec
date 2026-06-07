package mcp

import (
	"context"
	"encoding/json"
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
			"instructions": "crest-spec is a declarative code generation system. Use run_prompt for ad-hoc generation, code_review for multi-model review, bugbot for bug scanning. Use poll_result to check async job results.",
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

// handleResourcesList returns an empty resource list (implemented in SP3+).
func (s *Server) handleResourcesList(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{"resources": []any{}},
	}
}

// handleResourcesRead returns an error (no resources available yet).
func (s *Server) handleResourcesRead(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: -32602, Message: "no resources available yet"},
	}
}

// handlePromptsList returns an empty prompt list (implemented in SP4+).
func (s *Server) handlePromptsList(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{"prompts": []any{}},
	}
}

// handlePromptsGet returns an error (no prompts available yet).
func (s *Server) handlePromptsGet(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: -32602, Message: "no prompts available yet"},
	}
}
