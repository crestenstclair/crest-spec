# Workstream 3: Goal-Directed Planning and Change Reconciliation

Status: **Implemented**

Depends on: WS1, WS2  
Enables: WS4–WS8  
Original scope: Workstreams 3 and 11

## Objective

Extend reconciliation from resource hash differences to functional gap closure. Plans must retain deterministic resource operations and waves while explaining the goal, capability, behavior, evidence, and integration path each operation advances.

## Requirements

- Persist immutable plan snapshots and revisions for started sessions.
- Group operations into coherent capability slices without changing valid graph ordering.
- Prioritize required goals, goal dependencies, blockers, and completion gaps deterministically.
- Classify operations as `structural`, `corrective`, `integrative`, or `verification`.
- Record expected progress and compare it with actual status/evidence transitions.
- Record blockers, plan failures, and replanning decisions.
- Extend impact analysis from declarations/files through resources, dependents, capabilities, goals, validations, and evidence.
- Re-verify affected completed goals after relevant changes.
- Preserve human-gated deletion and current effective-hash semantics.

## Planning model

A plan contains ordered capability slices. Each slice contains:

- target goals and capability
- current functional gap
- expected observable behavior
- resource operations and categories
- graph dependencies and execution waves
- required validation/evidence
- current blockers
- expected completion transition

Resource waves remain calculated from the resource dependency graph. Slice priority changes which ready capability is selected first, not dependency correctness or stable ordering within a wave.

## Plan persistence lifecycle

- `spec/plan` builds a non-mutating preview with a deterministic content hash.
- `spec/begin` atomically persists the selected plan, its operations/slices/waves, the source specification hash, and the initial expected goal impact.
- The persisted session plan is immutable. A replan creates a revision linked to its predecessor with a reason and changed assumptions.
- Actual resource, validation, and goal outcomes are recorded against operations rather than rewriting expected values.
- Destroy operations remain pending until explicit confirmation and retain their goal-impact explanation.

## Impact and regression model

Impact starts from one of: specification declaration change, accepted candidate file set, deleted file/resource, validation definition change, witness change, or execution-policy change.

The engine persists the path:

```text
change event
  -> directly affected resource/declaration/file
  -> structurally affected dependents
  -> capability contribution
  -> goal
  -> evidence requiring invalidation/re-run
  -> project completion consequence
```

Evidence is current only for the exact relevant source, declaration, witness, validation, and context-policy hashes recorded when it passed. An impact that intersects those inputs marks the evidence stale. A previously complete affected goal transitions to `regressed` until new evidence passes.

## API changes

Enrich `PlannedAction` or replace it at the public boundary with fields for operation ID, slice ID, goals, capability, category, rationale, expected behavior/evidence, blockers, wave, cascade/impact source, and outcome.

Add query surfaces for:

- current plan preview
- persisted plan and revisions
- capability slice detail
- expected versus actual progress
- change-impact chain
- stale evidence and regressed goals
- recommended next work

## Tasks

### WS3-01 — Define and persist goal-directed plans

- Add plan, revision, slice, operation, dependency, blocker, and expected-impact domain types.
- Define canonical plan hashing and ordering.
- Add migrations, SQL queries, narrow repositories, and transactional `spec/begin` persistence.
- Retain the original resource action and wave data for compatibility and debugging.

Acceptance: starting a session creates one immutable, fully related plan snapshot or no records on failure.

### WS3-02 — Build functional gap analysis

- Compare declared capabilities/acceptance with resource state and current evidence.
- Produce gaps for missing declarations, unimplemented contributions, local validation, integration, behavioral evidence, regressions, and blockers.
- Map every resource operation to a gap and expected progress.

Acceptance: every planned operation has a goal-directed reason or is explicitly identified as shared infrastructure supporting named slices.

### WS3-03 — Plan vertical slices deterministically

- Group actions by capability while allowing shared operations to support multiple slices.
- Prioritize required goals, goal dependency order, regressed functionality, blocked prerequisites, and optional goals in that order.
- Preserve graph dependencies and deterministic wave ordering.
- Add operation categories and required verification work.

Acceptance: repeated plans over identical state are byte-stable and never schedule a dependent before its resource prerequisites.

### WS3-04 — Implement impact analysis

- Define change-event inputs and construct the full impact chain.
- Include direct resources, structural dependents, contributions, goals, validations, witnesses, and project status.
- Persist impact nodes/edges and human-readable reasons.
- Avoid invalidating unrelated goals that merely share a session.

Acceptance: a declaration or owned-file change returns the same affected set through in-memory and persisted queries.

### WS3-05 — Invalidate evidence and project completion

- Compare current hashes with evidence provenance.
- Mark evidence stale rather than deleting historical passing runs.
- Reproject affected goal/project state and record regression transitions.
- Add regression verification operations to the next plan.

Acceptance: a completed goal becomes regressed after a relevant change and returns to complete only after current evidence passes.

### WS3-06 — Expose plans and impact

- Extend MCP/API/CLI inspection for plan rationale, slices, operations, revisions, blockers, expected/actual progress, and impact paths.
- Build Plan Explorer and change-impact views through WS8.
- Deep-link operations to goals, resources, later attempts, validations, and evidence.

Acceptance: developers can explain why a resource is scheduled and which completed behavior a change invalidated.

## Test plan

- Planner golden tests for deterministic slices, shared resources, required/optional priority, goal dependencies, regressed goals, and verification-only work.
- Compatibility tests retaining create/modify/destroy classification, structural hashes, target filtering, force behavior, and waves.
- Session tests for immutable plan snapshots, revision links, expected/actual outcomes, and transactional failure.
- Impact tests for direct, transitive, multi-owner file, validation-only, witness-only, and unrelated changes.
- Regression tests for stale evidence, failed re-verification, restored completion, and project-level consequences.
- Human deletion tests proving impact is shown before confirmation and no deletion bypass is introduced.
- Dashboard/API tests for plan revision and full impact-chain deep links.

## Compatibility and rollout

Existing resource operations remain available in public results during a transition window, but new hosts and dashboard views consume persisted plan operation IDs. Session plan JSON may remain readable for migration, but new canonical plan state is normalized rather than extended through additional JSON blobs.

## Exit gate

- Plans are persisted, immutable, deterministic, goal-directed, and explainable.
- Vertical slices preserve dependency correctness.
- Change-impact paths and stale evidence are SQLite-backed.
- Completed goals regress and re-verify correctly.
- Existing reconciliation and deletion safeguards continue to pass.
