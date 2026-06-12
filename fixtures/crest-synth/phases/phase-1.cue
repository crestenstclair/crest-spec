package crestsynth

// Phase 1: Plumbing that makes noise
// Throwaway sine voice — just enough to hear MIDI.
// Replaced by Synth context in phase 2.

project: contexts: Audio: purpose: "minimal audio rendering: sine voice to prove MIDI-in-to-sound-out path works"

project: contexts: Audio: aggregates: SineVoice: {
	root:    true
	purpose: "plays a sine wave at a given pitch; placeholder for real synthesis"
	state: {noteId: "NoteId", noteNumber: "NoteNumber", frequency: "f64", phase: "f64", active: "bool"}
	commands: [
		{name: "NoteOn", payload: {noteId: "NoteId", noteNumber: "NoteNumber", velocity: "Velocity"}},
		{name: "NoteOff", payload: {noteId: "NoteId"}},
	]
	events: [
		{name: "VoiceStarted", payload: {noteId: "NoteId", frequency: "f64"}},
		{name: "VoiceStopped", payload: {noteId: "NoteId"}},
	]
	invariants: ["frequency must be positive", "phase wraps at 2*PI", "at most one voice per noteId"]
}

project: contexts: Audio: domainServices: AudioRenderer: {
	purpose: "mixes all active SineVoices into an output buffer each audio callback"
	uses: ["aggregate.Audio.SineVoice"]
}

// ── MIDI playback spine (the audible integration backbone) ─────────────
// A Standard MIDI File loader plus an offline player binary. From phase 1
// onward this is THE way a human hears the engine: load a .mid (or a
// built-in demo tune), drive it through the current engine, render to WAV.

project: assetKinds: "rust-bin-target": {
	description: "Rust binary in src/bin/ — a [[bin]] target runnable with `cargo run --bin <name>`"
	prompts: ["Place the file under src/bin/", "Must compile and run with `cargo run --bin <name>`", "Use only types from the crate's own lib plus declared dependencies"]
}

project: assets: MidiFileLoader: {
	kind:        "rust-module-declaration"
	description: "src/midi_file/: parses Standard MIDI Files into time-ordered kernel MidiEvents"
	uses: ["valueObject.Kernel.MidiEvent", "valueObject.Kernel.MidiEventKind", "valueObject.Kernel.NoteNumber", "valueObject.Kernel.Velocity", "valueObject.Kernel.ChannelAddress", "valueObject.Kernel.MidiGroup", "valueObject.Kernel.MidiChannel", "valueObject.Kernel.NoteId"]
	prompts: [
		"Create a module at src/midi_file/ (mod.rs plus submodules as needed); declare it from src/lib.rs is handled by the engine, just author the module body.",
		"Expose a loader that parses a Standard MIDI File (SMF) using the `midly` crate into a time-ordered Vec of (timestamp_seconds: f64, MidiEvent) tuples, sorted ascending by timestamp.",
		"Convert SMF delta ticks to absolute seconds: honor the header's ticks-per-quarter-note division and track Set Tempo (FF 51 03) meta-events, defaulting to 120 BPM (500000 microseconds/quarter) until the first tempo event.",
		"Map SMF note-on with velocity 0 to a NoteOff (the running-status convention); real note-off (8x) also maps to NoteOff.",
		"Map each event's SMF channel (0-15) to the kernel ChannelAddress / MidiGroup(0) + MidiChannel; allocate a fresh NoteId per sounding note and reuse the matching id on its note-off so per-note tracking is correct.",
		"Build kernel MidiEvent values via the kernel constructors (MidiEvent::note_on / note_off), using NoteNumber and Velocity (velocity normalized 0.0-1.0 from the 0-127 byte).",
		"Ignore SMF events that have no kernel representation (e.g. text meta-events) rather than erroring; return a clear error type for malformed files.",
		"Unit test with an IN-MEMORY SMF byte buffer (use midly's writer or hand-built bytes) — round-trip: write a tiny multi-note SMF to a Vec<u8>, load it, assert the event count, ordering, channel mapping, and that note-on velocity 0 became NoteOff. Do NOT write or read any .mid file on disk.",
	]
}

project: assets: MidiPlayMain: {
	kind:        "rust-bin-target"
	description: "src/bin/midi_play.rs: offline MIDI-file player — renders a .mid (or a built-in demo tune) to WAV through the phase-1 engine"
	uses: ["asset.MidiFileLoader", "aggregate.Audio.SineVoice", "domainService.Audio.AudioRenderer"]
	prompts: [
		"File path: src/bin/midi_play.rs",
		"CLI: `midi_play [FILE.mid] [--out OUT.wav]`. If FILE is omitted, play a BUILT-IN demo melody constructed in code as a short multi-bar tune (a recognizable arpeggio/melody spanning a few seconds) — so no .mid asset file must live in the repo.",
		"When FILE is given, load it with the MidiFileLoader module into the time-ordered (seconds, MidiEvent) timeline.",
		"Render the timeline OFFLINE through the phase-1 engine (SineVoice + AudioRenderer): step in fixed sample blocks, trigger note_on/note_off at the correct sample offsets, and render what the current engine supports — sum simultaneous notes (basic polyphony by summing active voices).",
		"Write 16-bit mono WAV (default path midi-play.wav, or the --out path) using a pure-Rust WAV writer (no external WAV crate).",
		#"Print a one-line-per-section summary to stdout. Include a verbatim line with the token `rendered seconds=` followed by the rendered duration in seconds (e.g. `rendered seconds=4.0`), plus total events, peak simultaneous voices, and the output path. The `rendered seconds=` token must appear verbatim so a validation can assert the offline render actually ran."#,
		"Exit 0 on success; exit non-zero with a clear stderr message if the FILE cannot be parsed.",
	]
	validations: [
		{kind: "compiles", command: ["make", "build"], description: "project builds cleanly"},
		{kind: "integration", command: ["make", "demo-midi"], description: "built-in demo tune renders to WAV", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "file_exists", path: "midi-play.wav"},
			{kind: "stdout_contains", pattern: "rendered seconds="},
		]},
	]
}
