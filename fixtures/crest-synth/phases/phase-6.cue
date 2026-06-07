package crestsynth

// Phase 6: Sample playback
// SF2/WAV loading, key/velocity zones, interpolation.
// SampleLibrary context manages sample data; Synth gains a SamplePlayer engine type.

// ── Synth addition ─────────────────────────────────────

project: contexts: Synth: valueObjects: SamplePlayerConfig: {
	state:       {sampleSetId: "SampleSetId", interpolation: "InterpolationMode", loopMode: "LoopMode"}
	description: "sample player engine config: which sample set, interpolation quality, loop behavior"
}

// ── SampleLibrary context ──────────────────────────────

project: contexts: SampleLibrary: purpose: "sample data management: loading, organizing, and serving sample sets to the engine"
project: contexts: SampleLibrary: ubiquitousLanguage: {
	SampleSet:  "a loaded collection of samples mapped by key/velocity zones"
	SampleZone: "a region of the keyboard + velocity range mapped to a specific sample"
	SampleData: "raw audio sample data (f32 frames) held in memory, swapped via basedrop"
}

project: contexts: SampleLibrary: valueObjects: SampleSetId:        {from: "u32", description: "unique identifier for a loaded sample set"}
project: contexts: SampleLibrary: valueObjects: InterpolationMode:  {from: "enum", description: "sample interpolation quality: Nearest, Linear, Cubic, Sinc"}
project: contexts: SampleLibrary: valueObjects: SampleMetadata: {
	state:       {sampleRate: "SampleRate", channels: "u8", lengthFrames: "u64", loopStart: "Option<u64>", loopEnd: "Option<u64>", rootNote: "NoteNumber"}
	description: "metadata about a single sample"
}
project: contexts: SampleLibrary: valueObjects: KeyVelocityRange: {
	state:       {keyLow: "NoteNumber", keyHigh: "NoteNumber", velocityLow: "Velocity", velocityHigh: "Velocity"}
	description: "the note and velocity range a sample zone responds to"
	invariants: ["keyLow <= keyHigh", "velocityLow <= velocityHigh"]
}

project: contexts: SampleLibrary: aggregates: SampleSet: {
	root:    true
	purpose: "a loaded collection of samples mapped to key/velocity zones"
	state:   {id: "SampleSetId", name: "string", zones: "Vec<SampleZone>", format: "SampleFormat"}
	commands: [
		{name: "LoadSampleSet", payload: {path: "string", format: "SampleFormat"}},
		{name: "UnloadSampleSet", payload: {id: "SampleSetId"}},
	]
	events: [
		{name: "SampleSetLoaded", payload: {id: "SampleSetId", name: "string", zoneCount: "u32"}},
		{name: "SampleSetUnloaded", payload: {id: "SampleSetId"}},
	]
	invariants: [
		"zones must not have overlapping key+velocity ranges within the same set",
		"sample data held via Arc; audio thread reads via shared reference",
		"unloading retires the Arc through DeferredDeallocator, never frees on audio thread",
	]
	entities: SampleZone: {state: {range: "KeyVelocityRange", metadata: "SampleMetadata", sampleDataRef: "Arc<[f32]>"}}
}

project: contexts: SampleLibrary: applicationServices: SampleLoader:   {purpose: "decodes sample files (SF2, WAV) from disk into SampleSet aggregates", uses: ["aggregate.SampleLibrary.SampleSet"]}
project: contexts: SampleLibrary: domainServices: SampleInterpolator:  {purpose: "reads sample data with pitch-shifted interpolation (linear, cubic, sinc)", uses: ["aggregate.SampleLibrary.SampleSet"]}

project: contexts: SampleLibrary: repositories: SampleSetRepository: {
	of:       "aggregate.SampleLibrary.SampleSet"
	contract: {findById: "SampleSetId -> Option<SampleSet>", save: "SampleSet -> ()", listAll: "() -> Vec<SampleSet>"}
}

// ── Invariants ─────────────────────────────────────────

project: invariants: samplePlayback: [
	{text: "sample-set swaps via Arc + DeferredDeallocator; audio thread never loads or frees sample data", meta: rationale: "sample sets can be multi-megabyte; loading/freeing must happen off the audio thread"},
]
