# Claude Code adapter for crest-spec

Claude Code is the first host adapter for crest-spec and currently acts as the reference implementation for the host-side orchestration loop.

## Control model

Claude Code is the agent host and orchestrator. crest-spec is a passive MCP state engine. Claude Code workflows and sub-agents do all thinking, generation, verification, retry policy, and human escalation.

## MCP configuration

Register crest-spec as a local stdio MCP server in the target project's `.mcp.json`:

```json
{
  "mcpServers": {
    "crest-spec": {
      "command": "/absolute/path/to/crest-spec",
      "env": {
        "CREST_SPEC_SPEC_DIR": "./spec",
        "CREST_SPEC_GENERATE_MODEL": "claude-sonnet-5"
      }
    }
  }
}
```

The model value is a stable state label. Claude Code chooses the actual model for its workflow agents.

## Reference workflow

The canonical Claude Code workflow is:

- `.claude/workflows/spec-generate.js`

It implements:

1. design contract agents per bounded context;
2. task/check agents per resource;
3. one generator sub-agent per resource per wave;
4. independent verifier sub-agents per behavioral check;
5. wave verification, triage, resolve, and stall handling;
6. finish-time blocker handling and learning capture.

The companion skill is:

- `.claude/skills/spec-generate/SKILL.md`

Use this adapter as executable pseudocode for other hosts. Do not move its responsibilities into crest-spec.

## HTTP drivers

For hosts or scripts that need fan-out without a native MCP client per worker, use crest-spec's HTTP transport:

```bash
CREST_SPEC_SPEC_DIR=./spec \
CREST_SPEC_HTTP_ADDR=127.0.0.1:8792 \
CREST_SPEC_GENERATE_MODEL=claude-sonnet-5 \
/absolute/path/to/crest-spec
```

Reference HTTP-transport drivers live outside this repository in crest-synth under `.crest-spec/*-http.js`:

- `spec-generate-http.js`
- `spec-behavioral-http.js`
- `spec-reauthor-http.js`
- `spec-verify-pending-http.js`

## Adapter invariants

- Claude Code workflows spawn the sub-agents; crest-spec never does.
- Sub-agents call `spec/context`, register the exact host attempt through
  `spec/execution_start`, and call `spec/commit` by attempt ID directly.
- Retries must use UPDATE-mode context.
- Verification must execute a declared witness through `spec/verify`; inspect the resulting SQLite-backed run and evidence rather than accepting agent-supplied observations.
- `spec/skip` and `spec/finish force` are human-level decisions.
