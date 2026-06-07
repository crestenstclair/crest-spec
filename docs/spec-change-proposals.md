# SPEC.md Change Proposals

Changes to SPEC.md that should be reviewed before applying. These are cases where
the spec either describes something differently than the intended implementation,
or where the code has evolved past what the spec documents.

**Rule:** SPEC.md is the source of truth. These proposals modify the spec only when
the spec is genuinely wrong, outdated, or missing documentation for features that
already exist and are correct.

---

## Proposal 1: Document `targets` edge kind in dependency table

**Section:** 1 — The CUE DSL (dependency edge kinds)

**Current spec:** Lists `uses`, `implements`, `of`, `consumes`, `publishes` as edge kinds.

**Proposal:** Add `targets` to the dependency table. It is implemented in the Go loader
and used in practice. Also remove `consumes` and `publishes` from the table — they are
declared in the spec but not implemented anywhere and have no clear use case yet. If
they are planned for future work, mark them as "planned" rather than listing them
as current features.

**Your response:**
- yes add targets
- no, consumes and publishes are important aspects of DDD. If they aren't implemented anywhere, they should be. they should be used for planning / diffing. They should also be passed around as context for sub-agents while developing

---

## Proposal 2: Document actual meta merge semantics (dedup vs concatenate)

**Section:** 1 — The CUE DSL (metadata)

**Current spec:** Claims meta merge concatenates values.

**Proposal:** Update to reflect actual behavior: meta merge uses dedup (duplicate
values are removed). This is the correct behavior — concatenation would produce
duplicates when the same rule appears at project and context level.

**Your response:**

Approved.


---

## Proposal 3: Update architecture package layout

**Section:** 2 — Architecture

**Current spec:** Lists `internal/jobs/`, `internal/verify/`, `internal/mocks/` as packages.

**Proposal:** Update to match actual layout:
- Remove `internal/jobs/` — job lifecycle lives in `internal/store/`
- Remove `internal/verify/` — verification lives in `internal/spec/validate.go`
- Remove `internal/mocks/` — tests use hand-written fakes, not counterfeiter
- Remove `internal/app/` reference if present — it is dead code (never imported)
- Note that `StdioTransport`/`HTTPTransport` are not separate types; `Server` handles both

**Your response:**

This is fine, i dont really care about code layout that much and it makes sense it would evolve organically.


---

## Proposal 4: Update engine method signatures

**Section:** 4 — Agent Wrapper & Engine Layer

**Current spec:** Shows engine methods with opts structs.

**Proposal:** Update method signatures to match actual positional-param style used in
`Generate`, `Review`, `CodeReview`, `Bugbot`. The opts struct pattern is already
implemented via `GenerateOpts`, `CodeReviewOpts`, `BugbotOpts` — update the spec to
show these actual type names.

**Your response:**

Approved. Makes sense this would evolve organically.

---

## Proposal 5: Document SSE streaming as not-yet-implemented

**Section:** 2 / 9 — Architecture / MCP Server Interface

**Current spec:** Claims SSE upgrade in HTTP transport.

**Proposal:** Mark SSE streaming as "planned" rather than "implemented". The HTTP
transport currently uses plain JSON-RPC only. SSE streaming for progress notifications
is a future enhancement.

**Your response:**

Approved. We can defer until later.

---

## Proposal 6: Document additional resources table columns

**Section:** 7 — SQLite Integration Layer

**Current spec:** Doesn't mention `kind`, `context_name`, `model` columns in resources table.

**Proposal:** Add these columns to the schema documentation. They exist, are functional,
and are needed by the system.

**Your response:**

Add them, makes sense this would evolve organically.

---

## Proposal 7: Update apply_actions CHECK constraint

**Section:** 7 — SQLite Integration Layer

**Current spec:** Claims apply_actions action column has CHECK constraint `create/modify/destroy`.

**Proposal:** Update to reflect that migration 006 removed this constraint. The action
column is now free-text. Document the current valid values used in practice.

**Your response:**

Approved.

---

## Proposal 8: Document `foreign_keys=ON` PRAGMA

**Section:** 7 — SQLite Integration Layer

**Proposal:** Add documentation that the database opens with `foreign_keys=ON` PRAGMA.
This is important for understanding cascade behavior.

**Your response:**

Approved.

---

## Proposal 9: Update field naming

**Section:** 9 — MCP Server Interface

**Current spec:** Uses `DispatchInstructions` field name in spec/context response.

**Proposal:** Update to `Instructions` to match actual implementation.

**Your response:**

Approved.

---

## Proposal 10: Document `--strict-mcp-config` flag

**Section:** 13 — Running Modes & Client Integration

**Proposal:** Document the `--strict-mcp-config` flag and env var filtering for
recursion prevention. These are implemented and functional but not in the spec.

**Your response:**

Approved.

---

## Proposal 11: Document undocumented features in CUE schema

**Section:** 1 — The CUE DSL

**Proposal:** Add documentation for:
- `framework` field in meta (functional but undocumented)
- `reviewLevel` values and validation behavior

**Your response:**

Approved.


---

## Proposal 12: Document dashboard running mode

**Section:** 13 — Running Modes & Client Integration

**Proposal:** Add dashboard mode to the running modes list. The dashboard is implemented
with API endpoints and serves as a monitoring interface.

**Your response:**

Approved.

---

## Proposal 13: Document run-phased-agent.sh script

**Section:** 13 — Running Modes & Client Integration

**Proposal:** Document `scripts/run-phased-agent.sh` as a multi-phase agent runner
that drives crest-spec through all 10 crest-synth phases with state carry-over.

**Your response:**

Approved.

---

## Proposal 14: Update Go version

**Section:** Overview

**Current spec:** Says Go 1.26.3.

**Proposal:** Check actual go.mod and update to match (likely 1.26.4 or similar).

**Your response:**

Approved.

---

## Proposal 15: Review prompt specificity

**Section:** 6 — Prompt Construction

**Current spec:** Claims review prompt includes hardcoded SOLID/DI checks.

**Proposal:** Update to reflect that the review prompt is generic. SOLID/DI checks
are part of the user's meta rules, not hardcoded in the review prompt template.
This is arguably better design — the spec should match the implementation.

**Your response:**

Approved.

I do want to make sure we are doing those checks though. I care about SOLID/DI/Clean code a lot.

Refer to the writing-clean-code and refactoring reviewer skills I have added to the "docs" folder in this repo to build those prompts.

---

## Proposal 16: Config isolation behavior

**Section:** 4 / 11

**Current spec:** Implies config isolation is always active.

**Proposal:** Document that config isolation only activates when an API key is set.
This is intentional — when no API key is configured, there's nothing to isolate.

**Your response:**

Approved.

---

*Generated 2026-06-07 during overnight drift fix session.*
