package crestsynth

// Override (phase 9): adds `check-gamepad` — a headless, device-free validation
// of the GamepadNavigator/GlyphResolver domain services (no gilrs/egui/eframe,
// no window). Picked up by run-phased-agent.sh (highest-numbered phase-N.
// override-BuildMakefile.cue with N <= target phase). REPLACES the phase-8
// override, so the FULL cumulative target list is enumerated here.
//
//   - `demo-*`   targets render to a WAV and are safe in validations.
//   - `check-*`  targets are device-free behavioral checks (no WAV, no device).
//   - `play-*`   targets are human-only: render then afplay.
//   - afplay appears ONLY in play-* targets, NEVER in a demo-*/check-*/validation.

project: assets: BuildMakefile: {
	kind:        "makefile"
	description: "Build automation for the crest-synth project"
	prompts: [
		"Default target: build",
		"build: cargo build",
		"test: cargo test",
		"check: cargo check",
		"clean: cargo clean",
		"run: cargo run  (runs the default tone-test binary)",
		"lint: cargo clippy -- -D warnings",
		"fmt: cargo fmt -- --check",
		"demo-midi: cargo run --bin midi_play -- $(FILE)  — renders a MIDI file (or built-in demo tune) to midi-play.wav. `make demo-midi FILE=song.mid` forwards the path.",
		"demo-voices: cargo run --bin voice_demo  — renders the over-polyphonic voice-stealing prover to voice-demo.wav and prints a `steals=` count. Takes no FILE argument.",
		"check-live: cargo run --bin midi_play_live -- --no-device-dry-run  — constructs the real-time pipeline without opening an audio device, prints `dry-run ok: pipeline constructed`, exits 0. Validation-safe.",
		"demo-patches: cargo run --bin patch_play -- $(FILE)  — renders the multi-patch integration proof to patch-play.wav. `make demo-patches FILE=song.mid` forwards the path.",
		"demo-mod: cargo run --bin mod_play -- $(FILE)  — renders the modulated (LFO vibrato + filter sweep) demo to mod-play.wav. `make demo-mod FILE=song.mid` forwards the path.",
		"demo-samples: cargo run --bin sample_demo  — synthesizes a sample, loads it, resolves key/velocity zones, interpolates, renders to sample-demo.wav and prints `zones loaded=` / `zone hit:` markers. Hermetic; no FILE argument.",
		"demo-effects: cargo run --bin effects_demo -- $(FILE)  — renders the multi-patch demo through per-patch + global EffectChains to effects-demo.wav and prints `slot order matters: true` / `bypass passthrough: true`. `make demo-effects FILE=song.mid` forwards the path.",
		"demo-presets: cargo run --bin preset_demo  — serializes a full Setup, reloads it, proves bit-identical re-render, writes preset-demo.wav and prints `setup roundtrip: equal` / `render identical: true`. No FILE argument.",
		"check-gamepad: cargo run --bin gamepad_demo  — headless prover for the GamepadNavigator/GlyphResolver domain services; feeds scripted events, asserts action mapping + per-controller glyphs, prints `nav actions ok:` / `glyphs resolved: per-controller`, exits 0. Opens NO device or window; validation-safe.",
		"play-midi: depends on demo-midi, then `afplay midi-play.wav`. `make play-midi FILE=song.mid` plays that file.",
		"play-voices: depends on demo-voices, then `afplay voice-demo.wav`.",
		"play-tone: run the tone test to produce tone-test.wav (cargo run), then `afplay tone-test.wav`.",
		"play-midi-live: cargo run --bin midi_play_live -- $(FILE)  — streams live through the default output device. `make play-midi-live FILE=song.mid` plays that file. Opens an audio device; no afplay; never used by a validation.",
		"play-patches: depends on demo-patches, then `afplay patch-play.wav`.",
		"play-mod: depends on demo-mod, then `afplay mod-play.wav`.",
		"play-samples: depends on demo-samples, then `afplay sample-demo.wav`.",
		"play-effects: depends on demo-effects, then `afplay effects-demo.wav`.",
		"play-presets: depends on demo-presets, then `afplay preset-demo.wav`.",
		"Use cargo for all Rust operations. Declare .PHONY for all targets. afplay must appear ONLY in play-midi, play-voices, play-tone, play-patches, play-mod, play-samples, play-effects, and play-presets. demo-* and check-* targets never use afplay and never open a device or window.",
	]
}
