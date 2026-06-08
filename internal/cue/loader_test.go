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
}
