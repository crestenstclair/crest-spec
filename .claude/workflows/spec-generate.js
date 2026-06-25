export const meta = {
  name: 'spec-generate',
  description: 'Drive a crest-spec generation session with behavioral verification: design → tasks → generate → verify',
  whenToUse: 'After spec_begin has produced a session. Pass {sessionId, model?, maxRetries?} as args.',
  phases: [
    { title: 'Design', detail: 'derive observable contracts per bounded context in parallel' },
    { title: 'Tasks', detail: 'decompose context contracts into resource tasks with behavioral checks in parallel' },
    { title: 'Wave', detail: 'one generator agent per resource, retry loop inside the agent' },
    { title: 'Verify', detail: 'independent verifier agents run witness harnesses per behavioral check; failures cycle back into generation' },
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

// ─── Schemas ──────────────────────────────────────────────────────────────────

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

const CONTEXTS_SCHEMA = {
  type: 'object',
  properties: {
    contexts: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          name: { type: 'string' },
          resources: { type: 'array', items: { type: 'string' } },
        },
        required: ['name', 'resources'],
      },
    },
  },
  required: ['contexts'],
}

const DESIGN_SCHEMA = {
  type: 'object',
  properties: {
    context_name: { type: 'string' },
    committed: { type: 'boolean' },
    contract_json: { type: 'string' },
    error: { type: 'string' },
  },
  required: ['context_name', 'committed'],
}

const TASKS_SCHEMA = {
  type: 'object',
  properties: {
    resource_id: { type: 'string' },
    committed: { type: 'boolean' },
    task_count: { type: 'number' },
    check_count: { type: 'number' },
    error: { type: 'string' },
  },
  required: ['resource_id', 'committed'],
}

const CHECKS_SCHEMA = {
  type: 'object',
  properties: {
    checks: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          check_id: { type: 'string' },
          behavior: { type: 'string' },
          check_json: { type: 'string' },
        },
        required: ['check_id', 'behavior'],
      },
    },
  },
  required: ['checks'],
}

const CHECK_VERIFY_SCHEMA = {
  type: 'object',
  properties: {
    check_id: { type: 'string' },
    passed: { type: 'boolean' },
    theater: { type: 'boolean' },
    reason: { type: 'string' },
    graduated: { type: 'boolean' },
  },
  required: ['check_id', 'passed'],
}

// ─── Prompt builders ──────────────────────────────────────────────────────────

function designPrompt(ctx) {
  return `You are a crest-spec behavioral design sub-agent for bounded context "${ctx.name}" (session ${sessionId}).
Resources in this context: ${ctx.resources.join(', ')}

Your task: derive an observable CONTRACT for this bounded context — the interface surface and behaviors that constitute "correct" operation, expressed as measurable predicates.

Steps:
1. ToolSearch "select:mcp__crest-spec__spec_inspect,mcp__crest-spec__spec_design_commit"
2. For each resource in this context, call spec_inspect to read its spec declaration (interface, invariants, what it must do).
3. Synthesize a contract JSON object:
   {
     "surfaces": [{"name": string, "kind": "func"|"method"|"type"|"endpoint", "signature": string}],
     "behaviors": [{
       "description": string,
       "predicates": [{"field": string, "op": "eq"|"gt"|"gte"|"lt"|"lte"|"range"|"count"|"distinct_count"|"differ"|"monotonic"|"set_contains", "value"?: number, "min"?: number, "max"?: number, "member"?: any}]
     }]
   }
   "field" is a quantity measurable by calling the surface (e.g. "return_value", "error", "item_count", "latency_ms").
   Include ONLY externally observable behaviors — no internal state claims.
4. Call spec/design_commit with {context_name: "${ctx.name}", contract_json: JSON.stringify(contract)}.
5. Return: {context_name: "${ctx.name}", committed: true/false, contract_json: "<the contract JSON as a string>", error: "<if any>"}.`
}

function tasksPrompt(resourceId, contextName, contractJson) {
  return `You are a crest-spec behavioral tasks sub-agent for resource "${resourceId}" (session ${sessionId}).
Bounded context: "${contextName}". Context observable contract:
${contractJson}

Your task: decompose the contract into resource-level tasks, each carrying BEHAVIORAL CHECKS using the check predicate vocabulary.

Steps:
1. ToolSearch "select:mcp__crest-spec__spec_inspect,mcp__crest-spec__spec_tasks_commit"
2. Call spec_inspect for "${resourceId}" to understand its specific interface within the context.
3. For each contract behavior that this resource is responsible for, produce a task:
   {
     description: "<what the resource must implement — actionable for a generator>",
     checks: [{
       behavior: "<human-readable behavioral claim>",
       check: {
         behavior: "<same claim>",
         predicates: [{"field": string, "op": string, "value"?: number, "min"?: number, "max"?: number, "member"?: any}]
       }
     }]
   }
   Field names must be quantities measurable from the resource's public interface.
   Valid ops: eq, gt, gte, lt, lte, range, count, distinct_count, differ, monotonic, set_contains.
4. Call spec/tasks_commit with {resource_id: "${resourceId}", tasks: [...]}.
5. Return: {resource_id: "${resourceId}", committed: true/false, task_count: N, check_count: N, error: "<if any>"}.`
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

function verifierPrompt(resourceId, checkId, behavior, checkJson) {
  return `You are a crest-spec behavioral VERIFIER for check "${checkId}" on resource "${resourceId}" (session ${sessionId}).

You are INDEPENDENT of the generator — you verify observable behavior through the public interface only. Do NOT read committed implementation files.

Behavior to verify: ${behavior}
Check definition (JSON): ${checkJson}

The check.predicates define WHAT to measure (each "field" is a quantity observable from calling the interface, each "op"+"value" defines the pass condition).

Steps:
1. ToolSearch "select:mcp__crest-spec__spec_inspect,mcp__crest-spec__spec_verify,mcp__crest-spec__spec_graduate"
2. Call spec_inspect for "${resourceId}" to learn the public interface (types, functions, endpoints) from the spec declaration — do NOT read committed source files.
3. Write a REAL WITNESS at /tmp/crest_witness_${checkId}/main.go (or appropriate extension for the project language). The witness must:
   a. Call the resource's public interface in a realistic scenario exercising "${behavior}".
   b. Measure each predicate field and collect the values into a JSON object.
   c. Print exactly one line to stdout: CREST_OBS:{"field1":value1,"field2":value2,...}
4. Write a STUB BASELINE at /tmp/crest_stub_${checkId}/main.go. The stub must:
   a. Use a degenerate no-op implementation (returns zero/nil/empty/identity for all calls) of the same interface.
   b. Make the identical measurements as the witness.
   c. Print exactly one line: CREST_STUB:{"field1":value1,"field2":value2,...}
   The stub must genuinely fail the behavioral check — if the stub also passes, the check is theater (non-discriminating).
5. Run both programs and capture stdout. Extract the JSON from the CREST_OBS: and CREST_STUB: lines by stripping those prefixes and parsing the remainder.
6. Call spec/verify with {check_id: "${checkId}", real_observation: <parsed CREST_OBS object>, stub_observation: <parsed CREST_STUB object>}.
7. If spec/verify returns passed=true: call spec/graduate with {check_id: "${checkId}"}.
   If passed=false or theater=true: do NOT graduate.
8. Return: {check_id: "${checkId}", passed: <bool>, theater: <bool, default false>, reason: "<from spec/verify>", graduated: <true only if spec/graduate was called and succeeded>}.`
}

// ─── Phase 0: DESIGN ─────────────────────────────────────────────────────────
// Discover bounded contexts from the resource graph, then derive observable
// contracts in parallel — one design agent per context.

log('Design phase: discovering bounded contexts...')
const discoveryResult = await agent(
  `You are discovering bounded contexts for a crest-spec session (session ${sessionId}).

Steps:
1. ToolSearch "select:mcp__crest-spec__spec_sql,mcp__crest-spec__spec_graph"
2. Call spec_sql with query: SELECT resource_id FROM resources ORDER BY resource_id
   (If resources table does not exist or query fails, return {"contexts": []}.)
3. Optionally call spec_graph with {format: "json"} to see dependency structure.
4. Group resource_ids into bounded contexts by naming convention and dependency clusters.
   Example: resources sharing a package or module prefix (e.g. "internal/store/...") form one context.
   Each resource must appear in exactly one context. Name contexts by their domain (e.g. "store", "handler", "cmd").
5. Return: {"contexts": [{"name": "<context-name>", "resources": ["<resource_id>", ...]}]}`,
  { label: 'discover-contexts', phase: 'Design', schema: CONTEXTS_SCHEMA }
)
const contexts = (discoveryResult?.contexts || []).filter(c => c.resources && c.resources.length > 0)
log(`Design phase: ${contexts.length} bounded context(s) discovered`)

// Map: context_name → contract_json (populated from committed design agents)
const designMap = {}

if (contexts.length > 0) {
  const designResults = await parallel(contexts.map(ctx => () =>
    agent(designPrompt(ctx), {
      label: `design:${ctx.name}`,
      phase: 'Design',
      model,
      schema: DESIGN_SCHEMA,
    })
  ))
  for (const d of designResults.filter(Boolean)) {
    if (d.committed && d.contract_json) {
      designMap[d.context_name] = d.contract_json
    }
  }
  log(`Design phase: ${Object.keys(designMap).length}/${contexts.length} context contracts committed`)
} else {
  log('Design phase: no contexts found — skipping behavioral contract derivation')
}

// ─── Phase 1: TASKS ──────────────────────────────────────────────────────────
// Flatten contexts → resources, run tasks agents in parallel.
// Resources without a committed design contract are skipped (pure generation path).

let totalChecks = 0
if (Object.keys(designMap).length > 0) {
  log('Tasks phase: decomposing contracts into resource-level tasks...')
  const resourceTaskPairs = contexts
    .filter(ctx => designMap[ctx.name])
    .flatMap(ctx => ctx.resources.map(rid => ({
      resource_id: rid,
      context_name: ctx.name,
      contract_json: designMap[ctx.name],
    })))

  if (resourceTaskPairs.length > 0) {
    const taskResults = await parallel(resourceTaskPairs.map(r => () =>
      agent(tasksPrompt(r.resource_id, r.context_name, r.contract_json), {
        label: `tasks:${r.resource_id}`,
        phase: 'Tasks',
        model,
        schema: TASKS_SCHEMA,
      })
    ))
    const committedTasks = taskResults.filter(Boolean).filter(t => t.committed)
    totalChecks = committedTasks.reduce((sum, t) => sum + (t.check_count || 0), 0)
    log(`Tasks phase: ${committedTasks.length}/${resourceTaskPairs.length} resources had tasks committed (${totalChecks} behavioral check(s) total)`)
  }
} else {
  log('Tasks phase: skipped (no committed design contracts)')
}

// ─── Phase 2 + 3: GENERATE + VERIFY (wave loop) ──────────────────────────────
// Standard wave loop: spec_next → parallel generate → behavioral verify →
// project-level verify → triage. Behavioral verify uses SEPARATE verifier
// agents (not generators) to prevent theater.

const triaged = []
const graduated = []
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

  // ── Behavioral VERIFY pass ─────────────────────────────────────────────────
  // For each resource that committed, list its pending behavioral checks and run
  // a SEPARATE verifier agent per check. Verifiers are independent of generators:
  // they test via the public interface only, author real+stub harnesses, and call
  // spec/verify (fail-closed: real must pass AND stub must fail). Failures feed
  // back via spec_resolve, cycling the resource into the next wave pass.
  const committedOutcomes = outcomes.filter(Boolean).filter(o => o.outcome === 'committed')

  for (const committed of committedOutcomes) {
    const checksResult = await agent(
      `ToolSearch "select:mcp__crest-spec__spec_sql"
Call spec_sql with query:
  SELECT c.id AS check_id, c.behavior, c.check_json
  FROM checks c
  JOIN tasks t ON c.task_id = t.id
  WHERE t.resource_id = '${committed.resource_id}' AND c.state = 'pending'
If the checks or tasks table does not exist (behavioral pipeline tables not yet migrated), return {"checks": []}.
Return: {"checks": [{"check_id": string, "behavior": string, "check_json": string}]}`,
      { label: `list-checks:${committed.resource_id}`, phase: 'Verify', schema: CHECKS_SCHEMA }
    )
    const checks = (checksResult?.checks || []).filter(Boolean)

    if (checks.length === 0) continue

    log(`Wave ${wave.wave_index}: verifying ${checks.length} behavioral check(s) for ${committed.resource_id}`)

    // Verifier agents run in parallel, independent of the generator that wrote the files.
    const verifyResults = await parallel(checks.map(c => () =>
      agent(verifierPrompt(committed.resource_id, c.check_id, c.behavior, c.check_json), {
        label: `verify:${c.check_id}`,
        phase: 'Verify',
        model,
        schema: CHECK_VERIFY_SCHEMA,
      })
    ))

    const passedChecks = verifyResults.filter(Boolean).filter(v => v.passed)
    const failedChecks = verifyResults.filter(Boolean).filter(v => !v.passed)

    if (passedChecks.length > 0) {
      for (const v of passedChecks) {
        graduated.push({ resource_id: committed.resource_id, check_id: v.check_id, graduated: v.graduated })
      }
      log(`Wave ${wave.wave_index}: ${passedChecks.length}/${checks.length} behavioral check(s) passed for ${committed.resource_id}`)
    }

    if (failedChecks.length > 0) {
      // Summarise all failures for the resolve guidance. Theater = stub also
      // passed = check is non-discriminating; real-fail = implementation wrong.
      const failureSummary = failedChecks
        .map(v => `- [${v.check_id}]${v.theater ? ' THEATER (stub also passed — check predicate is non-discriminating):' : ' FAIL:'} ${v.reason || 'verification failed'}`)
        .join('\n')
      log(`Wave ${wave.wave_index}: ${failedChecks.length} behavioral check(s) failed for ${committed.resource_id} — scheduling for regeneration`)
      await agent(
        `ToolSearch "select:mcp__crest-spec__spec_resolve"
Resource "${committed.resource_id}" committed files in session ${sessionId} but FAILED behavioral verification:
${failureSummary}

Call spec_resolve with:
  session_id: "${sessionId}"
  resource_id: "${committed.resource_id}"
  guidance: "Behavioral checks failed post-generation. The implementation must satisfy these observable behaviors (measured via public interface, not internal state):\\n${failureSummary.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n')}"

This resets the resource to pending so the next wave re-generates with the failure context injected into spec_context.
Report: resolved=true/false.`,
        { label: `verify-resolve:${committed.resource_id}`, phase: 'Verify' }
      )
      triaged.push({ resource_id: committed.resource_id, action: 'behavioral-verify-failed', failures: failedChecks })
    }
  }

  // ── Post-wave project-level verification (compile / test / fmt) ─────────────
  // Runs after behavioral verify so both feedback types are injected before
  // the next spec_next call. When verify resolves resources back to pending,
  // spec_next re-serves the SAME wave_index next pass — stall guard bounds these loops.
  const committedCount = committedOutcomes.length
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

  // ── Triage non-committed outcomes ──────────────────────────────────────────
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
  graduated,
  behavioral_checks_total: totalChecks,
  next_steps: 'Call spec_finish (main session). If FinishResult.reflection_prompt is non-empty, run it with a sonnet agent and submit the output via spec_record_learnings. Review the "graduated" list — each entry is a behavioral check the engine confirmed; author the checks back into the spec invariants via the spec-authoring skill.',
}
