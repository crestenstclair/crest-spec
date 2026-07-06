package crestsynth

// Phase 4: Multiple patches subscribed to channels
// Per-patch voice pools, channel dispatch, global mix.

// ── Kernel addition ────────────────────────────────────

project: contexts: Kernel: valueObjects: ChannelAddress: {
	state:       {group: "MidiGroup", channel: "MidiChannel"}
	description: "a (group, channel) pair — the 256-destination address space for MIDI 2.0"
}

// ── Patch context ──────────────────────────────────────

project: contexts: Patch: purpose: "patch management: each patch is a complete instrument subscribed to a MIDI channel"
project: contexts: Patch: ubiquitousLanguage: {
	Patch:               "a complete instrument: engine + parameters + voice pool + channel subscription"
	ChannelSubscription: "which (group, channel) address a patch listens to"
	VoicePool:           "per-patch pool of voices with its own polyphony limit and stealing policy"
	MpeZone:             "a span of channels treated as one expressive instrument"
}

project: contexts: Patch: valueObjects: PatchId:              {from: "u32", description: "unique identifier for a patch"}
project: contexts: Patch: valueObjects: MpeZone:              {state: {managerChannel: "MidiChannel", memberChannelStart: "MidiChannel", memberChannelEnd: "MidiChannel"}, description: "MPE zone configuration", invariants: ["memberChannelStart < memberChannelEnd", "manager channel must not overlap member channels"]}
project: contexts: Patch: valueObjects: ChannelSubscription:  {state: {address: "ChannelAddress", mpeZone: "Option<MpeZone>"}, description: "what a patch listens to"}
project: contexts: Patch: valueObjects: VoicePoolConfig:      {state: {maxVoices: "u8", stealingPolicy: "StealingPolicy"}, description: "per-patch voice pool sizing", invariants: ["maxVoices must be at least 1"]}

project: contexts: Patch: aggregates: Patch: {
	root:    true
	purpose: "a complete instrument: engine type, parameters, voice pool, channel subscription"
	state: {
		id: "PatchId", name: "string", engineType: "EngineType",
		oscillator: "OscillatorConfig", filter: "FilterConfig", ampEnvelope: "AmpEnvelopeConfig",
		subscription: "ChannelSubscription", voicePoolConfig: "VoicePoolConfig",
		gain: "Amplitude", pan: "f64", active: "bool",
	}
	commands: [
		{name: "CreatePatch", payload: {name: "string", engineType: "EngineType", subscription: "ChannelSubscription"}},
		{name: "UpdateSubscription", payload: {subscription: "ChannelSubscription"}},
		{name: "UpdateOscillator", payload: {config: "OscillatorConfig"}},
		{name: "UpdateFilter", payload: {config: "FilterConfig"}},
		{name: "UpdateEnvelope", payload: {config: "AmpEnvelopeConfig"}},
		{name: "SetGain", payload: {gain: "Amplitude"}},
		{name: "SetPan", payload: {pan: "f64"}},
		{name: "ActivatePatch", payload: {}},
		{name: "DeactivatePatch", payload: {}},
	]
	events: [
		{name: "PatchCreated", payload: {id: "PatchId", name: "string", engineType: "EngineType"}},
		{name: "SubscriptionChanged", payload: {id: "PatchId", subscription: "ChannelSubscription"}},
		{name: "PatchParametersUpdated", payload: {id: "PatchId"}},
		{name: "PatchActivated", payload: {id: "PatchId"}},
		{name: "PatchDeactivated", payload: {id: "PatchId"}},
	]
	invariants: ["each patch has its own independent voice pool", "pan must be -1.0 (left) to 1.0 (right)"]
}

project: contexts: Patch: aggregates: GlobalMixer: {
	root:    true
	purpose: "master mix bus: sums all patch outputs and applies master gain"
	state: {masterGain: "Amplitude"}
	commands: [{name: "SetMasterGain", payload: {gain: "Amplitude"}}]
	events:   [{name: "MasterGainChanged", payload: {gain: "Amplitude"}}]
}

project: contexts: Patch: domainServices: ChannelDispatcher: {purpose: "routes incoming MidiEvents to every subscribed patch", uses: ["aggregate.Patch.Patch"]}
project: contexts: Patch: domainServices: PatchMixer:        {purpose: "sums audio from all active patches, applying per-patch gain and pan", uses: ["aggregate.Patch.Patch"]}

project: contexts: Patch: repositories: PatchRepository: {
	of:       "aggregate.Patch.Patch"
	contract: {findById: "PatchId -> Option<Patch>", findByChannel: "ChannelAddress -> Vec<Patch>", save: "Patch -> ()", listAll: "() -> Vec<Patch>"}
}

// ── Multi-patch MIDI playback (the integration prover) ─────────────────
// patch_play is THE end-to-end integration proof: a multi-channel MIDI file
// fanned out by the ChannelDispatcher into per-patch voice pools and summed
// by the PatchMixer / GlobalMixer to one WAV.

project: assets: PatchPlayMain: {
	kind:        "rust-bin-target"
	description: "src/bin/patch_play.rs: multi-patch MIDI player — proves dispatcher → per-patch voice pools → global mix end to end"
	uses: ["asset.MidiFileLoader", "aggregate.Patch.Patch", "aggregate.Patch.GlobalMixer", "domainService.Patch.ChannelDispatcher", "domainService.Patch.PatchMixer", "domainService.Synth.VoiceAllocator"]
	prompts: [
		"File path: src/bin/patch_play.rs",
		"Configure 2-3 Patch aggregates with DISTINCT engine settings (different OscillatorConfig / FilterConfig / AmpEnvelopeConfig and gain/pan), each with its own VoicePoolConfig, and each subscribed (ChannelSubscription) to a DIFFERENT MIDI channel via its ChannelAddress.",
		"CLI: `patch_play [FILE.mid] [--out OUT.wav]`. With no FILE, build a BUILT-IN multi-channel demo tune in code: events spread across the channels the patches subscribe to (so every patch sounds), spanning a few bars.",
		"Load FILE (when given) with the MidiFileLoader module; otherwise use the built-in multi-channel timeline.",
		"Route EVERY event through the ChannelDispatcher to all subscribed patches; each patch drives its OWN VoiceAllocator / voice pool (independent polyphony + stealing), proving one patch cannot exhaust another's voices.",
		"Sum each patch's rendered audio through the PatchMixer (per-patch gain + pan), then the GlobalMixer (master gain), into one output buffer.",
		"Write 16-bit mono WAV (default patch-play.wav, or --out) with a pure-Rust WAV writer.",
		#"Print per-channel / per-patch statistics to stdout. For EACH patch print a line containing the verbatim token `Peak Voices` followed by that patch's peak simultaneous voice count (e.g. `Patch 1 \"Bass\": Peak Voices = 3`). Also print events delivered per patch and voice-steal counts per patch. The `Peak Voices` token must appear verbatim so a validation can assert the per-patch voice accounting ran."#,
		"Purpose: this binary proves the dispatcher → per-patch-pools → global-mix integration works end to end.",
	]
	validations: [
		{kind: "compiles", command: ["make", "build"], description: "patch player builds"},
		{kind: "integration", command: ["make", "demo-patches"], description: "multi-channel demo renders through all patches to WAV", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "file_exists", path: "patch-play.wav"},
			{kind: "stdout_contains", pattern: "Peak Voices"},
		]},
	]
}

// ── Invariants ─────────────────────────────────────────

project: invariants: patchIsolation: [
	{text: "each patch has an independent voice pool; one patch's polyphony cannot exhaust another's", meta: rationale: "a busy pad must not starve a bass patch of voices"},
	{text: "channel dispatch delivers events to all subscribed patches, not just the first match", meta: rationale: "two patches on the same channel layer automatically"},
]
