# Codex adapter for crest-spec

Codex can drive crest-spec directly from the desktop app, CLI, or IDE extension. The integration is project-scoped: Codex discovers the workflow from the target repository, connects to crest-spec over stdio MCP, and uses native subagents to execute dependency waves.

## Bootstrap

Run this from the target project root:

```bash
crest-spec init --agent codex
```

Use `--force` only when you intend to replace previously generated Codex integration files. The bootstrap writes:

| File | Responsibility |
|---|---|
| `.codex/config.toml` | Required local crest-spec MCP server, long validation timeout, and bounded subagent concurrency |
| `.codex/agents/crest-generator.toml` | One governed resource attempt at a time |
| `.codex/agents/crest-triage.toml` | Failure attribution and `spec/resolve` guidance |
| `.codex/agents/crest-verifier.toml` | One engine-executed witness and provenance inspection |
| `.codex/agents/crest-design.toml` | Legacy observable-contract authoring when needed |
| `.codex/agents/crest-tasks.toml` | Legacy typed task/check authoring when needed |
| `.agents/skills/apply-crest-spec/SKILL.md` | The natural-language-triggered orchestration loop |

Codex reads project `.codex/config.toml` only for a trusted repository. It builds the `AGENTS.md` and skill instruction chain at the start of a task, so trust the project and start a new task after bootstrapping. In the new task, this is sufficient:

```text
apply this crest-spec
```

The skill description includes that exact phrase, so Codex can select it implicitly. It also explicitly requires subagent delegation, which enables wave-parallel work without putting the orchestration loop into a separate script.

## Generated MCP configuration

The generated project config is equivalent to:

```toml
[agents]
max_threads = 8
max_depth = 1
job_max_runtime_seconds = 3600

[mcp_servers.crest-spec]
command = "/absolute/path/to/crest-spec"
cwd = "."
required = true
startup_timeout_sec = 30
tool_timeout_sec = 1800
default_tools_approval_mode = "auto"

[mcp_servers.crest-spec.tools."spec/confirm_destroys"]
approval_mode = "prompt"

[mcp_servers.crest-spec.tools."spec/skip"]
approval_mode = "prompt"

[mcp_servers.crest-spec.env]
CREST_SPEC_SPEC_DIR = "./spec"
CREST_SPEC_GENERATE_MODEL = "codex-gpt-5.6"
CREST_SPEC_MAX_RETRIES = "3"
CREST_SPEC_WAVE_MAX_RETRIES = "2"
```

`CREST_SPEC_GENERATE_MODEL` is a compatibility provenance label; changing it alone does not dirty resources or change incremental hashes. The generator separately records the exact Codex host version, provider, model, reasoning configuration, tools, permissions, templates, system instructions, and context hash in `spec/execution_start`.

The long MCP timeout is intentional: `spec/commit`, `spec/verify_wave`, and `spec/verify` may run complete build or behavioral commands. It does not change CrestSpec's fail-closed behavior.

## Native orchestration

The apply skill keeps the root Codex task focused on session state and decisions:

1. Validate project intent and inspect goal-directed plan rationale.
2. Begin one session and stop on unapproved destroys.
3. Call `spec/next` for the current dependency wave.
4. Preallocate a bounded set of logical `crest_generator` slots with stable, nonempty `worker_id` values plus one stable `host_session_id` for the host run. Because native spawn immediately starts its task, do not spawn an initially idle agent: claim the slot's first context, spawn it with that assignment, retain the returned identity, and use follow-up assignments for later resources.
5. Whenever workers are idle, call `spec/context_batch` with `limit` equal to exactly those idle slots. It returns at most that many immutable contexts; each is prepared but unclaimed. Report `Blocked=true` without assigning it. A context with `NoDispatch=true` means CrestSpec reinstated an exact accepted candidate; do not dispatch or execute it.
6. For every remaining returned context, choose an idle slot and have the parent record `dispatch_requested` with the context's `DispatchID` and the stable host/worker identities. Only after that succeeds may the parent spawn the slot's first agent or deliver a follow-up assignment to its retained identity. If it returns `rebuild_context`, do not assign the stale abandoned attempt; refill that slot through `spec/context_batch` without charging a retry. The worker records `worker_acknowledged` with the same identities before `spec/execution_start`. If spawn/delivery or the worker fails before execution registration, the live parent or worker immediately reports `dispatch_failed` with the same identities and concrete reason; this releases the exact claim without consuming a retry. These events are mandatory; CrestSpec rejects overlapping nonterminal assignments and execution without a valid lifecycle. Repeated capacity-limited batch calls refill newly idle slots instead of prefetching a whole wave.
7. Rejected candidates use persisted UPDATE-mode `spec/context`. The worker records `dispatch_requested` for that new context's `DispatchID`, then `worker_acknowledged`, before the next `spec/execution_start`. If retry dispatch or execution start returns `rebuild_context`, discard the abandoned stale attempt and have the worker fetch and dispatch a replacement without charging a generation retry. Reuse workers across waves, but preserve distinct dispatch, attempt, and execution identities for every assignment.
8. Reconcile the complete wave against CrestSpec state, run `spec/verify_wave`, and use `crest_triage` for attributable failures.
9. Dispatch `crest_verifier` agents for required or stale named witnesses; CrestSpec executes and classifies both cases.
10. Finish only when project goals and current evidence satisfy completion.

If a Codex client does not expose custom-agent selection to the current spawn surface, the skill falls back to a generic worker that reads and follows the corresponding `.codex/agents/*.toml` instructions. The governed engine protocol is the same either way.

## Candidate boundary

Generator agents must not edit implementation files before calling `spec/commit`. They construct complete candidate contents and send them to the engine by immutable attempt ID. CrestSpec snapshots, writes, validates, accepts, or restores the candidate. Direct pre-commit editing would bypass the candidate provenance boundary and is explicitly forbidden by the generated agent instructions.

Advisory roles use `spec/execution_report`; they never submit candidate files. Failure handoffs use the role selected by the persisted context rather than a universal coding prompt.

## Destructive changes and permissions

Codex subagents inherit the parent task's sandbox and approval policy. The generated MCP connection uses automatic approval for ordinary engine transitions while retaining explicit MCP prompts for `spec/confirm_destroys` and `spec/skip`; this is not deletion authority:

- `apply this crest-spec` does not authorize destruction.
- `spec/begin` only reports pending destroys.
- The skill stops and lists exact resources/files unless the user approved those deletions in the current conversation.
- `spec/skip` and forced finish remain explicit human decisions.

## Verification checklist

After bootstrap, verify the integration from a fresh Codex task:

1. The repository is trusted and `/mcp` shows `crest-spec` connected.
2. Asking `apply this crest-spec` selects the project skill.
3. Codex calls `spec/validate`, `spec/plan`, and `spec/begin` before dispatch.
4. `spec/context_batch` is called with `limit` equal to idle worker slots; no whole-wave context queue is prefetched.
5. Every assignment records `dispatch_requested` before scheduling, `worker_acknowledged` before execution, and has a context plus Codex execution manifest before commit; failed pre-execution delivery records `dispatch_failed` and releases the slot immediately.
6. Rejected candidates retry from prior files, raw validation output, and routed guidance using a new context and a new mandatory dispatch/acknowledgement lifecycle.
7. Named witnesses are executed by CrestSpec and visible through `spec/verification_inspect`.
8. `spec/finish` succeeds without force only when required goals are complete.

Codex project instructions and skills are documented in the [Codex customization guide](https://learn.chatgpt.com/docs/agent-configuration/agents-md), [skills guide](https://learn.chatgpt.com/docs/build-skills), [subagents guide](https://learn.chatgpt.com/docs/agent-configuration/subagents), and [MCP guide](https://learn.chatgpt.com/docs/extend/mcp).
