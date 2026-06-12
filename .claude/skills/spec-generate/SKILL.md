---
name: spec-generate
description: Use when the user asks to run/apply/generate a crest-spec session ("run the spec", "generate phase N", "apply the spec") — drives the full native generation pipeline via the spec-generate workflow with sonnet sub-agents
---

# crest-spec native generation

You are the orchestrator. The crest-spec MCP server is a pure state engine —
it never runs LLMs. Generation happens in YOUR sub-agents via the
spec-generate workflow. Default model: sonnet. Never haiku.

## Pipeline

1. `spec_plan` — review what will change. If empty, report "up to date" and stop.
2. `spec_begin` — returns session_id, plan, waves, PendingDestroys.
3. If PendingDestroys is non-empty: show the list to the user and call
   `spec_confirm_destroys` only for resources the user confirms (or all, if
   the user pre-authorized destructive applies).
4. Invoke the Workflow tool:
   `Workflow({scriptPath: ".claude/workflows/spec-generate.js", args: {sessionId: "<session_id>"}})`
   (Use `args.model` to override the generation model; complex resources can
   justify opus — never haiku.)
5. When the workflow completes, review its `triaged` list and surface skips
   to the user.
6. `spec_finish` — if the result's `reflection_prompt` is non-empty, run it
   with one sonnet sub-agent (Agent tool, general-purpose) and pass the raw
   output to `spec_record_learnings` (with the session_id).
7. Report: committed/skipped/errored counts, triage decisions, learnings added.

## Failure handling

- The workflow retries each resource internally (server injects failure
  context into the regenerated prompt) and triages persistent failures with
  spec_resolve/spec_skip.
- If the whole workflow dies, `spec_status`/`spec_wave_status` show where it
  stopped; re-invoking the workflow with the same sessionId resumes (spec_next
  re-serves non-terminal resources).
- A stale lock from a crashed session: `spec_unlock`, then `spec_begin` again.

## Rules

- Never write generated-resource code in the main session — every file comes
  from a sub-agent via spec_commit.
- Waves are sequential; resources within a wave run in parallel (the workflow
  handles this).
- Validation failures are signal, not noise: read them before retrying scope.
