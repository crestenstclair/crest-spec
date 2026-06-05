import { command, event } from "../../../src/index.js";
import { app, kernel, shell } from "./base.js";

// Phase 1: Plumbing that makes noise
// App shell (eframe window) + cpal output + midir input.
// On note-on play a sine; on note-off stop it.
// Normalize input through midi2 into (group, channel) + high-res + note-id from day one.

// ── Audio ───────────────────────────────────────────────
// Phase 1 only: a throwaway sine voice — just enough to hear MIDI.
// Replaced in phase 2 by a real polyphonic engine.

const audio = app.context("Audio", {
  purpose: "minimal audio rendering: sine voice to prove MIDI-in-to-sound-out path works",
});

const sineVoice = audio.aggregate("SineVoice", {
  root: true,
  purpose: "plays a sine wave at a given pitch; placeholder for real synthesis",
  state: {
    noteId: "NoteId",
    noteNumber: "NoteNumber",
    frequency: "f64",
    phase: "f64",
    active: "bool",
  },
  commands: [
    command("NoteOn", { noteId: "NoteId", noteNumber: "NoteNumber", velocity: "Velocity" }),
    command("NoteOff", { noteId: "NoteId" }),
  ],
  events: [
    event("VoiceStarted", { noteId: "NoteId", frequency: "f64" }),
    event("VoiceStopped", { noteId: "NoteId" }),
  ],
  invariants: [
    "frequency must be positive",
    "phase wraps at 2*PI",
    "at most one voice per noteId",
  ],
});

audio.domainService("AudioRenderer", {
  purpose: "mixes all active SineVoices into an output buffer each audio callback",
  uses: [sineVoice],
});

// ── Module Assets ───────────────────────────────────────

app.asset("LibRs", {
  kind: "rust-module-declaration",
  description: "Root src/lib.rs module declarations",
  prompts: [
    "File path: src/lib.rs",
    "Declare modules: kernel, Shell, Audio",
  ],
});

kernel.asset("KernelMod", {
  kind: "rust-module-declaration",
  description: "src/kernel/mod.rs module declarations for Kernel context",
  prompts: [
    "File path: src/kernel/mod.rs",
    "Declare modules for: MidiGroup, MidiChannel, NoteId, NoteNumber, Velocity, MidiEvent, SampleRate, AudioFrame",
  ],
});

audio.asset("AudioMod", {
  kind: "rust-module-declaration",
  description: "src/Audio/mod.rs module declarations for Audio context",
  prompts: [
    "File path: src/Audio/mod.rs",
    "Declare modules for: SineVoice, AudioRenderer",
  ],
});
