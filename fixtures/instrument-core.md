# A standalone gamepad-controlled MIDI synthesizer

*(working name: instrument-core)*

## What this is

A standalone desktop application that **receives MIDI from an external source and plays the notes.** That's the whole job. It is a sound source — a software sound module — with a deep, sound-design-focused engine and a gamepad-driven interface.

Concrete goals:

- **MIDI in, sound out.** It listens for MIDI from any external source (hardware sequencer, controller, another app) and renders the notes in real time. It reacts; it does not generate.
- **No sequencing. None.** There is no pattern memory, no arranger, no piano roll, no step recorder. It accepts MIDI messages and plays them. It *has* a clock and can also slave to incoming MIDI clock — but the clock exists only to sync time-based modulation (LFO rates, delay times). It never sequences notes.
- **Runs on the Steam Deck and any desktop.** Linux first (the Deck), but Windows and macOS for free from the same Rust codebase.
- **Gamepad-controlled.** The primary input device for the *interface* is a game controller. The UI, navigation, and sound-design workflow are all built around a pad, not a mouse and keyboard.
- **A list of patches, each subscribing to a MIDI channel.** It is multitimbral. You load as many patches as you want, and each one *subscribes* to a channel; an external source can drive a bass patch on one channel, a pad patch on another, and so on. Two patches can subscribe to the same channel to layer sounds. (See MIDI 2.0 below: addressing is by group + channel, so you aren't capped at 16.)

What it is **not**, to be unambiguous: not a DAW, not a groovebox, not a tracker, not a sequencer. It is the thing on the *other end* of a sequencer's MIDI cable.

---

## Design decisions and why

**The engine is a plain library; the app is a thin shell around it.** All sound generation, voice management, and modulation live in a host-agnostic Rust library that knows nothing about windows, controllers, or audio drivers. The standalone app is a small shell that wires real audio output, real MIDI input, and the GUI to that library. This keeps the hard part (the DSP and voice logic) testable in isolation, and it means a plugin version later is a different shell over the same engine, not a rewrite.

**It owns its own window and loop.** Because it's a full-screen, gamepad-driven app on the Steam Deck, the app drives its own window, render loop, and input — it is not shoehorned into a plugin host's standalone harness. That gives full control over the controller-first UX and the Deck's display.

**A hard split between the audio thread and everything else.** Audio runs on a dedicated real-time thread that may never allocate memory, take a lock, or block — any of those risks a missed buffer deadline and an audible dropout. MIDI handling, the GUI, patch editing, and file loading all run on other threads and communicate with the audio thread only through lock-free, wait-free channels. This split is the single most important architectural constraint and it shapes everything else.

**A list of patches subscribed to channels.** The engine holds a list of *patches*. A patch is a complete instrument — an engine instance, its sound parameters, its modulation, and its own pool of voices — and one of its properties is the channel it subscribes to. Incoming MIDI is dispatched to every patch subscribed to that channel. There is no fixed slot count, and nothing sits between a patch and its channel: the patch *is* the instrument, and the subscription is just one of its fields. Layering is two patches on the same channel; there's no separate "layer" concept to build.

**Sound design is the point, so the engine is pluggable and the modulation is deep.** Rather than one fixed synth, the engine supports multiple *types* (wavetable/virtual-analog, FM, and sample playback to start), all behind a common interface so different patches can use different engines. On top of that sits a modulation system — envelopes, LFOs, random, macros, MIDI expression — routable to almost any parameter. The gamepad workflow is built to make assigning and tweaking modulation fast.

**Per-note expression is modeled once, by note id.** Both MPE (the MIDI 1.0 technique that spreads one instrument's notes across channels to get per-note control) and MIDI 2.0's native per-note controllers describe the same thing: expression attached to an individual note rather than a whole channel. The engine models this internally as expression keyed by a *note id*, so a patch handles MPE and native MIDI-2 per-note input through one path. A patch's subscription can therefore be a single channel or an MPE zone (a span of channels treated as one expressive instrument).

**MIDI 2.0 ready by internal model, not by transport.** Broad MIDI 2.0 *transport* isn't usable yet — OS support is partial and most "MIDI 2.0 ready" controllers still emit MIDI 1.0 over USB — so the app receives 1.0 over the wire for now. Readiness comes instead from the internal event model: all input is normalized into a superset of MIDI 2.0 semantics — addressed by **(group, channel)** (256 destinations, not 16), carrying **high-resolution** normalized control values, and tagging notes with a **note id** for per-note expression. MIDI 1.0 upconverts losslessly into this model; when OS UMP transport matures, only the input layer changes and the engine is untouched.

---

## Libraries and tools

The Rust audio ecosystem covers almost all of the plumbing, so very little of this is hand-rolled.

**Audio output** — [`cpal`](https://github.com/RustAudio/cpal) for cross-platform device I/O (ALSA/PipeWire on the Deck, WASAPI/CoreAudio elsewhere). On Linux, optionally a JACK/PipeWire path for lower latency.

**MIDI input** — [`midir`](https://github.com/Boddlnagg/midir) for cross-platform MIDI I/O (the transport), with [`wmidi`](https://github.com/RustAudio/wmidi) for typing MIDI 1.0 messages. For MIDI 2.0 readiness, [`midi2`](https://docs.rs/midi2/) provides UMP and MIDI-CI types and upconverts MIDI 1.0 channel-voice messages to 2.0 resolution — use it as the *normalization* layer that turns whatever the wire delivers into your internal event model. When OS UMP transport is worth adopting, it arrives via ALSA UMP / PipeWire on Linux and CoreMIDI on macOS, behind that same normalization step.

**DSP building blocks** — [`fundsp`](https://github.com/SamiPerttu/fundsp) for filters, oscillators, envelopes, delays, and reverbs as composable nodes. This is the "don't write a reverb from scratch" layer.

**Sample playback** — [`symphonia`](https://github.com/pdeljanov/Symphonia) (or `hound` for plain WAV) to decode sample files, and [`dasp`](https://github.com/RustAudio/dasp) for sample interpolation primitives (you'll want decent sinc/linear interpolation for pitch-shifted playback).

**A starter sound engine** — [`rustysynth`](https://github.com/sinshu/rustysynth) or [`oxisynth`](https://github.com/PolyMeilex/OxiSynth), both pure-Rust SoundFont (SF2) synths that run in real time. Useful to drop in early so you have *sound* on day one, then progressively replace with your own engines. `oxisynth` also ships standalone reverb/chorus crates worth stealing.

**Real-time safety** — the unglamorous-but-essential set: [`rtrb`](https://github.com/mgeier/rtrb) (lock-free ring buffer for handing data to the audio thread), [`triple_buffer`](https://github.com/HadrienG2/triple-buffer) (latest-wins parameter snapshots), [`basedrop`](https://github.com/glowcoil/basedrop) (real-time-safe deferred deallocation, so you can swap multi-megabyte sample sets without ever calling `free` on the audio thread), and `assert_no_alloc` in debug builds to catch accidental allocations in the render path.

**GUI** — [`egui`](https://github.com/emilk/egui) via [`eframe`](https://github.com/emilk/egui/tree/master/crates/eframe), which sits on `winit` + `wgpu`. Immediate-mode fits a knob-and-matrix sound-design tool perfectly, it's cross-platform from one codebase, and on the Deck it runs natively on Vulkan through `wgpu`. Use `egui`'s custom painting for any bespoke visual style.

**Gamepad** — [`gilrs`](https://gitlab.com/gilrs-project/gilrs) for cross-platform controller input. egui has no built-in gamepad navigation (and you wouldn't want generic focus traversal anyway), so you read the pad with gilrs and map it to your own cursor/edit model.

**Persistence** — `serde` (with `serde_json` or a binary format) for saving and loading patches and the full setup (the patch list and each patch's channel subscription).

**Optional, later: plugin formats** — [`nih-plug`](https://github.com/robbert-vdh/nih-plug) if you ever want CLAP/VST3 versions. It wraps the same engine library and gives you the plugin formats plus a JACK-backed standalone for free. Not needed for the standalone app itself, but the library-first design keeps the door open at near-zero cost.

---

## Architecture and why

### Two worlds, one thin seam

```
   NON-REAL-TIME THREADS                          REAL-TIME AUDIO THREAD
   ─────────────────────                          ──────────────────────

   MIDI in (midir) ─► normalize (midi2)           for each audio block:
      → internal events keyed by                    • drain note / expression events
        (group, channel), high-res, note id         • dispatch each event to every patch
                                                       subscribed to that channel
   GUI (eframe/egui) + gamepad (gilrs)             • per patch: allocate / steal voices,
      → edit patches                                 run its engine, apply modulation ► DSP
                                                    • per-patch FX ► mix all patches ► master
   load patch / sample (symphonia, serde)          • write the output buffer

   ══ lock-free handoff ══►  rtrb (events / patch snapshots) · triple_buffer (params) · Arc (samples)
   ◄═ off-thread free ════   retired memory returned via basedrop
                                                   cpal pulls the output buffer to the device
```

The audio thread does nothing but render. Everything that allocates, blocks, or waits on the user stays on the other side and reaches the audio thread only through lock-free channels: `rtrb` for discrete messages (a new patch, a loaded sample set), `triple_buffer` for continuously-changing parameter values (latest wins), and `basedrop` to send retired memory *back* so the audio thread never frees anything. This is why the engine can hot-swap a sample set mid-performance without glitching.

### Patches subscribed to channels

The engine holds a list of patches. A patch = `{ engine instance, sound parameters, modulation, a voice pool, a channel subscription }`. Routing is just: for an incoming event, find every patch subscribed to its `(group, channel)` address and hand it the event. Voices are pooled per patch with their own polyphony limit and stealing, so a busy pad patch can't starve a bass patch. A global mix bus sums all patches, applies master FX, and writes the output. Two patches on the same channel layer automatically; a patch can also subscribe to an MPE zone.

### Input: normalize once, stay transport-agnostic

The input path has two stages on purpose. A *transport* delivers raw MIDI (today `midir` carrying MIDI 1.0; later ALSA UMP / PipeWire / CoreMIDI carrying UMP). A *normalize* stage (`midi2`) turns whatever arrives into one internal event type: addressed by `(group, channel)`, carrying high-resolution normalized values, and tagging note-level messages with a note id. Everything downstream — routing, voices, modulation — speaks only that internal type and never sees a raw byte or a wire format. MIDI 1.0 upconverts into it; MIDI 2.0 fills it natively. That is what "MIDI 2.0 ready" means in practice: the engine already speaks the richer language, so adopting real UMP transport later is a change to the transport stage alone.

### Pluggable engines

Each engine type (wavetable, FM, sample player) implements one common interface — render a block for a voice given pitch and modulation, handle note-on/off, report when it's finished. In the real-time inner loop this is enum dispatch, not dynamic dispatch, to keep the hot path free of vtable indirection. Adding a new engine type later is implementing that one interface; any patch can then select it.

### Modulation

A modulation layer evaluates sources (envelopes and per-note expression per voice; LFOs, random, and macros shared per patch) and mixes them onto destinations (pitch, filter cutoff, sample start, gain, pan, FX send, and so on). The gamepad UI is built to make routing a source to a destination and dialing its depth a fast, few-button operation. Time-based sources can sync to the internal clock or incoming MIDI clock — this is the *only* thing the clock is for.

### MPE: built later, not blocked

MPE is not built in the early phases, but the design is deliberately shaped so it can be added later without touching the engine core. The thing that would *block* MPE is a channel-centric voice/modulation model ("this channel's pitch bend"), which forces a rewrite the moment per-note expression is needed. The note-id model avoids that: expression is per-note from the start, so adding MPE becomes purely an input-translation task (rotate incoming member channels back into note ids at the normalize stage). To keep that door open, two invariants hold from day one, even before any MPE code exists:

1. **Per-note expression reaches the voice, not just the patch.** The note id carries through allocation so a `(pitch bend, timbre, pressure)` triple lands on the specific voice for that note. Voices must never assume expression is patch- or channel-level — that would rebuild the channel-centric trap one layer down.
2. **The three MPE dimensions are reserved as named per-voice modulation sources.** Per-note bend (X), timbre / CC74 (Y), and pressure (Z) exist in the modulation source list as first-class per-voice sources from the start, even while nothing emits them yet. Single-channel patches simply leave them at zero. "Building MPE" then means feeding real data into sources that already exist, not adding new source types or touching the matrix.

Everything else MPE needs — Lower/Upper zone setup, manager-channel handling, the MPE Configuration Message and pitch-bend-range negotiation — is input-side parsing that resolves into the note-id + X/Y/Z model before the engine proper sees it, so none of it constrains the core design and all of it can land whenever.

### GUI and gamepad

The app owns a `winit`/`wgpu` window through `eframe`, runs the `egui` UI immediate-mode, and reads the controller through `gilrs` each frame, translating pad input into navigation and edits. The UI is a pure view over the engine's current state plus an editor that sends edits across the seam to the audio thread. No audio logic lives in the UI.

---

## Suggested implementation phases

Ordered to get **sound from incoming MIDI as early as possible**, then deepen.

1. **Plumbing that makes noise.** App shell (eframe window) + `cpal` output + `midir` input. On note-on, play a sine; on note-off, stop it. Goal: plug in your external source and hear it. Proves the whole MIDI-in-to-audio-out path. Normalize input through `midi2` into your internal `(group, channel)` + high-res + note-id event type from day one — it's cheap now and avoids a refactor later.
2. **A real polyphonic engine.** One wavetable/virtual-analog engine: voice allocation with basic stealing, oscillator, filter, amp envelope. Now it's a real synth, single instrument.
3. **Harden the real-time seam.** Introduce `rtrb` + `triple_buffer` + `basedrop`. Move all parameter and patch changes across the lock-free boundary. Everything after this respects the audio deadline.
4. **Multiple patches subscribed to channels.** A list of patches, each subscribing to a `(group, channel)` address; dispatch incoming events to subscribers; per-patch voice pools; a global mix. Now an external source can drive several instruments at once, and two patches on one channel layer.
5. **Modulation.** Envelopes, LFOs, a routing matrix to a handful of destinations (pitch, cutoff, gain), with gamepad-driven assignment. This is where the "sound design focused" identity starts to show.
6. **Sample-playback engine.** SF2/sample loading via `symphonia` + `dasp` interpolation, with `basedrop` handling sample-set swaps. Bootstrap with `rustysynth`/`oxisynth` if you want sound immediately, then replace internals.
7. **Effects.** Per-patch and global reverb/chorus/delay via `fundsp`. The lush tail that makes pads and atmospheres sing.
8. **Patches and presets.** `serde` save/load for individual patches and the full setup (the patch list and their channel subscriptions); a preset browser.
9. **Gamepad UX polish + Steam Deck packaging.** Tighten navigation, full-screen layout for the Deck's display, controller glyphs, and a distributable build.
10. **(Optional) plugin wrapper.** A `nih-plug` shell over the same engine library if you want CLAP/VST3 versions.

By phase 1 you hear your MIDI source. By phase 4 it's a multitimbral sound module. By phases 5–7 it's a deep, sound-design-focused instrument.

---

## Open questions to decide

- **MPE / per-note expression depth** — readiness is committed (see "MPE: built later, not blocked"); still open is *how far* to take it: the full per-note controller set, zone-configuration specifics, and how MPE-zone patches coexist with single-channel patches on the same input.
- **How far to take MIDI 2.0 now** — internal-model readiness is cheap and assumed; adopting real UMP *transport* (ALSA UMP / PipeWire / CoreMIDI) and MIDI-CI capability negotiation is a later, optional step gated on usable device support.
- **Sample streaming vs. all-in-RAM** — RAM is simplest and fine on a desktop/Deck; streaming only matters if sample libraries get very large.
- **Voice pooling** — strictly per-patch pools vs. a shared pool with per-patch limits (affects how gracefully one patch can borrow headroom from another).
