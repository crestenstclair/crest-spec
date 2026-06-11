# Plan: Drop Drift Detection

**Status:** Implemented (2026-06-10)
**Date:** 2026-06-07
**Author:** cresten

## TL;DR

Remove content-based drift detection entirely. Stop treating manual edits to
generated files as a problem that must be resolved before work can continue.

The spec controls **what resources exist and how they are shaped**. Once a file
has been generated, the disk is the source of truth for its **content**. You are
free to format it, fix a typo, tweak a constant, or hand-patch a bug without the
system fighting you.

Regeneration becomes **opt-in**, with exactly two triggers:

1. **Edit the spec** (declaration or a dependency changes) → the resource is
   re-rendered.
2. **Delete the file(s)** → the resource is re-rendered to recreate them.

That's it. No drift state, no accept/revert dance, no `spec/drift` tool.

---

## Why

### The current behavior

Today `Planner.classifyResource` (`internal/plan/planner.go:137`) runs, as its
final check, `checkDrift` (`planner.go:166`). For every committed resource whose
declaration and dependencies are unchanged, it:

1. Reads each generated file from disk.
2. Computes its SHA256.
3. Compares against the `content_hash` stored in `generated_files`.
4. If the file is **missing** *or* the **content differs**, emits an
   `ActionDrift` (`internal/plan/action.go:9`).

Drift actions are then partitioned out at session start
(`internal/spec/session.go`, `partitionActions` / `BeginResult.DriftActions`),
seeded into `session_resources`, and surfaced to the orchestrator, who must call
the `spec/drift` MCP tool (`internal/mcp/tools.go:809`) with `accept` or
`revert` for each one. `revert` isn't even implemented
(`internal/spec/query.go:104`) — it returns "not yet implemented."

### The problem

This makes **great the enemy of perfect.** The premise of drift detection is
that generated code should be byte-for-byte reproducible from the spec, and any
human edit is "drift" to be reconciled. In practice that premise is wrong for
how this tool is actually used:

- **Formatting.** `cargo fmt` / `gofmt` / prettier touch the file → drift.
  (We already learned this lesson once — see the leading-blank-line fmt bug.)
- **Minor fixes.** A one-line fix to generated code → drift, instead of just…
  a fix.
- **Iteration friction.** Every manual touch forces an accept/revert round-trip
  through a tool, one of whose two options doesn't work.

The cost of enforcement vastly exceeds its benefit. LLM generation is not
deterministic anyway, so "byte-for-byte reproducible" was never a real
guarantee — we were paying friction for a property we don't have.

### The new mental model

> The spec is a **scaffold generator**, not a **content lock**.

- Spec defines the set of resources and their shape.
- Generation produces an initial implementation.
- After that, **the code is yours.** Edit it freely.
- Want a fresh render? Change the spec, or delete the file and re-run.

This is the natural, low-friction workflow the user already expects. We stop
asserting ownership over file contents we don't actually own.

---

## What stays vs. what goes

### Goes (content-drift enforcement)

- The `ActionDrift` action kind and everything that fans out from it.
- The content-hash comparison in `checkDrift`.
- The `spec/drift` MCP tool and its `accept`/`revert` handler.
- Drift partitioning at session begin.
- The drift-specific test.

### Stays (the real regeneration triggers)

- **Declaration-hash check** (`planner.go:149`) — edit the spec, get a re-render.
  Unchanged.
- **Effective-hash / dependency-cascade check** (`planner.go:153`) — a dependency
  changing still cascades a re-render. Unchanged.
- **Destroy detection** (`planMyDestroys` / `planDestroys`, `planner.go:192`) —
  removing a resource from the spec still cleans up its files. Unchanged.
- **`content_hash` column** in `generated_files`. Keep it. It's cheap, it's
  written on commit, and it's the natural hook for the one drift behavior we
  *do* want (below). No schema migration needed.

### Changes shape (missing-file → regenerate)

This is the important nuance. We do **not** simply delete `checkDrift` and
return `nil`. If we did, a resource whose spec is unchanged would be classified
as up-to-date *even if its files were deleted* — so "delete the code to
re-render" wouldn't work.

Instead, `checkDrift` becomes `checkMissing`: it ignores content differences and
only emits an action when a file is **absent**. A missing file means "render
this resource again."

```go
// internal/plan/planner.go

func (p *Planner) classifyResource(...) (*PlannedAction, error) {
    stored, exists := storedMap[id]
    if !exists {
        return &PlannedAction{ResourceID: id, Kind: ActionCreate, Reason: "new resource"}, nil
    }
    if stored.DeclarationHash != declHash(r.Declaration) {
        return &PlannedAction{ResourceID: id, Kind: ActionModify, Reason: "declaration changed"}, nil
    }
    if stored.EffectiveHash != effectiveHashes[id] {
        cascadedFrom := findChangedAncestor(id, g, effectiveHashes, storedMap)
        return &PlannedAction{
            ResourceID:   id,
            Kind:         ActionModify,
            Reason:       fmt.Sprintf("dependency changed (%s)", cascadedFrom),
            CascadedFrom: cascadedFrom,
        }, nil
    }
    return p.checkMissing(id)
}

// checkMissing re-renders a resource only when its generated files are gone.
// Content edits are intentionally ignored — once generated, the file is the
// user's to modify. To force a re-render, edit the spec or delete the file.
func (p *Planner) checkMissing(id string) (*PlannedAction, error) {
    files, err := p.store.GetGeneratedFiles(id)
    if err != nil {
        return nil, fmt.Errorf("get generated files for %s: %w", id, err)
    }
    for _, f := range files {
        if _, err := p.fs.ReadFile(f.Path); err != nil {
            return &PlannedAction{
                ResourceID: id, Kind: ActionModify,
                Reason: "generated file missing — regenerating", Files: filePaths(files),
            }, nil
        }
    }
    return nil, nil
}
```

Note the missing-file case now emits `ActionModify`, not a drift action. It
flows through the normal generate/commit path like any other change — no special
handling, no separate partition, no resolution step.

---

## Implementation instructions

Work in order; each step compiles on its own.

### 1. `internal/plan/planner.go`

- Replace `checkDrift` with `checkMissing` as shown above (drop the SHA256
  import usage there; the `sha256` import is still used by `declHash`, so leave
  the import).
- Update the call site at `planner.go:163` from `p.checkDrift(id)` to
  `p.checkMissing(id)`.

### 2. `internal/plan/action.go`

- Remove the `ActionDrift` constant (`action.go:9`). Confirm no remaining
  references (`grep -rn ActionDrift internal/`).

### 3. `internal/spec/session.go`

- Remove drift partitioning from `Begin()` (the block around `session.go:131`
  that splits drift actions out).
- Remove the `DriftActions` field from `BeginResult`.
- In `partitionActions`, drop the `ActionDrift` branch. If the function now only
  separates destroys from create/modify, keep it; if it becomes trivial, inline
  it.
- In `seedSessionResources`, drift actions are no longer passed in — missing-file
  regenerations arrive as ordinary `ActionModify` and are already handled.

### 4. `internal/spec/query.go`

- Remove the `DriftAction` method (`query.go:80`) entirely. `revert` was never
  implemented and `accept` only existed to clear drift.

### 5. `internal/mcp/tools.go`

- Remove the `spec/drift` tool registration (`tools.go:809`) and the
  `specDriftArgs` struct (`tools.go:487`).
- Update any tool-count assertions or tool-list docs that enumerate tools.

### 6. `internal/spec/state.go`

- `StateBlocked` is defined but never set and was adjacent to the drift flow.
  Leave it unless `grep` shows it's now fully dead; if dead, remove it in a
  follow-up to keep this change focused.

### 7. Tests

- Delete `TestPlan_DriftDetection` (`internal/plan/planner_test.go:182`).
- Add `TestPlan_MissingFileRegenerates`: stored resource, unchanged declaration,
  file deleted from disk → expect `ActionModify` with reason
  "generated file missing — regenerating".
- Add `TestPlan_ModifiedContentIsIgnored`: stored resource, unchanged
  declaration, file present but content changed on disk → expect **no action**
  (this is the whole point — assert the new behavior, don't just delete the old
  test).

### 8. Docs

- Update `SPEC.md` and any tool reference that lists `spec/drift` or describes
  drift reconciliation. Add a short "Regeneration triggers" note: edit the spec
  or delete the file.
- This supersedes the drift-related rows in `docs/spec-drift-audit.md`.

---

## Verification

Run a **real** end-to-end pass (not a simulation):

1. Generate a resource through the normal `spec_plan → begin → next → context →
   run_prompt → poll → commit → finish` pipeline.
2. **Format / edit** the committed file by hand. Re-run `spec_plan`. Expect: the
   resource shows **no action**. The edit survives.
3. **Delete** the committed file. Re-run `spec_plan`. Expect: `ActionModify`
   ("generated file missing"). Regenerate and commit. The file comes back.
4. **Edit the spec** declaration. Re-run `spec_plan`. Expect: `ActionModify`
   ("declaration changed").
5. `grep -rn "drift\|ActionDrift\|spec/drift\|DriftAction" internal/` returns
   nothing but comments/history.
6. `go build ./... && go test ./...` is green.

---

## Risks & non-goals

- **Risk: a resource's spec changes but its hand-edits get blown away on
  re-render.** This is *intended* — editing the spec is an explicit "I want this
  regenerated" signal. If you want to keep manual edits, don't change the spec.
  We can revisit a merge/3-way strategy later if it ever becomes painful; it is
  out of scope here.
- **Non-goal:** removing the `content_hash` column or the effective-hash graph
  machinery. Both are kept and used.
- **Non-goal:** any new config flag to toggle drift. We're not making drift
  configurable — we're deleting it. No half-measures.
