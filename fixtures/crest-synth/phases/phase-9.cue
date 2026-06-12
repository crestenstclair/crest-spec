package crestsynth

// Phase 9: Gamepad UX + all adapters + context map
// Controller input, full-screen layout, distributable build.

// ── Shell additions ────────────────────────────────────

project: contexts: Shell: ports: GamepadInput: {
	contract: {poll: "() -> Vec<GamepadEvent>", connectedControllers: "() -> Vec<ControllerId>", controllerType: "ControllerId -> ControllerType"}
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with GamepadInput port"}]
}
project: contexts: Shell: ports: GuiRenderer: {
	contract: {beginFrame: "() -> UiContext", endFrame: "UiContext -> ()", customPaint: "(Rect, PaintCallback) -> ()"}
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with GuiRenderer port"}]
}

project: contexts: Shell: valueObjects: GamepadAction:   {from: "enum", description: "Navigate, Select, Back, TweakUp, TweakDown, AssignMod, NextPage, PreviousPage, QuickSave", validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with GamepadAction"}]}
project: contexts: Shell: valueObjects: ControllerGlyph: {
	state: {button: "GamepadButton", controllerType: "ControllerType", glyphPath: "string"}
	description: "maps a logical button to the correct visual glyph for the connected controller"
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with ControllerGlyph"},
		{kind: "test", command: ["cargo", "test", "controller_glyph"], description: "ControllerGlyph unit tests pass"},
	]
}

project: contexts: Shell: domainServices: GamepadNavigator: {
	purpose: "translates raw gamepad events into GamepadActions and drives the cursor/edit model"
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with GamepadNavigator"},
		{kind: "test", command: ["cargo", "test", "gamepad_navigator"], description: "GamepadNavigator unit tests pass"},
	]
}
project: contexts: Shell: domainServices: GlyphResolver: {
	purpose: "resolves the correct controller glyph for each button based on connected controller type"
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with GlyphResolver"},
		{kind: "test", command: ["cargo", "test", "glyph_resolver"], description: "GlyphResolver unit tests pass"},
	]
}

// ── Adapters ───────────────────────────────────────────

project: adapters: MidirInput: {
	implements: "port.Shell.MidiInput"
	layer: "infrastructure"
	meta: notes: "midir: cross-platform MIDI I/O"
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with MidirInput adapter"}]
}
project: adapters: Midi2Normalizer: {
	implements: "port.Shell.MidiNormalizer"
	layer: "infrastructure"
	meta: notes: "midi2: MIDI 1.0 to internal model upconversion"
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with Midi2Normalizer adapter"}]
}
project: adapters: EframeWindow: {
	implements: "port.Shell.AppWindow"
	layer: "infrastructure"
	meta: notes: "eframe: winit + wgpu window shell for egui"
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with EframeWindow adapter"}]
}
project: adapters: GilrsGamepad: {
	implements: "port.Shell.GamepadInput"
	layer: "infrastructure"
	meta: notes: "gilrs: cross-platform gamepad input"
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with GilrsGamepad adapter"}]
}
project: adapters: EguiRenderer: {
	implements: "port.Shell.GuiRenderer"
	layer: "infrastructure"
	meta: notes: "egui: immediate-mode UI with custom painting"
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with EguiRenderer adapter"}]
}
project: adapters: FundspEffects: {
	implements: "port.Effects.EffectProcessor"
	layer: "infrastructure"
	meta: notes: "fundsp: composable DSP nodes for reverb, chorus, delay"
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with FundspEffects adapter"}]
}
project: adapters: SerdePresetCodec: {
	implements: "port.Presets.PresetCodec"
	layer: "infrastructure"
	meta: notes: "serde_json for presets, bincode for setups"
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with SerdePresetCodec adapter"},
		{kind: "test", command: ["cargo", "test", "serde_preset_codec"], description: "SerdePresetCodec round-trip tests pass"},
	]
}

// ── Context map ────────────────────────────────────────

project: contextMap: shellToSynth:        {from: "Shell", to: "Synth", kind: "customer-supplier", direction: "downstream"}
project: contextMap: shellToPatch:        {from: "Shell", to: "Patch", kind: "customer-supplier", direction: "downstream"}
project: contextMap: patchToSynth:        {from: "Patch", to: "Synth", kind: "customer-supplier", direction: "downstream"}
project: contextMap: patchToEffects:      {from: "Patch", to: "Effects", kind: "customer-supplier", direction: "downstream"}
project: contextMap: modToSynth:          {from: "Modulation", to: "Synth", kind: "customer-supplier", direction: "downstream"}
project: contextMap: modToPatch:          {from: "Modulation", to: "Patch", kind: "customer-supplier", direction: "downstream"}
project: contextMap: sampleLibToSynth:    {from: "SampleLibrary", to: "Synth", kind: "customer-supplier", direction: "downstream"}
project: contextMap: sampleLibToRealTime: {from: "SampleLibrary", to: "RealTime", kind: "customer-supplier", direction: "downstream"}
project: contextMap: presetsToPatch:      {from: "Presets", to: "Patch", kind: "customer-supplier", direction: "downstream"}
project: contextMap: presetsToMod:        {from: "Presets", to: "Modulation", kind: "customer-supplier", direction: "downstream"}
project: contextMap: presetsToEffects:    {from: "Presets", to: "Effects", kind: "customer-supplier", direction: "downstream"}
project: contextMap: kernelToSynth:       {from: "Kernel", to: "Synth", kind: "shared-kernel"}
project: contextMap: kernelToPatch:       {from: "Kernel", to: "Patch", kind: "shared-kernel"}
project: contextMap: kernelToMod:         {from: "Kernel", to: "Modulation", kind: "shared-kernel"}
project: contextMap: realTimeToSynth:     {from: "RealTime", to: "Synth", kind: "anti-corruption", direction: "upstream"}
project: contextMap: realTimeToPatch:     {from: "RealTime", to: "Patch", kind: "anti-corruption", direction: "upstream"}

// ── Gamepad navigation made provable (the phase-9 behavior prover) ─────
// gamepad_demo proves the controller-first navigation logic WITHOUT any device
// or window: it feeds a scripted sequence of raw GamepadEvents through the
// GamepadNavigator (translating them into GamepadActions that drive the app's
// own cursor/edit model) and resolves glyphs through the GlyphResolver for more
// than one controller type. The gilrs/egui/eframe adapters open real devices and
// windows, so they are NEVER invoked by a validation; this demo exercises the
// host-agnostic domain services (GamepadNavigator, GlyphResolver) that the
// adapters merely feed — which is exactly the "UI is a pure view / nav uses the
// app's own cursor model" invariant made checkable.

project: assets: GamepadNavDemoMain: {
	kind:        "rust-bin-target"
	description: "src/bin/gamepad_demo.rs: headless prover for GamepadNavigator + GlyphResolver — scripted events -> GamepadActions -> cursor model, glyph resolution per controller type"
	uses: ["domainService.Shell.GamepadNavigator", "domainService.Shell.GlyphResolver"]
	prompts: [
		"File path: src/bin/gamepad_demo.rs",
		"CLI: `gamepad_demo`. Takes no arguments and opens NO device and NO window — it is a headless harness over the host-agnostic Shell domain services. Do NOT import gilrs, egui, or eframe.",
		"Build a small app cursor/edit model (the app's OWN navigation state, not egui focus). Feed a SCRIPTED, deterministic sequence of raw GamepadEvents through the GamepadNavigator, which must translate them into GamepadActions (Navigate, Select, Back, TweakUp, TweakDown, AssignMod, NextPage, PreviousPage, QuickSave) and drive the cursor/edit model accordingly.",
		#"Assert in code that the scripted events produce the EXPECTED GamepadActions and the EXPECTED final cursor position (panic with a clear message on mismatch). Print a verbatim line `nav actions ok: N` where N is the number of actions dispatched."#,
		#"Drive the GlyphResolver for at least TWO different ControllerTypes (e.g. an Xbox-style and a PlayStation-style controller) and assert each resolves to a DIFFERENT glyph for the same logical button (panic if identical). Print a verbatim line `glyphs resolved: per-controller`."#,
		"Print a short summary. The `nav actions ok:` and `glyphs resolved: per-controller` tokens MUST appear verbatim so a validation can assert the navigation + glyph logic ran correctly with no device.",
		"Exit 0 on success (both in-code assertions must pass).",
	]
	validations: [
		{kind: "compiles", command: ["make", "build"], description: "gamepad demo builds"},
		// device-free + window-free: this validation exercises only the
		// host-agnostic GamepadNavigator/GlyphResolver domain services.
		{kind: "integration", command: ["make", "check-gamepad"], description: "scripted gamepad events map to actions and glyphs resolve per controller, no device", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "stdout_contains", pattern: "nav actions ok:"},
			{kind: "stdout_contains", pattern: "glyphs resolved: per-controller"},
		]},
	]
}

// ── Invariants ─────────────────────────────────────────

project: invariants: shellDesign: [
	{text: "the engine library is host-agnostic; no audio driver, window, or controller code in the library", meta: rationale: "standalone and plugin wrapper are different shells over the same library"},
	{text: "the UI is a pure view over engine state; no audio logic lives in the GUI layer", meta: rationale: "keeps DSP and voice logic testable in isolation"},
	{text: "all gamepad navigation uses the app's own cursor/edit model, not egui's built-in focus", meta: rationale: "generic focus traversal doesn't fit a controller-first workflow"},
]
