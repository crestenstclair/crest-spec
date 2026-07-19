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
	require.Equal(t, "pending", resourceState.State)
	require.Empty(t, resourceState.JobID)

	_, err = s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: result.DispatchID,
		SessionID: sessionID, ResourceID: resourceID,
		HostSessionID: "host-context-test", WorkerID: "worker-1",
	})
	require.NoError(t, err)
	resourceState, err = st.GetSessionResource(sessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, "dispatched", resourceState.State)
	require.Equal(t, result.AttemptID, resourceState.JobID)

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
	lifecycles, lifecycleErr := st.ListDispatchLifecyclesBySession(context.Background(), sessionID)
	require.NoError(t, lifecycleErr)
	require.Empty(t, lifecycles, "invalid role must not leave an orphan dispatch lifecycle")
}

func TestPrepareContextResponseLossReplaysUnclaimedAttempt(t *testing.T) {
	s, st, sessionID := newVerticalSliceContextSpec(t)
	ctx := context.Background()
	resourceID := "domainService.Audio.Renderer"

	first, err := s.PrepareContext(ctx, ContextOptions{SessionID: sessionID, ResourceID: resourceID})
	require.NoError(t, err)
	second, err := s.PrepareContext(ctx, ContextOptions{SessionID: sessionID, ResourceID: resourceID})
	require.NoError(t, err)
	require.Equal(t, first.AttemptID, second.AttemptID)
	require.Equal(t, first.DispatchID, second.DispatchID)
	_, err = s.PrepareContext(ctx, ContextOptions{
		SessionID: sessionID, ResourceID: resourceID, Role: "minimal_diff_repair",
	})
	require.ErrorContains(t, err, "already has unclaimed prepared attempt")

	attempts, err := st.ListGenerationAttemptsBySession(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	lifecycles, err := st.ListDispatchLifecyclesBySession(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, lifecycles, 1, "a rejected duplicate claim must not create an orphan lifecycle")
	resourceState, err := st.GetSessionResource(sessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", resourceState.State)
	require.Empty(t, resourceState.JobID)
}

func TestPrepareWaveContextsBoundsUnclaimedWorkAndDispatchClaimsByAttempt(t *testing.T) {
	s, st, sessionID := newVerticalSliceContextSpec(t)
	ctx := context.Background()
	wave, err := s.Next(ctx, sessionID)
	require.NoError(t, err)
	require.False(t, wave.Done)
	require.NotEmpty(t, wave.Resources)

	batch, err := s.PrepareWaveContexts(ctx, WaveContextOptions{SessionID: sessionID, Limit: len(wave.Resources)})
	require.NoError(t, err)
	require.Equal(t, wave.WaveIndex, batch.WaveIndex)
	require.False(t, batch.Done)
	require.False(t, batch.ValidationReady)
	require.Equal(t, -1, batch.ValidationWaveIndex)
	require.Len(t, batch.Contexts, len(wave.Resources))
	for index, item := range batch.Contexts {
		require.Equal(t, wave.Resources[index].ResourceID, item.ResourceID)
		require.Empty(t, item.Error)
		require.NotNil(t, item.Context)
		require.NotEmpty(t, item.Context.AttemptID)
		require.NotEmpty(t, item.Context.DispatchID)
		state, stateErr := st.GetSessionResource(sessionID, item.ResourceID)
		require.NoError(t, stateErr)
		require.Equal(t, "pending", state.State)
		require.Empty(t, state.JobID)
	}
	lifecycles, err := st.ListDispatchLifecyclesBySession(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, lifecycles, len(wave.Resources))
	for index := 1; index < len(lifecycles); index++ {
		require.NotNil(t, lifecycles[index-1].ContextPreparedAt)
		require.NotNil(t, lifecycles[index].ContextRequestedAt)
		require.False(t, lifecycles[index].ContextRequestedAt.Before(*lifecycles[index-1].ContextPreparedAt),
			"resource context lifecycles must represent serial preparation after the shared batch")
	}
	preparations, err := st.ListContextBatchesBySession(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, preparations, 1)
	require.Equal(t, "completed", preparations[0].Status)
	require.Equal(t, wave.WaveIndex, preparations[0].WaveIndex)
	require.NotNil(t, preparations[0].CompletedAt)

	second, err := s.PrepareWaveContexts(ctx, WaveContextOptions{SessionID: sessionID, Limit: len(wave.Resources)})
	require.NoError(t, err)
	require.Len(t, second.Contexts, len(wave.Resources), "unclaimed contexts are replayed after response loss")
	for index := range batch.Contexts {
		require.Equal(t, batch.Contexts[index].Context.AttemptID, second.Contexts[index].Context.AttemptID)
	}
	attempts, err := st.ListGenerationAttemptsBySession(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, attempts, len(wave.Resources))
	preparations, err = st.ListContextBatchesBySession(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, preparations, 1, "polling unclaimed prepared contexts does not repeat shared preparation")

	assigned := batch.Contexts[0]
	requested, err := s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: assigned.Context.DispatchID,
		SessionID: sessionID, ResourceID: assigned.ResourceID,
		HostSessionID: "host-batch-test", WorkerID: "worker-1",
	})
	require.NoError(t, err)
	require.Equal(t, assigned.Context.AttemptID, requested.AttemptID)
	state, err := st.GetSessionResource(sessionID, assigned.ResourceID)
	require.NoError(t, err)
	require.Equal(t, "dispatched", state.State)
	require.Equal(t, assigned.Context.AttemptID, state.JobID)
}

func TestPrepareWaveContextsDoesNotChargeLegacyCounterWithoutExecution(t *testing.T) {
	const parallelResources = `package crestsynth

project: name: "batch"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "represents tone"}]
}
project: contexts: Audio: valueObjects: Gain: {
	from: "f64"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "represents gain"}]
}
`
	s, session := newTestSpecWithSessionCUE(t, parallelResources)
	ctx := context.Background()
	const exhausted = "valueObject.Audio.Gain"
	require.NoError(t, s.store.UpdateSessionResourceState(
		session.sessionID, exhausted, "rejected", "prior attempts exhausted", "", 3, "",
	))

	batch, err := s.PrepareWaveContexts(ctx, WaveContextOptions{SessionID: session.sessionID, Limit: 2})
	require.NoError(t, err)
	require.Len(t, batch.Contexts, 2)
	items := make(map[string]WaveContextItem, len(batch.Contexts))
	for _, item := range batch.Contexts {
		items[item.ResourceID] = item
	}
	require.Empty(t, items[exhausted].Error)
	require.NotNil(t, items[exhausted].Context,
		"retry budget advances on registered executions, not a stale resource counter")
	prepared := items["valueObject.Audio.Tone"]
	require.Empty(t, prepared.Error)
	require.NotNil(t, prepared.Context)
	require.NotEmpty(t, prepared.Context.AttemptID)
	state, err := s.store.GetSessionResource(session.sessionID, prepared.ResourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", state.State)
	require.Empty(t, state.JobID)
}

func TestPrepareWaveContextsLimitPreventsWholeWavePrequeue(t *testing.T) {
	const parallelResources = `package crestsynth

project: name: "bounded-batch"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "represents tone"}]
}
project: contexts: Audio: valueObjects: Gain: {
	from: "f64"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "represents gain"}]
}
`
	s, session := newTestSpecWithSessionCUE(t, parallelResources)
	ctx := context.Background()
	first, err := s.PrepareWaveContexts(ctx, WaveContextOptions{
		SessionID: session.sessionID, Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, first.Contexts, 1)
	attempts, err := s.store.ListGenerationAttemptsBySession(ctx, session.sessionID)
	require.NoError(t, err)
	require.Len(t, attempts, 1, "wave width must not determine prepared queue width")

	replayed, err := s.PrepareWaveContexts(ctx, WaveContextOptions{
		SessionID: session.sessionID, Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, replayed.Contexts, 1)
	require.Equal(t, first.Contexts[0].Context.AttemptID, replayed.Contexts[0].Context.AttemptID)
	attempts, err = s.store.ListGenerationAttemptsBySession(ctx, session.sessionID)
	require.NoError(t, err)
	require.Len(t, attempts, 1, "duplicate polling must replay rather than prepare the rest of the wave")
}

func TestPrepareWaveContextsSignalsFinalValidationWithoutDummyWorker(t *testing.T) {
	s, st, sessionID := newVerticalSliceContextSpec(t)
	resources, err := st.ListSessionResources(sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, resources)
	gateWave := -1
	for _, resource := range resources {
		if resource.WaveIndex > gateWave {
			gateWave = resource.WaveIndex
		}
		require.NoError(t, st.UpdateSessionResourceState(
			sessionID, resource.ResourceID, "committed", "", "", resource.Attempts, resource.JobID,
		))
	}

	batch, err := s.PrepareWaveContexts(context.Background(), WaveContextOptions{SessionID: sessionID})
	require.NoError(t, err)
	require.False(t, batch.Done, "validation is an explicit queue transition before done")
	require.Empty(t, batch.Contexts)
	require.True(t, batch.ValidationReady)
	require.Equal(t, gateWave, batch.ValidationWaveIndex)
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

	refreshed, err := s.PrepareContext(context.Background(), ContextOptions{
		SessionID: sessionID, ResourceID: resourceID,
	})
	require.NoError(t, err)
	require.NotEqual(t, result.AttemptID, refreshed.AttemptID,
		"a context_blocked audit record must not occupy the unclaimed dispatch queue")
	require.False(t, refreshed.Blocked)
}

func TestDispatchRequestedInvalidatesStalePreparedContextWithoutClaimOrRetry(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	const outputPath = "generated/stale-before-dispatch.go"
	projectPath := filepath.Join(filepath.Dir(s.cfg.SpecDir), outputPath)
	_, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID, []CommitFile{{
		Path: outputPath, Content: "package generated\n\nconst Value = 1\n",
	}}, "", "test-model")
	require.NoError(t, err)
	_, err = s.store.ReopenSessionResources(ctx, state.sessionID, []string{state.resourceID}, "repair requested")
	require.NoError(t, err)

	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(projectPath, []byte("package generated\n\nconst Value = 2\n"), 0o644))
	_, err = s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: prepared.DispatchID,
		SessionID: state.sessionID, ResourceID: state.resourceID,
		HostSessionID: "host-stale-test", WorkerID: "worker-1",
	})
	require.ErrorContains(t, err, "rebuild_context")

	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", resource.State)
	require.Empty(t, resource.JobID)
	attempts, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)
	require.Equal(t, "abandoned", attempts[len(attempts)-1].Status)

	refreshed, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	require.NotEqual(t, prepared.AttemptID, refreshed.AttemptID)
	currentBytes := "package generated\n\nconst Value = 2\n"
	foundCurrentSource := false
	for _, section := range refreshed.Sections {
		if section.SourceKind == "repository_file" && section.SourcePath == outputPath &&
			section.Decision != contextmanifest.Omitted {
			require.Equal(t, contextmanifest.Hash(currentBytes), section.OriginalHash)
			foundCurrentSource = true
		}
	}
	require.True(t, foundCurrentSource, "replacement context must snapshot the current repository bytes")
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
	withoutAcceptance.action.ResourceID = "asset.Unlinked"
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
