package crestsynth

// Override (phase 9): adds the gamepad_demo [[bin]] and the full set of
// infrastructure-adapter crates that land in this phase (midir, midi2, eframe,
// egui, gilrs, fundsp). serde/serde_json carried from phase 8. Picked up by
// run-phased-agent.sh (highest-numbered phase-N.override-RootCargoToml.cue with
// N <= target phase). REPLACES the phase-8 override, so the FULL cumulative
// dependency + bin list is enumerated here. gamepad_demo itself pulls in no new
// crate — it exercises the host-agnostic GamepadNavigator/GlyphResolver — but
// the phase-9 adapter assets genuinely need these crates to compile.

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
		#"Include [[bin]] section: name = "preset_demo", path = "src/bin/preset_demo.rs""#,
		#"Include [[bin]] section: name = "gamepad_demo", path = "src/bin/gamepad_demo.rs""#,
		#"Dependencies: `midly` (0.5.x) for SMF parsing; `cpal` for audio output; the lock-free seam crates `rtrb`, `triple_buffer`, `basedrop`; `serde` (with `derive`) and `serde_json` for presets; and the phase-9 adapter crates: `midir` (MIDI I/O), `midi2` (MIDI 1.0 upconversion), `eframe`/`egui` (window + UI), `gilrs` (gamepad input), and `fundsp` (effects DSP)."#,
		#"CRITICAL eframe/egui version pin: depend on a CURRENT eframe/egui release — 0.28 or newer (prefer the latest 0.x line) — that transitively uses `objc2` 0.5+ and `winit` 0.30+. Do NOT use the eframe/egui 0.27 line: it pulls `winit` 0.29 → `objc2` 0.3-beta + `icrate` 0.0.4, which on current macOS aborts at window creation with a non-unwinding panic inside winit's `did_finish_launching` ("invalid message send to NSScreen countByEnumeratingWithState…: expected 'q', found 'Q'")."#,
		"Only include dependencies actually needed by the generated code. The adapter crates back the infrastructure adapters declared this phase; gamepad_demo itself uses only the in-crate domain services and pulls in none of them.",
	]
}
