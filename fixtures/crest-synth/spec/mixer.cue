package crestsynth

// Mixer — channel strips, buses, and sends. Standard console architecture:
// strip (input gain → inserts → volume/pan → send taps) → bus → master.

project: contexts: Mixer: purpose: "mixing console: per-patch channel strips with insert FX and sends, aux buses with returns, and a master bus with a limiter"

project: contexts: Mixer: valueObjects: {
	SendTap: {
		state: {bus: "BusId", level: "Amplitude", preFader: "bool"}
		description: "one send from a strip to an aux bus"
	}
	PeakLevel: {from: "f64", description: "most recent peak absolute sample level for metering", invariants: ["must be non-negative"]}
}

project: contexts: Mixer: aggregates: ChannelStrip: {
	root:    true
	purpose: "one patch's channel: input gain, insert chain, volume, pan, mute/solo, and up to 8 send taps"
	state: {inputGain: "Amplitude", volumeDb: "Decibel", pan: "Pan", mute: "bool", solo: "bool", sends: "list<SendTap>", peak: "PeakLevel"}
	commands: {
		SetVolume: {volumeDb: "Decibel"}
		SetPan: {pan: "Pan"}
		SetMute: {mute: "bool"}
		SetSolo: {solo: "bool"}
		SetSend: {index: "u8", tap: "SendTap"}
	}
	events: {
		LevelChanged: {volumeDb: "Decibel"}
		SoloChanged: {solo: "bool"}
	}
	invariants: [
		"a strip has at most 8 send taps",
		"peak metering reflects the level after volume and pan are applied",
	]
	contributesTo: [{capability: "capability.mix_to_stereo", contribution: "provides independent gain, inserts, pan, mute, solo, sends, and metering for one patch"}]
}

project: contexts: Mixer: aggregates: MixBus: {
	root:    true
	purpose: "a summing bus: aux buses receive send taps and return to the master; the master bus is the final summing point with its own inserts and limiter"
	state: {id: "BusId", returnLevel: "Amplitude"}
	commands: {
		SetReturnLevel: {level: "Amplitude"}
	}
	events: {
		ReturnLevelChanged: {level: "Amplitude"}
	}
	invariants: [
		"bus 0 is the master bus and cannot be removed",
		"aux buses feed the master bus, never each other",
	]
}

project: contexts: Mixer: domainServices: {
	MixEngine: {
		purpose: "one full mix pass: render strips, collect send taps into aux buses, process aux inserts, sum into the master bus, process master inserts and the limiter"
		uses: ["aggregate.Mixer.ChannelStrip", "aggregate.Mixer.MixBus", "domainService.Effects.ChainRenderer"]
		contributesTo: [{capability: "capability.mix_to_stereo", contribution: "coordinates the complete strip-to-aux-to-master signal path and final limiting"}]
	}
}

project: contexts: Mixer: applicationServices: {
	MixerController: {
		purpose: "application-level mixer operations: strip CRUD, solo group handling, bus management"
		uses: ["aggregate.Mixer.ChannelStrip", "aggregate.Mixer.MixBus"]
		operations: {
			setSolo: {input: {strip: "u32", solo: "bool"}}
		}
	}
}

// Solo is exclusive within a mix group: when any strip is soloed, all
// non-soloed strips are muted. Enforced by MixerController, observable at
// MixEngine output.
