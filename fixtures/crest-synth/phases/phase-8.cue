package crestsynth

// Phase 8: Presets
// serde save/load for patches and full setups; preset browser.

project: contexts: Presets: purpose: "persistence: save/load individual patches and full setups via serde"
project: contexts: Presets: ubiquitousLanguage: {
	Preset:     "a serialized snapshot of a single patch's complete state"
	PresetBank: "a named collection of presets, organized for browsing"
	Setup:      "the full app state: patch list, subscriptions, mixer, effects — everything to restore a session"
}

project: contexts: Presets: valueObjects: PresetId: {from: "string", description: "unique identifier for a preset (UUID or slug)"}
project: contexts: Presets: valueObjects: PresetMetadata: {
	state:       {name: "string", author: "string", category: "string", tags: "Vec<string>", createdAt: "string"}
	description: "metadata about a preset for browsing and search"
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
}

project: contexts: Presets: aggregates: Setup: {
	root:    true
	purpose: "the full app state: patch list + subscriptions + mixer + effects — restored on load"
	state:   {name: "string", patches: "Vec<SerializedPatch>", masterGain: "Amplitude", masterEffectChain: "SerializedEffectChain"}
	commands: [{name: "SaveSetup", payload: {name: "string"}}, {name: "LoadSetup", payload: {path: "string"}}]
	events:   [{name: "SetupSaved", payload: {name: "string", patchCount: "u32"}}, {name: "SetupLoaded", payload: {name: "string", patchCount: "u32"}}]
}

project: contexts: Presets: ports: PresetCodec: {
	contract: {serialize: "Preset -> Vec<u8>", deserialize: "Vec<u8> -> Result<Preset, CodecError>", serializeSetup: "Setup -> Vec<u8>", deserializeSetup: "Vec<u8> -> Result<Setup, CodecError>"}
	meta: notes: "serde with serde_json (human-readable) or bincode (compact binary)"
}

project: contexts: Presets: applicationServices: PresetBrowser: {purpose: "lists, searches, and previews presets from all banks", uses: ["aggregate.Presets.Preset", "aggregate.Presets.PresetBank"]}

project: contexts: Presets: repositories: PresetRepository: {
	of:       "aggregate.Presets.Preset"
	contract: {findById: "PresetId -> Option<Preset>", findByCategory: "string -> Vec<Preset>", search: "string -> Vec<Preset>", save: "Preset -> ()", listAll: "() -> Vec<Preset>"}
}

// ── Invariants ─────────────────────────────────────────

project: invariants: presetIntegrity: [
	{text: "preset serialization captures the complete patch state including modulation and effects", meta: rationale: "a loaded preset must reproduce the saved sound exactly"},
	{text: "setup save/load preserves the full session: all patches, subscriptions, mixer, and effect chains", meta: rationale: "restoring a setup must return the app to its exact prior state"},
]
