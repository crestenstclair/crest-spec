#!/bin/bash
# Run crest-spec agent sessions through all 10 crest-synth phases.
# Each phase gets its own interactive Claude session that drives the agent CLI.
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

PROMPT_TEMPLATE='You are driving a crest-spec agent session for phase PHASE_NUM of the crest-synth project.

The working directory is: WORK_DIR

## Commands available

Run these from the working directory:

- `CREST_SPEC_SPEC_DIR=SPEC_DIR CLI plan` — see what needs to change
- `CREST_SPEC_SPEC_DIR=SPEC_DIR CLI apply` — run the full apply (generates code via LLM, writes files, commits to state)
- `CREST_SPEC_SPEC_DIR=SPEC_DIR CLI validate` — check spec validity
- `CREST_SPEC_SPEC_DIR=SPEC_DIR CLI graph` — show dependency graph
- `CREST_SPEC_SPEC_DIR=SPEC_DIR CLI status` — show current state
- `CREST_SPEC_SPEC_DIR=SPEC_DIR CLI unlock` — force-clear stale lock

## Your job

1. Run `plan` to see what resources need to be created/modified for this phase.
2. Run `apply` to generate all resources. This will:
   - Start a session and acquire a lock
   - Process resources wave by wave
   - For each resource: build a prompt from the spec, call Claude to generate code, parse code blocks, write files to disk, commit to state
   - Advance through all waves and finalize
3. If any resources fail, check the error and decide whether to retry or skip.
4. Verify the output looks reasonable (check generated files in WORK_DIR/).

The spec directory contains CUE files for phases 1 through PHASE_NUM.
State from previous phases carries over — the planner only generates resources that are new or changed.'

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
  prompt="${prompt//WORK_DIR/$WORK_DIR}"
  prompt="${prompt//SPEC_DIR/$SPEC_DIR}"
  prompt="${prompt//CLI/$CLI}"

  echo "══════════════════════════════════════════════════════"
  echo "  Phase ${phase} / ${END}"
  echo "  Spec files: base.cue + phase-1..${phase}.cue"
  echo "══════════════════════════════════════════════════════"
  echo ""

  # Quick plan preview
  CREST_SPEC_SPEC_DIR="$SPEC_DIR" "$CLI" plan 2>/dev/null || true
  echo ""

  # Launch interactive Claude session
  (cd "$WORK_DIR" && claude "$prompt")

  echo ""
  echo "Phase ${phase} complete."
  echo ""
done

echo "All phases complete."
