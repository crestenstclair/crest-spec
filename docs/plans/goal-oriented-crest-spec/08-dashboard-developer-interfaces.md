# Workstream 8: Dashboard and Developer Interfaces

Status: **Implemented**
Depends on: delivered incrementally with WS1–WS7  
Original scope: Workstream 14 and cross-cutting developer experience

## Objective

Make the dashboard the primary operational interface for understanding project completion and generation provenance while exposing equivalent structured inspection through Go APIs, MCP, and read-only CLI commands.

The dashboard remains embedded in the Go binary and dependency-free at runtime. The current monolithic static page is split into maintainable embedded HTML, CSS, and JavaScript modules; no compiled SPA framework is introduced.

## Requirements

- Read all state from canonical SQLite-backed query services.
- Do not add dashboard-specific persistence or derived files.
- Use typed, versioned API response models rather than exposing store structs directly.
- Deep-link across the complete chain from goal to final evidence.
- Provide explainable status, not an opaque completion percentage.
- Make context, execution, validation, evidence, failure, and evaluation comparisons practical.
- Handle empty, loading, error, legacy, stale, blocked, regressed, and partially migrated records explicitly.
- Preserve local-only deployment defaults and avoid exposing secret-bearing data.

## Embedded application architecture

Split the embedded frontend into:

- a minimal document shell and navigation
- shared styles and accessible components
- API client and route/deep-link state
- feature modules for project, goals, resources, plans, contexts, executions, validations/evidence, evaluations, and failures
- reusable comparison, history, filter, raw-capture, and relationship components

Go query services build stable view models. JavaScript renders and navigates those models but does not recreate domain logic or completion calculations.

## Versioned API

Add `/api/v1` endpoints grouped by domain:

- project overview and completion explanation
- goals, capabilities, dependencies, histories, evidence, and blockers
- resources, contributions, dependencies/consumers, files, validations, contexts, and executions
- plans, revisions, slices, operations, waves, outcomes, and impacts
- attempts, context manifests/sources/omissions/comparisons
- execution manifests/candidates/retries/roles/handoffs/comparisons
- validations/witnesses/raw evidence/provenance/trends
- evaluations/cases/configurations/runs/metrics/comparisons
- failures/categories/evidence/actions/resolutions/trends

List endpoints use stable pagination, filters, and ordering. Detail responses return relationship links/IDs instead of nesting unbounded history.

## Required views

### Project overview

Show mission, actors, required/optional goals, overall state, current blockers, regressions, missing functionality, stale evidence, active plan, and recommended next work.

### Goal explorer

Show description, priority, dependencies, capabilities, requirements, contributing resources, acceptance scenarios, evidence, blockers, and status history.

### Resource explorer

Show declaration summary, kind, capabilities/goals, dependencies, consumers, files, current state, validations, context attempts, execution attempts, and impacts.

### Plan explorer

Show goal-directed rationale, capability slices, operations/categories, waves, blockers, expected versus actual progress, failures, revisions, and change-impact paths.

### Context inspector

Show manifest metadata, structured sections, token allocation, included/truncated/omitted sources, files, spec fragments, dependencies, consumers, failures, selection reasons, template/selector hashes, and side-by-side comparison.

### Execution inspector

Show redacted host/model/settings, role, tools/permissions, instructions, templates, context link, candidate files/diff/hash, validation result, goal impact, disposition, retry chain, handoffs, and comparison.

### Validation and evidence explorer

Show scope, definition, exact command, source/artifact hashes, raw stdout/stderr/artifacts, parsed observations, predicates, real/negative results, classification, retry, affected goals/resources, evidence currency, and trends.

### Evaluation dashboard

Show cases, configurations, run progress, baseline/candidate metrics, success/completion/retry/regression/cost/duration/intervention comparisons, trends, conclusions, and underlying attempts.

### Failure analysis

Show categories, trends, goals, resource kinds, hosts/models, context/template policies, validations, corrective actions, resolutions, and resolution rates.

## Deep-link model

Use stable URL routes/query parameters for every detail identity and comparison pair. Required navigation includes:

```text
goal -> capability -> plan slice -> operation -> resource -> attempt
     -> context -> execution -> candidate -> validation -> evidence
     -> completion transition
```

Backlinks are equally important: an evidence run must navigate to its goal and source attempt; a resource must navigate to affected/regressed goals.

## MCP and CLI alignment

- MCP domain queries return the same typed view models or equivalent fields as dashboard APIs.
- MCP resources expose compact current summaries; tools expose filtered/detail/comparison queries.
- CLI inspection remains human/read-only except explicit maintenance, import/export, and approval actions.
- Planned CLI forms include `status`, `inspect <kind> <id>`, `compare attempts <a> <b>`, `compare contexts <a> <b>`, and `eval` inspection/import/export/approval commands.
- Generation and orchestration remain MCP/host responsibilities; no generation CLI is added.

## Tasks

### WS8-01 — Establish typed query/view models

- Define API versioning, error envelope, pagination, filter, relationship-link, legacy, and redaction conventions.
- Introduce focused query services over SQLite repositories.
- Stop returning persistence structs directly from new endpoints.

Acceptance: API contracts remain stable when internal store representations change.

### WS8-02 — Modularize the embedded UI

- Split the static application into embedded modules and shared components.
- Add client-side routing/deep-link restoration without a build toolchain.
- Preserve server startup and single-binary distribution.
- Add accessible navigation, semantic controls, focus handling, and keyboard operation.

Acceptance: the existing session/resource/generation/learning functionality works from modular assets before feature expansion.

### WS8-03 — Deliver vertical dashboard slices

- Ship Project/Goal views with WS1.
- Ship DDD contribution traceability plus boundary/asset profiles with WS2.
- Ship Plan/Impact views with WS3.
- Ship Context Inspector with WS4.
- Ship Execution/Role/Failure views with WS5.
- Ship Validation/Evidence views with WS6.
- Ship Evaluation views with WS7.

Acceptance: no backend workstream reaches its exit gate with database-only or CLI-only functionality.

### WS8-04 — Add comparisons and deep links

- Implement relationship navigation and backlinks.
- Add context, execution, candidate, validation, and evaluation comparisons.
- Preserve filters and comparison identity in URLs.
- Add raw/structured toggles for bounded evidence and instructions.

Acceptance: the full goal-to-evidence path is navigable without manually copying IDs.

### WS8-05 — Align MCP, API, and CLI inspection

- Add equivalent detail/list/filter/comparison operations.
- Document which surface is intended for agents, human automation, or dashboard use.
- Add explicit commands answering every developer-experience question from the source brief.

Acceptance: each required question maps to at least one agent-facing and one human-facing operation.

### WS8-06 — Harden operations and documentation

- Add empty/error/loading/legacy/stale/blocked/regressed states.
- Apply redaction and output-size limits consistently.
- Add retention/vacuum inspection so deleted blobs never leave broken links.
- Update README, `SPEC.md`, agent integration, host docs, and dashboard documentation.
- Add seeded end-to-end scenarios and release checks.

Acceptance: a migrated historical database and a fresh goal-oriented project both render without console/API errors or misleading completion claims.

## Developer-experience question mapping

Every question below must have a named API/MCP/CLI/dashboard path and an automated contract test:

- What is the project's end goal and current completion state?
- Which required capabilities remain incomplete, blocked, or regressed?
- Why is this resource being generated and what progress is expected?
- What exact context did the agent receive, and why was each item included, truncated, or omitted?
- Which host/model/role/tools/settings produced this candidate?
- Which validation rejected it and what raw evidence was captured?
- Was failure caused by intent, specification, context, architecture, implementation, integration, validation, tool, host, or model behavior?
- What evidence proves a goal complete and for which exact source tree?
- Which completed goals were invalidated by a change?
- What should happen next?
- How do two attempts differ across context, execution, candidate, validation, cost, and outcome?

## Test plan

- Handler tests for every endpoint, error envelope, pagination/filter, redaction, legacy record, and relationship link.
- Query service tests against seeded fresh, active, complete, blocked, regressed, evaluation, and migrated historical databases.
- Static asset embed and route fallback tests.
- JavaScript unit tests for pure formatting/routing/comparison helpers using a dependency-free supported runner where practical; otherwise keep domain logic in tested Go view models.
- Accessibility checks for landmarks, labels, keyboard navigation, focus, status announcements, and color-independent state indicators.
- End-to-end browser smoke scenarios may run as an optional CI job; core Go/API tests remain sufficient for environments without a browser toolchain.
- MCP/CLI parity contract tests over representative IDs and filters.
- Deep-link integrity tests covering the full goal-to-evidence chain and backlinks.

## Compatibility and rollout

Existing unversioned endpoints remain during a short transition while the modular UI moves to `/api/v1`; remove them only after bundled UI and documented consumers are migrated. Existing dashboard information remains available throughout. Legacy records are explicitly labeled and never shown as provenance-complete.

## Exit gate

- All required views use canonical SQLite-backed query services.
- The embedded application is modular, accessible, and single-binary.
- Every feature workstream is visible through dashboard and inspection surfaces.
- Deep links cover the complete goal-to-evidence lifecycle.
- No second persistence model or operational sidecar exists.
