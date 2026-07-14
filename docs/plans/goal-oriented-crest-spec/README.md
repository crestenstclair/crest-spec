# Goal-Oriented crest-spec Improvement Program

Status: **Draft for workstream-by-workstream review**  
Source brief: [`gpt_improvements.md`](../../../gpt_improvements.md)  
Last updated: 2026-07-14

## Purpose

This program evolves crest-spec from a resource-oriented generation and validation engine into a goal-oriented reconciliation and verification engine. The completed system must connect declared project intent to resources, generated files, agent context, execution configuration, validation evidence, and an explainable project-completion decision.

This is an incremental extension of the current architecture, not a rewrite. It preserves:

- CUE project specifications and explicit resource dependency graphs
- Effective-hash planning and incremental dependency propagation
- Parallel dependency waves
- SQLite as the canonical operational store
- Mechanical, fail-closed validation
- External, model-independent agent orchestration
- Separation between generation and governance
- Human approval for destructive changes and learned-guidance promotion

Runtime manifests, evidence, execution records, evaluation data, projections, and diagnostics remain in SQLite. No operational JSON, YAML, Markdown, hidden metadata, session folders, or source-code sidecars are introduced.

## Current architecture diagnosis

crest-spec already has strong subsystem boundaries:

- `internal/cue` loads CUE into a DDD-oriented project model and registers resources.
- `internal/graph` builds dependency and reverse-dependency graphs, hashes structural declarations, and calculates waves.
- `internal/plan` compares declarations and effective hashes with persisted resource state.
- `internal/spec` owns session state, context construction, commit validation, retries, wave validation, finish gating, behavioral checks, amendments, and learnings.
- `internal/store`, generated SQL queries, and embedded migrations provide SQLite persistence.
- `internal/mcp` exposes the state-engine contract to external hosts.
- `cmd/crest-spec/dashboard.go` and its embedded static application expose current sessions, resources, generations, and learnings.
- Claude Code and OpenCode adapters preserve the core rule that the host dispatches agents while crest-spec plans, records, and validates.

The principal gaps are systemic rather than isolated:

- `project.mission` is prose context, not a machine-readable completion model.
- Resources cannot declare stable capability contributions, so traceability stops at resource dependencies.
- Plans explain hash differences, not how operations close functional gaps.
- Context selection is useful but implicit, unbudgeted, and not reproducible.
- A generation row is created at commit time, after context and most execution metadata have already been lost.
- Behavioral verification evaluates caller-supplied observations without independently proving their source.
- Validation outcomes are not represented as a unified, multi-scope evidence ledger.
- Learnings can be promoted by thresholds but cannot be evaluated against historical or held-out cases.
- The dashboard has no goal, plan rationale, context, execution, evidence, failure-analysis, or evaluation model.

The untracked `migrations/017_goal_orchestration.sql` is design input only. Its concepts must be reviewed and divided into cohesive migrations; it must not ship as an all-in-one schema change.

## Target architecture

```text
CUE project intent
  -> goal/capability/resource registry
  -> dependency + contribution graph
  -> goal-directed reconciliation plan
  -> context selection + immutable context manifest
  -> host-reported execution manifest
  -> candidate file snapshot
  -> resource/integration/goal/project verification
  -> evidence-backed completion projection
  -> historical evaluation corpus
  -> SQLite-backed API/MCP/dashboard read models
```

The specification remains the source of truth for intended state. SQLite stores reconciled specification identities, lifecycle state, manifests, evidence, history, and projections. Generated source code remains a project artifact, not an operational-state carrier.

## Public model decisions

### Goal and capability DSL

New specifications must define stable keyed maps for:

- `project.actors`
- `project.goals`
- `project.capabilities`
- `project.requirements`
- `project.nonGoals`
- `project.completion`

Goals and capabilities are separate so one capability can contribute to multiple goals. References use canonical IDs such as `goal.account_registration`, `capability.create_account`, and `requirement.email_uniqueness`.

Goal status is never authored in CUE. It is derived from the latest plan, accepted resources, validations, evidence, dependencies, and blockers.

### DDD remains the canonical implementation model

The goal model is an intent and verification layer above the existing DDD schemas, not a replacement architecture vocabulary. Aggregates, entities, value objects, domain services, application services, repositories, ports, adapters, and assets remain the only canonical implementation-resource families.

The division is explicit:

| Concern | Canonical schema |
|---|---|
| User outcome, project priority, completeness | Goal and capability |
| Observable end-to-end behavior | Capability acceptance scenario |
| Stateful business behavior and state machines | Aggregate/entity/value object |
| Domain computation without aggregate ownership | Domain service |
| Use-case/workflow coordination and data-pipeline orchestration | Application service |
| Boundary contract, event/message contract, persistence contract | Port plus value objects |
| HTTP/RPC/UI/CLI/job/message/device/external-system delivery | Adapter implementing a port |
| Build/configuration/migration/infrastructure/observability/deployment/docs | Structured asset profile |

All generatable DDD resources and assets gain capability contributions. Ports gain an explicit inbound/outbound direction, and adapters gain narrow boundary profiles so endpoints, screens, scheduled triggers, message consumers, devices, and external integrations can be expressed without creating parallel resource types. Assets gain structured profiles for recurring non-code or project-wide artifacts while retaining custom `assetKinds` as the escape hatch.

User journeys are acceptance scenarios, not generated resources. Event schemas remain value objects and port contracts. Workflows remain application services; state machines remain aggregates. Security rules remain invariants/application behavior unless they are deployment policy artifacts, in which case they are structured assets.

### Completion lifecycle

The normal lifecycle is:

```text
declared -> planned -> partially_implemented -> locally_validated
         -> integrated -> behaviorally_verified -> complete
```

`blocked` and `regressed` are exceptional states with persisted causes. Project completion requires all required goals and project checks to be complete; optional goals remain visible but do not block completion.

### Attempt protocol

The host contract becomes attempt-oriented:

1. `spec/context` creates an attempt and immutable context manifest.
2. The host calls `spec/execution_start` with redacted host/model/tool/permission metadata.
3. The host submits candidate files through `spec/commit` using the returned `attempt_id`.
4. crest-spec snapshots candidates, runs validations, records goal consequences, and accepts or rejects atomically.

This is a versioned breaking change to the generation protocol. Bundled host adapters and integration documentation must move in the same release.

## Workstreams

The [goal-oriented crest-synth target sample](examples/README.md) shows how the new intent, traceability, verification, and artifact features compose with the existing DDD schemas without introducing a redundant generic resource model.

| Plan | Consolidated scope | Original workstreams |
|---|---|---|
| [01 — Goal language and completion state](01-goal-language.md) | Machine-readable intent, lifecycle, blockers, completion | 1 |
| [02 — DDD traceability and boundary/artifact profiles](02-resource-traceability.md) | Goal links across the existing DDD model plus narrow delivery extensions | 2, 10 |
| [03 — Goal-directed reconciliation](03-goal-directed-reconciliation.md) | Vertical-slice planning, impact, regression | 3, 11 |
| [04 — Context manifests](04-context-manifests.md) | Rich context, budgeting, selection, observability | 4, 5 |
| [05 — Execution, roles, and failures](05-execution-roles-failures.md) | Execution manifests, specialization, failure triage | 8, 12, 13 |
| [06 — Validation and evidence](06-validation-evidence.md) | Multi-scope validation and behavioral provenance | 6, 7 |
| [07 — Evaluations](07-evaluations.md) | Host-driven empirical improvement | 9 |
| [08 — Dashboard and developer interfaces](08-dashboard-developer-interfaces.md) | Operational UI, MCP/API/CLI inspection, deep links | 14 and cross-cutting DX |

## Implementation order

| Stage | Deliverables | Entry requirement | Exit gate |
|---|---|---|---|
| 0 | Approve plans; replace the draft all-in-one migration with staged migration ownership | This planning package reviewed | Task and requirement matrix accepted |
| 1 | WS1 plus modular dashboard shell from WS8 | Stage 0 | Strict goal DSL, persisted completion state, migrated fixtures |
| 2 | WS2 plus goal/resource dashboard slices | WS1 | Bidirectional traceability across DDD, boundary, and artifact fixtures |
| 3 | WS3 plus plan/impact dashboard slices | WS1–2 | Persisted vertical-slice plans and deterministic regression invalidation |
| 4 | WS4 context/attempt foundation | WS1–3 | Deterministic budgeted manifests and context comparison |
| 5 | WS5 execution governance | WS4 | Versioned host protocol, candidate provenance, role/failure lifecycle |
| 6 | WS6 trustworthy verification | WS1–5; runner design may start after WS3 | Multi-scope evidence and engine-executed behavioral witnesses |
| 7 | WS7 empirical improvement | WS4–6 | Host-driven evaluation runs and evidence-gated promotion |
| 8 | WS8 integration hardening | All feature workstreams | Complete deep-link graph, docs, migration/e2e/release checks |

Dashboard, API, and MCP slices are part of each stage's definition of done. They are not deferred until Stage 8.

## Persistence and migration strategy

Use staged, additive migrations beginning at the next available migration number:

1. Goals, actors, capabilities, requirements, acceptance, and completion state
2. DDD contributions, port/adapter boundary profiles, and structured asset profiles
3. Plans, plan operations, impacts, status changes, and evidence invalidations
4. Content-addressed blobs, attempts, context manifests, sections, sources, and selection decisions
5. Execution manifests, roles, handoffs, candidate snapshots, retry chains, and failure classifications
6. Validation runs, witnesses, raw observations, evidence, and goal consequences
7. Evaluation cases, configurations, assignments, runs, metrics, and comparisons

Each logical event has a transaction-scoped repository operation. Large repeated content is stored once in a content-addressed SQLite BLOB table and referenced by SHA-256. Existing historical rows remain queryable through nullable new relationships and are labeled `legacy` when provenance cannot be reconstructed.

The migration test matrix must include a new database, an existing database at the current migration level, populated historical data, foreign-key checking, idempotent reopen, and rollback on a deliberately invalid migration fixture.

## Cross-cutting engineering requirements

- Stable IDs, canonical ordering, timestamps in UTC, and SHA-256 hashes are used consistently.
- Status projections are explainable: every transition records its inputs and reason.
- Missing intent, context, evidence, or integration is not converted into an implementation retry.
- Host-supplied metadata is validated and redacted; credentials are never stored.
- All commands and raw evidence are bounded by explicit retention and truncation policies while retaining hashes and classification metadata.
- No generator, verifier, reviewer, or host can unilaterally mark a goal or project complete.
- New store capabilities use focused repository interfaces rather than continually expanding one monolithic interface.
- SQL changes include generated query updates and store-level tests.
- Every new backend concept has MCP/API inspection and a dashboard representation in the same stage.

## Acceptance traceability

| Acceptance criteria from the brief | Owning workstream tasks |
|---|---|
| 1, 20 | WS1-01 through WS1-06 |
| 2, 25 | WS2-02 through WS2-05; WS8-03 through WS8-05 |
| 3 | WS3-01 through WS3-03 |
| 4 | WS4-01 through WS4-03 |
| 5–7 | WS4-04 through WS4-06 |
| 8–10 | WS1-04; WS3-04 through WS3-05; WS6-01 through WS6-05 |
| 11–12 | WS6-02 through WS6-06 |
| 13–15 | WS5-01 through WS5-06 |
| 16 | WS5-04 through WS5-06 |
| 17–18 | WS7-01 through WS7-06 |
| 19 | WS2-01 through WS2-06 |
| 21 | WS3-03 through WS3-06; regression suite |
| 22–24 | WS8-01 through WS8-06 and each workstream dashboard task |
| 26 | Migration strategy plus every workstream compatibility section |

## Review process

Review the documents in implementation order. Record workstream approval in this table before implementation begins:

| Workstream | Status | Reviewer notes |
|---|---|---|
| WS1 Goal language | Draft | |
| WS2 Resource traceability | Draft | |
| WS3 Goal-directed reconciliation | Draft | |
| WS4 Context manifests | Draft | |
| WS5 Execution, roles, failures | Draft | |
| WS6 Validation and evidence | Draft | |
| WS7 Evaluations | Draft | |
| WS8 Dashboard and developer interfaces | Draft | |

Changes to an earlier approved public contract must trigger review of dependent workstream plans. Implementation PRs should reference task IDs and update this roadmap when dependencies or acceptance criteria change.

## Program-level completion criteria

The program is complete only when:

- Every workstream exit gate passes.
- Every acceptance criterion maps to passing automated tests and a working inspection surface.
- All tracked examples use the required goal model.
- Existing databases migrate without losing historical records.
- The bundled host adapters implement the versioned attempt protocol.
- A representative fixture can be traced from goal through plan, context, execution, candidate, validation, evidence, and completion in both APIs and the dashboard.
- The full Go test suite and dashboard/API integration suite pass, including MCP HTTP tests in an environment that permits local listeners.
