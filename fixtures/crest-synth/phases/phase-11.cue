package crestsynth

// Phase 11: Standalone editor app — keyboard/gamepad-driven parameter editor.
//
// A real, runnable eframe/egui WINDOW that hosts the live engine (external
// MIDI in via MidirInput, audio out via cpal) and lets you edit synth
// parameters while external gear plays notes. This is an EDITOR, not a
// performance surface: there is NO on-screen keyboard, NO note triggering,
// and NO mouse/touch input. All notes come from external MIDI hardware.
//
// Architecture: a strict ONE-WAY event loop (Elm/Flux). The keyboard/gamepad
// input adapter translates raw keys/buttons into semantic EditorEvents; those
// events are applied to a single store (EditorState); the egui view is a pure
// function of that store's state. Widgets never mutate state and never touch a
// parameter directly — they only render. Edited parameter values are published
// to the audio engine across the phase-3 lock-free seam as a ParameterSnapshot;
// the audio model consumes external MIDI + that snapshot and knows nothing
// about EditorEvents.
//
// `make ui` launches the window; `make ui-smoke` is the hermetic headless
// validation (constructs app state, opens NO window and NO audio device).
// Depends only on phases 1-9 components; does NOT depend on the phase-10 plugin.

// ── Editor context: the control plane (one-way event loop) ─────────────
// EditorState is the single store. The egui shell and the gamepad adapter both
// emit the SAME EditorEvents into it, so keyboard and gamepad are interchangeable
// and the whole control plane is hermetically testable: feed an event sequence,
// assert focus / edit-mode / field values — no window, no device.

project: contexts: Editor: purpose: "keyboard/gamepad-driven parameter editor: a one-way event loop over a single store that edits live engine parameters"
project: contexts: Editor: ubiquitousLanguage: {
	EditorEvent: "a semantic input event (navigate or edit-mode change) emitted by the keyboard/gamepad adapter — the only thing that mutates editor state"
	EditorState: "the single store: focus position, edit-mode flag, and the list of editable parameter fields"
	ParamField:  "one editable parameter row: label, current value, bounds, and fine step"
	EditMode:    "active only while the edit modifier (J / a gamepad button) is held; directional input then adjusts the focused field's value instead of moving focus"
}

project: contexts: Editor: valueObjects: EditorEvent: {
	from:        "enum"
	description: "NavUp, NavDown, NavLeft, NavRight, EnterEditMode, ExitEditMode — the semantic input vocabulary; keyboard and gamepad adapters both emit these and nothing else"
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with EditorEvent"}]
}

project: contexts: Editor: valueObjects: ParamField: {
	state:       {id: "string", label: "string", value: "f64", min: "f64", max: "f64", step: "f64"}
	description: "one editable parameter row: a label, current value, inclusive bounds, and the fine adjustment step (coarse = 10x step)"
	invariants: ["min <= max", "value is always within [min, max]", "step > 0"]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with ParamField"},
		{kind: "test", command: ["cargo", "test", "param_field"], description: "ParamField clamp/bounds unit tests pass"},
	]
}

project: contexts: Editor: aggregates: EditorState: {
	root:    true
	purpose: "the single editor store: owns focus, edit-mode, and the editable parameter fields; the one entry point that reacts to EditorEvents"
	state: {fields: "Vec<ParamField>", focus: "usize", editMode: "bool"}
	meta: notes: """
		Implement EditorState as a Flux-style store with ONE mutation entry point:
		`fn apply(&mut self, event: EditorEvent)`. There are no setters and no other
		way to change state — this is the heart of the one-way loop.

		Behavior of apply(), by current mode:
		  - EnterEditMode  -> editMode = true
		  - ExitEditMode   -> editMode = false
		  - When editMode == false (NAVIGATE): NavUp/NavDown/NavLeft/NavRight move
		    `focus` between fields by one (saturating at the ends; no wrap unless you
		    note otherwise). (Vertical and horizontal both move focus by one in the
		    simple single-column MVP; layout may map them differently later.)
		  - When editMode == true (EDIT): directional input adjusts the FOCUSED
		    field's value instead of moving focus:
		      * NavLeft  -> value -= step   (fine, -1 unit)
		      * NavRight -> value += step   (fine, +1 unit)
		      * NavDown  -> value -= 10*step (coarse, -10 units)
		      * NavUp    -> value += 10*step (coarse, +10 units)
		    Every adjustment clamps to the field's [min, max].

		Keep it pure and allocation-free in apply(); no I/O, no rendering, no audio.
		This store is unit-tested by feeding EditorEvent sequences and asserting the
		resulting focus / editMode / field values — that is the `cargo test
		editor_state` validation, and it is the real proof that 'keyboard buttons
		work' without ever opening a window.
		"""
	invariants: [
		"apply(EditorEvent) is the ONLY way to mutate editor state",
		"focus always stays within the fields range",
		"in navigate mode directional events move focus; in edit mode they adjust the focused field's value",
		"horizontal adjust is one step (fine); vertical adjust is ten steps (coarse)",
		"every value adjustment clamps to the focused field's [min, max]",
	]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with EditorState"},
		{kind: "test", command: ["cargo", "test", "editor_state"], description: "EditorState event-reducer unit tests pass (nav, edit-mode, fine/coarse, clamping)"},
	]
}

// ── Standalone editor app (hosts the live engine) ──────────────────────

project: assets: StandaloneUiMain: {
	kind:        "rust-bin-target"
	description: "src/bin/synth_ui.rs: standalone eframe/egui parameter editor, keyboard+gamepad driven (no mouse/touch, no note triggering); hosts the live engine (external MIDI in via MidirInput, audio out via cpal); one-way EditorEvent -> EditorState -> view loop; hermetic --smoke headless mode"
	uses: [
		"aggregate.Editor.EditorState",
		"valueObject.Editor.EditorEvent",
		"adapter.EguiRenderer",
		"adapter.EframeWindow",
		"adapter.GilrsGamepad",
		"port.Shell.GamepadInput",
		"adapter.MidirInput",
		"port.Shell.MidiInput",
		"adapter.CpalAudioOutput",
		"port.Shell.AudioOutput",
		"aggregate.Patch.GlobalMixer",
		"domainService.Patch.PatchMixer",
		"port.Synth.SynthEngine",
		"domainService.Synth.VoiceAllocator",
	]
	prompts: [
		"File path: src/bin/synth_ui.rs",
		"This is the STANDALONE EDITOR app. It opens a real eframe/egui window AND hosts the live synth engine. It must build and run on macOS using eframe's default backend.",
		"SCOPE: keep this MVP very simple. The goal is to prove the window, the keyboard/gamepad input, the one-way event loop, and the live-engine hosting all work end to end — NOT to build a full editor. A short fixed list of editable parameters is enough.",
		"INPUT IS KEYBOARD + GAMEPAD ONLY. Do NOT implement any mouse or touch interaction — no clickable widgets, no draggable sliders, no hover behavior. (Mouse/touch may be added later; not now.)",
		"This is an EDITOR, not a performance surface: there is NO on-screen keyboard and NO note triggering of any kind from the UI. All note performance comes from EXTERNAL MIDI hardware.",
		#"Key bindings (keyboard): W = up, S = down, A = left, D = right. Holding J = edit mode (momentary: edit mode is active only while J is held; releasing J returns to navigate mode). The input layer reads raw egui key state each frame and translates it into semantic EditorEvents (NavUp/NavDown/NavLeft/NavRight on key-press edges; EnterEditMode/ExitEditMode on the J hold transitions). The gamepad adapter (GilrsGamepad / GamepadInput) maps the D-pad to the same Nav events and a face button to the same EnterEditMode/ExitEditMode, so keyboard and gamepad emit IDENTICAL EditorEvents."#,
		"ONE-WAY EVENT LOOP: the only way UI input changes state is by emitting EditorEvents and calling EditorState::apply on them. The egui draw code is a PURE VIEW over EditorState — it renders the field list, highlights the focused field, and shows an edit-mode indicator, but it NEVER mutates state directly and NEVER reads or writes a parameter except through EditorState.",
		"Seed EditorState with a small fixed set of ParamFields mapped to real engine parameters — e.g. master gain (0.0..=1.0) on the GlobalMixer, and one or two SynthEngine/voice parameters such as filter cutoff. Editing a field updates EditorState; after each event-loop tick, publish the current field values to the audio engine as a ParameterSnapshot across the phase-3 lock-free real-time seam (no locks/alloc/blocking on the audio callback).",
		"HOST THE LIVE ENGINE: open external MIDI input via the MidirInput adapter (Shell::MidiInput) and an audio output stream via the CpalAudioOutput adapter (Shell::AudioOutput). Notes arriving from external MIDI drive VoiceAllocator -> SynthEngine -> PatchMixer -> GlobalMixer; the editor's published ParameterSnapshot adjusts that signal live. The UI shows the parameter values (and may show a simple read-only activity/level indicator fed by published engine telemetry), but originates no notes itself.",
		"CLI: `synth_ui [--smoke]`. Default mode opens the window and runs the loop. Parse args yourself; treat any unknown flag as a clear stderr error with non-zero exit.",
		#"`--smoke` mode is HERMETIC and HEADLESS: construct the ENTIRE app state exactly as the window path would — the EditorState with its seeded ParamFields, the engine objects (VoiceAllocator/SynthEngine/PatchMixer/GlobalMixer), and the cpal stream-CONFIG value (sample rate / channels / buffer) — but do NOT call eframe::run_native, do NOT open a window, do NOT open or start any cpal stream or audio device, and do NOT open any MIDI device. Then drive a few EditorEvents through EditorState::apply to confirm the loop is wired, print EXACTLY `ui smoke ok: app constructed`, and exit 0."#,
		"In --smoke mode never touch cpal/midir device-opening APIs and never enter the eframe event loop; it must return 0 quickly and deterministically on any machine (including CI with no display, no audio, no MIDI). Building config/value objects is allowed; opening devices or windows is NOT. The token `ui smoke ok` must appear verbatim in stdout.",
	]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with the standalone editor app"},
		// hermetic: ui-smoke constructs full app state + drives a few EditorEvents
		// and exits 0 WITHOUT opening a window, audio device, or MIDI device. The
		// real window is launched manually via `make ui`.
		{kind: "integration", command: ["make", "ui-smoke"], description: "editor app constructs and runs the event loop headlessly", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "stdout_contains", pattern: "ui smoke ok"},
		]},
	]
}

// ── Invariants ─────────────────────────────────────────

project: invariants: standaloneEditor: [
	{text: "the editor UI is keyboard/gamepad driven only — no mouse or touch input in this implementation", meta: rationale: "keeps the initial implementation clean; pointer input can be added later without changing the event-loop core"},
	{text: "the editor is not a performance surface: it originates no notes; all note performance comes from external MIDI", meta: rationale: "the UI's job is editing parameters/patches, not playing"},
	{text: "the UI mutates state only by emitting EditorEvents applied to EditorState; egui draw code is a pure view", meta: rationale: "one-way data flow keeps state changes traceable and the control plane hermetically testable"},
	{text: "the audio model consumes external MIDI plus a published ParameterSnapshot and never observes EditorEvents", meta: rationale: "keeps the engine host-agnostic and the realtime path decoupled from the UI event loop"},
	{text: "the ui smoke path opens no window, no audio device, and no MIDI device; it only constructs state and drives the event loop", meta: rationale: "keeps the standalone app mechanically checkable with no display or hardware"},
	{text: "the window backend must use a current eframe/egui (0.28+ on objc2 0.5+/winit 0.30+), never the 0.27 line that pulls icrate 0.0.4", meta: rationale: "eframe 0.27/objc2-0.3-beta/icrate-0.0.4 aborts at window creation on current macOS (NSScreen enumeration ABI panic); this is invisible to ui-smoke (no window) so it can only be prevented by the dependency pin, not the validation loop"},
]
