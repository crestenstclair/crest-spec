# Goal-oriented crest-synth target sample

[`crest-synth.cue`](crest-synth.cue) is a compact target-schema sample derived from the existing `fixtures/crest-synth/spec` project. It intentionally reuses crest-synth's real bounded contexts and resource names while showing how the proposed goal-oriented functionality composes with them.

The sample is a design artifact for the planned schema; it is not expected to validate with the current crest-spec implementation. It becomes an executable fixture as WS1, WS2, and WS6 land.

## What it demonstrates

The sample has three layers:

1. **Intent:** actors, goals, capabilities, functional/nonfunctional requirements, acceptance journeys, evidence requirements, and completion rules.
2. **DDD implementation:** existing value objects, aggregates, domain services, application services, ports, adapters, and their capability contributions.
3. **Artifacts:** the existing manifest and verification binary represented as structured assets.

It deliberately does not add generic workflow, state-machine, screen, endpoint, event-schema, or external-integration resources:

- `Voice` is the stateful voice-lifecycle state machine because that behavior belongs to an aggregate.
- `SessionManager` and `MixerController` are workflows because use-case coordination belongs to application services.
- `MidiEvent` is the event schema because shared messages belong to value objects and port contracts.
- `MidirMidiInput`, `EguiRenderer`, `GilrsGamepadInput`, and `CpalAudioOutput` remain adapters; profiles only describe their MIDI/UI/device boundary mechanics.
- `ToneTestMain` remains an asset; its verification-harness profile describes why the generated binary exists.

## Expected runtime trace

For the required `goal.play_an_instrument`, the future engine should produce a trace similar to:

```text
goal.play_an_instrument
  -> capability.render_audible_polyphony
  -> acceptance.audible_a440
  -> aggregate.Engine.Voice
  -> domainService.Engine.VoiceAllocator
  -> domainService.Engine.EngineRenderer
  -> port.Shell.MidiInput
  -> adapter.MidirMidiInput
  -> port.Shell.AudioOutput
  -> adapter.CpalAudioOutput
  -> asset.ToneTestMain
  -> witness.audible_tone
  -> evidence.audible_tone
  -> goal complete
```

Calling `spec/context` for one of those resources creates the context manifest and attempt in SQLite. The external host then records the execution manifest, role, candidate, and retry metadata. crest-spec executes the declared validation/witness, persists raw evidence and provenance, updates goal status, and exposes the full trace in the dashboard. None of those runtime records are added to the CUE file or project tree.

## Evaluation use

After a historical run has complete context, execution, candidate, validation, and evidence provenance, WS7 may turn the attempt into an immutable SQLite evaluation case. The CUE sample does not declare evaluation runs because evaluation configuration and results are operational state, not intended project architecture.
