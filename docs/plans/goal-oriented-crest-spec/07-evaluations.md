# Workstream 7: Host-Driven Evaluations and Validated Learning

Status: **Draft**  
Depends on: WS4, WS5, WS6  
Enables: empirical release decisions and continuous improvement  
Original scope: Workstream 9

## Objective

Create an evaluation system that measures whether changes to context selection, templates, planning, role policies, or agent configuration improve results across historical and curated cases. Preserve operational retry learning while separating it from claims of reusable improvement.

crest-spec defines cases, assignments, metrics, and comparisons. External hosts execute model work through the same versioned attempt protocol; crest-spec does not become a provider SDK.

## Requirements

- Build immutable evaluation cases from historical attempts or curated SQLite imports.
- Reference exact project specification, goals, repository state, resource declaration, context, accepted candidate, and validation/evidence outcomes.
- Compare baseline and candidate configurations over identical case sets.
- Support host-driven case claiming, execution, result submission, interruption, and resumption.
- Measure acceptance, goal completion, integration, behavior, retries, regressions, diff size, tokens, cost, duration, intervention, security, and static-analysis outcomes.
- Separate per-attempt memory, reusable guidance, and empirically validated policy/template/planner improvements.
- Require evaluation evidence and human approval before promotion.
- Store all cases/runs/metrics in SQLite; loose files exist only through explicit import/export commands.

## Evaluation concepts

### Case

An immutable case references content-addressed snapshots of:

- project intent and target goal/capability
- resource declaration and relevant repository state
- source plan operation and expected outcome
- source context/execution/candidate/validation chain when historical
- acceptance and evidence requirements
- comparison outcome target

Cases have dataset splits (`training`, `development`, `held_out`) and provenance (`historical`, `curated`, `imported`). Held-out outcomes are hidden from an executing role until scoring.

### Configuration

A configuration identifies the planner version/policy, context selector and budget, templates, role policy, host/provider/model label, inference settings, validation version, and optional learnings set. It contains no provider credentials.

### Run

A run defines case set, variants, metric policy, assignment state, stopping rule, and comparison method. At minimum it supports baseline and candidate; the storage model may allow more named variants without changing metric semantics.

### Assignment

An external host claims one case/variant assignment with a lease. The assignment produces ordinary context, execution, candidate, validation, and evidence records linked back to the evaluation run. Expired leases may be reclaimed without overwriting the first attempt.

## Metric policy

Primary metrics:

- accepted implementation rate
- required goal-completion/evidence rate
- integration and behavioral success rate
- regression rate
- human intervention rate

Secondary metrics:

- attempts/retries to terminal result
- candidate diff size and file churn
- estimated/reported input/output tokens and cost
- execution and validation duration
- security/static-analysis outcomes

Comparisons report sample count, missing data, distribution, absolute/relative change, and configured practical-significance threshold. Small or incomplete runs are `inconclusive`, not winners.

## Learning/promotion policy

- Operational memory from one failure remains linked to that attempt/resource/session.
- Reusable guidance is a hypothesis with scope and provenance.
- A promotion proposal names the evaluation run/configuration and required metrics.
- A proposal is eligible only when it meets configured quality, regression, cost, and sample thresholds on the intended split, including held-out cases when required.
- Promotion remains human-gated and records the exact template/policy change and rollback identity.
- Failed or inconclusive evaluations never auto-promote.

## MCP/API workflow

Provide operations equivalent to:

- create/list/detail evaluation cases and datasets
- create a run from immutable configuration IDs
- claim/heartbeat/release an assignment
- obtain assignment context through the normal attempt protocol
- submit host usage metadata and terminal result
- finalize/compare a run
- propose/approve/reject promotion
- inspect underlying attempts, contexts, candidates, validations, and evidence

CLI support is primarily read-only plus explicit import/export and human promotion approval. Generation remains host-driven.

## Persistence ownership

WS7 owns:

- evaluation datasets and membership/splits
- cases and snapshot references
- immutable configurations and variant relationships
- runs, assignments, leases, attempts, and terminal states
- metric definitions, observations, aggregates, and comparisons
- conclusions, regression flags, and promotion proposals/decisions

Cases reference existing content blobs and provenance records instead of duplicating them. Explicit exports are user-requested artifacts and must never become the runtime canonical source.

## Tasks

### WS7-01 — Define immutable cases and datasets

- Define case provenance, required snapshots, splits, identity hashes, and eligibility.
- Build cases from complete historical attempt chains.
- Add a curated case API that stores imported content in SQLite.

Acceptance: recreating a case from unchanged source records yields the same identity and no duplicated blobs.

### WS7-02 — Define configurations and metrics

- Model immutable planner/context/template/role/host configurations.
- Define primary/secondary metrics, missing-data rules, thresholds, and inconclusive status.
- Prevent credentials or mutable template paths from serving as configuration identity.

Acceptance: baseline and candidate configuration hashes fully identify the policies being compared.

### WS7-03 — Implement host-driven run orchestration

- Add run creation, assignment leasing, claim/heartbeat/release, attempt linkage, and result submission.
- Reuse WS4–WS6 context/execution/validation paths.
- Handle host crash, lease expiry, duplicate submission, cancellation, and resumption.

Acceptance: multiple hosts can execute distinct assignments without duplicate authoritative results.

### WS7-04 — Aggregate and compare

- Calculate per-case outcomes and run-level metrics.
- Report sample/missing counts and practical thresholds.
- Detect quality, regression, retry, cost, and duration tradeoffs.
- Link every aggregate back to case attempts/evidence.

Acceptance: comparison output is deterministic and labels underpowered/incomplete data inconclusive.

### WS7-05 — Gate promotion

- Connect learnings, templates, selector policies, planner policies, and role policies to evaluation configurations.
- Require qualifying evaluation evidence before promotion eligibility.
- Preserve human approval, immutable decision history, and rollback identity.

Acceptance: threshold-only historical learnings can no longer be promoted without a qualifying evaluation when the new policy is enabled.

### WS7-06 — Expose evaluation analysis

- Add MCP/API/CLI queries and the Evaluation Dashboard through WS8.
- Show runs, configurations, metrics, trends, regressions, winners/losers/inconclusive outcomes, costs, retries, and goal completion.
- Deep-link to cases and their context/execution/evidence chains.

Acceptance: a claimed improvement can be audited from aggregate conclusion to raw attempt evidence.

## Test plan

- Case tests for historical eligibility, missing provenance, stable identity, splits, and blob reuse.
- Configuration tests for canonical hashing, redaction, immutable template/policy references, and variant isolation.
- Assignment concurrency tests for lease claim, heartbeat, expiry, reclaim, duplicate submission, cancellation, and crash recovery.
- Metric golden tests for missing values, rates, distributions, thresholds, regression flags, and inconclusive sample sizes.
- Promotion tests for qualifying, regressed, incomplete, held-out failure, human rejection, approval, and rollback history.
- Dashboard/API tests for comparisons and deep links to underlying records.
- End-to-end fake-host run comparing two deterministic context/template variants.

## Compatibility and rollout

Existing generations and learnings remain operational. Only historical attempts with sufficient immutable provenance become evaluation cases automatically; others are reported as ineligible with reasons. Existing promotion commands transition to the evidence-gated policy with an explicit migration message.

## Exit gate

- Historical and curated cases are SQLite-backed and reproducible.
- External hosts execute leased cases through the normal attempt protocol.
- Comparisons are deterministic, evidence-linked, and honest about missing/insufficient data.
- Reusable improvements require qualifying evaluation evidence and human approval.

