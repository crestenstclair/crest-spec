package crestsynth

// Manifest — the crate manifest and the demo binary. This file is the ONLY
// place in the spec where crate dependencies are named; everywhere else the
// spec is language-profile-clean (adapters carry a framework name only).

project: assets: RootCargoToml: {
	kind:        "cargo-manifest"
	description: "Cargo.toml for the crest-synth crate"
	prompts: [
		"File path: Cargo.toml",
		"Package name crest-synth, edition 2021, a lib target plus binary targets under src/bin/.",
		"Dependencies and why each exists: cpal (audio output), midir (MIDI input), eframe + egui (GUI), gilrs (gamepad input), rtrb (lock-free SPSC ring buffer), triple_buffer (lock-free latest-wins parameter sharing), basedrop (deferred deallocation for real-time), serde + serde_json (preset/session serialization), symphonia (audio file decoding).",
		"Choose current stable versions; the whole-tree gate (build/clippy/test) proves the resolution works.",
	]
}

project: assets: ToneTestMain: {
	kind:        "rust-bin-target"
	description: "src/bin/tone_test.rs: renders one second of A440 through the engine and asserts the output is audible"
	uses: ["domainService.Engine.EngineRenderer", "valueObject.Kernel.Frequency", "valueObject.Kernel.AudioFrame"]
	prompts: [
		"File path: src/bin/tone_test.rs",
		"Trigger a single A440 note through the engine, render one second of audio into a buffer, and MEASURE the peak absolute sample value of the rendered buffer.",
		#"Print exactly one line `peak=<value>` with the measured peak, then exit non-zero unless 0.1 < peak <= 1.0 — a silent or clipping render must fail the run."#,
	]
	validations: [
		{kind: "integration", command: ["cargo", "run", "--bin", "tone_test"], description: "renders an audible, non-clipping tone", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "stdout_contains", pattern: "peak="},
		]},
	]
}
