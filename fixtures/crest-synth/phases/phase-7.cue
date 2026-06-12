package crestsynth

// Phase 7: Effects
// Per-patch and global reverb/chorus/delay via fundsp.

project: contexts: Effects: purpose: "audio effects processing: per-patch and global reverb, chorus, delay via fundsp"
project: contexts: Effects: ubiquitousLanguage: {
	EffectChain: "an ordered list of effect slots applied to a patch's or the master mix's audio"
	EffectSlot:  "a single effect processor with its own type and parameters in a chain"
	DryWet:      "mix ratio between unprocessed (dry) and processed (wet) signal"
}

project: contexts: Effects: valueObjects: EffectChainId: {from: "u32", description: "unique identifier for an effect chain", validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with EffectChainId"}]}
project: contexts: Effects: valueObjects: ReverbConfig: {
	state:       {roomSize: "f64", damping: "f64", dryWet: "f64", preDelay: "f64"}
	description: "reverb parameters"
	invariants: ["roomSize, damping, dryWet must be 0.0-1.0", "preDelay must be non-negative"]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with ReverbConfig"},
		{kind: "test", command: ["cargo", "test", "reverb_config"], description: "ReverbConfig invariant tests pass"},
	]
}
project: contexts: Effects: valueObjects: ChorusConfig: {
	state:       {rate: "f64", depth: "f64", dryWet: "f64", voices: "u8"}
	description: "chorus parameters"
	invariants: ["rate must be positive", "depth, dryWet must be 0.0-1.0", "voices must be at least 1"]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with ChorusConfig"},
		{kind: "test", command: ["cargo", "test", "chorus_config"], description: "ChorusConfig invariant tests pass"},
	]
}
project: contexts: Effects: valueObjects: DelayConfig: {
	state:       {time: "f64", feedback: "f64", dryWet: "f64", syncToTempo: "bool"}
	description: "delay parameters"
	invariants: ["time must be positive", "feedback must be 0.0-1.0", "dryWet must be 0.0-1.0"]
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with DelayConfig"},
		{kind: "test", command: ["cargo", "test", "delay_config"], description: "DelayConfig invariant tests pass"},
	]
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
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with EffectChain"},
		{kind: "test", command: ["cargo", "test", "effect_chain"], description: "EffectChain unit tests pass"},
	]
}

project: contexts: Effects: ports: EffectProcessor: {
	contract: {process: "([AudioFrame], EffectParams) -> [AudioFrame]", reset: "() -> ()"}
	meta: notes: "implemented via fundsp nodes; enum dispatch for supported effect types"
	validations: [{kind: "compiles", command: ["cargo", "build"], description: "crate builds with EffectProcessor port"}]
}

project: contexts: Effects: repositories: EffectChainRepository: {
	of:       "aggregate.Effects.EffectChain"
	contract: {findById: "EffectChainId -> Option<EffectChain>", save: "EffectChain -> ()"}
	validations: [
		{kind: "compiles", command: ["cargo", "build"], description: "crate builds with EffectChainRepository"},
		{kind: "test", command: ["cargo", "test", "effect_chain_repository"], description: "EffectChainRepository unit tests pass"},
	]
}

// ── Effects made audible (the phase-7 behavior prover) ─────────────────
// effects_demo proves the EffectChain end to end through the real render path:
// it renders the multi-patch demo, runs each patch's audio through a per-patch
// EffectChain and the master mix through a global EffectChain, and MECHANICALLY
// proves the two invariants — (1) slot order matters, (2) a bypassed chain
// passes audio through unmodified. fundsp's adapter arrives in phase 9, so the
// demo supplies a tiny in-crate EffectProcessor impl (the port exists now); the
// proof is structural (ordering + bypass), not DSP-quality.

project: assets: EffectsDemoMain: {
	kind:        "rust-bin-target"
	description: "src/bin/effects_demo.rs: renders the multi-patch demo through per-patch + global EffectChains, proving slot-order and bypass-passthrough to WAV"
	uses: ["asset.MidiFileLoader", "aggregate.Patch.Patch", "aggregate.Patch.GlobalMixer", "domainService.Patch.ChannelDispatcher", "domainService.Patch.PatchMixer", "aggregate.Effects.EffectChain", "port.Effects.EffectProcessor"]
	prompts: [
		"File path: src/bin/effects_demo.rs",
		"CLI: `effects_demo [FILE.mid] [--out OUT.wav]`. Default output path effects-demo.wav. With no FILE, use the built-in multi-channel demo tune (sustained notes so the effect is audible).",
		"Start from the patch_play setup: 2-3 Patches subscribed to different channels via the ChannelDispatcher into per-patch voice pools, summed via PatchMixer then GlobalMixer.",
		"Provide a tiny in-crate implementation of the EffectProcessor port (port.Effects.EffectProcessor) — a couple of simple effects are enough (e.g. a gain/trim and a single-tap feedback delay). fundsp is NOT a dependency at this phase; do not import it.",
		"Build a per-patch EffectChain for at least one patch with at least TWO EffectSlots, and a global (master) EffectChain on the mix bus. Process signal flow STRICTLY in order: patch voices -> per-patch EffectChain (slot 0 then slot 1 ...) -> PatchMixer -> GlobalMixer -> master EffectChain -> output. Render the whole passage to WAV.",
		#"MECHANICALLY prove slot order: process one short test block through the chain in its declared slot order AND through the reversed slot order, and assert in code that the two outputs DIFFER (panic with a clear message if they are identical). Print a verbatim line `slot order matters: true`."#,
		#"MECHANICALLY prove bypass passthrough: take a short test block, run it through a BYPASSED EffectChain, and assert in code the output is BIT-IDENTICAL to the dry input (panic if not). Print a verbatim line `bypass passthrough: true`."#,
		"Write 16-bit mono WAV (default effects-demo.wav, or --out) with a pure-Rust WAV writer.",
		"Print per-patch/per-chain stats. The `slot order matters: true` and `bypass passthrough: true` tokens MUST appear verbatim so a validation can assert both EffectChain invariants held.",
		"Exit 0 on success (the two in-code assertions must pass for a normal run).",
	]
	validations: [
		{kind: "compiles", command: ["make", "build"], description: "effects demo builds"},
		{kind: "integration", command: ["make", "demo-effects"], description: "multi-patch demo renders through effect chains; slot-order and bypass invariants hold", assertions: [
			{kind: "exit_code", expected: 0},
			{kind: "file_exists", path: "effects-demo.wav"},
			{kind: "stdout_contains", pattern: "slot order matters: true"},
			{kind: "stdout_contains", pattern: "bypass passthrough: true"},
		]},
	]
}

// ── Invariants ─────────────────────────────────────────

project: invariants: effectsRouting: [
	{text: "effect chains process in slot order; signal flow is patch voices -> patch FX -> mix -> master FX -> output", meta: rationale: "deterministic signal routing; per-patch FX before the mix bus, master FX after"},
]
