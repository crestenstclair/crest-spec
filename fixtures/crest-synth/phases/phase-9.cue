package crestsynth

// Phase 9: Gamepad UX + all adapters + context map
// Controller input, full-screen layout, distributable build.

// ── Shell additions ────────────────────────────────────

project: contexts: Shell: ports: GamepadInput: contract: {poll: "() -> Vec<GamepadEvent>", connectedControllers: "() -> Vec<ControllerId>", controllerType: "ControllerId -> ControllerType"}
project: contexts: Shell: ports: GuiRenderer: contract:   {beginFrame: "() -> UiContext", endFrame: "UiContext -> ()", customPaint: "(Rect, PaintCallback) -> ()"}

project: contexts: Shell: valueObjects: GamepadAction:    {from: "enum", description: "Navigate, Select, Back, TweakUp, TweakDown, AssignMod, NextPage, PreviousPage, QuickSave"}
project: contexts: Shell: valueObjects: ControllerGlyph:  {state: {button: "GamepadButton", controllerType: "ControllerType", glyphPath: "string"}, description: "maps a logical button to the correct visual glyph for the connected controller"}

project: contexts: Shell: domainServices: GamepadNavigator: {purpose: "translates raw gamepad events into GamepadActions and drives the cursor/edit model"}
project: contexts: Shell: domainServices: GlyphResolver:    {purpose: "resolves the correct controller glyph for each button based on connected controller type"}

// ── Adapters ───────────────────────────────────────────

project: adapters: MidirInput:       {implements: "port.Shell.MidiInput", layer: "infrastructure", meta: notes: "midir: cross-platform MIDI I/O"}
project: adapters: Midi2Normalizer:  {implements: "port.Shell.MidiNormalizer", layer: "infrastructure", meta: notes: "midi2: MIDI 1.0 to internal model upconversion"}
project: adapters: EframeWindow:     {implements: "port.Shell.AppWindow", layer: "infrastructure", meta: notes: "eframe: winit + wgpu window shell for egui"}
project: adapters: GilrsGamepad:     {implements: "port.Shell.GamepadInput", layer: "infrastructure", meta: notes: "gilrs: cross-platform gamepad input"}
project: adapters: EguiRenderer:     {implements: "port.Shell.GuiRenderer", layer: "infrastructure", meta: notes: "egui: immediate-mode UI with custom painting"}
project: adapters: FundspEffects:    {implements: "port.Effects.EffectProcessor", layer: "infrastructure", meta: notes: "fundsp: composable DSP nodes for reverb, chorus, delay"}
project: adapters: SerdePresetCodec: {implements: "port.Presets.PresetCodec", layer: "infrastructure", meta: notes: "serde_json for presets, bincode for setups"}

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

// ── Invariants ─────────────────────────────────────────

project: invariants: shellDesign: [
	{text: "the engine library is host-agnostic; no audio driver, window, or controller code in the library", meta: rationale: "standalone and plugin wrapper are different shells over the same library"},
	{text: "the UI is a pure view over engine state; no audio logic lives in the GUI layer", meta: rationale: "keeps DSP and voice logic testable in isolation"},
	{text: "all gamepad navigation uses the app's own cursor/edit model, not egui's built-in focus", meta: rationale: "generic focus traversal doesn't fit a controller-first workflow"},
]
