package crestsynth

// Override (phase 1): the Makefile gains audible, human-runnable targets built
// around the MIDI-file playback spine. Picked up by run-phased-agent.sh, which
// copies the highest-numbered phase-N.override-BuildMakefile.cue (N <= target
// phase), so later phases extend this target list without a CUE
// list-unification conflict. Overrides REPLACE the whole asset definition, so
// the FULL cumulative target list is enumerated here.
//
// Convention for every phase's Makefile override:
//   - `demo-*`  targets render to a WAV and are safe in validations (no audio
//      device, no afplay).
//   - `play-*`  targets are human-only: they render then `afplay` the WAV.
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
		"demo-midi: cargo run --bin midi_play -- $(FILE)  — renders a MIDI file (or the built-in demo tune when FILE is empty) to midi-play.wav. Passing `make demo-midi FILE=song.mid` forwards the path; with no FILE the trailing `--` is harmless and the built-in demo plays.",
		"play-midi: depends on demo-midi, then `afplay midi-play.wav` so a human hears the result. `make play-midi FILE=song.mid` plays that file.",
		"play-tone: run the tone test to produce tone-test.wav (cargo run), then `afplay tone-test.wav`.",
		"Use cargo for all Rust operations. Declare .PHONY for all targets. afplay must appear ONLY in play-midi and play-tone, never in build/demo-midi/run.",
	]
}
