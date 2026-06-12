package crestsynth

// Override (phase 4): adds the patch_play [[bin]] (the integration prover).
// Picked up by run-phased-agent.sh (highest-numbered phase-N.override-
// RootCargoToml.cue with N <= target phase). REPLACES the phase-3 override, so
// the FULL cumulative dependency + bin list is enumerated here. No new crates:
// patch routing/mixing is all in-crate.

project: assets: RootCargoToml: {
	kind:        "cargo-manifest"
	description: "Root Cargo.toml for the crest-synth project"
	prompts: [
		"Package name: crest-synth, version 0.1.0",
		#"Include [[bin]] section: name = "crest-synth", path = "src/main.rs""#,
		#"Include [[bin]] section: name = "midi_play", path = "src/bin/midi_play.rs""#,
		#"Include [[bin]] section: name = "voice_demo", path = "src/bin/voice_demo.rs""#,
		#"Include [[bin]] section: name = "midi_play_live", path = "src/bin/midi_play_live.rs""#,
		#"Include [[bin]] section: name = "patch_play", path = "src/bin/patch_play.rs""#,
		"Dependencies: `midly` (0.5.x) for SMF parsing; `cpal` for audio output; the lock-free seam crates `rtrb`, `triple_buffer`, and `basedrop`.",
		"Only include dependencies actually needed by the generated code (WAV writing remains pure-Rust, no crate).",
	]
}
