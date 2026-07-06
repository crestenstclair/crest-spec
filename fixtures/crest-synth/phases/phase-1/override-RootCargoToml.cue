package crestsynth

// Override (phase 1): RootCargoToml must now declare the `midly` dependency so
// the MIDI-file playback spine (MidiFileLoader + midi_play bin) can parse
// Standard MIDI Files. Picked up by run-phased-agent.sh, which copies the
// highest-numbered phase-N.override-RootCargoToml.cue (N <= target phase),
// so a later phase can extend the dependency list without a CUE
// list-unification conflict. Overrides REPLACE the whole asset definition,
// so the full prompt + dependency list is enumerated here.

project: assets: RootCargoToml: {
	kind:        "cargo-manifest"
	description: "Root Cargo.toml for the crest-synth project"
	prompts: [
		"Package name: crest-synth, version 0.1.0",
		#"Include [[bin]] section: name = "crest-synth", path = "src/main.rs""#,
		#"Include [[bin]] section: name = "midi_play", path = "src/bin/midi_play.rs""#,
		"Dependencies: add `midly` (latest 0.5.x) for Standard MIDI File parsing.",
		"Only include dependencies actually needed by the generated code; at this phase that is `midly` (WAV writing is pure-Rust, no crate).",
	]
}
