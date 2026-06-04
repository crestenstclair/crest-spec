import { project, command, event, operation, invariant, relationship, layer } from "../src/index.js";

// ─────────────────────────────────────────────────────────────────────────────
// crest-tracker: a portable, controller-driven music tracker
// "M8 meets modular synth" — 16-track polyphonic sequencer with
// Song → Chain → Phrase → Table hierarchy, multiple synth engines,
// per-step FX commands, and gamepad-first interaction.
// ─────────────────────────────────────────────────────────────────────────────

const app = project("crest-tracker", {
  layers: ["domain", "application", "infrastructure", "interface"],
  rules: [
    layer("domain").dependsOn([]),
    layer("application").dependsOn(["domain"]),
    layer("infrastructure").dependsOn(["application", "domain"]),
    layer("interface").dependsOn(["application"]),
  ],
  meta: {
    language: "csharp",
    framework: "Godot 4",
    style: "C# Godot conventions; PascalCase public members; records for value objects; partial classes for nodes",
    avoid: ["GD.Print in domain layer", "static mutable state", "Service Locator pattern"],
    prompts: [
      "use Ticks for all musical time; convert to samples only at the audio buffer boundary",
      "events are past tense; commands are imperative",
      "FxValue encoding: (commandId << 8) | parameterByte",
      "Song is the single document root — no separate files for chains, phrases, patches",
      "all UI interaction assumes gamepad-first with 8 semantic buttons",
      "groove patterns are arrays of tick counts per row — [6,6,...] is straight, [7,5] is swing",
    ],
    references: ["./docs/architecture.md", "./docs/design-decisions.md"],
  },
});

// ═══════════════════════════════════════════════════════════════════════════════
// KERNEL — primitives shared across contexts
// ═══════════════════════════════════════════════════════════════════════════════

const kernel = app.context("Kernel", {
  purpose: "primitives shared across all contexts",
});

kernel.valueObject("Ticks", { from: "number", description: "musical time in ticks; default 6 ticks per row" });
kernel.valueObject("BPM", { from: "number", invariants: ["between 20 and 999"] });
kernel.valueObject("Note", { from: "number", invariants: ["MIDI note 0..127"] });
kernel.valueObject("Velocity", { from: "number", invariants: ["0..127"] });
kernel.valueObject("TrackIndex", { from: "number", invariants: ["0..15"] });
kernel.valueObject("PhraseIndex", { from: "number", invariants: ["0..4095"] });
kernel.valueObject("ChainIndex", { from: "number", invariants: ["0..4095"] });
kernel.valueObject("PatchIndex", { from: "number", invariants: ["0..255"] });
kernel.valueObject("TableIndex", { from: "number", invariants: ["0..4095"] });
kernel.valueObject("RowIndex", { from: "number", invariants: ["0..255"] });
kernel.valueObject("FxValue", { from: "number", description: "high byte = command ID, low byte = parameter value" });
kernel.valueObject("Division", { from: "number", description: "step subdivision: 8, 16, or 32", invariants: ["one of: 8, 16, 32"] });
kernel.valueObject("SampleRate", { from: "number", invariants: ["44100 or 48000"] });

// ═══════════════════════════════════════════════════════════════════════════════
// COMPOSITION — structural model of a song
// ═══════════════════════════════════════════════════════════════════════════════

const composition = app.context("Composition", {
  purpose: "structural model of a song — all authored content: tracks, phrases, chains, patches, tables, chords, grooves",
  ubiquitousLanguage: {
    "Song": "root container holding all authored content across 16 tracks",
    "Phrase": "16-row sequencer grid — the primary editing unit",
    "Chain": "ordered sequence of phrase references with transpose/repeat",
    "Patch": "instrument definition: synth engine + MIDI transformers + post-effects + sends",
    "Table": "tick-level micro-sequencer running alongside phrase playback",
    "SongGrid": "16 tracks × 256 rows matrix of chain references",
    "ChordBank": "collection of 30 reusable chord definitions",
    "GrooveBank": "collection of groove timing patterns",
  },
});

// --- Value objects ---

composition.valueObject("PhraseStep", {
  state: {
    note: "Note | null",
    velocity: "Velocity",
    patchId: "PatchIndex | null",
    fx1: "FxValue | null",
    fx2: "FxValue | null",
  },
});

composition.valueObject("ChainStep", {
  state: {
    phraseId: "PhraseIndex | null",
    transpose: "number",
    repeat: "number",
  },
  invariants: ["transpose is -12..12", "repeat is 0..255"],
});

composition.valueObject("TableStep", {
  state: {
    pitch: "number",
    volume: "number",
    fx1: "FxValue | null",
    fx2: "FxValue | null",
    fx3: "FxValue | null",
  },
  invariants: ["pitch is -127..127", "volume is 0..255"],
});

composition.valueObject("ChordDefinition", {
  state: { name: "string", intervals: "number[]" },
  description: "chord as semitone offsets from root; e.g. major = [0, 4, 7]",
});

composition.valueObject("EngineType", {
  from: "string",
  invariants: ["one of: MeltySynth, Plaits, Dexed, Sample"],
});

composition.valueObject("Scale", {
  from: "string",
  description: "scale name from 30+ catalog: major, minor, dorian, pentatonic, etc.",
});

composition.valueObject("PostEffectConfig", {
  state: { type: "string", parameters: "Record<string, number>" },
  description: "serializable effect instance within a patch's post-effect chain",
});

composition.valueObject("MidiTransformerConfig", {
  state: { type: "string", parameters: "Record<string, number>" },
  description: "serializable MIDI transformer instance: Arpeggiator, Portamento, or PitchLFO",
});

composition.valueObject("Groove", {
  state: { id: "number", name: "string", pattern: "number[]" },
  description: "tick counts per row; [6,6,...] = straight, [7,5] = swing",
});

// --- Ports ---

const phraseRender = composition.port("PhraseRender", {
  contract: { render: "(phrase: Phrase, context: PlaybackContext) => NoteEvent[]" },
});

const tableRender = composition.port("TableRender", {
  contract: { render: "(table: Table, tick: number) => ParameterEvent[]" },
});

// --- Aggregates ---

const song = composition.aggregate("Song", {
  root: true,
  purpose: "root container and single document; all composition data nests here",
  state: {
    name: "string",
    tempo: "BPM",
    scale: "Scale",
    rootNote: "Note",
    songGrid: "number[16][256]",
    tracks: "Track[16]",
    patches: "Patch[256]",
    chains: "Chain[4096]",
    phrases: "Phrase[4096]",
    tables: "Table[4096]",
    chordBank: "ChordDefinition[30]",
    grooveBank: "Groove[]",
    masterEffects: "MasterEffectsState",
  },
  invariants: [
    "exactly 16 tracks at all times",
    "tempo between 20 and 999 BPM",
    "chain references in songGrid are valid indices or -1",
    "phrase references in chains are valid indices or null",
  ],
  commands: [
    command("SetBpm", { bpm: "BPM" }),
    command("SetScale", { scale: "Scale" }),
    command("SetRootNote", { note: "Note" }),
    command("SetSongGridCell", { track: "TrackIndex", row: "RowIndex", chainId: "ChainIndex" }),
  ],
  events: [
    event("BpmChanged", { from: "BPM", to: "BPM" }),
    event("ScaleChanged", { from: "Scale", to: "Scale" }),
    event("RootNoteChanged", { from: "Note", to: "Note" }),
    event("SongGridCellSet", { track: "TrackIndex", row: "RowIndex", chainId: "ChainIndex" }),
  ],
});

song.entity("Track", {
  state: {
    index: "TrackIndex",
    volume: "number",
    pan: "number",
    muted: "boolean",
    soloed: "boolean",
    grooveId: "number | null",
  },
});

const phrase = composition.aggregate("Phrase", {
  root: true,
  purpose: "16-row sequencer grid; the primary editing unit",
  state: {
    index: "PhraseIndex",
    division: "Division",
    length: "number",
    chordBankId: "number | null",
    swing: "number",
    rootNote: "Note | null",
    steps: "PhraseStep[16]",
  },
  invariants: [
    "division is 8, 16, or 32",
    "length is between 1 and 16",
    "steps array always has exactly 16 slots",
  ],
  commands: [
    command("SetNote", { phrase: "PhraseIndex", row: "RowIndex", note: "Note | null" }),
    command("SetVelocity", { phrase: "PhraseIndex", row: "RowIndex", velocity: "Velocity" }),
    command("SetStepPatch", { phrase: "PhraseIndex", row: "RowIndex", patchId: "PatchIndex | null" }),
    command("SetFX", { phrase: "PhraseIndex", row: "RowIndex", slot: "number", value: "FxValue | null" }),
    command("SetDivision", { phrase: "PhraseIndex", division: "Division" }),
    command("SetPhraseLength", { phrase: "PhraseIndex", length: "number" }),
    command("SetPhraseSwing", { phrase: "PhraseIndex", swing: "number" }),
    command("SetPhraseRootNote", { phrase: "PhraseIndex", rootNote: "Note | null" }),
    command("SetPhraseChordBank", { phrase: "PhraseIndex", chordBankId: "number | null" }),
  ],
  events: [
    event("NoteSet", { phrase: "PhraseIndex", row: "RowIndex", note: "Note | null" }),
    event("VelocitySet", { phrase: "PhraseIndex", row: "RowIndex", velocity: "Velocity" }),
    event("StepPatchSet", { phrase: "PhraseIndex", row: "RowIndex", patchId: "PatchIndex | null" }),
    event("FXSet", { phrase: "PhraseIndex", row: "RowIndex", slot: "number", value: "FxValue | null" }),
    event("DivisionChanged", { phrase: "PhraseIndex", from: "Division", to: "Division" }),
    event("PhraseLengthChanged", { phrase: "PhraseIndex", from: "number", to: "number" }),
    event("PhraseSwingChanged", { phrase: "PhraseIndex", from: "number", to: "number" }),
    event("PhraseRootNoteChanged", { phrase: "PhraseIndex", from: "Note | null", to: "Note | null" }),
  ],
});

const chain = composition.aggregate("Chain", {
  root: true,
  purpose: "ordered sequence of phrase references with per-step transpose and repeat",
  state: {
    index: "ChainIndex",
    steps: "ChainStep[16]",
  },
  commands: [
    command("SetChainPhrase", { chain: "ChainIndex", row: "RowIndex", phraseId: "PhraseIndex | null" }),
    command("SetChainTranspose", { chain: "ChainIndex", row: "RowIndex", transpose: "number" }),
    command("SetChainRepeat", { chain: "ChainIndex", row: "RowIndex", repeat: "number" }),
  ],
  events: [
    event("ChainPhraseSet", { chain: "ChainIndex", row: "RowIndex", phraseId: "PhraseIndex | null" }),
    event("ChainTransposeSet", { chain: "ChainIndex", row: "RowIndex", transpose: "number" }),
    event("ChainRepeatSet", { chain: "ChainIndex", row: "RowIndex", repeat: "number" }),
  ],
});

const patch = composition.aggregate("Patch", {
  root: true,
  purpose: "instrument definition: synth engine + MIDI transformers + post-effects + send routing",
  state: {
    index: "PatchIndex",
    name: "string",
    engineType: "EngineType",
    engineParameters: "Record<string, number>",
    midiTransformers: "MidiTransformerConfig[]",
    postEffects: "PostEffectConfig[]",
    volume: "number",
    panning: "number",
    sendReverb: "number",
    sendDelay: "number",
    sendChorus: "number",
    chordBank: "ChordDefinition[] | null",
  },
  commands: [
    command("SetPatchName", { patch: "PatchIndex", name: "string" }),
    command("SetPatchEngine", { patch: "PatchIndex", engineType: "EngineType" }),
    command("SetEngineParameter", { patch: "PatchIndex", paramId: "string", value: "number" }),
    command("AddPostEffect", { patch: "PatchIndex", effectType: "string", at: "number" }),
    command("RemovePostEffect", { patch: "PatchIndex", at: "number" }),
    command("MovePostEffect", { patch: "PatchIndex", from: "number", to: "number" }),
    command("SetPostEffectParameter", { patch: "PatchIndex", effectIndex: "number", paramId: "string", value: "number" }),
    command("SetPatchVolume", { patch: "PatchIndex", volume: "number" }),
    command("SetPatchPanning", { patch: "PatchIndex", panning: "number" }),
    command("SetPatchSend", { patch: "PatchIndex", bus: "string", level: "number" }),
    command("AddMidiTransformer", { patch: "PatchIndex", type: "string", at: "number" }),
    command("RemoveMidiTransformer", { patch: "PatchIndex", at: "number" }),
    command("SetMidiTransformerParameter", { patch: "PatchIndex", transformerIndex: "number", paramId: "string", value: "number" }),
    command("SetPatchChordBank", { patch: "PatchIndex", chords: "ChordDefinition[] | null" }),
  ],
  events: [
    event("PatchRenamed", { patch: "PatchIndex", name: "string" }),
    event("PatchEngineChanged", { patch: "PatchIndex", from: "EngineType", to: "EngineType" }),
    event("EngineParameterSet", { patch: "PatchIndex", paramId: "string", value: "number" }),
    event("PostEffectAdded", { patch: "PatchIndex", effectType: "string", at: "number" }),
    event("PostEffectRemoved", { patch: "PatchIndex", at: "number" }),
    event("PostEffectMoved", { patch: "PatchIndex", from: "number", to: "number" }),
    event("PostEffectParameterSet", { patch: "PatchIndex", effectIndex: "number", paramId: "string", value: "number" }),
    event("PatchVolumeChanged", { patch: "PatchIndex", from: "number", to: "number" }),
    event("PatchPanningChanged", { patch: "PatchIndex", from: "number", to: "number" }),
    event("PatchSendChanged", { patch: "PatchIndex", bus: "string", level: "number" }),
    event("MidiTransformerAdded", { patch: "PatchIndex", type: "string", at: "number" }),
    event("MidiTransformerRemoved", { patch: "PatchIndex", at: "number" }),
    event("MidiTransformerParameterSet", { patch: "PatchIndex", transformerIndex: "number", paramId: "string", value: "number" }),
  ],
});

const table = composition.aggregate("Table", {
  root: true,
  purpose: "tick-level micro-sequencer running alongside phrase playback",
  state: {
    index: "TableIndex",
    length: "number",
    tickSpeed: "number",
    steps: "TableStep[32]",
  },
  invariants: ["length is 16 or 32", "tickSpeed is positive"],
  commands: [
    command("SetTablePitch", { table: "TableIndex", row: "RowIndex", pitch: "number" }),
    command("SetTableVolume", { table: "TableIndex", row: "RowIndex", volume: "number" }),
    command("SetTableFX", { table: "TableIndex", row: "RowIndex", slot: "number", value: "FxValue | null" }),
    command("SetTableLength", { table: "TableIndex", length: "number" }),
    command("SetTableTickSpeed", { table: "TableIndex", tickSpeed: "number" }),
  ],
  events: [
    event("TablePitchSet", { table: "TableIndex", row: "RowIndex", pitch: "number" }),
    event("TableVolumeSet", { table: "TableIndex", row: "RowIndex", volume: "number" }),
    event("TableFXSet", { table: "TableIndex", row: "RowIndex", slot: "number", value: "FxValue | null" }),
    event("TableLengthChanged", { table: "TableIndex", from: "number", to: "number" }),
    event("TableTickSpeedChanged", { table: "TableIndex", from: "number", to: "number" }),
  ],
});

const chordBank = composition.aggregate("ChordBank", {
  root: true,
  purpose: "global chord bank with 30 preset chord definitions",
  state: { chords: "ChordDefinition[30]" },
  commands: [
    command("SetChord", { index: "number", name: "string", intervals: "number[]" }),
    command("SetChordName", { index: "number", name: "string" }),
    command("SetChordIntervals", { index: "number", intervals: "number[]" }),
  ],
  events: [
    event("ChordSet", { index: "number", name: "string", intervals: "number[]" }),
    event("ChordNameChanged", { index: "number", name: "string" }),
    event("ChordIntervalsChanged", { index: "number", intervals: "number[]" }),
  ],
});

const grooveBank = composition.aggregate("GrooveBank", {
  root: true,
  purpose: "collection of groove timing patterns for per-track timing variation",
  state: { grooves: "Groove[]" },
  commands: [
    command("SetGroovePattern", { grooveId: "number", pattern: "number[]" }),
    command("AddGroove", { name: "string", pattern: "number[]" }),
    command("RemoveGroove", { grooveId: "number" }),
  ],
  events: [
    event("GroovePatternSet", { grooveId: "number", pattern: "number[]" }),
    event("GrooveAdded", { grooveId: "number", name: "string", pattern: "number[]" }),
    event("GrooveRemoved", { grooveId: "number" }),
  ],
});

const masterEffects = composition.aggregate("MasterEffects", {
  root: true,
  purpose: "master output chain: saturation, bass boost, stereo width, 3-band EQ, limiter",
  state: {
    saturationDrive: "number",
    saturationMode: "string",
    bassBoostAmount: "number",
    bassBoostFrequency: "number",
    spaceWidth: "number",
    eqLow: "number",
    eqMid: "number",
    eqHigh: "number",
    limiterThreshold: "number",
    limiterRelease: "number",
  },
  commands: [
    command("SetMasterSaturation", { drive: "number", mode: "string" }),
    command("SetMasterBassBoost", { amount: "number", frequency: "number" }),
    command("SetMasterSpace", { width: "number" }),
    command("SetMasterEQ", { low: "number", mid: "number", high: "number" }),
    command("SetMasterLimiter", { threshold: "number", release: "number" }),
  ],
  events: [
    event("MasterSaturationChanged", { drive: "number", mode: "string" }),
    event("MasterBassBoostChanged", { amount: "number", frequency: "number" }),
    event("MasterSpaceChanged", { width: "number" }),
    event("MasterEQChanged", { low: "number", mid: "number", high: "number" }),
    event("MasterLimiterChanged", { threshold: "number", release: "number" }),
  ],
});

// --- Repositories (all composition aggregates nested in Song document) ---

composition.repository("SongRepository", { of: song });
composition.repository("PhraseRepository", { of: phrase });
composition.repository("ChainRepository", { of: chain });
composition.repository("PatchRepository", { of: patch });
composition.repository("TableRepository", { of: table });
composition.repository("ChordBankRepository", { of: chordBank });
composition.repository("GrooveBankRepository", { of: grooveBank });
composition.repository("MasterEffectsRepository", { of: masterEffects });

// --- Application services ---

composition.applicationService("SongEditor", {
  purpose: "orchestrates song-level operations: BPM, scale, root note, grid placement",
  uses: [song],
  operations: [
    operation("SetBpm", { input: { bpm: "BPM" } }),
    operation("SetScale", { input: { scale: "Scale" } }),
    operation("SetRootNote", { input: { note: "Note" } }),
    operation("SetSongGridCell", { input: { track: "TrackIndex", row: "RowIndex", chainId: "ChainIndex" } }),
  ],
});

composition.applicationService("PhraseEditor", {
  purpose: "orchestrates phrase editing: notes, velocities, FX, division, length",
  uses: [phrase],
  operations: [
    operation("SetNote", { input: { phrase: "PhraseIndex", row: "RowIndex", note: "Note | null" } }),
    operation("SetVelocity", { input: { phrase: "PhraseIndex", row: "RowIndex", velocity: "Velocity" } }),
    operation("SetStepPatch", { input: { phrase: "PhraseIndex", row: "RowIndex", patchId: "PatchIndex | null" } }),
    operation("SetFX", { input: { phrase: "PhraseIndex", row: "RowIndex", slot: "number", value: "FxValue | null" } }),
    operation("SetDivision", { input: { phrase: "PhraseIndex", division: "Division" } }),
    operation("SetPhraseLength", { input: { phrase: "PhraseIndex", length: "number" } }),
  ],
});

composition.applicationService("ChainEditor", {
  purpose: "orchestrates chain editing: phrase assignment, transpose, repeat",
  uses: [chain],
  operations: [
    operation("SetChainPhrase", { input: { chain: "ChainIndex", row: "RowIndex", phraseId: "PhraseIndex | null" } }),
    operation("SetChainTranspose", { input: { chain: "ChainIndex", row: "RowIndex", transpose: "number" } }),
    operation("SetChainRepeat", { input: { chain: "ChainIndex", row: "RowIndex", repeat: "number" } }),
  ],
});

composition.applicationService("PatchEditor", {
  purpose: "orchestrates patch editing: engine, parameters, effects, sends",
  uses: [patch],
  operations: [
    operation("SetPatchEngine", { input: { patch: "PatchIndex", engineType: "EngineType" } }),
    operation("SetEngineParameter", { input: { patch: "PatchIndex", paramId: "string", value: "number" } }),
    operation("AddPostEffect", { input: { patch: "PatchIndex", effectType: "string", at: "number" } }),
    operation("RemovePostEffect", { input: { patch: "PatchIndex", at: "number" } }),
    operation("SetPostEffectParameter", { input: { patch: "PatchIndex", effectIndex: "number", paramId: "string", value: "number" } }),
    operation("SetPatchSend", { input: { patch: "PatchIndex", bus: "string", level: "number" } }),
  ],
});

composition.applicationService("TableEditor", {
  purpose: "orchestrates table editing: pitch, volume, FX, length, speed",
  uses: [table],
  operations: [
    operation("SetTablePitch", { input: { table: "TableIndex", row: "RowIndex", pitch: "number" } }),
    operation("SetTableVolume", { input: { table: "TableIndex", row: "RowIndex", volume: "number" } }),
    operation("SetTableFX", { input: { table: "TableIndex", row: "RowIndex", slot: "number", value: "FxValue | null" } }),
    operation("SetTableLength", { input: { table: "TableIndex", length: "number" } }),
    operation("SetTableTickSpeed", { input: { table: "TableIndex", tickSpeed: "number" } }),
  ],
});

composition.applicationService("ChordBankEditor", {
  purpose: "orchestrates chord bank editing: set chords, names, intervals",
  uses: [chordBank],
  operations: [
    operation("SetChord", { input: { index: "number", name: "string", intervals: "number[]" } }),
  ],
});

composition.applicationService("GrooveBankEditor", {
  purpose: "orchestrates groove editing: add/remove/modify groove patterns",
  uses: [grooveBank],
  operations: [
    operation("SetGroovePattern", { input: { grooveId: "number", pattern: "number[]" } }),
    operation("AddGroove", { input: { name: "string", pattern: "number[]" } }),
    operation("RemoveGroove", { input: { grooveId: "number" } }),
  ],
});

composition.applicationService("MasterEffectsEditor", {
  purpose: "orchestrates master effects: saturation, bass boost, space, EQ, limiter",
  uses: [masterEffects],
  operations: [
    operation("SetMasterSaturation", { input: { drive: "number", mode: "string" } }),
    operation("SetMasterBassBoost", { input: { amount: "number", frequency: "number" } }),
    operation("SetMasterEQ", { input: { low: "number", mid: "number", high: "number" } }),
    operation("SetMasterLimiter", { input: { threshold: "number", release: "number" } }),
  ],
});

composition.applicationService("MixerEditor", {
  purpose: "applies track volume, pan, mute, solo, and groove assignment",
  uses: [song],
  operations: [
    operation("SetTrackVolume", { input: { track: "TrackIndex", volume: "number" } }),
    operation("SetTrackPan", { input: { track: "TrackIndex", pan: "number" } }),
    operation("MuteTrack", { input: { track: "TrackIndex" } }),
    operation("UnmuteTrack", { input: { track: "TrackIndex" } }),
    operation("SoloTrack", { input: { track: "TrackIndex" } }),
    operation("UnsoloTrack", { input: { track: "TrackIndex" } }),
    operation("SetTrackGroove", { input: { track: "TrackIndex", grooveId: "number | null" } }),
  ],
});

composition.applicationService("SongSerializer", {
  purpose: "JSON round-trip with sparse encoding; only non-default/non-null values persisted; .quest file extension",
  uses: [song],
  operations: [
    operation("Save", { input: { path: "string" } }),
    operation("Load", { input: { path: "string" } }),
  ],
  meta: {
    rules: [
      "only serialize non-default, non-null values",
      "chains/phrases/tables serialized as sparse arrays (index → data)",
      "patches serialized with engine type and parameter map",
    ],
  },
});

// ═══════════════════════════════════════════════════════════════════════════════
// SYNTHESIS — sound generation via multiple engine backends
// ═══════════════════════════════════════════════════════════════════════════════

const synthesis = app.context("Synthesis", {
  purpose: "sound generation via four engine backends: SoundFont, wavetable, FM, and sample",
  ubiquitousLanguage: {
    "MeltySynth": "SoundFont playback engine; unlimited polyphony per instrument",
    "Plaits": "wavetable/oscillator engine based on Mutable Instruments; 8-voice ADSR",
    "Dexed": "FM synthesis engine (Yamaha DX7 emulation); 6-operator, 32 algorithms, 8-voice",
    "Sample": "WAV sample playback with pitch shifting and loop points",
  },
});

const audioProducer = synthesis.port("AudioProducer", {
  contract: {
    noteOn: "(note: Note, velocity: Velocity, channel: number) => void",
    noteOff: "(note: Note, channel: number) => void",
    render: "(frames: number) => Float32Array",
  },
});

const meltySynth = synthesis.aggregate("MeltySynthEngine", {
  root: true,
  purpose: "SoundFont playback engine with unlimited polyphony",
  implements: audioProducer,
  state: {
    soundFontPath: "string",
    program: "number",
    bank: "number",
  },
  commands: [
    command("SetSoundFont", { path: "string" }),
    command("SetProgram", { program: "number", bank: "number" }),
  ],
  events: [
    event("SoundFontLoaded", { path: "string" }),
    event("ProgramChanged", { program: "number", bank: "number" }),
  ],
});

const plaitsEngine = synthesis.aggregate("PlaitsEngine", {
  root: true,
  purpose: "wavetable/oscillator engine; 8-voice polyphony with ADSR envelope",
  implements: audioProducer,
  state: {
    oscillatorMode: "string",
    polyphony: "number",
    attack: "number",
    decay: "number",
    sustain: "number",
    release: "number",
    harmonics: "number",
    timbre: "number",
    morph: "number",
  },
  commands: [
    command("SetOscillatorMode", { mode: "string" }),
    command("SetEnvelope", { attack: "number", decay: "number", sustain: "number", release: "number" }),
    command("SetHarmonics", { value: "number" }),
    command("SetTimbre", { value: "number" }),
    command("SetMorph", { value: "number" }),
  ],
  events: [
    event("OscillatorModeChanged", { mode: "string" }),
    event("EnvelopeChanged", { attack: "number", decay: "number", sustain: "number", release: "number" }),
    event("HarmonicsChanged", { value: "number" }),
    event("TimbreChanged", { value: "number" }),
    event("MorphChanged", { value: "number" }),
  ],
});

const dexedEngine = synthesis.aggregate("DexedFmEngine", {
  root: true,
  purpose: "FM synthesis (DX7 emulation); 6 operators, 32 algorithms, 8-voice polyphony",
  implements: audioProducer,
  state: {
    algorithm: "number",
    feedback: "number",
    operators: "FmOperator[6]",
  },
  commands: [
    command("SetAlgorithm", { algorithm: "number" }),
    command("SetFeedback", { feedback: "number" }),
    command("SetOperator", { index: "number", ratio: "number", level: "number", detune: "number" }),
    command("LoadSysex", { path: "string" }),
  ],
  events: [
    event("AlgorithmChanged", { algorithm: "number" }),
    event("FeedbackChanged", { feedback: "number" }),
    event("OperatorChanged", { index: "number", ratio: "number", level: "number", detune: "number" }),
    event("SysexLoaded", { path: "string" }),
  ],
});

synthesis.valueObject("FmOperator", {
  state: { ratio: "number", level: "number", detune: "number", attack: "number", decay: "number", sustain: "number", release: "number" },
});

const sampleEngine = synthesis.aggregate("SampleEngine", {
  root: true,
  purpose: "WAV sample playback with pitch shifting and configurable loop points",
  implements: audioProducer,
  state: {
    samplePath: "string",
    rootNote: "Note",
    loopStart: "number",
    loopEnd: "number",
    loopMode: "string",
  },
  invariants: ["loopMode is one of: off, forward, pingpong"],
  commands: [
    command("SetSample", { path: "string" }),
    command("SetRootNote", { note: "Note" }),
    command("SetLoopPoints", { start: "number", end: "number", mode: "string" }),
  ],
  events: [
    event("SampleLoaded", { path: "string" }),
    event("RootNoteChanged", { note: "Note" }),
    event("LoopPointsChanged", { start: "number", end: "number", mode: "string" }),
  ],
});

// --- Synthesis repositories and services ---

synthesis.repository("MeltySynthEngineRepository", { of: meltySynth });
synthesis.repository("PlaitsEngineRepository", { of: plaitsEngine });
synthesis.repository("DexedFmEngineRepository", { of: dexedEngine });
synthesis.repository("SampleEngineRepository", { of: sampleEngine });

synthesis.applicationService("SynthEngineManager", {
  purpose: "creates and configures synth engine instances from patch configuration",
  uses: [meltySynth, plaitsEngine, dexedEngine, sampleEngine],
  operations: [
    operation("CreateEngine", { input: { engineType: "EngineType", params: "Record<string, number>" } }),
    operation("ConfigureEngine", { input: { engineId: "string", paramId: "string", value: "number" } }),
  ],
});

// ═══════════════════════════════════════════════════════════════════════════════
// MIDI TRANSFORMERS — pre-engine note-event transformations
// ═══════════════════════════════════════════════════════════════════════════════

const midiTransformers = app.context("MIDITransformers", {
  purpose: "note-event transformations applied before the synth engine via double-buffered pipeline",
  ubiquitousLanguage: {
    "Arpeggiator": "cycles held notes through a pattern at a configurable rate",
    "Portamento": "glides pitch between consecutive notes",
    "PitchLFO": "applies periodic pitch modulation",
  },
});

const noteTransform = midiTransformers.port("NoteTransform", {
  contract: { transform: "(events: NoteEvent[], context: PlaybackContext) => NoteEvent[]" },
});

const arpeggiator = midiTransformers.aggregate("Arpeggiator", {
  root: true,
  purpose: "cycles held notes through a pattern at a configurable rate; 6 modes",
  implements: noteTransform,
  state: {
    enabled: "boolean",
    pattern: "string",
    rate: "Ticks",
    octaveRange: "number",
    gate: "number",
  },
  invariants: ["pattern is one of: up, down, updown, random, order, chord", "octaveRange is 1..4", "gate is 0..1"],
  commands: [
    command("SetArpPattern", { pattern: "string" }),
    command("SetArpRate", { rate: "Ticks" }),
    command("SetArpOctaveRange", { range: "number" }),
    command("SetArpGate", { gate: "number" }),
    command("ToggleArp", { enabled: "boolean" }),
  ],
  events: [
    event("ArpPatternChanged", { pattern: "string" }),
    event("ArpRateChanged", { rate: "Ticks" }),
    event("ArpOctaveRangeChanged", { range: "number" }),
    event("ArpGateChanged", { gate: "number" }),
    event("ArpToggled", { enabled: "boolean" }),
  ],
});

const portamento = midiTransformers.aggregate("Portamento", {
  root: true,
  purpose: "glides pitch between consecutive notes",
  implements: noteTransform,
  state: {
    enabled: "boolean",
    time: "number",
    mode: "string",
  },
  invariants: ["mode is one of: always, legato"],
  commands: [
    command("SetPortamentoTime", { time: "number" }),
    command("SetPortamentoMode", { mode: "string" }),
    command("TogglePortamento", { enabled: "boolean" }),
  ],
  events: [
    event("PortamentoTimeChanged", { time: "number" }),
    event("PortamentoModeChanged", { mode: "string" }),
    event("PortamentoToggled", { enabled: "boolean" }),
  ],
});

const pitchLfo = midiTransformers.aggregate("PitchLFO", {
  root: true,
  purpose: "applies periodic pitch modulation",
  implements: noteTransform,
  state: {
    enabled: "boolean",
    rate: "number",
    depth: "number",
    waveform: "string",
  },
  invariants: ["waveform is one of: sine, triangle, square, saw"],
  commands: [
    command("SetPitchLFORate", { rate: "number" }),
    command("SetPitchLFODepth", { depth: "number" }),
    command("SetPitchLFOWaveform", { waveform: "string" }),
    command("TogglePitchLFO", { enabled: "boolean" }),
  ],
  events: [
    event("PitchLFORateChanged", { rate: "number" }),
    event("PitchLFODepthChanged", { depth: "number" }),
    event("PitchLFOWaveformChanged", { waveform: "string" }),
    event("PitchLFOToggled", { enabled: "boolean" }),
  ],
});

// --- MIDI Transformers repositories and services ---

midiTransformers.repository("ArpeggiatorRepository", { of: arpeggiator });
midiTransformers.repository("PortamentoRepository", { of: portamento });
midiTransformers.repository("PitchLFORepository", { of: pitchLfo });

midiTransformers.applicationService("MidiTransformerManager", {
  purpose: "creates and configures MIDI transformer instances for patch chains",
  uses: [arpeggiator, portamento, pitchLfo],
  operations: [
    operation("CreateTransformer", { input: { type: "string", params: "Record<string, number>" } }),
    operation("ConfigureTransformer", { input: { type: "string", paramId: "string", value: "number" } }),
  ],
});

// ═══════════════════════════════════════════════════════════════════════════════
// AUDIO EFFECTS — per-patch post-processing DSP
// ═══════════════════════════════════════════════════════════════════════════════

const audioEffects = app.context("AudioEffects", {
  purpose: "20+ post-effects applied after the synth engine, organized as an ordered chain per patch",
  ubiquitousLanguage: {
    "PostEffect": "a single DSP processor in a patch's effect chain",
    "EffectChain": "ordered list of post-effects applied sequentially to audio",
  },
});

const audioTransform = audioEffects.port("AudioTransform", {
  contract: {
    process: "(buffer: Float32Array, sampleRate: SampleRate) => Float32Array",
    getParameters: "() => ParameterInfo[]",
    setParameter: "(id: string, value: number) => void",
  },
});

// Filters (not aggregate roots — instantiated as part of Patch effect chains)
audioEffects.aggregate("LowPassFilter", { implements: audioTransform, state: { cutoff: "number", resonance: "number" } });
audioEffects.aggregate("HighPassFilter", { implements: audioTransform, state: { cutoff: "number", resonance: "number" } });
audioEffects.aggregate("BandPassFilter", { implements: audioTransform, state: { cutoff: "number", resonance: "number", bandwidth: "number" } });
audioEffects.aggregate("LadderFilter", { implements: audioTransform, state: { cutoff: "number", resonance: "number", drive: "number" } });
audioEffects.aggregate("ResonantFilterBank", { implements: audioTransform, state: { bands: "number[]" } });

// Envelopes
audioEffects.aggregate("AmpEnvelope", { implements: audioTransform, state: { attack: "number", decay: "number", sustain: "number", release: "number" } });
audioEffects.aggregate("FilterEnvelope", { implements: audioTransform, state: { attack: "number", decay: "number", sustain: "number", release: "number", depth: "number" } });

// Modulation
audioEffects.aggregate("Chorus", { implements: audioTransform, state: { rate: "number", depth: "number", mix: "number" } });
audioEffects.aggregate("Flanger", { implements: audioTransform, state: { rate: "number", depth: "number", feedback: "number", mix: "number" } });
audioEffects.aggregate("Phaser", { implements: audioTransform, state: { rate: "number", depth: "number", stages: "number", mix: "number" } });
audioEffects.aggregate("Tremolo", { implements: audioTransform, state: { rate: "number", depth: "number", waveform: "string" } });
audioEffects.aggregate("RingModulator", { implements: audioTransform, state: { frequency: "number", mix: "number" } });

// Distortion
audioEffects.aggregate("Waveshaper", { implements: audioTransform, state: { drive: "number", curve: "string", mix: "number" } });
audioEffects.aggregate("Bitcrusher", { implements: audioTransform, state: { bits: "number", sampleReduction: "number" } });
audioEffects.aggregate("DualBandDistortion", { implements: audioTransform, state: { crossover: "number", lowDrive: "number", highDrive: "number" } });

// Delay
audioEffects.aggregate("Delay", { implements: audioTransform, state: { time: "number", feedback: "number", mix: "number" } });

// Dynamics
audioEffects.aggregate("Compressor", { implements: audioTransform, state: { threshold: "number", ratio: "number", attack: "number", release: "number", makeupGain: "number" } });
audioEffects.aggregate("Gate", { implements: audioTransform, state: { threshold: "number", attack: "number", release: "number" } });

// EQ
audioEffects.aggregate("ThreeBandEQ", { implements: audioTransform, state: { low: "number", mid: "number", high: "number", lowFreq: "number", highFreq: "number" } });

// ═══════════════════════════════════════════════════════════════════════════════
// SEND BUS — shared effect buses (reverb, delay, chorus)
// ═══════════════════════════════════════════════════════════════════════════════

const sendBus = app.context("SendBus", {
  purpose: "shared effect buses receiving configurable send levels from each patch",
  ubiquitousLanguage: {
    "ReverbBus": "shared reverb using Clouds algorithm",
    "DelayBus": "shared tempo-synced delay",
    "ChorusBus": "shared modulation chorus",
  },
});

const reverbBus = sendBus.aggregate("ReverbBus", {
  root: true,
  state: { enabled: "boolean", size: "number", damping: "number", wet: "number", dry: "number" },
  commands: [
    command("SetReverbSize", { size: "number" }),
    command("SetReverbDamping", { damping: "number" }),
    command("SetReverbMix", { wet: "number", dry: "number" }),
    command("ToggleReverb", { enabled: "boolean" }),
  ],
  events: [
    event("ReverbSizeChanged", { size: "number" }),
    event("ReverbDampingChanged", { damping: "number" }),
    event("ReverbMixChanged", { wet: "number", dry: "number" }),
    event("ReverbToggled", { enabled: "boolean" }),
  ],
});

const delayBus = sendBus.aggregate("DelayBus", {
  root: true,
  state: { enabled: "boolean", time: "number", feedback: "number", wet: "number", pingPong: "boolean" },
  commands: [
    command("SetDelayTime", { time: "number" }),
    command("SetDelayFeedback", { feedback: "number" }),
    command("SetDelayWet", { wet: "number" }),
    command("SetDelayPingPong", { pingPong: "boolean" }),
    command("ToggleDelay", { enabled: "boolean" }),
  ],
  events: [
    event("DelayTimeChanged", { time: "number" }),
    event("DelayFeedbackChanged", { feedback: "number" }),
    event("DelayWetChanged", { wet: "number" }),
    event("DelayPingPongChanged", { pingPong: "boolean" }),
    event("DelayToggled", { enabled: "boolean" }),
  ],
});

const chorusBus = sendBus.aggregate("ChorusBus", {
  root: true,
  state: { enabled: "boolean", rate: "number", depth: "number", wet: "number" },
  commands: [
    command("SetChorusRate", { rate: "number" }),
    command("SetChorusDepth", { depth: "number" }),
    command("SetChorusWet", { wet: "number" }),
    command("ToggleChorus", { enabled: "boolean" }),
  ],
  events: [
    event("ChorusRateChanged", { rate: "number" }),
    event("ChorusDepthChanged", { depth: "number" }),
    event("ChorusWetChanged", { wet: "number" }),
    event("ChorusToggled", { enabled: "boolean" }),
  ],
});

// --- SendBus repositories and services ---

sendBus.repository("ReverbBusRepository", { of: reverbBus });
sendBus.repository("DelayBusRepository", { of: delayBus });
sendBus.repository("ChorusBusRepository", { of: chorusBus });

sendBus.applicationService("SendBusManager", {
  purpose: "configures shared send effect buses",
  uses: [reverbBus, delayBus, chorusBus],
  operations: [
    operation("ConfigureReverb", { input: { size: "number", damping: "number", wet: "number" } }),
    operation("ConfigureDelay", { input: { time: "number", feedback: "number", wet: "number" } }),
    operation("ConfigureChorus", { input: { rate: "number", depth: "number", wet: "number" } }),
  ],
});

// ═══════════════════════════════════════════════════════════════════════════════
// PLAYBACK — sample-accurate tick engine
// ═══════════════════════════════════════════════════════════════════════════════

const playback = app.context("Playback", {
  purpose: "sample-accurate tick engine; walks composition hierarchy and emits note/parameter events",
  meta: {
    rules: [
      "tick accumulation uses fractional carry for sample-accurate timing",
      "tempo changes are immediate (no ramp)",
      "all time arithmetic uses ticks; convert to samples only at the audio boundary",
      "per-track playback state tracks chain row, phrase row, transpose, repeat, and table position",
    ],
    style: "single-threaded tick loop, no shared mutable state",
  },
});

playback.valueObject("NoteEvent", {
  state: { tick: "Ticks", note: "Note", velocity: "Velocity", patchId: "PatchIndex", channel: "TrackIndex", duration: "Ticks | null" },
});

playback.valueObject("ParameterEvent", {
  state: { tick: "Ticks", parameter: "string", value: "number", channel: "TrackIndex" },
});

playback.valueObject("PlayMode", {
  from: "string",
  invariants: ["one of: song, chain, phrase"],
});

const player = playback.aggregate("Player", {
  root: true,
  purpose: "tick accumulator and sequencer spine; walks Song→Chain→Phrase→Table per track",
  state: {
    playing: "boolean",
    playMode: "PlayMode",
    tempo: "BPM",
    tickAccumulator: "number",
    perTrackState: "TrackPlayState[16]",
  },
  invariants: [
    "tick accumulator never goes negative",
    "per-track state array always has exactly 16 entries",
  ],
  commands: [
    command("Play", { mode: "PlayMode" }),
    command("Stop"),
    command("AdvanceSamples", { frames: "number" }),
  ],
  events: [
    event("PlaybackStarted", { mode: "PlayMode" }),
    event("PlaybackStopped"),
    event("TickAdvanced", { tick: "Ticks" }),
    event("NotesEmitted", { count: "number" }),
    event("ParametersEmitted", { count: "number" }),
  ],
});

player.entity("TrackPlayState", {
  state: {
    track: "TrackIndex",
    currentChainRow: "number",
    currentPhraseRow: "number",
    transpose: "number",
    repeatCounter: "number",
    tablePosition: "number | null",
    groovePosition: "number",
  },
});

const transport = playback.aggregate("Transport", {
  root: true,
  purpose: "playback position, seeking, and loop boundaries",
  state: {
    position: "Ticks",
    looping: "boolean",
    loopStart: "Ticks",
    loopEnd: "Ticks",
  },
  commands: [
    command("Seek", { to: "Ticks" }),
    command("SetLooping", { looping: "boolean", start: "Ticks", end: "Ticks" }),
  ],
  events: [
    event("Seeked", { from: "Ticks", to: "Ticks" }),
    event("LoopingChanged", { looping: "boolean", start: "Ticks", end: "Ticks" }),
  ],
});

const grooveApplicator = playback.aggregate("GrooveApplicator", {
  root: true,
  purpose: "applies per-track groove timing; modulates ticks-per-row cadence independently per track",
  state: { perTrackGroove: "Groove | null[16]" },
  commands: [
    command("ApplyGroove", { track: "TrackIndex", groove: "Groove | null" }),
  ],
  events: [
    event("GrooveApplied", { track: "TrackIndex" }),
  ],
});

// --- Playback repositories and services ---

playback.repository("PlayerRepository", { of: player });
playback.repository("TransportRepository", { of: transport });
playback.repository("GrooveApplicatorRepository", { of: grooveApplicator });

playback.applicationService("PlaybackService", {
  purpose: "orchestrates playback lifecycle: start, stop, seek, advance",
  uses: [player, transport, grooveApplicator],
  operations: [
    operation("Play", { input: { mode: "PlayMode" } }),
    operation("Stop", { input: {} }),
    operation("Seek", { input: { to: "Ticks" } }),
    operation("AdvanceSamples", { input: { frames: "number" } }),
  ],
});

// ═══════════════════════════════════════════════════════════════════════════════
// FX COMMANDS — per-step effect commands dispatched during playback
// ═══════════════════════════════════════════════════════════════════════════════

const fxCommands = app.context("FXCommands", {
  purpose: "per-step effect commands triggered at row boundaries; 11 registered commands",
  ubiquitousLanguage: {
    "FxValue": "encoded as (commandId << 8) | paramByte; two per phrase step, three per table step",
    "FxRegistry": "lookup table of all available FX commands with metadata",
  },
  meta: {
    rules: [
      "FX dispatched at row start (initialization) and per-tick (continuous)",
      "each command has a single parameter byte (0-255); semantics vary by command",
    ],
  },
});

const fxRegistry = fxCommands.aggregate("FxRegistry", {
  root: true,
  purpose: "registry of all available FX commands; provides lookup by ID and metadata",
  state: { commands: "FxCommandInfo[]" },
});

fxRegistry.entity("FxCommandInfo", {
  state: { id: "number", name: "string", parameterType: "string", description: "string" },
});

fxCommands.domainService("FxDispatcher", {
  purpose: "dispatches FX commands during playback; called per-row and per-tick by the Player",
  uses: [fxRegistry],
});

// The 11 registered FX commands:
fxCommands.valueObject("ChordCommand", { from: "number", description: "0x05: trigger chord from bank; param = chord index" });
fxCommands.valueObject("ArpCommand", { from: "number", description: "0x06: trigger arpeggiator; param = mode + speed" });
fxCommands.valueObject("RatchetCommand", { from: "number", description: "0x07: retrigger note N times within row; param = N" });
fxCommands.valueObject("SetTempoCommand", { from: "number", description: "0x0B: change BPM; param = offset from base tempo" });
fxCommands.valueObject("SetFilterCutoffCommand", { from: "number", description: "0x14: modulate filter cutoff; param = value 0-255" });
fxCommands.valueObject("DelayCommand", { from: "number", description: "0x20: delay note-on by N ticks; param = tick count" });
fxCommands.valueObject("ProbabilityCommand", { from: "number", description: "0x21: chance-based trigger; param = percentage 0-100" });
fxCommands.valueObject("TableSpeedCommand", { from: "number", description: "0x23: set table tick speed; param = ticks per step" });
fxCommands.valueObject("TableHopCommand", { from: "number", description: "0x24: jump to table row; param = row index" });
fxCommands.valueObject("ChordAddCommand", { from: "number", description: "0x25: add interval to active chord; param = semitones" });
fxCommands.valueObject("ChordInversionCommand", { from: "number", description: "0x26: set chord inversion; param = inversion index" });

// --- FXCommands repository and service ---

fxCommands.repository("FxRegistryRepository", { of: fxRegistry });

fxCommands.applicationService("FxCommandService", {
  purpose: "dispatches FX commands during playback; manages registry of available commands",
  uses: [fxRegistry],
  operations: [
    operation("DispatchFx", { input: { commandId: "number", param: "number", track: "TrackIndex" } }),
    operation("RegisterCommand", { input: { id: "number", name: "string", parameterType: "string" } }),
  ],
});

// ═══════════════════════════════════════════════════════════════════════════════
// PERFORMANCE — live, unscheduled note input
// ═══════════════════════════════════════════════════════════════════════════════

const performance = app.context("Performance", {
  purpose: "live note input bypassing the sequencer; chord-mode triggering and phrase preview",
});

const chordPad = performance.aggregate("ChordPad", {
  root: true,
  purpose: "triggers chords from the chord bank immediately (not sequenced)",
  state: {
    activeChordIndex: "number | null",
    voicing: "Note[]",
    patchId: "PatchIndex | null",
  },
  commands: [
    command("PressChord", { chordIndex: "number" }),
    command("ReleaseChord"),
  ],
  events: [
    event("ChordTriggered", { notes: "Note[]", patchId: "PatchIndex | null" }),
    event("ChordReleased", { notes: "Note[]" }),
  ],
});

const preview = performance.aggregate("Preview", {
  root: true,
  purpose: "triggers single notes for auditioning patches while editing",
  state: {
    activeNote: "Note | null",
    patchId: "PatchIndex | null",
  },
  commands: [
    command("PreviewNote", { note: "Note", velocity: "Velocity", patchId: "PatchIndex" }),
    command("StopPreview"),
  ],
  events: [
    event("NotePreviewTriggered", { note: "Note", velocity: "Velocity", patchId: "PatchIndex" }),
    event("PreviewStopped"),
  ],
});

// --- Performance repositories and services ---

performance.repository("ChordPadRepository", { of: chordPad });
performance.repository("PreviewRepository", { of: preview });

performance.applicationService("PerformanceService", {
  purpose: "handles live note input: chord triggering and note preview",
  uses: [chordPad, preview],
  operations: [
    operation("PressChord", { input: { chordIndex: "number" } }),
    operation("ReleaseChord", { input: {} }),
    operation("PreviewNote", { input: { note: "Note", velocity: "Velocity", patchId: "PatchIndex" } }),
    operation("StopPreview", { input: {} }),
  ],
});

// ═══════════════════════════════════════════════════════════════════════════════
// MIDI IMPORT — MIDI file → Song conversion pipeline
// ═══════════════════════════════════════════════════════════════════════════════

const midiImport = app.context("MIDIImport", {
  purpose: "converts standard MIDI files into Song data through a multi-stage pipeline",
  ubiquitousLanguage: {
    "Quantizer": "snaps MIDI note timing to the nearest grid position",
    "PolyphonySplitter": "splits polyphonic channels into monophonic voices",
    "PatchMapper": "maps GM program numbers to Song patch slots",
    "PhraseBuilder": "clusters note events into 16-row phrases",
    "ChainBuilder": "sequences phrases into chains",
    "ChordGrouper": "identifies simultaneous notes as chords",
  },
});

midiImport.valueObject("MidiImportSettings", {
  state: {
    quantizeGrid: "Division",
    splitPolyphony: "boolean",
    maxTracksPerChannel: "number",
    detectChords: "boolean",
  },
});

const importPipeline = midiImport.aggregate("ImportPipeline", {
  root: true,
  purpose: "orchestrates the full MIDI→Song conversion through all pipeline stages",
  state: {
    settings: "MidiImportSettings",
    status: "string",
  },
  commands: [
    command("ImportMidiFile", { path: "string", settings: "MidiImportSettings" }),
  ],
  events: [
    event("ImportCompleted", { tracksImported: "number", phrasesCreated: "number", chainsCreated: "number" }),
    event("ImportFailed", { reason: "string" }),
  ],
});

midiImport.domainService("Quantizer", { purpose: "snaps MIDI note timing to the nearest grid position" });
midiImport.domainService("PolyphonySplitter", { purpose: "splits polyphonic MIDI channels into monophonic voices (one per track)" });
midiImport.domainService("PatchMapper", { purpose: "maps GM program numbers to Song patches via GmProgramNames lookup" });
midiImport.domainService("PhraseBuilder", { purpose: "clusters quantized note events into 16-row phrases based on timing" });
midiImport.domainService("ChainBuilder", { purpose: "sequences built phrases into chains for song arrangement" });
midiImport.domainService("ChordGrouper", { purpose: "identifies simultaneous notes as chords and applies Chord FX commands" });

// --- MIDIImport repository and service ---

midiImport.repository("ImportPipelineRepository", { of: importPipeline });

midiImport.applicationService("MidiImportService", {
  purpose: "runs the full import pipeline: load MIDI file, quantize, split, map, build phrases/chains",
  uses: [importPipeline],
  operations: [
    operation("ImportFile", { input: { path: "string", settings: "MidiImportSettings" } }),
  ],
});

// ═══════════════════════════════════════════════════════════════════════════════
// EDITOR — user-facing editing state and navigation
// ═══════════════════════════════════════════════════════════════════════════════

const editor = app.context("Editor", {
  purpose: "view navigation, cursor state, overlay system, and per-view rendering",
  ubiquitousLanguage: {
    "ViewStack": "LIFO navigation; cursor position carries context IDs when drilling down",
    "Overlay": "modal dialog layered above the active view; EXIT pops one overlay",
    "QuickJump": "overlay for direct navigation to a specific view + ID",
  },
});

editor.valueObject("ViewKind", {
  from: "string",
  invariants: ["one of: mainMenu, song, chain, phrase, patch, table, mixer, chordDesigner, groove, masterEffects, projectSettings"],
});

editor.valueObject("ViewContext", {
  state: { view: "ViewKind", id: "number | null" },
  description: "a view + the ID of what's being edited (e.g. phrase #42)",
});

const viewStack = editor.aggregate("ViewStack", {
  root: true,
  purpose: "LIFO navigation stack with context-aware drill-down",
  state: {
    stack: "ViewContext[]",
    activeView: "ViewContext",
  },
  commands: [
    command("PushView", { view: "ViewKind", id: "number | null" }),
    command("PopView"),
    command("ReplaceView", { view: "ViewKind", id: "number | null" }),
  ],
  events: [
    event("ViewPushed", { view: "ViewKind", id: "number | null" }),
    event("ViewPopped", { view: "ViewKind", id: "number | null" }),
    event("ViewReplaced", { from: "ViewKind", to: "ViewKind" }),
  ],
});

const cursor = editor.aggregate("Cursor", {
  root: true,
  purpose: "current editing position within a view's grid",
  state: { row: "number", column: "number" },
  commands: [
    command("MoveCursor", { direction: "string", amount: "number" }),
    command("SetCursorPosition", { row: "number", column: "number" }),
  ],
  events: [
    event("CursorMoved", { fromRow: "number", fromCol: "number", toRow: "number", toCol: "number" }),
  ],
});

const overlayStack = editor.aggregate("OverlayStack", {
  root: true,
  purpose: "LIFO modal dialog queue; survives view navigation; EXIT pops one overlay",
  state: { overlays: "OverlayConfig[]" },
  commands: [
    command("PushOverlay", { type: "string", config: "object" }),
    command("PopOverlay"),
  ],
  events: [
    event("OverlayPushed", { type: "string" }),
    event("OverlayPopped", { type: "string" }),
  ],
});

editor.valueObject("OverlayType", {
  from: "string",
  invariants: ["one of: confirm, listPicker, parameterList, filePicker, quickJump"],
});

// --- Editor repositories and services ---

editor.repository("ViewStackRepository", { of: viewStack });
editor.repository("CursorRepository", { of: cursor });
editor.repository("OverlayStackRepository", { of: overlayStack });

editor.applicationService("NavigationService", {
  purpose: "handles view navigation: push, pop, replace, and cursor movement",
  uses: [viewStack, cursor, overlayStack],
  operations: [
    operation("PushView", { input: { view: "ViewKind", id: "number | null" } }),
    operation("PopView", { input: {} }),
    operation("MoveCursor", { input: { direction: "string", amount: "number" } }),
    operation("PushOverlay", { input: { type: "string", config: "object" } }),
    operation("PopOverlay", { input: {} }),
  ],
});

// ═══════════════════════════════════════════════════════════════════════════════
// CONTROLS — input translation
// ═══════════════════════════════════════════════════════════════════════════════

const controls = app.context("Controls", {
  purpose: "translates raw gamepad/keyboard input into semantic 8-button actions",
  meta: {
    rules: [
      "8 semantic buttons: UP, DOWN, LEFT, RIGHT, A (confirm), B (back/exit), SELECT, START",
      "input routing priority: global overlays → view overlays → active view",
      "EXIT (B button) always pops exactly one navigation layer",
    ],
  },
});

const inputSource = controls.port("InputSource", {
  contract: { onEvent: "(handler: (event: InputEvent) => void) => Unsubscribe" },
});

const inputRouter = controls.aggregate("InputRouter", {
  root: true,
  purpose: "maps raw input events to context-specific commands based on active view and overlay state",
  state: {
    activeProfile: "string",
    bindings: "Binding[]",
  },
  commands: [
    command("BindInput", { event: "string", action: "string", target: "string" }),
    command("UnbindInput", { event: "string" }),
    command("SetActiveProfile", { profile: "string" }),
  ],
  events: [
    event("InputBound", { event: "string", action: "string", target: "string" }),
    event("InputUnbound", { event: "string" }),
    event("ProfileChanged", { from: "string", to: "string" }),
  ],
});

inputRouter.entity("Binding", {
  state: { event: "string", action: "string", target: "string" },
});

// --- Controls repository and service ---

controls.repository("InputRouterRepository", { of: inputRouter });

controls.applicationService("InputService", {
  purpose: "handles input binding configuration and input event routing",
  uses: [inputRouter],
  operations: [
    operation("BindInput", { input: { event: "string", action: "string", target: "string" } }),
    operation("UnbindInput", { input: { event: "string" } }),
    operation("SetActiveProfile", { input: { profile: "string" } }),
  ],
});

// ═══════════════════════════════════════════════════════════════════════════════
// CONTEXT MAP
// ═══════════════════════════════════════════════════════════════════════════════

app.contextMap([
  // Kernel shared by all
  relationship("Composition", "Kernel", { kind: "shared-kernel" }),
  relationship("Synthesis", "Kernel", { kind: "shared-kernel" }),
  relationship("MIDITransformers", "Kernel", { kind: "shared-kernel" }),
  relationship("AudioEffects", "Kernel", { kind: "shared-kernel" }),
  relationship("SendBus", "Kernel", { kind: "shared-kernel" }),
  relationship("Playback", "Kernel", { kind: "shared-kernel" }),
  relationship("FXCommands", "Kernel", { kind: "shared-kernel" }),
  relationship("Performance", "Kernel", { kind: "shared-kernel" }),
  relationship("MIDIImport", "Kernel", { kind: "shared-kernel" }),
  relationship("Editor", "Kernel", { kind: "shared-kernel" }),
  relationship("Controls", "Kernel", { kind: "shared-kernel" }),

  // Playback reads Composition structure
  relationship("Playback", "Composition", { kind: "customer-supplier", direction: "downstream" }),

  // FXCommands invoked by Playback during tick processing
  relationship("FXCommands", "Playback", { kind: "customer-supplier", direction: "downstream" }),

  // Performance reads Composition (chords, patches)
  relationship("Performance", "Composition", { kind: "customer-supplier", direction: "downstream" }),

  // MIDITransformers process note events from Playback and Performance
  relationship("MIDITransformers", "Playback", { kind: "customer-supplier", direction: "downstream" }),
  relationship("MIDITransformers", "Performance", { kind: "customer-supplier", direction: "downstream" }),

  // Synthesis consumes transformed note events
  relationship("Synthesis", "MIDITransformers", { kind: "customer-supplier", direction: "downstream" }),

  // AudioEffects process Synthesis output
  relationship("AudioEffects", "Synthesis", { kind: "customer-supplier", direction: "downstream" }),

  // SendBus receives routed audio
  relationship("SendBus", "AudioEffects", { kind: "customer-supplier", direction: "downstream" }),

  // MIDIImport writes to Composition
  relationship("MIDIImport", "Composition", { kind: "customer-supplier", direction: "upstream" }),

  // Editor reads/writes Composition, reads Playback
  relationship("Editor", "Composition", { kind: "customer-supplier", direction: "both" }),
  relationship("Editor", "Playback", { kind: "customer-supplier", direction: "downstream" }),

  // Controls drives Editor, Playback, Performance
  relationship("Controls", "Editor", { kind: "customer-supplier", direction: "downstream" }),
  relationship("Controls", "Playback", { kind: "customer-supplier", direction: "downstream" }),
  relationship("Controls", "Performance", { kind: "customer-supplier", direction: "downstream" }),
]);

// ═══════════════════════════════════════════════════════════════════════════════
// INVARIANTS
// ═══════════════════════════════════════════════════════════════════════════════

app.invariants([
  invariant("exactly 16 tracks at all times", {
    meta: { rationale: "fixed grid size; no dynamic track creation" },
  }),
  invariant("Song is the single root document; all data nested within", {
    meta: { rationale: "simple serialization model; one file = one song" },
  }),
  invariant("all mutations flow through a central dispatcher", {
    meta: { rationale: "single audit point; enables undo/redo" },
  }),
  invariant("actions are immutable records", {
    meta: { rationale: "enables event replay and debugging" },
  }),
  invariant("one action handler per domain concern", {
    meta: { rationale: "SRP; each handler owns mutations for one bounded context" },
  }),
  invariant("views never mutate state directly", {
    meta: { rationale: "unidirectional data flow; views dispatch actions, handlers mutate" },
  }),
  invariant("Playback never mutates Composition aggregates", {
    meta: { rationale: "Composition is the model; Playback is the engine that reads it" },
  }),
  invariant("tick engine uses fractional carry for sample-accurate timing", {
    meta: { rationale: "prevents timing drift at any tempo" },
  }),
  invariant("per-step FX encoding uses high byte for command, low byte for value", {
    meta: { rationale: "compact representation; 256 commands × 256 values" },
  }),
  invariant("all features accessible via 8 semantic buttons", {
    meta: { rationale: "portable to handheld devices without touch or mouse" },
  }),
  invariant("contexts do not reach into each other's internals except via declared relationships", {
    meta: { rationale: "context boundaries are enforced; integration is explicit" },
  }),
  invariant("domain layer has no infrastructure imports", {
    meta: { rationale: "preserves Clean Architecture dependency rule" },
  }),
]);
