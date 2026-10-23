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

if [ "$START" -eq 1 ]; then
  echo "Starting from phase 1 — cleaning generated artifacts..."
  rm -rf src/ tests/ target/
  rm -f Cargo.toml Cargo.lock Makefile tone-test.wav
  rm -f crest-spec.db crest-spec.db-shm crest-spec.db-wal
  echo "Clean."
else
  echo "Resuming from phase ${START} — keeping existing state and files."
fi
echo ""

PROMPT_TEMPLATE='You are an orchestrator driving a crest-spec agent session for phase PHASE_NUM of the crest-synth project.

The spec file is: SPEC_PATH

## Commands available

- `bun ../../src/cli/main.ts agent begin --spec SPEC_PATH` — start the session and see the plan
- `bun ../../src/cli/main.ts agent next` — get the next wave of resources to implement
- `bun ../../src/cli/main.ts agent context <resource-id> --spec SPEC_PATH` — get the scoped prompt for a resource
- `bun ../../src/cli/main.ts agent dispatch <resource-id>` — mark a resource as dispatched to a sub-agent
- `bun ../../src/cli/main.ts agent complete <resource-id>` — mark a resource as completed by its sub-agent
- `bun ../../src/cli/main.ts agent validate <resource-id> --spec SPEC_PATH` — validate files on disk
- `bun ../../src/cli/main.ts agent note <resource-id> "<text>"` — save a note for downstream agents
- `bun ../../src/cli/main.ts agent commit <resource-id> --spec SPEC_PATH` — commit a resource to state
- `bun ../../src/cli/main.ts agent block <resource-id> "<reason>"` — mark a resource as blocked
- `bun ../../src/cli/main.ts agent error <resource-id> "<message>"` — mark a resource as errored
- `bun ../../src/cli/main.ts agent status` — check status of all resources in the current wave
- `bun ../../src/cli/main.ts agent finish` — finalize the session

## Your job — orchestrate sub-agents

You are the ORCHESTRATOR. You do NOT write code yourself. You dispatch sub-agents to generate code for each resource. Each sub-agent gets only the scoped context it needs.

### Wave loop

1. Run `agent begin` to start the session and see the plan.
2. Run `agent next` to get the current wave of resources.
3. For each resource in the wave, dispatch a sub-agent:
   a. Run `agent context <id>` to get the scoped generation prompt.
   b. Run `agent dispatch <id>` to mark it as dispatched.
   c. Spawn a sub-agent using the Agent tool. Give it:
      - The generation prompt from step (a) as its instructions
      - A clear directive: "Write the code files to disk at the paths specified in the prompt. Do not skip any file."
      - The working directory context (we are in fixtures/crest-synth/)
   d. When the sub-agent completes, run `agent complete <id>`.
   e. Run `agent validate <id>` to check the output.
   f. If validation fails, you may re-dispatch with corrections or mark as errored.
   g. Run `agent note <id> "<decisions and patterns the sub-agent used>"` to record context for downstream resources.
   h. Run `agent commit <id>` to record the resource in state.
4. After the wave is done, run `agent status` to check for issues.
5. Run `agent next` again. If there are more waves, repeat step 3. If done, continue.
6. Run `agent finish` to finalize.

### Sub-agent dispatch rules

- Dispatch resources within the same wave in PARALLEL using multiple Agent tool calls in one message where possible.
- Each sub-agent should be self-contained — give it the full context prompt, not a reference to "the earlier output."
- If a sub-agent reports it is blocked (needs a decision), run `agent block <id> "<reason>"` and handle it before proceeding.
- If a sub-agent fails, run `agent error <id> "<message>"`. You may retry by re-dispatching.
- NEVER write code yourself. Always delegate to a sub-agent.

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

  claude --dangerously-skip-permissions "$prompt"

  echo ""
  echo "Phase ${phase} complete."
  echo ""
done

echo "All phases complete."
