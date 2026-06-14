package crestsynth

// Override (phase 2): adds the voice_demo [[bin]] (the voice-stealing prover).
// Picked up by run-phased-agent.sh (highest-numbered phase-N.override-
// RootCargoToml.cue with N <= target phase). REPLACES the phase-1 override, so
// the FULL cumulative dependency + bin list is enumerated here. No new crates:
// the polyphonic engine and voice stealing are all in-crate (midly carried
// forward for the midi_play spine; WAV writing remains pure-Rust).

project: assets: RootCargoToml: {
	kind:        "cargo-manifest"
	description: "Root Cargo.toml for the crest-synth project"
	prompts: [
		"Package name: crest-synth, version 0.1.0",
		#"Include [[bin]] section: name = "crest-synth", path = "src/main.rs""#,
		#"Include [[bin]] section: name = "midi_play", path = "src/bin/midi_play.rs""#,
		#"Include [[bin]] section: name = "voice_demo", path = "src/bin/voice_demo.rs""#,
		"Dependencies: `midly` (0.5.x) for Standard MIDI File parsing.",
		"Only include dependencies actually needed by the generated code; at this phase that is `midly` (the synth engine, voice allocator, and WAV writing are all pure-Rust, no extra crate).",
	]
}
