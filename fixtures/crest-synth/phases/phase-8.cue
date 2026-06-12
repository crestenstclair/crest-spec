package crestsynth

// Phase 8: Presets
// serde save/load for patches and full setups; preset browser.

project: contexts: Presets: purpose: "persistence: save/load individual patches and full setups via serde"
project: contexts: Presets: ubiquitousLanguage: {
	Preset:     "a serialized snapshot of a single patch's complete state"
	PresetBank: "a named collection of presets, organized for browsing"
	Setup:      "the full app state: patch list, subscriptions, mixer, effects — everything to restore a session"
}

project: contexts: Presets: valueObjects: PresetId: {from: "string", description: "unique identifier for a preset (UUID or slug)", validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with PresetId"}]}
project: contexts: Presets: valueObjects: PresetMetadata: {
	state:       {name: "string", author: "string", category: "string", tags: "Vec<string>", createdAt: "string"}
	description: "metadata about a preset for browsing and search"
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with PresetMetadata"},
		{kind: "test", command: ["cargo", "test", "preset_metadata"], description: "PresetMetadata unit tests pass"},
	]
}

project: contexts: Presets: aggregates: Preset: {
	root:    true
	purpose: "a serialized snapshot of a single patch's complete sound and routing configuration"
	state: {
		id: "PresetId", metadata: "PresetMetadata", engineType: "EngineType",
		oscillator: "OscillatorConfig", filter: "FilterConfig", ampEnvelope: "AmpEnvelopeConfig",
		samplePlayer: "Option<SamplePlayerConfig>", modMatrix: "SerializedModMatrix", effectChain: "SerializedEffectChain",
	}
	commands: [
		{name: "SavePreset", payload: {patchId: "PatchId", metadata: "PresetMetadata"}},
		{name: "LoadPreset", payload: {presetId: "PresetId"}},
		{name: "DeletePreset", payload: {presetId: "PresetId"}},
		{name: "UpdateMetadata", payload: {presetId: "PresetId", metadata: "PresetMetadata"}},
	]
	events: [
		{name: "PresetSaved", payload: {id: "PresetId", name: "string"}},
		{name: "PresetLoaded", payload: {id: "PresetId", targetPatchId: "PatchId"}},
		{name: "PresetDeleted", payload: {id: "PresetId"}},
		{name: "PresetMetadataUpdated", payload: {id: "PresetId"}},
	]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with Preset"},
		{kind: "test", command: ["cargo", "test", "preset"], description: "Preset unit tests pass"},
	]
}

project: contexts: Presets: aggregates: PresetBank: {
	root:    true
	purpose: "a named collection of presets for organized browsing"
	state:   {name: "string", presetIds: "Vec<PresetId>", isFactory: "bool"}
	commands: [
		{name: "CreateBank", payload: {name: "string"}},
		{name: "AddPresetToBank", payload: {presetId: "PresetId"}},
		{name: "RemovePresetFromBank", payload: {presetId: "PresetId"}},
	]
	events: [
		{name: "BankCreated", payload: {name: "string"}},
		{name: "PresetAddedToBank", payload: {presetId: "PresetId"}},
		{name: "PresetRemovedFromBank", payload: {presetId: "PresetId"}},
	]
	invariants: ["factory banks are read-only; user cannot modify them"]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with PresetBank"},
		{kind: "test", command: ["cargo", "test", "preset_bank"], description: "PresetBank invariant tests pass"},
	]
}

project: contexts: Presets: aggregates: Setup: {
	root:    true
	purpose: "the full app state: patch list + subscriptions + mixer + effects — restored on load"
	state:   {name: "string", patches: "Vec<SerializedPatch>", masterGain: "Amplitude", masterEffectChain: "SerializedEffectChain"}
	commands: [{name: "SaveSetup", payload: {name: "string"}}, {name: "LoadSetup", payload: {path: "string"}}]
	events:   [{name: "SetupSaved", payload: {name: "string", patchCount: "u32"}}, {name: "SetupLoaded", payload: {name: "string", patchCount: "u32"}}]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with Setup"},
		{kind: "test", command: ["cargo", "test", "setup"], description: "Setup unit tests pass"},
	]
}

project: contexts: Presets: ports: PresetCodec: {
	contract: {serialize: "Preset -> Vec<u8>", deserialize: "Vec<u8> -> Result<Preset, CodecError>", serializeSetup: "Setup -> Vec<u8>", deserializeSetup: "Vec<u8> -> Result<Setup, CodecError>"}
	meta: notes: "serde with serde_json (human-readable) or bincode (compact binary)"
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with PresetCodec port"}]
}

project: contexts: Presets: applicationServices: PresetBrowser: {
	purpose: "lists, searches, and previews presets from all banks"
	uses: ["aggregate.Presets.Preset", "aggregate.Presets.PresetBank"]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with PresetBrowser"},
		{kind: "test", command: ["cargo", "test", "preset_browser"], description: "PresetBrowser unit tests pass"},
	]
}

project: contexts: Presets: repositories: PresetRepository: {
	of:       "aggregate.Presets.Preset"
	contract: {findById: "PresetId -> Option<Preset>", findByCategory: "string -> Vec<Preset>", search: "string -> Vec<Preset>", save: "Preset -> ()", listAll: "() -> Vec<Preset>"}
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with PresetRepository"},
		{kind: "test", command: ["cargo", "test", "preset_repository"], description: "PresetRepository unit tests pass"},
	]
}

// ── Preset round-trip made provable (the phase-8 behavior prover) ──────
// preset_demo proves the two presetIntegrity invariants MECHANICALLY: it builds
// a full Setup (several distinct patches + mixer + mod + effects), serializes it
// via the PresetCodec port, deserializes it back, asserts the reloaded Setup
// equals the original, and — the real proof of "reproduces the saved sound
// exactly" — renders the SAME demo passage through both the original and the
// reloaded Setup and asserts the two WAVs are BIT-IDENTICAL. The SerdePresetCodec
// adapter lands in phase 9; this phase provides the serde-backed codec inline so
// the round-trip is testable now.

project: assets: PresetRoundtripDemoMain: {
	kind:        "rust-bin-target"
	description: "src/bin/preset_demo.rs: serializes a full Setup, reloads it, and proves round-trip fidelity by rendering identical audio before/after"
	uses: ["asset.MidiFileLoader", "aggregate.Patch.Patch", "aggregate.Patch.GlobalMixer", "domainService.Patch.ChannelDispatcher", "domainService.Patch.PatchMixer", "aggregate.Presets.Preset", "aggregate.Presets.Setup", "port.Presets.PresetCodec"]
	prompts: [
		"File path: src/bin/preset_demo.rs",
		"CLI: `preset_demo [--out OUT.wav]`. Default output path preset-demo.wav.",
		"Build a full Setup: 2-3 distinct Patches (different OscillatorConfig/FilterConfig/AmpEnvelopeConfig, gain/pan, channel subscriptions) plus master gain. Each Patch's complete state must be captured as a Preset.",
		"Implement the PresetCodec port (port.Presets.PresetCodec) inline using serde + serde_json (derive Serialize/Deserialize on the serialized preset/setup value objects). serialize/deserialize a single Preset and serializeSetup/deserializeSetup for the whole Setup.",
		#"Round-trip the Setup: serializeSetup -> Vec<u8> -> deserializeSetup -> Setup'. Assert in code that Setup' EQUALS the original Setup (derive PartialEq; panic with a clear message on mismatch). Print a verbatim line `setup roundtrip: equal`."#,
		"Render a fixed built-in demo passage through the ORIGINAL Setup to an in-memory buffer, and the SAME passage through the RELOADED Setup' to a second buffer (same dispatcher -> per-patch pools -> PatchMixer -> GlobalMixer path, deterministic, fixed sample blocks).",
		#"Assert in code the two rendered buffers are BIT-IDENTICAL sample-for-sample (panic if any sample differs) — this is the real proof that the preset reproduces the saved sound exactly. Print a verbatim line `render identical: true`."#,
		"Write the (identical) rendered audio to 16-bit mono WAV (default preset-demo.wav, or --out) with a pure-Rust WAV writer.",
		"Print stats. The `setup roundtrip: equal` and `render identical: true` tokens MUST appear verbatim so a validation can assert both presetIntegrity invariants held.",
		"Exit 0 on success (both in-code assertions must pass).",
	]
	validations: [
		{kind: "compiles", command: ["make", "build"], description: "preset demo builds"},
		{kind: "integration", command: ["make", "demo-presets"], description: "Setup round-trips through the codec and re-renders bit-identical audio", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "file_exists", path: "preset-demo.wav"},
			{kind: "stdout_contains", pattern: "setup roundtrip: equal"},
			{kind: "stdout_contains", pattern: "render identical: true"},
		]},
	]
}

// ── Invariants ─────────────────────────────────────────

project: invariants: presetIntegrity: [
	{text: "preset serialization captures the complete patch state including modulation and effects", meta: rationale: "a loaded preset must reproduce the saved sound exactly"},
	{text: "setup save/load preserves the full session: all patches, subscriptions, mixer, and effect chains", meta: rationale: "restoring a setup must return the app to its exact prior state"},
]
