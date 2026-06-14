#!/bin/bash
# Run crest-spec agent sessions through all 10 crest-synth phases.
# Each phase gets its own interactive Claude session via `claude`.
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

PHASES_DIR="$REPO_ROOT/fixtures/crest-synth/phases"
WORK_DIR="$REPO_ROOT/fixtures/crest-synth/workspace"
SPEC_DIR="$WORK_DIR/spec"

# Build the binary
echo "Building crest-spec..."
go build -o "$REPO_ROOT/bin/crest-spec" "$REPO_ROOT/cmd/crest-spec"
echo ""

# Clean up all generated artifacts so every run starts fresh — UNLESS resuming.
# CREST_KEEP_WORKSPACE=1 preserves committed state (state.db + src) so you can
# continue from a later start phase without redoing earlier phases, e.g.:
#   CREST_KEEP_WORKSPACE=1 ./scripts/run-phased-agent.sh 5 9
if [ "${CREST_KEEP_WORKSPACE:-0}" = "1" ] && [ -f "$WORK_DIR/.crest-spec/state.db" ]; then
  echo "Keeping existing workspace (resume mode); preserving committed state."
  rm -f "$SPEC_DIR"/*.cue
else
  echo "Cleaning workspace..."
  rm -rf "$WORK_DIR"
  echo "Clean."
fi
mkdir -p "$SPEC_DIR"
echo ""

# Ensure the spec-generate skill and workflow are available in the workspace's
# .claude directory so the launched claude session can use them.
WORK_CLAUDE_DIR="$WORK_DIR/.claude"
mkdir -p "$WORK_CLAUDE_DIR/skills" "$WORK_CLAUDE_DIR/workflows"
ln -sfn "$REPO_ROOT/.claude/skills/spec-generate" "$WORK_CLAUDE_DIR/skills/spec-generate"
ln -sfn "$REPO_ROOT/.claude/workflows/spec-generate.js" "$WORK_CLAUDE_DIR/workflows/spec-generate.js"

# The workspace clean wiped .mcp.json (the deleted `crest-spec run` used to
# regenerate it); write it so the launched claude session gets the server.
cat > "$WORK_DIR/.mcp.json" <<EOF
{
  "mcpServers": {
    "crest-spec": {
      "command": "$REPO_ROOT/bin/crest-spec",
      "env": {
        "CREST_SPEC_SPEC_DIR": "$SPEC_DIR",
        "CREST_SPEC_GENERATE_MODEL": "claude-sonnet-4-6",
        "CREST_SPEC_MAX_RETRIES": "3"
      }
    }
  }
}
EOF

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

  # Apply per-asset overrides. Files named phase-N.override-<Asset>.cue carry a
  # definition that REPLACES an earlier phase's (e.g. a ToneTestMain validations
  # list, which CUE cannot unify across phases). For each asset we copy only the
  # highest-numbered override with N <= the current phase, so the latest phase
  # wins. This keeps conflict resolution in the harness's copy behavior rather
  # than in the spec content.
  override_assets=$(ls "$PHASES_DIR"/phase-*.override-*.cue 2>/dev/null \
    | sed -E 's#.*/phase-[0-9]+\.override-(.*)\.cue#\1#' | sort -u)
  for asset in $override_assets; do
    winner=""
    for p in $(seq 1 "$phase"); do
      f="$PHASES_DIR/phase-${p}.override-${asset}.cue"
      [ -f "$f" ] && winner="$f"
    done
    if [ -n "$winner" ]; then
      cp "$winner" "$SPEC_DIR/override-${asset}.cue"
      echo "  Override ${asset}: $(basename "$winner")"
    fi
  done

  echo "══════════════════════════════════════════════════════"
  echo "  Phase ${phase} / ${END}"
  echo "  Spec files: base.cue + phase-1..${phase}.cue"
  echo "══════════════════════════════════════════════════════"
  echo ""

  # Launch generation session via claude directly, with Remote Control enabled so
  # the session can be monitored/driven from claude.ai or the Claude mobile app.
  # Each phase gets its own named Remote Control session for easy identification.
  # Transient API/network failures kill a claude session with a nonzero exit;
  # retry the phase (the planner resumes from committed state) instead of
  # aborting the whole run. The lock is cleared between attempts.
  for attempt in 1 2 3; do
    if (cd "$WORK_DIR" && claude --remote-control "crest-synth phase ${phase}" \
      --model "${CREST_ORCH_MODEL:-opus}" \
      --permission-mode bypassPermissions \
      "Use the spec-generate skill to run a full crest-spec generation session for the spec in ${SPEC_DIR}. Work through every wave; do not stop for confirmation on destroys (this is a fixture run)."); then
      break
    fi
    echo "Phase ${phase}: claude session exited nonzero (attempt ${attempt}/3); clearing lock and retrying."
    sqlite3 "$WORK_DIR/.crest-spec/state.db" "DELETE FROM lock;" 2>/dev/null || true
    if [ "$attempt" = "3" ]; then
      echo "Phase ${phase} FAILED after 3 attempts."
      exit 1
    fi
  done

  echo ""
  echo "Phase ${phase} complete."
  echo ""
done

echo "All phases complete."
