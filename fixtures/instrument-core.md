# instrument-core

The shared Rust synth engine behind both the standalone synth and the gamepad tracker. This is the prose companion to `instrument-core.ts` (the crest-spec), the way `tracker.md` sits beside `crest-spec.md`. The crest-spec is the authoritative declaration; this document explains *why it is shaped that way*, spells out the cross-boundary protocol that the spec can only gesture at, and gives a build order.

It is a polyphonic, MPE-capable **synth** — true synthesis voices (wavetable/virtual-analog, FM) *and* sample-playback voices in one engine. "Rompler" undersells it: sample playback is one of three `SoundGenerator` implementations, not the whole machine.

One core, three front doors: a `nih-plug` plugin (CLAP/VST3/standalone), an `eframe` tracker GUI, and a headless `cpal` host. All three drive the same `EngineHost` port over the same engine.

---

## 1. The real-time boundary is the architecture

Every other decision falls out of one fact: the audio thread is hard real-time and may not allocate, lock, or block. Miss the buffer deadline and you get an audible dropout. So the system has two halves with different rules, and the seam between them is the most important boundary in the whole design — more important than any individual context.

**Non-real-time half** — editable, allocating, persistable, runs on normal threads:
`Patch` (the instrument as data), `SampleLibrary` (decode + RAM pool), `Parameter` (canonical store + automation), `Preset` (SFZ/SF2/native I/O).

**Real-time half** — runs inside `process()`, never allocates, never locks:
`VoiceAllocation` (polyphony), `Synthesis` (the voices), `Modulation` (the matrix), `Rendering` (mix + FX + output).

`Kernel` is the shared-kernel of value primitives underneath both.

This is why the contexts split where they do. `Patch` and `Synthesis` look like they could be one context — both are "about the instrument" — but they live on opposite sides of the deadline. `Patch` is heap-backed, editable, and serde-friendly; `Synthesis.Voice` is a flat, allocation-free thing that has to render in microseconds. They are the **same instrument in two representations**, and the boundary between them is a translation, not a shared object.

---

## 2. Spec vs. realization (the monotonic-regeneration analog)

The central DDD move mirrors crest-spec's core correctness property. In crest-spec, a resource is regenerated only when its declaration or an upstream dependency changes, and regeneration is deterministic. The audio analog:

> The realized runtime graph (`VoicePool`, `Voice`s, `ModulationMatrix`, `MixBus`) is a **deterministic projection** of `(PatchSnapshot, ParameterSnapshot, ordered PerformanceEvents)`. It is rebuilt only when an upstream snapshot **version** changes, and the rebuild is pure and idempotent.

Concretely:

- `Patch` is the declaration. Editing it bumps a `SnapshotVersion`.
- Freezing a `Patch` produces an immutable `PatchSnapshot` (published-language).
- The RT side adopts the new snapshot at a block boundary and rebuilds derived state (voice templates, the realized matrix, the FX chain) — but only the parts whose version actually moved.
- A `Voice` is a pure function of `(its zone in the snapshot, its NoteId, the ModBlock)`. Same inputs ⇒ identical samples. This is what makes the engine testable offline and what makes "render this song deterministically" possible.

The payoff is the same as in crest-spec: you don't rebuild what didn't change, and identical inputs are guaranteed to produce identical output.

---

## 3. The cross-boundary protocol

The crest-spec marks `Patch → Synthesis/Modulation/VoiceAllocation/Rendering` as **published-language** and the global invariants forbid shared mutable state across the deadline. Here is the actual mechanism those declarations stand for.

### What crosses, and how

| Payload | Direction | Mechanism | Cadence |
|---|---|---|---|
| `PerformanceEvent`s (note/expression/CC) | in → RT | `PerformanceBuffer` (pre-allocated, time-stamped) | every block |
| `ParameterSnapshot` (smoothed values) | non-RT → RT | `triple_buffer` (latest-wins, lock-free) | every block |
| `PatchSnapshot` / `SampleSnapshot` (versioned) | non-RT → RT | `rtrb` SPSC ring (discrete messages) | on edit/load only |
| `Arc<SampleData>` (immutable PCM) | non-RT → RT | ownership handoff inside the snapshot | on load |
| dropped graph/sample memory | RT → non-RT | `basedrop` collector | when a voice/graph is retired |

Three different tools because the payloads have different shapes. Parameters change continuously and you only want the newest value → `triple_buffer`. Snapshots are discrete, infrequent, and must not be lost → a real `rtrb` queue. Retired memory must leave the audio thread without `free()` → `basedrop`'s deferred deallocation, drained later by `collectGarbage`.

### The rules that protect it (global invariants in the spec)

- Snapshots are **adopted only at block boundaries**, never mid-block, so a render always runs against one consistent `(Patch, Parameter, Sample)` version.
- The RT side reads snapshots; it never touches the canonical `ParameterSet`, `Patch`, or `SampleBank`.
- A `SampleId` referenced by the active snapshot is always resident; eviction waits until no pending snapshot references it.
- The RT side never frees anything — it hands ownership back via `basedrop`.

### The two faces of Patch

Worth making explicit because it bites people: the **authoring** `Patch` aggregate uses `Vec`/`HashMap` and is ergonomic to edit. The **`PatchSnapshot`** is a flat, no_std-readable form — index-addressable arrays, no hashing, no indirection the RT side can't afford. `PatchEditingService::publishSnapshot` is the freeze step that translates one into the other. They are deliberately different types.

---

## 4. The per-block walk (`EngineProcessService::process`)

`EngineProcessService` is `process()` — the one application service that orchestrates the RT contexts. One block looks like:

```
                  ┌── adopt pending snapshots (block boundary only) ──┐
                  │   PatchSnapshot? ParameterSnapshot? SampleSnapshot? │
                  └───────────────────────┬───────────────────────────┘
                                          │ (rebuild only what changed)
   PerformanceBuffer ──► VoiceAllocation ─┤
   (sorted by offset)    • allocate/steal │
                         • NoteId→slot     ▼
                                       Modulation ──► evaluate global sources once
                                          │            evaluate per-voice sources per voice
                                          ▼
                                       Synthesis ──► for each active Voice:
                                          │            generators → filter → amp env
                                          │            (enum dispatch, no alloc)
                                          ▼
                                       Rendering ──► sum voices → inserts → sends (reverb/chorus/delay)
                                          │            → master gain → flush non-finite
                                          ▼
                                      OutputBuffer (deinterleaved stereo)
```

Events are drained in non-decreasing `SampleOffset` order, so note-ons, steals, and expression changes land at sample-accurate positions within the block. A `Voice` that finishes its release emits `VoiceFinished`; `VoiceAllocation` (the sole owner of voice lifecycle) reclaims the slot. Synthesis never frees its own slot — that invariant prevents the double-free/slot-race class of bugs entirely.

---

## 5. The contexts in brief

**Kernel.** The value primitives both halves share: `NormalizedValue` (0..1, the control unit), `Bipolar` (-1..1, the modulation unit), `NoteId` (identifies a note-*instance*, not a pitch — this is what makes MPE/per-note expression addressable), `SnapshotVersion`, `StereoFrame`, the rest. Everything else is shared-kernel with this.

**Performance.** The input anti-corruption layer. Raw MIDI bytes, MPE channel rotation, and `gilrs` gamepad events enter here and never leave — only normalized, Kernel-typed `PerformanceEvent`s do. `MpeChannelRouter` resolves which sounding `NoteId` a member-channel expression message belongs to. This is where the "raw units never enter the domain" invariant is enforced.

**Patch.** The editable instrument: `Zone`s (key/velocity regions → generators), `GeneratorSpec`, DAHDSR `EnvelopeSpec`, `LfoSpec`, `ModRoutingSpec`, `MacroSpec`. Non-RT, serde-backed. The source of truth that everything RT projects from.

**SampleLibrary.** Off-thread decode (symphonia/hound) into immutable `Arc<SampleData>`, a ref-counted resident pool, and the `basedrop` reclaim path. Streaming is modeled but optional (see open decisions).

**Parameter.** Canonical values, ranges, smoothing, automation, and the host/GUI bridge. `ParameterHost` is a port with two adapters: nih-plug's param system in the plugin, a plain store standalone. Produces the smoothed `ParameterSnapshot`.

**Modulation.** Your global-source-pool mod matrix, realized. `ModSource` is a polymorphic port — `LfoSource`, `EnvelopeSource`, `RandomSource`, `MacroSource`, `ExpressionSource`, `SequencerSource`, `MidiCcSource`. Global sources evaluate once per block and are shared; per-voice sources (envelopes) evaluate per voice. `SequencerSource` is the Table-style automation lane and is the obvious shared-kernel candidate with the tracker's `Table` aggregate.

**VoiceAllocation.** The polyphony consistency boundary: a fixed `VoicePool`, `NoteId → slot` mapping, deterministic stealing under `StealPolicy`, and play-mode enforcement (poly / mono / legato, the last two capping at one voice with glide vs. retrigger semantics).

**Synthesis.** The hot loop. `Voice` sums its `GeneratorInstance`s through filter and amp envelope. `SoundGenerator` is the polymorphic port: `WavetableOscillator`, `FmOperator`, `SamplePlayer` — this trio is precisely "synth, not rompler." `SamplePlayer` exposes sample-start and loop points as modulation destinations, which is where the modern sound-design character lives.

**Rendering.** Voice mixdown, the global FX bus (`Effect` port: `ReverbEffect`, `ChorusEffect`, `DelayEffect` — FluidSynth-lineage chorus for the SC/JV character), master gain, NaN/Inf flushing, and the `EngineHost` port the shells implement.

**Preset.** The format anti-corruption layer. `PresetCodec` translates SFZ opcodes and SF2 generators into the `Patch` model + sample refs — and those external vocabularies never leak past this context. Native save/load is serde.

---

## 6. Polymorphism via ports → enum dispatch in practice

The spec models three polymorphic families as ports (`SoundGenerator`, `ModSource`, `Effect`), the same pattern you approved for the tracker's Phrase family. One implementation note the spec deliberately leaves to code: in the RT inner loop these are **enum dispatch**, not `dyn` trait objects. The port is the conceptual contract; the realization is an `enum Generator { Wavetable(..), Fm(..), Sample(..) }` with a `match` in `render`. Same domain meaning, no vtable in the per-sample path. The `avoid` list in the spec's meta says exactly this.

---

## 7. Relationship to the tracker

`tracker.md` already declares `Synthesis`, `Sampling`, and `AudioEffects` as contexts. `instrument-core` is the **realization of those three**, expanded into the full real-time engine. The tracker's eleven contexts stay as they are; three of them now have a concrete engine behind them, consumed as a dependency.

Two integration seams to decide deliberately (flagged in §9):

- The tracker's `Table` aggregate and instrument-core's `SequencerSource` are the same idea (a step/automation lane). Candidate for a shared-kernel relationship rather than two implementations.
- The tracker's `Performance`/`MIDI`/`Controls` contexts produce note and controller intent; instrument-core's `Performance` context is an ACL for the same. When the tracker drives the engine in-process, the tracker's normalized events should feed `EngineHost` directly, bypassing the MIDI round-trip the plugin shell needs.

When the standalone synth drives the engine, input arrives as MIDI/MPE. When the tracker drives it, input arrives as in-process `PerformanceEvent`s. Same engine, two suppliers — which is exactly what the `EngineHost` port is for.

---

## 8. Cargo workspace layout

The crate split enforces the layer rules and the no_std boundary mechanically: the RT core simply cannot depend on the std host crates because they aren't in its dependency graph.

```
instrument-core/                 # cargo workspace
├── Cargo.toml                   # [workspace]
├── crates/
│   ├── ic-kernel/               # no_std · Kernel value objects (shared-kernel)
│   ├── ic-snapshot/             # no_std-readable · the published-language types:
│   │                            #   PatchSnapshot, ParameterSnapshot, SampleSnapshot,
│   │                            #   EngineSnapshot, PerformanceEvent/PerformanceBuffer
│   ├── ic-rt/                   # no_std · the whole real-time half:
│   │                            #   Synthesis, Modulation, VoiceAllocation, Rendering,
│   │                            #   + EngineProcessService (process()). Reads snapshots. No alloc.
│   ├── ic-patch/                # std · Patch authoring aggregate + freeze → PatchSnapshot
│   ├── ic-assets/               # std · SampleLibrary: symphonia decode, SampleBank, Arc handoff
│   ├── ic-params/               # std · ParameterSet, smoothing, automation; backends behind a trait
│   ├── ic-preset/               # std · PresetCodec (SFZ/SF2/native) → Patch + sample refs
│   └── ic-bridge/               # std · the boundary: rtrb rings, triple_buffer, basedrop collector,
│                                #   snapshot publication. Wires the non-RT host to ic-rt.
├── shells/
│   ├── ic-plugin/               # interface · nih-plug (CLAP/VST3/standalone)
│   └── ic-standalone/           # interface · cpal + midir headless host
└── (the eframe tracker shell lives in the quest-tracker repo and
     depends on ic-rt + ic-bridge as a path/git dependency)
```

Dependency direction (enforces the spec's layer rules):

```
ic-kernel  ◄── ic-snapshot ◄── ic-rt          (no_std core; the embedded-portable set)
   ▲              ▲              ▲
   └── ic-patch ──┤              │
   └── ic-assets ─┤              │
   └── ic-params ─┤              │
        ic-preset ─┘             │
              ic-bridge ─────────┘ (std host wiring)
                   ▲
        ic-plugin / ic-standalone / quest-tracker  (interface)
```

The embedded target (Daisy/Pi) takes `ic-kernel + ic-snapshot + ic-rt` plus a minimal embedded bridge, and drops every `std` crate — replacing `ic-assets` with an SD-card loader and `ic-params` with a hardware-control reader. That portability is the entire reason for the no_std discipline, and the workspace makes it a subtraction rather than a rewrite.

---

## 9. v1 build order

Ordered to hear sound from the OXI One as early as possible and to make the hard boundary real before piling features on it. This is the spiral discussed earlier — a throwaway sine voice first, then progressively swap in the real domain.

1. **Workspace skeleton** + `ic-kernel` value objects + `ic-snapshot` stubs.
2. **`ic-plugin` standalone that makes noise.** nih-plug standalone → JACK MIDI → one sine `Voice` → audio out. Goal: OXI One plays a sine. Proves the loop, nothing else.
3. **Real voices.** `VoiceAllocation` (poly + basic oldest-note stealing) + `WavetableOscillator` + amp envelope. Now it's a polyphonic synth.
4. **Make the boundary real.** `ic-bridge`: `rtrb` snapshot ring + `triple_buffer` params + `basedrop` collector, even with trivial snapshots. Everything after this respects the deadline.
5. **Parameters.** `Parameter` context + a few mapped params (cutoff, resonance, master gain) on the One's CC lanes. Real-time sound design via CC.
6. **Modulation.** One LFO + per-voice envelope + the matrix, wired to pitch and cutoff. The mod-matrix design, minimal but real.
7. **Samples.** `SampleLibrary` + `SamplePlayer` (symphonia decode, `Arc` handoff). Now it plays your SF2/sample library — the rompler half lights up.
8. **Global FX.** `ReverbEffect` + `ChorusEffect` in `Rendering`. The lush dungeon-synth tail.
9. **Editable patches.** `Patch` authoring + freeze → snapshot + `PatchEditingService`. Patches become data instead of hardcoded.
10. **Preset import.** `PresetCodec` — SFZ first (declarative, easy), then SF2. Pull in the existing library.
11. **MPE depth** (if pursuing): per-voice `ExpressionSource` scoping, member-channel routing.
12. **Tracker integration.** quest-tracker's eframe shell consumes `ic-rt` + `ic-bridge`; route the tracker's normalized events straight into `EngineHost`.

You're hearing polyphonic synth notes by step 3, the deadline-safe boundary is real by step 4, you're doing live sound design by step 6, and your actual sample library is playing by step 7.

---

## 10. Open decisions (deferred, not decided)

- **`SequencerSource` vs. tracker `Table`** — shared-kernel or two implementations. Decide against `tracker.md`.
- **MPE depth** — how far per-note expression goes; affects `ExpressionSource` scoping and the channel→voice map.
- **Sample streaming vs. all-RAM** — fine on the Pi, forced on the Daisy by SDRAM limits. The `SampleLoader::decodeStreaming` contract exists but the RT-side streaming voice is unbuilt.
- **Granular playback** — the big sound-design differentiator for static dungeon-synth samples. Slots into `SamplePlayer` as an alternate read mode; not yet modeled as its own generator.
- **Effects: insert vs. send topology** — the spec allows both; v1 may ship sends only (reverb/chorus) and defer per-voice inserts.
