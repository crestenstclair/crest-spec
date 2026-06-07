# Overnight Decisions Log

Decisions encountered while fixing drift issues that need your input.

---

## Decision 1: Destroy execution is automatic in Begin()

**Context:** Spec says destroys should delete files and state. The question was whether destroys should require explicit agent confirmation or be auto-executed.

**Decision:** Destroys execute automatically during `Begin()` — no LLM dispatch needed. The rationale is that destroys just delete files that are no longer in the spec, which is a deterministic operation. The `BeginResult` now includes a `DestroyedResources` field so agents can see what was cleaned up.

**Needs review?** Low risk, but check if you want a confirmation gate before auto-deleting files.

---

## Decision 2: Apply() uses "light" review level by default

**Context:** The `Apply()` automated pipeline needs a review level for the constraint loop. Options were: skip, light, full, solid.

**Decision:** Defaulted to `"light"` (bugbot scan). Full code review on every resource in automated mode would be very slow and expensive. The manual pipeline still allows agents to choose their own review level.

**Needs review?** May want to make this configurable via env var (e.g., `CREST_SPEC_APPLY_REVIEW_LEVEL`).

---

## Decision 3: Wave verification runs TypeCheckCommand and TestCommand

**Context:** `WaveMaxRetries` config existed but was never read. Added wave-level verification that runs after all resources in a wave are committed.

**Decision:** `VerifyWave()` now:
1. Checks for errored/rejected resources in the wave
2. Runs `TypeCheckCommand` if configured
3. Runs `TestCommand` if configured
4. Attempts to attribute errors to specific resources via file path matching in stderr

Error attribution is best-effort — if stderr doesn't contain a recognizable file path, the error is reported without a resource ID.

**Needs review?** The error attribution is simple string matching. May want more sophisticated parsing for specific build tools.

---

## Decision 4: spec/note and spec/resolve are now separate methods

**Context:** Both `spec/note` and `spec/resolve` were calling the same `Resolve()` method, making them functionally identical.

**Decision:** Separated them:
- `Note()` — saves a design decision note (no state changes)
- `Resolve()` — saves guidance, optionally stores model override, resets resource to "pending" for re-dispatch

**Needs review?** No — this matches the spec's intent.

---

## Decision 5: Amend cascades by invalidating effective hashes

**Context:** When a resource is amended, its dependents need to be flagged for regeneration.

**Decision:** `Amend()` sets dependent resources' effective hashes to empty string, which causes the planner to detect a mismatch on the next plan. It also resets session resource states to "pending".

**Needs review?** No — clean approach that uses existing planner logic.

---

## Decision 6: SPEC.md change proposals written separately

**Context:** Found 16 cases where SPEC.md should be updated to match intended behavior (not bugs — documentation gaps, naming differences, undocumented features).

**Decision:** Written to `docs/spec-change-proposals.md` for your review. SPEC.md was NOT modified per your directive.

**Needs review?** Yes — review the 16 proposals in that document.

---

## Summary of what was NOT implemented

These items from the audit are logged but not addressable without design decisions:

1. **Poll-based orchestrator loop (Critical #4):** The spec describes an internal polling orchestrator, but the current architecture correctly delegates orchestration to the external agent. This is arguably better design — the spec should be updated. See proposal doc.

2. **SSE streaming in HTTP transport:** Would require significant transport layer changes. Added to spec change proposals as "planned" feature.

3. **Drift revert:** `DriftAction("revert")` still returns "not yet implemented" — reversing generated files requires storing original content, which isn't in the current schema.

4. **`consumes`/`publishes` edge kinds:** Declared in spec but unclear use case. Not implemented. Proposed removing from spec or marking as planned.

5. **`bootstrap` MCP tool:** Spec lists 11 engine tools but only 10 exist. The `bootstrap` tool's purpose is unclear from spec alone.
