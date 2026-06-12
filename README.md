# crest-spec: Terraform for code generation

Declare your software architecture as [CUE](https://cuelang.org) spec files using DDD vocabulary, then generate implementation code one resource at a time with surgically scoped prompts. All state is tracked in SQLite -- plan what changed, generate what's needed, skip what's settled.

crest-spec runs as an MCP server that is a **pure spec state engine**: it plans, tracks state, runs mechanical validations (compile/test/custom) at commit time, and records history. **It never calls an LLM and never spawns subprocesses.** Claude Code is the orchestrator -- it runs the bundled `spec-generate` skill/workflow, which spawns one sub-agent per resource per wave to generate, judge invariants, and commit through the server's validation gate.

## Prerequisites

- **Go 1.26+**
- **Claude Code** (`claude`) -- the orchestrator that drives the server -- [install instructions](https://docs.anthropic.com/en/docs/claude-code/overview)

## Installation

Install directly:

```bash
go install github.com/crestenstclair/crest-spec/cmd/crest-spec@latest
```

Or clone and build:

```bash
git clone https://github.com/crestenstclair/crest-spec.git
cd crest-spec
make build        # binary lands in bin/crest-spec
make install      # installs to $GOPATH/bin
```

## Quick Start

The minimal path from zero to generated code:

```bash
# 1. Create a project directory
mkdir my-project && cd my-project

# 2. Write a minimal CUE spec
mkdir spec
cat > spec/project.cue << 'EOF'
package spec
project: {
  name: "my-project"
  meta: {
    language: "go"
    style: ["idiomatic"]
  }
  contexts: {
    core: {
      aggregates: {
        User: {
          purpose: "Manages user lifecycle"
          state: {
            id: "string"
            name: "string"
            email: "string"
          }
        }
      }
    }
  }
}
EOF

# 3. Configure crest-spec as an MCP server
cat > .mcp.json << 'EOF'
{
  "mcpServers": {
    "crest-spec": {
      "command": "crest-spec",
      "env": {
        "CREST_SPEC_SPEC_DIR": "./spec"
      }
    }
  }
}
EOF

# 4. Open Claude Code in your project -- crest-spec auto-connects
claude

# 5. Ask Claude to run the spec
# Just say: "Use the spec-generate skill to run a crest-spec generation session"
# (or "run the spec"). Claude drives spec/plan -> spec/begin -> the spec-generate
# workflow -> spec/finish, spawning a sub-agent per resource.
```

The `spec-generate` skill and workflow ship in this repo under `.claude/`. In your own project, copy them into the project's `.claude/skills/` and `.claude/workflows/` (or run from a checkout of this repo) so Claude Code can find them.

## How It Works

crest-spec follows a plan/apply lifecycle inspired by Terraform. The **server** plans and validates; **Claude Code** orchestrates generation:

1. **spec/plan** -- Diff the CUE spec against SQLite state. Shows what needs creating, updating, or destroying.
2. **spec/begin** -- Start a session. Returns a session ID, the execution plan, dependency-ordered waves, and pending destroys.
3. **spec-generate workflow** -- For each wave, the workflow spawns one sub-agent per resource (in parallel). Each sub-agent calls `spec/context` for a scoped prompt + invariants, authors files, judges each invariant, and calls `spec/commit`. The server writes the files, runs the resource's mechanical validations, and enforces the invariant verdicts -- any failure rejects the commit and the failure is injected into the next attempt (retry up to `MAX_RETRIES`). Persistent failures are triaged with `spec/resolve` or `spec/skip`.
4. Waves run sequentially until all are processed (`done=true`).
5. **spec/finish** -- Finalize the session, seal state. Optionally runs a reflection pass to distil learnings.

You don't need to memorize the pipeline. Just say "run the spec" (or invoke the `spec-generate` skill) and Claude handles the rest.

## Key MCP Tools

| Tool | Purpose |
|------|---------|
| `spec/plan` | Preview what needs generating (diff spec vs state) |
| `spec/begin` | Start a session, get plan + waves + pending destroys |
| `spec/confirm_destroys` | Confirm file deletions for removed resources |
| `spec/next` | Get the next dependency-ordered wave of resources |
| `spec/context` | Get a resource's scoped prompt + invariants |
| `spec/commit` | Commit files; runs validations + enforces invariant verdicts |
| `spec/resolve` | Provide guidance for a failed resource (resets to pending) |
| `spec/amend` | Fix the CUE spec for a resource, then regenerate |
| `spec/skip` | Skip a resource and move on |
| `spec/finish` | Finalize the session (optionally returns a reflection prompt) |
| `spec/evolve` / `spec/record_learnings` | Reflect over failures and persist learnings |
| `spec/bootstrap` | Auto-setup the spec dir, database, and MCP config |

> Note: the server never runs an LLM. There are no `run_prompt`/`code_review`/`bugbot` tools and no `spec/apply`/`spec/dispatch`/`spec/run_wave` -- generation is driven by Claude Code's own sub-agents via the spec-generate workflow.

## Configuration

All configuration is via environment variables prefixed with `CREST_SPEC_`:

| Variable | Default | Purpose |
|----------|---------|---------|
| `CREST_SPEC_SPEC_DIR` | `./spec` | Path to CUE spec directory |
| `CREST_SPEC_GENERATE_MODEL` | `claude-sonnet-4-6` | Model **label** recorded in state / used as the default commit label (the server does not invoke it) |
| `CREST_SPEC_MAX_RETRIES` | `3` | Per-resource retry budget for the commit/retry loop |
| `CREST_SPEC_WAVE_MAX_RETRIES` | `2` | Max retries for wave-level verification |
| `CREST_SPEC_TYPE_CHECK_CMD` | *(none)* | Custom type-check command (e.g., `cargo check`) |
| `CREST_SPEC_TEST_CMD` | *(none)* | Custom test command (e.g., `go test ./...`) |
| `CREST_SPEC_MODE` | `default` | Mode label folded into hashes (different modes regenerate) |
| `CREST_SPEC_EVOLVE` | `all` | When to emit reflection prompts (`finish`/`all`) |
| `CREST_SPEC_HTTP_ADDR` | *(none)* | Enable Streamable HTTP transport (e.g., `:8080`) |

Set these in `.mcp.json` under the `env` key, or export them in your shell.

## Multi-Phase Generation

For large projects, you can split specs into incremental phases and run them sequentially. The script `scripts/run-phased-agent.sh` demonstrates this pattern:

```bash
# Run all phases (1-10)
./scripts/run-phased-agent.sh

# Run phases 3 through 5
./scripts/run-phased-agent.sh 3 5
```

Each phase launches a separate Claude Code session that uses the `spec-generate` skill to run that phase's generation. State carries over between phases, so the planner detects diffs and only generates what changed.

## CLI Subcommands

When run without arguments, `crest-spec` starts the MCP server on stdio. It also supports:

```
crest-spec dashboard [--addr :8080]    # Web dashboard for monitoring sessions
crest-spec state list                  # Print all resources in state
crest-spec state rm <resourceId>       # Remove a resource from state
crest-spec diff <apply_a> <apply_b>    # Show changes between two applies
crest-spec vacuum --before <date>      # Compact history older than date
crest-spec sql <query>                 # Execute a read-only SQL query against state
```

## Development

```bash
make test         # go test ./...
make build        # build to bin/crest-spec
make fmt          # go fmt ./...
make lint         # golangci-lint run
```

See [SPEC.md](SPEC.md) for the full functional and implementation specification.
