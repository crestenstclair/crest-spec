#!/bin/bash
# Run crest-spec agent sessions through all 10 crest-synth phases.
# Each phase gets its own interactive Claude session that drives the agent CLI.
# State carries over between phases so the planner detects diffs.
#
# Usage: ./scripts/run-phased-agent.sh [start_phase] [end_phase]
#   start_phase: first phase to run (default: 1)
#   end_phase:   last phase to run (default: 10)

set -euo pipefail

cd "$(dirname "$0")/../fixtures/crest-synth"

START=${1:-1}
END=${2:-10}

CLI="bun ../../src/cli/main.ts"

# Clean up all generated artifacts so every run starts fresh
echo "Cleaning generated artifacts..."
rm -rf src/ tests/ target/
rm -f Cargo.toml Cargo.lock Makefile tone-test.wav
rm -f crest-spec.db crest-spec.db-shm crest-spec.db-wal
echo "Clean."
echo ""

PROMPT_TEMPLATE='You are driving a crest-spec agent session for phase PHASE_NUM of the crest-synth project.

The spec file is: SPEC_PATH

## Commands available

- `bun ../../src/cli/main.ts agent begin --spec SPEC_PATH` — start the session
- `bun ../../src/cli/main.ts agent next` — get the next resource(s) to implement
- `bun ../../src/cli/main.ts agent context <resource-id> --spec SPEC_PATH` — get the scoped prompt for a resource
- `bun ../../src/cli/main.ts agent validate <resource-id> --spec SPEC_PATH` — validate files on disk
- `bun ../../src/cli/main.ts agent note <resource-id> "<text>"` — save a note for downstream agents
- `bun ../../src/cli/main.ts agent commit <resource-id> --spec SPEC_PATH` — commit a resource to state
- `bun ../../src/cli/main.ts agent finish` — finalize the session

## Your job

1. Run `agent begin` to start the session and see the plan.
2. Run `agent next` to get the current wave of resources.
3. For each resource in the wave:
   a. Run `agent context <id>` to get the generation prompt.
   b. Use the system prompt and resource prompt to generate the code files. Write them to disk at the paths specified in the prompt output format.
   c. Run `agent note <id> "<decisions and patterns used>"` to record context for downstream resources.
   d. Run `agent commit <id>` to record the resource in state.
4. Run `agent next` again. If not done, repeat step 3. If done, continue.
5. Run `agent finish` to finalize.

Work through every resource. Do not skip any.'

for phase in $(seq "$START" "$END"); do
  spec="phases/crest-spec-phase-${phase}.ts"

  if [ ! -f "$spec" ]; then
    echo "Phase ${phase}: spec file not found: ${spec}" >&2
    exit 1
  fi

  prompt="${PROMPT_TEMPLATE//PHASE_NUM/$phase}"
  prompt="${prompt//SPEC_PATH/$spec}"

  echo "══════════════════════════════════════════════════════"
  echo "  Phase ${phase} / ${END}"
  echo "  Spec: ${spec}"
  echo "══════════════════════════════════════════════════════"
  echo ""

  claude "$prompt"

  echo ""
  echo "Phase ${phase} complete."
  echo ""
done

echo "All phases complete."
