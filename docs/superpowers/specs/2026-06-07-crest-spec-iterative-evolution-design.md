# crest-spec Iterative Evolution & Self-Improvement — Design

**Status:** Draft for review
**Date:** 2026-06-07

## Goal

Make crest-spec **non-static**: the *generator itself* gets better at its craft over time. This is **high-level and cross-cutting** — it's about how crest-spec writes a given language and domain (e.g. "how we write Rust": idioms, recurring anti-patterns, what clippy keeps flagging, what reviews keep rejecting), applied broadly across *all* resources of that language/kind. Every run already records what was generated, what failed validation/review, what errors recurred, and what fixes worked. This pillar closes the loop — it reflects on that history, distills reusable **learnings** scoped by language and resource-kind, injects them into future generation prompts, and (with human approval) promotes the strongest, most stable ones into the durable prompt scaffolding. It also adds default whole-crate validations (clippy, fmt, build, test) so the loop has rich, real signal to learn from.

### What this is NOT

This is **not** per-resource correction. Fixing a specific resource against a specific directive is the separate **amendments** workflow (spec-metadata amendments that trigger targeted re-apply via drift). The two are complementary: amendment activity and `spec/deep_review` findings are *signal sources* this pillar can read, but the pillar's output is **general craft guidance** ("derive PartialEq manually for NaN-carrying f32 types"), not a patch to one file. Amendments improve *this project*; learnings improve *the generator*.

## Principles

- **Craft-level, not resource-level.** Learnings are scoped to `(language, resource-kind)` and apply across every matching resource — not to a single resource ID.
- **Prompt-based, not static.** Learnings are LLM-extracted guidance injected into prompts at generation time. No mechanical/templated code derivation. (`[[feedback-no-static-mess]]`)
- **Nothing self-mutates source.** Learnings live in SQLite and flow into prompts at runtime. Writing a learning into the durable prompt scaffolding is always **human-gated** — a tool emits a diff to approve.
- **Reflection never blocks generation.** Evolution runs alongside/after the run; a reflection failure or empty result never fails a wave or session.
- **Bounded.** Injected learnings are scoped and top-N capped to avoid prompt bloat; reflection is incremental to control cost.
- **Sonnet default, opus for hard reasoning.** No haiku. (`[[feedback-no-haiku]]`)

## The loop

```
generate code → SQLite records every attempt/failure/fix         [EXISTS]
       │
  reflect (LLM) reads failure+fix history for a scope,
       │        emits concise reusable learnings (marker JSON)    [NEW]
       ▼
  learnings table  (scope, text, rationale, confidence, status)   [NEW: write-back to SQLite]
       │
  runtime context injects matching active learnings for the
  resource's (language, kind) — top-N                             [NEW: inject → prompts]
       │
  promote (human-gated): strong, stable learnings → proposed
  diff to a language-scoped prompt template (markdown), which
  then bakes into the system prompt for every run                 [NEW: durable write-back]

  signal sources also feed reflect: clippy/fmt/build/test
  failures, review rejections, amendments + deep_review findings
```

---

## Component 1 — Prompts as embedded markdown (enabling refactor)

Today the system-prompt scaffolding (role, output format, folder structure, SOLID, output requirements) is built with Go string literals in `internal/prompt/system.go`. We move that scaffolding into markdown files embedded with `//go:embed`, so prompts are **data**, not code.

- **New:** `internal/prompt/templates/*.md` — e.g. `system_role.md`, `output_format.md`, `folder_structure_rust.md`, `folder_structure_default.md`, `solid.md`, `output_requirements_rust.md`.
- Templates support simple `{{lang}}` / `{{ext}}` substitution (Go `text/template` or trivial `strings.NewReplacer` — no logic in templates).
- `BuildSystemPrompt` becomes a renderer: load templates, substitute, then append the dynamic parts from `project.Meta` (Style, Rules, Avoid) exactly as today.
- `//go:embed templates/*.md` keeps the binary self-contained.

**Why now:** it makes the durable home for "the main prompts" an editable markdown file, and makes learning-promotion a clean, reviewable diff instead of a Go-source edit. Behavior is unchanged on day one — this is a pure refactor with golden tests asserting the rendered prompt matches the current output.

---

## Component 2 — Default project-level validations (whole-crate)

`cargo clippy` / `fmt` / `build` are whole-crate checks; running them per-resource is wrong (the crate is incomplete mid-wave) and slow. They belong at **wave verification**, which already runs project-wide commands after a wave (`VerifyWave` in `internal/spec/session.go`).

- **CUE schema:** add a project-level `validations` field (`internal/cue/types.go` Project; reuse the existing `Validation` type). Registered as project metadata (not a generatable resource).
- **base.cue defaults:**
  ```cue
  project: validations: [
    {kind: "compiles", command: ["cargo", "fmt", "--", "--check"], description: "rustfmt clean"},
    {kind: "compiles", command: ["cargo", "clippy", "--", "-D", "warnings"], description: "clippy clean"},
    {kind: "compiles", command: ["cargo", "build"], description: "crate builds"},
    {kind: "test",     command: ["cargo", "test"], description: "tests pass"},
  ]
  ```
- **Makefile asset:** extend `BuildMakefile` prompt to include `lint` (`cargo clippy -- -D warnings`) and `fmt` (`cargo fmt -- --check`) targets so the project is checkable by hand too.
- **Wiring:** `VerifyWave` runs `registry.Project.Validations` (via `RunValidations` in project root = `filepath.Dir(SpecDir)`) in addition to the existing `cfg.TypeCheckCommand`/`TestCommand`. Failures attach to `WaveVerifyResult.Errors` and feed the existing retry/resolve path — and become learning signal.

These run after each wave on the (now more complete) crate. Phase overrides use the existing `phase-N.override-<Asset>.cue` mechanism if a later phase needs different validations.

---

## Component 3 — Learnings store (write-back to SQLite)

New migration `migrations/012_learnings.sql` (current latest is `011_resource_phase.sql`) + `sql/queries/learnings.sql` (sqlc) + `internal/store` wrappers.

```sql
CREATE TABLE learnings (
  id                   TEXT PRIMARY KEY,
  scope_lang           TEXT NOT NULL DEFAULT '',  -- 'rust' or '' (any)
  scope_kind           TEXT NOT NULL DEFAULT '',  -- 'adapter'|'aggregate'|'asset'|... or '' (any)
  text                 TEXT NOT NULL,             -- the actionable guidance
  rationale            TEXT NOT NULL DEFAULT '',  -- why it matters
  source_generation_id TEXT,                      -- provenance (nullable)
  source_apply_id      TEXT,
  confidence           REAL NOT NULL DEFAULT 0.5, -- 0..1
  status               TEXT NOT NULL DEFAULT 'active', -- active|retired|promoted
  times_applied        INTEGER NOT NULL DEFAULT 0,
  created_at           TEXT NOT NULL,
  updated_at           TEXT NOT NULL
);
CREATE INDEX idx_learnings_scope ON learnings(scope_lang, scope_kind, status);
```

Store API (wrappers over sqlc): `CreateLearning`, `ListActiveLearnings(lang, kind, limit)`, `ListLearnings(status)`, `UpdateLearning`, `RetireLearning(id)`, `IncrementTimesApplied(ids)`.

---

## Component 4 — Learnings injection (into prompts)

Extend the existing clean injection point rather than touching `system.go`.

- `internal/prompt/context.go`: add `Learnings []string` to `RuntimeContext`; `InjectRuntimeContext` renders a `## Learnings From Past Runs` section (after dependencies, before user guidance).
- `internal/spec/runtime.go` `buildRuntimeContext`: query `ListActiveLearnings(project.lang, resource.kind, N)` — union of kind-specific and global (`scope_kind=''`) learnings, ordered by confidence desc, capped at N (default 10, configurable). Increment `times_applied` for the injected set (provenance for later confidence tuning).
- Bounding keeps prompt size controlled; if none match, the section is omitted.

---

## Component 5 — Reflection engine

New package `internal/evolve/`.

- **Input:** a scope (session, apply, or wave). It reads from the store: rejected/failed `generations` (with `rejection_reason`), failed `invariant_checks` (with `details`), wave-validation failures (clippy/fmt/build/test), `session_resources.last_error`, review rejections, and the eventual accepted output for the same resource (the "what finally worked" signal). **Amendment activity and `spec/deep_review` findings are additional signal sources** — the separate amendments workflow records targeted fixes that often generalize into craft learnings. It also loads existing `learnings` so the LLM dedupes.
- **Output is craft-level.** The extraction prompt instructs: produce guidance that generalizes across resources of a `(language, kind)` — not a fix for one resource. A finding like "this adapter dropped frames with try_send" becomes "for audio-output adapters, prefer blocking send over try_send". Per-resource-only observations are discarded (those belong to amendments).
- **Extraction:** prompts the engine (sonnet default; opus for large/complex histories) to produce learnings as **marker-delimited JSON** — reusing the hardened `===CREST_REVIEW_BEGIN===`-style sentinel + parser pattern from the constraint loop (deterministic parse, no keyword heuristics). Each learning: `{scope_kind, text, rationale, confidence}` plus a `supersedes` id list for dedupe/refinement.
- **Write:** new learnings inserted; superseded ones updated/retired. Reflection is **idempotent-ish** — re-running over the same scope refines rather than duplicates because existing learnings are passed in.
- **Safety:** all failures are swallowed (logged), never propagated to the run. Empty extraction is a no-op.

---

## Component 6 — Triggers

All three, per the decision:

1. **After every wave** — `RunWave` kicks off an **incremental, non-blocking** reflection over just that wave's failures (goroutine; result not awaited by the wave response). Bounded to the wave scope to control cost.
2. **On demand** — `spec/evolve` MCP tool: reflect over a given session/apply (or "all recent"), returns a summary of learnings added/updated/retired.
3. **Auto at finish** — `spec/finish` runs a fuller session-scoped reflection pass synchronously (it's the end of the run, so latency is acceptable) before finalizing.

Cost controls: per-wave reflection only looks at *new* failures since the last reflection; a config flag (`CREST_SPEC_EVOLVE=off|wave|finish|all`, default `all`) lets a run dial it down.

---

## Component 7 — Promotion (human-gated durable write-back)

Because this pillar improves *the generator's craft for a language*, promoted learnings belong in the **language-scoped prompt scaffolding** (markdown, per Component 1), not in a project's CUE spec.

- **Target:** a per-language learned template, e.g. `internal/prompt/templates/learned/rust.md`, embedded via `//go:embed` and appended to the system prompt for every Rust project. (Project-specific guidance, if ever needed, can still go to that project's `base.cue meta.rules` — but that's the amendments/spec path, not this pillar's default.)
- **Tool:** `spec/promote_learnings` selects active learnings above thresholds (default `confidence >= 0.8` and `times_applied >= 3`, both configurable on the call) and emits a **proposed diff** to the language template. It does **not** write the file.
- The human reviews and applies the diff (a follow-up `--apply` flag writes it after explicit confirmation), then rebuilds so the learning is baked in permanently. On apply, those learnings are marked `status='promoted'` so they aren't double-injected at runtime.

---

## Component 8 — Surfaces

- **MCP tools:** `spec/evolve`, `spec/learnings` (list/retire), `spec/promote_learnings`.
- **Dashboard:** a Learnings panel (list, scope, confidence, times_applied, status) reading the new table via the existing read-only query path.

---

## Data flow summary

```
RunWave ─► dispatch resources ─► VerifyWave (project validations: clippy/fmt/build/test)
   │                                   │
   │                                   └─► failures → WaveVerifyResult → retry/resolve
   └─► (async) evolve.ReflectWave ─► learnings table
spec/finish ─► evolve.ReflectSession (sync) ─► learnings table
Context()  ─► buildRuntimeContext ─► ListActiveLearnings(lang,kind) ─► RuntimeContext.Learnings ─► prompt
spec/promote_learnings ─► proposed diff to templates/learned/<lang>.md ─► (human applies + rebuild) ─► status=promoted
amendments / deep_review ─► (signal) ─► evolve.Reflect
```

## Error handling & safety

- Reflection/evolve errors are logged and swallowed — never block a wave/session.
- Injected learnings are scoped + top-N bounded; missing → section omitted.
- Dedupe via `supersedes` so the table doesn't bloat with near-duplicates.
- `times_applied` provides the hook for future confidence decay / auto-retire of learnings that correlate with continued failures.
- Durable prompt-template changes are human-gated only.

## Testing strategy

- **Component 1:** golden test — rendered system prompt from markdown templates byte-matches the pre-refactor output for the crest-synth project.
- **Component 2:** unit test that `project.validations` from base.cue is loaded and run by `VerifyWave` (fake command runner); CUE load test still green across all phases.
- **Component 3:** store CRUD tests (create, list-by-scope, retire) on a temp DB; migration applies cleanly.
- **Component 4:** `InjectRuntimeContext` renders the Learnings section; `buildRuntimeContext` selects by scope and respects the cap.
- **Component 5:** reflection parses marker-JSON, dedupes via `supersedes`, swallows engine errors (fake engine).
- **Component 6/7:** tool handlers tested with a fake spec/engine; promotion emits a correct diff and marks promoted.

## Implementation decomposition (build order)

1. **Prompts → embedded markdown** (Component 1) — foundation, behavior-preserving.
2. **Default project validations** (Component 2) — standalone, immediate value, generates learning signal.
3. **Learnings store + injection** (Components 3–4) — the data spine.
4. **Reflection engine + triggers** (Components 5–6).
5. **Promotion + surfaces** (Components 7–8).

Each stage is independently testable and shippable.

## Out of scope (v1)

- Autonomous (non-human-gated) prompt/spec mutation.
- The **amendments** workflow itself (separate design) — this pillar only *reads* its activity as signal.
- Cross-project learning sharing (the learnings DB is per-project in v1, though learnings are language-scoped within it).
- Automatic confidence decay / auto-retire (the `times_applied` hook is laid down for it, but the policy is deferred).
