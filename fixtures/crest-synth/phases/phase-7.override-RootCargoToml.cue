package crestsynth

// Override (phase 7): adds the effects_demo [[bin]]. Picked up by
// run-phased-agent.sh (highest-numbered phase-N.override-RootCargoToml.cue with
// N <= target phase). REPLACES the phase-6 override, so the FULL cumulative
// dependency + bin list is enumerated here. NO new crate at this phase: the
// effects_demo supplies a tiny in-crate EffectProcessor impl to prove the
// EffectChain ordering/bypass invariants. fundsp arrives with its adapter in
// phase 9, not here.

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
		#"Include [[bin]] section: name = "mod_play", path = "src/bin/mod_play.rs""#,
		#"Include [[bin]] section: name = "sample_demo", path = "src/bin/sample_demo.rs""#,
		#"Include [[bin]] section: name = "effects_demo", path = "src/bin/effects_demo.rs""#,
		"Dependencies: `midly` (0.5.x) for SMF parsing; `cpal` for audio output; the lock-free seam crates `rtrb`, `triple_buffer`, and `basedrop`.",
		"Only include dependencies actually needed by the generated code (WAV writing/reading remain pure-Rust; the effects demo uses an in-crate EffectProcessor impl, so no fundsp at this phase).",
	]
}
