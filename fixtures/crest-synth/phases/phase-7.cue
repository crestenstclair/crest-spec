package crestsynth

// Phase 7: Effects
// Per-patch and global reverb/chorus/delay via fundsp.

project: contexts: Effects: purpose: "audio effects processing: per-patch and global reverb, chorus, delay via fundsp"
project: contexts: Effects: ubiquitousLanguage: {
	EffectChain: "an ordered list of effect slots applied to a patch's or the master mix's audio"
	EffectSlot:  "a single effect processor with its own type and parameters in a chain"
	DryWet:      "mix ratio between unprocessed (dry) and processed (wet) signal"
}

project: contexts: Effects: valueObjects: EffectChainId: {from: "u32", description: "unique identifier for an effect chain"}
project: contexts: Effects: valueObjects: ReverbConfig: {
	state:       {roomSize: "f64", damping: "f64", dryWet: "f64", preDelay: "f64"}
	description: "reverb parameters"
	invariants: ["roomSize, damping, dryWet must be 0.0-1.0", "preDelay must be non-negative"]
}
project: contexts: Effects: valueObjects: ChorusConfig: {
	state:       {rate: "f64", depth: "f64", dryWet: "f64", voices: "u8"}
	description: "chorus parameters"
	invariants: ["rate must be positive", "depth, dryWet must be 0.0-1.0", "voices must be at least 1"]
}
project: contexts: Effects: valueObjects: DelayConfig: {
	state:       {time: "f64", feedback: "f64", dryWet: "f64", syncToTempo: "bool"}
	description: "delay parameters"
	invariants: ["time must be positive", "feedback must be 0.0-1.0", "dryWet must be 0.0-1.0"]
}

project: contexts: Effects: aggregates: EffectChain: {
	root:    true
	purpose: "an ordered list of effect slots processed in series"
	state:   {id: "EffectChainId", slots: "Vec<EffectSlot>", bypass: "bool"}
	commands: [
		{name: "AddEffect", payload: {effectType: "EffectType", position: "u8"}},
		{name: "RemoveEffect", payload: {slotIndex: "u8"}},
		{name: "ReorderEffect", payload: {fromIndex: "u8", toIndex: "u8"}},
		{name: "UpdateEffectParams", payload: {slotIndex: "u8", params: "EffectParams"}},
		{name: "BypassChain", payload: {}},
		{name: "EnableChain", payload: {}},
	]
	events: [
		{name: "EffectAdded", payload: {effectType: "EffectType", position: "u8"}},
		{name: "EffectRemoved", payload: {slotIndex: "u8"}},
		{name: "EffectReordered", payload: {fromIndex: "u8", toIndex: "u8"}},
		{name: "EffectParamsUpdated", payload: {slotIndex: "u8"}},
		{name: "ChainBypassed", payload: {id: "EffectChainId"}},
		{name: "ChainEnabled", payload: {id: "EffectChainId"}},
	]
	invariants: ["effects process in slot order: slot 0 first, slot N last", "bypassed chain passes audio through unmodified"]
	entities: EffectSlot: {state: {effectType: "EffectType", params: "EffectParams", bypass: "bool"}}
}

project: contexts: Effects: ports: EffectProcessor: {
	contract: {process: "([AudioFrame], EffectParams) -> [AudioFrame]", reset: "() -> ()"}
	meta: notes: "implemented via fundsp nodes; enum dispatch for supported effect types"
}

project: contexts: Effects: repositories: EffectChainRepository: {
	of:       "aggregate.Effects.EffectChain"
	contract: {findById: "EffectChainId -> Option<EffectChain>", save: "EffectChain -> ()"}
}

// ── Invariants ─────────────────────────────────────────

project: invariants: effectsRouting: [
	{text: "effect chains process in slot order; signal flow is patch voices -> patch FX -> mix -> master FX -> output", meta: rationale: "deterministic signal routing; per-patch FX before the mix bus, master FX after"},
]
