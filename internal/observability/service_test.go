package observability

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/store"
)

func seededService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	ctx := context.Background()
	require.NoError(t, st.ReconcileProjectIntent(ctx, store.ProjectIntentSnapshot{
		ProjectName: "synth", Mission: "let a musician shape and hear a playable voice", SpecHash: "spec-v1",
		Actors: []store.IntentActor{{ID: "actor.musician", Description: "A performing musician"}},
		Goals: []store.IntentGoal{{
			ID: "goal.play", Description: "Play a stable note", Priority: "required", Actors: []string{"actor.musician"},
		}},
		Capabilities: []store.IntentCapability{{ID: "capability.voice", Description: "Render a playable voice", Goals: []string{"goal.play"}}},
		Requirements: []store.IntentRequirement{{
			ID: "requirement.low_latency", Kind: "nonfunctional", Description: "React promptly",
			Goals: []string{"goal.play"}, Capabilities: []string{"capability.voice"},
		}},
		Acceptance: []store.IntentAcceptance{{
			ID: "acceptance.a4", CapabilityID: "capability.voice", ActorID: "actor.musician",
			Description: "A4 can be heard", Evidence: []string{"evidence.audible"},
			Steps: []store.IntentAcceptanceStep{{Action: "play A4", Observes: "audible stable tone"}},
		}},
		Evidence:      []store.IntentEvidence{{ID: "evidence.audible", Kind: "behavioral", Description: "audible witness"}},
		RequiredGoals: []string{"goal.play"},
		ResourceTrace: []store.IntentResourceTrace{
			{ResourceID: "aggregate.Voice", ResourceKind: "aggregate", Contributions: []store.IntentContribution{{CapabilityID: "capability.voice", Description: "owns voice state"}}},
			{ResourceID: "adapter.Audio", ResourceKind: "adapter", Contributions: []store.IntentContribution{{CapabilityID: "capability.voice", Description: "renders samples"}}},
		},
	}))
	base := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, st.SetResource(store.Resource{ID: "aggregate.Voice", Kind: "aggregate", ContextName: "Engine", DeclarationHash: "decl-voice", EffectiveHash: "effective-voice", Model: "model", SettledAt: base}))
	require.NoError(t, st.SetResource(store.Resource{ID: "adapter.Audio", Kind: "adapter", ContextName: "Engine", DeclarationHash: "decl-audio", EffectiveHash: "effective-audio", Model: "model", SettledAt: base.Add(time.Minute)}))
	require.NoError(t, st.SetResource(store.Resource{ID: "endpoint.Play", Kind: "endpoint", ContextName: "Engine", DeclarationHash: "decl-play", EffectiveHash: "effective-play", Model: "model", SettledAt: base.Add(2 * time.Minute)}))
	require.NoError(t, st.SetDependency("adapter.Audio", "aggregate.Voice", "structural"))
	require.NoError(t, st.SetDependency("endpoint.Play", "aggregate.Voice", "structural"))
	require.NoError(t, st.SetGeneratedFile(store.GeneratedFile{
		Path: "src/voice.go", ResourceID: "aggregate.Voice", ContentHash: "content", PromptHash: "prompt", Model: "model", CreatedAt: base,
	}))
	return NewService(st), st
}

func TestProjectAndGoalViewsExplainMissingFunctionality(t *testing.T) {
	service, _ := seededService(t)
	ctx := context.Background()
	project, err := service.Project(ctx, "synth", true)
	require.NoError(t, err)
	assert.Equal(t, APIVersion, project.Version)
	assert.Equal(t, "let a musician shape and hear a playable voice", project.Project.Mission)
	require.Len(t, project.Project.Goals, 1)
	assert.Equal(t, []string{"adapter.Audio", "aggregate.Voice"}, project.Project.Goals[0].Resources)
	assert.Contains(t, project.Status.Missing, "goal.play")
	assert.NotEmpty(t, project.Status.RecommendedNext)
	assert.Equal(t, "/api/v1/goals/goal.play", project.Project.Goals[0].Links[0].HREF)

	goal, err := service.Goal(ctx, "synth", "goal.play")
	require.NoError(t, err)
	require.Len(t, goal.Requirements, 1)
	require.Len(t, goal.Acceptance, 1)
	require.Len(t, goal.Evidence, 1)
	assert.Equal(t, "missing", goal.Evidence[0].Currency)
	assert.Contains(t, goal.Status.Missing, "evidence.audible")
	assert.Equal(t, "actor.musician", goal.Actors[0].ID)
}

func TestResourceViewHasBidirectionalRelationshipsAndStablePagination(t *testing.T) {
	service, _ := seededService(t)
	ctx := context.Background()
	first, err := service.Resources(ctx, "synth", PageRequest{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	assert.True(t, first.Page.HasMore)
	assert.NotEmpty(t, first.Page.NextCursor)
	second, err := service.Resources(ctx, "synth", PageRequest{Limit: 2, Cursor: first.Page.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	assert.False(t, second.Page.HasMore)
	assert.NotEqual(t, first.Items[0].ID, second.Items[0].ID)

	detail, err := service.Resource(ctx, "synth", "aggregate.Voice")
	require.NoError(t, err)
	assert.Equal(t, []string{"goal.play"}, detail.Resource.Goals)
	require.Len(t, detail.Consumers, 2)
	consumerIDs := []string{detail.Consumers[0].ResourceID, detail.Consumers[1].ResourceID}
	assert.ElementsMatch(t, []string{"adapter.Audio", "endpoint.Play"}, consumerIDs)
	require.Len(t, detail.Files, 1)
	assert.Equal(t, "src/voice.go", detail.Files[0].Path)
	assert.Equal(t, "settled", detail.Status.State)
	assert.Contains(t, detail.Links[len(detail.Links)-2].HREF, "contexts?resource_id=")
}

func TestCapabilityViewsPreserveGoalAndResourceTraceability(t *testing.T) {
	service, _ := seededService(t)
	ctx := context.Background()
	page, err := service.Capabilities(ctx, "synth", PageRequest{GoalID: "goal.play"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, []string{"adapter.Audio", "aggregate.Voice"}, page.Items[0].Resources)

	detail, err := service.Capability(ctx, "synth", "capability.voice")
	require.NoError(t, err)
	require.Len(t, detail.Goals, 1)
	require.Len(t, detail.Requirements, 1)
	require.Len(t, detail.Acceptance, 1)
	require.Len(t, detail.Resources, 2)
	assert.Equal(t, "partially_implemented", detail.Status.State)
	assert.Equal(t, "/api/v1/resources?capability=capability.voice", detail.Links[2].HREF)

	resources, err := service.Resources(ctx, "synth", PageRequest{CapabilityID: "capability.voice"})
	require.NoError(t, err)
	require.Len(t, resources.Items, 2)
	goals, err := service.Goals(ctx, "synth", PageRequest{CapabilityID: "capability.voice"})
	require.NoError(t, err)
	require.Len(t, goals.Items, 1)
}

func TestPaginationRejectsInvalidOrUnboundedRequests(t *testing.T) {
	_, err := NormalizePageRequest(PageRequest{Limit: MaximumPageLimit + 1})
	require.Error(t, err)
	_, err = NormalizePageRequest(PageRequest{Cursor: "not-a-cursor"})
	require.Error(t, err)
}
