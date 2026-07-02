package crestsynth

// Shell — infrastructure adapters for audio and MIDI. The engine library
// stays host-agnostic; the shell is the only place that touches devices.
// Window/GUI/gamepad adapters arrive in phase 10.

project: contexts: Shell: purpose: "ports and adapters for the outside world: audio output and MIDI input (window/GUI/gamepad arrive in phase 10)"

project: contexts: Shell: valueObjects: {
	MidiPortInfo: {state: {name: "string", index: "u32"}, description: "one connectable MIDI input port"}
}

project: contexts: Shell: ports: {
	AudioOutput: {
		contract: {
			open:  "(sampleRate: SampleRate, bufferSize: BufferSize, callback: RenderCallback) -> result<Stream, AudioError>"
			close: "(stream: Stream) -> ()"
		}
		meta: notes: "the callback runs on the audio thread and is bound by the real-time invariants"
	}
	MidiInput: {
		contract: {
			listPorts:  "() -> list<MidiPortInfo>"
			connect:    "(port: MidiPortInfo, onEvent: EventCallback) -> result<Connection, MidiError>"
			disconnect: "(connection: Connection) -> ()"
		}
	}
}

project: contexts: Shell: domainServices: {
	MidiNormalizer: {
		purpose: "converts raw MIDI 1.0 bytes into normalized MidiEvents: addressed, high-resolution values, NoteId assigned"
		uses: ["valueObject.Kernel.MidiEvent"]
	}
}

project: adapters: CpalAudioOutput: {
	implements: "port.Shell.AudioOutput"
	layer:      "infrastructure"
	meta: framework: "cpal"
}

project: adapters: MidirMidiInput: {
	implements: "port.Shell.MidiInput"
	layer:      "infrastructure"
	meta: framework: "midir"
}
