package crestsynth

// Effects — audio processors. Each effect transforms an input buffer to an
// output buffer with internal state, behind one type-erased port.

project: contexts: Effects: purpose: "audio effect processors (reverb, delay, chorus, EQ, compressor, limiter) and ordered, bypassable effect chains"

project: contexts: Effects: valueObjects: {
	ReverbParams: {
		state: {roomSize: "f64", damping: "f64", wetDry: "f64", preDelayMs: "f64", width: "f64"}
		description: "algorithmic reverb settings"
		invariants: ["wetDry must be 0.0-1.0"]
	}
	DelayParams: {
		state: {timeMs: "f64", feedback: "f64", wetDry: "f64", pingPong: "bool", tempoSync: "bool"}
		description: "delay line settings"
		invariants: ["feedback must be 0.0-1.0 (unity or less, or the line self-oscillates unboundedly)", "wetDry must be 0.0-1.0"]
	}
	ChorusParams: {
		state: {rateHz: "f64", depth: "f64", wet: "f64", voiceCount: "u8"}
		description: "chorus/flanger settings"
		invariants: ["voiceCount must be positive"]
	}
	EqBandType: {description: "EQ band response: low-pass, high-pass, band-pass, notch, low shelf, high shelf, or peak"}
	EqBand: {
		state: {bandType: "EqBandType", frequency: "Frequency", gainDb: "Decibel", q: "f64"}
		description: "one parametric EQ band"
		invariants: ["q must be positive"]
	}
	EffectSlot: {
		state: {bypassed: "bool"}
		description: "one position in an effect chain holding a processor and its bypass flag"
	}
	CompressorParams: {
		state: {thresholdDb: "Decibel", ratio: "f64", attackMs: "f64", releaseMs: "f64", makeupDb: "Decibel", kneeDb: "f64"}
		description: "dynamics compressor settings"
		invariants: ["ratio must be >= 1.0"]
	}
}

project: contexts: Effects: ports: {
	EffectProcessor: {
		contract: {
			process: "(input: list<AudioFrame>) -> list<AudioFrame>"
			reset:   "() -> ()"
			latency: "() -> u32"
		}
	}
}

project: contexts: Effects: aggregates: EffectChain: {
	root:    true
	purpose: "an ordered sequence of effect slots, each independently bypassable"
	state: {slots: "list<EffectSlot>", maxSlots: "u8"}
	commands: {
		InsertSlot: {index: "u32"}
		RemoveSlot: {index: "u32"}
		ReorderSlot: {from: "u32", to: "u32"}
		SetBypass: {index: "u32", bypassed: "bool"}
	}
	events: {
		SlotInserted: {index: "u32"}
		SlotRemoved: {index: "u32"}
		SlotsReordered: {from: "u32", to: "u32"}
	}
	invariants: [
		"signal flows through slots strictly top-to-bottom in slot order",
		"a bypassed slot passes its input through unchanged",
		"the chain never exceeds maxSlots slots",
	]
	contributesTo: [{capability: "capability.mix_to_stereo", contribution: "owns the ordered, bypassable insert processing used by strips, aux buses, and the master"}]
}

project: contexts: Effects: domainServices: {
	ChainRenderer: {
		purpose: "processes a buffer through every non-bypassed slot of a chain in order"
		uses: ["aggregate.Effects.EffectChain", "port.Effects.EffectProcessor"]
		contributesTo: [{capability: "capability.mix_to_stereo", contribution: "executes effect slots in canonical signal-flow order"}]
	}
}
