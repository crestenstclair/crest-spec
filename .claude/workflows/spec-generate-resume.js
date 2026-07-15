export const meta = {
  name: 'spec-generate-resume',
  description: 'Resume a crest-spec session at the generate+verify wave loop (design+tasks already committed). Pass {sessionId (full UUID), model?, maxRetries?}.',
  whenToUse: 'When a session already has design contracts + behavioral tasks/checks committed and only needs generation + behavioral verification driven to completion.',
  phases: [
    { title: 'Wave', detail: 'one generator agent per resource, retry loop inside the agent' },
    { title: 'Verify', detail: 'independent verifier agents run witness harnesses per behavioral check; failures cycle back into generation' },
    { title: 'Triage', detail: 'resolve or skip resources still failing after retries' },
  ],
}

// args: { sessionId: string (FULL UUID — the MCP engine validates it), model?, maxRetries? }
const input = typeof args === 'string' ? JSON.parse(args) : (args || {})
const sessionId = input.sessionId
if (!sessionId) throw new Error('spec-generate-resume requires args.sessionId (full UUID)')
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

// ─── Prompt builders ──────────────────────────────────────────────────────────

function generatorPrompt(resourceId, waveIndex) {
  return `You are a crest-spec generation sub-agent for resource "${resourceId}" (session ${sessionId}, wave ${waveIndex}).

Load the crest-spec MCP tools first:
ToolSearch "select:mcp__crest-spec__spec_context,mcp__crest-spec__spec_execution_start,mcp__crest-spec__spec_commit"

Then run this loop (at most ${maxRetries + 1} attempts):
1. Call spec_context with {session_id: "${sessionId}", resource_id: "${resourceId}"}.
   It returns AttemptID, ContextHash, Role, RolePolicyVersion, ContextPolicy,
   TemplateHashes, SystemPrompt, Prompt, and Invariants (each invariant is
   {text, rationale}). Treat SystemPrompt as your role and follow Prompt
   exactly — it contains the mission, the resource declaration, dependencies,
   the bounded context's design contract, existing files (UPDATE mode), and —
   on retries — the sections "## Previous Errors" and "## Guidance".
2. BEFORE generating, call spec_execution_start exactly once with:
   {
     attempt_id: Context.AttemptID,
     protocol_version: "crest-execution-v1",
     idempotency_key: "claude-code:" + Context.AttemptID,
     context_hash: Context.ContextHash,
     host_name: "claude-code",
     host_version: "workflow-resume-v1",
     provider: "anthropic",
     model: "${model}",
     inference_config: {model_alias: "${model}"},
     agent_config: {role: Context.Role, role_policy_version: Context.RolePolicyVersion,
                    context_policy: Context.ContextPolicy, workflow: "spec-generate-resume"},
     tools: [{name: "crest-spec-mcp", permission: "context,execution_start,commit"}],
     template_hashes: Context.TemplateHashes,
     system_instructions: Context.SystemPrompt,
     host_session_id: "${sessionId}"
   }.
   Do not generate or commit if registration fails. Work as Context.Role; a
   retry may hand the resource to minimal_diff_repair or another specialist.
3. On a retry the context serves your PREVIOUS ATTEMPT'S files in UPDATE
   mode alongside "## Previous Errors". ITERATE: keep the working code and
   make the minimal edit that fixes that specific failure — never rewrite
   the resource from scratch over a minor bug.
4. Author the files the prompt asks for (full file contents, correct paths
   relative to the project root). Honor every invariant as a hard constraint —
   verification is independent (behavioral checks), so a violation WILL be
   caught after commit and cost a full regeneration. Follow the prompt's
   folder structure and style rules. Do NOT create files the prompt doesn't
   call for. Don't sweat formatting — the wave gate normalizes it
   automatically; your job is the design, not the whitespace.
5. Call spec_commit with {attempt_id: Context.AttemptID,
   files: [{path, content}], notes: <one-line design note>}. Commit through
   spec_commit rather than writing into the project tree with your own file
   tools — that keeps the loop's state and feedback coherent.
6. If the result has Committed=true → stop, report outcome "committed".
   If Committed=false → read result.Validations for the failure, go back to
   step 1 (the new context carries the failure) and fix the actual problem.
7. If still rejected after ${maxRetries + 1} attempts, report outcome
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
5. Run both programs and capture stdout. Extract the JSON from the CREST_OBS: and CREST_STUB: lines by stripping those prefixes and parsing the remainder. If either line is absent or unparseable, do NOT invent values — the behavior is unverified and the check must fail; report passed:false and stop (the workflow reconciles against engine state and will regenerate).
6. Call spec/verify with {check_id: "${checkId}", real_observation: <parsed CREST_OBS object>, stub_observation: <parsed CREST_STUB object>}. You MUST call spec/verify for the check to count — a self-reported pass without a real spec/verify call is treated as a failure by the workflow.
7. If spec/verify returns passed=true: call spec/graduate with {check_id: "${checkId}"}.
   If passed=false or theater=true: do NOT graduate.
8. Return: {check_id: "${checkId}", passed: <bool>, theater: <bool, default false>, reason: "<from spec/verify>", graduated: <true only if spec/graduate was called and succeeded>}.`
}

// ─── GENERATE + VERIFY (wave loop) ───────────────────────────────────────────

const triaged = []
const graduated = []
const blocked = []
const unresolved = []
let waveCount = 0
let lastWaveIndex = -1
let stallCount = 0
const MAX_STALLS = 3
// spec_next serves resources concurrency-gated (often one at a time) and can
// momentarily return zero while wave members are transiently locked/dispatched.
// An empty serve is NOT proof the session is drained (that case returns
// done=true). Re-poll a bounded number of times before concluding the wave is
// genuinely empty, so a transient gap can't terminate the loop early.
let emptyPolls = 0
const MAX_EMPTY_POLLS = 8

while (true) {
  const wave = await agent(
    `Load the crest-spec MCP tools (ToolSearch "select:mcp__crest-spec__spec_next"), call spec_next with {session_id: "${sessionId}"}, and return its result: done, wave_index, and resources (resource_id, attempts, last_error — last_error comes from each resource's Error.Message if set).`,
    { label: 'spec_next', phase: 'Wave', schema: WAVE_SCHEMA },
  )
  if (!wave || wave.done) break
  const resources = (wave.resources || []).filter(Boolean)
  if (resources.length === 0) {
    emptyPolls++
    if (emptyPolls > MAX_EMPTY_POLLS) break
    log(`spec_next returned no resources (transient; poll ${emptyPolls}/${MAX_EMPTY_POLLS}) — re-polling`)
    continue
  }
  emptyPolls = 0

  if (wave.wave_index === lastWaveIndex) {
    stallCount++
    if (stallCount > MAX_STALLS) {
      // LOUD HALT — no auto-skip. spec_skip is a human-level decision
      // (contradictory spec), not a convergence-failure escape hatch.
      for (const r of resources) {
        unresolved.push({ resource_id: r.resource_id, attempts: r.attempts, last_error: r.last_error || '' })
      }
      log(`Wave ${wave.wave_index}: HALTED after ${stallCount} passes — ${resources.length} resource(s) did not converge: ${resources.map(r => r.resource_id).join(', ')}. Nothing was auto-skipped; resolve or skip explicitly and re-run.`)
      break
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

    await parallel(checks.map(c => () =>
      agent(verifierPrompt(committed.resource_id, c.check_id, c.behavior, c.check_json), {
        label: `verify:${c.check_id}`,
        phase: 'Verify',
        model,
        schema: CHECK_VERIFY_SCHEMA,
      })
    ))

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
      { label: `reconcile:${committed.resource_id}`, phase: 'Verify', schema: RECONCILE_SCHEMA }
    )
    const stateByCheck = {}
    for (const r of (reconcile?.checks || []).filter(Boolean)) stateByCheck[r.check_id] = r

    const passedChecks = []
    const failedChecks = []
    for (const c of checks) {
      const row = stateByCheck[c.check_id]
      const state = row?.state
      if (state === 'passed' || state === 'graduated') {
        passedChecks.push({ check_id: c.check_id, state })
      } else if (state === 'malformed') {
        // The CHECK is structurally broken (predicate typing), not the code.
        // Resume has no design contracts in scope to re-author from, so this
        // is a BLOCKER: spec_finish refuses while it exists. Re-author the
        // resource's checks via the full spec-generate tasks stage.
        blocked.push({ resource_id: committed.resource_id, check_id: c.check_id, reason: row?.reason || 'malformed: check predicate is structurally invalid (type mismatch or unknown op)' })
        log(`BLOCKER: check ${c.check_id} for ${committed.resource_id} is structurally malformed — spec_finish will refuse until the check is re-authored (run the spec-generate tasks stage)`)
      } else if (state === 'theater') {
        failedChecks.push({ check_id: c.check_id, theater: true, reason: row?.reason || 'theater: stub baseline also satisfied the check (non-discriminating predicate)' })
      } else if (state === 'failed') {
        failedChecks.push({ check_id: c.check_id, theater: false, reason: row?.reason || 'behavioral verification failed: real observation did not satisfy the check' })
      } else {
        failedChecks.push({ check_id: c.check_id, theater: false, reason: `check still '${state || 'absent'}' after its verifier ran — no fail-closed verification was recorded (verifier crashed, skipped spec/verify, or spec/verify rejected a missing/malformed witness). Behavior is UNVERIFIED; regenerating.` })
      }
    }

    if (passedChecks.length > 0) {
      for (const v of passedChecks) {
        graduated.push({ resource_id: committed.resource_id, check_id: v.check_id, state: v.state })
      }
      log(`Wave ${wave.wave_index}: ${passedChecks.length}/${checks.length} behavioral check(s) verified via engine state for ${committed.resource_id}`)
    }

    if (failedChecks.length > 0) {
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
  const committedCount = committedOutcomes.length
  if (committedCount > 0) {
    const verify = await agent(
      `Load the crest-spec MCP tools (ToolSearch "select:mcp__crest-spec__spec_verify_wave,mcp__crest-spec__spec_resolve,mcp__crest-spec__spec_sql"), then call spec_verify_wave with {session_id: "${sessionId}", wave_index: ${wave.wave_index}}. If Passed is true, report "wave verified". If Passed is false, attribute each failure YOURSELF before resolving — the server's ResourceID field is a loose heuristic and routinely pins tree-wide clippy/test failures on the wrong resource. For each error: read the Message, find the file path(s) actually failing (compiler and clippy errors name their files), then map each file to its owning resource with spec_sql: SELECT resource_id FROM generated_files WHERE path LIKE '%<filename>%'. Call spec_resolve with {session_id: "${sessionId}", resource_id: <the OWNING resource>, guidance: <the exact failure for that file, condensed to what the regenerating agent must fix>}. Never resolve a resource for a failure that is not in its own files, and never resolve the same resource twice for an identical message — if the owner is not in this session or you cannot place a failure, report it as unattributed instead. Report: passed true/false, which resources you resolved, and any unattributed errors.`,
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
    const verdict = await agent(
      `Resource "${f.resource_id}" in crest-spec session ${sessionId} failed generation after ${f.attempts ?? '?'} attempts. Last error:\n${f.error || '(none reported)'}\n\nLoad tools: ToolSearch "select:mcp__crest-spec__spec_resolve,mcp__crest-spec__spec_skip,mcp__crest-spec__spec_history,mcp__crest-spec__spec_inspect"\n\nInspect spec_history for the resource if helpful. Default to spec_resolve: if the failure is fixable with concrete guidance (a specific API misuse, a missing import pattern, a misread of the spec), call spec_resolve with {session_id: "${sessionId}", resource_id: "${f.resource_id}", guidance: <specific, actionable guidance>} — this resets the resource to pending so the next wave pass retries it.\n\nspec_skip is reserved for the SPEC being wrong, never for the work being hard. Before you may call spec_skip you must: (1) call spec_inspect for the resource, (2) QUOTE the two contradictory declarations or name the dependency that does not exist in the spec, verbatim, in your skip reason. "Keeps failing", "too complex", or "won't converge" are NOT skip reasons — if you cannot quote a contradiction, you must spec_resolve instead. You MUST actually invoke exactly one of spec_resolve or spec_skip before finishing — a prose verdict alone leaves the resource stuck. Report which you chose and why.`,
      { label: `triage:${f.resource_id}`, phase: 'Triage' },
    )
    triaged.push({ resource_id: f.resource_id, action: verdict })
  }
}

return {
  waves_processed: waveCount,
  triaged,
  graduated,
  graduated_count: graduated.length,
  blocked,
  unresolved,
  next_steps: 'Call spec_finish (main session). It REFUSES (Blocked=true) while any behavioral check is pending, failed, theater, or malformed — force=true is an explicit human decision, never the default. If "unresolved" is non-empty the run HALTED without auto-skipping: fix the spec or guidance and re-run, or spec_skip explicitly with the contradiction quoted. "blocked" lists structurally malformed checks — re-author them via the spec-generate tasks stage. If FinishResult.reflection_prompt is non-empty, run it with a sonnet agent and submit via spec_record_learnings.',
}
