package spec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
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
	require.Equal(t, "resource_implementer", result.Role)
	require.Equal(t, "resource_implementer", result.RecommendedRole)
	require.Equal(t, "role-policy-v1", result.RolePolicyVersion)
	require.Equal(t, "resource-implementation-v1", result.ContextPolicy)
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

	require.NoError(t, st.UpdateSessionResourceState(sessionID, resourceID, "rejected", "go test: renderer returned silence", "", 1, ""))
	second, err := s.PrepareContext(ctx, ContextOptions{SessionID: sessionID, ResourceID: resourceID, Role: "minimal_diff_repair"})
	require.NoError(t, err)
	secondManifest, err := st.GetContextManifestByAttempt(ctx, second.AttemptID)
	require.NoError(t, err)
	require.Equal(t, 2, secondManifest.Attempt.RetryNumber)
	require.Equal(t, result.AttemptID, secondManifest.Attempt.ParentAttemptID)
	require.Equal(t, 24576, secondManifest.BudgetTokens)

	comparison, err := s.CompareContexts(ctx, result.ContextManifestID, second.ContextManifestID)
	require.NoError(t, err)
	require.True(t, comparison.BudgetChanged)
	require.False(t, comparison.SameContext)
	requireAddedContextKind(t, comparison.AddedSections, "previous_failure")

	summaries, err := s.ListContextAttempts(ctx, 10)
	require.NoError(t, err)
	require.Len(t, summaries, 2)
	require.Equal(t, second.ContextManifestID, summaries[0].ID)
}

func TestPrepareContextRejectsUnknownRoleBeforeDispatch(t *testing.T) {
	s, st, sessionID := newVerticalSliceContextSpec(t)
	resourceID := "domainService.Audio.Renderer"

	_, err := s.PrepareContext(context.Background(), ContextOptions{
		SessionID: sessionID, ResourceID: resourceID, Role: "universal_coding_agent",
	})
	require.ErrorContains(t, err, `unsupported agent role "universal_coding_agent"`)

	resourceState, stateErr := st.GetSessionResource(sessionID, resourceID)
	require.NoError(t, stateErr)
	require.Equal(t, "pending", resourceState.State)
	manifests, listErr := st.ListContextManifests(context.Background(), 10)
	require.NoError(t, listErr)
	require.Empty(t, manifests)
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

func TestReferenceContextRejectsOutsideAndSecretBearingPaths(t *testing.T) {
	projectRoot := t.TempDir()
	specDir := filepath.Join(projectRoot, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "AGENTS.md"), []byte("repository guidance"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, ".env"), []byte("TOKEN=do-not-capture"), 0o600))
	s := &Spec{fs: OSFileSystem{}, cfg: &config.Config{SpecDir: specDir}}

	candidates := s.referenceCandidates([]string{"AGENTS.md", ".env", "../outside.txt"})
	require.Len(t, candidates, 3)
	require.Equal(t, "repository guidance", candidates[0].Content)
	require.Empty(t, candidates[0].UnavailableReason)
	require.Contains(t, candidates[1].UnavailableReason, "secrets")
	require.Empty(t, candidates[1].Content)
	require.Contains(t, candidates[2].UnavailableReason, "outside the project root")
}

func TestContextSourcesExposeAvailableAndUnavailableDecisions(t *testing.T) {
	s, _, _ := newVerticalSliceContextSpec(t)
	ctx := context.Background()
	planResult, err := s.Plan(ctx)
	require.NoError(t, err)
	action, ok := contextPlanAction(planResult.Actions, "domainService.Audio.Renderer")
	require.True(t, ok)
	resource := planResult.Registry.Resources[action.ResourceID]
	input := contextCandidateInput{
		action: action, resource: resource, registry: planResult.Registry,
		runtime: promptpkg.RuntimeContext{}, runtimeErr: errors.New("runtime probe failed"),
		systemPrompt:   promptpkg.BuildSystemPrompt(planResult.Registry.Project),
		resourcePrompt: promptpkg.BuildResourcePrompt(resource, planResult.Registry),
		projectStatus:  s.contextProjectStatus(ctx, action, planResult.Registry),
	}

	core := coreContextSource(input)
	require.Equal(t, []string{"project_goal", "task", "resource_contract", "system_instructions"}, contextCandidateKinds(core))
	for _, candidate := range core {
		require.True(t, candidate.Mandatory)
		require.NotEmpty(t, candidate.Content)
		require.Empty(t, candidate.UnavailableReason)
	}

	acceptance := acceptanceContextSource(input)
	require.Len(t, acceptance, 1)
	require.True(t, acceptance[0].Mandatory)
	require.Contains(t, acceptance[0].Content, "a requested frequency produces an audio frame")

	withoutAcceptance := input
	withoutAcceptance.action.Capabilities = nil
	unavailableAcceptance := acceptanceContextSource(withoutAcceptance)
	require.Len(t, unavailableAcceptance, 1)
	require.Empty(t, unavailableAcceptance[0].Content)
	require.Contains(t, unavailableAcceptance[0].UnavailableReason, "no acceptance scenario")

	architecture := s.architectureContextSource(input)
	require.Equal(t, []string{"integration_contract", "dependency_contract", "consumer_contract"}, contextCandidateKinds(architecture))
	decisions := selectorDecisionContextSource(input)
	require.Equal(t, []string{"runtime_discovery", "call_sites", "recent_diff"}, contextCandidateKinds(decisions))
	for _, decision := range decisions {
		require.Empty(t, decision.Content)
		require.NotEmpty(t, decision.UnavailableReason)
	}

	all := s.contextCandidates(input)
	require.Equal(t, contextCandidateKinds(core), contextCandidateKinds(all[:len(core)]))
	require.Equal(t, contextCandidateKinds(decisions), contextCandidateKinds(all[len(all)-len(decisions):]))
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

func requireAddedContextKind(t *testing.T, changes []ContextSectionChange, kind string) {
	t.Helper()
	for _, change := range changes {
		if change.After != nil && change.After.Kind == kind {
			return
		}
	}
	require.Failf(t, "missing added context section", "kind %q not present", kind)
}

func contextCandidateKinds(candidates []contextmanifest.Candidate) []string {
	kinds := make([]string, len(candidates))
	for index, candidate := range candidates {
		kinds[index] = candidate.Kind
	}
	return kinds
}
