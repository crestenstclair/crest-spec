package crestsynth

// Phase 6: Sample playback
// SF2/WAV loading, key/velocity zones, interpolation.
// SampleLibrary context manages sample data; Synth gains a SamplePlayer engine type.

// ── Synth addition ─────────────────────────────────────

project: contexts: Synth: valueObjects: SamplePlayerConfig: {
	state:       {sampleSetId: "SampleSetId", interpolation: "InterpolationMode", loopMode: "LoopMode"}
	description: "sample player engine config: which sample set, interpolation quality, loop behavior"
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with SamplePlayerConfig"},
		{kind: "test", command: ["cargo", "test", "sample_player_config"], description: "SamplePlayerConfig unit tests pass"},
	]
}

// ── SampleLibrary context ──────────────────────────────

project: contexts: SampleLibrary: purpose: "sample data management: loading, organizing, and serving sample sets to the engine"
project: contexts: SampleLibrary: ubiquitousLanguage: {
	SampleSet:  "a loaded collection of samples mapped by key/velocity zones"
	SampleZone: "a region of the keyboard + velocity range mapped to a specific sample"
	SampleData: "raw audio sample data (f32 frames) held in memory, swapped via basedrop"
}

project: contexts: SampleLibrary: valueObjects: SampleSetId:        {from: "u32", description: "unique identifier for a loaded sample set", validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with SampleSetId"}]}
project: contexts: SampleLibrary: valueObjects: InterpolationMode:  {from: "enum", description: "sample interpolation quality: Nearest, Linear, Cubic, Sinc", validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with InterpolationMode"}]}
project: contexts: SampleLibrary: valueObjects: SampleMetadata: {
	state:       {sampleRate: "SampleRate", channels: "u8", lengthFrames: "u64", loopStart: "Option<u64>", loopEnd: "Option<u64>", rootNote: "NoteNumber"}
	description: "metadata about a single sample"
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with SampleMetadata"},
		{kind: "test", command: ["cargo", "test", "sample_metadata"], description: "SampleMetadata unit tests pass"},
	]
}
project: contexts: SampleLibrary: valueObjects: KeyVelocityRange: {
	state:       {keyLow: "NoteNumber", keyHigh: "NoteNumber", velocityLow: "Velocity", velocityHigh: "Velocity"}
	description: "the note and velocity range a sample zone responds to"
	invariants: ["keyLow <= keyHigh", "velocityLow <= velocityHigh"]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with KeyVelocityRange"},
		{kind: "test", command: ["cargo", "test", "key_velocity_range"], description: "KeyVelocityRange invariant tests pass"},
	]
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
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with SampleSet"},
		{kind: "test", command: ["cargo", "test", "sample_set"], description: "SampleSet unit tests pass"},
	]
}

project: contexts: SampleLibrary: applicationServices: SampleLoader: {
	purpose: "decodes sample files (SF2, WAV) from disk into SampleSet aggregates"
	uses: ["aggregate.SampleLibrary.SampleSet"]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with SampleLoader"},
		{kind: "test", command: ["cargo", "test", "sample_loader"], description: "SampleLoader unit tests pass"},
	]
}
project: contexts: SampleLibrary: domainServices: SampleInterpolator: {
	purpose: "reads sample data with pitch-shifted interpolation (linear, cubic, sinc)"
	uses: ["aggregate.SampleLibrary.SampleSet"]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with SampleInterpolator"},
		{kind: "test", command: ["cargo", "test", "sample_interpolator"], description: "SampleInterpolator unit tests pass"},
	]
}

project: contexts: SampleLibrary: repositories: SampleSetRepository: {
	of:       "aggregate.SampleLibrary.SampleSet"
	contract: {findById: "SampleSetId -> Option<SampleSet>", save: "SampleSet -> ()", listAll: "() -> Vec<SampleSet>"}
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with SampleSetRepository"},
		{kind: "test", command: ["cargo", "test", "sample_set_repository"], description: "SampleSetRepository unit tests pass"},
	]
}

// ── Sample playback made audible (the phase-6 behavior prover) ─────────
// sample_demo proves the SampleLibrary end to end: it SYNTHESIZES its own tiny
// WAV sample at startup (so no sample file ships in the repo), loads it through
// the SampleLoader into a SampleSet with multiple key/velocity zones, looks up
// zones by (key, velocity) via the engine's SamplePlayer path, reads them back
// pitch-shifted through the SampleInterpolator, and renders a short passage to
// WAV. This is what mechanically proves "loading + zone lookup + interpolation"
// rather than merely declaring the SampleLibrary context.

project: assets: SamplePlayDemoMain: {
	kind:        "rust-bin-target"
	description: "src/bin/sample_demo.rs: hermetic SampleLibrary prover — synthesizes a sample, loads it, maps key/velocity zones, interpolates, renders to WAV"
	uses: ["asset.MidiFileLoader", "aggregate.SampleLibrary.SampleSet", "applicationService.SampleLibrary.SampleLoader", "domainService.SampleLibrary.SampleInterpolator"]
	prompts: [
		"File path: src/bin/sample_demo.rs",
		"CLI: `sample_demo [--out OUT.wav]`. Default output path sample-demo.wav.",
		"HERMETIC: at startup, SYNTHESIZE a tiny mono 16-bit WAV sample in code (e.g. a short decaying sine ~0.3s at a known root note) and write it to a TEMP file (std::env::temp_dir() + a unique name). No sample/SF2 file may ship in the repo. Clean the temp file up at the end.",
		"Load that temp WAV through the SampleLoader (applicationService.SampleLibrary.SampleLoader) into a SampleSet aggregate (LoadSampleSet). Build a SampleSet with at least TWO non-overlapping zones differing in KeyVelocityRange (e.g. a low-key zone and a high-key zone, or two velocity layers) sharing the synthesized SampleData via Arc.",
		"Drive a short built-in passage of note-ons at DIFFERENT (note, velocity) pairs chosen so they land in DIFFERENT zones; for each note, look up the matching SampleZone by key+velocity, then read the sample pitch-shifted to the note's frequency through the SampleInterpolator (use Linear interpolation at minimum). Mix the rendered output in fixed sample blocks.",
		"Write 16-bit mono WAV (default sample-demo.wav, or --out) with a pure-Rust WAV writer.",
		#"Print verbatim behavior markers: a line `zones loaded=N` with the zone count, and for each played note a line containing the token `zone hit:` naming which zone matched the (key, velocity) lookup (e.g. `zone hit: low-key (note=48 vel=0.3)`). Both tokens must appear verbatim so a validation can assert that zone loading and key/velocity lookup actually ran."#,
		"Exit 0 on success.",
	]
	validations: [
		{kind: "compiles", command: ["make", "build"], description: "sample demo builds"},
		{kind: "integration", command: ["make", "demo-samples"], description: "synthesized sample loads, zones resolve by key/velocity, interpolated render to WAV", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "file_exists", path: "sample-demo.wav"},
			{kind: "stdout_contains", pattern: "zones loaded="},
			{kind: "stdout_contains", pattern: "zone hit:"},
		]},
	]
}

// ── Invariants ─────────────────────────────────────────

project: invariants: samplePlayback: [
	{text: "sample-set swaps via Arc + DeferredDeallocator; audio thread never loads or frees sample data", meta: rationale: "sample sets can be multi-megabyte; loading/freeing must happen off the audio thread"},
]
