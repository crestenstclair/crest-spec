package crestsynth

// Shell — infrastructure adapters for audio, MIDI, window/GUI, and gamepad.
// The engine library stays host-agnostic; the shell is the only place that
// touches devices and windows.

project: contexts: Shell: purpose: "ports and adapters for the outside world: audio output, MIDI input, windowing/GUI, and gamepad navigation (every UI action reachable via gamepad)"

project: contexts: Shell: valueObjects: {
	MidiPortInfo: {state: {name: "string", index: "u32"}, description: "one connectable MIDI input port"}
	GamepadAction: {description: "a mapped gamepad input: navigate (d-pad), fine-adjust (left stick), scroll (right stick), select (A), back (B), switch view (triggers), switch patch (bumpers), save session (start), open browser (select)"}
}

project: contexts: Shell: ports: {
	AudioOutput: {
		contract: {
			open:  "(sampleRate: SampleRate, bufferSize: BufferSize, callback: RenderCallback) -> result<Stream, AudioError>"
			close: "(stream: Stream) -> ()"
		}
		meta: notes: "the callback runs on the audio thread and is bound by the real-time invariants"
		contributesTo: [{capability: "capability.operate_audio_and_midi_devices", contribution: "owns lifecycle and callback delivery for the selected desktop audio stream"}]
	}
	MidiInput: {
		contract: {
			listPorts:  "() -> list<MidiPortInfo>"
			connect:    "(port: MidiPortInfo, onEvent: EventCallback) -> result<Connection, MidiError>"
			disconnect: "(connection: Connection) -> ()"
		}
		contributesTo: [
			{capability: "capability.accept_external_midi", contribution: "owns connection and event delivery for external MIDI hardware"},
			{capability: "capability.operate_audio_and_midi_devices", contribution: "owns selection and lifecycle of the external MIDI connection"},
		]
	}
	AppWindow: {
		contract: {
			run: "(app: App) -> result<(), WindowError>"
		}
	}
	GuiRenderer: {
		contract: {
			render: "(view: ViewState) -> ()"
		}
		meta: notes: "renders the patch editor, mixer view, preset browser, and mod matrix editor"
		contributesTo: [{capability: "capability.edit_without_pointer", contribution: "presents every sound-design and instrument-management view"}]
	}
	GamepadInput: {
		contract: {
			poll: "() -> list<GamepadAction>"
		}
		contributesTo: [{capability: "capability.edit_without_pointer", contribution: "defines the complete pointer-free editing action vocabulary"}]
	}
}

project: contexts: Shell: domainServices: {
	MidiNormalizer: {
		purpose: "converts raw MIDI 1.0 bytes into normalized MidiEvents: addressed, high-resolution values, NoteId assigned"
		uses: ["valueObject.Kernel.MidiEvent"]
		contributesTo: [{capability: "capability.accept_external_midi", contribution: "turns raw MIDI 1.0 input into addressed high-resolution events with stable note identity"}]
	}
}

project: adapters: CpalAudioOutput: {
	implements: "port.Shell.AudioOutput"
	layer:      "infrastructure"
	meta: framework: "cpal"
	contributesTo: [{capability: "capability.operate_audio_and_midi_devices", contribution: "opens the physical audio stream and invokes the accepted real-time callback"}]
}

project: adapters: MidirMidiInput: {
	implements: "port.Shell.MidiInput"
	layer:      "infrastructure"
	meta: framework: "midir"
	contributesTo: [
		{capability: "capability.accept_external_midi", contribution: "delivers raw events from the selected external MIDI device"},
		{capability: "capability.operate_audio_and_midi_devices", contribution: "implements external MIDI port discovery and connection lifecycle"},
	]
}

project: adapters: EframeAppWindow: {
	implements: "port.Shell.AppWindow"
	layer:      "infrastructure"
	meta: framework: "eframe"
}

project: adapters: EguiRenderer: {
	implements: "port.Shell.GuiRenderer"
	layer:      "infrastructure"
	meta: framework: "egui"
	contributesTo: [{capability: "capability.edit_without_pointer", contribution: "renders the gamepad- and keyboard-navigable instrument views"}]
}

project: adapters: GilrsGamepadInput: {
	implements: "port.Shell.GamepadInput"
	layer:      "infrastructure"
	meta: framework: "gilrs"
	contributesTo: [{capability: "capability.edit_without_pointer", contribution: "converts physical controller input into normalized editor actions"}]
}
