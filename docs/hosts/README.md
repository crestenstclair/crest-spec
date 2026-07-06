# crest-spec host adapters

This directory holds host-specific adapter notes for running the same crest-spec MCP contract from different agent hosts.

The canonical contract is `docs/AGENT_INTEGRATION.md`. Host adapters must not redefine crest-spec's responsibilities. They only describe how a host supplies:

1. MCP connectivity to the passive crest-spec engine.
2. Native sub-agent dispatch for generators, verifiers, design agents, task agents, and triage agents.
3. A top-level driver that runs the canonical session loop.
4. Permissions and safety rules for that host.

## Current adapters

| Host | Adapter |
|---|---|
| Claude Code | `docs/hosts/claude-code.md` |
| OpenCode | `docs/hosts/opencode.md` |

## Adapter rule

If a host adapter says crest-spec should interpret a natural-language task, spawn an agent, call an LLM, or run a generation job CLI, the adapter is wrong. The host owns all thinking, dispatch, retry policy, and human escalation. crest-spec only answers tool calls and gates commits.
