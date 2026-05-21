# tracker — reference project spec

A worked example of a project built using crest-spec. The tracker is a music-composition tool in the lineage of LSDJ, M8, Renoise, and OctaTrack. This document declares its architecture in the DDD vocabulary crest-spec uses: bounded contexts, aggregates, value objects, commands, events, ports, adapters, and the context map between them.

This is a specification, not implementation. Each declaration here corresponds to a resource in a `crest-spec.ts` file, which the crest-spec planner realizes into actual TypeScript.

## Overview

The tracker is a music-composition tool in the lineage of LSDJ, M8, Renoise, and OctaTrack. The user authors songs as a hierarchy of chains, phrases, patches, and tables, plays them back through synth and sampler engines, and interacts via keyboard or gamepad. The architecture is split into eleven bounded contexts, each with its own ubiquitous language and consistency boundary.

## Bounded contexts

1. **Composition** — the structural model of a song. Aggregates: Song, Chain, Phrase family (polymorphic), Patch, Table, Chord. Value objects for IDs, time units, and musical primitives. Pure domain; no IO, no runtime concerns.
2. **Synthesis** — oscillator-based sound generation. Aggregates: Synth, Voice, Envelope, Filter. Produces audio buffers.
3. **Sampling** — sample-based sound generation. Aggregates: Sample, SamplePlayer, LoopPoint. Produces audio buffers.
4. **MIDIEffects** — note-event transformations referenced by Patches. Aggregates: Arpeggiator, ChordGenerator, NoteRepeater (and others over time). Each takes a stream of note events and produces a transformed stream.
5. **AudioEffects** — DSP referenced by Patches. Aggregates: Reverb, Delay, Filter, Distortion (and others). Each takes an audio buffer and produces a transformed buffer.
6. **Mixer** — channel routing, levels, pan, sends. Aggregates: Channel, Send, MasterBus. No effects (those are in Patches).
7. **Playback** — scheduling and emission of timed note events. Aggregates: Transport, Sequencer, Playhead, Groove. Reads from Composition, walks the chain/phrase/table structure, applies groove timing, emits scheduled note and parameter events.
8. **Performance** — live, unscheduled note input. Aggregates: ChordPad, Preview, LiveInput. Emits note events immediately rather than via the Sequencer. Used for chord-mode triggering and phrase preview while editing.
9. **MIDI** — external MIDI I/O and routing. Aggregates: MIDIInput, MIDIOutput, MIDIRoute. Translates between internal note representation and MIDI bytes.
10. **Editor** — what the user is currently editing. Aggregates: Cursor, Selection, ViewState, EditMode. Per-phrase-type views live here.
11. **Controls** — input translation. Aggregates: KeyBinding, GamepadBinding, InputMap. Translates raw input events into commands directed at other contexts.

Persistence is not a context; it is infrastructure, with one repository adapter per aggregate root.

## Cross-cutting value objects

These are used across multiple contexts and should be declared in a shared kernel:

```ts
const kernel = app.context("Kernel", {
  purpose: "primitives shared across contexts",
})

kernel.valueObject("Ticks", { from: "number", description: "musical time in 1/96-note ticks" })
kernel.valueObject("BPM", { from: "number", invariants: ["between 20 and 999"] })
kernel.valueObject("Note", { from: "number", invariants: ["MIDI note 0..127"] })
kernel.valueObject("Velocity", { from: "number", invariants: ["0..127"] })
kernel.valueObject("Instant", { from: "number", description: "wall-clock milliseconds for live events" })
kernel.valueObject("TickRange", { state: { start: "Ticks", end: "Ticks" } })
```

Shared kernel is the one DDD relationship that allows direct sharing of model elements between contexts. Use sparingly.

## Composition context

The structural heart of the tracker. All authored content lives here.

### Value objects

```ts
composition.valueObject("SongId", { from: "string", format: "uuid" })
composition.valueObject("ChainId", { from: "string", format: "uuid" })
composition.valueObject("PhraseId", { from: "string", format: "uuid" })
composition.valueObject("PatchId", { from: "string", format: "uuid" })
composition.valueObject("TableId", { from: "string", format: "uuid" })
composition.valueObject("ChordId", { from: "string", format: "uuid" })

composition.valueObject("Index", { from: "number", invariants: ["non-negative integer"] })
composition.valueObject("StepIndex", { from: "number", invariants: ["0..255"] })

composition.valueObject("MIDIEffectRef", { state: { id: "string", context: "string" } })
composition.valueObject("EngineRef", { state: { id: "string", context: "string" } })
composition.valueObject("AudioEffectRef", { state: { id: "string", context: "string" } })

composition.valueObject("Scale", { from: "string", description: "scale name (major, minor, dorian, etc.)" })
composition.valueObject("Transpose", { from: "number", invariants: ["-127..127"] })
```

### Ports

```ts
const phraseRender = composition.port("PhraseRender", {
  contract: {
    render: "(context: MusicalContext, range: TickRange) => NoteEvent[]"
  },
})

const tableRender = composition.port("TableRender", {
  contract: {
    render: "(context: MusicalContext, range: TickRange) => ParameterEvent[]"
  },
})
```

### Aggregates

**`MusicalContext`** — a value-object-heavy aggregate read by stochastic and generative phrases.

```ts
const musicalContext = composition.aggregate("MusicalContext", {
  root: true,
  state: {
    activeChord: "ChordId | null",
    scale: "Scale",
    transpose: "Transpose",
  },
  commands: [
    command("SetActiveChord", { id: "ChordId | null" }),
    command("SetScale", { scale: "Scale" }),
    command("SetTranspose", { transpose: "Transpose" }),
  ],
  events: [
    event("ActiveChordChanged", { from: "ChordId | null", to: "ChordId | null" }),
    event("ScaleChanged", { from: "Scale", to: "Scale" }),
    event("TransposeChanged", { from: "Transpose", to: "Transpose" }),
  ],
})
```

**`Song`** — the top-level aggregate.

```ts
const song = composition.aggregate("Song", {
  root: true,
  state: {
    id: "SongId",
    name: "string",
    tempo: "BPM",
    chains: "ChainId[]",
  },
  invariants: [
    "tempo is between 20 and 999 BPM",
    "chain IDs are unique within the song",
  ],
  commands: [
    command("RenameSong", { name: "string" }),
    command("SetTempo", { bpm: "BPM" }),
    command("AddChain", { id: "ChainId", at: "Index" }),
    command("RemoveChain", { id: "ChainId" }),
    command("MoveChain", { id: "ChainId", to: "Index" }),
  ],
  events: [
    event("SongRenamed", { id: "SongId", name: "string" }),
    event("SongTempoChanged", { id: "SongId", from: "BPM", to: "BPM" }),
    event("ChainAddedToSong", { id: "SongId", chainId: "ChainId", at: "Index" }),
    event("ChainRemovedFromSong", { id: "SongId", chainId: "ChainId" }),
    event("ChainMovedInSong", { id: "SongId", chainId: "ChainId", from: "Index", to: "Index" }),
  ],
})
```

**`Chain`** — an ordered list of phrase references with optional transpose per slot.

```ts
const chain = composition.aggregate("Chain", {
  root: true,
  state: {
    id: "ChainId",
    name: "string",
    slots: "ChainSlot[]",
  },
  commands: [
    command("RenameChain", { name: "string" }),
    command("SetSlot", { at: "Index", phraseId: "PhraseId | null", transpose: "Transpose" }),
    command("ClearSlot", { at: "Index" }),
  ],
  events: [
    event("ChainRenamed", { id: "ChainId", name: "string" }),
    event("ChainSlotSet", { id: "ChainId", at: "Index", phraseId: "PhraseId | null", transpose: "Transpose" }),
    event("ChainSlotCleared", { id: "ChainId", at: "Index" }),
  ],
})

chain.entity("ChainSlot", {
  state: { at: "Index", phraseId: "PhraseId | null", transpose: "Transpose" },
})
```

**`Phrase` family** — polymorphic aggregates implementing `PhraseRender`.

```ts
// Common across all phrase types
const linearPhrase = composition.aggregate("LinearPhrase", {
  root: true,
  implements: phraseRender,
  state: {
    id: "PhraseId",
    name: "string",
    length: "number",          // number of steps
    patchId: "PatchId | null", // optional default patch
    tableId: "TableId | null", // optional default table
    steps: "Step[]",
  },
  commands: [
    command("RenameLinearPhrase", { name: "string" }),
    command("SetLength", { length: "number" }),
    command("SetStep", { at: "StepIndex", note: "Note | null", velocity: "Velocity" }),
    command("ClearStep", { at: "StepIndex" }),
    command("SetPhrasePatch", { patchId: "PatchId | null" }),
    command("SetPhraseTable", { tableId: "TableId | null" }),
  ],
  events: [
    event("LinearPhraseRenamed", { id: "PhraseId", name: "string" }),
    event("LinearPhraseLengthChanged", { id: "PhraseId", from: "number", to: "number" }),
    event("LinearPhraseStepSet", { id: "PhraseId", at: "StepIndex", note: "Note | null", velocity: "Velocity" }),
    event("LinearPhraseStepCleared", { id: "PhraseId", at: "StepIndex" }),
  ],
})

linearPhrase.entity("Step", {
  state: { at: "StepIndex", note: "Note | null", velocity: "Velocity" },
})

const drumPhrase = composition.aggregate("DrumPhrase", {
  root: true,
  implements: phraseRender,
  state: {
    id: "PhraseId",
    name: "string",
    length: "number",
    lanes: "DrumLane[]", // each lane targets a different patch
  },
  commands: [
    command("RenameDrumPhrase", { name: "string" }),
    command("SetLaneStep", { lane: "Index", at: "StepIndex", on: "boolean", velocity: "Velocity" }),
    command("SetLanePatch", { lane: "Index", patchId: "PatchId" }),
    command("AddLane", { patchId: "PatchId" }),
    command("RemoveLane", { lane: "Index" }),
  ],
  events: [
    event("DrumLaneStepSet", { id: "PhraseId", lane: "Index", at: "StepIndex", on: "boolean", velocity: "Velocity" }),
    event("DrumLanePatchSet", { id: "PhraseId", lane: "Index", patchId: "PatchId" }),
    event("DrumLaneAdded", { id: "PhraseId", lane: "Index", patchId: "PatchId" }),
    event("DrumLaneRemoved", { id: "PhraseId", lane: "Index" }),
  ],
})

drumPhrase.entity("DrumLane", {
  state: { lane: "Index", patchId: "PatchId", steps: "boolean[]", velocities: "Velocity[]" },
})

const euclideanPhrase = composition.aggregate("EuclideanPhrase", {
  root: true,
  implements: phraseRender,
  state: {
    id: "PhraseId",
    name: "string",
    steps: "number",       // total steps in the pattern
    pulses: "number",      // number of "on" events distributed across steps
    rotation: "number",    // pattern offset
    note: "Note",          // single note value the pattern fires
    velocity: "Velocity",
    patchId: "PatchId | null",
  },
  invariants: [
    "pulses <= steps",
    "rotation >= 0 && rotation < steps",
  ],
  commands: [
    command("RenameEuclideanPhrase", { name: "string" }),
    command("SetEuclideanParams", { steps: "number", pulses: "number", rotation: "number" }),
    command("SetEuclideanNote", { note: "Note", velocity: "Velocity" }),
  ],
  events: [
    event("EuclideanParamsChanged", { id: "PhraseId", steps: "number", pulses: "number", rotation: "number" }),
    event("EuclideanNoteChanged", { id: "PhraseId", note: "Note", velocity: "Velocity" }),
  ],
})

const stochasticPhrase = composition.aggregate("StochasticPhrase", {
  root: true,
  implements: phraseRender,
  state: {
    id: "PhraseId",
    name: "string",
    length: "number",
    steps: "StochasticStep[]", // each step has a probability and a set of possible notes
    patchId: "PatchId | null",
  },
  commands: [
    command("SetStochasticStep", { at: "StepIndex", probability: "number", candidates: "Note[]" }),
    command("ClearStochasticStep", { at: "StepIndex" }),
  ],
  events: [
    event("StochasticStepSet", { id: "PhraseId", at: "StepIndex", probability: "number", candidates: "Note[]" }),
    event("StochasticStepCleared", { id: "PhraseId", at: "StepIndex" }),
  ],
})

stochasticPhrase.entity("StochasticStep", {
  state: { at: "StepIndex", probability: "number", candidates: "Note[]", velocity: "Velocity" },
})

const generativePhrase = composition.aggregate("GenerativePhrase", {
  root: true,
  implements: phraseRender,
  state: {
    id: "PhraseId",
    name: "string",
    length: "number",
    generator: "GeneratorKind",     // "arpeggio" | "scale-walk" | "expression"
    params: "GeneratorParams",       // shape depends on generator
    patchId: "PatchId | null",
  },
  commands: [
    command("SetGenerator", { generator: "GeneratorKind", params: "GeneratorParams" }),
  ],
  events: [
    event("GeneratorChanged", { id: "PhraseId", generator: "GeneratorKind", params: "GeneratorParams" }),
  ],
})
```

**`Patch`** — the signal-chain composition object.

```ts
const patch = composition.aggregate("Patch", {
  root: true,
  state: {
    id: "PatchId",
    name: "string",
    midiEffects: "MIDIEffectRef[]",   // ordered chain, applied before the engine
    engine: "EngineRef",               // the sound generator
    audioEffects: "AudioEffectRef[]",  // ordered chain, applied after the engine
  },
  commands: [
    command("RenamePatch", { name: "string" }),
    command("SetEngine", { engine: "EngineRef" }),
    command("AddMIDIEffect", { ref: "MIDIEffectRef", at: "Index" }),
    command("RemoveMIDIEffect", { at: "Index" }),
    command("MoveMIDIEffect", { from: "Index", to: "Index" }),
    command("AddAudioEffect", { ref: "AudioEffectRef", at: "Index" }),
    command("RemoveAudioEffect", { at: "Index" }),
    command("MoveAudioEffect", { from: "Index", to: "Index" }),
  ],
  events: [
    event("PatchRenamed", { id: "PatchId", name: "string" }),
    event("PatchEngineSet", { id: "PatchId", engine: "EngineRef" }),
    event("PatchMIDIEffectAdded", { id: "PatchId", ref: "MIDIEffectRef", at: "Index" }),
    event("PatchMIDIEffectRemoved", { id: "PatchId", at: "Index" }),
    event("PatchMIDIEffectMoved", { id: "PatchId", from: "Index", to: "Index" }),
    event("PatchAudioEffectAdded", { id: "PatchId", ref: "AudioEffectRef", at: "Index" }),
    event("PatchAudioEffectRemoved", { id: "PatchId", at: "Index" }),
    event("PatchAudioEffectMoved", { id: "PatchId", from: "Index", to: "Index" }),
  ],
})
```

**`Table`** — modulation aggregate, implements `TableRender`.

```ts
const table = composition.aggregate("Table", {
  root: true,
  implements: tableRender,
  state: {
    id: "TableId",
    name: "string",
    length: "number",
    rows: "TableRow[]", // each row is a per-step parameter change
  },
  commands: [
    command("RenameTable", { name: "string" }),
    command("SetTableLength", { length: "number" }),
    command("SetTableRow", { at: "StepIndex", parameter: "string", value: "number" }),
    command("ClearTableRow", { at: "StepIndex" }),
  ],
  events: [
    event("TableRenamed", { id: "TableId", name: "string" }),
    event("TableLengthChanged", { id: "TableId", from: "number", to: "number" }),
    event("TableRowSet", { id: "TableId", at: "StepIndex", parameter: "string", value: "number" }),
    event("TableRowCleared", { id: "TableId", at: "StepIndex" }),
  ],
})

table.entity("TableRow", {
  state: { at: "StepIndex", parameter: "string", value: "number" },
})
```

**`Chord`** — a value-object-heavy aggregate; reusable harmonic definitions.

```ts
const chord = composition.aggregate("Chord", {
  root: true,
  state: {
    id: "ChordId",
    name: "string",
    notes: "Note[]",
  },
  commands: [
    command("RenameChord", { name: "string" }),
    command("SetChordNotes", { notes: "Note[]" }),
  ],
  events: [
    event("ChordRenamed", { id: "ChordId", name: "string" }),
    event("ChordNotesSet", { id: "ChordId", notes: "Note[]" }),
  ],
})
```

### Repositories

One per aggregate root:

```ts
composition.repository("SongRepository",          { of: song })
composition.repository("ChainRepository",         { of: chain })
composition.repository("LinearPhraseRepository",  { of: linearPhrase })
composition.repository("DrumPhraseRepository",    { of: drumPhrase })
composition.repository("EuclideanPhraseRepository", { of: euclideanPhrase })
composition.repository("StochasticPhraseRepository", { of: stochasticPhrase })
composition.repository("GenerativePhraseRepository", { of: generativePhrase })
composition.repository("PatchRepository",         { of: patch })
composition.repository("TableRepository",         { of: table })
composition.repository("ChordRepository",         { of: chord })
composition.repository("MusicalContextRepository", { of: musicalContext })
```

Each repository contract has `findById`, `save`, `delete`, and a `list` operation. The infrastructure layer provides one adapter per repository (filesystem-backed for v1).

## Synthesis context

```ts
const synthesis = app.context("Synthesis", {
  purpose: "oscillator-based sound generation",
})

const audioBuffer = synthesis.port("AudioBufferProducer", {
  contract: { produce: "(noteEvent: NoteEvent, frames: number) => Float32Array" },
})

const synth = synthesis.aggregate("Synth", {
  root: true,
  implements: audioBuffer,
  state: {
    id: "string",
    name: "string",
    oscillators: "OscillatorConfig[]",
    envelope: "EnvelopeConfig",
    filter: "FilterConfig",
    polyphony: "number",
  },
  commands: [ /* set oscillator, envelope, filter params */ ],
  events: [ /* corresponding events */ ],
})

synthesis.aggregate("Voice", {
  root: true,
  state: {
    id: "string",
    active: "boolean",
    note: "Note | null",
    velocity: "Velocity",
    startedAt: "Instant",
  },
})
```

## Sampling context

```ts
const sampling = app.context("Sampling", {
  purpose: "sample-based sound generation",
})

const sample = sampling.aggregate("Sample", {
  root: true,
  state: {
    id: "string",
    name: "string",
    path: "string",           // file path to the audio file
    loopPoints: "LoopPoint | null",
    rootNote: "Note",
    polyphony: "number",
  },
  commands: [ /* set loop points, root note, polyphony */ ],
  events: [ /* ... */ ],
})

sampling.valueObject("LoopPoint", { state: { start: "number", end: "number", loopMode: "string" } })

sampling.aggregate("SamplePlayer", {
  root: true,
  implements: audioBuffer, // same port as Synthesis
  state: { /* runtime state */ },
})
```

## MIDIEffects context

```ts
const midiEffects = app.context("MIDIEffects", {
  purpose: "transformations applied to streams of note events",
})

const noteTransform = midiEffects.port("NoteTransform", {
  contract: { transform: "(events: NoteEvent[]) => NoteEvent[]" },
})

midiEffects.aggregate("Arpeggiator", {
  root: true,
  implements: noteTransform,
  state: { id: "string", name: "string", pattern: "string", rate: "Ticks", octaveRange: "number" },
  commands: [ /* set pattern, rate, octave range */ ],
  events: [ /* ... */ ],
})

midiEffects.aggregate("ChordGenerator", {
  root: true,
  implements: noteTransform,
  state: { id: "string", name: "string", voicing: "Note[]" },
  commands: [ /* set voicing */ ],
  events: [ /* ... */ ],
})

midiEffects.aggregate("NoteRepeater", {
  root: true,
  implements: noteTransform,
  state: { id: "string", name: "string", repeats: "number", interval: "Ticks", decay: "number" },
  commands: [ /* set repeats, interval, decay */ ],
  events: [ /* ... */ ],
})
```

## AudioEffects context

```ts
const audioEffectsCtx = app.context("AudioEffects", {
  purpose: "DSP applied to audio buffers",
})

const audioTransform = audioEffectsCtx.port("AudioTransform", {
  contract: { transform: "(buffer: Float32Array) => Float32Array" },
})

audioEffectsCtx.aggregate("Reverb", {
  root: true,
  implements: audioTransform,
  state: { id: "string", name: "string", roomSize: "number", damping: "number", wet: "number", dry: "number" },
  commands: [ /* set params */ ],
  events: [ /* ... */ ],
})

audioEffectsCtx.aggregate("Delay", {
  root: true,
  implements: audioTransform,
  state: { id: "string", name: "string", time: "Ticks", feedback: "number", wet: "number" },
  commands: [ /* set params */ ],
  events: [ /* ... */ ],
})

audioEffectsCtx.aggregate("FilterEffect", {
  root: true,
  implements: audioTransform,
  state: { id: "string", name: "string", mode: "string", cutoff: "number", resonance: "number" },
  commands: [ /* set params */ ],
  events: [ /* ... */ ],
})

audioEffectsCtx.aggregate("Distortion", {
  root: true,
  implements: audioTransform,
  state: { id: "string", name: "string", drive: "number", tone: "number", mix: "number" },
  commands: [ /* set params */ ],
  events: [ /* ... */ ],
})
```

## Mixer context

```ts
const mixer = app.context("Mixer", {
  purpose: "routing of audio from patches to outputs; no effects",
})

const channel = mixer.aggregate("Channel", {
  root: true,
  state: {
    id: "string",
    name: "string",
    level: "number",   // 0..1
    pan: "number",     // -1..1
    muted: "boolean",
    soloed: "boolean",
    sends: "Send[]",
  },
  commands: [
    command("SetChannelLevel", { level: "number" }),
    command("SetChannelPan", { pan: "number" }),
    command("MuteChannel"),
    command("UnmuteChannel"),
    command("SoloChannel"),
    command("UnsoloChannel"),
    command("AddSend", { to: "string", level: "number" }),
    command("RemoveSend", { to: "string" }),
  ],
  events: [ /* ... */ ],
})

channel.entity("Send", { state: { to: "string", level: "number" } })

mixer.aggregate("MasterBus", {
  root: true,
  state: { level: "number", limiterEnabled: "boolean" },
  commands: [ /* ... */ ],
  events: [ /* ... */ ],
})
```

## Playback context

The runtime engine. Reads Composition, walks the structure, schedules events.

```ts
const playback = app.context("Playback", {
  purpose: "scheduling and emission of timed note events; owns groove",
  meta: {
    rules: [
      "tempo changes must be lock-free",
      "all time arithmetic uses Ticks; never mix with seconds at this layer",
    ],
    avoid: ["setInterval", "Date.now()"],
    style: "single-threaded scheduler, no shared mutable state",
  },
})

playback.valueObject("NoteEvent", {
  state: { tick: "Ticks", note: "Note", velocity: "Velocity", patchId: "PatchId", channel: "number", duration: "Ticks" },
})

playback.valueObject("ParameterEvent", {
  state: { tick: "Ticks", parameter: "string", value: "number", channel: "number" },
})

const transport = playback.aggregate("Transport", {
  root: true,
  state: {
    playing: "boolean",
    looping: "boolean",
    loopStart: "Ticks",
    loopEnd: "Ticks",
    position: "Ticks",
    tempo: "BPM",
  },
  commands: [
    command("Play"),
    command("Stop"),
    command("Seek", { to: "Ticks" }),
    command("SetLooping", { looping: "boolean", start: "Ticks", end: "Ticks" }),
    command("SetTransportTempo", { bpm: "BPM" }),
  ],
  events: [
    event("Played"),
    event("Stopped"),
    event("Seeked", { from: "Ticks", to: "Ticks" }),
    event("LoopingChanged", { looping: "boolean", start: "Ticks", end: "Ticks" }),
    event("TransportTempoChanged", { from: "BPM", to: "BPM" }),
  ],
})

const sequencer = playback.aggregate("Sequencer", {
  root: true,
  state: {
    activeChainId: "ChainId | null",
    activePhrasePerTrack: "(PhraseId | null)[]",
  },
  commands: [
    command("LoadChain", { id: "ChainId" }),
    command("AdvanceTick", { delta: "Ticks" }),
  ],
  events: [
    event("ChainLoaded", { id: "ChainId" }),
    event("NotesEmitted", { events: "NoteEvent[]" }),
    event("ParametersEmitted", { events: "ParameterEvent[]" }),
  ],
})

const playhead = playback.aggregate("Playhead", {
  root: true,
  state: {
    perTrackPosition: "PlayheadPosition[]",
  },
  commands: [
    command("UpdatePlayhead", { positions: "PlayheadPosition[]" }),
  ],
  events: [
    event("PlayheadAdvanced", { positions: "PlayheadPosition[]" }),
  ],
})

playhead.entity("PlayheadPosition", {
  state: { track: "Index", chainSlot: "Index", phraseStep: "StepIndex" },
})

const groove = playback.aggregate("Groove", {
  root: true,
  state: {
    id: "string",
    name: "string",
    swing: "number",        // 0..1
    microtimingTemplate: "Ticks[]", // per-step offset
  },
  commands: [
    command("SetSwing", { swing: "number" }),
    command("SetMicrotiming", { template: "Ticks[]" }),
  ],
  events: [
    event("SwingChanged", { from: "number", to: "number" }),
    event("MicrotimingChanged", { template: "Ticks[]" }),
  ],
})
```

The Sequencer's tick-advance loop is the spine of the runtime: it walks the active chain, finds the active phrase per track, asks each phrase to render itself over the next tick range, applies Groove, and emits `NotesEmitted` events. Downstream contexts (MIDIEffects, Synthesis, Sampling, MIDI) subscribe.

## Performance context

```ts
const performance = app.context("Performance", {
  purpose: "live, unscheduled note input bypassing the sequencer",
})

const chordPad = performance.aggregate("ChordPad", {
  root: true,
  state: {
    activeChordId: "ChordId | null",
    voicing: "Note[]",
    patchId: "PatchId | null",
  },
  commands: [
    command("PressChord", { id: "ChordId" }),
    command("ReleaseChord"),
  ],
  events: [
    event("ChordTriggered", { notes: "Note[]", patchId: "PatchId | null", at: "Instant" }),
    event("ChordReleased", { notes: "Note[]", at: "Instant" }),
  ],
})

const preview = performance.aggregate("Preview", {
  root: true,
  state: { activePhraseId: "PhraseId | null" },
  commands: [
    command("StartPreview", { phraseId: "PhraseId" }),
    command("StopPreview"),
  ],
  events: [
    event("PreviewStarted", { phraseId: "PhraseId" }),
    event("PreviewStopped", { phraseId: "PhraseId" }),
  ],
})
```

## MIDI context

```ts
const midi = app.context("MIDI", {
  purpose: "external MIDI I/O and routing",
})

const midiOutput = midi.port("MIDIOutput", {
  contract: {
    sendNote: "(channel: number, note: Note, velocity: Velocity, at: Instant) => void",
    sendCC: "(channel: number, controller: number, value: number, at: Instant) => void",
    sendClock: "(at: Instant) => void",
  },
})

const midiInput = midi.port("MIDIInput", {
  contract: {
    onNote: "(handler: (channel: number, note: Note, velocity: Velocity, at: Instant) => void) => Unsubscribe",
    onCC: "(handler: (channel: number, controller: number, value: number, at: Instant) => void) => Unsubscribe",
  },
})

midi.aggregate("MIDIRoute", {
  root: true,
  state: {
    id: "string",
    source: "string",     // patch id or "transport"
    destination: "string", // midi output id
    channel: "number",
    enabled: "boolean",
  },
  commands: [ /* enable/disable, set channel, etc. */ ],
  events: [ /* ... */ ],
})
```

## Editor context

What the user is currently editing. Reads from Composition and Playback; sends commands.

```ts
const editor = app.context("Editor", {
  purpose: "user-facing editing state: cursor, selection, views",
})

editor.valueObject("ViewKind", { from: "string", description: "song | chain | phrase | patch | table" })

const cursor = editor.aggregate("Cursor", {
  root: true,
  state: {
    view: "ViewKind",
    contextId: "string",  // which song/chain/phrase/etc. is open
    row: "number",
    column: "number",
  },
  commands: [
    command("MoveCursor", { direction: "string", amount: "number" }),
    command("SetCursorPosition", { row: "number", column: "number" }),
    command("OpenView", { view: "ViewKind", contextId: "string" }),
  ],
  events: [
    event("CursorMoved", { from: "object", to: "object" }),
    event("ViewOpened", { view: "ViewKind", contextId: "string" }),
  ],
})

const selection = editor.aggregate("Selection", {
  root: true,
  state: {
    start: "object | null",
    end: "object | null",
  },
  commands: [
    command("BeginSelection", { at: "object" }),
    command("ExtendSelection", { to: "object" }),
    command("ClearSelection"),
  ],
  events: [
    event("SelectionBegan", { at: "object" }),
    event("SelectionExtended", { to: "object" }),
    event("SelectionCleared"),
  ],
})

const viewState = editor.aggregate("ViewState", {
  root: true,
  state: {
    activeView: "ViewKind",
    scrollOffset: "number",
    zoom: "number",
    visibleTrackDisplay: "string", // per phrase type, the active display variant
  },
  commands: [ /* ... */ ],
  events: [ /* ... */ ],
})

const editMode = editor.aggregate("EditMode", {
  root: true,
  state: { mode: "string" }, // "edit" | "play" | "live"
  commands: [
    command("SetEditMode", { mode: "string" }),
  ],
  events: [
    event("EditModeChanged", { from: "string", to: "string" }),
  ],
})
```

Editor has per-phrase-type views, declared as ports:

```ts
const phraseView = editor.port("PhraseView", {
  contract: {
    render: "(phrase: Phrase, cursor: Cursor, selection: Selection) => UINode",
    handleKey: "(key: string, phrase: Phrase, cursor: Cursor) => Command[]",
  },
})

// One adapter per phrase type:
editor.adapter("LinearPhraseView", { implements: phraseView })
editor.adapter("DrumPhraseView", { implements: phraseView })
editor.adapter("EuclideanPhraseView", { implements: phraseView })
editor.adapter("StochasticPhraseView", { implements: phraseView })
editor.adapter("GenerativePhraseView", { implements: phraseView })
```

## Controls context

Translates input into commands.

```ts
const controls = app.context("Controls", {
  purpose: "translate raw input events into commands directed at other contexts",
})

const inputSource = controls.port("InputSource", {
  contract: {
    onEvent: "(handler: (event: InputEvent) => void) => Unsubscribe",
  },
})

controls.adapter("KeyboardInput",  { implements: inputSource, layer: "infrastructure" })
controls.adapter("GamepadInput",   { implements: inputSource, layer: "infrastructure" })
controls.adapter("MIDIInputAsControl", { implements: inputSource, layer: "infrastructure" })

const inputMap = controls.aggregate("InputMap", {
  root: true,
  state: {
    bindings: "Binding[]",
    activeProfile: "string",
  },
  commands: [
    command("BindInput", { event: "string", command: "string", target: "string" }),
    command("UnbindInput", { event: "string" }),
    command("SetActiveProfile", { profile: "string" }),
  ],
  events: [
    event("InputBound", { event: "string", command: "string", target: "string" }),
    event("InputUnbound", { event: "string" }),
    event("ProfileChanged", { from: "string", to: "string" }),
  ],
})

inputMap.entity("Binding", {
  state: { event: "string", command: "string", target: "string" },
})
```

## ContextMap (full)

```ts
app.contextMap([
  // Kernel is shared by every other context
  relationship(composition,    kernel, { kind: "shared-kernel" }),
  relationship(synthesis,      kernel, { kind: "shared-kernel" }),
  relationship(sampling,       kernel, { kind: "shared-kernel" }),
  relationship(midiEffects,    kernel, { kind: "shared-kernel" }),
  relationship(audioEffectsCtx, kernel, { kind: "shared-kernel" }),
  relationship(mixer,          kernel, { kind: "shared-kernel" }),
  relationship(playback,       kernel, { kind: "shared-kernel" }),
  relationship(performance,    kernel, { kind: "shared-kernel" }),
  relationship(midi,           kernel, { kind: "shared-kernel" }),
  relationship(editor,         kernel, { kind: "shared-kernel" }),
  relationship(controls,       kernel, { kind: "shared-kernel" }),

  // Playback consumes Composition (published language: events from Composition are stable)
  relationship(playback, composition, { kind: "customer-supplier", direction: "downstream" }),

  // Performance consumes Composition (reads chords, phrases, patches for live triggering)
  relationship(performance, composition, { kind: "customer-supplier", direction: "downstream" }),

  // Synthesis and Sampling consume note events from Playback and Performance
  relationship(synthesis, playback,     { kind: "customer-supplier", direction: "downstream" }),
  relationship(synthesis, performance,  { kind: "customer-supplier", direction: "downstream" }),
  relationship(sampling,  playback,     { kind: "customer-supplier", direction: "downstream" }),
  relationship(sampling,  performance,  { kind: "customer-supplier", direction: "downstream" }),

  // MIDIEffects sit between note sources and consumers (referenced by Patches in Composition)
  relationship(midiEffects, playback,    { kind: "customer-supplier", direction: "downstream" }),
  relationship(midiEffects, performance, { kind: "customer-supplier", direction: "downstream" }),

  // AudioEffects sit between engines and the mixer (referenced by Patches in Composition)
  relationship(audioEffectsCtx, synthesis, { kind: "customer-supplier", direction: "downstream" }),
  relationship(audioEffectsCtx, sampling,  { kind: "customer-supplier", direction: "downstream" }),

  // Mixer consumes audio from engines (via AudioEffects)
  relationship(mixer, audioEffectsCtx, { kind: "customer-supplier", direction: "downstream" }),

  // MIDI receives note events from Playback and Performance
  relationship(midi, playback,    { kind: "customer-supplier", direction: "downstream" }),
  relationship(midi, performance, { kind: "customer-supplier", direction: "downstream" }),

  // Editor reads from Composition and Playback (Playhead); sends commands to Composition
  relationship(editor, composition, { kind: "customer-supplier", direction: "both" }),
  relationship(editor, playback,    { kind: "customer-supplier", direction: "downstream" }),

  // Controls drives Editor, Playback, and Performance
  relationship(controls, editor,      { kind: "customer-supplier", direction: "downstream" }),
  relationship(controls, playback,    { kind: "customer-supplier", direction: "downstream" }),
  relationship(controls, performance, { kind: "customer-supplier", direction: "downstream" }),
])
```

## The note-event pipeline

```
[Playback]   ─┐
              ├──► [MIDIEffects] ──► [Synthesis] ──► [AudioEffects] ──┐
[Performance]─┘                  ─► [Sampling]   ──► [AudioEffects] ──┤
                                 ─► [MIDI out]                        │
                                                                      ▼
                                                                  [Mixer] ──► audio out
```

The pipeline is the ContextMap, not a runtime detail. Each arrow is a declared relationship.

## Playhead vs Cursor

- **Playhead** lives in Playback. Driven by the Sequencer clock. Represents "what is being played right now." Read-only from outside Playback.
- **Cursor** lives in Editor. Driven by Controls. Represents "what the user is editing right now." Has no meaning during pure playback.

Both rendered together in the UI, but they live in different contexts, have different lifecycles, and respond to different commands.

## Project-level invariants

```ts
app.invariants([
  invariant("domain layer has no infrastructure imports", {
    meta: { rationale: "preserves Clean Architecture's Dependency Rule" },
  }),
  invariant("all mutations go through ApplicationServices", {
    meta: { rationale: "single audit point; enables event sourcing" },
  }),
  invariant("every command has a corresponding handler", {
    meta: { rationale: "no dangling commands" },
  }),
  invariant("every aggregate has a repository", {
    meta: { rationale: "every root is persistable" },
  }),
  invariant("every public command and event has a test", {
    meta: { rationale: "behavioral contract under test" },
  }),
  invariant("contexts do not reach into each other's internals except via ContextMap relationships", {
    meta: { rationale: "context boundaries are enforced; integration is explicit" },
  }),
  invariant("Playback never mutates Composition aggregates directly", {
    meta: { rationale: "Composition is the model; Playback is the engine" },
  }),
  invariant("Editor commands targeting Composition flow through Composition's ApplicationServices", {
    meta: { rationale: "Editor cannot bypass invariants on Composition aggregates" },
  }),
])
```

## Project-level meta

```ts
app.meta({
  style: "functional, no classes; prefer pure functions and immutable data structures",
  avoid: ["any", "as unknown as", "setInterval", "Date.now() at the domain layer"],
  references: [
    "./docs/architecture.md",
    "./docs/composition-model.md",
    "./docs/timing-model.md",
    "./docs/context-map.md",
  ],
  prompts: [
    "use Ticks for all musical time; convert to seconds only at the audio buffer boundary",
    "events are past tense; commands are imperative",
    "repository operations return promises even when the adapter is synchronous, so the contract is uniform",
  ],
})
```

## Suggested file layout

Generated code is organized by context, then layer:

```
src/
  kernel/
    ticks.ts
    bpm.ts
    note.ts
    ...
  composition/
    domain/
      song.ts
      song.test.ts
      chain.ts
      linear-phrase.ts
      drum-phrase.ts
      euclidean-phrase.ts
      stochastic-phrase.ts
      generative-phrase.ts
      patch.ts
      table.ts
      chord.ts
      musical-context.ts
      ports/
        phrase-render.ts
        table-render.ts
      repositories/
        song-repository.ts
        ...
    application/
      song-editor.ts
      ...
    infrastructure/
      fs-song-repository.ts
      ...
  synthesis/
    domain/
    application/
    infrastructure/
  sampling/
    ...
  midi-effects/
    ...
  audio-effects/
    ...
  mixer/
    ...
  playback/
    domain/
      transport.ts
      sequencer.ts
      playhead.ts
      groove.ts
    application/
    infrastructure/
  performance/
    ...
  midi/
    ...
  editor/
    domain/
    views/
      linear-phrase-view.ts
      drum-phrase-view.ts
      ...
  controls/
    domain/
    infrastructure/
      keyboard-input.ts
      gamepad-input.ts
      midi-input-as-control.ts
```

This layout is derived from the spec, not declared in it. The planner computes file paths from context and layer.

## v1 priorities for the tracker

When a local LLM begins generating, the priority order is:

1. Kernel value objects.
2. Composition aggregates and their repositories (interfaces only).
3. Composition ApplicationServices.
4. Composition infrastructure: filesystem-backed repository adapters.
5. Playback: Transport, Sequencer, Playhead, Groove.
6. One concrete Synth and one concrete Sample adapter.
7. The simplest phrase type (LinearPhrase) and its Editor view.
8. The remaining phrase types in order: DrumPhrase, EuclideanPhrase, StochasticPhrase, GenerativePhrase.
9. MIDIEffects and AudioEffects (start with Arpeggiator and Reverb).
10. Mixer.
11. Performance and MIDI.
12. Controls and full Editor.

The state file makes this incremental: each layer settles, then the next builds on it without regenerating what came before.
