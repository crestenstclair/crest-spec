package crestsynth

// RealTime — the lock-free seam between the audio thread and everything else.
// Everything crossing the boundary goes through one of these three ports.

project: contexts: RealTime: purpose: "the lock-free boundary: discrete events over a SPSC ring, latest-wins parameter snapshots, and deferred deallocation of memory retired by the audio thread"

project: contexts: RealTime: valueObjects: {
	BoundaryMessage: {description: "a discrete message crossing the boundary: NoteOn, NoteOff, ControlChange, PatchChange, PresetLoad, or ParameterUpdate"}
	ParameterSnapshot: {description: "a complete, immutable snapshot of every audio-thread-readable parameter"}
}

project: contexts: RealTime: ports: {
	EventRing: {
		contract: {
			push: "(message: BoundaryMessage) -> result<(), RingFull>"
			pop:  "() -> option<BoundaryMessage>"
		}
		meta: notes: "single producer (UI/MIDI thread), single consumer (audio thread)"
	}
	ParameterBridge: {
		contract: {
			publish: "(snapshot: ParameterSnapshot) -> ()"
			read:    "() -> ParameterSnapshot"
		}
		meta: notes: "writer publishes; reader always gets the latest snapshot without blocking"
	}
	DeferredDeallocator: {
		contract: {
			retire:  "(allocation: Retired) -> ()"
			collect: "() -> u32"
		}
		meta: notes: "the audio thread retires; a background thread frees"
	}
}

project: adapters: RtrbEventRing: {
	implements: "port.RealTime.EventRing"
	layer:      "infrastructure"
	meta: framework: "rtrb"
}

project: adapters: TripleBufferParameterBridge: {
	implements: "port.RealTime.ParameterBridge"
	layer:      "infrastructure"
	meta: framework: "triple_buffer"
}

project: adapters: BasedropDeferredDeallocator: {
	implements: "port.RealTime.DeferredDeallocator"
	layer:      "infrastructure"
	meta: framework: "basedrop"
}
