package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReconcileProjectIntentRoundTripAndTombstones(t *testing.T) {
	st, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	snapshot := testProjectIntentSnapshot()
	require.NoError(t, st.ReconcileProjectIntent(context.Background(), snapshot))

	got, err := st.GetProjectIntent(context.Background(), snapshot.ProjectName)
	require.NoError(t, err)
	require.Equal(t, snapshot.ProjectName, got.ProjectName)
	require.Equal(t, snapshot.Mission, got.Mission)
	require.Equal(t, "declared", got.CompletionStatus)
	require.Equal(t, []string{"goal.play_notes"}, got.RequiredGoals)
	require.Len(t, got.Goals, 3)
	require.Equal(t, []string{"actor.musician"}, got.Goals[1].Actors)
	require.Equal(t, []string{"goal.configure_synth"}, got.Goals[1].DependsOn)
	require.Equal(t, []string{"goal.play_notes"}, got.Capabilities[1].Goals)
	require.Equal(t, []IntentAcceptanceStep{{Action: "Press a key", Observes: "A note sounds"}}, got.Acceptance[0].Steps)
	require.Equal(t, []string{"evidence.note_witness"}, got.Acceptance[0].Evidence)
	contributions, err := st.ListResourceContributions(context.Background(), snapshot.ProjectName)
	require.NoError(t, err)
	require.Len(t, contributions, 2)
	require.Equal(t, "adapter.AudioOutput", contributions[0].ResourceID)

	profileRows, err := st.ReadOnlyQuery("SELECT profile_kind, device FROM resource_boundary_profiles WHERE resource_id = 'adapter.AudioOutput'")
	require.NoError(t, err)
	require.Equal(t, "device_output", profileRows[0]["profile_kind"])
	require.Equal(t, "system audio", profileRows[0]["device"])

	// A subsequent spec removes an optional goal. The current projection omits
	// it, while its stable row remains tombstoned for historical references.
	snapshot.SpecHash = "spec-v2"
	snapshot.Goals = snapshot.Goals[:2]
	require.NoError(t, st.ReconcileProjectIntent(context.Background(), snapshot))
	got, err = st.GetProjectIntent(context.Background(), snapshot.ProjectName)
	require.NoError(t, err)
	require.Len(t, got.Goals, 2)

	rows, err := st.ReadOnlyQuery(`SELECT active FROM project_goals WHERE id = 'goal.optional_export'`)
	require.NoError(t, err)
	require.Equal(t, int64(0), rows[0]["active"])

	foreignKeyRows, err := st.sqlDB.Query("PRAGMA foreign_key_check")
	require.NoError(t, err)
	require.False(t, foreignKeyRows.Next())
	require.NoError(t, foreignKeyRows.Close())
}

func TestReconcileProjectIntentRollsBackInvalidRelationship(t *testing.T) {
	st, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	snapshot := testProjectIntentSnapshot()
	require.NoError(t, st.ReconcileProjectIntent(context.Background(), snapshot))

	invalid := snapshot
	invalid.Mission = "This must not be committed"
	invalid.SpecHash = "invalid-spec"
	invalid.Capabilities = append([]IntentCapability(nil), snapshot.Capabilities...)
	invalid.Capabilities[0].Goals = []string{"goal.does_not_exist"}
	err = st.ReconcileProjectIntent(context.Background(), invalid)
	require.ErrorContains(t, err, "link capability goal")
	require.ErrorContains(t, err, "FOREIGN KEY constraint failed")

	got, err := st.GetProjectIntent(context.Background(), snapshot.ProjectName)
	require.NoError(t, err)
	require.Equal(t, snapshot.Mission, got.Mission)
	require.Equal(t, snapshot.SpecHash, got.SpecHash)
	require.Equal(t, []string{"goal.configure_synth"}, got.Capabilities[0].Goals)
}

func testProjectIntentSnapshot() ProjectIntentSnapshot {
	return ProjectIntentSnapshot{
		ProjectName: "crest-synth",
		Mission:     "Let musicians play a software synthesizer.",
		SpecHash:    "spec-v1",
		Actors: []IntentActor{
			{ID: "actor.musician", Description: "A musician"},
		},
		Goals: []IntentGoal{
			{ID: "goal.configure_synth", Description: "Configure the synthesizer", Priority: "required", Actors: []string{"actor.musician"}},
			{ID: "goal.play_notes", Description: "Play notes", Priority: "required", Actors: []string{"actor.musician"}, DependsOn: []string{"goal.configure_synth"}},
			{ID: "goal.optional_export", Description: "Export audio", Priority: "optional"},
		},
		Capabilities: []IntentCapability{
			{ID: "capability.configure", Description: "Configure the synth", Goals: []string{"goal.configure_synth"}},
			{ID: "capability.play", Description: "Play a note", Goals: []string{"goal.play_notes"}},
		},
		Requirements: []IntentRequirement{
			{ID: "requirement.low_latency", Kind: "nonfunctional", Description: "Audio stays responsive", Goals: []string{"goal.play_notes"}, Capabilities: []string{"capability.play"}},
		},
		Acceptance: []IntentAcceptance{
			{ID: "acceptance.play.press_key", CapabilityID: "capability.play", ActorID: "actor.musician", Description: "A key produces sound", Steps: []IntentAcceptanceStep{{Action: "Press a key", Observes: "A note sounds"}}, Evidence: []string{"evidence.note_witness"}},
		},
		Evidence: []IntentEvidence{
			{ID: "evidence.note_witness", Kind: "behavioral_witness", Description: "Captured note output"},
		},
		NonGoals: []IntentNonGoal{
			{ID: "non_goal.cloud_hosting", Description: "Cloud hosting is excluded"},
		},
		RequiredGoals: []string{"goal.play_notes"},
		ResourceTrace: []IntentResourceTrace{
			{ResourceID: "aggregate.Engine.Voice", ResourceKind: "aggregate", Contributions: []IntentContribution{{CapabilityID: "capability.play", Description: "owns the sounding voice lifecycle"}}},
			{ResourceID: "adapter.AudioOutput", ResourceKind: "adapter", Contributions: []IntentContribution{{CapabilityID: "capability.play", Description: "delivers samples to the device"}}, Boundary: &IntentBoundaryProfile{Kind: "device_output", Device: "system audio"}},
			{ResourceID: "asset.Guide", ResourceKind: "asset", Asset: &IntentAssetProfile{Kind: "documentation", Audience: "musicians", RequiredExamples: []string{"play a note"}}},
		},
	}
}
