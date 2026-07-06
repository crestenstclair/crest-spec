# Design: Behavioral Verification Pipeline (design → tasks → verify)

**Status:** Proposed
**Date:** 2026-06-25
**Scope:** crest-spec engine — adds two derivation stages and a verification stage to
the generation loop. Not CUE authoring; this changes the engine (sqlite schema,
MCP state tools, the spec-generate workflow).

---

## 1. The problem

The loop does not produce broken code. It produces **theater**, and then generates
its own validations that are *also* theater, in the same pass. Evidence from the
crest-synth run (130 files, ~6h, builds clean, all demos render WAVs):

- **1617 invariant checks, zero failures.** The judge has never once said no.
- The mixer spec asked for 6 channel strips with meters; the loop emitted a single
  text column and a `ui-smoke` check that only asserts "non-silent render" — green.
- `de-theater` commits in crest-synth document the pattern by hand:
  - `voice_demo` printed `steals=N` but never asserted `N>0`, so `steals=0` passed.
  - `sample_demo` had **zero** in-code asserts; `zones loaded=` printed regardless.

### Root cause

A **conflict of interest**: the same agent writes the code *and* writes the check
that "proves" it. An agent optimizing to pass satisfies the *letter* of the prompt
and authors a check that passes trivially. The entire assertion vocabulary today —
`exit_code`, `stdout_contains`, `stderr_empty`, `file_exists`, `file_not_empty`,
`file_matches` — checks **text the program chose to print** or static file
properties. The program is both actor and witness.

The human is currently the missing verifier: the `de-theater` commits *are* the
quality gate, applied by hand. That does not scale and is why the loop "churns for
hours" and still misses the design.

### Secondary problem: spec bloat

Every fix appended another imperative paragraph to an asset's `prompts:` array.
`editor.cue` reached 26KB. The spec has become a dumping ground of nudges rather
than a declarative description. Any new machinery must **not** compound this.

---

## 2. Design principles

1. **Separate the actor from the witness.** The thing that decides "acceptable"
   must not be the thing that wrote the code.
2. **Truth is anchored in behavior, not text.** A check's verdict must derive from
   a source the implementation cannot fake by choosing what it emits.
3. **A check that a no-op can pass is not a check.** Falsification is mandatory.
4. **The user describes behavior, never mechanism.** No files, no witness programs,
   no filenames in the surface the human touches. WHAT, not HOW — same split the
   rest of crest-spec already runs on.
5. **Derived artifacts never touch the `.cue`.** Contracts, tasks, and checks live
   in sqlite, regenerable from spec-hash. The spec stays terse.
6. **The spec converges toward structure.** Maturing a component *replaces* prose
   prompts with proven behavioral checks; it does not accumulate alongside them.
7. **Autonomous loop; human ratifies after.** No mid-loop human gate. Correction
   happens post-run via structured spec edits / amendments.

---

## 3. The pipeline

```
crest-design   per BOUNDED CONTEXT (taking its resources into account), the loop
   (sqlite)    derives an observable CONTRACT: the interface surface + the
               behaviors that constitute "correct". Keyed by spec-hash,
               regenerated each run. Never written to .cue.
                    ↓
crest-tasks    per RESOURCE, the loop derives TASKS. Each task carries a
   (sqlite)    BEHAVIORAL acceptance check, defaulting to FAIL.
                    ↓
generate       a sub-agent implements one task.
                    ↓
verify         (a) deterministic: run the behavioral check (see §4).
               (b) adversarial: a separate fail-closed agent asks
                   "is this behavior real, or theater?" — running the
                   pattern-4/5 taxonomy as its rubric.
                    ↓ fail → retry with the refutation injected as feedback
```

- **Design granularity = bounded context.** ~15 designs for crest-synth, coherent
  and cheap. Each design accounts for the resources within the context.
- **Task granularity = resource.** ~130 tasks for crest-synth, fine-grained enough
  to verify individually.

This preserves the settled architecture: the MCP server stays a **pure state
engine** (stores designs/tasks/verdicts, runs deterministic checks, never calls an
LLM). Claude Code orchestrates the design / tasks / generate / verify LLM stages
with sub-agents — the same shape as today's `context → commit`.

---

## 4. The check language (behavioral)

The only surface the user ever sees is a **behavioral claim + expected property:**

```
behavior: "two different key/velocity inputs resolve to different sample zones"
expect:   the outcomes differ

behavior: "the mixer presents 6 independent channel strips, each with a level
           meter that moves with playback"
expect:   6 distinct strips, meters vary independently

behavior: "past the polyphony limit, voice stealing occurs"
expect:   steals > 0
```

Underneath, four layers do the work — all invisible to the user:

- **A — Typed predicates (the vocabulary).** `differ`, `gt`, `gte`, `eq`, `range`,
  `count`, `distinct_count`, `monotonic`, `set_contains`. Numeric/structural, not
  substring.
- **B — Independent witness (where the assert lives).** The loop derives a separate
  harness that observes the behavior from **real return values / real signal**, not
  from printed strings. Generated by a *different* agent than the implementation.
  Owned by the resource, hidden from the user.
- **C — Falsification gate (must-fail-on-stub).** Before a check counts, the
  framework runs it against a degenerate no-op and **requires failure**. A check a
  stub passes is auto-rejected as theater. This mechanizes the manual de-theater
  audit and is the highest-leverage primitive.
- **D — Artifact observers (for audible/visual behavior).** When the behavior is
  "meters move" or "interpolation changes the sound", the loop measures the real
  output (spectral analysis, frame region detection) instead of trusting a printed
  string. Still surfaced to the user as *behavior*, never as a filename.

> A is the vocabulary, B is where the assert lives, C is the gate that proves the
> assert has teeth, D extends it to artifacts.

---

## 5. Anti-bloat: graduation

1. **During the loop:** nothing is written to `.cue`. All derived contracts, tasks,
   and checks live in sqlite, regenerable and discardable.
2. **After the loop:** the human reviews the result. When a behavioral check has
   *proven itself* (caught real theater, gates the wanted behavior), it
   **graduates** — from a derived sqlite artifact into a **structured behavioral
   check** attached to the component in the spec. A behavioral claim, not a prose
   paragraph and not a filename.
3. **Over time the spec converges toward a list of proven behaviors** the synth
   must keep exhibiting. Prose prompts get *replaced*, not accumulated.

Graduation is also the post-loop correction path: truth is not anchored up front;
it is ratified after, and ratification is structured.

---

## 6. Data model (sqlite)

New tables (regenerable; keyed by spec-hash so they cache across runs):

| Table | Grain | Purpose |
|-------|-------|---------|
| `designs` | bounded context | derived contract: interface surface + required behaviors |
| `tasks` | resource | derived task + its behavioral check, state, fail-closed default |
| `checks` | per task | behavioral claim, typed predicate, witness ref, falsification result |
| `verifications` | per generate attempt | deterministic result + adversarial verdict + refutation text |

Relationship to existing tables: `tasks` references `session_resources`;
`verifications` supersedes the role today played by the non-discriminating
`invariant_checks` ingestion (which must become fail-closed).

---

## 7. New MCP state tools (no LLM, mirror existing `context`/`commit`)

- `design_context` / `design_commit` — derive + store a context-level contract.
- `tasks_context` / `tasks_commit` — derive + store resource-level tasks + checks.
- `verify` — run the deterministic behavioral check + falsification gate; ingest the
  adversarial verdict; **fail-closed** (any failed check or failed falsification or
  adversarial refutation rejects the commit).
- `graduate` — promote a proven behavioral check from sqlite into the terse spec.

---

## 8. Cost & latency

The synth already churns ~6h. This roughly doubles LLM stages per component, so
cost control is designed in, not bolted on:

- **Cache contracts by spec-hash** — unchanged contexts skip re-derivation.
- **Verify only the diff** — re-verify a resource only when its task or deps changed.
- **Falsification is cheap** for "stub returns default"; reserve targeted mutation
  for behaviors where a trivial stub isn't degenerate enough.
- **Adversarial review** runs on the generated diff, not the whole tree.

---

## 9. Open questions for implementation

1. **Falsification mechanics** — how the framework synthesizes the no-op/degenerate
   case per behavior class (return-default stub vs. targeted mutation).
2. **Witness ownership** — how generated witness harnesses are tracked per resource
   without per-file scope validations (which the project explicitly disallows;
   tooling runs whole-tree).
3. **Adversarial rubric** — encode the pattern-4 (validation theater) / pattern-5
   (unproven claims) taxonomy as the reviewer's explicit checklist.
4. **Graduation format** — the structured behavioral-check schema in CUE that
   replaces prose prompts.

---

## 10. What's locked

- Pipeline: design (context) → tasks (resource) → generate → verify.
- Check language: behavioral claims + typed predicates (A), hidden independent
  witnesses (B), mandatory must-fail-on-stub (C), real-output observers for
  audio/UI (D). User sees behavior only.
- Anti-bloat: derived artifacts in sqlite; only proven behaviors graduate into the
  terse spec.
- Autonomous loop; human ratifies after via structured spec edits / amendments.
- Architecture unchanged: MCP server is a pure state engine; Claude Code
  orchestrates with sub-agents.
