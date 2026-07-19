package spec

import (
	"strings"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
	"github.com/stretchr/testify/require"
)

func TestTargetAcceptanceContextIncludesExactLinkedWitness(t *testing.T) {
	registry := &cuepkg.Registry{
		Project: &cuepkg.Project{
			Capabilities: map[string]cuepkg.Capability{
				"mix": {
					Description: "mix isolated channels",
					Acceptance: map[string]cuepkg.AcceptanceScenario{
						"isolated": {Description: "editing one channel leaves another unchanged"},
					},
				},
			},
			Witnesses: map[string]cuepkg.Witness{
				"isolation": {
					ID: "witness.isolation", Resources: []string{"domainService.Audio.Mixer"},
					Command: []string{"observe", "real"}, NegativeCommand: []string{"observe", "collapsed"},
					Observation: cuepkg.ObservationDeclaration{
						Kind: "json_stdout", Marker: "OBS ", Schema: map[string]string{"unaffected_unchanged": "bool"},
					},
					Predicates: []cuepkg.WitnessPredicate{{Field: "unaffected_unchanged", Op: "eq", Value: true}},
				},
			},
		},
		ResourceCapabilities: map[string][]string{
			"domainService.Audio.Mixer": {"capability.mix"},
		},
	}
	action := planpkg.PlannedAction{ResourceID: "domainService.Audio.Mixer"}

	context := targetAcceptanceContext(action, registry)
	require.Contains(t, context, "witness.isolation")
	require.Contains(t, context, "unaffected_unchanged")
	require.Contains(t, context, "collapsed")
	require.Contains(t, context, "production paths")
}

func TestRuntimeImplementationContextRequiresEveryOwnedFileInFull(t *testing.T) {
	input := contextCandidateInput{
		resource: cuepkg.Resource{ID: "valueObject.Audio.Parameters"},
		runtime: promptpkg.RuntimeContext{ExistingFiles: map[string]string{
			"src/audio/mod.rs":        "pub mod parameters;",
			"src/audio/parameters.rs": "pub struct Parameters;",
		}},
	}

	candidates := runtimeImplementationContextSource(input)
	require.Len(t, candidates, 2)
	require.NotEqual(t, candidates[0].SourceID, candidates[1].SourceID)
	for _, candidate := range candidates {
		require.Equal(t, "owned_code", candidate.Kind)
		require.True(t, candidate.Mandatory)
		require.False(t, candidate.Truncatable)
		require.Contains(t, candidate.SourceID, candidate.SourcePath)
		require.Contains(t, candidate.Content, "UPDATE MODE")
	}

	selection := contextmanifest.Select(candidates, contextmanifest.MinimumBudget)
	require.False(t, selection.Blocked)
	require.Len(t, selection.Sections, 2)
	for _, section := range selection.Sections {
		require.Equal(t, contextmanifest.Included, section.Decision)
	}
}

func TestRuntimeImplementationContextBlocksInsteadOfTruncatingOwnedFiles(t *testing.T) {
	input := contextCandidateInput{
		resource: cuepkg.Resource{ID: "valueObject.Audio.Parameters"},
		runtime: promptpkg.RuntimeContext{ExistingFiles: map[string]string{
			"src/audio/parameters.rs": strings.Repeat("pub struct Parameter;\n", 400),
		}},
	}

	selection := contextmanifest.Select(runtimeImplementationContextSource(input), contextmanifest.MinimumBudget)
	require.True(t, selection.Blocked)
	require.Contains(t, selection.BlockedReason, "mandatory context requires")
	require.Equal(t, contextmanifest.Omitted, selection.Sections[0].Decision)
}

func TestRuntimeImplementationContextHashesExactModuleConfigurationBytes(t *testing.T) {
	const rawManifest = "\n[package]\nname = \"crest-synth\"\n\n"
	input := contextCandidateInput{
		resource: cuepkg.Resource{ID: "domainService.Audio.Renderer"},
		runtime: promptpkg.RuntimeContext{ModuleFiles: map[string]string{
			"Cargo.toml": rawManifest,
		}},
	}

	selection := contextmanifest.Select(runtimeImplementationContextSource(input), contextmanifest.MinimumBudget)
	require.Len(t, selection.Sections, 1)
	section := selection.Sections[0]
	require.Equal(t, contextmanifest.Included, section.Decision)
	require.Equal(t, rawManifest, section.Content)
	require.Equal(t, contextmanifest.Hash(rawManifest), section.OriginalHash)
}

func TestRuntimeImplementationContextHashesRawOwnedCodeOutsidePresentationEnvelope(t *testing.T) {
	const rawSource = "pub fn render() {}\n"
	input := contextCandidateInput{
		resource: cuepkg.Resource{ID: "domainService.Audio.Renderer"},
		runtime: promptpkg.RuntimeContext{ExistingFiles: map[string]string{
			"src/lib.rs": rawSource,
		}},
	}

	selection := contextmanifest.Select(runtimeImplementationContextSource(input), contextmanifest.MinimumBudget)
	require.Len(t, selection.Sections, 1)
	section := selection.Sections[0]
	require.Equal(t, contextmanifest.Hash(rawSource), section.OriginalHash)
	require.Equal(t, len([]byte(rawSource)), section.OriginalBytes)
	require.Contains(t, section.Content, "UPDATE MODE")
	require.NotEqual(t, section.OriginalHash, section.SelectedHash)
}
