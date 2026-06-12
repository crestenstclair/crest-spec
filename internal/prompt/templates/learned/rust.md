<!-- Promoted craft learnings are appended below this line. Do not edit by hand; use spec/promote_learnings. -->

- Do not invent newtype constructors (e.g. `Velocity(0.8)`) in binaries or tests unless the type is actually in scope at that location; check the module's imports and re-exports first, and construct values through the type's published API.
  - Distilled from a crest-synth phase-1 run: a generated `src/main.rs` failed compilation by calling a kernel newtype's tuple constructor that was not imported; the retry succeeded after switching to the public constructor.
