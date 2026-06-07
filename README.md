# crest-spec: Terraform for code generation

Declare your software architecture as [CUE](https://cuelang.org) spec files using DDD vocabulary, then generate implementation code by dispatching each resource to an LLM sub-agent. All state is tracked in SQLite -- plan what changed, generate what's needed, skip what's settled. crest-spec runs as an MCP server that Claude connects to and drives automatically.

## Prerequisites

- **Go 1.26+**
- **Claude CLI** (`claude`) -- [install instructions](https://docs.anthropic.com/en/docs/claude-code/overview)
- A **Claude API key** (set via `ANTHROPIC_API_KEY` or `CREST_SPEC_API_KEY`)

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

# 4. Start a Claude session -- crest-spec auto-connects
claude

# 5. Tell Claude to run a generation session
# Just say: "Run a crest-spec generation session"
# Claude calls spec/begin, gets orchestrator instructions, and drives the pipeline.
```

## How It Works

crest-spec follows a plan/apply lifecycle inspired by Terraform:

1. **spec/plan** -- Diff the CUE spec against SQLite state. Shows what needs creating, updating, or destroying.
2. **spec/begin** -- Start a session. Returns a session ID, the execution plan, dependency-ordered waves, and orchestrator instructions that tell Claude how to drive the pipeline.
3. **spec/run_wave** -- Dispatch an entire wave of resources to sub-agents in parallel. Each sub-agent generates code, which passes through a constraint loop (validate, fix, retry) before committing.
4. Repeat step 3 until all waves are processed (`done=true`).
5. **spec/finish** -- Finalize the session, seal state.

Claude follows the orchestrator instructions returned by `spec/begin` automatically -- you don't need to memorize the pipeline. Just say "run a crest-spec generation session" and it handles the rest.

## Key MCP Tools

| Tool | Purpose |
|------|---------|
| `spec/plan` | Preview what needs generating (diff spec vs state) |
| `spec/begin` | Start a session, get plan + waves + orchestrator instructions |
| `spec/run_wave` | Dispatch entire wave in parallel with constraint loops |
| `spec/dispatch` | Re-dispatch a single resource (after failure/guidance) |
| `spec/resolve` | Provide guidance for a failed resource, then re-dispatch |
| `spec/amend` | Fix CUE spec inline and re-dispatch |
| `spec/skip` | Skip a resource and move on |
| `spec/confirm_destroys` | Confirm file deletions for removed resources |
| `spec/deep_review` | SOLID/DI/clean code review of generated code |
| `spec/bootstrap` | Auto-setup environment (deps, toolchain) |
| `spec/finish` | Finalize session |

## Configuration

All configuration is via environment variables prefixed with `CREST_SPEC_`:

| Variable | Default | Purpose |
|----------|---------|---------|
| `CREST_SPEC_SPEC_DIR` | `./spec` | Path to CUE spec directory |
| `CREST_SPEC_GENERATE_MODEL` | `claude-sonnet-4-6` | Model for code generation sub-agents |
| `CREST_SPEC_VERIFY_MODEL` | `claude-sonnet-4-6` | Model for verification/review |
| `CREST_SPEC_MAX_RETRIES` | `3` | Max constraint loop retries per resource |
| `CREST_SPEC_WAVE_MAX_RETRIES` | `2` | Max retries per wave |
| `CREST_SPEC_MAX_CONCURRENCY` | `5` | Max parallel sub-agent processes |
| `CREST_SPEC_DEFAULT_MODEL` | `claude-sonnet-4-6` | Default model for general agent tasks |
| `CREST_SPEC_TYPE_CHECK_CMD` | *(none)* | Custom type-check command (e.g., `cargo check`) |
| `CREST_SPEC_TEST_CMD` | *(none)* | Custom test command (e.g., `go test ./...`) |
| `CREST_SPEC_HTTP_ADDR` | *(none)* | Enable Streamable HTTP transport (e.g., `:8080`) |
| `CREST_SPEC_PERMISSION_MODE` | `default` | Claude permission mode |
| `CREST_SPEC_TIMEOUT` | `0s` | Sub-agent timeout (0 = no timeout) |

Set these in `.mcp.json` under the `env` key, or export them in your shell.

## Multi-Phase Generation

For large projects, you can split specs into incremental phases and run them sequentially. The script `scripts/run-phased-agent.sh` demonstrates this pattern:

```bash
# Run all phases (1-10)
./scripts/run-phased-agent.sh

# Run phases 3 through 5
./scripts/run-phased-agent.sh 3 5
```

Each phase launches a separate Claude session. State carries over between phases, so the planner detects diffs and only generates what changed.

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
