package crestsynth

// Phase 11: Standalone GUI app — the visual analog of the audible demos.
// A real, runnable eframe/egui window that wires the existing engine
// (patches + mixer + voice allocation) and cpal audio into an interactive
// synth you can SEE and HEAR. `make ui` launches the window; `make ui-smoke`
// is the hermetic, headless validation (constructs the full app state, opens
// NO window and NO audio device). Depends only on phases 1-9 components — it
// does NOT depend on the phase-10 plugin wrapper.

// ── Standalone interactive synth window (the visual prover) ────────────
// synth_ui is the GUI counterpart to the demo-*/play-* binaries: instead of
// rendering to a WAV, it opens a live window over the SAME engine library —
// EguiRenderer/EframeWindow drive the view, the Patch aggregate + GlobalMixer
// + PatchMixer + SynthEngine/VoiceAllocator drive synthesis, and cpal makes
// triggered notes audible. The UI stays a pure view over engine state (the
// shellDesign invariant). The validation never opens a window or device: it
// runs the hermetic `--smoke` path, which constructs the entire app state
// (patches, engine, mixer, and the cpal stream-config object) WITHOUT calling
// eframe::run_native and WITHOUT opening any cpal stream — making the wiring
// mechanically checkable on any machine, including CI.

project: assets: StandaloneUiMain: {
	kind:        "rust-bin-target"
	description: "src/bin/synth_ui.rs: standalone eframe/egui synth window — interactive patch list, on-screen note triggers, master gain, level meter, cpal audio; hermetic --smoke headless mode for validation"
	uses: [
		"adapter.EguiRenderer",
		"adapter.EframeWindow",
		"adapter.CpalAudioOutput",
		"port.Shell.AudioOutput",
		"aggregate.Patch.Patch",
		"aggregate.Patch.GlobalMixer",
		"domainService.Patch.PatchMixer",
		"port.Synth.SynthEngine",
		"domainService.Synth.VoiceAllocator",
		"repository.Patch.PatchRepository",
	]
	prompts: [
		"File path: src/bin/synth_ui.rs",
		"This is the STANDALONE GUI app: the visual analog of the audible demo binaries. It opens a real, interactive window over the same engine library that the demos render to WAV. It must build and run on macOS using eframe's default backend.",
		"CLI: `synth_ui [--smoke]`. Two modes only: default (interactive window) and `--smoke` (hermetic headless self-check). Parse args yourself; treat any unknown flag as an error with a clear stderr message and non-zero exit.",
		#"DEFAULT mode (no args): build the full app state, then call `eframe::run_native` to open a window titled "crest-synth". Render the UI with egui each frame. Where practical, route drawing through the EguiRenderer/GuiRenderer abstraction (the GuiRenderer port + EguiRenderer adapter); it is acceptable to call egui directly inside this bin since the bin IS the shell. Use the EframeWindow adapter / AppWindow port as the window shell where it fits the run-loop model."#,
		"Build the app state from a PatchRepository: populate it with 2-3 distinct Patch aggregates (different OscillatorConfig / FilterConfig / AmpEnvelopeConfig and gain/pan, each on its own ChannelSubscription), then read them back via the repository so the UI lists what the repository holds — the UI must be a pure VIEW over engine/repository state, never its own source of truth.",
		"The interactive UI must be BASIC BUT REAL: (1) a Patch list panel showing every patch from the PatchRepository, with a way to select the active/target patch; (2) an on-screen row of note buttons or a simple clickable one-octave keyboard that triggers note-on/note-off on the selected patch; (3) a master gain slider bound to the GlobalMixer's master gain; (4) a simple live level / active-voice meter (e.g. a bar showing current output peak or the number of active voices).",
		"Triggering a note from the UI must be AUDIBLE: open a cpal output stream (via the CpalAudioOutput adapter / Shell::AudioOutput port) and feed it audio produced by the engine — note-ons go through the selected patch's VoiceAllocator into Voices rendered by the SynthEngine, summed by the PatchMixer (per-patch gain/pan) and the GlobalMixer (master gain). Respect the lock-free audio-thread invariants from phase 3 (no heap alloc / locks / blocking I/O on the audio callback); cross the boundary with the existing real-time seam.",
		"Keep the UI a pure view: no DSP or voice logic in the egui draw code. UI widgets enqueue intents (note-on/off, set gain, select patch); the engine/audio side consumes them across the real-time boundary and the meter reads back published engine state.",
		#"`--smoke` mode is HERMETIC and HEADLESS: construct the ENTIRE app state exactly as the window path would — the PatchRepository with its patches, the per-patch VoiceAllocator(s)/SynthEngine, the PatchMixer, the GlobalMixer, AND the cpal stream-CONFIG object (sample rate / channels / buffer config) that the live path would use — but do NOT call eframe::run_native, do NOT open a window, and do NOT open or start any cpal stream or audio device. Then print EXACTLY a line `ui smoke ok: app constructed` and exit 0."#,
		"In --smoke mode never touch cpal's host/device stream-opening APIs and never enter the eframe event loop; it must return 0 quickly and deterministically on any machine (including CI with no display and no audio device). Building the cpal StreamConfig value is allowed; opening/playing a stream is NOT.",
		"The token `ui smoke ok` must appear verbatim in stdout so the validation can assert the headless construction ran. The smoke path must open NO window and NO audio device.",
	]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with the standalone UI app"},
		// hermetic: ui-smoke constructs the full app state (patches, engine,
		// mixer, cpal stream-config) and exits 0 WITHOUT opening a window or an
		// audio device. The real window is launched manually via `make ui`.
		{kind: "integration", command: ["make", "ui-smoke"], description: "UI app constructs headlessly", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "stdout_contains", pattern: "ui smoke ok"},
		]},
	]
}

// ── Invariants ─────────────────────────────────────────
// (reinforces the phase-9 shellDesign invariants for the standalone window)

project: invariants: standaloneUi: [
	{text: "the standalone window is one shell over the engine library; the audible demos are another — both share the same engine, patches, and mixer", meta: rationale: "the GUI must not fork engine behavior away from the WAV-rendering demos"},
	{text: "the UI smoke path opens no window and no audio device; it only constructs app state", meta: rationale: "keeps the standalone app mechanically checkable with no display or audio hardware"},
]
