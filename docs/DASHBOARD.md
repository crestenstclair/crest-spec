# Dashboard and inspection interfaces

The embedded dashboard is crest-spec's human operational interface. It reads the same canonical SQLite records as the Go query service, MCP tools, and read-only CLI; it does not maintain a cache database or write status sidecars into the generated project.

Start it from the project root:

```bash
CREST_SPEC_SPEC_DIR=./spec crest-spec dashboard --addr 127.0.0.1:8080
```

The dashboard intentionally defaults to a local operator-controlled deployment. It has no authentication layer and should not be bound to an untrusted network. Host metadata is redacted before persistence, raw operational captures are bounded, and typed responses label legacy, stale, redacted, blocked, and regressed state explicitly.

## Views and deep links

The application is embedded as a dependency-free HTML/CSS/JavaScript module set in the Go binary. A `view` query parameter selects a view; identity parameters restore drill-down and comparison state after reload or browser navigation.

| View | Deep-link examples | Purpose |
|---|---|---|
| Project and goals | `?view=project&goal={id}` | Mission, actors, completion explanation, missing capabilities, goal acceptance, current evidence, blockers, regressions, and history |
| Plan | `?view=plan&operation={id}` | Capability slices, operation rationale, expected behavior/evidence, waves, actual progress, and change impact |
| Resources | `?view=resources&resource={id}` | Goal contributions, dependencies, consumers, files, attempts, validation history, and impact |
| Contexts | `?view=contexts&context={id}` | Exact immutable prompt, section allocations, included/truncated/omitted sources, hashes, and retry comparison |
| Executions | `?view=executions&attempt_left={a}&attempt_right={b}` | Redacted host/model/role/tool provenance and whole-attempt comparison across context, candidate, validation, failure, cost, and outcome |
| Validations | `?view=verifications&verification={id}` | Definition, commands, source/executable hashes, captured output/artifacts, observations, predicates, classification, and evidence currency |
| Evaluations | `?view=evaluations&run={id}` | Cases, datasets, immutable configurations, metrics, comparisons, conclusions, and promotion evidence |
| Failures | `?view=failures&failure={id}` | Deterministic root-cause category, evidence, corrective route, retry policy, affected goals, and resolution |

Navigation tabs are keyboard-operable with Left/Right, Home, and End. Loading and error messages use live status regions, and state is never communicated by color alone.

## API conventions

New query contracts are under `/api/v1`. List queries use `limit` (default 50, maximum 200), an opaque `cursor`, stable ordering, and domain filters such as `status`, `kind`, `goal`, `capability`, `attempt`, and `q`. Errors use this envelope:

```json
{
  "version": "v1",
  "error": {
    "code": "not_found",
    "message": "the requested observability record does not exist",
    "retryable": false
  }
}
```

The preferred lifecycle aggregate is `GET /api/v1/attempts/{id}`. It links the task, context, redacted execution identity, candidate file hashes, validations, failures, cost, and outcome without returning candidate contents. `GET /api/v1/attempt-comparison?left={a}&right={b}` compares those governed identities. Context, execution, validation, and evaluation detail endpoints expose the bounded immutable records established by their owning workstreams.

Unversioned `/api/*` routes remain only for the bundled compatibility views. New integrations should not depend on them.

## Which interface to use

- Use the dashboard for interactive diagnosis and relationship navigation.
- Use the typed Go `observability.Service` from crest-spec code.
- Use `/api/v1` for read-only human automation.
- Use MCP inspection tools from agent hosts; lifecycle and generation remain MCP-driven.
- Use `crest-spec status`, `inspect`, and `compare` for terminal inspection and scripts. These commands never start generation.

## Developer-question map

This table is mirrored by the tested `observability.DeveloperQuestionRoutes` contract.

| Question | HTTP API | MCP | CLI | Dashboard |
|---|---|---|---|---|
| End goal and completion state | `/api/v1/project` | `spec/project_overview` | `crest-spec status` | `?view=project` |
| Incomplete, blocked, or regressed capabilities | `/api/v1/project`, `/api/v1/capabilities` | `spec/project_overview`, `spec/capabilities` | `crest-spec inspect project` | `?view=project` |
| Why a resource is generated and expected progress | `/api/v1/plan/operations/{id}` | `spec/plan_inspect` | `crest-spec inspect plan` | `?view=plan&operation={id}` |
| Exact selected, truncated, and omitted context | `/api/v1/contexts/{id}` | `spec/context_inspect` | `crest-spec inspect context {id}` | `?view=contexts&context={id}` |
| Host/model/role/tools/settings behind a candidate | `/api/v1/attempts/{id}` | `spec/attempt_inspect` | `crest-spec inspect attempt {id}` | `?view=executions&attempt={id}` |
| Rejecting validation and raw evidence | `/api/v1/verifications/{id}` | `spec/verification_inspect` | `crest-spec inspect validation {id}` | `?view=verifications&verification={id}` |
| Failure root cause and corrective route | `/api/v1/failures/{id}` | `spec/failures` | `crest-spec inspect failure {id}` | `?view=failures&failure={id}` |
| Goal-completion evidence and source tree | `/api/v1/goals/{id}` | `spec/goals`, `spec/verification_inspect` | `crest-spec inspect goal {id}` | `?view=project&goal={id}` |
| Completed goals invalidated by a change | `/api/v1/resources/{id}/impact` | `spec/impact` | `crest-spec inspect impact {id}` | Plan impact drill-down |
| Recommended next action | `/api/v1/project` | `spec/project_overview` | `crest-spec status` | `?view=project` |
| Difference between attempts | `/api/v1/attempt-comparison?left={a}&right={b}` | `spec/attempt_compare` | `crest-spec compare attempts {a} {b}` | Executions comparison |

## Historical data, retention, and limits

An additive migration does not invent provenance for old records. Missing declaration/effective hashes, selector versions, execution protocol hashes, or failure evidence hashes are labeled `legacy`; they are never presented as provenance-complete. Execution settings and credential-shaped text are labeled `redacted`. A mismatched linked context hash is labeled `stale`. Failure evidence returned inline is limited to 64 KiB and reports its original byte count and truncation state.

`crest-spec vacuum --before <date>` removes only old operational history that has no retained retry descendant or downstream execution, validation, failure, handoff, or evaluation reference. Blob collection checks every context, execution instruction, candidate, validation capture/artifact, failure evidence, evaluation case/configuration, and promotion reference before deleting content. This preserves navigable provenance rather than leaving broken dashboard links.

## Reference scenario

Use `fixtures/crest-synth/spec` as one complete sample specification. It combines project intent, DDD resources, delivery boundaries, artifacts, traceability, multi-scope validation, and behavioral witnesses into a single system. `fixtures/crest-synth/phases` is test evolution data only; phases are not a crest-spec runtime concept and should not be treated as separate pieces of the sample architecture.

After runtime attempts exist, trace one path as:

```text
project goal -> capability -> plan operation -> resource -> attempt
             -> context -> execution -> candidate -> validation/evidence
             -> completion or regression transition
```
