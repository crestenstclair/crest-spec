package crestsynth

// Override (phase 4): adds `demo-patches` (renders the multi-patch integration
// proof to WAV — validation-safe) and `play-patches` (human-only afplay).
// Picked up by run-phased-agent.sh (highest-numbered phase-N.override-
// BuildMakefile.cue with N <= target phase). REPLACES the phase-3 override, so
// the FULL cumulative target list is enumerated here.
//
//   - `demo-*`  targets render to a WAV and are safe in validations.
//   - `play-*`  targets are human-only: render then afplay.
//   - afplay appears ONLY in play-* targets, NEVER in a demo-* or validation.
//   - play-midi-live opens an audio device and is NEVER used by a validation.

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
		"play-midi: depends on demo-midi, then `afplay midi-play.wav`. `make play-midi FILE=song.mid` plays that file.",
		"play-voices: depends on demo-voices, then `afplay voice-demo.wav`.",
		"play-tone: run the tone test to produce tone-test.wav (cargo run), then `afplay tone-test.wav`.",
		"play-midi-live: cargo run --bin midi_play_live -- $(FILE)  — streams live through the default output device. `make play-midi-live FILE=song.mid` plays that file. Opens an audio device; no afplay; never used by a validation.",
		"demo-patches: cargo run --bin patch_play -- $(FILE)  — renders the multi-patch integration proof (dispatcher → per-patch pools → global mix) to patch-play.wav. `make demo-patches FILE=song.mid` forwards the path.",
		"play-patches: depends on demo-patches, then `afplay patch-play.wav`.",
		"Use cargo for all Rust operations. Declare .PHONY for all targets. afplay must appear ONLY in play-midi, play-voices, play-tone, and play-patches.",
	]
}
