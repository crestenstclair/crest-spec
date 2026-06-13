<!-- Promoted craft learnings are appended below this line. Do not edit by hand; use spec/promote_learnings. -->

- Do not invent newtype constructors (e.g. `Velocity(0.8)`) in binaries or tests unless the type is actually in scope at that location; check the module's imports and re-exports first, and construct values through the type's published API.
  - Distilled from a crest-synth phase-1 run: a generated `src/main.rs` failed compilation by calling a kernel newtype's tuple constructor that was not imported; the retry succeeded after switching to the public constructor.

- In `///` doc examples (doctests, compiled by `cargo test`), never declare a binding the example doesn't use — an unused `let x: Vec<_> = vec![]` fails to compile with E0282 (type annotations needed) and breaks the test suite. Either use every binding or omit it; pass empty literals like `&[]` directly to the call so element types infer from the signature.
  - Distilled from a crest-synth phase-4 soak run: PatchMixer's doc example declared `let patches: Vec<_> = vec![];`, never used it, and the doctest failed E0282 — which kept the wave's `cargo test` validation red.
