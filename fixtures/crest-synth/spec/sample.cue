package crestsynth

// Sample — sample playback and soundfonts with key/velocity zone mapping.

project: contexts: Sample: purpose: "sample-based sound sources: WAV and SF2 loading, zone mapping by key and velocity range, pitched playback with interpolation and looping"

project: contexts: Sample: valueObjects: {
	InterpolationMode: {description: "resampling quality: none (nearest), linear, cubic, or sinc"}
	LoopMode: {description: "playback looping: no loop, forward, ping-pong, or release (loop until note-off then play to end)"}
	KeyRange: {state: {low: "NoteNumber", high: "NoteNumber"}, description: "inclusive note range a zone responds to", invariants: ["low must be <= high"]}
	VelocityRange: {state: {low: "Velocity", high: "Velocity"}, description: "inclusive velocity range a zone responds to", invariants: ["low must be <= high"]}
	Zone: {
		state: {keys: "KeyRange", velocities: "VelocityRange", rootKey: "NoteNumber", fineTuneCents: "f64", gain: "Amplitude", pan: "Pan", loopMode: "LoopMode"}
		description: "maps a key range + velocity range to one sample with per-zone playback settings"
	}
}

project: contexts: Sample: aggregates: SampleSet: {
	root:    true
	purpose: "a named collection of zones backing one instrument sound"
	state: {zones: "list<Zone>", interpolation: "InterpolationMode"}
	commands: {
		AddZone: {zone: "Zone"}
		RemoveZone: {index: "u32"}
	}
	events: {
		ZoneAdded: {index: "u32"}
		ZoneRemoved: {index: "u32"}
	}
	invariants: [
		"resolving a note returns every zone whose key range and velocity range both match",
	]
}

project: contexts: Sample: ports: {
	SampleLoader: {
		contract: {
			loadWav: "(path: Path) -> result<SampleSet, LoadError>"
			loadSf2: "(path: Path) -> result<list<SampleSet>, LoadError>"
		}
	}
	SampleStore: {
		contract: {
			put: "(id: u32, set: SampleSet) -> ()"
			get: "(id: u32) -> option<SampleSet>"
		}
	}
}

project: contexts: Sample: domainServices: {
	ZoneResolver: {
		purpose: "given a note and velocity, finds every matching zone in a sample set"
		uses: ["aggregate.Sample.SampleSet"]
	}
	SamplePlayer: {
		purpose: "plays a zone's sample at the correct pitch with the configured interpolation and loop mode"
		uses: ["aggregate.Sample.SampleSet", "domainService.Sample.ZoneResolver"]
	}
}

project: adapters: SymphoniaSampleLoader: {
	implements: "port.Sample.SampleLoader"
	layer:      "infrastructure"
	meta: framework: "symphonia"
}
