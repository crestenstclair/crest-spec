package crestsynth

// crest-synth — phase 2 of the evolving design (see ../../DESIGN.md).
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

	// Signal flow

	// MIDI routing

	// Persistence
]

// Bounded-context relationships (DDD context map).
project: contextMap: [
	{from: "Kernel", to: "Engine", kind: "shared-kernel"},
]

project: assetKinds: {
	"cargo-manifest": {description: "the crate's Cargo.toml manifest", filePattern: "Cargo.toml"}
	"rust-bin-target": {description: "a binary target under src/bin/", filePattern: "src/bin/*.rs"}
}
