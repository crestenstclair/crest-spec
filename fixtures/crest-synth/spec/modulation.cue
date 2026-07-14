package crestsynth

// Modulation — mod matrix, LFOs, and expression routing from any source to
// any destination.

project: contexts: Modulation: purpose: "flexible modulation routing: LFOs, envelopes, velocity/key tracking, MIDI and MPE sources, routed to synthesis and mixer destinations"

project: contexts: Modulation: valueObjects: {
	ModSource: {description: "a modulation source: LFO 1-4, amp/filter/pitch/mod envelope, velocity, key tracking, aftertouch, pitch bend, mod wheel, expression, MPE X/Y/Z, or any CC 0-127"}
	ModDestination: {description: "a modulation destination: oscillator pitch or pulse width, filter cutoff or resonance, amp level, pan, LFO rate or depth, an effect parameter (slot + param index), or a send level (bus)"}
	ModCurve: {description: "response curve for a route: linear, exponential, S-curve, or stepped"}
	ModRoute: {
		state: {source: "ModSource", destination: "ModDestination", amount: "f64", curve: "ModCurve", bipolar: "bool", via: "option<ModSource>"}
		description: "one modulation route; the optional via source scales the route's depth"
		invariants: ["amount must be -1.0 to 1.0"]
	}
	LfoShape: {description: "LFO waveform: sine, triangle, saw, square, sample-and-hold, or random"}
	LfoConfig: {
		state: {shape: "LfoShape", rateHz: "f64", depth: "f64", tempoSync: "bool", retrigger: "bool", startPhase: "f64"}
		description: "settings for one of the four LFOs"
		invariants: ["rateHz must be positive", "startPhase must be 0.0-1.0"]
	}
}

project: contexts: Modulation: aggregates: ModMatrix: {
	root:    true
	purpose: "the set of active modulation routes and LFO configurations for one patch"
	state: {routes: "list<ModRoute>", lfos: "list<LfoConfig>", maxRoutes: "u8"}
	commands: {
		AddRoute: {route: "ModRoute"}
		RemoveRoute: {index: "u32"}
		SetLfo: {index: "u8", config: "LfoConfig"}
	}
	events: {
		RouteAdded: {index: "u32"}
		RouteRemoved: {index: "u32"}
	}
	invariants: [
		"the matrix never exceeds maxRoutes routes",
		"there are exactly 4 LFOs",
	]
	contributesTo: [{capability: "capability.configure_complete_patch", contribution: "owns the modulation routes and LFO configuration contained by a playable patch"}]
}

project: contexts: Modulation: domainServices: {
	ModProcessor: {
		purpose: "evaluates every source, applies every route through its curve and via-depth, and produces per-sample parameter offsets"
		uses: ["aggregate.Modulation.ModMatrix"]
		contributesTo: [{capability: "capability.render_expressive_sound", contribution: "applies MIDI, MPE, envelope, and LFO expression to rendered sound"}]
	}
}
