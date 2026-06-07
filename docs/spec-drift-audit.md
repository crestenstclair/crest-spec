# SPEC.md Drift Audit

14-section audit of SPEC.md claims vs actual codebase. 123 findings total.

| Severity | Count |
|----------|-------|
| Critical | 11    |
| Major    | 43    |
| Minor    | 50    |
| Info     | 19    |

---

## Critical (11)

### Section 5 — Terraform-Inspired Lifecycle

1. **spec/apply is a stub.** The MCP tool only calls `Begin()` and returns the plan. It does NOT execute destroys, generate code, run the constraint loop, write files, or release the lock. The entire automated pipeline described in 5.2 is unimplemented.

2. **Constraint loop is dead code.** `runConstraintLoop` in `loop.go` exists and is tested but is never called from any production code path. The only call to `engine.Generate` is inside this dead function.

### Section 8 — Plan/Apply/Dispatch/Retry Loop

3. **Constraint loop only implements Generate → Parse → Review.** Missing Resource Validations and Invariant Check steps. Validations run post-hoc in `Commit()` instead of in the retry loop — so failures can't trigger automatic retries with error context.

4. **No poll-based orchestrator loop.** No polling, no automatic state checking, no error/blocked/timeout reporting, no automatic re-dispatch. Orchestration is entirely delegated to the external agent via text instructions.

5. **No automatic state transitions.** States are defined as constants in `state.go` but no code ever transitions resources through pending→dispatched→completed→committed. `Next()` always returns `StatePending`. `Commit()` writes directly to committed without going through the state machine. *(Partially addressed by today's session_resources wiring.)*

6. **No wave-level verification.** `WaveMaxRetries` config exists but is never read. No error attribution, no file path matching, no sliding window analysis, no wave retry mechanism.

### Section 13 — Running Modes & Client Integration

7. **`state list` CLI subcommand missing.** Spec claims it exists; only `check job` and `dashboard` are implemented.

8. **`state rm` CLI subcommand missing.** `DeleteResource` exists in store but isn't exposed as CLI.

9. **`diff` CLI subcommand missing.** MCP tool returns `"diff not yet implemented"`.

10. **`vacuum` CLI subcommand missing.** MCP tool returns `"vacuum not yet implemented"`.

11. **`sql` CLI subcommand missing.** MCP tool returns `"sql not yet implemented"`.

---

## Major (43)

### Section 1 — The CUE DSL
- `consumes` and `publishes` dependency edge kinds are declared in spec but not implemented anywhere

### Section 2 — Architecture
- No `internal/jobs/` package — job lifecycle lives in `store`
- No `internal/verify/` package — verification lives in `spec/validate.go`
- `StdioTransport` / `HTTPTransport` don't exist as separate types; `Server` handles both
- No SSE upgrade in HTTP transport — plain JSON-RPC only
- `internal/mocks/` is empty; tests use hand-written fakes, no counterfeiter

### Section 4 — Agent Wrapper & Engine Layer
- All engine method signatures differ from spec (positional params vs opts struct): `Generate`, `Review`, `CodeReview`, `Bugbot`
- Config isolation only activates when API key is set, not always
- `MCPServers` / `MCPTools` read-only commands not implemented

### Section 5 — Terraform-Inspired Lifecycle
- Destroy actions are planned but never executed — no code deletes files or state
- `Target` and `Force` params in `BeginOpts` are accepted but completely ignored
- Effective hash doesn't include merged meta — parent meta changes won't trigger regeneration
- Only `accept` drift action works; `revert` returns "not yet implemented"

### Section 6 — Prompt Construction
- No language-specific rules (e.g., Rust module casing) — only user-supplied `meta.Rules`
- `layer` field not rendered in domain prompts
- `WaveErrors` field in `RuntimeContext` is never populated in production
- Agent notes injected **twice** with different formatting (via `buildRuntimeContext` and `session.go`)

### Section 7 — SQLite Integration Layer
- `RecordInvariantCheck` store method doesn't exist; `invariant_checks` table is dead schema
- Apply operation method names differ from spec (`CreateApplyAction`/`UpdateApplyAction` vs `RecordAction`)

### Section 8 — Plan/Apply/Dispatch/Retry Loop
- `spec/amend` is a minimal stub — doesn't recompute hashes, cascade dependents, or record audit trail
- `spec/resolve` ignores the `model` parameter and doesn't re-dispatch the resource
- `BlockedContext` and `ErrorContext` structs are defined but never populated
- No fallback to `TypeCheckCommand` / `TestCommand` when a resource has no declared validations

### Section 9 — MCP Server Interface
- No SSE streaming in HTTP transport
- No `bootstrap` tool (spec lists 11 engine tools, 10 exist)
- `spec/note` and `spec/resolve` are functionally identical (both call `Resolve()`)
- `spec/resolve` doesn't re-dispatch — only saves a note
- `spec/state` ignores its `action` and `resource_id` params; always returns full status
- `spec/diff`, `spec/vacuum`, `spec/sql` are all stubs
- `resource_prompt` listed in prompts but not handled in `handlePromptsGet`
- `spec/apply` automated mode described in spec is not implemented

### Section 10 — crest-synth Reference Spec
- Domain-organized CUE directory (`crest-synth/spec/`) doesn't exist — only phase fixtures

### Section 11 — Lifecycle & Robustness
- Semaphore limits engine calls, not actual subprocess count (CodeReview fans out 3x per slot)
- Config isolation conditional on API key, not always active
- Lock table has no PID liveness check — stale locks require manual `spec/unlock`

### Section 12 — Build, Tooling & Conventions
- `internal/app` package is dead code — never imported

### Section 13 — Running Modes & Client Integration
- `spec/apply` only starts a session — no full apply loop

### Section 14 — Design Principles
- "Audit Everything" claim: `CreateGeneration`/`UpdateGeneration` never called from production code; `invariant_checks` table never written to *(partially addressed by today's `run_prompt` generation tracking wiring)*
- Full 5-step constraint loop (parse → type check → invariant → test → review) not implemented; only parse + partial invariant/review exist

---

## Minor (50) — Highlights

- `targets` edge kind implemented but not in spec's dependency table
- Meta merge uses dedup, spec says concatenate
- Meta `...` extensibility claim but Go struct has fixed fields
- `Mode` and `RelevantPaths` in `RunOpts` are dead fields
- `--strict-mcp-config` flag undocumented
- Env var filtering for recursion prevention undocumented
- Review prompt is generic, not hardcoded SOLID/DI checks as spec claims
- `apply_actions` CHECK constraint removed by migration 006, spec still says `create/modify/destroy`
- `foreign_keys=ON` PRAGMA not documented
- `resources` table has `kind`, `context_name`, `model` columns not in spec
- `file_matches` assertion not implemented in `CheckAssertions`
- `spec/context` field named `Instructions` not `DispatchInstructions`
- `make fmt` doesn't run `goimports-reviser`
- `make mocks` target is non-functional (empty directory)
- No `.golangci.yml` config file despite spec claiming gci import ordering
- Drift revert unimplemented

---

## Info (19) — Highlights

- `framework` field functional but undocumented in meta CUE definition
- `reviewLevel` values not validated at Go level
- `UserGuidance` field in RuntimeContext exists but never populated
- `Audio` context from phase-1 not in Contexts Overview table
- `run-phased-agent.sh` script not documented in spec
- Dashboard running mode not mentioned in section 13

---

*Generated 2026-06-07 by 14-agent parallel audit workflow (15 agents, 790k tokens, ~8 min).*
