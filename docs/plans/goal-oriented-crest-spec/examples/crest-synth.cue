package crestsynth

// TARGET-SCHEMA SAMPLE
//
// This is a compact goal-oriented version of fixtures/crest-synth/spec. It
// preserves the existing DDD/hexagonal architecture and adds intent,
// traceability, named verification, boundary profiles, and structured assets.
// Runtime context/execution manifests, evidence runs, failures, and evaluations
// are derived from this declaration and stored only in SQLite.

project: {
	name:    "crest-synth-goal-oriented-sample"
	mission: "Let performers design, play, mix, and preserve expressive synthesized instruments on a desktop or Steam Deck without violating hard real-time audio constraints."

	layers: ["domain", "application", "infrastructure"]
	layerRules: {
		application:    {dependsOn: ["domain"]}
		infrastructure: {dependsOn: ["domain", "application"]}
	}

	meta: {
		language: "rust"
		style:    "idiomatic Rust; newtypes for domain quantities; every UI action reachable by gamepad"
		avoid: [
			"heap allocation on the audio thread",
			"mutex or blocking lock on the audio thread",
			"blocking I/O on the audio thread",
		]
	}

	actors: {
		performer: {
			description: "a musician playing notes from external MIDI hardware"
		}
		sound_designer: {
			description: "a user editing voices, modulation, mixer state, and presets"
		}
	}

	nonGoals: {
		daw_plugin:       "CLAP, VST3, AU, and other DAW plug-in formats are excluded from this standalone instrument"
		cloud_sync:       "Preset and session cloud synchronization is excluded"
		mobile_interface: "Phone and tablet interfaces are excluded"
		onscreen_keyboard: "The editor changes parameters but does not trigger notes; performance comes from external MIDI"
		midi_file_performance: "MIDI-file playback is verification input, not the product performance workflow"
	}

	requirements: {
		audible_output: {
			kind:         "functional"
			description:  "A note received from external MIDI produces audible, non-clipping stereo output"
			goals:        ["goal.play_an_instrument"]
			capabilities: ["capability.render_audible_polyphony"]
		}
		gamepad_parity: {
			kind:         "functional"
			description:  "Every GUI editing action is reachable by gamepad; the GUI does not originate performance notes"
			goals:        ["goal.operate_from_steam_deck"]
			capabilities: ["capability.navigate_by_gamepad"]
		}
		realtime_callback: {
			kind:         "nonfunctional"
			description:  "The audio callback never allocates, locks, blocks, or frees retired memory"
			goals:        ["goal.play_an_instrument"]
			capabilities: ["capability.realtime_safe_control"]
		}
		atomic_session_restore: {
			kind:         "functional"
			description:  "A failed session restore leaves all prior state unchanged"
			goals:        ["goal.preserve_work"]
			capabilities: ["capability.save_and_restore_sessions"]
		}
	}

	goals: {
		play_an_instrument: {
			description: "A performer can play expressive polyphonic notes from external MIDI and hear safe stereo output"
			priority:    "required"
			actors:      ["actor.performer"]
			dependsOn:   []
			capabilities: [
				"capability.render_audible_polyphony",
				"capability.realtime_safe_control",
			]
			requirements: ["requirement.audible_output", "requirement.realtime_callback"]
		}
		mix_multiple_patches: {
			description: "A sound designer can mix patches through strips, sends, buses, and a protected master output"
			priority:    "required"
			actors:      ["actor.sound_designer"]
			dependsOn:   ["goal.play_an_instrument"]
			capabilities: ["capability.mix_signal_path"]
		}
		operate_from_steam_deck: {
			description:  "A performer can navigate and edit the synthesizer without a keyboard or mouse"
			priority:     "required"
			actors:       ["actor.performer", "actor.sound_designer"]
			dependsOn:    ["goal.play_an_instrument"]
			capabilities: ["capability.navigate_by_gamepad"]
			requirements: ["requirement.gamepad_parity"]
		}
		preserve_work: {
			description:  "A sound designer can save and atomically restore presets and complete sessions"
			priority:     "required"
			actors:       ["actor.sound_designer"]
			dependsOn:    ["goal.mix_multiple_patches"]
			capabilities: ["capability.save_and_restore_sessions"]
			requirements: ["requirement.atomic_session_restore"]
		}
	}

	capabilities: {
		render_audible_polyphony: {
			description: "Route normalized MIDI into allocated voices and render their summed output"
			goals:       ["goal.play_an_instrument"]
			acceptance: {
				audible_a440: {
					description: "A note-on for A440 renders one second of audible, non-clipping output"
					actor:       "actor.performer"
					steps: [
						{action: "send A4 with non-zero velocity from external MIDI", observes: "the normalized event activates a voice"},
						{action: "render one second", observes: "measured peak is greater than 0.1 and at most 1.0"},
					]
					evidence: ["evidence.audible_tone"]
				}
			}
		}
		realtime_safe_control: {
			description: "Move events, parameter snapshots, and retired memory across one lock-free seam"
			goals:       ["goal.play_an_instrument"]
			acceptance: {
				parameter_update: {
					description: "A UI parameter update becomes visible to the audio thread without locking"
					actor:       "actor.performer"
					steps: [
						{action: "publish a new parameter snapshot", observes: "audio reader observes the latest version"},
						{action: "inspect callback counters", observes: "allocations and blocking locks remain zero"},
					]
					evidence: ["evidence.realtime_boundary"]
				}
			}
		}
		mix_signal_path: {
			description: "Process strips, sends, aux buses, master inserts, and limiter in canonical order"
			goals:       ["goal.mix_multiple_patches"]
			acceptance: {
				post_fader_send: {
					description: "Changing a strip fader changes its default post-fader aux send"
					actor:       "actor.sound_designer"
					steps: [
						{action: "route a strip to an aux bus", observes: "aux receives the strip signal"},
						{action: "lower the strip fader", observes: "post-fader aux level falls"},
					]
					evidence: ["evidence.mixer_signal_path"]
				}
			}
		}
		navigate_by_gamepad: {
			description: "Map gamepad input to every GUI navigation and editing action"
			goals:       ["goal.operate_from_steam_deck"]
			acceptance: {
				edit_patch_without_pointer: {
					description: "A performer opens the patch editor and changes oscillator settings by gamepad"
					actor:       "actor.performer"
					steps: [
						{action: "open the patch editor with a gamepad action", observes: "patch editor becomes active"},
						{action: "adjust oscillator waveform", observes: "view state and patch configuration update"},
					]
					evidence: ["evidence.gamepad_navigation"]
				}
			}
		}
		save_and_restore_sessions: {
			description: "Persist versioned patch/session state and restore complete state atomically"
			goals:       ["goal.preserve_work"]
			acceptance: {
				atomic_round_trip: {
					description: "A complete session round-trips and a malformed session leaves current state intact"
					actor:       "actor.sound_designer"
					steps: [
						{action: "save a configured session", observes: "versioned session data is persisted"},
						{action: "restore the saved session", observes: "all patch and mixer state matches"},
						{action: "restore malformed data", observes: "the prior restored state is unchanged"},
					]
					evidence: ["evidence.session_round_trip"]
				}
			}
		}
	}

	evidence: {
		audible_tone:       {kind: "behavioral_witness", description: "Measured audible tone from the accepted engine"}
		realtime_boundary:   {kind: "behavioral_witness", description: "Lock-free boundary counters and observations"}
		mixer_signal_path:   {kind: "behavioral_witness", description: "Measured canonical mixer routing"}
		gamepad_navigation:  {kind: "behavioral_witness", description: "Gamepad-only UI journey"}
		session_round_trip:  {kind: "behavioral_witness", description: "Successful and failed atomic restore observations"}
	}

	completion: {
		requiredGoals: [
			"goal.play_an_instrument",
			"goal.mix_multiple_patches",
			"goal.operate_from_steam_deck",
			"goal.preserve_work",
		]
		projectChecks: ["validation.format", "validation.clippy", "validation.build", "validation.test"]
	}

	// Named validations preserve the existing mechanical gates while allowing
	// completion/evidence records to reference stable identifiers.
	validations: {
		format:  {kind: "custom", command: ["cargo", "fmt"], description: "normalize formatting"}
		clippy:  {kind: "compiles", command: ["cargo", "clippy", "--all-targets", "--", "-D", "warnings"]}
		build:   {kind: "compiles", command: ["cargo", "build"]}
		test:    {kind: "test", command: ["cargo", "test"]}
	}

	// Witnesses are executed by crest-spec against exact accepted source and
	// artifact hashes. Both commands use the same JSON observation schema.
	witnesses: {
		audible_tone: {
			goal: "goal.play_an_instrument"
			capability: "capability.render_audible_polyphony"
			command:         ["cargo", "run", "--bin", "tone_test", "--", "--case", "real"]
			negativeCommand: ["cargo", "run", "--bin", "tone_test", "--", "--case", "silent-stub"]
			artifacts: ["target/debug/tone_test"]
			observation: {
				kind:   "json_stdout"
				marker: "CREST_OBS"
				schema: {peak: "number"}
			}
			predicates: [{field: "peak", op: "range", min: 0.1, max: 1.0}]
		}
		realtime_boundary: {
			goal: "goal.play_an_instrument"
			capability: "capability.realtime_safe_control"
			command:         ["cargo", "test", "realtime_witness", "--", "--nocapture", "--case=real"]
			negativeCommand: ["cargo", "test", "realtime_witness", "--", "--nocapture", "--case=blocking-stub"]
			observation: {kind: "json_stdout", marker: "CREST_OBS", schema: {allocations: "number", locks: "number", latest_version: "number"}}
			predicates: [
				{field: "allocations", op: "eq", value: 0},
				{field: "locks", op: "eq", value: 0},
				{field: "latest_version", op: "gt", value: 0},
			]
		}
		mixer_signal_path: {
			goal: "goal.mix_multiple_patches"
			capability: "capability.mix_signal_path"
			command:         ["cargo", "test", "mixer_witness", "--", "--nocapture", "--case=real"]
			negativeCommand: ["cargo", "test", "mixer_witness", "--", "--nocapture", "--case=pre-fader-stub"]
			observation: {kind: "json_stdout", marker: "CREST_OBS", schema: {before: "number", after: "number"}}
			predicates: [{field: "before", op: "gt", value: 0.5}, {field: "after", op: "lt", value: 0.5}]
		}
		gamepad_navigation: {
			goal: "goal.operate_from_steam_deck"
			capability: "capability.navigate_by_gamepad"
			command:         ["cargo", "test", "gamepad_journey_witness", "--", "--nocapture", "--case=real"]
			negativeCommand: ["cargo", "test", "gamepad_journey_witness", "--", "--nocapture", "--case=no-mapping-stub"]
			observation: {kind: "json_stdout", marker: "CREST_OBS", schema: {editor_opened: "bool", patch_changed: "bool"}}
			predicates: [{field: "editor_opened", op: "equals", member: true}, {field: "patch_changed", op: "equals", member: true}]
		}
		session_round_trip: {
			goal: "goal.preserve_work"
			capability: "capability.save_and_restore_sessions"
			command:         ["cargo", "test", "session_witness", "--", "--nocapture", "--case=real"]
			negativeCommand: ["cargo", "test", "session_witness", "--", "--nocapture", "--case=partial-restore-stub"]
			observation: {kind: "json_stdout", marker: "CREST_OBS", schema: {round_trip_equal: "bool", failure_preserved_state: "bool"}}
			predicates: [{field: "round_trip_equal", op: "equals", member: true}, {field: "failure_preserved_state", op: "equals", member: true}]
		}
	}
}

// --------------------------------------------------------------------------
// Existing DDD implementation model, now linked to capabilities.
// --------------------------------------------------------------------------

project: contexts: Kernel: {
	purpose: "foundation value types for MIDI addressing and audio primitives"
	valueObjects: {
		NoteId:     {from: "u32", description: "unique per sounding note"}
		NoteNumber: {from: "u8", description: "MIDI note number", invariants: ["must be 0-127"]}
		Velocity:   {from: "f64", description: "normalized note velocity", invariants: ["must be 0.0-1.0"]}
		Frequency:  {from: "f64", description: "frequency in Hz", invariants: ["must be positive and finite"]}
		AudioFrame: {state: {left: "f32", right: "f32"}, description: "stereo sample pair"}

		// The event/message schema remains a value object, not a new resource kind.
		MidiEvent: {
			state: {noteId: "NoteId", note: "NoteNumber", velocity: "Velocity", kind: "string"}
			description: "normalized internal MIDI event"
			contributesTo: [{capability: "capability.render_audible_polyphony", contribution: "defines the normalized note event consumed by the engine"}]
		}
	}
}

project: contexts: Engine: {
	purpose: "polyphonic sound generation and voice allocation"
	aggregates: Voice: {
		root:    true
		purpose: "one sounding note with oscillator, filter, envelopes, and per-note expression"
		state: {noteId: "NoteId", note: "NoteNumber", velocity: "Velocity", stage: "string"}
		commands: {Trigger: {note: "NoteNumber", velocity: "Velocity", noteId: "NoteId"}, Release: {noteId: "NoteId"}}
		events: {Triggered: {noteId: "NoteId"}, Released: {noteId: "NoteId"}, BecameIdle: {noteId: "NoteId"}}
		invariants: [
			"the amp envelope progresses Idle -> Attack -> Decay -> Sustain -> Release -> Idle",
			"a voice is reclaimable only when its envelope reaches Idle",
		]
		contributesTo: [{capability: "capability.render_audible_polyphony", contribution: "owns the stateful voice lifecycle and renders one note"}]
	}
	domainServices: {
		VoiceAllocator: {
			purpose: "assign incoming notes to voices and steal according to policy"
			uses: ["aggregate.Engine.Voice"]
			contributesTo: [{capability: "capability.render_audible_polyphony", contribution: "maintains bounded polyphony under note load"}]
		}
		EngineRenderer: {
			purpose: "render all active voices and sum their output to stereo"
			uses: ["aggregate.Engine.Voice", "domainService.Engine.VoiceAllocator"]
			contributesTo: [{capability: "capability.render_audible_polyphony", contribution: "produces the engine stereo stream"}]
		}
	}
}

project: contexts: Mixer: {
	purpose: "channel strips, aux buses, and protected master output"
	aggregates: ChannelStrip: {
		root: true
		purpose: "one patch's gain, inserts, volume, pan, mute/solo, and sends"
		state: {volumeDb: "f64", pan: "f64", sends: "list<string>"}
		commands: {SetVolume: {volumeDb: "f64"}, SetPan: {pan: "f64"}}
		invariants: ["send taps are post-fader by default"]
		contributesTo: [{capability: "capability.mix_signal_path", contribution: "applies per-patch processing and send levels"}]
	}
	domainServices: MixEngine: {
		purpose: "process strips, aux buses, master inserts, and limiter in canonical order"
		uses: ["aggregate.Mixer.ChannelStrip"]
		contributesTo: [{capability: "capability.mix_signal_path", contribution: "renders the complete canonical mixer path"}]
	}
	applicationServices: MixerController: {
		purpose: "coordinate strip, solo-group, and bus use cases"
		uses: ["aggregate.Mixer.ChannelStrip"]
		operations: {setSolo: {input: {strip: "u32", solo: "bool"}}}
		contributesTo: [{capability: "capability.mix_signal_path", contribution: "coordinates user-visible mixer workflows"}]
	}
}

project: contexts: RealTime: {
	purpose: "lock-free seam between the UI/MIDI and audio threads"
	ports: {
		EventRing: {
			direction: "inbound"
			contract: {push: "(message: MidiEvent) -> result<(), RingFull>", pop: "() -> option<MidiEvent>"}
			contributesTo: [{capability: "capability.realtime_safe_control", contribution: "moves discrete events without locks"}]
		}
		ParameterBridge: {
			direction: "inbound"
			contract: {publish: "(snapshot: ParameterSnapshot) -> ()", read: "() -> ParameterSnapshot"}
			contributesTo: [{capability: "capability.realtime_safe_control", contribution: "provides latest-wins parameter snapshots"}]
		}
		DeferredDeallocator: {
			direction: "outbound"
			contract: {retire: "(allocation: Retired) -> ()", collect: "() -> u32"}
			contributesTo: [{capability: "capability.realtime_safe_control", contribution: "moves deallocation off the audio thread"}]
		}
	}
}

project: adapters: {
	RtrbEventRing: {
		implements: "port.RealTime.EventRing"
		layer:      "infrastructure"
		profile:    {kind: "in_process", topology: "single-producer-single-consumer"}
		meta:       {framework: "rtrb"}
		contributesTo: [{capability: "capability.realtime_safe_control", contribution: "implements the lock-free event boundary"}]
	}
	TripleBufferParameterBridge: {
		implements: "port.RealTime.ParameterBridge"
		layer:      "infrastructure"
		profile:    {kind: "in_process", topology: "single-writer-single-reader"}
		meta:       {framework: "triple_buffer"}
		contributesTo: [{capability: "capability.realtime_safe_control", contribution: "implements latest-wins parameter publication"}]
	}
}

project: contexts: Shell: {
	purpose: "ports for external MIDI performance, audio output, GUI rendering, and gamepad editing"
	ports: {
		MidiInput: {
			direction: "inbound"
			contract: {connect: "(onEvent: MidiCallback) -> result<Connection, MidiError>"}
			contributesTo: [{capability: "capability.render_audible_polyphony", contribution: "defines external performance events entering the engine"}]
		}
		AudioOutput: {
			direction: "outbound"
			contract: {open: "(callback: RenderCallback) -> result<Stream, AudioError>"}
			contributesTo: [{capability: "capability.render_audible_polyphony", contribution: "defines delivery of rendered frames to an audio device"}]
		}
		GuiRenderer: {
			direction: "outbound"
			contract: {render: "(view: ViewState) -> ()"}
			contributesTo: [{capability: "capability.navigate_by_gamepad", contribution: "renders gamepad-navigable view state"}]
		}
		GamepadInput: {
			direction: "inbound"
			contract: {poll: "() -> list<GamepadAction>"}
			contributesTo: [{capability: "capability.navigate_by_gamepad", contribution: "defines gamepad commands entering the application"}]
		}
	}
}

project: adapters: {
	MidirMidiInput: {
		implements: "port.Shell.MidiInput"
		layer:      "infrastructure"
		profile:    {kind: "device_input", device: "external MIDI controller"}
		meta:       {framework: "midir"}
		contributesTo: [{capability: "capability.render_audible_polyphony", contribution: "normalizes MIDI hardware events for the accepted voice engine"}]
	}
	CpalAudioOutput: {
		implements: "port.Shell.AudioOutput"
		layer:      "infrastructure"
		profile:    {kind: "device_output", device: "system audio"}
		meta:       {framework: "cpal"}
		contributesTo: [{capability: "capability.render_audible_polyphony", contribution: "delivers accepted engine output to the physical audio device"}]
	}
	EguiRenderer: {
		implements: "port.Shell.GuiRenderer"
		layer:      "infrastructure"
		profile: {
			kind:    "ui"
			surfaces: ["patch_editor", "mixer", "preset_browser", "mod_matrix"]
			accessibility: ["every action has a gamepad mapping"]
		}
		meta: {framework: "egui"}
		contributesTo: [{capability: "capability.navigate_by_gamepad", contribution: "renders all navigable synthesizer surfaces"}]
	}
	GilrsGamepadInput: {
		implements: "port.Shell.GamepadInput"
		layer:      "infrastructure"
		profile:    {kind: "device_input", device: "gamepad"}
		meta:       {framework: "gilrs"}
		contributesTo: [{capability: "capability.navigate_by_gamepad", contribution: "converts physical gamepad events into application actions"}]
	}
}

project: contexts: Preset: {
	purpose: "versioned preset and complete-session persistence"
	valueObjects: Preset: {
		state: {id: "u32", version: "u32"}
		description: "versioned snapshot of one patch"
		contributesTo: [{capability: "capability.save_and_restore_sessions", contribution: "defines a versioned persisted patch representation"}]
	}
	aggregates: Session: {
		root: true
		purpose: "snapshot of patches, mixer, buses, tempo, and time signature"
		commands: {Restore: {}}
		events: {Restored: {}}
		invariants: ["a failed restore leaves prior state untouched"]
		contributesTo: [{capability: "capability.save_and_restore_sessions", contribution: "owns atomic whole-session replacement semantics"}]
	}
	ports: PresetStorage: {
		direction: "outbound"
		contract: {save: "(preset: Preset) -> result<(), StorageError>", load: "(id: u32) -> result<Preset, StorageError>"}
		contributesTo: [{capability: "capability.save_and_restore_sessions", contribution: "defines durable preset/session storage"}]
	}
	applicationServices: SessionManager: {
		purpose: "coordinate save, load, list, delete, and atomic restore use cases"
		uses: ["aggregate.Preset.Session", "port.Preset.PresetStorage"]
		contributesTo: [{capability: "capability.save_and_restore_sessions", contribution: "coordinates the persistence workflow"}]
	}
}

project: adapters: FsPresetStorage: {
	implements: "port.Preset.PresetStorage"
	layer:      "infrastructure"
	profile:    {kind: "persistence", medium: "versioned local files"}
	contributesTo: [{capability: "capability.save_and_restore_sessions", contribution: "stores versioned presets and sessions durably"}]
}

// --------------------------------------------------------------------------
// Existing asset model, with narrow structured profiles.
// --------------------------------------------------------------------------

project: assetKinds: {
	"cargo-manifest": {description: "the crate's Cargo.toml", filePattern: "Cargo.toml"}
	"rust-bin-target": {description: "a binary under src/bin", filePattern: "src/bin/*.rs"}
	"rust-config":     {description: "typed runtime configuration", filePattern: "src/config/*.rs"}
	"rust-metrics":    {description: "runtime metrics definitions", filePattern: "src/observability/*.rs"}
	"markdown-guide":  {description: "human-facing project documentation", filePattern: "docs/*.md"}
}

project: assets: {
	RootCargoToml: {
		kind:        "cargo-manifest"
		description: "Cargo manifest for the generated synthesizer"
		profile:    {kind: "build_manifest", ecosystem: "cargo"}
		targets: ["adapter.CpalAudioOutput", "adapter.EguiRenderer", "adapter.GilrsGamepadInput"]
	}
	ToneTestMain: {
		kind:        "rust-bin-target"
		description: "Render A440 and emit the observation consumed by witness.audible_tone"
		profile:    {kind: "verification_harness", witness: "witness.audible_tone"}
		targets: ["domainService.Engine.EngineRenderer", "port.Shell.AudioOutput"]
		contributesTo: [{capability: "capability.render_audible_polyphony", contribution: "provides executable end-to-end evidence without becoming domain architecture"}]
	}
	AudioRuntimeConfig: {
		kind:        "rust-config"
		description: "Typed sample-rate, buffer-size, and device configuration with real-time-safe defaults"
		profile: {
			kind:          "configuration"
			source:        "environment and command-line overrides"
			secretPolicy:  "contains no secrets"
			failurePolicy: "reject invalid values before opening the audio stream"
		}
		targets: ["adapter.CpalAudioOutput"]
		contributesTo: [{capability: "capability.realtime_safe_control", contribution: "prevents invalid runtime configuration from reaching the audio callback"}]
	}
	RealtimeMetrics: {
		kind:        "rust-metrics"
		description: "Counters for callback allocations, locks, underruns, and boundary versions"
		profile: {
			kind:    "observability"
			signals: ["audio_callback_allocations", "audio_callback_locks", "audio_underruns", "parameter_snapshot_version"]
			constraint: "recording is wait-free and allocation-free on the audio thread"
		}
		targets: ["port.RealTime.EventRing", "port.RealTime.ParameterBridge", "adapter.CpalAudioOutput"]
		contributesTo: [{capability: "capability.realtime_safe_control", contribution: "makes real-time constraint violations observable and verifiable"}]
	}
	SteamDeckControlsGuide: {
		kind:        "markdown-guide"
		description: "Gamepad map and complete keyboard-free patch-editing walkthrough"
		profile: {
			kind:     "documentation"
			audience: "performers and sound designers using a Steam Deck"
			requiredExamples: ["open patch editor", "change oscillator", "mix a patch", "save and restore a session"]
		}
		targets: ["adapter.EguiRenderer", "adapter.GilrsGamepadInput"]
		contributesTo: [{capability: "capability.navigate_by_gamepad", contribution: "documents the same gamepad journey required by executable acceptance"}]
	}
}
