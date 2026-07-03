export const meta = {
  name: 'spec-verify-pending',
  description: 'Drain ALL remaining pending behavioral checks for a session via independent witness/stub verification (no generation). Pass {sessionId, model?}.',
  whenToUse: 'After generation is complete but behavioral checks for already-committed resources remain pending (e.g. a resumed session whose wave loop only verified freshly-generated resources).',
  phases: [
    { title: 'Discover', detail: 'list every pending behavioral check across all resources' },
    { title: 'Verify', detail: 'one independent verifier agent per check: real witness + stub baseline + spec/verify' },
    { title: 'Reconcile', detail: 'read authoritative engine state for the final breakdown' },
  ],
}

const input = typeof args === 'string' ? JSON.parse(args) : (args || {})
const sessionId = input.sessionId
if (!sessionId) throw new Error('spec-verify-pending requires args.sessionId')
const model = input.model || 'sonnet'

const PENDING_SCHEMA = {
  type: 'object',
  properties: {
    checks: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          check_id: { type: 'string' },
          resource_id: { type: 'string' },
          behavior: { type: 'string' },
          check_json: { type: 'string' },
        },
        required: ['check_id', 'resource_id', 'behavior'],
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

const BREAKDOWN_SCHEMA = {
  type: 'object',
  properties: {
    states: {
      type: 'array',
      items: {
        type: 'object',
        properties: { state: { type: 'string' }, count: { type: 'number' } },
        required: ['state', 'count'],
      },
    },
  },
  required: ['states'],
}

// Authoritative per-check state read after verification, used to detect
// 'malformed' checks (structurally invalid predicate — the CHECK's fault,
// not the code's). Malformed checks are BLOCKERS: spec/finish refuses to
// finalize the session while they exist. This workflow surfaces them for
// re-authoring (spec-generate tasks stage); it never waves them through.
const RECONCILE_SCHEMA = {
  type: 'object',
  properties: {
    checks: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          check_id: { type: 'string' },
          state: { type: 'string' },
          reason: { type: 'string' },
        },
        required: ['check_id'],
      },
    },
  },
  required: ['checks'],
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
SCENE-RUNNER PREFERENCE: if the project ships a scene runner binary
(src/bin/scene_run.rs — check with spec/sql: SELECT 1 FROM generated_files
WHERE path LIKE '%scene_run%'), PREFER it as the witness: author a scene
file exercising the behavior, run scene_run --scene <file>, and map the
CREST_OBS fields from the emitted snapshot JSON / summary line. The stub
baseline is the same scene evaluated against a degenerate no-op reducer
shape (never the real crate). Fall back to a bespoke harness crate only
when the behavior is not reachable through applied events.
3. Write a REAL WITNESS at /tmp/crest_witness_${checkId}/main.go (or appropriate extension for the project language). The witness must:
   a. Call the resource's public interface in a realistic scenario exercising "${behavior}".
   b. Measure each predicate field and collect the values into a JSON object.
   c. Print exactly one line to stdout: CREST_OBS:{"field1":value1,"field2":value2,...}
4. Write a STUB BASELINE at /tmp/crest_stub_${checkId}/main.go. The stub must:
   a. Use a degenerate no-op implementation (returns zero/nil/empty/identity for all calls) of the same interface.
   b. Make the identical measurements as the witness.
   c. Print exactly one line: CREST_STUB:{"field1":value1,"field2":value2,...}
   The stub must genuinely fail the behavioral check — if the stub also passes, the check is theater (non-discriminating).
   IMPORTANT: CREST_STUB MUST contain the SAME predicate fields as CREST_OBS. spec/verify is strict and fail-closed: it REJECTS a verification whose stub_observation is empty or missing any predicate field (returns passed:false, reason contains "malformed verification"). A missing or malformed CREST_OBS / CREST_STUB line is a verification FAILURE, not a skip — never fabricate observations.
5. Run both programs and capture stdout. Extract the JSON from the CREST_OBS: and CREST_STUB: lines by stripping those prefixes and parsing the remainder. If either line is absent or unparseable, do NOT invent values — the behavior is unverified and the check must fail; report passed:false and stop.
6. Call spec/verify with {check_id: "${checkId}", real_observation: <parsed CREST_OBS object>, stub_observation: <parsed CREST_STUB object>}. You MUST call spec/verify for the check to count — a self-reported pass without a real spec/verify call is treated as a failure.
7. If spec/verify returns passed=true: call spec/graduate with {check_id: "${checkId}"}.
   If passed=false or theater=true: do NOT graduate.
8. Return: {check_id: "${checkId}", passed: <bool>, theater: <bool, default false>, reason: "<from spec/verify>", graduated: <true only if spec/graduate was called and succeeded>}.`
}

// ─── Discover ────────────────────────────────────────────────────────────────
log('Discovering all pending behavioral checks...')
const pending = await agent(
  `ToolSearch "select:mcp__crest-spec__spec_sql"
Call spec_sql with query:
  SELECT c.id AS check_id, t.resource_id AS resource_id, c.behavior, c.check_json
  FROM checks c JOIN tasks t ON c.task_id = t.id
  WHERE c.state = 'pending'
  ORDER BY t.resource_id
Return ALL rows: {"checks": [{"check_id": string, "resource_id": string, "behavior": string, "check_json": string}]}`,
  { label: 'discover-pending', phase: 'Discover', schema: PENDING_SCHEMA }
)
const checks = (pending?.checks || []).filter(Boolean)
log(`Discover: ${checks.length} pending check(s) to verify`)

// ─── Verify (parallel, concurrency-capped) ───────────────────────────────────
// One independent verifier per pending check. Their self-report isn't trusted
// for control flow; spec/verify writes the authoritative state. Running the
// agent triggers the witness/stub/verify work. Reconciliation below reads truth.
if (checks.length > 0) {
  await parallel(checks.map(c => () =>
    agent(verifierPrompt(c.resource_id, c.check_id, c.behavior, c.check_json), {
      label: `verify:${c.resource_id}:${String(c.check_id).slice(0, 8)}`,
      phase: 'Verify',
      model,
      schema: CHECK_VERIFY_SCHEMA,
    })
  ))
}

// ─── Reconcile ───────────────────────────────────────────────────────────────
const breakdown = await agent(
  `ToolSearch "select:mcp__crest-spec__spec_sql"
Call spec_sql with query: SELECT state, COUNT(*) AS count FROM checks GROUP BY state
Return: {"states": [{"state": string, "count": number}]}`,
  { label: 'final-breakdown', phase: 'Reconcile', schema: BREAKDOWN_SCHEMA }
)

// Pull out just-verified checks that landed in 'malformed' — a structurally
// invalid predicate (type mismatch / unknown op) is the check's fault, not
// the resource's. These are BLOCKERS: spec/finish refuses while they exist.
// The fix is re-authoring the check (spec-generate tasks stage), never
// bypassing it.
const blocked = []
if (checks.length > 0) {
  const checkIdList = checks.map(c => `'${String(c.check_id).replace(/'/g, "''")}'`).join(', ')
  const reconcile = await agent(
    `ToolSearch "select:mcp__crest-spec__spec_sql"
Read the AUTHORITATIVE state of these behavioral checks from the engine. Report exactly what the rows say — do not infer, alter, or fill in missing rows.
Call spec_sql with query:
  SELECT c.id AS check_id, c.state AS state,
    (SELECT v.reason FROM verifications v WHERE v.check_id = c.id ORDER BY v.created_at DESC LIMIT 1) AS reason
  FROM checks c
  WHERE c.id IN (${checkIdList})
Return: {"checks": [{"check_id": string, "state": string, "reason": string}]}`,
    { label: 'reconcile-malformed', phase: 'Reconcile', schema: RECONCILE_SCHEMA }
  )
  const byId = {}
  for (const c of checks) byId[c.check_id] = c
  for (const row of (reconcile?.checks || []).filter(Boolean)) {
    if (row.state === 'malformed') {
      const c = byId[row.check_id]
      blocked.push({
        resource_id: c?.resource_id,
        check_id: row.check_id,
        reason: row.reason || 'malformed: check predicate is structurally invalid (type mismatch or unknown op) against the field it targets',
      })
      log(`BLOCKER: check ${row.check_id} for ${c?.resource_id || '(unknown resource)'} is structurally malformed — spec_finish will refuse until the check is re-authored (spec-generate tasks stage): ${row.reason || ''}`)
    }
  }
}

return {
  pending_verified: checks.length,
  final_breakdown: breakdown?.states || [],
  blocked,
  next_steps: 'spec_finish refuses while any check is pending, failed, theater, or malformed. Re-author "blocked" checks via the spec-generate tasks stage; regenerate resources whose checks failed; force=true is an explicit human decision, never the default.',
}
