# claude-mcp — Spec Refinement Design

> Design document capturing all refinements to SPEC.md agreed upon during brainstorming.
> This is the authoritative record of changes; SPEC.md itself will be updated in-place.

## Summary of Changes

### Factual Corrections
- **Module path** → `github.com/crestenstclair/claude-mcp-server`
- **Effort levels** → `low | medium | high | max` (drop `xhigh`)
- **Default model for `run_prompt`** → `claude-sonnet-4-6` (was `claude-opus-4-8`)
- **Config `DefaultModel`** → `claude-sonnet-4-6` to match
- **Server version** → `0.3.0`

### New: Streamable HTTP Transport
- New config field: `HTTPAddr` (`CLAUDE_MCP_HTTP_ADDR`, string, default none)
- `Mode` env var removed; stdio always starts; HTTP starts if `HTTPAddr` is set
- Single endpoint: `POST /mcp` — inline JSON-RPC response for sync tools, SSE upgrade for async
- Stateless sessions; job IDs are the correlation mechanism
- Shared `Server` instance across both transports
- Extract dispatch logic into shared `dispatch(ctx, request) response` method
- `net/http` from stdlib, no framework
- Graceful shutdown via `http.Server.Shutdown(ctx)` with 30s drain

### New: Progress Notifications (Phased + Partial Results)
- MCP `notifications/progress` support for all async jobs
- Requires `_meta.progressToken` from client; no token = no notifications (but state tracked internally for poll_result peek)
- Fan-out tools emit: job started → model X started → model X finished (with partial result in `data` field) → all complete → final result
- Single-model `run_prompt` emits: started → complete
- Stdio: notifications written to stdout as JSON-RPC notifications
- HTTP: notifications sent as SSE events on open response stream
- Async exec funcs receive a `progressFunc(phase, partialResult)` callback

### Changed: Tool Modifications
- `run_prompt` default model → `claude-sonnet-4-6`
- `poll_result` gains optional `consume` parameter (bool, default `false`)
  - `false`: peek — returns status/result without deleting; repeatable
  - `true`: returns result and soft-deletes (current behavior)
  - `check job` CLI always consumes
- Effort levels: `low | medium | high | max` everywhere (drop `xhigh`)

### New: MCP Resources
- `resources/list` and `resources/read` handlers
- Resources:
  - `claude-mcp://models` — curated model aliases and full IDs (application/json)
  - `claude-mcp://config` — current server configuration (application/json)
  - `claude-mcp://jobs` — up to 50 recent jobs with status (application/json)
  - `claude-mcp://metrics` — live server metrics (application/json)

### New: MCP Prompts
- `prompts/list` and `prompts/get` handlers
- Prompts:
  - `code_review` — built-in review prompt; args: `focus?` (string)
  - `bugbot` — built-in bug scan prompt; args: `focus?` (string)
  - `review_with_focus` — general review template; args: `focus` (required), `severity?`
- Each returns `messages` array with fully-rendered system/user messages

### Changed: Capabilities
- `initialize` response capabilities expand to `{tools: {}, resources: {}, prompts: {}}`
- `notifications/initialized` handled as no-op (logged, session considered established)

### New: Global Concurrency Limit
- New config field: `MaxConcurrency` (`CLAUDE_MCP_MAX_CONCURRENCY`, int, default `5`)
- `chan struct{}` semaphore shared across all async exec funcs
- Enforced at `runner.RunPrompt` call site (before subprocess spawn)
- Replaces per-tool semaphore of 10 in `parallel_prompt`
- Progress notifications include "waiting for slot" state

### Changed: Job Store
- **Remove auto-prune.** No `pruneDeleted()` on startup. Soft-deleted jobs remain until manual cleanup or DB deletion.
- **Exponential backoff for `WaitForCompletion`.** 100ms initial, double each poll, cap at 2s. ~12 polls for a 30s job vs. ~120 with fixed 250ms.

### Defined: Prompt Size Threshold
- Prompts ≤ 8 KB passed as positional arg to `claude`
- Prompts > 8 KB piped via stdin

## Decisions Preserved (No Change)
- Go version 1.26.3
- `code_review` and `bugbot` as separate tools (UX clarity)
- `force: true` stays as-is
- No cost budgeting (handled at subscription level)
- Architecture: `app` package, constructor side-effects in `mcp.New()`, `CLAUDE_CONFIG_DIR` outside config struct — all kept as-is
