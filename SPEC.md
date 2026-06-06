# claude-mcp — Feature & Implementation Spec

> **Note on naming.** This spec describes the server as wrapping the **`claude`
> CLI** (Claude Code). The project is referred to as `claude-mcp` throughout
> (module `github.com/crestenstclair/claude-mcp-server`, binary `claude-mcp`).

## 1. Overview

`claude-mcp` is a Go-based [Model Context Protocol](https://modelcontextprotocol.io)
server that wraps the `claude` CLI (Claude Code) and exposes its agentic
capabilities as MCP tools. Any MCP client — Claude Code itself, Cursor Desktop,
or any other MCP host — can drive Claude models (Opus, Sonnet, Haiku) through a
uniform tool interface.

The server's signature use case is local, multi-model **code review** and a
lightweight **Bugbot** pre-push scan: a developer can fan a diff out across
several Claude tiers/effort levels concurrently and get severity-ranked findings
in seconds, without pushing to CI.

Because a single `claude` invocation can take minutes, every long-running tool is
**asynchronous**: the tool call returns a job ID immediately, the work proceeds
in a background goroutine, and the result is collected later via a companion CLI
subcommand (`claude-mcp check job <id>`) or the `poll_result` tool. Job state is
persisted in a SQLite database so it survives across the stdio request/response
lifecycle and can be reconciled after crashes.

- **Module:** `github.com/crestenstclair/claude-mcp-server`
- **Go version:** 1.26.3
- **Server version reported over MCP:** `0.3.0`
- **MCP protocol version:** `2024-11-05`
- **Transport:** stdio (JSON-RPC over stdin/stdout) and Streamable HTTP (`POST /mcp` with SSE upgrade)
- **Wrapped CLI:** `claude` (Claude Code), `--print`/`-p` non-interactive mode
- **Platform assumptions:** Unix-like (uses `syscall.Kill`, process groups, `ps`); developed on macOS/Darwin.

---

## 2. Architecture

### 2.1 Component map

```
cmd/claude-mcp/main.go           Entrypoint + `check job` subcommand + flag/help handling
  └─ config.New()                envconfig (CLAUDE_MCP_ prefix)
  └─ jobs.NewStore(dbPath)       SQLite-backed job store (WAL mode)
  └─ agent.New(...)              claude CLI wrapper
  └─ mcp.New(...)                MCP server (shared dispatch, tools, jobs, metrics)
       ├─ StdioTransport         reads stdin, writes stdout
       ├─ HTTPTransport          net/http server, POST /mcp, SSE upgrade
       └─ app.New(srv).Run(ctx)  lifecycle wrapper (Run until ctx cancelled)

internal/
  agent/        Wraps the claude binary via os/exec; bootstrap installer
  app/          Minimal application lifecycle (New + Run)
  config/       envconfig-based configuration + usage/help text
  db/           sqlc-generated query code (DO NOT EDIT)
  errors/       Const error sentinel type (`type New string`)
  jobs/         SQLite job store: create/complete/fail/cancel, orphan cleanup, wait
  mcp/          JSON-RPC server, tool definitions, dispatch, async jobs, metrics, recursion guard
  mocks/        counterfeiter fakes for `runner` and `processTree` (committed)
migrations/     SQL schema, embedded via go:embed; applied at store startup
sql/queries/    sqlc query definitions (source for internal/db)
```

### 2.2 Dependency injection / interfaces

The codebase uses **package-private interfaces at the consumer** for testability:

- `mcp.runner` — the surface of `agent.Agent` the server depends on
  (`RunPrompt`, `Models`, `About`, `Status`, `MCPServers`, `MCPTools`). Faked by
  `mocks.FakeRunner`.
- `mcp.processTree` — abstraction over process-tree walking (`ParentProcess`,
  `SelfPID`). Real impl is `mcp.OSProcessTree`; faked by `mocks.FakeProcessTree`.
- `app.server` — anything with `Run(ctx) error`.

Mocks are generated with counterfeiter (`//go:generate` directives) and
committed under `internal/mocks/`.

### 2.3 Startup sequence (`main.go`)

1. **Subcommand check** — if `os.Args` is `check job <id>`, run `checkJob()` and exit (see §7.5).
2. **Help** — `-h`/`--help` prints usage + env var table (`config.Help()`), exits 0.
3. **Config** — `config.New()`; on error, print help and panic.
4. **Store** — `jobs.NewStore(dbPath())` where `dbPath()` is
   `~/.cache/claude-mcp/jobs.db`. `defer store.Close()`.
5. **Orphan cleanup** — `store.CleanupOrphans()` marks jobs whose owner PID is
   dead as `failed` (logged, non-fatal on error).
6. **Signal context** — `signal.NotifyContext(ctx, SIGINT, SIGTERM)`.
7. **Agent** — `agent.New(...)` from config.
8. **Transport setup** — stdio always starts; HTTP starts if `HTTPAddr` is set:
   - Build `mcp.New(agent, store, OSProcessTree{}, os.Stdin, os.Stdout, log, cfg)`.
   - `srv.SetBootstrap(...)` wires the bootstrap handler.
   - If `cfg.HTTPAddr != ""`, start the Streamable HTTP transport on that address.
   - `app.New(srv).Run(ctx)`.
   - On exit: if `ctx.Err() != nil` it was a graceful shutdown (log info);
     otherwise panic with the error.

`must[T]` is a generic helper that panics (via zerolog `Panic`, per convention —
not `Fatal`) on error.

---

## 3. Configuration (`internal/config`)

All env vars use the `CLAUDE_MCP_` prefix via
`envconfig.Process("CLAUDE_MCP", &cfg)`.

| Field          | Env var                     | Type            | Default            | Purpose |
|----------------|-----------------------------|-----------------|--------------------|---------|
| `APIKey`       | `CLAUDE_MCP_API_KEY`        | string          | (none)             | Passed to subprocess as `ANTHROPIC_API_KEY`. If unset, the child uses the developer's OAuth/keychain session. |
| `AgentPath`    | `CLAUDE_MCP_AGENT_PATH`     | string          | `claude`           | Path/name of the claude binary. |
| `DefaultModel` | `CLAUDE_MCP_DEFAULT_MODEL`  | string          | `claude-sonnet-4-6`| Model used when a call omits one. Accepts aliases (`opus`/`sonnet`/`haiku`) or full IDs. |
| `PermissionMode` | `CLAUDE_MCP_PERMISSION_MODE` | string        | `default`          | Default permission mode when a call omits `mode` (read-only/safe). |
| `Timeout`      | `CLAUDE_MCP_TIMEOUT`        | `time.Duration` | `0s`               | Default per-`RunPrompt` timeout; `0s` = no timeout. |
| `HTTPAddr`     | `CLAUDE_MCP_HTTP_ADDR`      | string          | (none)             | Listen address for Streamable HTTP transport (e.g., `:8080`). If unset, only stdio is active. |
| `MaxConcurrency` | `CLAUDE_MCP_MAX_CONCURRENCY` | int           | `5`                | Maximum concurrent `claude` subprocess spawns server-wide. |

`config.Help()` renders an aligned usage table to stderr using `tabwriter` +
`envconfig.Usagef`.

A separate env var `CLAUDE_CONFIG_DIR` (read directly in `agent.New`) overrides
the Claude Code config directory (default `~/.claude`); it governs where the
child process reads/writes `.claude.json`, credentials, and session state.

> There is no `TRUST` setting. Claude's workspace-trust dialog is automatically
> skipped in `--print` mode, so no `--trust` flag is needed.

---

## 4. The agent wrapper (`internal/agent`)

### 4.1 `Agent` struct

Holds `path`, `apiKey`, `defaultModel`, `permissionMode`, `timeout`, and
`configDir` (resolved to `~/.claude` or `$CLAUDE_CONFIG_DIR`). Constructed by
`New(path, apiKey, defaultModel, permissionMode, timeout)`; errors if `path` is
empty or home dir can't be resolved.

### 4.2 Data types

- `RunOpts` — Prompt, Model, Mode, Effort, Cwd, RelevantPaths, AddDirs,
  Continue, Resume, SessionID, Force, AllowedTools, AppendSystemPrompt.
- `RunResult` — Output, Stderr, Model, SessionID, DurationMS, NumTurns, CostUSD, IsError, `*Usage`.
- `Usage` — InputTokens, OutputTokens, CacheReadTokens, CacheCreationTokens.
- `Model` — ID, Name.
- `AboutInfo` — Version, Account, Subscription, AuthStatus, OS.
- `StatusInfo` — Output (raw text of `claude auth status`).

### 4.3 `RunPrompt` — the core invocation

Builds the `claude` argv:

- Always: `--print --output-format json`.
- `--model <model>` (call's model, else `defaultModel`).
- **Permission / mode mapping** (see §5.4): `--permission-mode <mode>` unless the
  effective mode is empty.
- `--effort <level>` if `Effort` set (`low`/`medium`/`high`/`max`).
- `--add-dir <cwd>` if `cwd` set, plus one `--add-dir` per entry in `AddDirs`.
- `--continue` if `Continue`; `--resume <id>` if `Resume`; `--session-id <uuid>`
  if `SessionID`.
- `--dangerously-skip-permissions` if `Force` (auto-approve everything; overrides
  `--permission-mode`).
- `--allowedTools <list>` if `AllowedTools` set.
- `--append-system-prompt <text>` if `AppendSystemPrompt` set.
- `--no-session-persistence` for parallel/fan-out tools so concurrent children
  don't contend on the session store (see §4.4).
- **Relevant paths** are not flags — they're prepended to the prompt text as
  `Relevant paths: a, b, c\n\n<prompt>`.
- The prompt is passed as the final positional arg for prompts ≤ 8 KB, or piped
  via **stdin** for prompts > 8 KB (conservative threshold to stay well within
  OS `ARG_MAX` limits after env vars and shell expansion).

Execution details:

- If `timeout > 0`, wraps ctx in `context.WithTimeout`.
- **Config isolation** — calls `isolatedConfigDir()` (see §4.4) and sets
  `CLAUDE_CONFIG_DIR` to the temp dir for the child; `defer cleanup()`.
- **Process group** — `SysProcAttr{Setpgid: true}` so the child gets its own
  process group; `cmd.Cancel` sends `SIGKILL` to the whole group
  (`syscall.Kill(-pid, SIGKILL)`); `cmd.WaitDelay = 5s`.
- Working dir set to `cwd` if given.
- Env: inherited + `CLAUDE_CONFIG_DIR`, plus `ANTHROPIC_API_KEY` if `apiKey` set.
- stdout/stderr captured into buffers.
- On error: still parses whatever stdout was produced, attaches Model + Stderr,
  and returns `(partialResult, wrappedError)` — callers can surface partial
  output. On success returns parsed result. Note: a JSON envelope with
  `is_error: true` (e.g. "Not logged in") is surfaced as an error even when the
  process exit code is 0.

### 4.4 Config-dir isolation (`isolatedConfigDir`)

Concurrent `claude` processes write the mutable `.claude.json` (project state,
startup bookkeeping) and the session store; without isolation, parallel
invocations race on these files. Each invocation therefore gets a fresh temp
config dir (`os.MkdirTemp` keyed by a UUID) that mirrors `~/.claude`:

- **Subdirectories** (e.g. `projects/`, `statsig/`) → symlinked (shared state).
- **`.claude.json`** (the mutating file) → **copied** so each process has its own
  writable instance.
- **Credentials / all other files** (e.g. `.credentials.json`) → **hard-linked**
  (shares the auth session), with a copy fallback if `os.Link` fails (e.g.,
  cross-device).
- Missing config dir is tolerated (returns the empty temp dir).
- `cleanup` is `os.RemoveAll(dir)`.

Fan-out tools additionally pass `--no-session-persistence` so children never
write resumable session files at all — belt-and-suspenders against contention.

### 4.5 Read-only / info commands

`Models`, `About`, `Status`, `MCPServers`, `MCPTools` use `exec(ctx, args...)`,
which applies the same isolation + process-group + env treatment as `RunPrompt`,
captures stdout, and returns it raw (or a stderr-wrapped error). Mapping to the
`claude` CLI:

| Method        | `claude` invocation                  | Notes |
|---------------|--------------------------------------|-------|
| `Status`      | `claude auth status`                 | Auth/subscription status. |
| `About`       | `claude --version` + `claude auth status` | Aggregated into `AboutInfo`. |
| `MCPServers`  | `claude mcp list`                    | Configured MCP servers (health-checked). |
| `MCPTools`    | `claude mcp get <name>`              | Details for one MCP server (claude has no per-server tool list; `get` is the closest surface). |
| `Models`      | *(none)*                             | `claude` has no `models` subcommand — `Models` returns a curated static list (see below). |

Parsers / helpers:

- `Models` — returns a curated, wrapper-maintained list of supported aliases and
  latest full IDs (e.g. `opus`→`claude-opus-4-8`, `sonnet`→`claude-sonnet-4-6`,
  `haiku`→`claude-haiku-4-5`). Not derived from CLI output.
- `parseAbout` — extracts the version from `claude --version` and account /
  subscription / auth lines from `claude auth status` into `AboutInfo`.
- `parseRunOutput` — parses the `claude --output-format json` result envelope:
  `result` (string), `session_id`, `is_error`, `num_turns`, `duration_ms`,
  `total_cost_usd`, and `usage` (snake_case fields: `input_tokens`,
  `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`).
  Falls back to treating raw stdout as the output if JSON parse fails.

### 4.6 Bootstrap (`bootstrap.go`)

`Bootstrap(ctx, agentPath)` installs the `claude` CLI when it's missing:

- If `exec.LookPath(agentPath)` succeeds → already installed; returns a message
  reminding the user to run `! claude auth login` if not authenticated.
- Otherwise installs via the official channel (`npm install -g
  @anthropic-ai/claude-code`, or the native installer script) with a 5-minute
  timeout; on failure returns the wrapped stderr; on success returns an install
  confirmation + login guidance.
- Returns `*BootstrapResult{Installed bool, Message string}`.

> The bootstrap only installs a CLI; it never provisions external accounts or
> hosting.

---

## 5. MCP server (`internal/mcp`)

### 5.1 Transports & concurrency

The server supports two transports, both backed by a shared `dispatch(ctx,
request) response` method that handles all JSON-RPC routing, tool dispatch, and
job management.

#### 5.1.1 Stdio transport

Always active. `Server.Run(ctx)`:

- Derives `serverCtx` from `ctx`.
- Reads stdin line-by-line with a `bufio.Scanner` whose buffer is grown to
  **10 MiB** to accommodate large requests.
- Each non-empty line is copied and dispatched in its **own goroutine** — so many
  requests can be in flight concurrently. A `sync.WaitGroup` tracks reader
  goroutines.
- Empty/blank lines skipped. JSON parse failures emit a `-32700 Parse error`.
- Run blocks on `select { scanErr | ctx.Done() }`.
- Progress notifications are written to stdout as JSON-RPC notification lines.

**Output serialization:** `writeResponse` marshals and writes a single line to
stdout under `outMu` (mutex), so concurrent responses don't interleave.

#### 5.1.2 Streamable HTTP transport

Active when `HTTPAddr` is configured. A `net/http` server (stdlib, no framework)
listening on the configured address.

- **Single endpoint:** `POST /mcp` — clients send JSON-RPC requests in the POST
  body.
- **Sync tools:** the response is a plain JSON-RPC response (no SSE upgrade).
- **Async tools:** the response is upgraded to an SSE stream. The server emits
  progress notifications as SSE events as they happen, then the final tool result,
  then closes the stream.
- **Session management:** stateless — each request is independent. Job IDs are the
  correlation mechanism, same as stdio. HTTP clients can also use `poll_result`
  for polling-style access instead of SSE streaming.
- **Shutdown:** `http.Server.Shutdown(ctx)` with the same 30s drain timeout as
  stdio.

Both transports share the same `Server` instance — same job store, metrics,
concurrency pool, and tool definitions.

**Server-wide shutdown:** when ctx is cancelled or stdin closes, it cancels
`serverCtx` and `bgCtx` (the background-job context), shuts down the HTTP server
if active, then waits up to **30s** for `asyncWg` (in-flight async jobs) to drain
before returning; logs a warning if it times out.

### 5.2 Request routing (`handleRequest`)

| Method                      | Handler                |
|-----------------------------|------------------------|
| `initialize`                | `handleInitialize`     |
| `notifications/initialized` | `handleInitialized`    |
| `tools/list`                | `handleToolsList`      |
| `tools/call`                | `handleToolCall`       |
| `resources/list`            | `handleResourcesList`  |
| `resources/read`            | `handleResourcesRead`  |
| `prompts/list`              | `handlePromptsList`    |
| `prompts/get`               | `handlePromptsGet`     |
| other                       | `-32601 Method not found` (notifications with nil ID are dropped) |

`handleInitialize` returns protocolVersion `2024-11-05`, capabilities
`{tools: {}, resources: {}, prompts: {}}`, serverInfo
`{name: claude-mcp, version: 0.3.0}`, and an `instructions` string telling the
client how to collect async results via `claude-mcp check job <id>`.

`handleInitialized` is a no-op — it logs that the session is fully established
and returns no response (it's a notification).

### 5.3 Tool-set selection at construction (`New`)

`New` validates non-nil runner/store/processTree/in/out, then **chooses which
tools to expose**:

1. **Recursion check** (`DetectRecursion`, §5.7). If a nested `claude` session is
   detected, expose **only** a single placeholder tool
   (`claude_mcp_only_available_in_top_level_session`) and set `recursion = true`.
2. Otherwise expose the full tool set (`buildToolDefinitions`), and additionally
   probe `runner.Status(ctx)`. If status fails (claude not installed / logged
   out), **prepend** the `bootstrap` tool so the client can self-heal.

`bgCtx`/`bgCancel` are created here for background jobs.

### 5.4 Permission / mode mapping

Incoming `mode` defaults to `ask`. The wrapper maps the three logical modes onto
`claude --permission-mode`:

| Logical `mode` | `claude` flag                       | Behavior |
|----------------|-------------------------------------|----------|
| `ask` (default) | `--permission-mode default`        | Read-only Q&A. In `--print` mode, edit/exec tools requiring approval are denied (no interactive prompt), so the workspace is not modified. |
| `plan`         | `--permission-mode plan`            | Read-only planning. |
| `agent`        | `--permission-mode acceptEdits`     | Full agent that may write files. |

`force: true` adds `--dangerously-skip-permissions`, which auto-approves
everything and overrides the permission mode. `effort` maps to `--effort`.

### 5.5 Tools

| Tool | Sync/Async | Notes |
|------|-----------|-------|
| `run_prompt` | **async** | Core single-prompt invocation. Default model: `claude-sonnet-4-6`. |
| `parallel_prompt` | **async** | Same/per-entry prompt across multiple models/efforts. |
| `code_review` | **async** | Multi-model review with a built-in default prompt. |
| `bugbot` | **async** | Lightweight severity-ranked pre-push review. |
| `list_models` | sync | Curated static model list. |
| `about` | sync | `claude --version` + `claude auth status`. |
| `status` | sync | `claude auth status`. |
| `list_mcp_servers` | sync | `claude mcp list`. |
| `list_mcp_tools` | sync | `claude mcp get <identifier>`. |
| `poll_result` | sync | Peek or consume a job result (see `consume` flag). |
| `cancel_job` | sync | Cancel a running job + kill subprocess group. |
| `list_jobs` | sync | List up to 50 recent non-deleted jobs. |
| `live_metrics` | sync | Self-monitoring snapshot. |
| `bootstrap` | sync | Only registered when startup `status` fails. |
| `claude_mcp_only_available_in_top_level_session` | (placeholder) | Only registered under recursion. |

### 5.6 Async job model (`startJob`)

The heart of the async design:

1. Generate a UUID job ID; capture `os.Getpid()` as the owner PID.
2. Create a `jobCtx` derived from `bgCtx` with its own cancel; register the cancel
   in `s.cancels[id]` (guarded by `cancelsMu`).
3. `store.Create(id, tool, pid)` — persist as `running`. On failure, unwind
   (cancel + delete from map) and return an error.
4. `asyncWg.Add(1)`; spawn a goroutine that:
   - defers `wg.Done`, `jobCancel`, and removal from `s.cancels`.
   - runs the job func, times it, records metrics.
   - **Outcome dispatch:** `err == nil` → `store.Complete(id, result)`;
     `jobCtx.Err() != nil` (cancelled) → `store.Cancel(id)`; else →
     `store.Fail(id, err)`.
5. Returns immediately a `textContent` instructing the caller to run
   `claude-mcp check job <id>` as a background command.

**Global concurrency:** All `runner.RunPrompt` calls acquire a slot from a shared
`chan struct{}` semaphore of size `MaxConcurrency` (default 5) before spawning the
subprocess, and release it when the subprocess exits. This replaces per-tool
semaphores. If all slots are full, new invocations queue and wait (respecting
`ctx` cancellation). A job can be `running` (goroutine alive, waiting for a slot)
before its subprocess actually starts.

**Progress notifications:** Each async exec func receives a
`progressFunc(phase, partialResult)` callback. The MCP dispatch layer wraps this
to emit `notifications/progress` if the client provided a `_meta.progressToken`.
For fan-out tools, progress phases are: job started (`progress: 0, total: N`),
model started, model finished (with partial result in `data` field), all complete,
then the final tool result. For single-model `run_prompt`, only start and
complete. If no progress token was provided, progress is tracked internally (for
`poll_result` peek) but no notifications are sent.

The four async exec funcs:

- **`execRunPrompt`** — single `runner.RunPrompt`; records a model-scoped metric
  (`run_prompt:<model>`); on error with stderr, builds a combined error string
  including partial output; on success appends a metadata footer (`formatRunMeta`:
  duration | model | session | tokens | cache | cost). Emits start/complete
  progress only.
- **`execParallelPrompt`** — runs each `prompts[]` entry concurrently, bounded by
  the global concurrency semaphore, honoring `ctx.Done()`; per-entry prompt
  override falls back to the shared `prompt`; metrics keyed
  `parallel_prompt:<label>`; results aggregated via `formatParallelResults`. Emits
  per-model start/finish progress with partial results.
- **`execCodeReview`** — like parallel_prompt but over `models[]`
  (default: `claude-opus-4-8`, `claude-sonnet-4-6`, `claude-haiku-4-5`) with a
  comprehensive built-in review prompt covering architecture, nil
  derefs/bounds/errors, performance, leaks, and concurrency, plus explicit
  multi-repo/worktree instructions. Metrics keyed `code_review:<model>`. Emits
  per-model progress with partial results.
- **`execBugbot`** — defaults to `["claude-haiku-4-5"]` (fast, lightweight); uses a
  strict pre-push prompt that demands per-finding `### Title`, severity
  (High/Medium/Low), a "Why" (runtime implications), and a "Remedy"; optional user
  `prompt` is appended as "Additional Focus Instructions". Metrics keyed
  `bugbot:<model>`. Emits per-model progress with partial results.

`modelLabel(id)` normalizes raw model IDs into short labels (`opus`, `sonnet`,
`haiku`, plus passthrough for unknown IDs) for display.

`formatParallelResults` renders each result as a `## <label>` section (error +
stderr + partial output, or output), and a one-line metadata summary per result
(`label: duration, N in / M out`).

### 5.7 Recursion guard (`recursion.go`)

`claude-mcp` can itself be invoked by a `claude` session that has the MCP server
configured — risking infinite recursion and runaway API spend.
`DetectRecursion` walks up the process tree from `SelfPID()`:

- For each ancestor, lower-cases the basename of the command; counts processes
  whose name contains `claude` but **not** `claude-mcp` and **not** `mcp`.
- If it finds **more than one** such `claude` process, returns `true`.
- Loop protection: a `visited` map and `pid > 1` guard prevent infinite loops on
  self-referential PIDs.

`OSProcessTree.ParentProcess` shells out to `ps -p <pid> -o ppid= -o comm=` with a
2s timeout. On detection, the server replaces all tools with the single
placeholder and `dispatch` refuses real work.

### 5.8 Metrics (`metrics.go`)

Lock-free per-tool counters using `atomic.Int64` for Calls, Errors, TotalNs,
MinNs, MaxNs (min/max via CAS loops). `Metrics` holds a `map[string]*toolMetric`
under an `RWMutex` and a start time. `snapshot()` (returned by `live_metrics`)
reports uptime, total calls/errors, and per-tool `{calls, errors, avg_ms, min_ms,
max_ms}`. Metric keys include model-scoped variants (`run_prompt:<model>`,
`code_review:<model>`, etc.).

### 5.9 Job-status tools

- **`poll_result`** — `store.Get`; `running` → "still running (Ns elapsed)";
  `failed`/`cancelled` → error text; `completed` → return result. If `consume`
  is `true` (default `false`), soft-delete the job after returning its result.
  When `consume` is `false`, the result can be read repeatedly. Unknown job →
  friendly "not found".
- **`cancel_job`** — `store.Cancel` (DB transition `running → cancelled`); if the
  job's cancel func is still registered, invoke it (kills the subprocess group via
  the agent's `cmd.Cancel`). Already-terminal/unknown → friendly message.
- **`list_jobs`** — lists up to 50 recent non-deleted jobs as
  `- <id>: <tool> (<status>, <elapsed>)`.

### 5.10 MCP Resources

Resources are read-only snapshots exposed via `resources/list` and
`resources/read`. They duplicate data available through tools, but let MCP clients
browse/subscribe without making tool calls — useful for dashboards and IDE
integrations.

| URI | Name | MIME type | Content |
|-----|------|-----------|---------|
| `claude-mcp://models` | Model List | `application/json` | Curated model aliases and full IDs |
| `claude-mcp://config` | Server Config | `application/json` | Current configuration (default model, concurrency cap, transports active) |
| `claude-mcp://jobs` | Recent Jobs | `application/json` | Up to 50 recent jobs with status and elapsed time |
| `claude-mcp://metrics` | Server Metrics | `application/json` | Live uptime, call counts, error rates, per-tool stats |

### 5.11 MCP Prompts

Prompts are templates exposed via `prompts/list` and `prompts/get`. Each returns a
`messages` array with fully-rendered system/user messages that the corresponding
tool would use. Clients can preview, customize, or use them directly with their
own model calls.

| Name | Description | Arguments |
|------|-------------|-----------|
| `code_review` | Built-in multi-model code review prompt | `focus?` (string) — optional area to emphasize |
| `bugbot` | Built-in pre-push bug scan prompt | `focus?` (string) — optional additional focus instructions |
| `review_with_focus` | General review template for custom review dimensions | `focus` (string, required), `severity?` (string) |

---

## 6. API Surface

This section is the contract reference: the wire API (JSON-RPC + tools), the CLI,
and the Go package API.

### 6.1 JSON-RPC methods (MCP wire protocol)

Transport is newline-delimited JSON-RPC 2.0 over stdio, and/or Streamable HTTP
(`POST /mcp` with SSE upgrade). Supported methods:

| Method                      | Params                                   | Result |
|-----------------------------|------------------------------------------|--------|
| `initialize`                | (client info; ignored)                   | `{protocolVersion, capabilities:{tools:{}, resources:{}, prompts:{}}, instructions, serverInfo:{name, version}}` |
| `notifications/initialized` | —                                        | (no response — notification) |
| `tools/list`                | —                                        | `{tools: [<tool definition>...]}` |
| `tools/call`                | `{name: string, arguments: object}`      | `{content: [{type:"text", text}], isError?: bool}` |
| `resources/list`            | —                                        | `{resources: [<resource definition>...]}` |
| `resources/read`            | `{uri: string}`                          | `{contents: [{uri, mimeType, text}]}` |
| `prompts/list`              | —                                        | `{prompts: [<prompt definition>...]}` |
| `prompts/get`               | `{name: string, arguments?: object}`     | `{messages: [{role, content}...]}` |

Error envelopes: `-32700` parse error, `-32601` method not found, `-32602`
invalid params. Tool-level failures are **not** JSON-RPC errors — they return a
normal result with `isError: true` and a JSON body `{status:"error", error:"…"}`.
Notifications (requests with `id: null`) receive no response.

**Progress notifications** (server → client, for async tools with a progress
token):
```json
{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"<from request>","progress":2,"total":3,"message":"sonnet complete","data":"<partial result>"}}
```

### 6.2 MCP Tools Reference

The canonical tool contract — this is the table to regenerate the server from.
`claude-mcp` exposes 13 first-class tools (plus `bootstrap` and the recursion
placeholder, which are registered conditionally).

| Tool Name | Description | Default Model |
|---|---|---|
| `bugbot` | Lightweight pre-push review catching critical bugs, races, and leaks; severity-ranked findings. Async (returns a job ID). | `claude-haiku-4-5` |
| `code_review` | Parallel, multi-model review comparing workspace changes to base branches. Async (returns a job ID). | Multi-model (`claude-opus-4-8`, `claude-sonnet-4-6`, `claude-haiku-4-5`) |
| `parallel_prompt` | Run a custom prompt concurrently across multiple specified models/efforts. Async (returns a job ID). | User-specified per entry |
| `run_prompt` | Run a single prompt/task via `claude`. Async (returns a job ID). | `claude-sonnet-4-6` |
| `list_models` | List the curated, supported model aliases and full IDs. | — |
| `about` | Local system info: version, account, subscription, auth status. | — |
| `status` | Authentication / subscription status (`claude auth status`). | — |
| `list_mcp_servers` | List MCP servers configured in `claude` (`claude mcp list`). | — |
| `list_mcp_tools` | Get details for one configured MCP server (`claude mcp get`). | — |
| `poll_result` | Check a job's status; optionally consume its result (`consume` flag). | — |
| `cancel_job` | Cancel a running job and kill its subprocess group. | — |
| `list_jobs` | List up to 50 recent jobs with status and elapsed time. | — |
| `live_metrics` | Live latency, throughput, and error rates of this MCP server. | — |
| `bootstrap` | Install `claude` and guide login. *Only registered when startup `status` fails.* | — |
| `claude_mcp_only_available_in_top_level_session` | Placeholder shown when recursion is detected; all real tools disabled. *Only registered under recursion.* | — |

### 6.3 Tool catalog (input → output)

Every tool result is wrapped as MCP text content. "Output shape" below describes
the `text` payload(s).

#### Async tools (return a job ID immediately)

**`run_prompt`** — run a single `claude` prompt.
- Input: `prompt` *(required, string)*, `model` (default `claude-sonnet-4-6`),
  `mode` (`ask|plan|agent`, default `ask`), `effort` (`low|medium|high|max`),
  `cwd`, `relevant_paths` (`string[]`), `add_dirs` (`string[]`), `continue`
  (bool), `resume` (string), `session_id` (string), `force` (bool),
  `allowed_tools` (`string[]`), `append_system_prompt` (string).
- Output: `"Job <id> started for run_prompt. … claude-mcp check job <id>"`.

**`parallel_prompt`** — same/per-entry prompt across models in parallel.
- Input: `prompts` *(required)*: array of `{label *(req)*, model *(req)*, prompt?}`;
  shared `prompt`, `cwd`, `relevant_paths`, `mode`, `force`.
- Output: job-started message. Final result (via `check job`) is a `## <label>`
  section per entry + a metadata line.

**`code_review`** — parallel multi-model review vs. base branches.
- Input: `prompt?` (override/focus), `models?` (`string[]`, default
  `[claude-opus-4-8, claude-sonnet-4-6, claude-haiku-4-5]`), `cwd`,
  `relevant_paths`, `mode` (default `ask`), `force`.
- Output: job-started message → aggregated multi-model review.

**`bugbot`** — lightweight severity-ranked pre-push review.
- Input: `prompt?` (appended focus), `models?` (default `[claude-haiku-4-5]`),
  `cwd`, `relevant_paths`, `mode` (default `ask`), `force`.
- Output: job-started message → findings (`### Title`, severity, Why, Remedy).

#### Synchronous tools

| Tool | Input | Output |
|------|-------|--------|
| `list_models` | — | `{status:"success", models:[{id,name}…]}` |
| `about` | — | `{status:"success", info: AboutInfo}` |
| `status` | — | `{status:"success", info: StatusInfo}` |
| `list_mcp_servers` | — | `{status:"success", output: <claude mcp list>}` |
| `list_mcp_tools` | `identifier` *(req)* | `{status:"success", output: <claude mcp get>}` |
| `poll_result` | `job_id` *(req)*, `consume?` (bool, default `false`) | running/elapsed text, or result/error text; soft-deletes if `consume: true` |
| `cancel_job` | `job_id` *(req)* | confirmation text |
| `list_jobs` | — | newline list `- <id>: <tool> (<status>, <elapsed>)` |
| `live_metrics` | — | `{status, uptime_sec, total_calls, total_errors, tools:[ToolStats…]}` |
| `bootstrap` | — | `BootstrapResult{installed, message}` *(only when registered)* |

### 6.4 CLI surface (`cmd/claude-mcp`)

| Invocation | Behavior |
|------------|----------|
| `claude-mcp` | Run as a stdio MCP server (default). |
| `claude-mcp check job <id>` | Block until job `<id>` completes; print result to stdout (exit 0) or error to stderr (exit 1); consume the job. SIGINT/SIGTERM-aware. |
| `claude-mcp -h` / `--help` | Print usage + env-var table; exit 0. |

### 6.5 Go package API (exported identifiers)

| Package | Exported surface |
|---------|------------------|
| `internal/config` | `Config` (APIKey, AgentPath, DefaultModel, PermissionMode, Timeout, HTTPAddr, MaxConcurrency); `New() (*Config, error)`; `Help() error` |
| `internal/agent` | `Agent`; `New(path, apiKey, defaultModel, permissionMode, timeout) (*Agent, error)`; methods `RunPrompt`, `Models`, `About`, `Status`, `MCPServers`, `MCPTools`; types `RunOpts`, `RunResult`, `Usage`, `Model`, `AboutInfo`, `StatusInfo`; `Bootstrap(ctx, agentPath) (*BootstrapResult, error)`; `BootstrapResult` |
| `internal/jobs` | `Store`; `NewStore(dbPath) (*Store, error)`; `Job`; `ErrUnknownJob`; methods `Create`, `Complete`, `Fail`, `Get`, `List`, `Cancel`, `Delete`, `CleanupOrphans`, `WaitForCompletion`, `Close` |
| `internal/mcp` | `Server`; `New(runner, store, processTree, in, out, log, cfg) (*Server, error)`; `(*Server).Run(ctx) error`; `(*Server).SetBootstrap(BootstrapFunc)`; `BootstrapFunc`; `OSProcessTree`; `DetectRecursion(processTree) (bool, error)`; `Metrics`, `ToolStats`; sentinels `ErrNilRunner`, `ErrNilReader`, `ErrNilWriter` |
| `internal/app` | `App`; `New(server) (*App, error)`; `(*App).Run(ctx) error`; `ErrNilServer` |
| `internal/errors` | `New` (string sentinel type); `Is`, `As` |
| `internal/db` | sqlc-generated `Queries`, params/row structs (do not edit) |
| `migrations` | `FS embed.FS` (embedded `.sql` files) |

Internal-only interfaces (unexported, for DI): `mcp.runner`, `mcp.processTree`,
`app.server`.

---

## 7. Job store (`internal/jobs`) & persistence

### 7.1 Storage

- SQLite via `modernc.org/sqlite` (pure-Go, CGO-free) at
  `~/.cache/claude-mcp/jobs.db`.
- PRAGMAs at open: `journal_mode=WAL`, `busy_timeout=5000`.
- Migrations: SQL files embedded via `migrations.FS` (go:embed), applied in
  filename order, tracked in a `schema_migrations(filename)` table; each applied
  transactionally. (`001_jobs.sql` creates the `jobs` table.)
- Queries are sqlc-generated into `internal/db/` (do not hand-edit); query source
  is `sql/queries/jobs.sql`. A couple of dynamic UPDATEs (`Cancel`,
  `CleanupOrphans`) use raw `conn.Exec` because they're conditional on status.

### 7.2 `jobs` table

| Column      | Type    | Notes |
|-------------|---------|-------|
| `id`        | TEXT PK | UUID |
| `tool`      | TEXT    | originating tool name |
| `status`    | TEXT    | `running` / `completed` / `failed` / `cancelled` / `deleted` (default `running`) |
| `result`    | TEXT    | populated on completion |
| `error`     | TEXT    | populated on failure |
| `pid`       | INTEGER | owner process PID |
| `started_at`| TEXT    | RFC3339Nano UTC |
| `done_at`   | TEXT    | RFC3339Nano UTC |

### 7.3 Store API

`Create`, `Complete`, `Fail`, `Cancel` (only affects `running` rows; 0 rows →
`ErrUnknownJob`), `Get` (excludes `deleted`; `sql.ErrNoRows → ErrUnknownJob`),
`List` (≤50, newest first, excludes `deleted`), `Delete` (soft delete),
`CleanupOrphans`, `WaitForCompletion`, `Close`.

State transitions are guarded by `AND status = 'running'` clauses so terminal
states are immutable. Times are stored as RFC3339Nano strings and parsed
tolerantly (`parseTime` tries RFC3339Nano then RFC3339).

### 7.4 Orphan detection & liveness

`processAlive(pid)` uses `syscall.Kill(pid, 0)` (alive if no error or `EPERM`).

- **`CleanupOrphans`** (run at startup and by the `check job` subcommand) finds
  distinct PIDs of `running` jobs and marks any whose owner process is dead as
  `failed` with error `process exited`.
- **`WaitForCompletion(ctx, id)`** polls with exponential backoff (100ms initial,
  doubling each poll, capped at 2s): returns the job once it leaves `running`; if
  the owner PID has died mid-wait it marks the job failed and returns an "orphaned"
  error; tolerates up to 5 consecutive transient DB errors before giving up;
  respects `ctx` cancellation.

### 7.5 The `check job` subcommand (`main.go`)

`claude-mcp check job <id>` is the recommended result collector — run by the MCP
client as a **background Bash command**:

1. Open the store, run `CleanupOrphans`.
2. `WaitForCompletion(ctx, id)` (blocks, SIGINT/SIGTERM-aware).
3. `completed` → print `result` to stdout, soft-delete the job, exit `0`.
4. `failed`/`cancelled` → print status+error to stderr, soft-delete, exit `1`.
5. anything else → error to stderr, exit `1`.

This decouples long jobs from the synchronous stdio request loop: the model fires
a tool, gets a job ID, launches `check job` in the background, and is notified
when it completes.

---

## 8. Lifecycle & robustness

- **app.New() + Run(ctx)** — minimal lifecycle wrapper (`internal/app`).
- **Graceful shutdown** — SIGINT/SIGTERM via `signal.NotifyContext`; HTTP server
  (if active) shuts down via `http.Server.Shutdown(ctx)`; server drains in-flight
  jobs up to 30s; `cmd.Cancel` + process groups + `WaitDelay` ensure subprocess
  trees are SIGKILLed rather than leaked.
- **Global concurrency** — a `chan struct{}` semaphore of size `MaxConcurrency`
  (default 5) ensures at most N `claude` subprocesses run simultaneously across
  all tools and transports.
- **Process groups** — every subprocess runs with `Setpgid: true` and is killed via
  `kill(-pid, SIGKILL)` so children (claude's own subprocesses, e.g. MCP servers
  and shells) die too.
- **Config isolation** — prevents concurrent `claude` processes from corrupting
  `~/.claude/.claude.json` or contending on the session store.
- **Logging** — zerolog to stderr with timestamps; `Panic` (not `Fatal`) per
  convention so deferred cleanup runs.
- **Crash recovery** — persistent SQLite job state + PID liveness reconciliation
  means stale `running` jobs from a previous (dead) process are cleaned up on next
  start.

---

## 9. Build, tooling & conventions

### 9.1 Make targets

| Target | Action |
|--------|--------|
| `make` / `make all` | `fmt test lint` |
| `make build` | build to `bin/claude-mcp` |
| `make install` | `go install ./cmd/claude-mcp` |
| `make test` | `go test ./...` |
| `make fmt` | `go fmt` + goimports-reviser |
| `make mocks` | regenerate counterfeiter fakes (`mcp.runner`, `mcp.processTree`) |
| `make sqlc` | `sqlc generate` |
| `make lint` / `lint-fix` | golangci-lint (v1.64.8) |
| `make update` | `go get -u` + `go mod tidy` |

### 9.2 Conventions (inherited from `go-app`)

- `app.New() + Run(ctx)` lifecycle.
- `envconfig` config with a service prefix (`CLAUDE_MCP_`).
- zerolog structured logging; `Panic` not `Fatal`.
- SIGINT + SIGTERM graceful shutdown.
- Package-private interfaces for DI; counterfeiter mocks committed.
- sqlc for type-safe SQLite; queries in `sql/queries/`, schema in `migrations/`.
- Const error sentinels via `type New string` (`internal/errors`).
- Table-driven tests (error cases first, success last); testify `require` for
  guards, `assert` for multi-field checks.
- golangci-lint with gci import ordering.

### 9.3 Tests

- `internal/agent/agent_test.go` — parser tests (`parseAbout`, run-output JSON
  envelope parsing, model-list).
- `internal/mcp/server_test.go` — table-driven server/dispatch tests using
  `FakeRunner`.
- `internal/mcp/recursion_test.go` — recursion detection: no-recursion,
  multi-`claude` detection, mcp/exclusions, and self-loop protection.

### 9.4 Key dependencies

`github.com/google/uuid`, `github.com/kelseyhightower/envconfig`,
`github.com/rs/zerolog`, `github.com/stretchr/testify`, `modernc.org/sqlite`
(CGO-free SQLite).

---

## 10. Client integration

Install: `go install github.com/crestenstclair/claude-mcp-server/cmd/claude-mcp@latest`
(ensure `~/go/bin` is on `PATH`).

**Stdio client config** (e.g. `~/.mcp.json`):

```json
{
  "mcpServers": {
    "claude": { "command": "claude-mcp", "args": [] }
  }
}
```

Auth: uses the developer's existing local Claude Code session (`~/.claude`
OAuth/keychain); no API key required unless `CLAUDE_MCP_API_KEY` is set (passed
through as `ANTHROPIC_API_KEY`). If `claude` is missing or logged out, the server
auto-exposes the `bootstrap` tool, which installs the CLI and instructs the user
to run `! claude auth login`.

**Streamable HTTP client config** — for remote/web clients, start the server with
`CLAUDE_MCP_HTTP_ADDR=:8080` and connect to `http://localhost:8080/mcp` via any
MCP client that supports Streamable HTTP transport.

Typical stdio flow:
1. Client calls e.g. `bugbot` → gets a job ID.
2. Client runs `claude-mcp check job <id>` as a background command.
3. On completion the result (review text + per-model metadata footer) is printed
   and the job is consumed.

Typical Streamable HTTP flow:
1. Client POSTs `tools/call` to `/mcp` for an async tool → response upgrades to
   SSE.
2. Client receives progress notifications as SSE events (per-model start/finish
   with partial results).
3. Final tool result arrives as the last SSE event; stream closes.

---

## 11. Notable design decisions & caveats

- **Everything long-running is async + persisted.** This sidesteps stdio timeouts
  and lets a single agent fire several reviews in parallel while staying
  responsive.
- **`relevant_paths` are injected into the prompt**, not passed as flags — a
  deliberate choice so any model can "orient" itself; `add_dirs` separately maps
  to `--add-dir` for actual tool file access.
- **`ask`/`plan`/`agent` map onto `--permission-mode`.** Read-only `default` is the
  safe default everywhere; write access is opt-in via `agent`/`force`.
- **`claude` has no `models` subcommand**, so `list_models` returns a
  wrapper-curated list that must be kept current as new models ship.
- **Multi-model means multi-tier here.** Unlike a multi-vendor setup, fan-out tools
  vary the Claude model tier (`opus`/`sonnet`/`haiku`) and/or `--effort`.
- **Result consumption is opt-in.** `poll_result` defaults to peek (non-destructive);
  pass `consume: true` to soft-delete after reading. `check job` always consumes.
- **Recursion guard relies on `ps` and process names** containing `claude`; it's
  Unix-specific and name-heuristic based, and excludes `claude-mcp`/`mcp` names.
- **Platform-specific.** Process groups, `syscall.Kill`, and `ps` assume a
  Unix-like OS; there is no Windows path.
- **`is_error` vs. exit code.** `claude --output-format json` can return an error
  envelope (e.g. "Not logged in") with a 0 exit code; the wrapper treats
  `is_error: true` as a failure.
- **Dual transport.** Stdio always runs (required for MCP hosts that launch the
  server as a subprocess). Streamable HTTP is additive — activated by setting
  `CLAUDE_MCP_HTTP_ADDR`. Both share one `Server` instance.
- **Global concurrency cap** (default 5) prevents resource exhaustion when
  multiple fan-out tools run concurrently. The cap is per-server, not per-tool.
- **Progress notifications with partial results** let clients render incremental
  findings as each model finishes, rather than waiting for the full aggregation.
  The `data` field in progress notifications is an extension beyond the base MCP
  progress spec.
- **`run_prompt` defaults to Sonnet**, not Opus. The orchestrator (the MCP client)
  is typically running on Opus already; the subprocess should be a cheaper model
  by default. Callers who want Opus can request it explicitly.
- **Prompts ≤ 8 KB** are passed as positional args; larger prompts are piped via
  stdin. This threshold is conservative relative to OS `ARG_MAX` limits but
  avoids edge cases with shell expansion and environment variable size.
