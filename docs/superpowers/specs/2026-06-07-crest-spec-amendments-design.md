# Implementation Brief: Spec Amendments for crest-spec

> **Status:** design handoff. Self-contained — written so an implementer without the
> originating conversation can build it.
> **Date:** 2026-06-07
> **Related:** `2026-06-07-crest-spec-iterative-evolution-design.md` (the evolution
> pillar). See §0.1 for the relationship — they are **separate** mechanisms.

## 0. Orientation (read first)

**crest-spec** is a declarative, agent-driven code generator. The system of record is a
set of **CUE spec files** (`phases/base.cue` + `phases/phase-N.cue` +
`phases/*.override-*.cue`). From the spec, the engine derives **resources** (value
objects, ports, aggregates, domain services, assets), groups them into dependency-ordered
**waves**, and for each resource produces a scoped prompt via `spec_context` that a
sub-agent turns into code. Generation is gated by **validations**
(`{kind, command, assertions}` — e.g. `compiles`, `integration`, and project-wide
`cargo clippy -D warnings`/`cargo test`). Session/run state lives in **SQLite**. The
pipeline is `plan → begin → (drift?) → run_wave/dispatch → validate → commit → finish`,
with a post-hoc `deep_review` that emits SOLID/clean-code findings.

Your job: add a first-class **`amendment`** concept that turns review findings (and other
targeted change requests) into durable, spec-resident, incrementally-applied
modifications — without forking the generation model into an unreliable patch mode.

Before writing code, locate and read: the CUE schema definition for `project:` (where
`invariants`, `prompts`, `validations` are defined), `internal/spec/loop.go`,
`internal/spec/dispatch.go`, the SQLite schema/migrations, `internal/prompt/system.go`
(prompt assembly), and the `spec_context`/`spec_amend`/`spec_drift`/`spec_deep_review`
tool handlers.

### 0.1 Relationship to the evolution pillar (do not conflate)

The **evolution pillar** (separate design doc) is *high-level craft improvement of the
generator itself* — "how we write Rust" idioms and recurring anti-patterns, scoped by
`(language, resource-kind)` and applied across **all** matching resources, promoted into
a language-scoped prompt template (`templates/learned/<lang>.md`).

**Amendments are NOT that.** Amendments are *per-resource corrections* to a specific
resource's spec. They may **feed signal** into the evolution pillar's reflection (a
finding that recurs across many resources is a craft-level learning, not a one-off
amendment), but the two are distinct mechanisms with distinct storage and lifecycles.
Keep this PR scoped to amendments.

## 1. Problem & justification

Today, review findings are **advisory and ephemeral**. `deep_review` produces
high-quality findings (e.g. "EqualTemperament skips validate-at-construction"), but there
is no durable channel to feed them back. The two naive options both fail:

- **Manual prose prompt tweaks** → not tracked, not reproducible, lost on regen, don't
  compound across runs.
- **Patching generated artifacts directly** → creates two sources of truth (spec says one
  thing, patches say another); a clean regen reproduces the bug forever; deltas to the
  same file don't compose; "patched" never means "correct."

The fix must satisfy the project's existing **evolution-pillar** principles: nothing
self-mutates source (**human-gated** write-back), prompts/knowledge are **data not
hardcode**, and improvements **compound** over time rather than being re-applied forever.

**Amendments** solve this by making a targeted change a *spec-resident, provenance-tagged,
lifecycle-tracked* entry that flows through the **normal** generation loop.

## 2. Worked use case

`deep_review` reports (real finding from a live run):

> `src/audio/equal_temperament.rs:17` (major): `EqualTemperament::new` accepts invalid
> reference pitches (0.0, negative, NaN, ∞) with no validation, breaking the
> validate-at-construction pattern that `NoteNumber`/`Velocity` follow.

Desired flow:
1. The finding is drafted into an **amendment** (name + targeted prompt + provenance),
   attached to the `EqualTemperament` resource in the spec.
2. A human approves the amendment (it appears as a CUE diff).
3. Adding it changes that resource's spec hash → the resource shows as **drifted/pending**
   on the next `plan`/`begin`.
4. `apply` re-applies **only that resource**, in **update mode**: the agent gets the
   *existing* `equal_temperament.rs` + the amendment text flagged as "the change to make,"
   and produces a minimal diff.
5. The same validation gate runs (compiles, clippy, tests, plus optionally an
   amendment-specific assertion like "`try_new(0.0)` returns `Err`").
6. On commit, the resource's generated-spec-hash now includes the amendment → it reads as
   **applied**.
7. Later, once stable, the amendment **graduates**: its intent is folded into the
   resource's canonical `invariants`/`prompts`, and the amendment retires — so the spec
   describes the system cleanly rather than accumulating a patch log.

## 3. Design constraints (MUST honor — settled decisions, not open questions)

1. **Amendments live in the spec (CUE), as resource-scoped metadata** — they are the
   source of truth and are written back to the spec. They are **not** artifact-level
   patches.
2. **`applied` is DERIVED from the resource's spec hash, not an independently-mutated
   flag.** An amendment is "applied" iff the current committed output was generated from a
   spec snapshot that included it. SQLite may **materialize/cache** this for querying and
   dashboards, but must reconcile it from drift — never maintain a second, divergent
   notion of "current."
3. **Re-apply at RESOURCE granularity through the full validation→commit loop.** Do **not**
   implement partial in-file edits as a separate generation mode. (Rejected because:
   thinner context → regressions; deltas to one file don't compose; bypasses the
   validation gate.)
4. **Re-apply is context-aware, not blank-slate.** The generation agent receives the
   existing committed output **plus the spec delta (the amendment text), explicitly flagged
   as what changed**. It makes the minimal change preserving everything else. This is a
   correctness/reviewability win (small diffs, contained blast radius), not just a cost
   saving.
5. **Implement the diff-aware behavior generically as an UPDATE mode in `spec_context`**,
   usable by *any* already-generated resource that drifts — amendments are its first
   consumer, ordinary spec drift is another. Do not build an amendment-only codepath.
6. **Keep a blank-slate `force` regen path** as the reproducibility escape hatch and the
   **graduation check**: if a clean regen passes without the amendment doing work, the
   intent has been absorbed into the canonical spec and the amendment can retire.
7. **Write-back is human-gated** (consistent with the evolution pillar): amendment creation
   and graduation emit a CUE **diff to approve**; nothing silently mutates source.
8. **`applied` ≠ `fixed`.** Where an amendment's intent is mechanically checkable, it SHOULD
   carry/spawn a `validation` assertion so resolution is verified, not assumed.

## 4. Data model

### 4.1 CUE schema (source of truth)

Add an `amendments` list to the resource schema (wherever `invariants`/`prompts`/
`validations` are defined). Example shape:

```cue
#Amendment: {
    name:      string                    // stable id within the resource, kebab-case
    prompt:    string                    // the targeted change instruction (data, LLM-draftable)
    origin:    "deep_review" | "manual" | "bugbot" | string
    finding?: {                          // provenance, optional
        severity: "major" | "minor" | string
        file?:    string
        line?:    int
        text?:    string
    }
    validation?: #Validation             // optional check that proves the intent (applied != fixed)
    graduated:  *false | bool            // true once folded into canonical invariants/prompts
    createdAt:  string
}

// attached per resource, e.g.:
project: contexts: Audio: <...>: EqualTemperament: amendments: [ #Amendment, ... ]
```

> Note: resources are declared in different places (value objects in `base.cue`,
> aggregates/services possibly elsewhere or synthesized). Locate the canonical resource
> declaration site; amendments attach at the same level as that resource's `invariants`.

### 4.2 SQLite (materialized state / index)

Add an `amendments` table as a **cache** over the CUE truth, not a parallel source:

```
amendments(
  id, session_id, resource_id, name,
  content_hash,           -- hash of {name, prompt, finding} for identity
  origin, finding_json, validation_json,
  state,                  -- derived: PENDING | APPLIED | VERIFIED | GRADUATED | FAILED
  applied_spec_hash,      -- the resource spec hash that incorporated it (null if pending)
  created_at, applied_at, graduated_at
)
```

`state` is **computed** during plan/drift reconciliation by comparing each amendment's
presence in the current spec against the resource's last-generated spec hash. The table is
rewritten from that computation; it is never the authority.

## 5. Lifecycle / state machine

```
(none) --propose--> PROPOSED --approve(write CUE diff)--> PENDING
PENDING --re-apply + commit--> APPLIED
APPLIED --validation/assertion passes--> VERIFIED
APPLIED/VERIFIED --validation fails / finding persists--> FAILED  (re-draft, loop)
VERIFIED --human-gated graduate(fold into invariants/prompts, remove amendment)--> GRADUATED
```

- **PROPOSED → PENDING**: human approves the CUE diff that inserts the amendment.
- **PENDING → APPLIED**: derived — resource committed from a spec hash containing the
  amendment.
- **APPLIED → VERIFIED**: the amendment's `validation` (or a re-run `deep_review` no longer
  reporting the finding) confirms intent.
- **VERIFIED → GRADUATED**: human-gated; emits a CUE diff that rewrites the canonical
  `invariants`/`prompts` and deletes the amendment, then a `force` clean regen must still
  pass.

## 6. How it fits the constraint loop

| Stage | Amendment behavior |
|---|---|
| `spec_plan` / `spec_begin` | Reconcile amendments: any whose content isn't reflected in the resource's current generated-spec-hash surface as an **update action** (reuse/extend the existing `DriftActions` mechanism). |
| `spec_context` | If the resource has committed output **and** pending amendments (or drift), emit **UPDATE mode**: existing files + system prompt + current amended spec + a highlighted "CHANGES TO MAKE" block containing the amendment prompt(s) and/or spec diff. Instruct minimal-diff modification. |
| `spec_run_wave` / `spec_dispatch` | Re-apply the affected resource(s) only, in update mode. Same concurrency/retry behavior. |
| Validations | Unchanged gate. If the amendment declares a `validation`, include it for that resource. Retries on failure as today. |
| `spec_commit` | On success, persist the new generated-spec-hash → amendments become APPLIED. |
| `spec_deep_review` | Re-running it is the verification signal: finding gone ⇒ VERIFIED; finding persists ⇒ FAILED (re-draft). |
| `spec_finish` | Optionally report unapplied/unverified/ungraduated amendments as outstanding work. |

## 7. Tool surface (MCP)

Prefer extending existing tools over adding parallel ones; reconcile with the current
`spec_amend` (which today "fixes CUE + re-dispatches"):

- **`spec/propose_amendments`** (or fold into `deep_review`): given findings, an LLM
  **drafts** `{name, prompt, finding}` per actionable finding. Output is a proposal, not yet
  written.
- **`spec/apply_amendments`** (human-gated write-back): writes approved amendments into the
  CUE spec, returning the **diff** for approval. After approval, normal drift/apply picks
  them up.
- **`spec/list_amendments`** (`session_id`, optional `state` filter): query the materialized
  table (dashboard + "show unapplied").
- **`spec/graduate_amendment`** (`resource_id`, `name`): human-gated; emits the CUE diff
  folding the amendment into canonical `invariants`/`prompts` and removing it; then requires
  a clean `force` regen to pass.
- Ensure `spec/dispatch` and `spec/run_wave` route through the new `spec_context`
  **update mode** when the resource already has output.

## 8. High-level implementation steps

1. **CUE schema**: add `#Amendment` and the per-resource `amendments` list; ensure it
   threads into the resource model the engine reads.
2. **Spec write-back**: implement programmatic, **human-gated** insertion of amendments into
   the correct CUE file (likely a phase-override file, matching the existing override
   convention with a WHY comment) and a diff renderer for approval. Re-use however overrides
   are already authored.
3. **Hashing/drift**: include `amendments` in the resource's spec-hash computation so adding
   one naturally produces drift. Verify the existing drift detector already treats spec-hash
   change as "needs update."
4. **`spec_context` UPDATE mode**: detect existing committed output for a resource; assemble
   the update prompt (existing files + system prompt + amended spec + flagged changes). Add
   a create-vs-update branch; make it generic for drift too.
5. **SQLite**: add the `amendments` table + reconciliation routine run during plan/begin that
   recomputes `state` from CUE-vs-generated-hash.
6. **Validation wiring**: when an amendment declares a `validation`, include it in that
   resource's validation set during re-apply.
7. **Tools**: implement/extend the MCP handlers in §7.
8. **Graduation**: implement the fold-into-canonical diff + post-graduation `force`
   clean-regen verification.
9. **Prompts-as-data**: keep all new prompt scaffolding (the "CHANGES TO MAKE" framing, the
   proposer's drafting prompt) in markdown/embedded templates, not hardcoded Go strings —
   consistent with the evolution pillar.

## 9. Edge cases & failure modes to handle

- **Multiple amendments on one resource**: apply them together in a single update (the agent
  sees all of them → they compose, like multiple invariants), not sequentially.
- **Amendment + genuine spec drift on the same resource**: both are just spec-hash changes;
  one update pass handles both. Define precedence only if instructions conflict (surface,
  don't silently merge).
- **Validation never passes after N retries**: mark FAILED, keep the amendment PENDING,
  surface for re-draft — do **not** commit a partial fix.
- **`applied` vs `fixed` divergence**: re-run `deep_review` (or the amendment's validation)
  to detect amendments that "applied" but didn't resolve the finding.
- **Graduation regression**: if the post-graduation `force` regen fails the amendment's
  check, graduation is rejected and the amendment stays VERIFIED.
- **Idempotency**: re-running apply with no new amendments must be a no-op (derived state
  must be stable).

## 10. Non-goals

- No in-file partial patching as a generation mode (explicitly rejected — see §3.3).
- No self-mutating spec (all write-backs are human-gated diffs).
- No cross-project / craft-level learning store here (that's the **evolution pillar** —
  see §0.1; keep this PR scoped to amendments).

## 11. Acceptance criteria (verify with a REAL end-to-end run — not simulated)

1. Run `deep_review` on the existing `crest-synth` workspace; propose an amendment for the
   real `EqualTemperament` finding.
2. Approve it; confirm it's written into the CUE spec as a reviewable diff.
3. Run `plan`/`begin`; confirm **only** `EqualTemperament`'s resource shows as needing
   update (drift via spec hash), nothing else.
4. Run apply; confirm the sub-agent received the existing file + the flagged change (inspect
   the prompt), and that the resulting diff is **minimal** (doesn't rewrite unrelated code).
5. Confirm `make build` + `cargo test` + clippy still pass, and the amendment's assertion
   (`EqualTemperament::try_new(0.0)` ⇒ `Err`) passes.
6. Re-run `deep_review`; confirm the finding is gone ⇒ amendment VERIFIED.
7. Graduate it; confirm the invariant now appears in canonical spec, the amendment is
   removed, and a `force` clean regen still passes.
