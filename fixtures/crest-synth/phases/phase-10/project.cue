package crestsynth

// crest-synth — reference spec for crest-spec.
// The spec is the source of truth; the Rust crate is generated from it.
// Full design rationale: ../DESIGN.md

project: name: "crest-synth"

// Injected into every generator's system prompt.
project: mission: "A standalone, gamepad-friendly MIDI synthesizer built in Rust — designed for the Steam Deck, runs on any desktop. Hexagonal architecture around a hard real-time audio thread: the audio callback has a hard deadline and must never allocate, lock, or block. Two threads — real-time audio and UI/MIDI — communicate only across a lock-free boundary (ring buffer for events, latest-wins snapshots for parameters, deferred deallocation for retired memory)."

project: layers: ["domain", "application", "infrastructure"]
project: layerRules: {
	application: {dependsOn: ["domain"]}
	infrastructure: {dependsOn: ["domain", "application"]}
}

project: meta: {
	language: "rust"
	style:    "idiomatic Rust; newtypes for domain quantities; small focused modules; every UI action reachable via gamepad"
	avoid: [
		"heap allocation on the audio thread",
		"mutex or other blocking locks on the audio thread",
		"blocking I/O on the audio thread",
		"dynamic dispatch in the inner sample loop",
	]
}

// Whole-tree gate: runs across the entire crate at wave verification.
// Formatting is NORMALIZED, never policed: `cargo fmt` auto-fixes the tree
// and always passes. The gate blocks on design-level failures only —
// lints, compilation, behavior.
project: validations: [
	{kind: "custom", command: ["cargo", "fmt"], description: "normalize formatting (auto-fix, never blocks)"},
	{kind: "compiles", command: ["cargo", "clippy", "--all-targets", "--", "-D", "warnings"], description: "clippy clean"},
	{kind: "compiles", command: ["cargo", "build"], description: "crate builds"},
	{kind: "test", command: ["cargo", "test"], description: "tests pass"},
]

// Architectural invariants — behavioral rules, injected into every generator
// prompt. Code that violates one is wrong even if it compiles and tests pass.
project: invariants: [
	// Real-time safety
	{text: "the audio thread never allocates heap memory", meta: rationale: "the callback has a hard deadline; the allocator can block unboundedly"},
	{text: "the audio thread never acquires a mutex or blocking lock", meta: rationale: "priority inversion causes audible dropouts"},
	{text: "the audio thread never performs blocking I/O", meta: rationale: "disk and network latency dwarf the callback budget"},
	{text: "all parameter changes cross the thread boundary via the ParameterBridge or the EventRing", meta: rationale: "a single auditable seam between the RT and non-RT worlds"},
	{text: "memory retired by the audio thread is freed via the DeferredDeallocator, never on the audio thread itself", meta: rationale: "freeing is allocation's twin; both are unbounded"},

	// Signal flow
	{text: "signal flows engine output → channel strip inserts → volume and pan → send taps and bus routing → aux bus inserts → master bus inserts → limiter → output", meta: rationale: "one canonical signal path; every processor knows its place"},
	{text: "insert chains process slots strictly in order with no feedback loops within a chain", meta: rationale: "feedback inside a chain makes latency and stability unanalyzable"},
	{text: "send taps are post-fader by default; pre-fader is an explicit opt-in per send", meta: rationale: "matches mixing-console convention"},

	// MIDI routing
	{text: "a MidiEvent is dispatched to exactly the set of patches whose channel mapping matches its address", meta: rationale: "layering is intentional (multiple matches allowed); leakage is not"},
	{text: "MPE zones never overlap across patches", meta: rationale: "an overlapping zone makes per-note expression ambiguous"},

	// Persistence
	{text: "presets serialize with an explicit version and older versions are migrated on load", meta: rationale: "user libraries must survive format evolution"},
	{text: "restoring a session replaces all state atomically — a failed load leaves prior state untouched", meta: rationale: "no partial loads; a half-restored session is corruption"},
]

// Bounded-context relationships (DDD context map).
project: contextMap: [
	{from: "Kernel", to: "Engine", kind: "shared-kernel"},
	{from: "Kernel", to: "Sample", kind: "shared-kernel"},
	{from: "Kernel", to: "Effects", kind: "shared-kernel"},
	{from: "Kernel", to: "Mixer", kind: "shared-kernel"},
	{from: "Kernel", to: "Modulation", kind: "shared-kernel"},
	{from: "Kernel", to: "Patch", kind: "shared-kernel"},
	{from: "Kernel", to: "Preset", kind: "shared-kernel"},
	{from: "Kernel", to: "RealTime", kind: "shared-kernel"},
	{from: "Engine", to: "Patch", kind: "customer-supplier", direction: "upstream"},
	{from: "Sample", to: "Patch", kind: "customer-supplier", direction: "upstream"},
	{from: "Modulation", to: "Patch", kind: "customer-supplier", direction: "upstream"},
	{from: "Effects", to: "Mixer", kind: "customer-supplier", direction: "upstream"},
	{from: "Patch", to: "Preset", kind: "customer-supplier", direction: "upstream"},
	{from: "Mixer", to: "RealTime", kind: "anti-corruption", direction: "downstream"},
	{from: "Patch", to: "RealTime", kind: "anti-corruption", direction: "downstream"},
	{from: "Shell", to: "RealTime", kind: "anti-corruption", direction: "downstream"},
]

project: assetKinds: {
	"cargo-manifest": {description: "the crate's Cargo.toml manifest", filePattern: "Cargo.toml"}
	"rust-bin-target": {description: "a binary target under src/bin/", filePattern: "src/bin/*.rs"}
}
