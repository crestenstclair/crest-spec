package crestsynth

// Phase 10: Plugin wrapper (optional)
// nih-plug shell over the same engine library for CLAP/VST3.

project: contexts: Plugin: purpose: "plugin wrapper: exposes the engine library as CLAP/VST3 plugins via nih-plug"
project: contexts: Plugin: ubiquitousLanguage: {
	PluginHost:      "the DAW or host application that loads the plugin"
	PluginParameter: "an engine parameter exposed to the host for automation"
	PluginFormat:    "the wire format: CLAP or VST3, abstracted by nih-plug"
}

project: contexts: Plugin: valueObjects: PluginFormat: {from: "enum", description: "CLAP, VST3"}
project: contexts: Plugin: valueObjects: ParameterId:  {from: "u32", description: "stable numeric ID for a plugin parameter, used by the host for automation"}
project: contexts: Plugin: valueObjects: ParameterRange: {
	state:       {min: "f64", max: "f64", defaultValue: "f64", step: "Option<f64>"}
	description: "value range and default for a host-visible parameter"
	invariants: ["min < max", "defaultValue must be within [min, max]"]
}

project: contexts: Plugin: ports: PluginHost: {
	contract: {processBlock: "(AudioBuffer, MidiEvents) -> AudioBuffer", getParameter: "ParameterId -> f64", setParameter: "(ParameterId, f64) -> ()", saveState: "() -> Vec<u8>", loadState: "Vec<u8> -> Result<(), StateError>"}
	meta: notes: "nih-plug provides the Plugin trait; this port maps to its process(), params(), and state methods"
}

project: contexts: Plugin: aggregates: PluginInstance: {
	root:    true
	purpose: "wraps the engine library as a plugin: parameter mapping, state persistence, MIDI routing via host"
	state:   {format: "PluginFormat", parameters: "Vec<PluginParameter>", patchCount: "u8", sampleRate: "SampleRate"}
	commands: [
		{name: "Initialize", payload: {sampleRate: "SampleRate", maxBlockSize: "u32"}},
		{name: "Reset", payload: {}},
		{name: "SetParameter", payload: {id: "ParameterId", value: "f64"}},
	]
	events: [
		{name: "PluginInitialized", payload: {sampleRate: "SampleRate"}},
		{name: "PluginReset", payload: {}},
		{name: "ParameterChanged", payload: {id: "ParameterId", value: "f64"}},
	]
	invariants: [
		"plugin parameters map 1:1 to engine parameters",
		"state save/load uses the same PresetCodec as the standalone app",
		"MIDI events from the host are normalized through the same MidiNormalizer",
	]
	entities: PluginParameter: {state: {id: "ParameterId", name: "string", range: "ParameterRange", currentValue: "f64", engineMapping: "string"}}
}

project: contexts: Plugin: applicationServices: PluginShell: {purpose: "orchestrates plugin lifecycle: init, process, param sync, state persistence via the host"}

// ── Adapter ────────────────────────────────────────────

project: adapters: NihPlugHost: {implements: "port.Plugin.PluginHost", layer: "infrastructure", meta: notes: "nih-plug: Rust framework for CLAP/VST3 plugin development"}

// ── Context map additions ──────────────────────────────

project: contextMap: pluginToSynth:   {from: "Plugin", to: "Synth", kind: "customer-supplier", direction: "downstream"}
project: contextMap: pluginToPatch:   {from: "Plugin", to: "Patch", kind: "customer-supplier", direction: "downstream"}
project: contextMap: pluginToPresets: {from: "Plugin", to: "Presets", kind: "customer-supplier", direction: "downstream"}

// ── Invariants ─────────────────────────────────────────

project: invariants: pluginCompat: [
	{text: "plugin state save/load uses the same PresetCodec as the standalone for format compatibility", meta: rationale: "presets created in standalone should load in the plugin and vice versa"},
	{text: "plugin parameters have stable numeric IDs across versions for host automation compatibility", meta: rationale: "changing parameter IDs breaks saved automation in DAW projects"},
]
