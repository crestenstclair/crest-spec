<!-- Promoted craft learnings are appended below this line. Do not edit by hand; use spec/promote_learnings. -->

- Do not invent newtype constructors (e.g. `Velocity(0.8)`) in binaries or tests unless the type is actually in scope at that location; check the module's imports and re-exports first, and construct values through the type's published API.
  - Distilled from a crest-synth phase-1 run: a generated `src/main.rs` failed compilation by calling a kernel newtype's tuple constructor that was not imported; the retry succeeded after switching to the public constructor.

- In `///` doc examples (doctests, compiled by `cargo test`), never declare a binding the example doesn't use — an unused `let x: Vec<_> = vec![]` fails to compile with E0282 (type annotations needed) and breaks the test suite. Either use every binding or omit it; pass empty literals like `&[]` directly to the call so element types infer from the signature.
  - Distilled from a crest-synth phase-4 soak run: PatchMixer's doc example declared `let patches: Vec<_> = vec![];`, never used it, and the doctest failed E0282 — which kept the wave's `cargo test` validation red.

- Use `RangeInclusive::contains` for bounds checks, not manual comparisons: write `!(lo..=hi).contains(&value)` instead of `value < lo || value > hi` (clippy::manual_range_contains denies the manual form under -D warnings). Combine with NaN checks as `value.is_nan() || !(0.0..=1.0).contains(&value)`.
  - Distilled from a crest-synth phase-1 soak: Velocity's bounds check churned 23 generation attempts against clippy::manual_range_contains.
- Never call `.ok()` on a `Result` just to pattern-match the `Some` — use the `Result` directly: `if let Ok(x) = Type::try_new(...)`, not `if let Some(x) = Type::try_new(...).ok()` (clippy::match_result_ok denies the redundant `.ok()` under -D warnings).
  - Distilled from the same run: MidiFileLoader churned on clippy::match_result_ok at three call sites.

- To call a trait's methods you must have the trait in scope: `use path::to::TheTrait;`. Calling e.g. `engine.note_on(...)` on a concrete type whose `note_on` comes from a trait fails with E0599 ("no method named … found … trait … is implemented but not in scope") unless the trait is imported. Import every trait whose methods you call, not just the concrete type.
  - Distilled from a crest-synth phase-11 build: synth_ui called SynthEngine trait methods (note_on/note_off/render_block) on SineSynthEngine without `use ...synth_engine::SynthEngine;`.
- A `cpal::Stream` is NOT `Send`/`Sync` on macOS (CoreAudio) — never move a cpal Stream (or any struct that owns one) into `thread::spawn` (fails with "*mut () cannot be sent between threads safely"). Create and keep the stream on one thread (e.g. the main/UI thread) and feed its ring buffer from that same thread; cross thread boundaries only with Send data (e.g. MIDI events over an mpsc/rtrb channel), never the stream itself.
  - Distilled from the same run: synth_ui tried to spawn an audio thread owning CpalAudioOutput (which holds the cpal Stream), failing E0277.
