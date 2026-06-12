package crestsynth

// Override (phase 3): adds the live-playback human target `play-midi-live`,
// which streams a MIDI file through the default output device via cpal.
// Picked up by run-phased-agent.sh, which copies the highest-numbered
// phase-N.override-BuildMakefile.cue (N <= target phase). This REPLACES the
// phase-1 override, so the FULL cumulative target list is enumerated here.
//
//   - `demo-*`  targets render to a WAV and are safe in validations.
//   - `play-*`  targets are human-only: render/stream then (for offline) afplay.
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
		"demo-midi: cargo run --bin midi_play -- $(FILE)  — renders a MIDI file (or the built-in demo tune when FILE is empty) to midi-play.wav. `make demo-midi FILE=song.mid` forwards the path.",
		"demo-voices: cargo run --bin voice_demo  — renders the over-polyphonic voice-stealing prover to voice-demo.wav and prints a `steals=` count. Takes no FILE argument.",
		"check-live: cargo run --bin midi_play_live -- --no-device-dry-run  — constructs the real-time pipeline (rtrb ring buffer, triple_buffer param bridge, basedrop deferred dealloc) WITHOUT opening an audio device, prints `dry-run ok: pipeline constructed`, exits 0. Validation-safe (no device, no afplay).",
		"play-midi: depends on demo-midi, then `afplay midi-play.wav`. `make play-midi FILE=song.mid` plays that file.",
		"play-voices: depends on demo-voices, then `afplay voice-demo.wav`.",
		"play-tone: run the tone test to produce tone-test.wav (cargo run), then `afplay tone-test.wav`.",
		"play-midi-live: cargo run --bin midi_play_live -- $(FILE)  — streams the MIDI file (or built-in demo tune) live through the default output device. `make play-midi-live FILE=song.mid` plays that file. This target opens an audio device; it does NOT use afplay and is never invoked by a validation.",
		"Use cargo for all Rust operations. Declare .PHONY for all targets. afplay must appear ONLY in play-midi, play-voices, and play-tone. check-live and demo-voices are validation-safe (no device, no afplay).",
	]
}
