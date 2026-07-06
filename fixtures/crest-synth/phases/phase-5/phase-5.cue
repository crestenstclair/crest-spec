package crestsynth

// Phase 5: Modulation
// Envelopes, LFOs, routing matrix, per-note expression (MPE-ready).

project: contexts: Modulation: purpose: "modulation routing: sources (envelopes, LFOs, expression) mapped to destinations via a matrix"
project: contexts: Modulation: ubiquitousLanguage: {
	ModSource:          "a signal that drives modulation: envelope, LFO, per-note expression, macro"
	ModDestination:     "a parameter target: pitch, filter cutoff, gain, pan, etc."
	ModRouting:         "a single source-to-destination connection with a depth control"
	ModMatrix:          "the full set of active routings for a patch"
	PerNoteExpression:  "X (pitch bend), Y (timbre/CC74), Z (pressure) — per-voice mod sources for MPE"
}

project: contexts: Modulation: valueObjects: PerNoteExpression: {
	state:       {bendX: "f64", timbreY: "f64", pressureZ: "f64"}
	description: "per-note expression triple: X=pitch bend, Y=timbre, Z=pressure. Per-voice, not per-patch."
	invariants: ["all values normalized 0.0-1.0 (bend is bipolar, stored with 0.5 center)"]
}
project: contexts: Modulation: valueObjects: ModSourceType:      {from: "enum", description: "modulation source types: Envelope, LFO, Random, Macro, Velocity, KeyTrack, PerNoteBendX, PerNoteTimbreY, PerNotePressureZ"}
project: contexts: Modulation: valueObjects: ModDestinationType: {from: "enum", description: "modulation target parameter types"}
project: contexts: Modulation: valueObjects: LfoConfig: {
	state:       {waveform: "LfoWaveform", rate: "f64", depth: "f64", syncToTempo: "bool", phase: "f64"}
	description: "LFO parameters"
	invariants: ["rate must be positive", "depth must be 0.0-1.0"]
}
project: contexts: Modulation: valueObjects: ModEnvelopeConfig: {
	state:       {attack: "f64", decay: "f64", sustain: "f64", release: "f64"}
	description: "modulation envelope (same ADSR shape as amp, routed to arbitrary destinations)"
	invariants: ["attack, decay, release must be non-negative", "sustain must be 0.0-1.0"]
}

project: contexts: Modulation: aggregates: ModMatrix: {
	root:    true
	purpose: "per-patch modulation routing: maps sources to destinations with adjustable depth"
	state:   {patchId: "PatchId", routings: "Vec<ModRouting>", lfoConfigs: "Vec<LfoConfig>", modEnvelopes: "Vec<ModEnvelopeConfig>"}
	commands: [
		{name: "AddRouting", payload: {source: "ModSourceType", destination: "ModDestinationType", depth: "f64"}},
		{name: "RemoveRouting", payload: {routingIndex: "u8"}},
		{name: "UpdateRoutingDepth", payload: {routingIndex: "u8", depth: "f64"}},
		{name: "ConfigureLfo", payload: {lfoIndex: "u8", config: "LfoConfig"}},
		{name: "ConfigureModEnvelope", payload: {envIndex: "u8", config: "ModEnvelopeConfig"}},
	]
	events: [
		{name: "RoutingAdded", payload: {source: "ModSourceType", destination: "ModDestinationType", depth: "f64"}},
		{name: "RoutingRemoved", payload: {routingIndex: "u8"}},
		{name: "RoutingDepthChanged", payload: {routingIndex: "u8", depth: "f64"}},
		{name: "LfoConfigured", payload: {lfoIndex: "u8"}},
		{name: "ModEnvelopeConfigured", payload: {envIndex: "u8"}},
	]
	invariants: ["depth is bipolar: -1.0 to 1.0", "per-note expression sources are per-voice, not per-patch", "LFOs and macros are per-patch (shared across all voices)"]
	entities: ModRouting: {state: {source: "ModSourceType", destination: "ModDestinationType", depth: "f64"}}
}

project: contexts: Modulation: domainServices: ModulationProcessor: {
	purpose: "evaluates all mod sources and applies routed modulation to destination parameters each audio block"
	uses: ["aggregate.Modulation.ModMatrix"]
}

// ── Modulation made audible ────────────────────────────────────────────
// mod_play is patch_play with the Modulation context active: an LFO vibrato
// and a filter sweep, routed through the ModMatrix and applied each block by
// the ModulationProcessor, so the modulation is something a human can hear.

project: assets: ModPlayMain: {
	kind:        "rust-bin-target"
	description: "src/bin/mod_play.rs: multi-patch MIDI player with the Modulation context active — audible LFO vibrato + filter sweep"
	uses: ["asset.MidiFileLoader", "aggregate.Patch.Patch", "aggregate.Patch.GlobalMixer", "domainService.Patch.ChannelDispatcher", "domainService.Patch.PatchMixer", "aggregate.Modulation.ModMatrix", "domainService.Modulation.ModulationProcessor"]
	prompts: [
		"File path: src/bin/mod_play.rs",
		"Start from the patch_play setup: 2-3 Patches with distinct engine settings, each subscribed to a different MIDI channel, fed by the ChannelDispatcher into per-patch voice pools, summed via PatchMixer / GlobalMixer.",
		"For each patch build a ModMatrix (aggregate.Modulation.ModMatrix). Configure at least one LfoConfig (ConfigureLfo) and add routings via AddRouting: (1) an LFO vibrato — ModSourceType::Lfo routed to the pitch ModDestinationType with a small depth; (2) a filter sweep — a ModSourceType (Lfo or an Envelope from a ModEnvelopeConfig) routed to the filter-cutoff ModDestinationType with a clearly audible depth.",
		"Each audio block, run the ModulationProcessor over each patch's ModMatrix to evaluate the mod sources and apply the routed modulation to the destination parameters (pitch / filter cutoff) before rendering that patch's voices.",
		"CLI: `mod_play [FILE.mid] [--out OUT.wav]`. With no FILE, use the built-in multi-channel demo tune (sustained/legato notes so the vibrato and sweep are clearly audible).",
		"Load FILE (when given) with the MidiFileLoader module; otherwise use the built-in timeline.",
		"Write 16-bit mono WAV (default mod-play.wav, or --out) with a pure-Rust WAV writer.",
		#"Print stats: events per patch and peak voices per patch. For the active modulation print a verbatim line per routing tagged with the token `mod routing:` — e.g. `mod routing: LFO vibrato -> pitch` and `mod routing: sweep -> filter cutoff` — so a validation can assert the ModMatrix routings were actually configured and applied. The `mod routing:` token must appear verbatim."#,
	]
	validations: [
		{kind: "compiles", command: ["make", "build"], description: "mod player builds"},
		{kind: "integration", command: ["make", "demo-mod"], description: "modulated demo renders to WAV with active routings", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "file_exists", path: "mod-play.wav"},
			{kind: "stdout_contains", pattern: "mod routing:"},
		]},
	]
}

// ── Invariants ─────────────────────────────────────────

project: invariants: modulationSafety: [
	{text: "per-note expression (X, Y, Z) reaches the voice directly, never just the patch", meta: rationale: "voices must not assume expression is patch-level — blocks MPE later"},
	{text: "MPE expression dimensions exist as named per-voice mod sources from day one", meta: rationale: "building MPE later means feeding data into sources that already exist"},
]
