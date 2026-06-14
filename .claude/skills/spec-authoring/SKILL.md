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
- **No phases. No overrides.** Those were a test-harness artifact for simulating
  incremental builds; they do not belong in a real spec. Each asset has exactly
  one declaration.
- Every file starts with the same package clause (e.g. `package crestsynth`).
- Co-locate an **adapter** with the context whose port it implements, and a
  **demo/binary asset** with the domain it exercises.

## The CUE shape

Everything hangs off `project:`. Read the existing `spec/*.cue` as living
examples; `references/cue-schema.md` is the full field reference. The essentials:

```cue
package crestsynth

// project.cue — config + the gate
project: name: "crest-synth"
project: layers: ["domain", "application", "infrastructure"]
project: layerRules: application: {dependsOn: ["domain"]}
project: meta: {language: "rust", style: "…", avoid: ["heap alloc on audio thread"]}

// Whole-tree validations = THE GATE. Run at wave verification across the
// entire crate. This is where formatters/linters/build/test live.
project: validations: [
	{kind: "compiles", command: ["cargo", "fmt", "--", "--check"], description: "rustfmt clean"},
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
(rules the generator must honor; the loop judges them).

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
- Make success **mechanically checkable** — have the binary print a verbatim
  token (e.g. `steals=37`, `render non-silent: true`) and assert on it in a
  `validations` entry. A behavior with no assertion is a behavior the loop can't
  prove.
- Encode hard-won rules in the prompt text (e.g. threading, units) rather than
  hoping the model infers them.
- Use CUE raw strings `#"…"#` when the prompt contains quotes/backticks.

## The validation gate (the north star)

The core loop is **generate → commit → validate → retry-with-feedback**. Two
layers of validation:
- **Whole-tree** (`project: validations`) — fmt, clippy `--all-targets`, build,
  test across the *entire crate*. This is the primary gate. It runs at wave
  verification. Keep it strict.
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

## Anti-patterns (stop if you catch yourself)

- Hand-writing `.rs` (or any target code). → Put intent in an asset `prompt`.
- Re-introducing `phase-N.cue` / `override-*.cue`. → Group by domain instead.
- Per-file resource→file mapping or scoping validations to one file. → The gate
  is whole-tree; leave it that way.
- A new behavior with no assertion. → Add a printed token + a `validations` entry.
- Narrating a prompt in chat as if you implemented it. → "I added an asset whose
  prompt asks for X," and link the `.cue`.
- Static/mechanical generation of content the LLM could produce from a prompt. →
  Prefer the prompt.
