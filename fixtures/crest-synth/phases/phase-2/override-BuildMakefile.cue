package crestsynth

// Override (phase 2): adds `demo-voices` (renders the voice-stealing prover to
// WAV — validation-safe) and `play-voices` (human-only afplay). Picked up by
// run-phased-agent.sh (highest-numbered phase-N.override-BuildMakefile.cue with
// N <= target phase). REPLACES the phase-1 override, so the FULL cumulative
// target list is enumerated here.
//
//   - `demo-*`  targets render to a WAV and are safe in validations.
//   - `play-*`  targets are human-only: render then afplay.
//   - afplay appears ONLY in play-* targets, NEVER in a demo-* or validation.

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
		"demo-midi: cargo run --bin midi_play -- $(FILE)  — renders a MIDI file (or the built-in demo tune when FILE is empty) to midi-play.wav. `make demo-midi FILE=song.mid` forwards the path.",
		"demo-voices: cargo run --bin voice_demo  — renders the over-polyphonic voice-stealing prover to voice-demo.wav and prints a `steals=` count. Takes no FILE argument.",
		"play-midi: depends on demo-midi, then `afplay midi-play.wav`. `make play-midi FILE=song.mid` plays that file.",
		"play-voices: depends on demo-voices, then `afplay voice-demo.wav`.",
		"play-tone: run the tone test to produce tone-test.wav (cargo run), then `afplay tone-test.wav`.",
		"Use cargo for all Rust operations. Declare .PHONY for all targets. afplay must appear ONLY in play-midi, play-voices, and play-tone, never in build/demo-midi/demo-voices/run.",
	]
}
