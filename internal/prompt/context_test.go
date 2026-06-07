package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInjectRuntimeContext_AllFields(t *testing.T) {
	base := "base prompt content"
	ctx := RuntimeContext{
		ModuleTree:      "src/\n  Synth/\n    Voice.go",
		DependencyFiles: map[string]string{"aggregate.Synth.Voice": "package voice\n..."},
		AgentNotes:      map[string]string{"aggregate.Synth.Voice": "Used builder pattern for state"},
		WaveErrors:      "error[E0425]: cannot find value `Oscillator`",
		UserGuidance:    "Use try_send for audio thread",
	}

	result := InjectRuntimeContext(base, ctx)

	assert.Contains(t, result, "base prompt content")
	assert.Contains(t, result, "Module Tree")
	assert.Contains(t, result, "src/\n  Synth/\n    Voice.go")
	assert.Contains(t, result, "Existing Dependencies")
	assert.Contains(t, result, "package voice")
	assert.Contains(t, result, "Notes from Dependencies")
	assert.Contains(t, result, "Used builder pattern for state")
	assert.Contains(t, result, "Previous Errors")
	assert.Contains(t, result, "cannot find value")
	assert.Contains(t, result, "User Guidance")
	assert.Contains(t, result, "try_send")
}

func TestInjectRuntimeContext_Empty(t *testing.T) {
	base := "base prompt"
	ctx := RuntimeContext{}

	result := InjectRuntimeContext(base, ctx)

	assert.Equal(t, base, result)
}

func TestInjectRuntimeContext_PartialFields(t *testing.T) {
	base := "base prompt"
	ctx := RuntimeContext{
		ModuleTree: "src/\n  Synth/",
	}

	result := InjectRuntimeContext(base, ctx)

	assert.Contains(t, result, "Module Tree")
	assert.NotContains(t, result, "Existing Dependencies")
	assert.NotContains(t, result, "Notes from Dependencies")
	assert.NotContains(t, result, "Previous Errors")
	assert.NotContains(t, result, "User Guidance")
}
