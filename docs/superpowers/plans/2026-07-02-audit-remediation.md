# Audit Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Execute the 2026-07-02 viability-audit findings: close the feedback loop, close the fail-open gates, split the regen hash, inject mission context, surface drift, remove quiet-workaround paths, and rewrite the crest-synth fixture spec at the right altitude per `fixtures/crest-synth/DESIGN.md`.

**Architecture:** All engine changes live in the Go MCP server (`internal/`). The orchestration prompts live in `.claude/workflows/*.js` + `.claude/skills/`. The fixture spec moves from phase-based (`phases/*.cue` + overrides) to domain-grouped (`spec/*.cue`), mirroring the proven layout of the live `~/workspace/crest-synth` project.

**Tech Stack:** Go 1.26, SQLite (sqlc), CUE, Claude Code workflows (JS).

## Global Constraints

- Server stays a pure state engine: never calls an LLM, never spawns generation subprocesses (validations via RunCommand are fine).
- Gates fail CLOSED and LOUD. Never classify a failure out of the blocking set silently.
- No hand-written target-language (Rust) code anywhere in the fixture.
- `make test` green after every task; commit per task.

---

### Task 1: Wire retry feedback into spec/context

**Files:**
- Modify: `internal/spec/session.go:350-394` (Context)
- Modify: `internal/spec/runtime.go:12` (buildRuntimeContext signature unchanged; new assignments happen in Context)
- Test: `internal/spec/context_feedback_test.go` (new)

**Interfaces:**
- Produces: `Context()` result prompt containing `## Previous Errors` (from `session_resources.last_error`) and `## Guidance` (from the resource's OWN note for the current apply, excluding the `#model` note).

Steps:
- [x] Failing test: seed a session resource with `last_error` = "cargo check failed: mismatched types", a note on the resource itself ("use try_new, not new"), call `Context`, assert both strings appear in `Prompt`.
- [x] Implement: in `Context()` after `sr` fetch, set `runtimeCtx.WaveErrors = sr.LastError` (when non-empty) and `runtimeCtx.UserGuidance = store.GetNote(resourceID, sess.ApplyID)`. Rename the injected section header from "User Guidance" to "Guidance" (it now carries triage/resolve output).
- [x] `go test ./internal/spec/` green; commit.

### Task 2: Fail-closed validation launch at commit

**Files:**
- Modify: `internal/spec/session.go:512-545` (runCommitValidations)
- Test: `internal/spec/commit_test.go` (extend)

Steps:
- [x] Failing test: resource declares a validation whose command is a nonexistent binary; `Commit` must return `Committed=false` with a validation result explaining the launch failure (today it commits).
- [x] Implement: `if err != nil` from `RunValidations` → synthesize `ValidationResult{Kind:"validation", Passed:false, Message: "validation could not run: " + err.Error()}` and reject via `checkForFailure`.
- [x] Green; commit.

### Task 3: Retire generator-self-judged invariant verdicts

**Files:**
- Modify: `internal/spec/session.go` (Commit signature drops `invariantChecks`; delete `ingestInvariantChecks`), `internal/spec/spec.go`, `internal/mcp/server.go`, `internal/mcp/handlers.go`, `internal/mcp/tools.go` (schema), `internal/evolve/evolve.go` (reads of invariant_checks stay — table remains for history)
- Test: update `internal/spec/commit_test.go`, `internal/mcp/server_test.go`

Steps:
- [x] Remove the `invariant_checks` parameter end-to-end. `spec/commit` schema loses the field; `Commit` no longer records or gates on orchestrator verdicts. Invariants still flow OUT via `ContextResult.Invariants` (they inform generation; behavioral checks verify).
- [x] Update tool description: verification is mechanical validations at commit + behavioral checks at wave/finish.
- [x] Green; commit.

### Task 4: Behavioral gate at finish; malformed is loud

**Files:**
- Modify: `internal/spec/session.go:643-690` (Finish), `internal/spec/types.go` (FinishResult), `internal/mcp/tools.go` (finish description)
- Test: `internal/spec/finish_gate_test.go` (new)

**Interfaces:**
- Produces: `Finish(ctx, sessionID, force)` returns error-free refusal `FinishResult{Blocked:true, BlockingChecks:[...]}` listing every check in `pending|failed|theater|malformed` for the session's resources, unless `force=true`. Malformed checks block: the remedy is fixing the CHECK (re-run tasks stage) or explicit `force` — never silence.

Steps:
- [x] Failing test: session with a committed resource that has one `malformed` and one `pending` check → Finish returns Blocked with both listed; `force=true` finalizes.
- [x] Implement via `store.ListChecksByResource` over session resources.
- [x] Green; commit.

### Task 5: Split structural vs guidance hash

**Files:**
- Modify: `internal/graph/hash.go`, add `internal/graph/structural.go`
- Test: `internal/graph/structural_test.go` (new)

**Interfaces:**
- Produces: `StructuralJSON(decl any) []byte` — canonical JSON with guidance-only keys (`prompts`, `description`, `notes`, `rationale`, `examples`, `references`, `style`, `avoid`, `purpose`, `reviewLevel`) stripped recursively. `ComputeEffectiveHashes` hashes `StructuralJSON` instead of full decl JSON.
- Result: prompt/description edits regen the edited resource (declaration_hash, full JSON, still catches them — planner.go:149) but do NOT cascade to dependents (effective hash unchanged). NOTE: one-time full regen on upgrade since all effective hashes change; acceptable pre-1.0.

Steps:
- [x] Failing tests: (a) two declarations differing only in `prompts`/`description` → equal effective hashes; (b) differing in `state`/`invariants`/`contract` → different; (c) dependent of a guidance-only-changed resource classifies as no-op in planner.
- [x] Implement strip via marshal→map→recursive delete→re-marshal.
- [x] Green; commit.

### Task 6: Drift surfacing at plan time

**Files:**
- Modify: `internal/plan/planner.go`, `internal/plan/types.go` (or wherever PlannedAction lives), `internal/spec/spec.go` (PlanResult), `internal/mcp/tools.go`/`handlers.go` (spec/plan response)
- Test: `internal/plan/drift_test.go` (new)

**Interfaces:**
- Produces: `Planner.Plan` additionally returns `[]Drift{ResourceID, Path}` for files whose on-disk sha256 ≠ `generated_files.content_hash`. Advisory only — surfaced in `spec/plan` output as `drift`; does NOT regen (on-disk edits remain the user's).

Steps:
- [x] Failing test: commit a file, mutate it on disk, plan → drift entry present; untouched file → none.
- [x] Implement hash compare inside the existing `checkMissing` walk (rename to `checkFiles`).
- [x] Green; commit.

### Task 7: Mission layer + design-contract injection

**Files:**
- Modify: `internal/cue/types.go` (Project.Mission), `internal/prompt/system.go` (mission + architectural invariants sections), `internal/prompt/context.go` (DesignContract section), `internal/spec/runtime.go` (fetch design by resource.ContextName)
- Test: `internal/prompt/system_test.go`, `internal/spec/context_feedback_test.go` (extend)

**Interfaces:**
- Produces: `project: mission: "..."` CUE field → rendered as `# Project Mission` at the top of every system prompt, followed by `# Architectural Invariants` (project-level invariant texts + rationales). `RuntimeContext.DesignContract` renders as `## Bounded-Context Design Contract` in the resource prompt when a design row exists for the resource's context.

Steps:
- [x] Failing tests for all three injections.
- [x] Implement.
- [x] Green; commit.

### Task 8: Remove quiet-workaround paths from the workflow layer

**Files:**
- Modify: `.claude/workflows/spec-generate.js`, `.claude/workflows/spec-generate-resume.js`, `.claude/workflows/spec-verify-pending.js`, `.claude/skills/spec-generate/SKILL.md`, `.claude/skills/spec-authoring/SKILL.md`

Steps (no Go tests; consistency-check by re-reading):
- [x] Generator prompt: drop the judge-your-own-invariants step and `invariant_checks` from the commit call; state that `spec_context` now includes prior failure + guidance, so retries must address the `## Previous Errors` section explicitly.
- [x] Stall guard: replace auto-skip (`MAX_STALLS`/`MAX_STALLS_HARD` skipping) with loud halt: report unresolved resources and return a failed run. Skips happen only by explicit human-visible triage of a contradictory spec.
- [x] Triage prompt: `spec_skip` requires quoting the contradictory declarations; "hard" is not "impossible".
- [x] Malformed checks: workflow reports them as BLOCKERS (finish now refuses); remove "quarantined, continue" language. The remedy path is re-running the tasks stage for that resource to author a well-typed check.
- [x] SKILL.md files updated to match (anti-theater section documents the finish gate; authoring skill documents `mission`, structural-vs-guidance hash, and the language-profile convention).

### Task 9: Rewrite the crest-synth fixture spec (spec-authoring)

**Files:**
- Delete: `fixtures/crest-synth/phases/*.cue` (34 files)
- Create: `fixtures/crest-synth/spec/{project,kernel,engine,sample,effects,mixer,modulation,patch,preset,realtime,shell,manifest}.cue`
- Modify: `internal/cue/phases_test.go` → `internal/cue/fixture_test.go` (load the new layout; assert mission present, no crate names in domain files)

Content rules (from DESIGN.md + audit):
- `project.cue`: `mission` (2-3 sentences from DESIGN.md intro), layers + layerRules, contextMap (DESIGN.md context map), the 13 architectural invariants (behavioral text + rationale only), whole-tree `validations` (cargo fmt --check, clippy, build, test), meta: language/style/avoid.
- Domain files: value objects/aggregates/ports per DESIGN.md bounded contexts. Invariants stay behavioral ("must be 0.0-1.0", "voice reclaimable only when envelope Idle"). NO crate names, NO Rust API paths, NO version pins, NO compiler-error lore, NO stdout-token recipes, NO step-by-step implementation prompts.
- Crate/toolchain facts live in ONE place: adapter declarations may name their framework (`meta: framework: "cpal"`) and `manifest.cue`'s RootCargoToml asset lists the dependency table from DESIGN.md — nothing else mentions crates.
- No phase files, no override files.

Steps:
- [x] Author the CUE (spec-authoring skill loaded first).
- [x] Replace phases_test.go with fixture_test.go: loads `fixtures/crest-synth/spec`, asserts project name/mission, asserts zero occurrences of crate names in non-manifest, non-adapter files.
- [x] `go test ./internal/cue/` green; commit.

### Task 10: Documentation truth-up

**Files:**
- Modify: `SPEC.md` (Overview §, commit/verify §8: invariant-verdict removal, finish gate, hash split, drift, mission), `README.md` (loop description)

Steps:
- [x] Update; commit.

## Self-Review Notes

- Spec coverage: audit items 1-7 map to Tasks 5+9 (leakage/altitude), 1 (feedback), 3+4+2+8 (gate), 7 (mission), 6 (drift), 8 (workarounds), 10 (docs). Item "stop growing the engine" is honored: net engine LOC change is negative (Task 3 removes more than Tasks 1/2/4 add).
- Type consistency: FinishResult.Blocked/BlockingChecks named identically in Task 4 and Task 8's workflow updates.
