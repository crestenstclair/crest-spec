package cue

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadMinimalProject(t *testing.T) *Project {
	t.Helper()
	p, err := Load(testdataDir("minimal"))
	require.NoError(t, err)
	return p
}

func TestNewRegistry_AllResourceIDs(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	expectedIDs := []string{
		"project.test-project",
		"context.Synth",
		"aggregate.Synth.Voice",
		"entity.Synth.Voice.Oscillator",
		"valueObject.Synth.Voice.NoteId",
		"valueObject.Synth.Frequency",
		"repository.Synth.VoicePool",
		"port.Synth.AudioOutput",
		"domainService.Synth.Mixer",
		"adapter.CoreAudioAdapter",
		"assetKind.source_file",
		"asset.README",
		"asset.Synth.VoiceImpl",
	}

	for _, id := range expectedIDs {
		assert.True(t, reg.Has(id), "missing resource: %s", id)
	}

	assert.Equal(t, len(expectedIDs), len(reg.Resources))
}

func TestNewRegistry_ResourceKinds(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	tests := map[string]string{
		"project.test-project":          "project",
		"context.Synth":                 "context",
		"aggregate.Synth.Voice":         "aggregate",
		"entity.Synth.Voice.Oscillator": "entity",
		"valueObject.Synth.Frequency":   "valueObject",
		"repository.Synth.VoicePool":    "repository",
		"port.Synth.AudioOutput":        "port",
		"domainService.Synth.Mixer":     "domainService",
		"adapter.CoreAudioAdapter":      "adapter",
		"assetKind.source_file":         "assetKind",
		"asset.README":                  "asset",
		"asset.Synth.VoiceImpl":         "asset",
	}

	for id, expectedKind := range tests {
		r := reg.Resources[id]
		assert.Equal(t, expectedKind, r.Kind, "wrong kind for %s", id)
	}
}

func TestNewRegistry_ContextName(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	assert.Equal(t, "", reg.Resources["project.test-project"].ContextName)
	assert.Equal(t, "Synth", reg.Resources["context.Synth"].ContextName)
	assert.Equal(t, "Synth", reg.Resources["aggregate.Synth.Voice"].ContextName)
	assert.Equal(t, "", reg.Resources["adapter.CoreAudioAdapter"].ContextName)
}

func TestNewRegistry_MetaMerging(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	voiceMeta := reg.Resources["aggregate.Synth.Voice"].Meta
	assert.Equal(t, "go", voiceMeta.Language)
	assert.Equal(t, "DDD", voiceMeta.Style)
	assert.Equal(t, "Core audio context", voiceMeta.Notes)
}

func TestNewRegistry_DependencyEdges(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	// adapter implements port
	adapter := reg.Resources["adapter.CoreAudioAdapter"]
	assert.Len(t, adapter.Dependencies, 1)
	assert.Equal(t, "port.Synth.AudioOutput", adapter.Dependencies[0].TargetID)
	assert.Equal(t, "implements", adapter.Dependencies[0].Kind)

	// repository of aggregate
	repo := reg.Resources["repository.Synth.VoicePool"]
	assert.Len(t, repo.Dependencies, 1)
	assert.Equal(t, "aggregate.Synth.Voice", repo.Dependencies[0].TargetID)
	assert.Equal(t, "of", repo.Dependencies[0].Kind)

	// domain service uses aggregate
	mixer := reg.Resources["domainService.Synth.Mixer"]
	hasUsesDep := false
	for _, dep := range mixer.Dependencies {
		if dep.TargetID == "aggregate.Synth.Voice" && dep.Kind == "uses" {
			hasUsesDep = true
		}
	}
	assert.True(t, hasUsesDep)

	// asset targets aggregate
	asset := reg.Resources["asset.Synth.VoiceImpl"]
	hasTargetDep := false
	hasKindDep := false
	for _, dep := range asset.Dependencies {
		if dep.TargetID == "aggregate.Synth.Voice" && dep.Kind == "targets" {
			hasTargetDep = true
		}
		if dep.TargetID == "assetKind.source_file" && dep.Kind == "uses" {
			hasKindDep = true
		}
	}
	assert.True(t, hasTargetDep)
	assert.True(t, hasKindDep)
}

func TestNewRegistry_DanglingReference(t *testing.T) {
	p := &Project{
		Name: "bad-refs",
		Contexts: map[string]Context{
			"A": {
				Purpose: "test",
				DomainServices: map[string]DomainService{
					"Svc": {
						Purpose: "test",
						Uses:    []string{"nonexistent.Thing"},
					},
				},
			},
		},
	}

	_, err := NewRegistry(p)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent.Thing")
}

func TestNewRegistry_Declaration(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	voice := reg.Resources["aggregate.Synth.Voice"]
	agg, ok := voice.Declaration.(Aggregate)
	require.True(t, ok)
	assert.True(t, agg.Root)
	assert.Equal(t, "Manages a single voice", agg.Purpose)
}
