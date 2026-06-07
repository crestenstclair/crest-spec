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

// ── Invariants ─────────────────────────────────────────

project: invariants: patchIsolation: [
	{text: "each patch has an independent voice pool; one patch's polyphony cannot exhaust another's", meta: rationale: "a busy pad must not starve a bass patch of voices"},
	{text: "channel dispatch delivers events to all subscribed patches, not just the first match", meta: rationale: "two patches on the same channel layer automatically"},
]
