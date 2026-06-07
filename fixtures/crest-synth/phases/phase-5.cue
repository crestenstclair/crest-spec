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

// ── Invariants ─────────────────────────────────────────

project: invariants: modulationSafety: [
	{text: "per-note expression (X, Y, Z) reaches the voice directly, never just the patch", meta: rationale: "voices must not assume expression is patch-level — blocks MPE later"},
	{text: "MPE expression dimensions exist as named per-voice mod sources from day one", meta: rationale: "building MPE later means feeding data into sources that already exist"},
]
