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

## Decision 7: Vision doc implementation — completed state transition

**Context:** The spec state machine diagram shows `completed` as an intermediate state between file writing and final commit. The existing code went directly from `dispatched` to `committed`.

**Decision:** Modified `Commit()` to transition to `completed` after writing files and before running validations. This matches the Terraform model: apply writes, then verifies, then marks done. The state was already defined in state.go — it just wasn't used.

**Needs review?** No — straightforward spec alignment.

---

## Decision 8: Structured review output with fallback

**Context:** The constraint loop's review step was doing fragile string matching (`FAIL`, `PASS`, `critical`). Vision doc flagged this as Tier 1 priority.

**Decision:** Added `ReviewOutput` and `ReviewFinding` structs. Review prompts now request JSON output. Response parsing tries JSON unmarshal first, falls back to string matching if the LLM doesn't comply. This means existing behavior is preserved while enabling structured output when available.

**Needs review?** No — backward compatible, strictly better.

---

## Decision 9: Error attribution now parses compiler output

**Context:** Wave verification's error attribution was just `strings.Contains(errorOutput, filePath)`. Vision doc flagged this as Tier 1.

**Decision:** Added `parseErrorFilePaths()` that understands Go, Rust, TypeScript, Python, and C/C++ compiler output formats using regex patterns. Falls back to substring matching if no pattern hits. Normalizes paths (strips `./` prefix) before comparing.

**Needs review?** No — additive improvement with fallback.

---

## Decision 10: Mode/environment support via CREST_SPEC_MODE

**Context:** Vision doc identified Terraform workspace equivalence as a Tier 2 gap.

**Decision:** Added `CREST_SPEC_MODE` env var (default: "default") and `project.meta.mode` CUE field. Mode is included in effective hash computation, so changing mode cascades regeneration correctly. CUE mode takes precedence over env var when set. Added `spec/mode` tool to query current mode.

**Needs review?** Low risk. Users can set `CREST_SPEC_MODE=debug` to regenerate everything with debug-specific rules.

---

## Decision 11: spec/import uses heuristic classification, no LLM

**Context:** Vision doc's spec/import MVP. The question was whether to use LLM classification or filename heuristics.

**Decision:** MVP uses filename/path heuristics only (e.g., "repository" in name → Repository resource, "service" → DomainService). Groups by directory into bounded contexts. Generates skeleton CUE that users hand-correct. LLM classification can be added later as an enhancement.

**Needs review?** No — intentionally conservative. "Good enough to hand-correct" is the right bar for MVP.

---

## Decision 12: spec/prompt tool for debugging

**Context:** Vision doc listed `spec/prompt <resource_id>` as part of the debugging console gap.

**Decision:** Added `spec/prompt` tool that builds and returns the full system prompt + resource prompt without dispatching to an LLM. Users can review exactly what the model would see.

**Needs review?** No — read-only debugging aid.

---

## Decision 13: Vacuum now cleans agent_notes and session_resources

**Context:** Audit finding #15 — Vacuum only cleaned generations, invariant_checks, apply_actions, and applies. Agent_notes and session_resources accumulated forever.

**Decision:** Added both tables to Vacuum. Agent_notes cleaned by `created_at`, session_resources by `updated_at` (it doesn't have a `created_at` column).

**Needs review?** Low risk. These are historical data that grows indefinitely without cleanup.

---

## Summary of what was NOT implemented

These items from the audit are logged but not addressable without design decisions:

1. **Poll-based orchestrator loop (Critical #4):** The spec describes an internal polling orchestrator, but the current architecture correctly delegates orchestration to the external agent. This is arguably better design — the spec should be updated. See proposal doc.

2. **SSE streaming in HTTP transport:** Would require significant transport layer changes. Added to spec change proposals as "planned" feature.

3. **Drift revert:** `DriftAction("revert")` still returns "not yet implemented" — reversing generated files requires storing original content, which isn't in the current schema.

4. **`consumes`/`publishes` edge kinds:** Declared in spec but unclear use case. Not implemented. Proposed removing from spec or marking as planned.

5. **`bootstrap` MCP tool:** Spec lists 11 engine tools but only 10 exist. The `bootstrap` tool's purpose is unclear from spec alone.
