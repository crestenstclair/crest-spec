// =============================================================================
// instrument-core.ts
//
// crest-spec for the shared Rust synth engine that powers both the standalone
// synth (nih-plug shell) and the gamepad tracker (eframe shell). It is a
// polyphonic, MPE-capable synth with BOTH true synthesis voices (oscillators,
// FM) AND sample-playback voices — not "just" a rompler.
//
// ORGANIZING PRINCIPLE — THE REAL-TIME BOUNDARY
// ---------------------------------------------
// Contexts split along the audio thread. The NON-RT contexts (Patch,
// SampleLibrary, Parameter, Preset) own editable, allocating, persistable
// state. The RT contexts (VoiceAllocation, Synthesis, Modulation, Rendering)
// run inside process() and may never allocate, lock, or block. Everything that
// crosses from non-RT to RT does so as an immutable, versioned *snapshot*
// published over a lock-free channel; sample/graph memory crosses as an Arc
// handoff and is freed off-thread (basedrop). This is the anti-corruption seam
// of the whole system.
//
// CORRECTNESS PROPERTY (the crest-spec monotonic-regeneration analog)
// -------------------------------------------------------------------
// The realized runtime graph (VoicePool, Voices, ModulationMatrix, MixBus) is a
// deterministic projection of (active PatchSnapshot, ParameterSnapshot, ordered
// PerformanceEvent stream). Derived runtime state is rebuilt ONLY when an
// upstream snapshot version changes, and the rebuild is pure and idempotent.
//
// Type strings are Rust (Option<T>, Arc<T>, Vec<T>, [T; N], f32, u8, ...).
// =============================================================================

import { project, command, event, operation, invariant, relationship, layer } from "crest-spec";

const app = project("instrument-core", {
  layers: ["domain", "application", "infrastructure", "interface"],
  rules: [
    layer("domain").dependsOn([]),
    layer("application").dependsOn(["domain"]),
    layer("infrastructure").dependsOn(["application", "domain"]),
    layer("interface").dependsOn(["application", "domain"]),
  ],
  meta: {
    language: "rust",
    framework: "no_std core; nih-plug + eframe + cpal shells",
    style:
      "data-oriented, ECS-free, real-time-safe. Fixed-size arrays over heap in RT paths. " +
      "Enum dispatch over trait objects in per-sample hot loops. Newtype value objects.",
    avoid: [
      "heap allocation, locking, or blocking on the audio thread",
      "trait objects / dynamic dispatch in the per-sample inner loop (prefer enum dispatch)",
      "panics or unwraps in render()",
      "shared mutable state across the RT boundary (snapshots only)",
      "free() on the audio thread (hand back to basedrop instead)",
    ],
    prompts: [
      "the `domain` layer MUST compile under no_std (alloc allowed only behind a feature for std builds)",
      "RT hot paths get #[inline]; render() is branch-light and allocation-free",
      "prefer fundsp for filters / oscillators / reverbs; dasp for sample interpolation",
      "snapshots are Arc<...Snapshot>; cross the boundary via rtrb; deallocate via basedrop",
    ],
    references: ["./instrument-core.md", "./crest-spec.md", "./tracker.md"],
  },
});

// =============================================================================
// KERNEL — shared primitives (shared-kernel with every other context)
// =============================================================================

const kernel = app.context("Kernel", {
  purpose: "real-time-safe value primitives shared across every context",
  ubiquitousLanguage: {
    Frame: "one stereo sample slot; the unit of audio time within a block",
    Block: "a contiguous run of frames processed in a single render() call",
    NormalizedValue: "a unipolar 0..1 control value; the canonical control unit",
    Bipolar: "a -1..1 control value; the canonical modulation unit",
    NoteId: "identifies one specific note-on instance (not a pitch); enables per-note/MPE expression",
  },
});

kernel.valueObject("SampleRate", { from: "f32", description: "frames per second", invariants: ["> 0.0", "typically 44100 or 48000"] });
kernel.valueObject("FrameCount", { from: "usize", description: "number of frames in a block", invariants: [">= 0"] });
kernel.valueObject("SampleOffset", { from: "usize", description: "frame offset of an event within a block", invariants: ["< block FrameCount"] });
kernel.valueObject("NoteNumber", { from: "u8", description: "MIDI note number", invariants: ["0..=127"] });
kernel.valueObject("NoteId", { from: "u64", description: "monotonic id for one note-on instance", invariants: ["strictly increasing per engine session"] });
kernel.valueObject("Velocity", { from: "u8", description: "note-on/off velocity", invariants: ["0..=127"] });
kernel.valueObject("Pitch", { from: "f32", description: "fundamental frequency in Hz", invariants: ["> 0.0"] });
kernel.valueObject("Cents", { from: "f32", description: "pitch offset in cents (100 cents = 1 semitone)" });
kernel.valueObject("NormalizedValue", { from: "f32", description: "unipolar control value", invariants: ["0.0..=1.0"] });
kernel.valueObject("Bipolar", { from: "f32", description: "bipolar modulation value", invariants: ["-1.0..=1.0"] });
kernel.valueObject("Gain", { from: "f32", description: "linear amplitude multiplier", invariants: [">= 0.0"] });
kernel.valueObject("Pan", { from: "f32", description: "stereo position", invariants: ["-1.0 (L) ..= 1.0 (R)"] });
kernel.valueObject("MidiChannel", { from: "u8", description: "MIDI channel", invariants: ["0..=15"] });
kernel.valueObject("StereoFrame", { state: { left: "f32", right: "f32" }, description: "one stereo sample" });
kernel.valueObject("SnapshotVersion", { from: "u64", description: "monotonic version stamp on any published snapshot", invariants: ["strictly increasing; equal version => identical content"] });

// =============================================================================
// PERFORMANCE — anti-corruption layer for all external input (MIDI/MPE/gamepad)
// =============================================================================

const performance = app.context("Performance", {
  purpose: "translate raw external input into the engine's normalized note + expression language",
  ubiquitousLanguage: {
    PerformanceEvent: "a sample-accurate, normalized intent (note/expression/controller) consumed downstream",
    Expression: "continuous per-note control: pitch bend, pressure, timbre (the MPE triad)",
    SustainState: "whether held notes are sustained by the damper pedal",
  },
  meta: {
    notes: "ACL: raw MIDI bytes, MPE channel rotation, and gilrs gamepad events never escape this context. " +
           "Only Kernel-typed PerformanceEvents do.",
  },
});

performance.valueObject("PitchBend", { from: "Bipolar", description: "normalized pitch bend, scaled to range downstream" });
performance.valueObject("Pressure", { from: "NormalizedValue", description: "channel/poly aftertouch (MPE Z)" });
performance.valueObject("Timbre", { from: "NormalizedValue", description: "MPE Y / CC74 slide" });
performance.valueObject("ControllerId", { from: "u8", description: "MIDI CC number", invariants: ["0..=127"] });

const inputSource = performance.port("InputSource", {
  contract: {
    drain: "(into: &mut PerformanceBuffer) -> ()", // pull pending raw input, already time-stamped
  },
});

const perfState = performance.aggregate("PerformanceState", {
  root: true,
  purpose: "hold live controller/expression state and emit normalized, ordered PerformanceEvents",
  state: {
    perChannelBend: "[PitchBend; 16]",
    perChannelPressure: "[Pressure; 16]",
    perChannelTimbre: "[Timbre; 16]",
    sustain: "bool",
    nextNoteId: "NoteId",
    mpeEnabled: "bool",
  },
  invariants: [
    "every emitted NoteOn carries a fresh NoteId from nextNoteId",
    "in MPE mode, member-channel state maps to the active note on that channel",
    "events are emitted in non-decreasing SampleOffset order within a block",
  ],
  commands: [
    command("IngestNoteOn", { channel: "MidiChannel", note: "NoteNumber", velocity: "Velocity", at: "SampleOffset" }),
    command("IngestNoteOff", { channel: "MidiChannel", note: "NoteNumber", velocity: "Velocity", at: "SampleOffset" }),
    command("IngestPitchBend", { channel: "MidiChannel", value: "PitchBend", at: "SampleOffset" }),
    command("IngestPressure", { channel: "MidiChannel", value: "Pressure", at: "SampleOffset" }),
    command("IngestTimbre", { channel: "MidiChannel", value: "Timbre", at: "SampleOffset" }),
    command("IngestController", { channel: "MidiChannel", controller: "ControllerId", value: "NormalizedValue", at: "SampleOffset" }),
    command("SetSustain", { on: "bool", at: "SampleOffset" }),
  ],
  events: [
    event("NoteTriggered", { noteId: "NoteId", note: "NoteNumber", velocity: "Velocity", channel: "MidiChannel", at: "SampleOffset" }),
    event("NoteReleased", { noteId: "NoteId", velocity: "Velocity", at: "SampleOffset" }),
    event("ExpressionChanged", { noteId: "NoteId | null", dimension: "string", value: "Bipolar", at: "SampleOffset" }),
    event("ControllerChanged", { controller: "ControllerId", value: "NormalizedValue", at: "SampleOffset" }),
  ],
});

performance.domainService("MpeChannelRouter", {
  purpose: "resolve which sounding NoteId a member-channel expression message belongs to",
  uses: [perfState],
});

// =============================================================================
// PATCH — the editable, declarative instrument definition ("instrument as data")
// =============================================================================

const patch = app.context("Patch", {
  purpose: "author and hold the declarative instrument: zones, generators, filters, amp, default mod routings, macros",
  ubiquitousLanguage: {
    Patch: "the complete declarative specification of one instrument",
    Zone: "a key/velocity region that maps to a sound source (sampled or synthesized)",
    GeneratorSpec: "declarative description of a sound source (wavetable, FM, or sample player)",
    PlayMode: "poly / mono / legato voicing behavior",
    PatchSnapshot: "an immutable, versioned projection of a Patch published to the RT side",
  },
  meta: { notes: "Design-time, allocating, serde-serializable. NOT touched by the audio thread directly." },
});

patch.valueObject("KeyRange", { state: { low: "NoteNumber", high: "NoteNumber" }, invariants: ["low <= high"] });
patch.valueObject("VelocityRange", { state: { low: "Velocity", high: "Velocity" }, invariants: ["low <= high"] });
patch.valueObject("GeneratorKind", { from: "string", description: "tag", invariants: ['one of "wavetable" | "fm" | "sample"'] });
patch.valueObject("FilterKind", { from: "string", invariants: ['one of "lp" | "hp" | "bp" | "notch" | "comb" | "off"'] });
patch.valueObject("EnvelopeSpec", {
  state: { delay: "f32", attack: "f32", hold: "f32", decay: "f32", sustain: "NormalizedValue", release: "f32" },
  description: "DAHDSR envelope shape in seconds (sustain is a level)",
  invariants: ["all times >= 0.0"],
});
patch.valueObject("LfoSpec", {
  state: { shape: "string", rateHz: "f32", retrigger: "bool", phase: "NormalizedValue", bipolar: "bool" },
  invariants: ["rateHz > 0.0"],
});
patch.valueObject("ModRoutingSpec", {
  state: { source: "ModSourceId", destination: "ModDestinationId", depth: "Bipolar", curve: "ModCurve" },
  description: "one default routing baked into the patch",
});
patch.valueObject("MacroSpec", { state: { name: "string", value: "NormalizedValue" }, description: "a user-facing macro knob the OXI One / GUI can drive" });

const patchAgg = patch.aggregate("Patch", {
  root: true,
  purpose: "owns the full instrument definition and is the single source of truth for the realized graph",
  state: {
    id: "PatchId",
    name: "string",
    playMode: "PlayMode",
    maxVoices: "u8",
    glide: "f32",
    zones: "Vec<Zone>",
    routings: "Vec<ModRoutingSpec>",
    macros: "Vec<MacroSpec>",
    version: "SnapshotVersion",
  },
  invariants: [
    "maxVoices >= 1 and <= engine voice ceiling",
    "every Zone.generator references a GeneratorSpec that exists in the patch",
    "every ModRoutingSpec.destination is a valid, addressable parameter or per-voice target",
    "mono/legato playMode implies the realized VoicePool caps at 1 active voice",
    "any structural edit bumps version (monotonic)",
  ],
  commands: [
    command("Rename", { name: "string" }),
    command("SetPlayMode", { playMode: "PlayMode" }),
    command("SetMaxVoices", { maxVoices: "u8" }),
    command("AddZone", { keyRange: "KeyRange", velRange: "VelocityRange", generator: "GeneratorKind" }),
    command("RemoveZone", { index: "usize" }),
    command("AddRouting", { routing: "ModRoutingSpec" }),
    command("RemoveRouting", { index: "usize" }),
    command("SetMacro", { index: "usize", value: "NormalizedValue" }),
  ],
  events: [
    event("PatchRenamed", { from: "string", to: "string" }),
    event("PlayModeChanged", { from: "PlayMode", to: "PlayMode" }),
    event("ZoneAdded", { index: "usize", keyRange: "KeyRange", velRange: "VelocityRange" }),
    event("ZoneRemoved", { index: "usize" }),
    event("RoutingAdded", { index: "usize", routing: "ModRoutingSpec" }),
    event("RoutingRemoved", { index: "usize" }),
    event("PatchVersionBumped", { from: "SnapshotVersion", to: "SnapshotVersion" }),
  ],
});

patchAgg.entity("Zone", {
  state: {
    keyRange: "KeyRange",
    velRange: "VelocityRange",
    generator: "GeneratorSpec",
    filterKind: "FilterKind",
    filterEnv: "EnvelopeSpec",
    ampEnv: "EnvelopeSpec",
    tune: "Cents",
    gain: "Gain",
    pan: "Pan",
  },
});

patchAgg.entity("GeneratorSpec", {
  state: {
    kind: "GeneratorKind",
    // sample-backed:
    sampleRef: "SampleId | null",
    loopMode: "string",
    // synthesized:
    waveform: "String | null",
    unison: "u8",
    fmRatio: "f32 | null",
  },
});

patch.repository("PatchRepository", { of: patchAgg });

patch.applicationService("PatchEditingService", {
  purpose: "apply edit commands to a Patch and publish a fresh PatchSnapshot to the RT side",
  uses: [patchAgg],
  operations: [
    operation("applyEdit", { input: { patchId: "PatchId", command: "PatchCommand" } }),
    operation("publishSnapshot", { input: { patchId: "PatchId" } }),
  ],
  meta: { notes: "publishSnapshot freezes the Patch into Arc<PatchSnapshot> and sends it over rtrb to the engine." },
});

// =============================================================================
// SAMPLE LIBRARY — sample assets, decoding, the in-RAM pool, RT handoff
// =============================================================================

const samples = app.context("SampleLibrary", {
  purpose: "load/decode sample files and own the reference-counted in-memory sample pool",
  ubiquitousLanguage: {
    SampleData: "immutable decoded PCM shared with the audio thread via Arc",
    SampleBank: "the set of currently resident samples and their ref counts",
    Streaming: "reading long samples from disk in chunks instead of resident RAM",
  },
  meta: { notes: "Loading/decoding (symphonia/hound) is off-thread. The RT side only ever holds Arc<SampleData>." },
});

samples.valueObject("SampleId", { from: "u32", description: "stable id for a resident sample" });
samples.valueObject("LoopPoints", { state: { start: "usize", end: "usize", mode: "string" }, invariants: ["start <= end"] });
samples.valueObject("SampleMeta", { state: { rate: "SampleRate", channels: "u8", frames: "usize" } });
samples.valueObject("SampleData", {
  state: { meta: "SampleMeta", pcm: "Arc<[f32]>", loop: "LoopPoints | null" },
  description: "immutable; only ever shared, never mutated, across the RT boundary",
});

const sampleLoader = samples.port("SampleLoader", {
  contract: {
    decode: "(path: &Path) -> Result<SampleData, LoadError>",
    decodeStreaming: "(path: &Path) -> Result<SampleStream, LoadError>",
  },
});

const sampleBank = samples.aggregate("SampleBank", {
  root: true,
  purpose: "manage resident samples and hand immutable Arc<SampleData> to the engine; reclaim off-thread",
  state: { resident: "HashMap<SampleId, SampleData>", refCounts: "HashMap<SampleId, u32>" },
  invariants: [
    "a SampleId referenced by the active PatchSnapshot is always resident",
    "samples are never mutated after decode; updates produce a new SampleId",
    "eviction only happens when refCount reaches zero AND no pending snapshot references it",
  ],
  commands: [
    command("LoadSample", { path: "String" }),
    command("UnloadSample", { id: "SampleId" }),
    command("Retain", { id: "SampleId" }),
    command("Release", { id: "SampleId" }),
  ],
  events: [
    event("SampleLoaded", { id: "SampleId", meta: "SampleMeta" }),
    event("SampleUnloaded", { id: "SampleId" }),
    event("LoadFailed", { path: "String", reason: "string" }),
  ],
});

samples.repository("SampleBankRepository", { of: sampleBank });

samples.applicationService("SampleLoadingService", {
  purpose: "decode files off-thread and publish Arc<SampleData> + a SampleSnapshot to the engine",
  uses: [sampleBank],
  operations: [
    operation("loadForPatch", { input: { patchId: "PatchId" } }),
    operation("collectGarbage", { input: {} }), // drain basedrop-returned samples and evict
  ],
});

// =============================================================================
// PARAMETER — canonical parameter store, smoothing, automation, host/GUI bridge
// =============================================================================

const parameters = app.context("Parameter", {
  purpose: "own canonical parameter values, smoothing, and automation; bridge host/GUI to the engine",
  ubiquitousLanguage: {
    Parameter: "a host/GUI-addressable continuous control with a range and a smoother",
    Gesture: "a begin/end-bracketed user edit (for host automation write)",
    ParameterSnapshot: "smoothed per-block parameter values handed to the RT side via triple_buffer",
  },
  meta: { notes: "Backed by nih-plug's param system in the plugin shell; a plain store in the standalone shell." },
});

parameters.valueObject("ParameterId", { from: "u32", description: "stable, host-visible parameter id" });
parameters.valueObject("ParamRange", { state: { min: "f32", max: "f32", default: "f32", skew: "f32" }, invariants: ["min < max"] });
parameters.valueObject("SmoothingMs", { from: "f32", invariants: [">= 0.0"] });

const parameterHost = parameters.port("ParameterHost", {
  contract: {
    get: "(id: ParameterId) -> NormalizedValue",
    set: "(id: ParameterId, value: NormalizedValue) -> ()",
    beginGesture: "(id: ParameterId) -> ()",
    endGesture: "(id: ParameterId) -> ()",
  },
});

const paramSet = parameters.aggregate("ParameterSet", {
  root: true,
  purpose: "hold canonical parameter values and produce a smoothed ParameterSnapshot per block",
  state: { values: "HashMap<ParameterId, NormalizedValue>", ranges: "HashMap<ParameterId, ParamRange>", smoothing: "HashMap<ParameterId, SmoothingMs>" },
  invariants: [
    "every value lies within its ParamRange after denormalization",
    "the RT side reads only the smoothed snapshot, never the canonical store",
  ],
  commands: [
    command("SetParameter", { id: "ParameterId", value: "NormalizedValue" }),
    command("BeginGesture", { id: "ParameterId" }),
    command("EndGesture", { id: "ParameterId" }),
    command("LoadAutomation", { id: "ParameterId", points: "Vec<(SampleOffset, NormalizedValue)>" }),
  ],
  events: [
    event("ParameterChanged", { id: "ParameterId", from: "NormalizedValue", to: "NormalizedValue" }),
    event("GestureBegan", { id: "ParameterId" }),
    event("GestureEnded", { id: "ParameterId" }),
  ],
});

parameters.applicationService("ParameterService", {
  purpose: "apply param edits and publish the per-block smoothed ParameterSnapshot",
  uses: [paramSet],
  operations: [operation("publishSnapshot", { input: { block: "FrameCount" } })],
});

// =============================================================================
// MODULATION — the global-source-pool mod matrix (RT evaluation)
// =============================================================================

const modulation = app.context("Modulation", {
  purpose: "evaluate modulation sources per block/voice and mix them onto destinations (the mod matrix)",
  ubiquitousLanguage: {
    ModSource: "anything that produces a Bipolar stream: LFO, envelope, random, macro, expression, sequencer, CC",
    GlobalSource: "a source in the shared pool, evaluated once per block and shared by all voices",
    PerVoiceSource: "a source instanced per voice (e.g. per-note envelope), evaluated per voice",
    ModDestination: "an addressable modulation target (pitch, cutoff, sample-start, gain, pan, FX send, ...)",
    ModulationMatrix: "the realized set of routings that mixes evaluated sources onto destinations",
  },
  meta: { notes: "Realizes Patch.routings against the live source pool. Per-voice sources live with the Voice in Synthesis." },
});

modulation.valueObject("ModSourceId", { from: "u16", description: "id into the global source pool or a per-voice source slot" });
modulation.valueObject("ModDestinationId", { from: "u16", description: "addressable modulation target id" });
modulation.valueObject("ModCurve", { from: "string", invariants: ['one of "linear" | "exp" | "log" | "sCurve"'] });
modulation.valueObject("Slew", { from: "f32", description: "per-destination smoothing of summed modulation", invariants: [">= 0.0"] });

const modSource = modulation.port("ModSource", {
  contract: {
    evaluate: "(ctx: &ModContext, out: &mut [Bipolar]) -> ()", // fill one block of bipolar modulation
    retrigger: "() -> ()",
  },
});

modulation.aggregate("LfoSource", { implements: modSource, purpose: "free or tempo-synced LFO", state: { spec: "LfoSpec", phase: "f32" } });
modulation.aggregate("EnvelopeSource", { implements: modSource, purpose: "DAHDSR envelope (typically per-voice)", state: { spec: "EnvelopeSpec", stage: "string", level: "NormalizedValue" } });
modulation.aggregate("RandomSource", { implements: modSource, purpose: "sample & hold / smooth random", state: { rateHz: "f32", smooth: "bool", current: "Bipolar" } });
modulation.aggregate("MacroSource", { implements: modSource, purpose: "exposes a Patch macro as a mod source", state: { macroIndex: "usize" } });
modulation.aggregate("ExpressionSource", { implements: modSource, purpose: "MPE pressure/timbre/bend as a per-note source", state: { dimension: "string" } });
modulation.aggregate("SequencerSource", { implements: modSource, purpose: "a Table-style step/automation lane (mirrors tracker Table)", state: { steps: "Vec<Bipolar>", rateHz: "f32", position: "usize" } });

const modMatrix = modulation.aggregate("ModulationMatrix", {
  root: true,
  purpose: "consistency boundary for the realized routing set; evaluates and applies modulation per block",
  state: {
    routings: "Vec<RealizedRouting>",
    globalSources: "Vec<ModSourceId>",
    slew: "HashMap<ModDestinationId, Slew>",
    version: "SnapshotVersion",
  },
  invariants: [
    "depth is Bipolar; total applied modulation at a destination is clamped to the destination's valid range",
    "global sources are evaluated exactly once per block; per-voice sources once per active voice",
    "no routing forms a feedback cycle among modulation-rate destinations",
    "matrix is a pure projection of PatchSnapshot.routings + the live source pool",
  ],
  commands: [
    command("Rebuild", { from: "PatchSnapshot" }),
    command("SetDepth", { routing: "usize", depth: "Bipolar" }),
    command("AddRouting", { routing: "RealizedRouting" }),
    command("RemoveRouting", { routing: "usize" }),
  ],
  events: [
    event("MatrixRebuilt", { version: "SnapshotVersion" }),
    event("DepthChanged", { routing: "usize", from: "Bipolar", to: "Bipolar" }),
  ],
});

modulation.domainService("ModulationEvaluator", {
  purpose: "evaluate all sources for a block and accumulate per-destination modulation for a voice",
  uses: [modMatrix],
});

// =============================================================================
// VOICE ALLOCATION — polyphony, voice stealing, MPE channel mapping (RT)
// =============================================================================

const allocation = app.context("VoiceAllocation", {
  purpose: "map note-on/off events to voice slots, steal under pressure, enforce play mode",
  ubiquitousLanguage: {
    VoicePool: "the fixed array of voice slots and the live NoteId -> slot mapping",
    Stealing: "freeing a sounding voice to honor a new note-on when the pool is full",
    PlayMode: "poly / mono / legato",
  },
  meta: { notes: "Sole owner of voice lifecycle. Synthesis reports completion; it never frees its own slot." },
});

allocation.valueObject("VoiceIndex", { from: "u8", description: "slot index into the VoicePool" });
allocation.valueObject("StealPolicy", { from: "string", invariants: ['one of "oldest" | "quietest" | "lowestPriority"'] });
allocation.valueObject("PlayMode", { from: "string", invariants: ['one of "poly" | "mono" | "legato"'] });

const voicePool = allocation.aggregate("VoicePool", {
  root: true,
  purpose: "the consistency boundary for which notes occupy which voices",
  state: {
    slots: "[VoiceSlot; 64]",
    noteToSlot: "HashMap<NoteId, VoiceIndex>",
    playMode: "PlayMode",
    maxVoices: "u8",
    stealPolicy: "StealPolicy",
    glide: "f32",
  },
  invariants: [
    "at most maxVoices slots are active simultaneously",
    "mono/legato => at most one active slot; legato reuses the slot and glides, mono retriggers envelopes",
    "stealing is deterministic for a given (state, StealPolicy, note)",
    "a NoteId maps to at most one slot; releasing an unknown NoteId is a no-op",
  ],
  commands: [
    command("Allocate", { noteId: "NoteId", note: "NoteNumber", velocity: "Velocity", at: "SampleOffset" }),
    command("Release", { noteId: "NoteId", at: "SampleOffset" }),
    command("Steal", { target: "VoiceIndex" }),
    command("FreeFinished", { index: "VoiceIndex" }),
    command("SetPlayMode", { playMode: "PlayMode" }),
    command("AllNotesOff", {}),
  ],
  events: [
    event("VoiceAllocated", { index: "VoiceIndex", noteId: "NoteId" }),
    event("VoiceStolen", { index: "VoiceIndex", from: "NoteId", to: "NoteId" }),
    event("VoiceReleased", { index: "VoiceIndex", noteId: "NoteId" }),
    event("VoiceFreed", { index: "VoiceIndex" }),
  ],
});

voicePool.entity("VoiceSlot", {
  state: { active: "bool", noteId: "NoteId | null", startFrame: "u64", priority: "u8" },
});

allocation.domainService("VoiceStealer", {
  purpose: "choose the victim voice under the active StealPolicy",
  uses: [voicePool],
});

// =============================================================================
// SYNTHESIS — the realized Voice and its sound generators (RT, hot path)
// =============================================================================

const synthesis = app.context("Synthesis", {
  purpose: "render one sounding note: generators -> filter -> amp, driven by pitch and modulation",
  ubiquitousLanguage: {
    Voice: "the realized, sounding projection of a Patch zone for one NoteId",
    SoundGenerator: "a polymorphic per-voice audio source (wavetable, FM, sample player)",
    "Per-voice modulation": "envelopes and per-note expression instanced inside the Voice",
  },
  meta: { notes: "Innermost hot loop. Enum dispatch over generators; no allocation; #[inline] throughout." },
});

synthesis.valueObject("VoiceId", { from: "u32", description: "engine-internal id for a realized voice instance" });
synthesis.valueObject("Phase", { from: "f32", description: "oscillator/sample read phase", invariants: ["wraps to sample length or 0..1"] });
synthesis.valueObject("Cutoff", { from: "f32", description: "filter cutoff in Hz", invariants: ["> 0.0"] });
synthesis.valueObject("Resonance", { from: "NormalizedValue", description: "filter resonance" });

const soundGenerator = synthesis.port("SoundGenerator", {
  contract: {
    render: "(pitch: Pitch, mod: &ModBlock, out: &mut [StereoFrame]) -> ()",
    note_on: "(note: NoteNumber, velocity: Velocity) -> ()",
    note_off: "() -> ()",
    is_finished: "() -> bool",
  },
});

synthesis.aggregate("WavetableOscillator", {
  implements: soundGenerator,
  purpose: "band-limited wavetable / virtual-analog generator",
  state: { table: "Arc<[f32]>", phase: "Phase", unison: "u8", detune: "Cents" },
});
synthesis.aggregate("FmOperator", {
  implements: soundGenerator,
  purpose: "2-4 operator FM generator for the DX-style / metallic dungeon-synth timbres",
  state: { ratios: "[f32; 4]", indices: "[NormalizedValue; 4]", phases: "[Phase; 4]" },
});
synthesis.aggregate("SamplePlayer", {
  implements: soundGenerator,
  purpose: "pitched sample playback with loop and modulatable sample-start (the rompler core)",
  state: { sample: "Arc<SampleData>", phase: "Phase", startOffset: "NormalizedValue", interpolation: "string" },
  meta: { prompts: ["sample-start and loop points are valid modulation destinations", "interpolate via dasp sinc/linear"] },
});

const voice = synthesis.aggregate("Voice", {
  root: true,
  purpose: "the per-note consistency boundary; sums its generators through filter and amp envelope",
  state: {
    id: "VoiceId",
    noteId: "NoteId",
    note: "NoteNumber",
    currentPitch: "Pitch",      // tracks glide
    generators: "Vec<GeneratorInstance>",
    filterCutoff: "Cutoff",
    resonance: "Resonance",
    ampEnv: "EnvelopeSource",
    filterEnv: "EnvelopeSource",
    gain: "Gain",
    pan: "Pan",
    released: "bool",
  },
  invariants: [
    "a Voice is a pure function of (its source zone in PatchSnapshot, its NoteId, the ModBlock); same inputs => same output",
    "after note_off, the amp envelope enters release; when it completes, is_finished() is true",
    "is_finished() => the Voice emits VoiceFinished and must not write further audio",
    "the Voice never allocates and never frees its own pool slot",
  ],
  commands: [
    command("Start", { noteId: "NoteId", note: "NoteNumber", velocity: "Velocity", from: "PatchSnapshot" }),
    command("Release", {}),
    command("Kill", {}), // immediate, for steal
    command("SetPitchTarget", { pitch: "Pitch" }), // glide
    command("RenderBlock", { mod: "ModBlock", frames: "FrameCount" }),
  ],
  events: [
    event("VoiceStarted", { id: "VoiceId", noteId: "NoteId" }),
    event("VoiceEnteredRelease", { id: "VoiceId" }),
    event("VoiceFinished", { id: "VoiceId", noteId: "NoteId" }),
  ],
});

voice.entity("GeneratorInstance", {
  state: { generator: "SoundGenerator", level: "Gain", enabled: "bool" },
});

// =============================================================================
// RENDERING — voice mixdown, global FX bus, output; the per-block orchestrator
// =============================================================================

const rendering = app.context("Rendering", {
  purpose: "sum active voices, apply global insert/send effects, and write the output buffer",
  ubiquitousLanguage: {
    MixBus: "the master bus summing all voices",
    InsertEffect: "an in-line effect (e.g. drive, EQ)",
    SendEffect: "a parallel effect fed by per-voice/bus sends (reverb, chorus, delay)",
    EngineHost: "the public surface the shells drive (process, note event, set parameter)",
  },
});

rendering.valueObject("BusId", { from: "u8" });
rendering.valueObject("SendLevel", { from: "NormalizedValue" });
rendering.valueObject("OutputBuffer", { state: { left: "Vec<f32>", right: "Vec<f32>" }, description: "deinterleaved stereo output for one block" });

const effect = rendering.port("Effect", {
  contract: { process: "(io: &mut [StereoFrame]) -> ()", reset: "() -> ()" },
});
rendering.aggregate("ReverbEffect", { implements: effect, purpose: "algorithmic reverb (FluidSynth-lineage / fundsp)", state: { size: "NormalizedValue", damp: "NormalizedValue", mix: "NormalizedValue" } });
rendering.aggregate("ChorusEffect", { implements: effect, purpose: "SC/JV-style chorus", state: { rateHz: "f32", depth: "NormalizedValue", mix: "NormalizedValue" } });
rendering.aggregate("DelayEffect", { implements: effect, purpose: "tempo-syncable delay", state: { timeMs: "f32", feedback: "NormalizedValue", mix: "NormalizedValue" } });

const mixBus = rendering.aggregate("MixBus", {
  root: true,
  purpose: "the master mix consistency boundary: voice sum + FX chain + master gain",
  state: { masterGain: "Gain", inserts: "Vec<Effect>", sends: "Vec<(BusId, SendLevel, Effect)>", version: "SnapshotVersion" },
  invariants: [
    "output is finite (no NaN/Inf escapes the bus; non-finite samples are flushed to 0)",
    "effect chain is rebuilt only when PatchSnapshot/ParameterSnapshot version changes",
  ],
  commands: [
    command("SetMasterGain", { gain: "Gain" }),
    command("AddInsert", { at: "usize", effect: "Effect" }),
    command("RemoveInsert", { at: "usize" }),
    command("SetSend", { bus: "BusId", level: "SendLevel" }),
  ],
  events: [
    event("MasterGainChanged", { from: "Gain", to: "Gain" }),
    event("InsertAdded", { at: "usize" }),
    event("SendChanged", { bus: "BusId", from: "SendLevel", to: "SendLevel" }),
  ],
});

const engineHost = rendering.port("EngineHost", {
  contract: {
    process: "(events: &PerformanceBuffer, out: &mut OutputBuffer, frames: FrameCount) -> ()",
    push_snapshot: "(snapshot: EngineSnapshot) -> ()", // PatchSnapshot + ParameterSnapshot + SampleSnapshot
  },
});

rendering.applicationService("EngineProcessService", {
  purpose: "the per-block RT orchestrator: drain events -> allocate -> evaluate mod -> render voices -> mix -> output",
  uses: [voicePool, modMatrix, voice, mixBus],
  operations: [
    operation("process", { input: { events: "PerformanceBuffer", frames: "FrameCount" } }),
    operation("adoptSnapshot", { input: { snapshot: "EngineSnapshot" } }),
  ],
  meta: {
    notes: "This is process(). It consumes published snapshots at block boundaries only, never mid-block, " +
           "so a render is always against a single consistent (Patch, Parameter, Sample) version.",
    rules: ["must not allocate", "must not lock", "must complete in well under one block's wall-clock budget"],
  },
});

// =============================================================================
// PRESET — import/export ACL (SFZ, SF2, native) + bank persistence
// =============================================================================

const preset = app.context("Preset", {
  purpose: "translate external instrument formats (SFZ, SF2) and native files into Patch + SampleBank",
  ubiquitousLanguage: {
    Preset: "a saved Patch plus the sample references it needs",
    Bank: "an ordered collection of presets",
    Codec: "a bidirectional translator for one external format",
  },
  meta: { notes: "Anti-corruption layer: SFZ opcodes / SF2 generators are translated into the Patch model and never leak." },
});

preset.valueObject("PresetId", { from: "u32" });
preset.valueObject("BankId", { from: "u32" });

const presetCodec = preset.port("PresetCodec", {
  contract: {
    import: "(path: &Path) -> Result<(Patch, Vec<SampleId>), ImportError>",
    export: "(patch: &Patch, path: &Path) -> Result<(), ExportError>",
  },
});

const presetLibrary = preset.aggregate("PresetLibrary", {
  root: true,
  purpose: "manage banks and presets and coordinate import/export against Patch + SampleBank",
  state: { banks: "HashMap<BankId, Vec<PresetId>>", presets: "HashMap<PresetId, PatchId>" },
  invariants: [
    "importing a preset yields a valid Patch satisfying all Patch invariants, or fails atomically",
    "every imported preset's sample references resolve to loadable SampleIds",
  ],
  commands: [
    command("ImportSfz", { path: "String", into: "BankId" }),
    command("ImportSf2", { path: "String", into: "BankId" }),
    command("SaveNative", { presetId: "PresetId", path: "String" }),
    command("LoadNative", { path: "String", into: "BankId" }),
    command("DeletePreset", { presetId: "PresetId" }),
  ],
  events: [
    event("PresetImported", { presetId: "PresetId", patchId: "PatchId", format: "string" }),
    event("PresetSaved", { presetId: "PresetId", path: "String" }),
    event("ImportRejected", { path: "String", reason: "string" }),
  ],
});

preset.repository("PresetLibraryRepository", { of: presetLibrary });

preset.applicationService("PresetService", {
  purpose: "orchestrate format translation into Patch + SampleBank and back",
  uses: [presetLibrary, patchAgg, sampleBank],
  operations: [
    operation("importSfz", { input: { path: "String", bank: "BankId" } }),
    operation("importSf2", { input: { path: "String", bank: "BankId" } }),
    operation("save", { input: { presetId: "PresetId", path: "String" } }),
  ],
});

// =============================================================================
// ADAPTERS — infrastructure (input/output/codecs) and interface (the shells)
// =============================================================================

// input
app.adapter("MidiInput", { implements: inputSource });                 // midir / nih-plug NoteEvent stream
app.adapter("GamepadInput", { implements: inputSource });              // gilrs (tracker performance / live play)

// sample loading
app.adapter("SymphoniaLoader", { implements: sampleLoader });

// parameter backends
app.adapter("NihPlugParameterHost", { implements: parameterHost });
app.adapter("StandaloneParameterHost", { implements: parameterHost });

// preset codecs
app.adapter("SfzCodec", { implements: presetCodec });
app.adapter("Sf2Codec", { implements: presetCodec });
app.adapter("NativeCodec", { implements: presetCodec });

// the three front doors over the one core
app.adapter("NihPlugShell", { implements: engineHost, layer: "interface" });        // CLAP/VST3/standalone synth
app.adapter("EframeTrackerShell", { implements: engineHost, layer: "interface" });  // gamepad tracker GUI
app.adapter("CpalStandaloneShell", { implements: engineHost, layer: "interface" }); // headless / embedded host

// =============================================================================
// CONTEXT MAP
// =============================================================================

app.contextMap([
  // everything shares the Kernel primitives
  relationship("Performance", "Kernel", { kind: "shared-kernel" }),
  relationship("Patch", "Kernel", { kind: "shared-kernel" }),
  relationship("SampleLibrary", "Kernel", { kind: "shared-kernel" }),
  relationship("Parameter", "Kernel", { kind: "shared-kernel" }),
  relationship("Modulation", "Kernel", { kind: "shared-kernel" }),
  relationship("VoiceAllocation", "Kernel", { kind: "shared-kernel" }),
  relationship("Synthesis", "Kernel", { kind: "shared-kernel" }),
  relationship("Rendering", "Kernel", { kind: "shared-kernel" }),
  relationship("Preset", "Kernel", { kind: "shared-kernel" }),

  // input -> allocation
  relationship("VoiceAllocation", "Performance", { kind: "customer-supplier", direction: "downstream" }),

  // the RT boundary: Patch is a published, versioned language consumed by the RT contexts
  relationship("Synthesis", "Patch", { kind: "published-language" }),
  relationship("Modulation", "Patch", { kind: "published-language" }),
  relationship("VoiceAllocation", "Patch", { kind: "published-language" }),
  relationship("Rendering", "Patch", { kind: "published-language" }),

  // samples flow into the synthesis voices
  relationship("Synthesis", "SampleLibrary", { kind: "customer-supplier", direction: "downstream" }),

  // parameters feed the RT contexts (smoothed snapshot)
  relationship("Modulation", "Parameter", { kind: "customer-supplier", direction: "downstream" }),
  relationship("Synthesis", "Parameter", { kind: "customer-supplier", direction: "downstream" }),
  relationship("Rendering", "Parameter", { kind: "customer-supplier", direction: "downstream" }),

  // modulation feeds the voices
  relationship("Synthesis", "Modulation", { kind: "customer-supplier", direction: "downstream" }),

  // the render orchestrator consumes the other RT contexts
  relationship("Rendering", "VoiceAllocation", { kind: "customer-supplier", direction: "downstream" }),
  relationship("Rendering", "Synthesis", { kind: "customer-supplier", direction: "downstream" }),
  relationship("Rendering", "Modulation", { kind: "customer-supplier", direction: "downstream" }),

  // preset writes Patch + SampleBank, and translates external formats (ACL)
  relationship("Preset", "Patch", { kind: "customer-supplier", direction: "upstream" }),
  relationship("Preset", "SampleLibrary", { kind: "customer-supplier", direction: "upstream" }),
]);

// =============================================================================
// GLOBAL INVARIANTS
// =============================================================================

app.invariants([
  invariant("the audio thread never allocates, locks, or blocks", {
    meta: { rationale: "any allocation/lock/syscall risks priority inversion and buffer underruns (audible dropouts)" },
  }),
  invariant("non-RT contexts reach RT contexts only via immutable, versioned snapshots over a lock-free channel", {
    meta: {
      rationale:
        "the RT boundary is the system's anti-corruption seam; sharing mutable state would require locks. " +
        "Patch/Parameter/Sample state crosses as Arc<...Snapshot> via rtrb.",
    },
  }),
  invariant("sample and DSP-graph memory is allocated and freed only off the audio thread", {
    meta: { rationale: "free() is not real-time-safe; the RT side relinquishes ownership for deferred deallocation (basedrop)" },
  }),
  invariant("the realized runtime graph is a deterministic projection of (PatchSnapshot, ParameterSnapshot, ordered PerformanceEvents)", {
    meta: {
      rationale:
        "monotonic-regeneration analog: identical inputs yield identical audio; derived runtime state is rebuilt " +
        "ONLY when an upstream snapshot version changes, and rebuilding is pure and idempotent",
    },
  }),
  invariant("snapshots are adopted only at block boundaries, never mid-block", {
    meta: { rationale: "guarantees every render() runs against one consistent (Patch, Parameter, Sample) version" },
  }),
  invariant("the domain layer compiles under no_std", {
    meta: { rationale: "the same core must target embedded hosts (Daisy/Pi) where std is unavailable or undesirable" },
  }),
  invariant("only Kernel-typed NormalizedValue/Bipolar values reach the DSP; raw host/MIDI units never enter the domain", {
    meta: { rationale: "keeps the ubiquitous language clean and confines unit translation to the Performance/Parameter ACLs" },
  }),
  invariant("voice lifecycle is owned solely by VoiceAllocation; Synthesis signals completion but never frees its slot", {
    meta: { rationale: "single owner of the polyphony consistency boundary prevents double-free and slot races" },
  }),
  invariant("contexts do not reach into each other's internals except via declared relationships", {
    meta: { rationale: "the context map is the only legal integration surface" },
  }),
]);
