#!/bin/bash
# Run crest-spec agent sessions through all 10 crest-synth phases.
# Each phase gets its own interactive Claude session that connects to
# crest-spec via MCP and drives the generation pipeline.
# State carries over between phases so the planner detects diffs.
#
# Usage: ./scripts/run-phased-agent.sh [start_phase] [end_phase]
#   start_phase: first phase to run (default: 1)
#   end_phase:   last phase to run (default: 10)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

START=${1:-1}
END=${2:-10}

CLI="$REPO_ROOT/bin/crest-spec"
PHASES_DIR="$REPO_ROOT/fixtures/crest-synth/phases"
WORK_DIR="$REPO_ROOT/fixtures/crest-synth/workspace"
SPEC_DIR="$WORK_DIR/spec"

# Build the binary
echo "Building crest-spec..."
(cd "$REPO_ROOT" && make build)
echo ""

# Clean up all generated artifacts so every run starts fresh
echo "Cleaning workspace..."
rm -rf "$WORK_DIR"
mkdir -p "$SPEC_DIR"
echo "Clean."
echo ""

# Write .mcp.json in the workspace so Claude connects to crest-spec
cat > "$WORK_DIR/.mcp.json" <<MCPEOF
{
  "mcpServers": {
    "crest-spec": {
      "command": "$CLI",
      "env": {
        "CREST_SPEC_SPEC_DIR": "$SPEC_DIR",
        "CREST_SPEC_GENERATE_MODEL": "claude-sonnet-4-6",
        "CREST_SPEC_VERIFY_MODEL": "claude-sonnet-4-6",
        "CREST_SPEC_MAX_RETRIES": "3",
        "CREST_SPEC_PERMISSION_MODE": "dangerously-skip-permissions"
      }
    }
  }
}
MCPEOF

PROMPT_TEMPLATE='You are driving a crest-spec generation session for phase PHASE_NUM of the crest-synth project (a Rust synthesizer).

You have crest-spec connected as an MCP server. Use these tools in order:

## Pipeline

1. **spec/begin** — start a session (returns session_id, plan, waves, and orchestrator instructions)
2. **spec/next** (session_id) — get the next wave of uncommitted resources
3. For each resource in the wave:
   a. **spec/context** (session_id, resource_id) — get system_prompt + prompt
   b. **run_prompt** (prompt, system_prompt, session_id, resource_id) — dispatch to Claude sub-agent (returns job_id)
   c. **poll_result** (job_id) — wait for output
   d. Parse the output: extract fenced code blocks with `// path:` annotations
   e. **spec/commit** (session_id, resource_id, files) — commit the parsed files
      - Check the response: if committed=false, validation failed
      - Read the validation errors and re-dispatch with the errors in the prompt
      - After max retries, ask the user or call spec/skip
   f. **spec/note** (resource_id, content) — record design decisions for downstream agents
4. Repeat step 2 until spec/next returns done=true
5. **spec/finish** (session_id) — finalize and release the lock

## Constraint Loop

When spec/commit returns committed=false with validation errors:
1. Build a fix prompt: include the original prompt + the previous output + the validation error
2. Re-dispatch via run_prompt with the fix prompt
3. Parse and try spec/commit again
4. After 3 failed attempts, surface the error to the user for guidance
   - The user can provide guidance (you pass it to spec/resolve)
   - Or the user can tell you to skip (spec/skip)

## Important

- Do NOT use shell commands to run crest-spec. Use the MCP tools exclusively.
- You are a DISPATCHER, not a code generator. Never write code yourself.
- You can dispatch multiple run_prompt calls in parallel for resources in the same wave.
- Waves must be processed sequentially (wave N+1 depends on wave N).
- Code blocks in run_prompt output have path annotations like: // path: src/some/file.rs
- Pass those as files: [{path: "src/some/file.rs", content: "..."}] to spec/commit.
- When a sub-agent raises an issue it cannot resolve, surface it to the user.

Work through every resource. Do not skip any unless generation truly fails.'

for phase in $(seq "$START" "$END"); do
  # Assemble spec dir: base.cue + phase-1 through phase-N
  rm -f "$SPEC_DIR"/*.cue
  cp "$PHASES_DIR/base.cue" "$SPEC_DIR/"
  for p in $(seq 1 "$phase"); do
    src="$PHASES_DIR/phase-${p}.cue"
    if [ ! -f "$src" ]; then
      echo "Phase ${p}: spec file not found: ${src}" >&2
      exit 1
    fi
    cp "$src" "$SPEC_DIR/"
  done

  prompt="${PROMPT_TEMPLATE//PHASE_NUM/$phase}"

  echo "══════════════════════════════════════════════════════"
  echo "  Phase ${phase} / ${END}"
  echo "  Spec files: base.cue + phase-1..${phase}.cue"
  echo "══════════════════════════════════════════════════════"
  echo ""

  # Launch interactive Claude session from workspace dir
  # Claude picks up .mcp.json and connects to crest-spec MCP server
  (cd "$WORK_DIR" && claude --dangerously-skip-permissions "$prompt")

  echo ""
  echo "Phase ${phase} complete."
  echo ""
done

echo "All phases complete."
