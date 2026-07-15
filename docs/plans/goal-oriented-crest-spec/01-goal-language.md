# Workstream 1: Goal Language and Completion State

Status: **Implemented**

Depends on: none  
Enables: WS2–WS8  
Original scope: Workstream 1

## Objective

Make project intent a required, machine-readable part of the CUE specification and introduce an evidence-derived project-completion model. Goals must affect planning, context, validation, persistence, inspection, and finish decisions rather than acting as decorative prose.

## Requirements

### Specification

- Require a non-empty mission, at least one actor, goal, capability, requirement, and required goal.
- Give every actor, goal, capability, requirement, acceptance scenario, evidence requirement, and completion check a stable key.
- Support required and optional goals, goal dependencies, functional and nonfunctional requirements, explicit non-goals, target actors, acceptance scenarios, and evidence requirements.
- Allow capabilities to support multiple goals and goals to require multiple capabilities.
- Validate all references and reject goal dependency cycles.
- Permit a declared capability to have no resource contribution so crest-spec can report a specification/architecture gap instead of hiding it.
- Keep lifecycle status out of CUE; status is operational state.

### Completion

- Derive goal state from current declarations, plan coverage, contributing resource state, integration validation, behavioral evidence, goal dependencies, and blockers.
- Preserve the lifecycle `declared`, `planned`, `partially_implemented`, `locally_validated`, `integrated`, `behaviorally_verified`, `complete`, `blocked`, and `regressed`.
- Treat optional goals as visible but non-blocking.
- Require all required goals and project checks before project completion.
- Persist every state transition with its reason, source event, session, and prior state.
- Never accept a caller-supplied `complete` status.

### Persistence and visibility

- Reconcile specification identities into normalized SQLite tables without making SQLite the specification authoring source.
- Expose mission, actors, goals, capabilities, requirements, acceptance, evidence requirements, blockers, status, and history through Go APIs, MCP, read-only CLI inspection, and dashboard endpoints.
- Do not introduce goal status files or generated reports.

## Proposed CUE interface

```cue
project: {
    name:    "example"
    mission: "Allow teams to register and administer accounts safely"

    actors: {
        end_user: {description: "person creating and using an account"}
        admin:    {description: "operator administering accounts"}
    }

    goals: {
        registration: {
            description:  "An end user can create a usable account"
            priority:     "required"
            actors:       ["actor.end_user"]
            dependsOn:    []
            capabilities: ["capability.create_account"]
            requirements: ["requirement.unique_email"]
        }
    }

    capabilities: {
        create_account: {
            description: "Accept registration details and create an account"
            goals:       ["goal.registration"]
            acceptance: {
                successful_registration: {
                    description: "Valid details create an active account"
                    evidence: ["evidence.registration_witness"]
                }
            }
        }
    }

    requirements: {
        unique_email: {
            kind:        "functional"
            description: "An email address identifies at most one account"
            goals:       ["goal.registration"]
        }
    }

    evidence: {
        registration_witness: {
            kind:        "behavioral_witness"
            description: "Registration succeeds and the account can authenticate"
        }
    }

    nonGoals: {
        social_login: "OAuth and social identity providers are excluded"
    }

    completion: {
        requiredGoals: ["goal.registration"]
        projectChecks: ["validation.full_test_suite"]
    }
}
```

Exact witness and validation definitions are completed in WS6. WS1 validates reference syntax and identity even when the referenced executable definition is introduced by that later schema extension.

## State derivation rules

Evaluate states in this precedence order:

1. `regressed` when a previously complete goal has stale or failed required evidence after an impact event.
2. `blocked` when an unresolved specification, context, integration, tool, host, or implementation failure prevents the next required transition.
3. `complete` when dependencies are complete, all required capabilities are behaviorally verified, all evidence requirements are current, and goal checks pass.
4. `behaviorally_verified` when acceptance evidence passes but a dependency or goal-level completion check remains.
5. `integrated` when all required resource contributions pass integration checks.
6. `locally_validated` when all currently declared contributing resources pass current resource checks.
7. `partially_implemented` when at least one contribution is accepted but local coverage is incomplete.
8. `planned` when an active plan targets the goal but no contribution is accepted.
9. `declared` otherwise.

Project state uses the same exceptional-state precedence. A project is `complete` only when every `completion.requiredGoals` goal is complete and every project check has current passing evidence.

## Data model ownership

WS1 owns tables or equivalent repository concepts for:

- projects and reconciled specification hashes
- actors
- goals and goal dependencies
- capabilities and capability-goal relationships
- requirements and their goal/capability relationships
- acceptance scenarios
- evidence requirements
- project completion requirements
- current goal/project status
- goal/project status history
- blockers and blocker resolutions

Specification rows use deterministic IDs derived from canonical CUE identifiers. Historical rows use generated event IDs. Deleting a declaration during reconciliation must preserve historical status/evidence references through tombstoning or snapshot identity, not destructive cascading.

## API changes

- Add public domain types for `Goal`, `Capability`, `Requirement`, `AcceptanceScenario`, `EvidenceRequirement`, `CompletionPolicy`, `CompletionStatus`, `StatusTransition`, and `Blocker`.
- Extend plan results with project identity and current completion projection.
- Add MCP resources or tools for project overview, goal detail, goal history, blockers, and completion explanation.
- Add read-only CLI forms equivalent to `crest-spec status` and `crest-spec inspect goal <id>`.
- Add typed dashboard endpoints under a versioned API namespace.

## Tasks

### WS1-01 — Define the intent domain model

- Add the CUE-decoded Go types and canonical ID helpers.
- Define enums and validation rules for priorities, requirement kinds, evidence kinds, and statuses.
- Specify canonical ordering for hashing and rendering.
- Document why status is derived rather than authored.

Acceptance: representative multi-goal CUE decodes deterministically and produces stable IDs independent of map iteration order.

### WS1-02 — Validate goal specifications

- Validate required project fields and non-empty collections.
- Resolve actor, goal, capability, requirement, evidence, and completion references.
- Detect goal cycles and self-dependencies.
- Report all discoverable validation errors in one deterministic result.
- Classify missing contributions as a completion blocker, not a loader error.

Acceptance: invalid references and cycles fail `spec/validate`; declared but unimplemented capabilities remain inspectable as incomplete.

### WS1-03 — Add transactional persistence

- Replace the broad draft migration with the first focused migration.
- Add SQL queries, store domain types, and narrow repository interfaces.
- Add an explicit transaction helper for reconciling a complete intent snapshot.
- Preserve historical references when current declarations are removed.

Acceptance: reconciliation is atomic and foreign-key clean; a forced mid-transaction failure leaves the prior snapshot intact.

### WS1-04 — Implement completion projection

- Implement the precedence and transition rules above as a deterministic service.
- Persist current projection and append-only transition history.
- Record the inputs/reasons used for each transition.
- Make repeated projection idempotent.

Acceptance: identical inputs produce no duplicate transition; changed evidence produces exactly one explainable transition.

### WS1-05 — Expose inspection surfaces

- Add Go query services, MCP tools/resources, CLI inspection, and dashboard API models.
- Return concrete missing capabilities, missing evidence, dependencies, and blockers rather than a score alone.
- Add the first Project Overview and Goal Explorer dashboard panels through WS8's modular shell.

Acceptance: a developer can answer the mission, incomplete required goals, blockers, and completion reason without direct SQL.

### WS1-06 — Migrate examples and documentation

- Update all tracked fixtures, phase fixtures, e2e specs, README examples, authoring skill examples, and `SPEC.md`.
- Add a migration guide for external users because goals are required immediately.
- Ensure bootstrap/import output produces a valid goal skeleton or a loud TODO that cannot be mistaken for completion.

Acceptance: every tracked CUE fixture passes strict goal validation and no legacy implicit-goal path exists.

## Test plan

- Loader tests: minimal valid project, empty mission, missing collections, invalid IDs, duplicate canonical IDs, dangling references, and goal cycles.
- Registry tests: many-to-many capabilities, cross-goal requirements, optional goals, and declared capability without resources.
- Store tests: empty/current database migration, snapshot reconciliation, tombstoned declaration, transaction rollback, history retention, and reopen idempotence.
- Projection table tests covering every normal and exceptional transition, dependency effects, stale evidence, and optional goals.
- MCP/API tests for list, detail, history, blockers, and explanations.
- Fixture and e2e validation tests proving all bundled examples use the strict model.

## Compatibility and rollout

This is an intentional specification-level breaking release. Specs without the required intent model fail validation with a structured migration message. Existing SQLite databases migrate additively and retain historical resources, sessions, generations, checks, and learnings. No synthetic legacy goals are created.

The current project cannot enter a generation session until its CUE spec passes strict goal validation. That prevents apparently successful legacy runs from producing an undefined project-completion result.

## Exit gate

- Strict goal DSL and reference validation are merged.
- Current intent and status history are SQLite-backed and transactional.
- Completion is deterministic and explainable.
- Every tracked fixture and public example is migrated.
- Project and goal state are visible through MCP/API/CLI/dashboard.
