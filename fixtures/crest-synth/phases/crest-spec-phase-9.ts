import { invariant, relationship } from "../../../src/index.js";
import { audioOutputPort, midiInputPort, midiNormalizerPort, appWindowPort } from "./base.js";
import { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer, modulation, modMatrix, sampleLib, sampleSet, effects, effectChain, effectProcessor, presetsCtx, preset, presetBank, setup, presetCodec } from "./crest-spec-phase-8.js";
export { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer, modulation, modMatrix, sampleLib, sampleSet, effects, effectChain, effectProcessor, presetsCtx, preset, presetBank, setup, presetCodec } from "./crest-spec-phase-8.js";

// Phase 9: Gamepad UX polish + Steam Deck packaging
// Tighten navigation, full-screen layout, controller glyphs, distributable build.
// Adds GamepadInput port, adapters for all infrastructure ports, context map,
// and the full set of architectural invariants.

// ── Shell additions ──────────────────────────────────────
// Shell gains GamepadInput + GuiRenderer ports, GamepadAction + ControllerGlyph
// value objects, and GamepadNavigator + GlyphResolver domain services.

export const gamepadInput = shell.port("GamepadInput", {
  contract: {
    poll: "() -> Vec<GamepadEvent>",
    connectedControllers: "() -> Vec<ControllerId>",
    controllerType: "ControllerId -> ControllerType",
  },
});

export const guiRenderer = shell.port("GuiRenderer", {
  contract: {
    beginFrame: "() -> UiContext",
    endFrame: "UiContext -> ()",
    customPaint: "(Rect, PaintCallback) -> ()",
  },
});

shell.valueObject("GamepadAction", {
  from: "enum",
  description: "Navigate, Select, Back, TweakUp, TweakDown, AssignMod, NextPage, PreviousPage, QuickSave",
});

shell.valueObject("ControllerGlyph", {
  state: {
    button: "GamepadButton",
    controllerType: "ControllerType",
    glyphPath: "string",
  },
  description: "maps a logical button to the correct visual glyph for the connected controller brand",
});

shell.domainService("GamepadNavigator", {
  purpose: "translates raw gamepad events into GamepadActions and drives the cursor/edit model",
});

shell.domainService("GlyphResolver", {
  purpose: "resolves the correct controller glyph for each button based on the connected controller type",
});

// ── Adapters ────────────────────────────────────────────
// Concrete infrastructure implementations of all shell ports.

app.adapter("CpalAudioOutput", {
  implements: audioOutputPort,
  layer: "infrastructure",
  meta: { notes: "cpal: cross-platform audio output (ALSA/PipeWire on Linux, WASAPI, CoreAudio)" },
});

app.adapter("MidirInput", {
  implements: midiInputPort,
  layer: "infrastructure",
  meta: { notes: "midir: cross-platform MIDI I/O for raw MIDI 1.0 messages" },
});

app.adapter("Midi2Normalizer", {
  implements: midiNormalizerPort,
  layer: "infrastructure",
  meta: { notes: "midi2 crate: upconverts MIDI 1.0 to internal (group, channel) + high-res + note-id model" },
});

app.adapter("EframeWindow", {
  implements: appWindowPort,
  layer: "infrastructure",
  meta: { notes: "eframe: winit + wgpu window shell for egui" },
});

app.adapter("GilrsGamepad", {
  implements: gamepadInput,
  layer: "infrastructure",
  meta: { notes: "gilrs: cross-platform gamepad input with controller type detection" },
});

app.adapter("EguiRenderer", {
  implements: guiRenderer,
  layer: "infrastructure",
  meta: { notes: "egui: immediate-mode UI with custom painting for sound design widgets" },
});

app.adapter("FundspEffects", {
  implements: effectProcessor,
  layer: "infrastructure",
  meta: { notes: "fundsp: composable DSP nodes for reverb, chorus, delay" },
});

app.adapter("SerdePresetCodec", {
  implements: presetCodec,
  layer: "infrastructure",
  meta: { notes: "serde_json for human-readable presets, bincode for compact setup snapshots" },
});

// ── Context Map ─────────────────────────────────────────

app.contextMap([
  relationship("Shell", "Synth", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Shell", "Patch", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Patch", "Synth", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Patch", "Effects", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Modulation", "Synth", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Modulation", "Patch", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("SampleLibrary", "Synth", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("SampleLibrary", "RealTime", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Presets", "Patch", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Presets", "Modulation", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Presets", "Effects", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Kernel", "Synth", {
    kind: "shared-kernel",
  }),
  relationship("Kernel", "Patch", {
    kind: "shared-kernel",
  }),
  relationship("Kernel", "Modulation", {
    kind: "shared-kernel",
  }),
  relationship("RealTime", "Synth", {
    kind: "anti-corruption",
    direction: "upstream",
  }),
  relationship("RealTime", "Patch", {
    kind: "anti-corruption",
    direction: "upstream",
  }),
]);

// ── Invariants ──────────────────────────────────────────
// Accumulates all invariants from phase 8 plus new ones for phase 9.

app.invariants([
  invariant("audio thread must never allocate heap memory", {
    meta: { rationale: "any allocation risks missing the audio buffer deadline and causing a dropout" },
  }),
  invariant("audio thread must never acquire a mutex or blocking lock", {
    meta: { rationale: "lock contention causes unbounded latency on the real-time thread" },
  }),
  invariant("audio thread must never perform blocking I/O", {
    meta: { rationale: "file/network I/O has unpredictable latency incompatible with audio deadlines" },
  }),
  invariant("all parameter changes cross the boundary via ParameterBridge or EventRingBuffer", {
    meta: { rationale: "enforces the lock-free seam; no shared mutable state between threads" },
  }),
  invariant("retired memory from the audio thread is freed via DeferredDeallocator, never directly", {
    meta: { rationale: "basedrop ensures free() never runs on the audio thread" },
  }),
  invariant("each patch has an independent voice pool; one patch's polyphony cannot exhaust another's", {
    meta: { rationale: "a busy pad patch must not starve a bass patch of voices" },
  }),
  invariant("channel dispatch delivers events to all subscribed patches, not just the first match", {
    meta: { rationale: "two patches on the same channel layer automatically by design" },
  }),
  invariant("per-note expression (X, Y, Z) reaches the voice directly, never just the patch", {
    meta: { rationale: "voices must not assume expression is patch-level — that would block MPE later" },
  }),
  invariant("MPE expression dimensions (bend, timbre, pressure) exist as named per-voice mod sources from day one", {
    meta: { rationale: "building MPE later means feeding data into sources that already exist, not adding new types" },
  }),
  invariant("sample-set swaps are performed via Arc + DeferredDeallocator; the audio thread never loads or frees sample data", {
    meta: { rationale: "sample sets can be multi-megabyte; loading and freeing must happen off the audio thread" },
  }),
  invariant("effect chains process in slot order; signal flow is patch voices -> patch FX -> mix -> master FX -> output", {
    meta: { rationale: "deterministic signal routing; per-patch FX before the mix bus, master FX after" },
  }),
  invariant("preset serialization captures the complete patch state including modulation and effects", {
    meta: { rationale: "a loaded preset must reproduce the saved sound exactly" },
  }),
  invariant("setup save/load preserves the full session: all patches, subscriptions, mixer, and effect chains", {
    meta: { rationale: "restoring a setup must return the app to its exact prior state" },
  }),
  // New in phase 9:
  invariant("the engine library is host-agnostic; no audio driver, window, or controller code in the library", {
    meta: { rationale: "the standalone app and a future plugin wrapper are different shells over the same library" },
  }),
  invariant("the UI is a pure view over engine state; no audio logic lives in the GUI layer", {
    meta: { rationale: "keeps the hard part (DSP and voice logic) testable in isolation" },
  }),
  invariant("all gamepad navigation uses the app's own cursor/edit model, not egui's built-in focus", {
    meta: { rationale: "generic focus traversal doesn't fit a controller-first sound-design workflow" },
  }),
]);

// ── Module Assets ───────────────────────────────────────

app.asset("RootCargoToml", {
  kind: "cargo-manifest",
  description: "Root Cargo.toml for the crest-synth project",
  prompts: [
    "Package name: crest-synth, version 0.1.0",
    "Dependencies likely needed: cpal (audio output), midir (MIDI input), eframe/egui (GUI), gilrs (gamepad), fundsp (effects DSP), serde + serde_json (preset serialization)",
  ],
});

shell.asset("ShellMod", {
  kind: "rust-module-declaration",
  description: "src/Shell/mod.rs module declarations for Shell context",
  prompts: ["File path: src/Shell/mod.rs", "Declare modules for: AudioOutput, MidiInput, MidiNormalizer, AppWindow, GamepadInput, GuiRenderer, GamepadAction, ControllerGlyph, GamepadNavigator, GlyphResolver"],
});

app.asset("CpalAudioOutputAdapter", {
  kind: "rust-module-declaration",
  description: "CpalAudioOutput adapter: cpal cross-platform audio output",
  prompts: ["File path: src/adapters/cpal_audio_output.rs", "Implement AudioOutput port using cpal"],
});

app.asset("MidirInputAdapter", {
  kind: "rust-module-declaration",
  description: "MidirInput adapter: midir cross-platform MIDI I/O",
  prompts: ["File path: src/adapters/midir_input.rs", "Implement MidiInput port using midir"],
});

app.asset("Midi2NormalizerAdapter", {
  kind: "rust-module-declaration",
  description: "Midi2Normalizer adapter: MIDI 1.0 to internal model upconversion",
  prompts: ["File path: src/adapters/midi2_normalizer.rs", "Implement MidiNormalizer port using midi2 crate"],
});

app.asset("EframeWindowAdapter", {
  kind: "rust-module-declaration",
  description: "EframeWindow adapter: winit + wgpu window shell",
  prompts: ["File path: src/adapters/eframe_window.rs", "Implement AppWindow port using eframe"],
});

app.asset("GilrsGamepadAdapter", {
  kind: "rust-module-declaration",
  description: "GilrsGamepad adapter: cross-platform gamepad input",
  prompts: ["File path: src/adapters/gilrs_gamepad.rs", "Implement GamepadInput port using gilrs"],
});

app.asset("EguiRendererAdapter", {
  kind: "rust-module-declaration",
  description: "EguiRenderer adapter: immediate-mode UI renderer",
  prompts: ["File path: src/adapters/egui_renderer.rs", "Implement GuiRenderer port using egui"],
});

app.asset("FundspEffectsAdapter", {
  kind: "rust-module-declaration",
  description: "FundspEffects adapter: composable DSP effects",
  prompts: ["File path: src/adapters/fundsp_effects.rs", "Implement EffectProcessor port using fundsp"],
});

app.asset("SerdePresetCodecAdapter", {
  kind: "rust-module-declaration",
  description: "SerdePresetCodec adapter: JSON/bincode preset serialization",
  prompts: ["File path: src/adapters/serde_preset_codec.rs", "Implement PresetCodec port using serde_json and bincode"],
});
