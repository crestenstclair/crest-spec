package crestsynth

// Preset — persistence. Presets snapshot one patch; banks collect presets;
// sessions snapshot everything.

project: contexts: Preset: purpose: "save and restore: presets (one patch's complete config), banks (ordered preset collections), and sessions (a snapshot of all state)"

project: contexts: Preset: valueObjects: {
	PresetMetadata: {
		state: {name: "string", author: "string", tags: "list<string>", category: "string", description: "string"}
		description: "searchable metadata carried by every preset"
	}
	Preset: {
		state: {id: "PresetId", meta: "PresetMetadata", version: "u32"}
		description: "a versioned snapshot of one patch's complete configuration (voice, sample, mod, mixer, sends, inserts)"
	}
}

project: contexts: Preset: aggregates: Bank: {
	root:    true
	purpose: "an ordered collection of presets, like a soundfont bank or user folder"
	state: {id: "BankId", presets: "list<PresetId>", readOnly: "bool"}
	commands: {
		AddPreset: {preset: "PresetId"}
		RemovePreset: {preset: "PresetId"}
	}
	events: {
		PresetAdded: {preset: "PresetId"}
		PresetRemoved: {preset: "PresetId"}
	}
	invariants: [
		"a bank never contains the same preset twice",
		"a read-only bank rejects all mutating commands",
	]
}

project: contexts: Preset: aggregates: Session: {
	root:    true
	purpose: "a snapshot of everything: all patches, mixer state, aux buses, master bus, tempo, and time signature"
	state: {tempo: "Tempo", timeSignature: "TimeSignature"}
	commands: {
		Restore: {}
	}
	events: {
		Restored: {}
	}
	invariants: [
		"restoring a session replaces all state atomically — a failed restore leaves prior state untouched",
	]
}

project: contexts: Preset: ports: {
	PresetCodec: {
		contract: {
			encode: "(preset: Preset) -> result<bytes, CodecError>"
			decode: "(data: bytes) -> result<Preset, CodecError>"
		}
	}
	PresetStorage: {
		contract: {
			save: "(preset: Preset) -> result<(), StorageError>"
			load: "(id: PresetId) -> result<Preset, StorageError>"
			list: "() -> list<PresetMetadata>"
			delete: "(id: PresetId) -> result<(), StorageError>"
		}
	}
}

project: contexts: Preset: applicationServices: {
	PresetBrowser: {
		purpose: "search and filter presets, preview with a test note, load into a patch, save from a patch, import SF2, export a bank"
		uses: ["aggregate.Preset.Bank", "port.Preset.PresetCodec", "port.Preset.PresetStorage"]
	}
	SessionManager: {
		purpose: "save, load, list, and delete full sessions"
		uses: ["aggregate.Preset.Session", "port.Preset.PresetStorage"]
	}
}

project: adapters: SerdePresetCodec: {
	implements: "port.Preset.PresetCodec"
	layer:      "infrastructure"
	meta: framework: "serde"
}

project: adapters: FsPresetStorage: {
	implements: "port.Preset.PresetStorage"
	layer:      "infrastructure"
}
