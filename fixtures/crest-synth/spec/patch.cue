package crestsynth

// Patch — one instrument: engine config + optional sample set + mod matrix +
// MIDI routing + mixer channel.

project: contexts: Patch: purpose: "instruments and MIDI routing: a patch binds a voice configuration, optional sample set, mod matrix, channel mapping, and a mixer strip"

project: contexts: Patch: valueObjects: {
	ChannelMapping: {
		state: {addresses: "list<ChannelAddress>", omni: "bool"}
		description: "which MIDI addresses a patch listens on; omni responds to all channels; multiple patches may layer on the same address intentionally"
	}
	MpeZone: {
		state: {managerChannel: "MidiChannel", memberChannels: "list<MidiChannel>"}
		description: "an MPE zone: a manager channel for global CCs plus contiguous member channels for per-note expression"
		invariants: ["memberChannels must be contiguous", "memberChannels must number at most 15"]
	}
}

project: contexts: Patch: aggregates: Patch: {
	root:    true
	purpose: "one playable instrument and its complete configuration"
	state: {id: "PatchId", voice: "VoiceConfig", sampleSet: "option<u32>", modMatrix: "ModMatrix", mapping: "ChannelMapping", mpeZone: "option<MpeZone>", mixerStrip: "u32"}
	commands: {
		SetVoiceConfig: {voice: "VoiceConfig"}
		SetMapping: {mapping: "ChannelMapping"}
		SetMpeZone: {zone: "option<MpeZone>"}
		AssignSampleSet: {sampleSet: "option<u32>"}
	}
	events: {
		ConfigChanged: {id: "PatchId"}
		MappingChanged: {id: "PatchId"}
	}
	invariants: [
		"a patch's MPE zone never overlaps another patch's zone",
	]
}

project: contexts: Patch: domainServices: {
	MidiDispatcher: {
		purpose: "routes each normalized MidiEvent to exactly the patches whose channel mapping matches its address"
		uses: ["aggregate.Patch.Patch"]
	}
}

project: contexts: Patch: applicationServices: {
	PatchManager: {
		purpose: "application-level patch CRUD: create/delete patches, edit voice/sample/mod/routing configuration"
		uses: ["aggregate.Patch.Patch"]
		operations: {
			createPatch: {output: {id: "PatchId"}}
			deletePatch: {input: {id: "PatchId"}}
		}
	}
}
