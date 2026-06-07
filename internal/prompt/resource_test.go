package prompt

import (
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/stretchr/testify/assert"
)

func makeTestRegistry() *cuepkg.Registry {
	portDecl := cuepkg.Port{
		Contract: map[string]string{"write": "([]float64) -> error"},
	}
	aggDecl := cuepkg.Aggregate{
		Root:    true,
		Purpose: "Manages a single voice",
		State:   map[string]string{"frequency": "float64", "amplitude": "float64"},
		Commands: map[string]map[string]string{
			"NoteOn": {"frequency": "float64", "velocity": "float64"},
		},
		Events: map[string]map[string]string{
			"VoiceStarted": {"frequency": "float64"},
		},
		Invariants: []string{"frequency > 0"},
		Implements: "port.Synth.AudioOutput",
	}
	svcDecl := cuepkg.DomainService{
		Purpose: "Combines voice outputs",
		Uses:    []string{"aggregate.Synth.Voice"},
	}
	repoDecl := cuepkg.Repository{
		Of:       "aggregate.Synth.Voice",
		Contract: map[string]string{"acquire": "() -> Voice"},
	}
	adapterDecl := cuepkg.Adapter{
		Implements: "port.Synth.AudioOutput",
		Layer:      "infrastructure",
	}
	assetKindDecl := cuepkg.AssetKind{
		Description: "Go source file",
		FilePattern: "{{snakeCase .Name}}.go",
		Prompts:     []string{"Follow Go conventions"},
	}
	assetDecl := cuepkg.Asset{
		Kind:        "source_file",
		Description: "Voice implementation",
		Prompts:     []string{"Include comprehensive tests"},
		Targets:     []string{"aggregate.Synth.Voice"},
	}

	return &cuepkg.Registry{
		Project: &cuepkg.Project{Name: "test"},
		Resources: map[string]cuepkg.Resource{
			"aggregate.Synth.Voice": {
				ID: "aggregate.Synth.Voice", Kind: "aggregate", ContextName: "Synth",
				Declaration: aggDecl,
				Dependencies: []cuepkg.Edge{{TargetID: "port.Synth.AudioOutput", Kind: "implements"}},
			},
			"port.Synth.AudioOutput": {
				ID: "port.Synth.AudioOutput", Kind: "port", ContextName: "Synth",
				Declaration: portDecl,
			},
			"domainService.Synth.Mixer": {
				ID: "domainService.Synth.Mixer", Kind: "domainService", ContextName: "Synth",
				Declaration: svcDecl,
				Dependencies: []cuepkg.Edge{{TargetID: "aggregate.Synth.Voice", Kind: "uses"}},
			},
			"repository.Synth.VoicePool": {
				ID: "repository.Synth.VoicePool", Kind: "repository", ContextName: "Synth",
				Declaration: repoDecl,
				Dependencies: []cuepkg.Edge{{TargetID: "aggregate.Synth.Voice", Kind: "of"}},
			},
			"adapter.CoreAudioAdapter": {
				ID: "adapter.CoreAudioAdapter", Kind: "adapter",
				Declaration: adapterDecl,
				Dependencies: []cuepkg.Edge{{TargetID: "port.Synth.AudioOutput", Kind: "implements"}},
			},
			"assetKind.source_file": {
				ID: "assetKind.source_file", Kind: "assetKind",
				Declaration: assetKindDecl,
			},
			"asset.Synth.VoiceImpl": {
				ID: "asset.Synth.VoiceImpl", Kind: "asset", ContextName: "Synth",
				Declaration: assetDecl,
				Dependencies: []cuepkg.Edge{
					{TargetID: "assetKind.source_file", Kind: "uses"},
					{TargetID: "aggregate.Synth.Voice", Kind: "targets"},
				},
			},
		},
	}
}

func TestBuildResourcePrompt_Aggregate(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["aggregate.Synth.Voice"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "aggregate")
	assert.Contains(t, prompt, "Voice")
	assert.Contains(t, prompt, "aggregate.Synth.Voice")
	assert.Contains(t, prompt, "Synth")
	assert.Contains(t, prompt, "Manages a single voice")
	assert.Contains(t, prompt, "NoteOn")
	assert.Contains(t, prompt, "VoiceStarted")
	assert.Contains(t, prompt, "frequency > 0")
	assert.Contains(t, prompt, "port.Synth.AudioOutput")
	assert.Contains(t, prompt, "([]float64) -> error")
}

func TestBuildResourcePrompt_DomainService(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["domainService.Synth.Mixer"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "domainService")
	assert.Contains(t, prompt, "Mixer")
	assert.Contains(t, prompt, "Combines voice outputs")
	assert.Contains(t, prompt, "aggregate.Synth.Voice")
	assert.Contains(t, prompt, "Manages a single voice")
}

func TestBuildResourcePrompt_Adapter(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["adapter.CoreAudioAdapter"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "adapter")
	assert.Contains(t, prompt, "CoreAudioAdapter")
	assert.Contains(t, prompt, "port.Synth.AudioOutput")
	assert.Contains(t, prompt, "([]float64) -> error")
}

func TestBuildResourcePrompt_Repository(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["repository.Synth.VoicePool"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "repository")
	assert.Contains(t, prompt, "VoicePool")
	assert.Contains(t, prompt, "aggregate.Synth.Voice")
}

func TestBuildResourcePrompt_Asset(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["asset.Synth.VoiceImpl"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "Asset")
	assert.Contains(t, prompt, "VoiceImpl")
	assert.Contains(t, prompt, "source_file")
	assert.Contains(t, prompt, "Go source file")
	assert.Contains(t, prompt, "Follow Go conventions")
	assert.Contains(t, prompt, "Voice implementation")
	assert.Contains(t, prompt, "Include comprehensive tests")
	assert.Contains(t, prompt, "aggregate.Synth.Voice")
}

func TestBuildResourcePrompt_NoDependencies(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["port.Synth.AudioOutput"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "port")
	assert.Contains(t, prompt, "AudioOutput")
	assert.NotContains(t, prompt, "## Dependencies")
}
