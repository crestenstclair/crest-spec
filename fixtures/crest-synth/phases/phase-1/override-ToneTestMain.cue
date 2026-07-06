package crestsynth

// Override (phase 1): ToneTestMain must build and actually run, producing audible
// output / a WAV file — not just compile. Picked up by run-phased-agent.sh, which
// copies the highest-numbered phase-N.override-ToneTestMain.cue (N <= target phase),
// so a later phase can replace this without a CUE list-unification conflict.

project: assets: ToneTestMain: validations: [
	{kind: "compiles", command: ["make", "build"], description: "project builds cleanly"},
	{kind: "integration", command: ["make", "run"], description: "tone test runs and produces WAV output", assertions: [
		{kind: "exit_code", expected: 0},
		{kind: "file_exists", path: "tone-test.wav"},
	]},
]
