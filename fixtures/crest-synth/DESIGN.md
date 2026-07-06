# crest-synth Design Document

A standalone, gamepad-friendly MIDI synthesizer built in Rust. Designed for the Steam Deck but runs on any desktop. Hexagonal architecture with a hard real-time audio thread at its core.

## Architecture

Hexagonal / ports-and-adapters. The audio callback is the innermost constraint — it has a hard deadline and must never allocate, lock, or block.

```
┌─────────────────────────────────────────────────┐
│  Shell (infrastructure)                         │
│  cpal · midir · eframe/egui · gilrs             │
│  ┌───────────────────────────────────────────┐  │
│  │  Application Services                     │  │
│  │  PatchManager · PresetBrowser             │  │
│  │  SessionManager · MixerController         │  │
│  │  ┌─────────────────────────────────────┐  │  │
│  │  │  Domain                             │  │  │
│  │  │  Engine · Sample · Effects · Mixer  │  │  │
│  │  │  Modulation · Patch · Preset        │  │  │
│  │  │  ┌───────────────────────────────┐  │  │  │
│  │  │  │  Kernel (shared value types)  │  │  │  │
│  │  │  └───────────────────────────────┘  │  │  │
│  │  │  ┌───────────────────────────────┐  │  │  │
│  │  │  │  RealTime (lock-free seam)    │  │  │  │
│  │  │  └───────────────────────────────┘  │  │  │
│  │  └─────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────┘  │
└─────────────────────────────────────────────────┘
```

### Threading Model

Two threads with a lock-free boundary:

- **Audio thread** (real-time): reads parameter snapshots, pops MIDI events from ring buffer, renders voices through the mixer, writes to audio output. Never allocates, never locks, never does I/O.
- **UI/MIDI thread** (non-RT): polls MIDI and gamepad, renders GUI, pushes parameter snapshots and events across the boundary.

The boundary uses:
- **rtrb** — SPSC lock-free ring buffer for discrete events (MIDI, patch changes)
- **triple_buffer** — latest-wins parameter snapshots readable without locking
- **basedrop** — deferred deallocation so the audio thread never frees memory

## Bounded Contexts

### Kernel — Shared Value Types

Foundation types used across all contexts. No logic, just data.

**MIDI addressing:**
- `MidiGroup` (u8, 0-15) — MIDI 2.0 group index
- `MidiChannel` (u8, 0-15) — channel within a group
- `ChannelAddress` — (group, channel) pair; 256 addressable destinations
- `NoteId` (u32) — unique per sounding note; enables per-note expression and MPE
- `NoteNumber` (u8, 0-127)
- `Velocity` (f64, 0.0-1.0) — upconverted from MIDI 1.0 7-bit to high-res
- `MidiEventKind` — NoteOn, NoteOff, ControlChange, PitchBend, Aftertouch, PolyPressure, ProgramChange
- `MidiEvent` — normalized internal event: (group, channel) addressed, high-res values, NoteId tagged

**Audio primitives:**
- `SampleRate` (u32) — e.g. 44100, 48000
- `AudioFrame` — stereo pair (f32 left, f32 right)
- `Frequency` (f64) — Hz
- `Amplitude` (f64) — linear, 0.0 = silence, 1.0 = unity
- `Decibel` (f64) — logarithmic; 0 dB = unity, -inf = silence
- `Pan` (f64) — -1.0 hard left, 0.0 center, 1.0 hard right

**Time:**
- `SampleCount` (u64) — absolute position since stream start
- `BufferSize` (usize) — frames per callback, typically power-of-two
- `TimeSignature` — numerator/denominator (e.g. 4/4, 6/8)
- `Tempo` (f64) — BPM

**Identifiers:**
- `PatchId`, `PresetId`, `BankId`, `BusId`

### Engine — Polyphonic Synthesis

The core sound generation engine. Multiple engine types share the same voice lifecycle.

**Engine types:**
- Virtual analog (classic subtractive)
- Wavetable
- Sample playback (delegates to Sample context)
- FM synthesis

**Voice lifecycle:**
- Each voice: oscillator → filter → amp/filter/pitch envelopes
- Voice allocation with configurable stealing (oldest, quietest, lowest velocity, or refuse)
- Per-note expression (MPE pressure, slide, pitch bend)
- Max polyphony configurable per patch (typically 8-64)

**Oscillator:**
- Waveforms: sine, saw, square, triangle, noise, wavetable
- Detune (cents), pulse width, unison voices (1-16) with spread

**Filter:**
- Types: low-pass, high-pass, band-pass, notch, low shelf, high shelf, peak
- Cutoff (20-20000 Hz), resonance, drive, key tracking
- Separate filter envelope with configurable amount

**Envelopes (x3 per voice):**
- Amp envelope — controls volume
- Filter envelope — modulates cutoff
- Pitch envelope — modulates frequency
- All ADSR with configurable times and sustain level

**Ports (pluggable implementations):**
- `Oscillator` — render sample at phase + advance phase
- `Filter` — process sample through filter state
- `EnvelopeGenerator` — tick/trigger/release

**Services:**
- `VoiceAllocator` — assigns notes to voices with stealing
- `VoiceRenderer` — renders one voice for one buffer
- `EngineRenderer` — iterates all active voices, sums to stereo

### Sample — Sample Playback & Soundfonts

Sample-based sound sources with key/velocity zone mapping.

**Formats:**
- WAV loading (mono/stereo, 16-bit/float)
- SF2 soundfont loading (multiple presets per file)
- Interpolation: none (nearest), linear, cubic, sinc

**Zone mapping:**
- Each `SampleSet` contains zones
- Each zone maps a key range + velocity range to a sample
- Root key, fine tune, gain, pan per zone
- Loop modes: no loop, forward, ping-pong, release (loop until note-off then play to end)

**Ports:**
- `SampleLoader` — loads WAV/SF2 from disk (infrastructure)
- `SampleStore` — in-memory store for decoded sample data

**Services:**
- `SamplePlayer` — plays at correct pitch with interpolation and looping
- `ZoneResolver` — given note + velocity, finds matching zone(s)

### Effects — Audio Processors

Each effect is a pure function: input buffer → output buffer, with internal state.

**Effect types:**
- **Reverb** — algorithmic; room size, damping, wet/dry, pre-delay, width
- **Delay** — time, feedback, wet/dry, ping-pong mode, tempo sync
- **Chorus/Flanger** — rate, depth, wet, voice count
- **EQ** — parametric bands (filter type, frequency, gain, Q)
- **Compressor** — threshold, ratio, attack, release, makeup gain, knee
- **Distortion** — (future)
- **Limiter** — (used on master bus)

**Effect chain:**
- Ordered sequence of slots, each independently bypassable
- Insert, remove, reorder slots
- Configurable max slots per chain
- Signal flows top-to-bottom

**Port:**
- `EffectProcessor` — unified interface (process, reset, latency) so chains are type-erased

**Service:**
- `ChainRenderer` — processes a buffer through all non-bypassed slots

### Mixer — Channel Strips, Buses, Sends

Standard mixing console architecture.

**Signal flow:**
```
Patch → ChannelStrip (input gain → insert FX → volume/pan → send taps)
         ↓                    ↓
         → Bus output         → AuxBus (own insert FX → return level)
                                        ↓
                              MasterBus (insert FX → limiter → output)
```

**Channel strip:**
- Input gain, volume (dB), pan, mute, solo
- Insert FX chain (per-channel effects)
- Up to 8 send taps (pre- or post-fader, configurable level)
- Peak level metering

**Buses:**
- Master bus: final summing point with its own insert chain + limiter
- Aux buses: receive send taps, apply their own FX, feed back to master
- Bus 0 = master (reserved), 1+ = aux/send

**Solo behavior:**
- Exclusive within a mix group — when any channel is soloed, non-soloed channels are muted

**Service:**
- `MixEngine` — full mix pass: render strips → collect sends → sum aux buses → process master

### Modulation — Mod Matrix, LFOs, Expression

Flexible modulation routing from any source to any destination.

**Sources:**
- LFO 1-4 (sine, triangle, saw, square, sample-and-hold, random)
- Amp/filter/pitch/mod envelopes
- Velocity, key tracking
- MIDI: aftertouch, pitch bend, mod wheel, expression
- MPE axes: X (slide), Y (pressure), Z (lift)
- Any CC (0-127)

**Destinations:**
- Oscillator pitch, pulse width
- Filter cutoff, resonance
- Amp level, pan
- LFO rate, depth
- Effect parameters (by slot + param index)
- Send levels (by bus)

**Routing:**
- Each route: source → destination, amount (-1.0 to 1.0), curve (linear/exponential/S-curve/stepped)
- Bipolar or unipolar
- Optional "via" source (depth modulator) — e.g. mod wheel controls LFO-to-cutoff depth
- Max routes configurable (typically 32-64)

**LFO config:**
- Shape, rate (Hz or beat fraction), depth, tempo sync, retrigger per note, start phase

**Service:**
- `ModProcessor` — evaluates all sources, applies routes, produces per-sample parameter offsets

### Patch — Instruments & MIDI Routing

A Patch is one instrument: engine config + sample set + mod matrix + MIDI routing.

**Patch contents:**
- Voice config (engine type, oscillator, filter, envelopes, polyphony, steal policy)
- Optional sample set reference (for sample playback or layering)
- Mod matrix
- Channel mapping (which MIDI addresses to listen on)
- Mixer channel strip reference

**MIDI routing:**
- Each patch subscribes to specific (group, channel) addresses
- Omni mode: respond to all channels
- MPE zones: manager channel + member channels for per-note expression
- Multiple patches can layer on the same channel (intentional)

**MPE zones:**
- Manager channel for global CCs
- Contiguous member channels (up to 15) for per-note expression
- Zones must not overlap across patches

**Services:**
- `MidiDispatcher` — routes MidiEvents to matching patches
- `PatchManager` (app service) — CRUD for patches, voice/sample/mod/routing config

### Preset — Persistence

Save and restore everything.

**Preset:** a snapshot of one patch's complete config (voice, sample, mod, mixer, sends, inserts)
- Metadata: name, author, tags, category, description, timestamps
- Searchable and browsable

**Bank:** an ordered collection of presets (like a soundfont bank or user folder)
- No duplicates within a bank
- Read-only banks for factory presets

**Session:** a snapshot of *everything*: all patches, mixer state, aux buses, master bus, tempo, time signature
- Restore replaces all state atomically — no partial loads

**Ports:**
- `PresetCodec` — serde-based serialization; versioned for forward compatibility
- `PresetStorage` — filesystem adapter (~/.crest-synth/presets/, sessions/)

**App services:**
- `PresetBrowser` — search, filter, preview, load-into-patch, save-from-patch, import SF2, export bank
- `SessionManager` — save/load/list/delete sessions

### RealTime — Lock-Free Boundary

The seam between the audio thread and everything else.

**Components:**
- `EventRingBuffer` — SPSC lock-free ring buffer (rtrb) for discrete messages. Producer: UI/MIDI thread. Consumer: audio thread.
- `ParameterBridge` — triple_buffer: writer publishes parameter snapshots, reader always gets latest without blocking.
- `DeferredDeallocator` — basedrop: audio thread retires owned memory, background thread frees later.

**Boundary message types:** NoteOn, NoteOff, ControlChange, PatchChange, PresetLoad, ParameterUpdate

### Shell — Infrastructure Adapters

Ports and adapters for external systems.

**Audio I/O:**
- `AudioOutput` (cpal) — opens stream, provides callback that must be lock-free
- Adapter: `CpalAudioOutput`

**MIDI I/O:**
- `MidiInput` (midir) — list ports, connect, disconnect
- `MidiNormalizer` — converts raw MIDI 1.0 bytes to normalized MidiEvent with NoteId
- Adapter: `MidirMidiInput`

**Window & GUI:**
- `AppWindow` (eframe) — creates window, runs render loop
- `GuiRenderer` (egui) — renders patch editor, mixer view, preset browser, mod matrix editor
- `GamepadInput` (gilrs) — gamepad events for Steam Deck navigation

## GUI Views

### Patch Editor
- Oscillator section: waveform selector, detune, pulse width, unison
- Filter section: type, cutoff, resonance, drive, key tracking, envelope amount
- Envelope section: ADSR for amp, filter, pitch (visual curves)
- Engine type selector (VA, wavetable, sample, FM)
- Sample set browser (when in sample mode)

### Mixer View
- Channel strips with faders, pan knobs, mute/solo buttons
- Insert FX slots (click to edit chain)
- Send level knobs per bus
- Aux bus returns
- Master bus with limiter
- Peak meters on every channel

### Preset Browser
- Search by name, filter by tags/category
- Bank navigation
- Preview (play a test note with the preset)
- Save/load/import/export

### Modulation Matrix Editor
- Grid: sources as rows, destinations as columns
- Amount knobs at intersections
- LFO config panels
- Curve selector per route

### MIDI/Channel Config
- Per-patch channel assignment
- MPE zone setup
- MIDI learn (CC to parameter mapping)

## Gamepad Controls (Steam Deck)

Navigation-first design: every UI action reachable via gamepad.

- D-pad: navigate between sections/params
- Left stick: fine-adjust selected parameter
- Right stick: scroll lists
- A: select/confirm
- B: back/cancel
- Triggers: switch between views (patch/mixer/preset/mod)
- Bumpers: switch between patches
- Start: save session
- Select: open preset browser

## Dependencies

| Crate | Purpose |
|-------|---------|
| cpal | Audio output (cross-platform) |
| midir | MIDI input (cross-platform) |
| eframe + egui | GUI framework |
| gilrs | Gamepad input |
| rtrb | Lock-free SPSC ring buffer |
| triple_buffer | Lock-free latest-wins parameter sharing |
| basedrop | Deferred deallocation for real-time |
| serde + serde_json | Preset/session serialization |
| symphonia | Audio file decoding (WAV, etc.) |

## Architectural Invariants

### Real-Time Safety
1. The audio thread must never allocate heap memory
2. The audio thread must never acquire a mutex or blocking lock
3. The audio thread must never perform blocking I/O
4. All parameter changes cross the boundary via ParameterBridge or EventRingBuffer
5. Retired memory from the audio thread is freed via DeferredDeallocator
6. No dynamic dispatch in the inner sample loop

### Signal Flow
7. Signal flow: Engine/SamplePlayer → ChannelStrip inserts → volume/pan → send taps + bus → AuxBus inserts → master bus inserts → limiter → output
8. Insert chains process in slot order; no feedback loops within a chain
9. Send taps are post-fader by default; pre-fader is opt-in per send

### MIDI Routing
10. A MidiEvent is dispatched to exactly the patches whose ChannelMapping matches
11. MPE zones do not overlap across patches

### Persistence
12. Presets use versioned serialization; older versions are migrated on load
13. Restoring a session replaces all state atomically — no partial loads

## Context Map (relationships between bounded contexts)

```
Kernel ──shared-kernel──→ Engine, Sample, Effects, Mixer, Modulation, Patch, Preset, RealTime

Engine ──customer-supplier──→ Patch (upstream)
Sample ──customer-supplier──→ Patch (upstream)
Modulation ──customer-supplier──→ Patch (upstream)
Effects ──customer-supplier──→ Mixer (upstream)
Patch ──customer-supplier──→ Preset (upstream)

Mixer ──anti-corruption──→ RealTime (downstream)
Patch ──anti-corruption──→ RealTime (downstream)
Shell ──anti-corruption──→ RealTime (downstream)
```

## Development Phases (rough)

1. **Plumbing that makes noise** — cpal output + midir input + sine voice. On note-on play a sine; on note-off stop it.
2. **Real polyphonic engine** — voice allocation, wavetable oscillator, resonant filter, amp envelope. Replace sine with real synth.
3. **Lock-free boundary** — rtrb + triple_buffer + basedrop. Move parameters across the RT seam.
4. **Effects** — reverb + delay. Insert chain on channel strip.
5. **Mixer** — channel strips, sends, aux buses, master bus with limiter.
6. **Sample playback** — WAV loading, zone mapping, interpolated playback.
7. **Soundfont support** — SF2 loading, multi-preset import.
8. **Modulation** — mod matrix, 4 LFOs, per-note expression (MPE).
9. **Preset system** — save/load presets and banks, import/export.
10. **Session & UI** — full session save/restore, gamepad navigation, all editor views.
