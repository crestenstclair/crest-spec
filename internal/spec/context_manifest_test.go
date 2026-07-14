package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/require"
)

const verticalSliceContextCUE = `package contexttest

project: {
	name: "context-test"
	mission: "Let a musician request and hear a rendered tone"
	actors: musician: {description: "a musician"}
	goals: hear_tone: {
		description: "hear the requested tone"
		priority: "required"
		actors: ["actor.musician"]
		capabilities: ["capability.render_tone"]
	}
	capabilities: render_tone: {
		description: "render a requested tone"
		goals: ["goal.hear_tone"]
		acceptance: audible: {
			description: "a requested frequency produces an audio frame"
			actor: "actor.musician"
			steps: [{action: "request 440 Hz", observes: "a non-silent frame"}]
			evidence: ["evidence.render_test"]
		}
	}
	requirements: deterministic_render: {
		kind: "nonfunctional"
		description: "the same tone request produces deterministic output"
		goals: ["goal.hear_tone"]
		capabilities: ["capability.render_tone"]
	}
	evidence: render_test: {kind: "validation", description: "the renderer integration test"}
	completion: requiredGoals: ["goal.hear_tone"]
	meta: {language: "go", rules: ["keep rendering deterministic"]}
	contexts: Audio: {
		purpose: "tone rendering"
		valueObjects: Tone: {
			from: "float64"
			contributesTo: [{capability: "capability.render_tone", contribution: "validates the requested frequency"}]
		}
		domainServices: Renderer: {
			purpose: "turn a tone into one audio frame"
			uses: ["valueObject.Audio.Tone"]
			contributesTo: [{capability: "capability.render_tone", contribution: "renders the validated tone"}]
		}
		applicationServices: PlayTone: {
			purpose: "coordinate a tone request"
			uses: ["domainService.Audio.Renderer"]
			operations: {play: {input: {frequency: "float64"}, output: {sample: "float64"}}}
			contributesTo: [{capability: "capability.render_tone", contribution: "connects the request to rendering"}]
		}
	}
}
`

func TestPrepareContextPersistsVerticalSliceWithDependenciesConsumersAndAcceptance(t *testing.T) {
	s, st, sessionID := newVerticalSliceContextSpec(t)
	ctx := context.Background()
	resourceID := "domainService.Audio.Renderer"

	result, err := s.PrepareContext(ctx, ContextOptions{SessionID: sessionID, ResourceID: resourceID})
	require.NoError(t, err)
	require.NotEmpty(t, result.AttemptID)
	require.NotEmpty(t, result.ContextManifestID)
	require.NotEmpty(t, result.ContextHash)
	require.False(t, result.Blocked)
	require.Contains(t, result.Prompt, "Behavioral Acceptance and Evidence")
	require.Contains(t, result.Prompt, "a requested frequency produces an audio frame")
	require.Contains(t, result.Prompt, "Dependency Contract: valueObject.Audio.Tone")
	require.Contains(t, result.Prompt, "Consumer Contract: applicationService.Audio.PlayTone")

	manifest, err := st.GetContextManifest(ctx, result.ContextManifestID)
	require.NoError(t, err)
	require.Equal(t, result.AttemptID, manifest.Attempt.ID)
	require.Equal(t, result.Prompt, manifest.RenderedPrompt)
	require.Equal(t, result.SystemPrompt, manifest.SystemPrompt)
	require.Equal(t, result.ContextHash, manifest.ContextHash)
	require.Equal(t, "resource_implementer", manifest.Attempt.Role)
	require.Equal(t, 1, manifest.Attempt.RetryNumber)
	requireContextSection(t, manifest.Sections, "acceptance", "included")
	requireContextSection(t, manifest.Sections, "dependency_contract", "included")
	requireContextSection(t, manifest.Sections, "consumer_contract", "included")
	requireContextSection(t, manifest.Sections, "call_sites", "omitted")

	resourceState, err := st.GetSessionResource(sessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, "dispatched", resourceState.State)

	second, err := s.PrepareContext(ctx, ContextOptions{SessionID: sessionID, ResourceID: resourceID, Role: "minimal_diff_repair"})
	require.NoError(t, err)
	secondManifest, err := st.GetContextManifestByAttempt(ctx, second.AttemptID)
	require.NoError(t, err)
	require.Equal(t, 2, secondManifest.Attempt.RetryNumber)
	require.Equal(t, result.AttemptID, secondManifest.Attempt.ParentAttemptID)
	require.Equal(t, 24576, secondManifest.BudgetTokens)
}

func TestPrepareContextRecordsMandatoryBudgetBlockWithoutDispatch(t *testing.T) {
	s, st, sessionID := newVerticalSliceContextSpec(t)
	resourceID := "domainService.Audio.Renderer"

	result, err := s.PrepareContext(context.Background(), ContextOptions{
		SessionID: sessionID, ResourceID: resourceID, BudgetTokens: contextmanifest.MinimumBudget,
	})
	require.NoError(t, err)
	require.True(t, result.Blocked)
	require.Contains(t, result.BlockedReason, "mandatory context requires")
	require.Empty(t, result.Prompt)

	manifest, err := st.GetContextManifestByAttempt(context.Background(), result.AttemptID)
	require.NoError(t, err)
	require.True(t, manifest.Blocked)
	require.Equal(t, "context_blocked", manifest.Attempt.Status)
	resourceState, err := st.GetSessionResource(sessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", resourceState.State)
}

func newVerticalSliceContextSpec(t *testing.T) (*Spec, *store.Store, string) {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spec.cue"), []byte(verticalSliceContextCUE), 0o644))
	s := New(st, OSFileSystem{}, &config.Config{SpecDir: dir, GenerateModel: "test-model", MaxRetries: 3})
	begin, err := s.Begin(context.Background(), BeginOpts{})
	require.NoError(t, err)
	require.NotEmpty(t, begin.SessionID)
	return s, st, begin.SessionID
}

func requireContextSection(t *testing.T, sections []store.ContextManifestSection, kind, decision string) {
	t.Helper()
	for _, section := range sections {
		if section.Kind == kind {
			require.Equal(t, decision, section.Decision)
			return
		}
	}
	require.Failf(t, "missing context section", "kind %q not present", kind)
}
