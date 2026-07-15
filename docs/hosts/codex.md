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

`CREST_SPEC_GENERATE_MODEL` is the stable plan/hash label. The generator separately records the exact Codex host version, provider, model, reasoning configuration, tools, permissions, templates, system instructions, and context hash in `spec/execution_start`.

The long MCP timeout is intentional: `spec/commit`, `spec/verify_wave`, and `spec/verify` may run complete build or behavioral commands. It does not change CrestSpec's fail-closed behavior.

## Native orchestration

The apply skill keeps the root Codex task focused on session state and decisions:

1. Validate project intent and inspect goal-directed plan rationale.
2. Begin one session and stop on unapproved destroys.
3. Call `spec/next` for the current dependency wave.
4. Spawn one `crest_generator` subagent per resource, concurrently up to the thread limit.
5. Each generator performs `spec/context → spec/execution_start → spec/commit` itself and retries only from persisted UPDATE-mode context.
6. Reconcile the wave against CrestSpec state, run `spec/verify_wave`, and use `crest_triage` for attributable failures.
7. Dispatch `crest_verifier` agents for required or stale named witnesses; CrestSpec executes and classifies both cases.
8. Finish only when project goals and current evidence satisfy completion.

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
4. Current-wave resources appear as parallel subagent tasks.
5. Every implementation attempt has a context manifest and Codex execution manifest before commit.
6. Rejected candidates retry from prior files, raw validation output, and routed guidance.
7. Named witnesses are executed by CrestSpec and visible through `spec/verification_inspect`.
8. `spec/finish` succeeds without force only when required goals are complete.

Codex project instructions and skills are documented in the [Codex customization guide](https://learn.chatgpt.com/docs/agent-configuration/agents-md), [skills guide](https://learn.chatgpt.com/docs/build-skills), [subagents guide](https://learn.chatgpt.com/docs/agent-configuration/subagents), and [MCP guide](https://learn.chatgpt.com/docs/extend/mcp).
