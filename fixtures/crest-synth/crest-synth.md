# crest-synth — phased fixture

This directory contains a phased breakdown of the crest-synth domain (a standalone gamepad-controlled MIDI synthesizer) for testing iterative crest-spec construction. Each phase file is a **cumulative** crest-spec — phase N contains everything from phases 1 through N.

The domain is modeled fresh from `instrument-core.md` requirements. These phases decompose the build order into ten discrete, testable snapshots.

## Phases

1. **Plumbing that makes noise.** App shell + cpal output + midir input. Note-on plays a sine; note-off stops it. Normalizes MIDI input into (group, channel) + high-res + note-id events from day one.
2. **A real polyphonic engine.** Replaces SineVoice with a Synth context: wavetable/VA engine, voice allocation with stealing, oscillator + filter + amp envelope.
3. **Harden the real-time seam.** Introduces RealTime context with rtrb + triple_buffer + basedrop. All parameter changes cross the lock-free boundary. RT safety invariants added.
4. **Multiple patches subscribed to channels.** Patch context: per-patch voice pools, (group, channel) dispatch, MPE zone support, global mixer.
5. **Modulation.** Modulation context: ModMatrix aggregate, LFOs, envelopes, per-note expression (X/Y/Z) reserved as first-class mod sources for MPE readiness.
6. **Sample-playback engine.** SampleLibrary context: SF2/WAV loading via symphonia, key/velocity zones, sample interpolation. Synth gains SamplePlayer engine type.
7. **Effects.** Effects context: per-patch and global effect chains (reverb, chorus, delay via fundsp). Signal flow formalized in invariants.
8. **Patches and presets.** Presets context: Preset/PresetBank/Setup aggregates, PresetCodec port, PresetBrowser service. serde save/load for patches and full sessions.
9. **Gamepad UX polish + Steam Deck packaging.** Shell gains GamepadInput + GuiRenderer ports, navigation and glyph services. All adapters declared. Full context map and architectural invariants.
10. **(Optional) Plugin wrapper.** Plugin context: nih-plug shell over the same engine library for CLAP/VST3. PluginHost port, PluginParameter entity, NihPlugAdapter. Cross-format preset compatibility invariants.

## Bounded contexts (by final phase)

| Context | Introduced | Purpose |
|---------|-----------|---------|
| Kernel | Phase 1 | Shared MIDI/audio value types |
| Shell | Phase 1 | App shell: audio, MIDI, window, gamepad, GUI |
| Audio | Phase 1 only | Throwaway sine voice (replaced by Synth in phase 2) |
| Synth | Phase 2 | Polyphonic engine: voices, allocator, pluggable engine types |
| RealTime | Phase 3 | Lock-free boundary: rtrb, triple_buffer, basedrop |
| Patch | Phase 4 | Patch management: instruments subscribed to channels |
| Modulation | Phase 5 | Mod sources, destinations, routing matrix |
| SampleLibrary | Phase 6 | Sample loading, zones, interpolation |
| Effects | Phase 7 | Per-patch and global FX chains |
| Presets | Phase 8 | Persistence: presets, banks, setup snapshots |
| Plugin | Phase 10 | CLAP/VST3 wrapper via nih-plug |

## Test strategy

A test suite iterates through phases in order, validating that each is a well-formed crest-spec and that each phase is a superset of the previous one (with the exception of phase 1's Audio context being replaced by Synth in phase 2).
