package crestsynth

// Phase 4 turns the engine into a multitimbral instrument: channel-addressed
// MIDI fans out to independently configured patches and one global mix.
project: {
	mission: "A standalone, gamepad-friendly MIDI synthesizer for Steam Deck and desktop, built around a hard real-time audio core."
	actors: performer: {description: "a musician performing a multichannel arrangement with layered patches"}
	goals: perform_multitimbral_arrangement: {
		description: "A performer can route MIDI channels to multiple instruments and hear their combined stereo mix"
		priority: "required"
		actors: ["actor.performer"]
		capabilities: ["capability.route_and_mix_patches"]
		requirements: ["requirement.intentional_channel_dispatch"]
	}
	capabilities: route_and_mix_patches: {
		description: "Dispatch addressed MIDI to matching patch voice pools and sum every active patch through the global mixer"
		goals: ["goal.perform_multitimbral_arrangement"]
		acceptance: multichannel_demo: {
			description: "A multi-channel MIDI arrangement activates distinct patches and renders their combined output"
			actor: "actor.performer"
			steps: [{action: "play channel-addressed events", observes: "only matching patches receive each event"}, {action: "render all patches", observes: "the global mix contains every active instrument"}]
			evidence: ["evidence.patch_demo"]
		}
	}
	requirements: intentional_channel_dispatch: {kind: "functional", description: "Each event reaches exactly the matching patch set; layering is allowed and MPE zones do not overlap", goals: ["goal.perform_multitimbral_arrangement"], capabilities: ["capability.route_and_mix_patches"]}
	evidence: patch_demo: {kind: "behavioral_witness", description: "make demo-patches proves dispatcher, per-patch voice pools, and global mix in one rendered WAV"}
	completion: {requiredGoals: ["goal.perform_multitimbral_arrangement"], projectChecks: ["validation.build", "validation.demo_patches"]}
}
