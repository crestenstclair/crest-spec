Read and follow `.agents/skills/apply-crest-spec/SKILL.md` completely. Resume and finish the EXISTING CrestSpec session `e3360b9c-fac4-4313-9b18-1f2a0b7fc1ed` in this repository.

Hard boundaries:

- Before any mutation, call `about` and require `buildFingerprint` exactly `b2b02658ec1971622603e682bce7ffadc6197e9dbb60cb5141c81710c5d7031e`. Stop and report if it differs.
- Use only the existing session. Do NOT call `spec/begin`, abandon it, amend CUE, create another session, destroy anything, skip anything, or force finish.
- Do NOT manually edit Rust, Cargo, Makefile, CUE, or any governed project file. Every implementation byte must be an exact complete-file candidate submitted through the CrestSpec lifecycle. Preserve the dirty worktree; do not commit, reset, revert, or clean.
- This is a targeted recovery/update, not regeneration. Do not rescan or rewrite unchanged resources. Never dispatch a worker for a `NoDispatch` reinstatement.
- Use a persistent generic worker only for a real context. Reuse it if another true repair context is explicitly routed. Do not create a worker per resource. Time is the priority.
- For every real context, follow `dispatch_requested -> worker_acknowledged -> execution_start -> commit`. Candidate bytes must be raw repository bytes only: no `UPDATE MODE`, `File:`, Markdown fences, tool-output banners, or commentary. Never use file-mode `rustfmt --emit stdout PATH` output as candidate content because it prefixes the filename. If formatting is needed, format via stdin or another banner-free path and verify exact bytes.
- Run broad affected-closure validation once after the actual repair frontier is terminal, not after each no-op reinstatement.
- If an engine/lifecycle/provenance error remains, preserve the session and report the exact error. Do not work around it.

Exact current state:

- Six historical exhausted/handoffless innocent owners are already atomically reinstated and committed: `valueObject.Control.EventLog`, `valueObject.Control.EventRecord`, `valueObject.Control.StateTree`, `domainService.Control.StateProjector`, `applicationService.Control.AppLoop`, and `applicationService.Testing.AutomaticMidiTest`. Do not reopen or regenerate them.
- Five handoff-backed failures are already explicitly resolved by accepted StandaloneApplication attempt `723f8501-a42d-4e38-8165-032768c3aae3`: `applicationService.Shell.KeyboardInputTranslator`, `adapter.EframeTextWindow`, `applicationService.Testing.ExhaustiveGuiDemo`, `asset.CrestSynthMain`, and `asset.LibraryRoot`. Their resource projections are already resolved. Do not call `spec/failure_resolve` or `spec/resolve` again.
- KeyboardInputTranslator has an accepted handoff whose target attempt `f88d0030-aad4-4980-ad5a-cb4820beb1f6` was abandoned before dispatch, execution, or candidate submission. The new engine must retire that exact provenance and return `NoDispatch`. EframeTextWindow and ExhaustiveGuiDemo have exact pending handoffs and must also return `NoDispatch`. CrestSynthMain and LibraryRoot are downstream of the true repair and must be reinstated without workers when their wave becomes reachable.
- `valueObject.Testing.DemoScene` and `valueObject.Testing.DemoSceneReport` already contain accepted formatting repairs and must remain unchanged.
- The sole real repair owner is `applicationService.Shell.StandaloneApplication`, using its existing legal handoff. Dispatch exactly this resource unless a new structured diagnostic proves another true owner.

Product evidence already measured from the unchanged repository:

- `cargo test --all-targets` runs 174 tests: 173 pass and only `shell::standalone_application::tests::standalone_exhaustive_gui_demo_composes_a_complete_production_trace` fails at `src/shell/standalone_application.rs:1396` because its two-patch test report is incomplete.
- The real production demo is healthy: `make demo` exits 0 with `demo_scene_complete=true`, `coverage_missing=0`, 508 event-log records, 504 accepted events, 4 rejected events, all 7 global parameters exercised, all patch parameter cases exercised, coherent event/state hash chains, and 15 distinct patch rows.
- Its controlled negative is discriminating: `--degenerate-control` exits nonzero with `demo_scene_complete=false` and exactly one missing coverage identifier.
- `make observe` exits 0 and proves distinct channels/stems, per-patch audio isolation, non-first-patch editing, nonfatal boundary no-op, and continued accepted editing.

Therefore the remaining repair is the stale or insufficient StandaloneApplication unit-test composition/fixture, not a broad product rewrite. Inspect the owned context and make the smallest change that makes the unit fixture faithfully exercise the already-working production behavior. Do not weaken or delete the assertions, coverage requirements, positive/negative witness, event log, state tree, or audio-effect checks. Do not change unrelated production behavior.

Required workflow and acceptance:

1. Call `spec/status`/`spec/next`, then consume the innocent wave-1 through wave-3 resources as `NoDispatch` reinstatements. Confirm generation-attempt, execution, and candidate counts do not increase for them.
2. Obtain the one real StandaloneApplication context and run its full governed dispatch/execution/commit lifecycle with one minimal complete-file candidate.
3. Consume downstream CrestSynthMain and LibraryRoot as `NoDispatch` reinstatements when reachable. No downstream worker is allowed without a new structured owner diagnostic.
4. Run one affected-closure gate after the frontier is terminal. It must pass all project checks, including 174/174 tests and rustfmt/compiler checks, with no unresolved diagnostic.
5. Reconcile every required or stale behavioral witness through real `crest_verifier` execution and persisted evidence. This includes positive and negative runs for `control_path`, `running_synth`, and `exhaustive_demo_scene`, proving the event log/state tree cover all current GUI events, parameters, properties, accepted/rejected and boundary outcomes, per-patch isolation, and emitted/listened events.
6. Inspect persisted witness results, then call `spec/finish` without force only when CrestSpec reports both required goals complete with current evidence.

Keep progress concise and frequent: fingerprint, resource/count deltas, the single real worker, closure result, witness results, and exact blocker if any.
