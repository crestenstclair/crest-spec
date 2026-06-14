export const meta = {
  name: 'spec-generate',
  description: 'Drive a crest-spec generation session: waves of sub-agents generate, commit, and retry against server-side validations',
  whenToUse: 'After spec_begin has produced a session. Pass {sessionId, model?, maxRetries?} as args.',
  phases: [
    { title: 'Wave', detail: 'one generator agent per resource, retry loop inside the agent' },
    { title: 'Verify', detail: 'post-wave project-level verification (type check, tests, project validations)' },
    { title: 'Triage', detail: 'resolve or skip resources still failing after retries' },
  ],
}

// args: { sessionId: string, model?: string, maxRetries?: number }
// Tolerate args arriving as a JSON-encoded string (harness serialization quirk).
const input = typeof args === 'string' ? JSON.parse(args) : (args || {})
const sessionId = input.sessionId
if (!sessionId) throw new Error('spec-generate requires args.sessionId (run spec_begin first)')
const model = input.model || 'sonnet'          // NEVER haiku
const maxRetries = input.maxRetries ?? 3

const WAVE_SCHEMA = {
  type: 'object',
  properties: {
    done: { type: 'boolean' },
    wave_index: { type: 'number' },
    resources: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          resource_id: { type: 'string' },
          attempts: { type: 'number' },
          last_error: { type: 'string' },
        },
        required: ['resource_id'],
      },
    },
  },
  required: ['done'],
}

const OUTCOME_SCHEMA = {
  type: 'object',
  properties: {
    resource_id: { type: 'string' },
    outcome: { type: 'string', enum: ['committed', 'rejected', 'skipped', 'error'] },
    attempts: { type: 'number' },
    error: { type: 'string' },
    files: { type: 'array', items: { type: 'string' } },
  },
  required: ['resource_id', 'outcome'],
}

const VERIFY_SCHEMA = {
  type: 'object',
  properties: {
    passed: { type: 'boolean' },
    resolved: { type: 'array', items: { type: 'string' } },
    unattributed: { type: 'array', items: { type: 'string' } },
  },
  required: ['passed'],
}

function generatorPrompt(resourceId, waveIndex) {
  return `You are a crest-spec generation sub-agent for resource "${resourceId}" (session ${sessionId}, wave ${waveIndex}).

Load the crest-spec MCP tools first:
ToolSearch "select:mcp__crest-spec__spec_context,mcp__crest-spec__spec_commit"

Then run this loop (at most ${maxRetries + 1} attempts):
1. Call spec_context with {session_id: "${sessionId}", resource_id: "${resourceId}"}.
   It returns SystemPrompt, Prompt, and Invariants (each invariant is
   {text, rationale}). Treat SystemPrompt as your role and follow Prompt
   exactly — it contains the resource declaration, dependencies, existing
   files (UPDATE mode), and any prior failure context.
2. Author the files the prompt asks for (full file contents, correct paths
   relative to the project root). Follow the prompt's folder structure and
   style rules. Do NOT create files the prompt doesn't call for. Before
   committing, if the project has a formatter (cargo fmt, gofmt, prettier),
   run it over your files and commit the formatted content — tree-wide
   format checks run after the wave and a diff costs a full retry loop.
3. Judge EACH invariant from the context against your files, producing
   {invariant, passed, summary} where "invariant" is the invariant's text
   field verbatim. Be honest — a wrong "passed" will fail wave validation
   later and cost another round trip.
4. Call spec_commit with {session_id, resource_id, files: [{path, content}],
   model: "${model}", notes: <one-line design note>, invariant_checks: [...]}.
5. If the result has Committed=true → stop, report outcome "committed".
   If Committed=false → read result.Validations for the failure, go back to
   step 1 (the new context includes the failure) and fix the actual problem.
6. If still rejected after ${maxRetries + 1} attempts, report outcome
   "rejected" with the final error message. Do not call spec_skip yourself.

Your final message is parsed as data: report resource_id, outcome, attempts,
error (last validation message, if any), and the file paths you committed.`
}

const triaged = []
let waveCount = 0
// Stall guard: 'rejected' is not a terminal state server-side, so if a wave's
// post-verify keeps resetting resources to pending, spec_next would re-serve the
// same wave forever. Two-tier escalation:
//   - SOFT (stallCount > MAX_STALLS): skip ONLY genuine culprits — resources that
//     have NEVER produced a committed file (zero generated_files). Resources that
//     already committed and were merely reset as collateral are LEFT to regenerate;
//     once the real culprit is skipped the wave converges. This is the fix for the
//     long-standing over-skip bug where a healthy, already-committed resource
//     (e.g. a port/aggregate that committed 3x) got force-skipped as collateral.
//   - HARD (stallCount > MAX_STALLS_HARD): last resort — skip every remaining
//     non-committed resource to guarantee the run terminates.
let lastWaveIndex = -1
let stallCount = 0
const MAX_STALLS = 2
const MAX_STALLS_HARD = 5

while (true) {
  const wave = await agent(
    `Load the crest-spec MCP tools (ToolSearch "select:mcp__crest-spec__spec_next"), call spec_next with {session_id: "${sessionId}"}, and return its result: done, wave_index, and resources (resource_id, attempts, last_error — last_error comes from each resource's Error.Message if set).`,
    { label: 'spec_next', phase: 'Wave', schema: WAVE_SCHEMA },
  )
  if (!wave || wave.done) break
  const resources = (wave.resources || []).filter(Boolean)
  if (resources.length === 0) break

  if (wave.wave_index === lastWaveIndex) {
    stallCount++
    if (stallCount > MAX_STALLS) {
      const hard = stallCount > MAX_STALLS_HARD
      const verdict = await agent(
        `Wave ${wave.wave_index} of crest-spec session ${sessionId} has repeated ${stallCount} times — some resource(s) won't converge. Load ToolSearch "select:mcp__crest-spec__spec_skip,mcp__crest-spec__spec_sql". The still-pending resources are: ${resources.map(r => r.resource_id).join(', ')}.\n\n` +
        (hard
          ? `This is the HARD last resort. spec_skip EVERY one of those resources (reason: "auto-skipped: wave unresolved after ${stallCount} passes") so the run can terminate. Report the skipped ids.`
          : `Skip ONLY genuine culprits — resources that have never produced committed output. For EACH resource run spec_sql: SELECT COUNT(*) AS n FROM generated_files WHERE resource_id='<id>'. If n = 0 (never committed any file), spec_skip it (reason: "auto-skipped: no committed output after ${stallCount} passes"). If n > 0, DO NOT skip it — it already produced valid output and is only stuck as collateral; leave it pending so it regenerates once the real culprit is gone. Report which ids you skipped and which you left.`),
        { label: `stall-guard:wave-${wave.wave_index}`, phase: 'Triage' },
      )
      triaged.push({ resource_id: `wave-${wave.wave_index}`, action: verdict })
      log(`Wave ${wave.wave_index}: stall guard fired (pass ${stallCount}, ${hard ? 'HARD skip-all' : 'soft skip-culprits-only'})`)
      continue
    }
  } else {
    lastWaveIndex = wave.wave_index
    stallCount = 0
  }
  waveCount++
  log(`Wave ${wave.wave_index}: ${resources.length} resource(s)`)

  const outcomes = await parallel(resources.map(r => () =>
    agent(generatorPrompt(r.resource_id, wave.wave_index), {
      label: `gen:${r.resource_id}`,
      phase: 'Wave',
      model,
      schema: OUTCOME_SCHEMA,
    })
  ))

  // Post-wave verification: run project-level checks only if something committed.
  // When verify resolves resources back to pending, spec_next re-serves the SAME
  // wave_index next pass — so the stall guard above also bounds these verify-loops.
  const committedCount = outcomes.filter(Boolean).filter(o => o.outcome === 'committed').length
  if (committedCount > 0) {
    const verify = await agent(
      `Load the crest-spec MCP tools (ToolSearch "select:mcp__crest-spec__spec_verify_wave,mcp__crest-spec__spec_resolve,mcp__crest-spec__spec_sql"), then call spec_verify_wave with {session_id: "${sessionId}", wave_index: ${wave.wave_index}}. If Passed is true, report "wave verified". If Passed is false, attribute each failure YOURSELF before resolving — the server's ResourceID field is a loose heuristic and routinely pins tree-wide fmt/clippy/test failures on the wrong resource. For each error: read the Message, find the file path(s) actually failing (rustfmt diffs and compiler errors name their files), then map each file to its owning resource with spec_sql: SELECT resource_id FROM generated_files WHERE path LIKE '%<filename>%'. Call spec_resolve with {session_id: "${sessionId}", resource_id: <the OWNING resource>, guidance: <the exact failure for that file, condensed to what the regenerating agent must fix>}. Never resolve a resource for a failure that is not in its own files, and never resolve the same resource twice for an identical message — if the owner is not in this session or you cannot place a failure, report it as unattributed instead. Report: passed true/false, which resources you resolved, and any unattributed errors.`,
      { label: `verify:wave-${wave.wave_index}`, phase: 'Verify', schema: VERIFY_SCHEMA },
    )
    if (verify && verify.passed === false) {
      triaged.push({ resource_id: `wave-${wave.wave_index}`, action: verify })
      log(`Wave ${wave.wave_index}: verification failed — ${ (verify.resolved || []).length } resource(s) reset for regeneration`)
    }
  }

  const failed = outcomes.filter(Boolean).filter(o => o.outcome !== 'committed')
  for (const f of failed) {
    // One triage agent per failure: decide resolve-with-guidance vs skip.
    const verdict = await agent(
      `Resource "${f.resource_id}" in crest-spec session ${sessionId} failed generation after ${f.attempts ?? '?'} attempts. Last error:\n${f.error || '(none reported)'}\n\nLoad tools: ToolSearch "select:mcp__crest-spec__spec_resolve,mcp__crest-spec__spec_skip,mcp__crest-spec__spec_history"\n\nInspect spec_history for the resource if helpful. If the failure looks fixable with concrete guidance (a specific API misuse, a missing import pattern, a misread of the spec), call spec_resolve with {session_id: "${sessionId}", resource_id: "${f.resource_id}", guidance: <specific, actionable guidance>} — this resets the resource to pending so the next wave pass retries it. If it looks structurally impossible (contradictory spec, missing dependency), call spec_skip with a reason. You MUST actually invoke exactly one of spec_resolve or spec_skip before finishing — a prose verdict alone leaves the resource stuck. Report which you chose and why.`,
      { label: `triage:${f.resource_id}`, phase: 'Triage' },
    )
    triaged.push({ resource_id: f.resource_id, action: verdict })
  }
  // Loop continues: spec_next re-serves resolved (pending) resources in the
  // same wave, or advances when the wave is terminal.
}

return {
  waves_processed: waveCount,
  triaged,
  next_steps: 'Call spec_finish (main session). If FinishResult.reflection_prompt is non-empty, run it with a sonnet agent and submit the output via spec_record_learnings.',
}
