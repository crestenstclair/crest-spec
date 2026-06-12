package crestsynth

// Override (phase 3): the live MIDI player adds a third [[bin]] (midi_play_live)
// and phase 3 introduces the real-time/audio-device crates (cpal, plus the
// lock-free seam crates). Picked up by run-phased-agent.sh, which copies the
// highest-numbered phase-N.override-RootCargoToml.cue (N <= target phase). This
// REPLACES the phase-1 override, so the FULL cumulative dependency + bin list
// is enumerated here (midly is carried forward).

project: assets: RootCargoToml: {
	kind:        "cargo-manifest"
	description: "Root Cargo.toml for the crest-synth project"
	prompts: [
		"Package name: crest-synth, version 0.1.0",
		#"Include [[bin]] section: name = "crest-synth", path = "src/main.rs""#,
		#"Include [[bin]] section: name = "midi_play", path = "src/bin/midi_play.rs""#,
		#"Include [[bin]] section: name = "voice_demo", path = "src/bin/voice_demo.rs""#,
		#"Include [[bin]] section: name = "midi_play_live", path = "src/bin/midi_play_live.rs""#,
		"Dependencies: `midly` (0.5.x) for SMF parsing; `cpal` for cross-platform audio output; the lock-free seam crates `rtrb`, `triple_buffer`, and `basedrop`.",
		"Only include dependencies actually needed by the generated code (WAV writing remains pure-Rust, no crate). midly stays required by the midi_file module and both midi_play binaries.",
	]
}
