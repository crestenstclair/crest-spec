import { command, event, invariant, relationship } from "../../../src/index.js";
import { app, kernel, shell, synth, voice, realtime, patchCtx, patchAggregate, globalMixer, modulation, modMatrix, sampleLib, sampleSet, effects, effectChain, effectProcessor, presetsCtx, preset, presetBank, setup, presetCodec, gamepadInput, guiRenderer } from "./crest-spec-phase-9.js";

// Phase 10: (Optional) Plugin wrapper
// A nih-plug shell over the same engine library for CLAP/VST3 versions.
// The engine library is host-agnostic; this phase adds a Plugin context
// that wraps it for plugin host environments.

// ── Plugin ──────────────────────────────────────────────
// A nih-plug shell over the same engine library for CLAP/VST3 versions.
// The Plugin context wraps the host-agnostic engine into plugin format conventions:
// parameter exposition, state save/restore via the host, and MIDI routing
// through the host's event bus instead of midir.

export const plugin = app.context("Plugin", {
  purpose: "plugin wrapper: exposes the engine library as CLAP/VST3 plugins via nih-plug",
  ubiquitousLanguage: {
    "PluginHost": "the DAW or host application that loads the plugin and provides audio/MIDI I/O",
    "PluginParameter": "an engine parameter exposed to the host for automation and UI binding",
    "PluginFormat": "the wire format: CLAP or VST3, abstracted by nih-plug",
  },
});

plugin.valueObject("PluginFormat", {
  from: "enum",
  description: "CLAP, VST3 — which plugin format the binary is built for",
});

plugin.valueObject("ParameterId", {
  from: "u32",
  description: "stable numeric ID for a plugin parameter, used by the host for automation",
});

plugin.valueObject("ParameterRange", {
  state: {
    min: "f64",
    max: "f64",
    defaultValue: "f64",
    step: "Option<f64>",
  },
  description: "the value range and default for a host-visible parameter",
  invariants: ["min < max", "defaultValue must be within [min, max]"],
});

export const pluginHost = plugin.port("PluginHost", {
  contract: {
    processBlock: "(AudioBuffer, MidiEvents) -> AudioBuffer",
    getParameter: "ParameterId -> f64",
    setParameter: "(ParameterId, f64) -> ()",
    saveState: "() -> Vec<u8>",
    loadState: "Vec<u8> -> Result<(), StateError>",
  },
  meta: {
    notes: "nih-plug provides the Plugin trait; this port maps to its process(), params(), and state methods",
  },
});

plugin.aggregate("PluginInstance", {
  root: true,
  purpose: "wraps the engine library as a plugin: parameter mapping, state persistence, MIDI routing via host",
  state: {
    format: "PluginFormat",
    parameters: "Vec<PluginParameter>",
    patchCount: "u8",
    sampleRate: "SampleRate",
  },
  commands: [
    command("Initialize", { sampleRate: "SampleRate", maxBlockSize: "u32" }),
    command("Reset", {}),
    command("SetParameter", { id: "ParameterId", value: "f64" }),
  ],
  events: [
    event("PluginInitialized", { sampleRate: "SampleRate" }),
    event("PluginReset", {}),
    event("ParameterChanged", { id: "ParameterId", value: "f64" }),
  ],
  invariants: [
    "plugin parameters map 1:1 to engine parameters; no parameter exists without a backing engine param",
    "state save/load uses the same PresetCodec as the standalone app for format compatibility",
    "MIDI events from the host are normalized through the same MidiNormalizer as the standalone app",
  ],
});

plugin.entity("PluginParameter", {
  state: {
    id: "ParameterId",
    name: "string",
    range: "ParameterRange",
    currentValue: "f64",
    engineMapping: "string",
  },
});

plugin.applicationService("PluginShell", {
  purpose: "orchestrates plugin lifecycle: init, process, param sync, and state persistence via the host",
});

// ── Adapter ──────────────────────────────────────────────

app.adapter("NihPlugHost", {
  implements: pluginHost,
  layer: "infrastructure",
  meta: { notes: "nih-plug: Rust framework for CLAP/VST3 plugin development; wraps the engine as a plugin" },
});

// ── Context Map ──────────────────────────────────────────
// Full context map: all phase 9 relationships plus Plugin additions.

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
  // New in phase 10:
  relationship("Plugin", "Synth", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Plugin", "Patch", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
  relationship("Plugin", "Presets", {
    kind: "customer-supplier",
    direction: "downstream",
  }),
]);

// ── Invariants ──────────────────────────────────────────
// Accumulates all invariants from phase 9 plus new ones for phase 10.

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
  invariant("the engine library is host-agnostic; no audio driver, window, or controller code in the library", {
    meta: { rationale: "the standalone app and the plugin wrapper are different shells over the same library" },
  }),
  invariant("the UI is a pure view over engine state; no audio logic lives in the GUI layer", {
    meta: { rationale: "keeps the hard part (DSP and voice logic) testable in isolation" },
  }),
  invariant("all gamepad navigation uses the app's own cursor/edit model, not egui's built-in focus", {
    meta: { rationale: "generic focus traversal doesn't fit a controller-first sound-design workflow" },
  }),
  // New in phase 10:
  invariant("plugin state save/load uses the same PresetCodec as the standalone for format compatibility", {
    meta: { rationale: "presets created in the standalone app should load in the plugin and vice versa" },
  }),
  invariant("plugin parameters have stable numeric IDs across versions for host automation compatibility", {
    meta: { rationale: "changing parameter IDs breaks saved automation in DAW projects" },
  }),
]);

// ── Module Assets ───────────────────────────────────────

app.asset("RootCargoToml", {
  kind: "cargo-manifest",
  description: "Root Cargo.toml for the crest-synth project",
  prompts: [
    "Package name: crest-synth, version 0.1.0",
    "Dependencies likely needed: cpal (audio output), midir (MIDI input), eframe/egui (GUI), gilrs (gamepad), fundsp (effects DSP), serde + serde_json (preset serialization), nih-plug (plugin hosting framework)",
  ],
});

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: ["File path: src/lib.rs", "Declare modules: kernel, Shell, Synth, RealTime, Patch, Modulation, SampleLibrary, Effects, Presets, Plugin"],
});

app.asset("NihPlugHostAdapter", {
  kind: "rust-module-declaration",
  description: "NihPlugHost adapter: CLAP/VST3 plugin framework",
  prompts: ["File path: src/adapters/nih_plug_host.rs", "Implement PluginHost port using nih-plug"],
});

plugin.asset("PluginMod", {
  kind: "rust-module-declaration",
  description: "src/Plugin/mod.rs module declarations for Plugin context",
  prompts: ["File path: src/Plugin/mod.rs", "Declare modules for: PluginFormat, ParameterId, ParameterRange, PluginHost, PluginInstance, PluginParameter, PluginShell"],
});
