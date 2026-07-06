package crestsynth

// Override (phase 3): live playback is now the default, so the tone test writes a
// WAV only with `--wav`. This replaces the phase-1 override (run-phased-agent.sh
// keeps only the highest-numbered phase-N.override-ToneTestMain.cue <= target).

project: assets: ToneTestMain: validations: [
	{kind: "integration", command: ["cargo", "run", "--", "--wav"], description: "arpeggio renders to WAV", assertions: [
		{kind: "exit_code", expected: 0},
		{kind: "file_exists", path: "tone-test.wav"},
	]},
]
