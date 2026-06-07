#!/bin/bash
# Run crest-spec agent sessions through all 10 crest-synth phases.
# Each phase gets its own interactive Claude session via `crest-spec run`.
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

  # Launch generation session via crest-spec run
  (cd "$WORK_DIR" && "$CLI" run --spec-dir "$SPEC_DIR")

  echo ""
  echo "Phase ${phase} complete."
  echo ""
done

echo "All phases complete."
