# Workstream 4: Context Selection, Budgeting, and Manifests

Status: **Draft**  
Depends on: WS1, WS2, WS3  
Enables: WS5, WS6, WS7, WS8  
Original scope: Workstreams 4 and 5

## Objective

Give each sub-agent enough relevant information to implement integrated functionality and make the exact served context reproducible, inspectable, and comparable through canonical SQLite state.

## Requirements

- Shape context around project intent, target capability, acceptance behavior, resource contract, dependencies, consumers, relevant code, integration behavior, validation, and failures.
- Select relevant material rather than dump the repository.
- Apply explicit budgets and preserve priority in this order: goal/task, acceptance, contract, dependencies/consumers, code, failures, conventions/examples, broader background.
- Record included, truncated, and omitted sources with hashes, sizes, reasons, and token estimates.
- Persist template version/hash, selector version/configuration, rendered section hashes, total context hash, and host/model references when available.
- Reconstruct exactly what crest-spec served without reading mutable working-tree files.
- Compare two attempts structurally and by source/content changes.
- Store no context-manifest project files.

## Context sections

The selector builds typed candidates for:

1. Project mission, actors, target goals, capability, requirements, non-goals, completion status, and current gaps
2. Acceptance scenarios and required evidence
3. Exact task, operation category, expected outcome, allowed/forbidden files, and required commands
4. Resource declaration, architectural responsibility, invariants, and amendments
5. Direct dependencies and their contracts/owned implementation
6. Direct consumers/dependents and their expected calls/data flow
7. Cross-resource invariants, design contracts, and context relationships
8. Existing owned files, call sites, related types/interfaces, tests, manifests, schemas, and migrations
9. Integration inputs/outputs, side effects, errors, persistence, and downstream consequences
10. Previous candidate, raw validation failure, classifications, triage guidance, and accepted patterns
11. Repository instructions, conventions, neighboring examples, recent relevant diffs, and active learnings

## Selection and budgeting

- Use deterministic candidate discovery and canonical ordering.
- Assign a mandatory/priority class, estimated token count, and inclusion reason to every candidate.
- Mandatory sections that exceed budget produce a structured `context_budget_blocked` result; crest-spec does not silently drop the goal, acceptance criteria, task, or resource contract.
- Lower-priority material may be truncated using source-type-specific strategies or omitted with a reason.
- The initial estimator is deterministic characters/bytes-to-token approximation with an explicit estimator version; host-reported actual tokens may be recorded later but never rewrite the manifest.
- Default budgets are configurable by role and host request within engine-defined minimum/maximum bounds.
- Context hashes cover ordered section kind, title, content hash, source decisions, selector version, template hashes, and budget.

## Candidate discovery

Prefer explicit, reliable relationships:

- registry dependencies and reverse dependents
- generated-file ownership
- declared metadata references/examples
- validation command/file references
- bounded-context design contracts
- project manifests and language-specific module declarations
- resource-owned tests and common test naming patterns
- relevant current git diff when available
- accepted prior files and retry candidates

Language-specific call-site discovery is an optional selector plugin with deterministic output. Failure to run an optional discovery plugin is recorded as an omission, not hidden.

## Attempt and manifest lifecycle

`spec/context(session_id, resource_id, role, budget?)` performs one transaction:

1. Validate that the resource/plan operation is eligible.
2. Create a generation attempt with a server-generated ID, retry number, role, session, plan operation, resource, and optional parent attempt.
3. Snapshot selected content into content-addressed SQLite blobs.
4. Persist the context manifest, sections, sources, omissions, selection decisions, and hashes.
5. Transition the session resource to dispatched.
6. Return the attempt and manifest identifiers plus structured/rendered context.

Each call creates a new immutable attempt. A retry points to its parent; it does not mutate the previous manifest. If the transaction fails, dispatch state and partial manifest rows are rolled back.

## Persistence ownership

WS4 owns:

- content-addressed blobs and content metadata
- generation attempts and parent/retry relationships at context-prepared state
- context manifests
- context sections and token allocations
- source snapshots and specification-fragment references
- included/truncated/omitted decisions and reasons
- selector/template/estimator versions and hashes

Blob content is deduplicated by hash. A manifest references immutable blobs so later file edits do not change historical context. Retention may release unreferenced blobs only after referential checks.

## API changes

`ContextResult` gains:

- `attempt_id`
- `context_manifest_id`
- `context_hash`
- `selector_version`
- `template_hashes`
- `budget` and estimated allocation
- structured ordered sections
- rendered system/resource prompt for hosts that consume concatenated text

Add context detail, source, omission, and comparison queries to Go, MCP, CLI, and dashboard APIs.

## Tasks

### WS4-01 — Define selection policy and budgets

- Define section/source kinds, mandatory classes, priority ordering, budgets, estimator version, and truncation rules.
- Define role-specific defaults without coupling to a model provider.
- Define blocked behavior when mandatory material exceeds the budget.

Acceptance: policy configuration is versioned and identical candidates produce identical decisions.

### WS4-02 — Build rich context candidates

- Extend runtime context discovery with goals, acceptance, consumers, call sites, tests, manifests, schemas, migrations, diffs, failures, and integration contracts.
- Record discovery failures and unsupported selectors.
- Avoid duplicate content through source identity and content hashes.

Acceptance: a vertical-slice fixture includes both dependencies and consumers plus the acceptance behavior it must enable.

### WS4-03 — Select, truncate, and render deterministically

- Implement budget allocation in required priority order.
- Add safe source-specific truncation with explicit markers.
- Produce canonical ordered structured sections and legacy rendered prompts.
- Calculate manifest and section hashes.

Acceptance: reducing a budget removes lower-priority context first and never silently removes mandatory intent/contract sections.

### WS4-04 — Persist immutable manifests

- Add content-blob and context migrations, generated queries, repositories, and transaction operations.
- Snapshot included and truncated material; record omitted material metadata and reasons.
- Add reference-safe retention/vacuum behavior.

Acceptance: a manifest remains reconstructable after all referenced working-tree files change.

### WS4-05 — Make context attempt-oriented

- Change `spec/context` to create immutable attempts/manifests and return identifiers.
- Link retries to parents and planner operations.
- Ensure dispatch and persistence are atomic.
- Preserve failure/guidance UPDATE-mode behavior as new attempt inputs.

Acceptance: every newly dispatched generation has exactly one context manifest and no orphan dispatched state exists.

### WS4-06 — Add inspection and comparison

- Expose complete context, allocation, sources, decisions, omissions, truncation, versions, and hashes.
- Compare section/source additions, removals, content changes, budget changes, and template/selector changes.
- Build the Context Inspector and attempt comparison view through WS8.

Acceptance: a failed and successful retry can be compared without querying mutable project files.

## Test plan

- Candidate-discovery tests for dependencies, consumers, owned files, tests, manifests, schemas, recent diffs, retry failures, and unsupported plugins.
- Budget table tests at boundary sizes, mandatory overflow, truncation, omission, and deterministic tie-breaking.
- Golden structured/rendered context tests with stable hashes.
- Store tests for transaction rollback, blob deduplication, immutable reconstruction, retry ancestry, and retention.
- Security tests ensuring path normalization remains within project scope and secret-bearing environment/config values are not captured.
- MCP/API/dashboard tests for detail and comparisons.
- Regression tests for all existing context feedback, design-contract, learnings, and UPDATE-mode behavior.

## Compatibility and rollout

Hosts may continue consuming concatenated `system_prompt` and `prompt` fields during the protocol migration, but must propagate `attempt_id` once WS5 makes attempt-aware commit mandatory. Existing historical generation rows remain visible as legacy attempts without fabricated manifests.

## Exit gate

- Every new context dispatch creates a complete immutable SQLite manifest.
- Selection is relevant, budgeted, deterministic, and explainable.
- Goals, acceptance, dependencies, consumers, code, integration, and failure evidence are represented.
- Context comparison works across attempts.
- No manifest or observability sidecar is written to the project tree.

