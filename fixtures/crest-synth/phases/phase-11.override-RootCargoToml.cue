package crestsynth

// Override (phase 11): adds the synth_ui [[bin]] (the standalone eframe/egui
// window). No NEW crates are needed — eframe/egui (window + UI) and cpal
// (audio output) were already declared in phase 9; synth_ui is a new shell
// over the existing engine + adapters. Picked up by run-phased-agent.sh
// (highest-numbered phase-N.override-RootCargoToml.cue with N <= target phase).
// REPLACES the phase-9 override, so the FULL cumulative dependency + bin list
// is enumerated here. This phase does NOT add nih-plug — the plugin wrapper is
// phase 10's concern and phase 11 must not depend on it.

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
		#"Include [[bin]] section: name = "synth_ui", path = "src/bin/synth_ui.rs""#,
		#"Dependencies: `midly` (0.5.x) for SMF parsing; `cpal` for audio output; the lock-free seam crates `rtrb`, `triple_buffer`, `basedrop`; `serde` (with `derive`) and `serde_json` for presets; and the phase-9 adapter crates: `midir` (MIDI I/O), `midi2` (MIDI 1.0 upconversion), `eframe`/`egui` (window + UI), `gilrs` (gamepad input), and `fundsp` (effects DSP)."#,
		#"CRITICAL eframe/egui version pin: depend on a CURRENT eframe/egui release — 0.28 or newer (prefer the latest 0.x line) — that transitively uses `objc2` 0.5+ and `winit` 0.30+. Do NOT use the eframe/egui 0.27 line: it pulls `winit` 0.29 → `objc2` 0.3-beta + `icrate` 0.0.4, which on current macOS aborts at window creation with a non-unwinding panic inside winit's `did_finish_launching` ("invalid message send to NSScreen countByEnumeratingWithState…: expected 'q', found 'Q'"). The crate builds fine and `ui-smoke` passes regardless (it opens no window), so this MUST be pinned here — the validation loop cannot catch a window-creation runtime panic."#,
		"Only include dependencies actually needed by the generated code. The standalone synth_ui binary is a new shell over the existing engine; it reuses the already-declared eframe/egui (window + UI) and cpal (audio) crates and pulls in NO new crate. Do NOT add nih-plug — that is the phase-10 plugin wrapper's dependency and phase 11 must not depend on it.",
	]
}
