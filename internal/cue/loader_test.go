package cue

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testdataDir(sub string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", sub)
}

func TestLoad_Minimal(t *testing.T) {
	p, err := Load(testdataDir("minimal"))
	require.NoError(t, err)

	assert.Equal(t, "test-project", p.Name)
	assert.Equal(t, []string{"domain", "application", "infrastructure"}, p.Layers)
	assert.Equal(t, "go", p.Meta.Language)
	assert.Equal(t, "DDD", p.Meta.Style)

	ctx, ok := p.Contexts["Synth"]
	require.True(t, ok)
	assert.Equal(t, "Audio synthesis engine", ctx.Purpose)
	assert.Equal(t, "Waveform generator", ctx.UbiquitousLanguage["oscillator"])

	voice, ok := ctx.Aggregates["Voice"]
	require.True(t, ok)
	assert.True(t, voice.Root)
	assert.Equal(t, "Manages a single voice", voice.Purpose)
	assert.Equal(t, "float64", voice.State["frequency"])
	assert.Contains(t, voice.Invariants, "frequency > 0")

	osc, ok := voice.Entities["Oscillator"]
	require.True(t, ok)
	assert.Equal(t, "string", osc.State["waveform"])

	noteId, ok := voice.ValueObjects["NoteId"]
	require.True(t, ok)
	assert.Equal(t, "MIDI note number", noteId.Description)

	freq, ok := ctx.ValueObjects["Frequency"]
	require.True(t, ok)
	assert.Equal(t, "float64", freq.State["hz"])
	assert.Len(t, freq.Invariants, 2)

	pool, ok := ctx.Repositories["VoicePool"]
	require.True(t, ok)
	assert.Equal(t, "aggregate.Synth.Voice", pool.Of)

	ao, ok := ctx.Ports["AudioOutput"]
	require.True(t, ok)
	assert.Equal(t, "([]float64) -> error", ao.Contract["write"])

	mixer, ok := ctx.DomainServices["Mixer"]
	require.True(t, ok)
	assert.Equal(t, []string{"aggregate.Synth.Voice"}, mixer.Uses)

	voiceImpl, ok := ctx.Assets["VoiceImpl"]
	require.True(t, ok)
	assert.Equal(t, "source_file", voiceImpl.Kind)
	assert.Equal(t, []string{"aggregate.Synth.Voice"}, voiceImpl.Targets)

	adapter, ok := p.Adapters["CoreAudioAdapter"]
	require.True(t, ok)
	assert.Equal(t, "port.Synth.AudioOutput", adapter.Implements)
	assert.Equal(t, "infrastructure", adapter.Layer)

	ak, ok := p.AssetKinds["source_file"]
	require.True(t, ok)
	assert.Equal(t, "Go source file", ak.Description)

	readme, ok := p.Assets["README"]
	require.True(t, ok)
	assert.Equal(t, "source_file", readme.Kind)

	assert.Len(t, p.Invariants, 1)
	assert.Equal(t, "All aggregates must have a root entity", p.Invariants[0].Text)

	assert.Len(t, p.ContextMap, 1)
	assert.Equal(t, "Synth", p.ContextMap[0].From)
}

func TestLoad_MultiFile(t *testing.T) {
	p, err := Load(testdataDir("multi"))
	require.NoError(t, err)

	assert.Equal(t, "multi-test", p.Name)

	alpha := p.Contexts["Alpha"]
	widget := alpha.Aggregates["Widget"]
	assert.Equal(t, "int", widget.State["size"])
	assert.Equal(t, "string", widget.State["color"])

	_, ok := alpha.ValueObjects["WidgetId"]
	assert.True(t, ok)
}

func TestLoad_Invalid(t *testing.T) {
	_, err := Load(testdataDir("invalid"))
	assert.Error(t, err)
}

func TestLoad_NonexistentDir(t *testing.T) {
	_, err := Load("/nonexistent/path")
	assert.Error(t, err)
}

func TestLoad_ProjectValidations(t *testing.T) {
	dir := t.TempDir()
	cue := `package p
project: name: "v"
project: mission: "Verify named project validations"
project: actors: developer: {description: "the developer validating the project"}
project: goals: valid_project: {description: "project validation succeeds", priority: "required", actors: ["actor.developer"], capabilities: ["capability.validate_project"], requirements: ["requirement.validation_runs"]}
project: capabilities: validate_project: {description: "run project validation", goals: ["goal.valid_project"], acceptance: validation_passes: {description: "validation commands pass", actor: "actor.developer", evidence: ["evidence.validation_result"]}}
project: requirements: validation_runs: {kind: "functional", description: "declared validation runs", goals: ["goal.valid_project"], capabilities: ["capability.validate_project"]}
project: evidence: validation_result: {kind: "validation", description: "the validation result"}
project: completion: requiredGoals: ["goal.valid_project"]
project: validations: [
	{kind: "compiles", command: ["cargo", "build"], description: "builds"},
	{kind: "test", command: ["cargo", "test"]},
]
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "p.cue"), []byte(cue), 0o644))
	p, err := Load(dir)
	require.NoError(t, err)
	require.Len(t, p.Validations, 2)
	assert.Equal(t, "compiles", p.Validations[0].Kind)
	assert.Equal(t, []string{"cargo", "build"}, p.Validations[0].Command)
	assert.Equal(t, "project", p.Validations[0].Scope)
	assert.Contains(t, p.Validations[0].ID, "validation.legacy.")
}

func TestLoad_NamedValidationAndExecutableWitness(t *testing.T) {
	dir := t.TempDir()
	cue := `package p
project: {
	name: "verified"
	mission: "execute trustworthy evidence"
	actors: developer: {description: "developer"}
	goals: verified: {description: "behavior is verified", priority: "required", actors: ["actor.developer"], capabilities: ["capability.run"], requirements: ["requirement.proven"]}
	capabilities: run: {description: "run behavior", goals: ["goal.verified"], acceptance: works: {description: "behavior works", actor: "actor.developer", evidence: ["evidence.witness"]}}
	requirements: proven: {kind: "functional", description: "behavior is proven", goals: ["goal.verified"], capabilities: ["capability.run"]}
	evidence: witness: {kind: "behavioral", description: "controlled witness", validations: ["validation.project_test"], witnesses: ["witness.round_trip"]}
	completion: {requiredGoals: ["goal.verified"], projectChecks: ["validation.project_test"]}
	validations: project_test: {scope: "project", kind: "test", command: ["go", "test", "./..."]}
	witnesses: round_trip: {
		scope: "goal", goal: "goal.verified", capability: "capability.run", evidence: ["evidence.witness"]
		command: ["go", "test", "./e2e", "-run", "TestReal"]
		negativeCommand: ["go", "test", "./e2e", "-run", "TestNegative"]
		workingDirectory: ".", timeout: "2m", environment: ["PATH"], artifacts: ["bin/example"]
		observation: {kind: "json_stdout", marker: "CREST_OBS", schema: {worked: "bool"}}
		predicates: [{field: "worked", op: "equals", member: true}]
	}
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "p.cue"), []byte(cue), 0o644))
	p, err := Load(dir)
	require.NoError(t, err)
	require.Len(t, p.Validations, 1)
	require.Equal(t, "validation.project_test", p.Validations[0].ID)
	require.True(t, p.Validations[0].Named)
	require.Equal(t, "witness.round_trip", p.Witnesses["round_trip"].ID)
}

func TestLoad_RejectsUnsafeOrNonFalsifiableWitness(t *testing.T) {
	dir := t.TempDir()
	cue := `package p
project: {
	name: "invalid-witness", mission: "reject invalid evidence"
	actors: developer: {description: "developer"}
	goals: verified: {description: "verified", priority: "required", capabilities: ["capability.run"]}
	capabilities: run: {description: "run", goals: ["goal.verified"], acceptance: works: {description: "works", evidence: ["evidence.witness"]}}
	requirements: proven: {kind: "functional", description: "proven", goals: ["goal.verified"]}
	evidence: witness: {kind: "behavioral", description: "witness"}
	completion: requiredGoals: ["goal.verified"]
	witnesses: bad: {
		scope: "goal", goal: "goal.verified", command: ["go", "test"], negativeCommand: ["go", "test"]
		workingDirectory: "../outside", observation: {kind: "json_stdout", marker: "", schema: {worked: "bool"}}
		predicates: [{field: "missing", op: "equals", member: true}]
	}
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "p.cue"), []byte(cue), 0o644))
	_, err := Load(dir)
	require.Error(t, err)
	require.ErrorContains(t, err, "negativeCommand must differ")
	require.ErrorContains(t, err, "workingDirectory must remain inside")
	require.ErrorContains(t, err, "absent from the observation schema")
}
