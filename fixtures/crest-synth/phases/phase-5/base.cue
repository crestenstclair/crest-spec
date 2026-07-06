package crestsynth

// Foundation — loaded alongside phase-N.cue files.
// To evaluate phase 3: load base.cue + phase-1.cue + phase-2.cue + phase-3.cue

// ── Project config ─────────────────────────────────────

project: name: "crest-synth"
project: layers: ["domain", "application", "infrastructure"]
project: layerRules: domain:         {dependsOn: []}
project: layerRules: application:    {dependsOn: ["domain"]}
project: layerRules: infrastructure: {dependsOn: ["domain", "application"]}

project: meta: {
	language: "rust"
	style:    "idiomatic Rust; lock-free audio thread; gamepad-driven UI"
	avoid: ["heap allocation on audio thread", "mutex locks on audio thread", "blocking I/O on audio thread"]
}

// ── Default whole-crate validations (run at wave verification) ──
project: validations: [
	{kind: "compiles", command: ["cargo", "fmt", "--", "--check"], description: "rustfmt clean"},
	{kind: "compiles", command: ["cargo", "clippy", "--all-targets", "--", "-D", "warnings"], description: "clippy clean (incl. tests/bins)"},
	{kind: "compiles", command: ["cargo", "build"], description: "crate builds"},
	{kind: "test", command: ["cargo", "test"], description: "tests pass"},
]

// ── Kernel ─────────────────────────────────────────────

project: contexts: Kernel: purpose: "shared value types for MIDI addressing, audio primitives, and note identity"
project: contexts: Kernel: ubiquitousLanguage: {
	MidiEvent:      "normalized internal event addressed by (group, channel) with high-res values and note-id"
	NoteId:         "unique identifier for a sounding note, enabling per-note expression"
	ChannelAddress: "a (group, channel) pair — 256 addressable destinations"
}

project: contexts: Kernel: valueObjects: MidiGroup:   {from: "u8", description: "MIDI 2.0 group index (0-15)", invariants: ["must be 0-15"]}
project: contexts: Kernel: valueObjects: MidiChannel: {from: "u8", description: "MIDI channel (0-15 within a group)", invariants: ["must be 0-15"]}
project: contexts: Kernel: valueObjects: NoteId:      {from: "u32", description: "unique identifier for a sounding note"}
project: contexts: Kernel: valueObjects: NoteNumber:  {from: "u8", description: "MIDI note number (0-127)", invariants: ["must be 0-127"]}
project: contexts: Kernel: valueObjects: Velocity:    {from: "f64", description: "normalized note velocity (0.0-1.0)", invariants: ["must be 0.0-1.0"]}
project: contexts: Kernel: valueObjects: SampleRate:  {from: "u32", description: "audio sample rate in Hz", invariants: ["must be positive"]}
project: contexts: Kernel: valueObjects: AudioFrame:  {state: {left: "f32", right: "f32"}, description: "one stereo sample pair"}
project: contexts: Kernel: valueObjects: MidiEvent: {
	description: "normalized internal event: (group, channel) addressed, high-res values, note-id tagged"
	state: {
		group: "MidiGroup", channel: "MidiChannel", noteId: "NoteId",
		kind: "MidiEventKind", noteNumber: "NoteNumber", velocity: "Velocity", value: "f64",
	}
}

// ── Shell ──────────────────────────────────────────────

project: contexts: Shell: purpose: "application shell: wires audio output, MIDI input, and the window to the engine"

project: contexts: Shell: ports: AudioOutput: contract:    {openStream: "SampleRate -> AudioStream", writeBuffer: "[AudioFrame] -> ()", availableFrames: "() -> usize"}
project: contexts: Shell: ports: MidiInput: contract:      {listPorts: "() -> Vec<MidiPortInfo>", connect: "MidiPortId -> MidiConnection", nextEvent: "() -> Option<RawMidiMessage>"}
project: contexts: Shell: ports: MidiNormalizer: contract:  {normalize: "RawMidiMessage -> MidiEvent"}
project: contexts: Shell: ports: AppWindow: contract:       {create: "WindowConfig -> Window", runLoop: "FrameCallback -> ()"}

// ── Asset kinds ────────────────────────────────────────

project: assetKinds: "cargo-manifest": {
	description: "Rust Cargo.toml project manifest"
	filePattern: "Cargo.toml"
	prompts: ["Use edition 2021", "Only include dependencies actually needed by the generated code", #"Include [lib] section with path = "src/lib.rs""#]
}
project: assetKinds: makefile:                  {description: "GNU Makefile for build automation", filePattern: "Makefile", prompts: ["Include targets: build, test, clean, check, run", "Use cargo for all Rust operations"]}
project: assetKinds: "rust-binary":             {description: "Rust main.rs binary entry point", filePattern: "src/main.rs", prompts: ["Must compile and execute with `cargo run`", "Use only types from the crate's own lib"]}
project: assetKinds: "rust-module-declaration": {description: "Rust mod.rs or lib.rs module declaration file", prompts: ["Only output module declarations (pub mod) and re-exports", "Do not add any implementation code"]}
project: assetKinds: "rust-adapter":            {description: "Rust infrastructure adapter implementing a port trait", prompts: ["Implement the port trait using the specified crate", "Include proper error handling and resource cleanup"]}

// ── Stable project assets ──────────────────────────────
// Module declarations (lib.rs, mod.rs) are derived by the engine from the registry.

// RootCargoToml and BuildMakefile carry per-phase `prompts` lists (deps and
// make targets grow phase to phase). CUE cannot unify two concrete lists of
// different lengths, so their `prompts` live in phase-N.override-<Asset>.cue
// files (the harness copies the highest override N <= target phase). base.cue
// declares only the stable kind/description; every phase 1..N ships an override
// that supplies the prompts. See phase-1.override-{RootCargoToml,BuildMakefile}.cue.
project: assets: RootCargoToml: {
	kind:        "cargo-manifest"
	description: "Root Cargo.toml for the crest-synth project"
}
project: assets: BuildMakefile: {
	kind:        "makefile"
	description: "Build automation for the crest-synth project"
}
project: assets: ToneTestMain: {
	kind:        "rust-binary"
	description: "src/main.rs: tone test exercising the synth engine"
	prompts: [
		"File path: src/main.rs",
		"Play a 3-second C4-E4-G4 arpeggio (notes at 0.0s, 0.5s, 1.0s; each ~0.4s duration)",
		"Render in 256-sample blocks, triggering note_on/note_off at the correct sample offsets",
		"Write output to tone-test.wav using a pure-Rust WAV writer (no external crates)",
	]
}
