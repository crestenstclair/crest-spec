---
name: spec-authoring
description: Use when authoring or modifying a crest-spec CUE spec — adding/changing a bounded context, value object, aggregate, port, adapter, or asset, or designing a new feature for a crest-spec project (e.g. crest-synth). Establishes the vision and methodology for writing the .cue files. NOT for running generation (use spec-generate for that).
---

# Authoring crest-spec CUE specs

## The one rule that governs everything

**You author the SPEC. The generate→validate→retry loop writes the code.**
You never hand-write the target language (Rust, etc.). When asked to "add a
feature" you express it as CUE — a context, value object, port, or asset whose
`prompts` describe *what* to build — and the loop generates and verifies the
implementation. If you find yourself writing a `.rs` file, stop: that belongs in
an asset's `prompts`, not in your editor.

Corollary: when describing your work in chat, say "I added a CUE asset whose
prompt asks the generator to build X" and point at the `.cue` file. Do NOT narrate
the prompt in heavy implementation detail — it reads like you wrote the code.

## How the spec is loaded (mental model)

crest-spec loads the spec **directory** as one CUE package: every `.cue` file is
unified together (`load.Instances`, `Dir: specDir`). So:

- **Group by domain — one file per bounded context.** `kernel.cue`, `synth.cue`,
  `effects.cue`, … plus `project.cue` (config) and `manifest.cue` (Cargo/Makefile).
- Every file starts with the same package clause (e.g. `package crestsynth`).
- Co-locate an **adapter** with the context whose port it implements, and a
  **demo/binary asset** with the domain it exercises.

**Phases are hydrated snapshot directories.** The iterative design history
lives in `phases/phase-N/` subdirectories: each holds the original phased
spec RESOLVED for that stage — `base.cue`, the cumulative `phase-1..N.cue`
files, and the single winning `override-<Asset>.cue` per asset (no
override-number resolution left to do in your head). Every directory is
standalone and copy-paste-able: point `CREST_SPEC_SPEC_DIR` at any phase to
build that stage; move it to the next and the planner regenerates the delta.
The whole-picture `spec/` directory is the current domain-grouped design.
When authoring NEW work, work in `spec/` (or the current project's domain
files) — a new phase snapshot is a directory holding the complete spec of
that stage, never a loose overlay file dropped next to other phases.

## The CUE shape

Everything hangs off `project:`. Read the existing `spec/*.cue` as living
examples; `references/cue-schema.md` is the full field reference. The essentials:

```cue
package crestsynth

// project.cue — mission + config + the gate
project: name: "crest-synth"

// The mission is REQUIRED context: it is injected into every generator's
// system prompt so leaf-level decisions are made against the whole. State
// what is being built, for whom, and the architectural intent — 2-4 sentences.
project: mission: "A standalone, gamepad-friendly MIDI synthesizer for the Steam Deck. Hexagonal architecture around a hard real-time audio thread that must never allocate, lock, or block."

project: layers: ["domain", "application", "infrastructure"]
project: layerRules: application: {dependsOn: ["domain"]}
project: meta: {language: "rust", style: "…", avoid: ["heap alloc on audio thread"]}

// Whole-tree validations = THE GATE. Run at wave verification across the
// entire crate. This is where formatters/linters/build/test live.
project: validations: [
	{kind: "custom", command: ["cargo", "fmt"], description: "normalize formatting (auto-fix, never blocks)"},
	{kind: "compiles", command: ["cargo", "clippy", "--all-targets", "--", "-D", "warnings"], description: "clippy clean"},
	{kind: "compiles", command: ["cargo", "build"], description: "crate builds"},
	{kind: "test", command: ["cargo", "test"], description: "tests pass"},
]
```

```cue
// kernel.cue — a bounded context
project: contexts: Kernel: purpose: "shared value types for MIDI + audio primitives"
project: contexts: Kernel: ubiquitousLanguage: {NoteId: "unique id for a sounding note"}
project: contexts: Kernel: valueObjects: Velocity: {from: "f64", description: "0.0-1.0", invariants: ["must be 0.0-1.0"]}
project: contexts: Kernel: valueObjects: AudioFrame: {state: {left: "f32", right: "f32"}, description: "stereo sample pair"}
```

Modelling vocabulary (DDD): `valueObjects` (immutable data; `from:` for newtypes,
`state:` for structs), `aggregates` (`root: true`, with `commands`/`events`/
`invariants`), `entities`, `ports` (hexagonal interfaces with a `contract:`),
`domainServices`, `repositories`, `applicationServices`, and `invariants`
(constraints injected into the generator's prompt; verification is INDEPENDENT
— behavioral checks and mechanical validations, never the generator's own word).

**Keep invariants behavioral, keep the language out of the domain.**
An invariant states an observable rule ("must be 0.0-1.0", "voice is
reclaimable only when its envelope reaches Idle") — never an implementation
("use Arc", "apply 2^(semitones/12)"), never a dependency pin. Crate and
toolchain facts live in exactly two places: `manifest.cue` (the Cargo.toml
asset's dependency list) and an adapter's `meta: framework:` field. A crate
name appearing anywhere else in the spec is leakage.

**Structural vs guidance edits (hash split).** The planner cascades
regeneration through dependents only for STRUCTURAL changes (types, `state`,
`contract`, `commands`/`events`, `invariants`, dependencies). Guidance edits —
`prompts`, `description`, `notes`, `rationale`, `purpose`, `style`, `avoid`,
`examples`, `references` — regenerate only the edited resource. Tune prose
freely; it no longer churns the tree.

## Assets — where generation intent lives

An asset is a concrete file to generate. Its `prompts` array is the brief; its
`validations` prove it works:

```cue
project: assets: VoiceDemoMain: {
	kind:        "rust-bin-target"
	description: "src/bin/voice_demo.rs: over-polyphonic passage that forces voice stealing"
	uses: ["aggregate.Synth.Voice", "domainService.Synth.VoiceAllocator"]
	prompts: [
		"File path: src/bin/voice_demo.rs",
		"Build a VoiceAllocator with a small polyphony limit and feed it MORE notes…",
		#"Print EXACTLY a line containing `steals=` followed by the integer count…"#,
	]
	validations: [
		{kind: "integration", command: ["make", "demo-voices"], description: "renders + forces stealing", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "stdout_contains", pattern: "steals="},
		]},
	]
}
```

Writing good `prompts`:
- Lead with the file path, then behavior, then constraints.
- Make success **mechanically checkable on a MEASURED value** — the binary must
  compute the real quantity, print it (`steals=37`, `audio peak: 0.52`,
  `strips visible: 6`), assert IN CODE against it (exit non-zero on mismatch),
  and a `validations` entry asserts on that printed measurement. Asserting
  `stdout_contains` on a token the binary prints *unconditionally* (`ok`,
  `: true`, a bare completion marker) is **validation theater**: it proves the
  line printed, not that the behavior happened. The test is — a real run that
  renders one channel or plays silence MUST fail. A behavior with no assertion
  is a behavior the loop can't prove.
- Encode hard-won rules in the prompt text (e.g. threading, units) rather than
  hoping the model infers them.
- Use CUE raw strings `#"…"#` when the prompt contains quotes/backticks.

## Where does intent belong? (`notes` is the last resort)

Every rule has a structural home; `meta: notes` / `prompts` prose is the *small
remainder*, not the default. Before writing prose, ask:

- **Global / cross-cutting rule?** → ONE project-level `meta`/`invariants` entry,
  or rely on the whole-tree gate. Never paste the same rule into N resources'
  notes. If the gate already enforces it (clippy, fmt, build), do NOT restate it
  per-resource at all.
- **A checkable property?** → an `invariant` (the loop JUDGES it) or a
  `validation` (the loop PROVES it) — not a sentence in notes.
- **Domain logic?** → push it into `contract` / `commands` / `events` / `state`
  + `invariants`. Do NOT spell a reducer/algorithm out as English pseudo-code;
  that duplicates the structure and rots.
- **Already encoded by a type/enum/contract?** → reference the type; don't
  re-prose its variants or fields.
- **An irreducible environmental fact?** (threading constraint, version pin,
  units, a hardware gotcha) → THIS is what `notes` is for. Keep it short.

The smell: if a bug becomes "another sentence in a note" instead of a new
invariant/validation/type, the spec is growing prose, not getting better.

## The validation gate (the north star)

The core loop is **generate → commit → validate → retry-with-feedback**. Two
layers of validation:
- **Whole-tree** (`project: validations`) — clippy `--all-targets`, build, and
  test across the *entire crate*, plus `cargo fmt` as auto-normalization (it
  fixes formatting rather than failing on it — the gate polices design, not
  whitespace). This is the primary gate. It runs at wave verification. Keep
  the design checks strict.
- **Per-asset** (`validations:` on an asset) — `make` targets with `assertions`
  that prove that asset's behavior.

Do NOT track per-file ownership or try to scope validation to "this resource's
files." The gate is the whole tree — that's what lets formatters and lints run
freely. Strengthen the spec by making validations catch more, not by narrowing
scope.

## Authoring workflow

1. Identify the bounded context your change belongs to → open that domain `.cue`
   (or add a new `<context>.cue`). Project-wide deps/targets go in `manifest.cue`;
   config/validations in `project.cue`.
2. Express the change as CUE: add/modify the value object / aggregate / port /
   adapter / asset. Add or tighten `invariants` and `validations` so the loop can
   prove it.
3. Run generation with the **spec-generate** skill. The planner diffs your spec
   against the settled baseline (effective hashes) and regenerates ONLY what your
   edit touched; the loop validates the whole tree and commits.
4. Commit the spec change + regenerated code together.

**Code-neutral edits adopt without regenerating.** If an edit only cleans prose
(deletes notes that duplicate invariants, references a type instead of re-prosing
it) the generated files don't change — so re-baseline instead of regenerating:
`spec_begin`, then `spec_commit` each changed resource with NO `files` (the server
re-validates the on-disk code against the new spec and re-settles the hash). A
strengthened `validation` that the existing code already passes also adopts this
way; one it FAILS is the validation doing its job — that resource genuinely needs
a regen (don't adopt a validation you never ran green).

## Anti-patterns (stop if you catch yourself)

- Hand-writing `.rs` (or any target code). → Put intent in an asset `prompt`.
- Re-introducing `phase-N.cue` / `override-*.cue`. → Group by domain instead.
- Per-file resource→file mapping or scoping validations to one file. → The gate
  is whole-tree; leave it that way.
- A new behavior with no assertion. → Add a `validations` entry that asserts a
  measured value (not just that the binary printed a token).
- A `validation` that asserts only a self-printed token (`stdout_contains: "ok"`
  / `": true"` / a bare completion marker). → **Validation theater.** Assert a
  MEASURED quantity the code computes and make the run exit non-zero when the
  behavior is actually broken.
- Pasting a cross-cutting rule into many resources' notes, or restating a
  gate-enforced rule (clippy, fmt, build) per-resource. → ONE project-level home,
  or lean on the gate.
- Spelling a reducer/algorithm out as English pseudo-code in `meta: notes`. → It
  belongs in `commands`/`events`/`state` + `invariants`.
- Re-prosing what a type/enum/contract already defines. → Reference the type.
- Crate names, Rust API paths, version pins, or compiler-error lore in a domain
  file. → Crates belong ONLY in `manifest.cue` and adapter `meta: framework:`;
  environmental gotchas go in ONE short project-level note or a learning.
- Writing a prompt that narrates the implementation line-by-line (a debugging
  journal). → State the observable behavior + the assertion that proves it;
  if a past bug taught a rule, make the rule an invariant or validation.
- Narrating a prompt in chat as if you implemented it. → "I added an asset whose
  prompt asks for X," and link the `.cue`.
- Static/mechanical generation of content the LLM could produce from a prompt. →
  Prefer the prompt.
