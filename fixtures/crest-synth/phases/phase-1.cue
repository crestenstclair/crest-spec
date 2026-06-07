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
