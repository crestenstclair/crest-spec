# crest-spec

![Claude Code's workflows view during a spec-generate run: a wave of sub-agents generating, committing, and verifying resources in parallel](docs/images/spec-generate-run.png)

<sub>Claude Code's `/workflows` view during a live `spec-generate` run. The workflow orchestrates a wave of sub-agents that fill in each resource (value objects, aggregates) in parallel — each pulling its scoped prompt, generating, and committing — followed by Verify and Triage phases. This is the declare → coordinate → fill → check loop below, as seen from inside the session.</sub>

crest-spec is a declarative code-generation engine for developers who model software as a domain: you declare your architecture as [CUE](https://cuelang.org) using DDD vocabulary — bounded contexts, aggregates, value objects, ports, adapters — and crest-spec drives an LLM to fill in the implementation, one resource at a time, against the invariants you stated.

You write the *declaration*; crest-spec coordinates the *fill*. State lives in SQLite, so the engine plans what changed, generates only what's needed, and leaves settled resources alone — a plan/apply lifecycle for code, not infrastructure.

## The declare → fill loop

A value object in the `Kernel` bounded context, declared in CUE (`fixtures/crest-synth/spec/kernel.cue`):

```cue
project: contexts: Kernel: valueObjects: Frequency: {
	from: "f64"
	description: "frequency in Hz"
	invariants: ["must be positive"]
}
```

That's the whole declaration — a type, a description, an invariant. No body. crest-spec turns it into the implementation (`src/kernel/frequency.rs`), enforcing the invariant in code and proving it with tests:

```rust
/// Frequency in Hz.
///
/// `Frequency` wraps a `f64` and enforces the invariant that the value
/// is strictly positive (> 0.0) and not NaN or infinite.
#[derive(Debug, Clone, Copy, PartialEq, PartialOrd)]
pub struct Frequency(f64);

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct FrequencyError(f64);

impl Frequency {
    /// Construct a `Frequency` from a raw `f64` in Hz.
    ///
    /// Returns `Err` if the value is NaN, infinite, or not strictly positive.
    pub fn try_new(value: f64) -> Result<Self, FrequencyError> {
        if value.is_nan() || value.is_infinite() || value <= 0.0 {
            return Err(FrequencyError(value));
        }
        Ok(Self(value))
    }

    #[inline]
    pub fn value(self) -> f64 {
        self.0
    }
}

#[cfg(test)]
mod tests {
    // a440_is_valid, zero_is_rejected, negative_is_rejected,
    // nan_is_rejected, infinity_is_rejected, copy_semantics, ...
}
```

The invariant `"must be positive"` in the spec becomes the `value <= 0.0` guard in `try_new` and the `zero_is_rejected` / `negative_is_rejected` tests. The declaration is the source of truth; the body is regenerated whenever the declaration's hash changes.

## What it is

crest-spec runs as an **MCP server that is a pure spec state engine**. It plans, tracks resource state in SQLite, runs mechanical validations (compile / test / custom) at commit time, and records history. It never calls an LLM and never spawns subprocesses.

Claude Code is the orchestrator. It runs the bundled `spec-generate` skill/workflow, which spawns one sub-agent per resource per wave to generate files and commit through the server's validation gate. The server is the quality gate; Claude does the writing — and never grades its own work: mechanical validations gate the commit, independent falsification-gated behavioral checks gate the wave, and `spec/finish` refuses to seal a session while any check is unresolved.

## Prerequisites

- **Go 1.26+**
- **Claude Code** (`claude`) — the orchestrator that drives the server — [install instructions](https://docs.anthropic.com/en/docs/claude-code/overview)

## Install

```bash
go install github.com/crestenstclair/crest-spec/cmd/crest-spec@latest
```

Or from a checkout:

```bash
git clone https://github.com/crestenstclair/crest-spec.git
cd crest-spec
make build        # binary lands in bin/crest-spec
make install      # go install ./cmd/crest-spec
```

## Quickstart

```bash
# 1. A project directory with a CUE spec
mkdir my-project && cd my-project && mkdir spec
cat > spec/project.cue << 'EOF'
package spec
project: {
	name: "my-project"
	meta: {language: "go", style: "idiomatic Go, DDD"}
	contexts: Core: {
		purpose: "user lifecycle"
		aggregates: User: {
			root: true
			purpose: "manages a user"
			state: {id: "string", name: "string", email: "string"}
		}
	}
}
EOF

# 2. Register crest-spec as an MCP server
cat > .mcp.json << 'EOF'
{
  "mcpServers": {
    "crest-spec": {
      "command": "crest-spec",
      "env": { "CREST_SPEC_SPEC_DIR": "./spec" }
    }
  }
}
EOF

# 3. Open Claude Code — crest-spec auto-connects over stdio
claude

# 4. Ask Claude to run the spec:
#    "Use the spec-generate skill to run a crest-spec session" (or just "run the spec")
```

The `spec-generate` skill and workflow ship in this repo under `.claude/`. Copy them into your project's `.claude/skills/` and `.claude/workflows/` (or run from a checkout) so Claude Code can find them.

## How it works

![The constraint loop: declare invariants in CUE, a sub-agent generates the code, the server validates against the invariants, and failures fold back into the next prompt to regenerate](docs/images/constraint-loop.svg)

A plan/apply lifecycle: the **server** plans and checks; **Claude Code** fills.

1. **Declare** — Write your domain as CUE in the spec directory: contexts, aggregates, value objects, domain/application services, repositories, ports, adapters, and assets. Each is a *resource*.
2. **Coordinate** (`spec/plan`, `spec/begin`) — The server diffs the CUE spec against SQLite state by declaration hash, computes a dependency graph, and returns an execution plan ordered into waves, plus any pending destroys for removed resources.
3. **Fill** (`spec/next` → `spec/context` → `spec/commit`) — For each resource in a wave, a sub-agent fetches a scoped prompt from `spec/context` — the project mission, the declaration, its bounded context's design contract, the invariants to honor, and (on retries) the previous failure and any triage guidance — authors the files, and commits.
4. **Check** — On `spec/commit` the server writes the files and runs the resource's mechanical validations, fail-closed (a validation that cannot launch is a failure). Independent verifier agents then prove behavior via falsification-gated checks (`spec/verify`: a real witness must pass AND a degenerate stub must fail). Any failure rejects/regenerates with the detail folded into the next `spec/context`, up to `CREST_SPEC_MAX_RETRIES`. Persistent failures are triaged with `spec/resolve`, or `spec/skip` when the spec itself is provably contradictory.
5. **Finish** (`spec/finish`) — Waves run until none remain. Finish refuses while behavioral checks are unresolved (`force` is an explicit override). Optionally a reflection pass distils learnings via `spec/evolve` / `spec/record_learnings`.

Settled resources whose declaration hash is unchanged are skipped — regeneration is driven by spec changes, not wall-clock. Structural changes (types, contracts, invariants) cascade to dependents; guidance changes (prompts, descriptions) regenerate only the edited resource. Once generated, a file's content is yours: the planner guides architecture and invariants, it does not police file contents.

![High-level flow: CUE spec files load into an in-memory resource registry, diff against SQLite state to produce a plan of waves, the orchestrator's sub-agents generate each resource, and the server's commit gate validates — passes are persisted, failures fold back into the next context and regenerate](docs/images/constraint-loop-flow.svg)

<sub>The same loop in full, end to end: the **server** (pure state engine) loads and diffs the CUE spec to plan waves; **Claude Code**'s sub-agents fill each resource; the server's **commit gate** validates and either persists the result or rejects it, folding the failure back across the MCP boundary into the next `spec/context`.</sub>

### Key MCP tools

| Tool | Purpose |
|------|---------|
| `spec/plan` | Preview what needs generating (diff spec vs state) |
| `spec/begin` | Start a session; returns plan, waves, pending destroys |
| `spec/confirm_destroys` | Confirm file deletions for removed resources |
| `spec/next` | Get the next dependency-ordered wave |
| `spec/context` | Get a resource's scoped prompt + invariants |
| `spec/commit` | Commit files; runs validations + enforces invariant verdicts |
| `spec/resolve` | Provide guidance for a failed resource (resets to pending) |
| `spec/amend` | Add a spec-resident correction to a resource, then regenerate |
| `spec/skip` | Skip a resource and move on |
| `spec/finish` | Finalize the session (may return a reflection prompt) |
| `spec/bootstrap` | Auto-create the spec dir, database, and MCP config |

## Scope

crest-spec deliberately does **not**:

- **Call an LLM or spawn subprocesses.** The server is a state engine. All generation is driven by Claude Code's own sub-agents — there are no `run_prompt` / `code_review` / `dispatch` tools, and no `spec/apply` / `spec/run_wave`.
- **Generate from prose.** Resources are declared in CUE with DDD structure (contexts, aggregates, value objects, ports…), not free-form feature descriptions.
- **Pin a language or framework.** `meta.language` and the asset kinds in your spec decide the target; the engine is language-agnostic. The reference fixture (`fixtures/crest-synth`) targets Rust; the e2e spec targets Go.
- **Judge correctness on your behalf.** It runs the mechanical validations *you* declare (compile/test/custom) and mechanically enforces the behavioral falsification gate (`spec/verify`: real witness passes, stub baseline fails). It never grades semantics itself — and neither does the generator: there are no self-judged verdicts anywhere in the loop.
- **Start from scratch when code exists.** Once a resource has code — a committed implementation or even a rejected attempt — regeneration runs in UPDATE mode: the existing files are served back with the failure or the changed requirement, and the generator makes the minimal edit. Iteration is the default; a blank-slate rewrite happens only for brand-new resources.

## Configuration

All configuration is via environment variables prefixed with `CREST_SPEC_`, set under the `env` key in `.mcp.json` (or exported in your shell):

| Variable | Default | Purpose |
|----------|---------|---------|
| `CREST_SPEC_SPEC_DIR` | `./spec` | Path to the CUE spec directory |
| `CREST_SPEC_GENERATE_MODEL` | `claude-sonnet-4-6` | Model **label** recorded in state (the server never invokes it) |
| `CREST_SPEC_MAX_RETRIES` | `3` | Per-resource retry budget for the commit/retry loop |
| `CREST_SPEC_WAVE_MAX_RETRIES` | `2` | Max retries for wave-level verification |
| `CREST_SPEC_TYPE_CHECK_CMD` | *(none)* | Custom type-check command (e.g. `cargo check`) |
| `CREST_SPEC_TEST_CMD` | *(none)* | Custom test command (e.g. `go test ./...`) |
| `CREST_SPEC_MODE` | `default` | Mode label folded into hashes (different modes regenerate) |
| `CREST_SPEC_EVOLVE` | `all` | When to emit reflection prompts (`finish` / `all`) |
| `CREST_SPEC_HTTP_ADDR` | *(none)* | Enable Streamable HTTP transport (e.g. `:8080`) |

## CLI

With no arguments, `crest-spec` starts the MCP server on stdio. It also supports:

```
crest-spec dashboard [--addr :8080]    # web dashboard for monitoring sessions
crest-spec adopt                       # seed baseline state from on-disk code
crest-spec state list                  # print all resources in state
crest-spec state rm <resourceId>       # remove a resource from state
crest-spec diff <apply_a> <apply_b>    # show changes between two applies
crest-spec vacuum --before <date>      # compact history older than date
crest-spec sql <query>                 # read-only SQL query against state
crest-spec --help                      # full usage and env-var reference
```

## Development

```bash
make test         # go test ./...
make build        # build to bin/crest-spec
make fmt          # go fmt ./...
make lint         # golangci-lint run
```

See [SPEC.md](SPEC.md) for the full functional and implementation specification, and `fixtures/crest-synth` for the reference spec. Its `spec/` directory is the whole-picture design (domain-grouped, one file per bounded context); `phases/phase-1/` through `phases/phase-11/` are the original phased spec hydrated into standalone directories — each holds `base.cue`, the cumulative phase files, and the resolved winning overrides for that stage. Point `CREST_SPEC_SPEC_DIR` at any phase to replay that stage of the evolving design, then at the next one and the planner regenerates exactly the delta.

## Status

Pre-1.0 and under active development. The native-orchestration architecture (server as pure state engine, Claude Code sub-agents doing generation) is validated on multi-phase runs, but the MCP tool surface, CUE schema, and configuration are not yet stable and may change without notice between commits. There is no compatibility guarantee on the SQLite state schema; expect to regenerate from spec. Use it on projects you can afford to re-run.
