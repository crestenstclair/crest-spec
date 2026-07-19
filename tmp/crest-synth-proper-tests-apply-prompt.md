Read and follow `.agents/skills/apply-crest-spec/SKILL.md` completely. Apply the CURRENT CUE specification in this repository incrementally and finish only after its behavioral evidence is current.

Framework identity and superseded session:

- Before any mutation, call `about` and require `buildFingerprint` exactly `79a38b3b85aedf74004f2a620b609f95e5331ad30c76068907c2593e959dd1f3`. Stop if it differs.
- Call `spec/validate` first. The current CUE has already been loader-validated by the maintainer and intentionally strengthens behavioral testing.
- Session `e3360b9c-fac4-4313-9b18-1f2a0b7fc1ed` is still active but was created from the superseded acceptance contract. The user explicitly authorizes abandoning that exact session, without deleting or reverting any project files, after inspecting its status. Record the reason as: `superseded by stronger schema-derived, faithful-audio, egui-context, and real-seam mutation acceptance`. Do not try to finish stale evidence under the old spec.
- Then plan and begin one new apply for the current spec. Preserve all accepted project bytes already present. Do not abandon any new session unless an engine-level safety failure makes continuation impossible.

Hard boundaries:

- This is an incremental UPDATE, not regeneration. Inspect the plan/impact and process only the changed ownership/dependency frontier. Unchanged or dependency-only consumers must use CrestSpec's `NoDispatch`/`validate_existing`/candidate-reuse paths; never send them through a model merely to inspect or recommit unchanged files.
- Do not manually edit Rust, Cargo, Makefile, CUE, or any governed project file. Every implementation byte must be a complete-file candidate submitted through CrestSpec's dispatch/execution/commit lifecycle. Preserve the dirty worktree; do not commit, reset, revert, clean, or delete unrelated files.
- Maintain a warm pool of up to three reusable generic workers. Use `spec/context_batch` with `limit` equal to idle slots, dispatch all independent current-wave owners immediately, and reuse workers across waves. Do not create one fresh worker per resource.
- For every real context, follow `dispatch_requested -> worker_acknowledged -> execution_start -> commit`. Candidate content must be exact raw repository bytes only: no Markdown fences, UPDATE MODE envelope, filename banner, or commentary.
- Batch structured diagnostics and repair all routed owners concurrently. Run broad affected-closure validation once per completed repair wave/batch, not once per no-op resource.
- Never weaken, delete, skip, ignore, or mark as expected-failure any test, predicate, mutation case, witness, coverage item, or completion check. Never force finish.

Why this apply exists:

The prior positive demo was broad, but an independent audit found that its negative case could append a fake missing identifier and its unit effects double could synthesize wet excitation from dry output, bypassing Patch sends. Filtered `cargo test <name>` commands could also succeed with zero matching tests. The current CUE closes those holes. Implement the declared contracts, not a workaround:

1. Production-owned typed descriptors and discovered serialized leaves must define the current surface. Expected and observed sets must be equal in both directions; duplicated hand-written coverage strings cannot define their own passing universe.
2. Demo checkpoints must compare exact AppState, StateTree, TextProjection, selected-line/value, and ParameterSnapshot values from the same accepted generation, and restore every reversible parameter, send, selection, and projection exactly to its captured baseline.
3. Wet/global audio comparisons must first establish nonzero reverb and delay sends through the GUI/reducer path, observe the actual supplied effect inputs, and compare paired renders from identical engine/effect state. No dry-derived fake excitation or naturally evolving tail may count.
4. A real headless `egui::Context` must drive RawInput through `EframeApplication.update`, invoke the production callback, render exact projection values, and target the exact selected line; directly testing only a normalization helper is insufficient.
5. Add the bounded verification-only `BehavioralMutationHarness` and `crest-synth-witness` binary. Four isolated cases must alter exactly one real seam: drop an Adjust before AppLoop, rewrite one PatchId at command routing, omit one typed StateTree leaf during serialization, or zero the AudioRenderer output after render. They must not edit completed reports or observation fields.
6. The four governed Cargo integration-test targets must exist: `tests/exhaustive_demo_scene.rs`, `tests/schema_surface.rs`, `tests/eframe_context.rs`, and `tests/behavioral_mutation_harness.rs`. Project validation invokes each with `cargo test --test`, so a missing test cannot pass as zero matched tests. Each target must call public production seams and contain concrete assertions.
7. Keep all previously working behavior: distinct Patch channels/stems, non-first-Patch isolation, nonfatal boundary edits, real SoundFont/MIDI smoke, lossless event/state hash chains, callback safety, and current CLI/Make targets.

Testing and finish requirements:

- Capture the initial narrow failures for missing/new acceptance targets before repairing their owners when practical; do not manufacture red results after implementation.
- After the changed frontier is terminal, run one authoritative affected-closure gate. It must pass formatting, compile/check/clippy, all existing tests, and the four named integration targets with no reopened owner.
- Run all current required witnesses through `spec/verify`. At minimum: `running_synth`, `control_path`, `exhaustive_demo_scene`, `dropped_adjustment_mutant`, `patch_misroute_mutant`, `omitted_state_tree_leaf_mutant`, and `zero_renderer_mutant`.
- Execute independent witnesses concurrently. The rebuilt engine serializes their SQLite completion projections safely. Each witness must persist a complete positive observation and a schema-complete negative observation, enforce its declared exact exit code, reject theater, and produce current evidence.
- Inspect the persisted runs/predicates/evidence. Call `spec/finish` without force only when every required goal and project check is complete, all resources are terminal, and no unresolved failure remains.

Keep progress concise and frequent: fingerprint/validation, old-session transition, incremental plan frontier, worker reuse/concurrency, real candidate deltas, closure result, seven witness results, and exact blocker if any.
