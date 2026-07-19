package plan

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/graph"
	"github.com/crestenstclair/crest-spec/internal/store"
	projectworkspace "github.com/crestenstclair/crest-spec/internal/workspace"

	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStore struct {
	resources map[string]store.Resource
	files     map[string][]store.GeneratedFile
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		resources: make(map[string]store.Resource),
		files:     make(map[string][]store.GeneratedFile),
	}
}

func (f *fakeStore) GetResource(id string) (*store.Resource, error) {
	r, ok := f.resources[id]
	if !ok {
		return nil, cserrors.ErrNotFound
	}
	return &r, nil
}

func (f *fakeStore) ListResources() ([]store.Resource, error) {
	var out []store.Resource
	for _, r := range f.resources {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) SetResource(resource store.Resource) error {
	f.resources[resource.ID] = resource
	return nil
}

func (f *fakeStore) GetGeneratedFiles(resourceID string) ([]store.GeneratedFile, error) {
	return f.files[resourceID], nil
}

type fakeFS struct {
	files map[string][]byte
}

func newFakeFS() *fakeFS {
	return &fakeFS{files: make(map[string][]byte)}
}

func (f *fakeFS) ReadFile(path string) ([]byte, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, cserrors.ErrNotFound
	}
	return data, nil
}

func buildPlanInputs(resources map[string]cuepkg.Resource) (*cuepkg.Registry, *graph.Graph) {
	reg := &cuepkg.Registry{
		Project:   &cuepkg.Project{Name: "test"},
		Resources: resources,
	}
	for id, resource := range resources {
		resource.VerificationContractHash = reg.VerificationContractForResource(id).Hash()
		resources[id] = resource
	}
	g, _ := graph.Build(resources)
	return reg, g
}

func TestPlan_AllCreates(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate", Declaration: map[string]string{"purpose": "test"}},
		"aggregate.Synth.Osc":   {ID: "aggregate.Synth.Osc", Kind: "aggregate", Declaration: map[string]string{"purpose": "test2"}},
	}
	reg, g := buildPlanInputs(resources)
	p := New(newFakeStore(), newFakeFS())
	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	assert.Len(t, actions, 2)
	for _, a := range actions {
		assert.Equal(t, ActionCreate, a.Kind)
		assert.Equal(t, "new resource", a.Reason)
	}
}

func TestPlan_NoChanges(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate", Declaration: map[string]string{"purpose": "test"}},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, resources, "opus", "default")
	p := New(st, newFakeFS())
	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	assert.Empty(t, actions)
}

func TestPlan_DeclarationChanged(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate", Declaration: map[string]string{"purpose": "updated"}},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	st.resources["aggregate.Synth.Voice"] = store.Resource{
		ID: "aggregate.Synth.Voice", Kind: "aggregate",
		DeclarationHash: "old-hash", EffectiveHash: "old-effective", Model: "opus", SettledAt: time.Now(),
	}
	p := New(st, newFakeFS())
	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, ActionModify, actions[0].Kind)
	assert.Equal(t, "declaration changed", actions[0].Reason)
}

func TestPlan_DependencyCascade(t *testing.T) {
	oldResources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Declaration:  map[string]string{"purpose": "voice"},
			Dependencies: []cuepkg.Edge{{TargetID: "aggregate.Synth.Osc", Kind: "uses"}}},
		"aggregate.Synth.Osc": {ID: "aggregate.Synth.Osc", Kind: "aggregate",
			Declaration: map[string]string{"state": "osc-old"}},
	}
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Declaration:  map[string]string{"purpose": "voice"},
			Dependencies: []cuepkg.Edge{{TargetID: "aggregate.Synth.Osc", Kind: "uses"}}},
		"aggregate.Synth.Osc": {ID: "aggregate.Synth.Osc", Kind: "aggregate",
			Declaration: map[string]string{"state": "osc-updated"}},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, oldResources, "opus", "default")
	p := New(st, newFakeFS())
	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	assert.Len(t, actions, 2)
	actionMap := make(map[string]PlannedAction)
	for _, a := range actions {
		actionMap[a.ResourceID] = a
	}
	oscAction := actionMap["aggregate.Synth.Osc"]
	assert.Equal(t, ActionModify, oscAction.Kind)
	assert.Equal(t, "declaration changed", oscAction.Reason)
	voiceAction := actionMap["aggregate.Synth.Voice"]
	assert.Equal(t, ActionModify, voiceAction.Kind)
	assert.Contains(t, voiceAction.Reason, "dependency compatibility requires validation")
	assert.Equal(t, ExecutionStrategyValidateExisting, voiceAction.ExecutionStrategy)
	assert.Equal(t, "aggregate.Synth.Osc", voiceAction.CascadedFrom)
}

func TestPlan_BehavioralContractChange(t *testing.T) {
	oldResources := planningFrontierResources(cuepkg.ValueObject{From: "f64"})
	oldFrequency := oldResources["valueObject.Synth.Frequency"]
	oldFrequency.BehavioralContractHash = "old-witness"
	oldResources[oldFrequency.ID] = oldFrequency
	resources := planningFrontierResources(cuepkg.ValueObject{From: "f64"})
	frequency := resources["valueObject.Synth.Frequency"]
	frequency.BehavioralContractHash = "new-witness"
	resources[frequency.ID] = frequency
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, oldResources, "opus", "default")

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	assert.Empty(t, actions,
		"witness and acceptance definitions are reconciled as evidence, not dispatched as resource work")
}

func TestPlan_CurrentV2FingerprintAdoptsV3WithoutPlanningEveryResource(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"asset.Synth.Control": {
			ID: "asset.Synth.Control", Kind: "asset",
			Declaration:            map[string]string{"purpose": "control"},
			BehavioralContractHash: "current-acceptance-and-witnesses",
		},
	}
	reg, g := buildPlanInputs(resources)
	currentV2 := graph.ComputeLegacyGenerationInputHashesV2(resources, reg.Project)
	exported := graph.ComputeExportedContractHashes(resources)
	dependencies := graph.ComputeDependencyContractHashes(resources, exported)
	st := newFakeStore()
	st.resources["asset.Synth.Control"] = store.Resource{
		ID: "asset.Synth.Control", Kind: "asset",
		DeclarationHash:        declHash(resources["asset.Synth.Control"].Declaration),
		GenerationInputHash:    currentV2["asset.Synth.Control"],
		ExportedContractHash:   exported["asset.Synth.Control"],
		DependencyContractHash: dependencies["asset.Synth.Control"],
		EffectiveHash:          "settled-v2", SettledAt: time.Now(),
	}

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	assert.Empty(t, actions, "an algorithm version change must not create a whole-project validation or generation plan")
	assert.Equal(t, graph.ComputeGenerationInputHashes(resources, reg.Project)["asset.Synth.Control"],
		st.resources["asset.Synth.Control"].GenerationInputHash)
	assert.Equal(t, graph.ComputeCodeDeclarationHashes(resources)["asset.Synth.Control"],
		st.resources["asset.Synth.Control"].CodeDeclarationHash)
}

func TestPlan_VerificationOnlyLocalDeclarationAndNewResourceRouteIndependently(t *testing.T) {
	oldResources := map[string]cuepkg.Resource{
		"asset.Synth.WitnessOwner": {
			ID: "asset.Synth.WitnessOwner", Kind: "asset",
			Declaration: map[string]string{"purpose": "observe"}, BehavioralContractHash: "witness-v1",
		},
		"asset.Synth.LocalCode": {
			ID: "asset.Synth.LocalCode", Kind: "asset",
			Declaration: map[string]string{"purpose": "old code"},
		},
	}
	resources := map[string]cuepkg.Resource{
		"asset.Synth.WitnessOwner": {
			ID: "asset.Synth.WitnessOwner", Kind: "asset",
			Declaration: map[string]string{"purpose": "observe"}, BehavioralContractHash: "witness-v2",
		},
		"asset.Synth.LocalCode": {
			ID: "asset.Synth.LocalCode", Kind: "asset",
			Declaration: map[string]string{"purpose": "new code"},
		},
		"asset.Synth.NewCode": {
			ID: "asset.Synth.NewCode", Kind: "asset",
			Declaration: map[string]string{"purpose": "brand new"},
		},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, oldResources, "model", "mode")

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	require.Len(t, actions, 2)
	byID := make(map[string]PlannedAction, len(actions))
	for _, action := range actions {
		byID[action.ResourceID] = action
	}
	_, witnessScheduled := byID["asset.Synth.WitnessOwner"]
	assert.False(t, witnessScheduled, "witness observers are not resource generation or validation jobs")
	assert.Equal(t, ExecutionStrategyGenerate, byID["asset.Synth.LocalCode"].ExecutionStrategy)
	assert.Equal(t, "declaration changed", byID["asset.Synth.LocalCode"].Reason)
	assert.Equal(t, ExecutionStrategyGenerate, byID["asset.Synth.NewCode"].ExecutionStrategy)
	assert.Equal(t, ActionCreate, byID["asset.Synth.NewCode"].Kind)
}

func TestPlan_ResourceValidationCommandEditSchedulesOnlyOwnerValidation(t *testing.T) {
	oldValidation := cuepkg.Validation{ID: "validation.frequency", Kind: "custom", Command: []string{"old-check"}}
	newValidation := cuepkg.Validation{ID: "validation.frequency", Kind: "custom", Command: []string{"new-check"}}
	oldResources := map[string]cuepkg.Resource{
		"valueObject.Synth.Frequency": {
			ID: "valueObject.Synth.Frequency", Kind: "valueObject",
			Declaration: cuepkg.ValueObject{From: "f64", Validations: []cuepkg.Validation{oldValidation}},
			Validations: []cuepkg.Validation{oldValidation},
		},
		"aggregate.Synth.Voice": {
			ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Declaration:  cuepkg.Aggregate{State: map[string]string{"frequency": "Frequency"}},
			Dependencies: []cuepkg.Edge{{TargetID: "valueObject.Synth.Frequency", Kind: "uses"}},
		},
	}
	resources := map[string]cuepkg.Resource{
		"valueObject.Synth.Frequency": {
			ID: "valueObject.Synth.Frequency", Kind: "valueObject",
			Declaration: cuepkg.ValueObject{From: "f64", Validations: []cuepkg.Validation{newValidation}},
			Validations: []cuepkg.Validation{newValidation},
		},
		"aggregate.Synth.Voice": oldResources["aggregate.Synth.Voice"],
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, oldResources, "model", "mode")

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "valueObject.Synth.Frequency", actions[0].ResourceID)
	assert.Equal(t, ExecutionStrategyValidateExisting, actions[0].ExecutionStrategy)
	assert.Contains(t, actions[0].Reason, "verification contract")
}

func TestPlan_LegacyValidationOnlyEditNeverGeneratesOrFansOut(t *testing.T) {
	oldValidation := cuepkg.Validation{ID: "validation.frequency", Kind: "custom", Command: []string{"old-check"}}
	newValidation := cuepkg.Validation{ID: "validation.frequency", Kind: "custom", Command: []string{"new-check"}}
	oldResources := map[string]cuepkg.Resource{
		"valueObject.Synth.Frequency": {
			ID: "valueObject.Synth.Frequency", Kind: "valueObject",
			Declaration: cuepkg.ValueObject{From: "f64", Validations: []cuepkg.Validation{oldValidation}},
			Validations: []cuepkg.Validation{oldValidation},
		},
		"aggregate.Synth.Voice": {
			ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Declaration:  cuepkg.Aggregate{State: map[string]string{"frequency": "Frequency"}},
			Dependencies: []cuepkg.Edge{{TargetID: "valueObject.Synth.Frequency", Kind: "uses"}},
		},
	}
	resources := map[string]cuepkg.Resource{
		"valueObject.Synth.Frequency": {
			ID: "valueObject.Synth.Frequency", Kind: "valueObject",
			Declaration: cuepkg.ValueObject{From: "f64", Validations: []cuepkg.Validation{newValidation}},
			Validations: []cuepkg.Validation{newValidation},
		},
		"aggregate.Synth.Voice": oldResources["aggregate.Synth.Voice"],
	}
	reg, g := buildPlanInputs(resources)
	oldProject := &cuepkg.Project{Name: "test"}
	oldGeneration := graph.ComputeLegacyGenerationInputHashesV2(oldResources, oldProject)
	oldExported := graph.ComputeExportedContractHashes(oldResources)
	oldDependencies := graph.ComputeDependencyContractHashes(oldResources, oldExported)
	st := newFakeStore()
	for id, resource := range oldResources {
		st.resources[id] = store.Resource{
			ID: id, Kind: resource.Kind, DeclarationHash: declHash(resource.Declaration),
			GenerationInputHash: oldGeneration[id], ExportedContractHash: oldExported[id],
			DependencyContractHash: oldDependencies[id], EffectiveHash: "legacy", SettledAt: time.Now(),
			// CodeDeclarationHash and VerificationContractHash intentionally empty.
		}
	}

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "valueObject.Synth.Frequency", actions[0].ResourceID)
	assert.Equal(t, ExecutionStrategyValidateExisting, actions[0].ExecutionStrategy)
	assert.Empty(t, st.resources["valueObject.Synth.Frequency"].CodeDeclarationHash,
		"ambiguous legacy provenance remains durable until closure validation")
}

func TestPlan_ProjectPromptAndInheritedMetaDriftValidateWithoutModelWork(t *testing.T) {
	const resourceID = "asset.Synth.Control"
	oldProject := &cuepkg.Project{Name: "test", Mission: "old mission", Meta: cuepkg.Meta{Language: "rust"}}
	newProject := &cuepkg.Project{Name: "test", Mission: "new mission", Meta: cuepkg.Meta{Language: "rust"}}
	oldResources := map[string]cuepkg.Resource{
		resourceID: {
			ID: resourceID, Kind: "asset", Declaration: cuepkg.Asset{Kind: "source"},
			Meta: cuepkg.Meta{Language: "rust", Prompts: []string{"old inherited guidance"}},
		},
	}
	resources := map[string]cuepkg.Resource{
		resourceID: {
			ID: resourceID, Kind: "asset", Declaration: cuepkg.Asset{Kind: "source"},
			Meta: cuepkg.Meta{Language: "rust", Prompts: []string{"new inherited guidance"}},
		},
	}
	reg := &cuepkg.Registry{Project: newProject, Resources: resources}
	g, err := graph.Build(resources)
	require.NoError(t, err)
	oldGeneration := graph.ComputeGenerationInputHashes(oldResources, oldProject)
	oldCode := graph.ComputeCodeDeclarationHashes(oldResources)
	exported := graph.ComputeExportedContractHashes(oldResources)
	dependencies := graph.ComputeDependencyContractHashes(oldResources, exported)
	st := newFakeStore()
	st.resources[resourceID] = store.Resource{
		ID: resourceID, Kind: "asset", DeclarationHash: declHash(oldResources[resourceID].Declaration),
		CodeDeclarationHash: oldCode[resourceID], GenerationInputHash: oldGeneration[resourceID],
		ExportedContractHash: exported[resourceID], DependencyContractHash: dependencies[resourceID],
		EffectiveHash: "settled", SettledAt: time.Now(),
	}

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, ExecutionStrategyValidateExisting, actions[0].ExecutionStrategy)
	assert.Contains(t, actions[0].Reason, "generation fingerprint")
}

func TestPlan_GenerationFingerprintV1UsesValidationBridgeWithoutModelWork(t *testing.T) {
	const resourceID = "aggregate.Synth.Voice"
	resources := map[string]cuepkg.Resource{
		resourceID: {
			ID: resourceID, Kind: "aggregate",
			Declaration:            map[string]string{"state": "voice"},
			BehavioralContractHash: "behavior-v1",
			Validations: []cuepkg.Validation{{
				Kind: "custom", Command: []string{"true"},
			}},
		},
	}
	reg, g := buildPlanInputs(resources)
	legacyV1 := graph.ComputeLegacyGenerationInputHashesV1(resources)
	exported := graph.ComputeExportedContractHashes(resources)
	dependencies := graph.ComputeDependencyContractHashes(resources, exported)
	st := newFakeStore()
	st.resources[resourceID] = store.Resource{
		ID: resourceID, Kind: "aggregate",
		DeclarationHash:        declHash(resources[resourceID].Declaration),
		GenerationInputHash:    legacyV1[resourceID],
		ExportedContractHash:   exported[resourceID],
		DependencyContractHash: dependencies[resourceID],
		EffectiveHash:          "settled-v1",
		SettledAt:              time.Now(),
	}
	st.files[resourceID] = []store.GeneratedFile{{Path: "voice.go", ResourceID: resourceID}}
	fs := newFakeFS()
	fs.files["voice.go"] = []byte("package synth\n")

	actions, err := New(st, fs).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, ExecutionStrategyValidateExisting, actions[0].ExecutionStrategy)
	assert.Equal(t, legacyV1[resourceID], st.resources[resourceID].GenerationInputHash,
		"v1 does not prove the current project/effective-meta prompt and remains unsettled until closure validation")
	assert.Equal(t, graph.ComputeCodeDeclarationHashes(resources)[resourceID],
		st.resources[resourceID].CodeDeclarationHash)
}

func TestPlan_GenerationFingerprintV1BehaviorChangeDoesNotDispatchModelWork(t *testing.T) {
	const resourceID = "aggregate.Synth.Voice"
	oldResources := map[string]cuepkg.Resource{
		resourceID: {
			ID: resourceID, Kind: "aggregate", Declaration: map[string]string{"state": "voice"},
			BehavioralContractHash: "behavior-v1",
		},
	}
	resources := map[string]cuepkg.Resource{
		resourceID: {
			ID: resourceID, Kind: "aggregate", Declaration: map[string]string{"state": "voice"},
			BehavioralContractHash: "behavior-v2",
		},
	}
	reg, g := buildPlanInputs(resources)
	exported := graph.ComputeExportedContractHashes(resources)
	dependencies := graph.ComputeDependencyContractHashes(resources, exported)
	st := newFakeStore()
	st.resources[resourceID] = store.Resource{
		ID: resourceID, Kind: "aggregate",
		DeclarationHash:        declHash(resources[resourceID].Declaration),
		GenerationInputHash:    graph.ComputeLegacyGenerationInputHashesV1(oldResources)[resourceID],
		ExportedContractHash:   exported[resourceID],
		DependencyContractHash: dependencies[resourceID],
		EffectiveHash:          "settled-v1",
		SettledAt:              time.Now(),
	}

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, ExecutionStrategyValidateExisting, actions[0].ExecutionStrategy)
	assert.NotEqual(t, ExecutionStrategyGenerate, actions[0].ExecutionStrategy)
}

func TestPlan_LegacyBehavioralContractUsesEvidenceReconciliationNotGeneration(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"asset.Synth.Control": {
			ID: "asset.Synth.Control", Kind: "asset",
			Declaration:            map[string]string{"purpose": "control"},
			BehavioralContractHash: "unproven-behavior",
		},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	st.resources["asset.Synth.Control"] = store.Resource{
		ID: "asset.Synth.Control", Kind: "asset",
		DeclarationHash: declHash(resources["asset.Synth.Control"].Declaration),
		EffectiveHash:   "legacy-effective", Model: "legacy-model", SettledAt: time.Now(),
	}

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, ExecutionStrategyValidateExisting, actions[0].ExecutionStrategy)
	assert.Empty(t, st.resources["asset.Synth.Control"].GenerationInputHash)
	assert.NotEmpty(t, st.resources["asset.Synth.Control"].CodeDeclarationHash)
}

func TestPlan_LegacyHashesRequireValidationButNeverModelWork(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {
			ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Declaration:  map[string]string{"state": "voice"},
			Dependencies: []cuepkg.Edge{{TargetID: "valueObject.Synth.Frequency", Kind: "uses"}},
		},
		"valueObject.Synth.Frequency": {
			ID: "valueObject.Synth.Frequency", Kind: "valueObject",
			Declaration: cuepkg.ValueObject{From: "f64"},
		},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	for id, resource := range resources {
		st.resources[id] = store.Resource{
			ID: id, Kind: resource.Kind, DeclarationHash: declHash(resource.Declaration),
			EffectiveHash: "legacy-effective-hash", Model: "old-model", SettledAt: time.Now(),
		}
	}

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "new-model", "new-mode")
	require.NoError(t, err)
	require.Len(t, actions, 2)
	for _, action := range actions {
		assert.Equal(t, ExecutionStrategyValidateExisting, action.ExecutionStrategy)
	}
	for id := range resources {
		assert.Empty(t, st.resources[id].GenerationInputHash,
			"legacy rows do not prove the project/effective-meta prompt until validation")
		assert.NotEmpty(t, st.resources[id].CodeDeclarationHash)
		assert.NotEmpty(t, st.resources[id].ExportedContractHash)
		assert.NotEmpty(t, st.resources[id].DependencyContractHash)
	}
}

func TestPlan_ModelAndModeChangesDoNotDirtyResources(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {
			ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Declaration: map[string]string{"state": "voice"},
		},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, resources, "old-model", "old-mode")

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "new-model", "new-mode")
	require.NoError(t, err)
	assert.Empty(t, actions)
}

func TestPlan_GuidanceChangeIsLocalAndDoesNotDirtyDependent(t *testing.T) {
	base := cuepkg.ValueObject{
		From: "f64", Description: "frequency", Meta: cuepkg.Meta{Prompts: []string{"old guidance"}},
	}
	edited := base
	edited.Description = "frequency in hertz"
	edited.Meta.Prompts = []string{"new guidance"}
	oldResources := planningFrontierResources(base)
	resources := planningFrontierResources(edited)
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, oldResources, "model", "mode")

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "valueObject.Synth.Frequency", actions[0].ResourceID)
	assert.Equal(t, "declaration changed", actions[0].Reason)
}

func TestPlan_ExportedContractChangeStopsAtDirtyFrontier(t *testing.T) {
	oldResources := planningFrontierResources(cuepkg.ValueObject{From: "f64"})
	resources := planningFrontierResources(cuepkg.ValueObject{From: "f32"})
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, oldResources, "model", "mode")

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	require.Len(t, actions, 2)
	byResource := make(map[string]PlannedAction, len(actions))
	for _, action := range actions {
		byResource[action.ResourceID] = action
	}
	assert.Equal(t, "declaration changed", byResource["valueObject.Synth.Frequency"].Reason)
	assert.Equal(t, "valueObject.Synth.Frequency", byResource["aggregate.Synth.Voice"].CascadedFrom)
	_, transitiveDirty := byResource["domainService.Synth.Render"]
	assert.False(t, transitiveDirty,
		"a dependent implementation update must not fan out when its own exported contract is unchanged")
}

func TestPlan_ChangedDeclarationDirtiesOnlyDirectDependents(t *testing.T) {
	oldResources := planningFrontierResources(cuepkg.ValueObject{From: "f64"})
	resources := planningFrontierResources(cuepkg.ValueObject{From: "f32"})
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, oldResources, "model", "mode")

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	require.Len(t, actions, 2)
	byResource := make(map[string]PlannedAction, len(actions))
	for _, action := range actions {
		byResource[action.ResourceID] = action
	}
	assert.Equal(t, "declaration changed", byResource["valueObject.Synth.Frequency"].Reason)
	assert.Equal(t, "valueObject.Synth.Frequency", byResource["aggregate.Synth.Voice"].CascadedFrom)
	_, transitiveDirty := byResource["domainService.Synth.Render"]
	assert.False(t, transitiveDirty)
}

func planningFrontierResources(frequency cuepkg.ValueObject) map[string]cuepkg.Resource {
	return map[string]cuepkg.Resource{
		"valueObject.Synth.Frequency": {
			ID: "valueObject.Synth.Frequency", Kind: "valueObject", Declaration: frequency,
		},
		"aggregate.Synth.Voice": {
			ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Declaration:  cuepkg.Aggregate{State: map[string]string{"frequency": "Frequency"}},
			Dependencies: []cuepkg.Edge{{TargetID: "valueObject.Synth.Frequency", Kind: "uses"}},
		},
		"domainService.Synth.Render": {
			ID: "domainService.Synth.Render", Kind: "domainService",
			Declaration:  cuepkg.DomainService{Purpose: "render a voice"},
			Dependencies: []cuepkg.Edge{{TargetID: "aggregate.Synth.Voice", Kind: "uses"}},
		},
	}
}

func seedPlanningSnapshot(st *fakeStore, resources map[string]cuepkg.Resource, model, mode string) {
	g, _ := graph.Build(resources)
	effectiveHashes := graph.ComputeEffectiveHashes(resources, g, model, mode)
	generationInputHashes := graph.ComputeGenerationInputHashes(resources, &cuepkg.Project{Name: "test"})
	codeDeclarationHashes := graph.ComputeCodeDeclarationHashes(resources)
	verificationContractHashes := graph.ComputeVerificationContractHashes(resources)
	exportedContractHashes := graph.ComputeExportedContractHashes(resources)
	dependencyContractHashes := graph.ComputeDependencyContractHashes(resources, exportedContractHashes)
	for id, resource := range resources {
		st.resources[id] = store.Resource{
			ID: id, Kind: resource.Kind, DeclarationHash: declHash(resource.Declaration),
			EffectiveHash: effectiveHashes[id], GenerationInputHash: generationInputHashes[id],
			CodeDeclarationHash:      codeDeclarationHashes[id],
			VerificationContractHash: verificationContractHashes[id],
			ExportedContractHash:     exportedContractHashes[id], Model: model, SettledAt: time.Now(),
			DependencyContractHash: dependencyContractHashes[id],
		}
	}
}

func TestPlan_Destroy(t *testing.T) {
	resources := map[string]cuepkg.Resource{}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	st.resources["aggregate.Synth.Voice"] = store.Resource{
		ID: "aggregate.Synth.Voice", Kind: "aggregate",
		DeclarationHash: "h", EffectiveHash: "e", Model: "opus", SettledAt: time.Now(),
	}
	st.files["aggregate.Synth.Voice"] = []store.GeneratedFile{
		{Path: "src/voice.go", ResourceID: "aggregate.Synth.Voice", ContentHash: "c"},
	}
	p := New(st, newFakeFS())
	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, ActionDestroy, actions[0].Kind)
	assert.Equal(t, "removed from spec", actions[0].Reason)
	assert.Equal(t, []string{"src/voice.go"}, actions[0].Files)
}

func TestPlan_MissingFileRegenerates(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate", Declaration: map[string]string{"purpose": "test"}},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, resources, "opus", "default")
	st.files["aggregate.Synth.Voice"] = []store.GeneratedFile{
		{Path: "src/voice.go", ResourceID: "aggregate.Synth.Voice", ContentHash: "original-content-hash"},
	}
	// File is absent from disk → resource regenerates as a normal modify.
	p := New(st, newFakeFS())
	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, ActionModify, actions[0].Kind)
	assert.Contains(t, actions[0].Reason, "generated file missing")
}

func TestPlan_MissingFileTakesPrecedenceOverDependencyValidation(t *testing.T) {
	oldResources := planningFrontierResources(cuepkg.ValueObject{From: "f64"})
	resources := planningFrontierResources(cuepkg.ValueObject{From: "f32"})
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, oldResources, "model", "mode")
	st.files["aggregate.Synth.Voice"] = []store.GeneratedFile{{
		Path: "src/voice.go", ResourceID: "aggregate.Synth.Voice", ContentHash: "accepted",
	}}

	actions, err := New(st, newFakeFS()).Plan(context.Background(), reg, g, "model", "mode")
	require.NoError(t, err)
	byResource := make(map[string]PlannedAction, len(actions))
	for _, action := range actions {
		byResource[action.ResourceID] = action
	}
	voice := byResource["aggregate.Synth.Voice"]
	assert.Contains(t, voice.Reason, "generated file missing")
	assert.Equal(t, ExecutionStrategyGenerate, voice.ExecutionStrategy)
}

func TestPlan_ModifiedContentIsIgnored(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate", Declaration: map[string]string{"purpose": "test"}},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, resources, "opus", "default")
	st.files["aggregate.Synth.Voice"] = []store.GeneratedFile{
		{Path: "src/voice.go", ResourceID: "aggregate.Synth.Voice", ContentHash: "original-content-hash"},
	}
	fs := newFakeFS()
	fs.files["src/voice.go"] = []byte("modified content on disk")
	p := New(st, fs)
	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	// Hand-edits to generated files are the user's business — no action.
	assert.Empty(t, actions)
}

func TestPlanResolvesPersistedFilesInsideProjectWorkspace(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate", Declaration: map[string]string{"purpose": "test"}},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	seedPlanningSnapshot(st, resources, "opus", "default")
	st.files["aggregate.Synth.Voice"] = []store.GeneratedFile{{
		Path: "src/voice.go", ResourceID: "aggregate.Synth.Voice", ContentHash: "content-hash",
	}}
	root := t.TempDir()
	fs := newFakeFS()
	fs.files[filepath.Join(root, "src", "voice.go")] = []byte("package voice")

	planner := NewInWorkspace(st, fs, projectworkspace.FromRoot(root))
	actions, err := planner.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	assert.Empty(t, actions, "persisted paths must be resolved from the project, not the process working directory")
}

func TestPlan_StructuralKindsExcluded(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"project.test":          {ID: "project.test", Kind: "project", Declaration: map[string]string{"name": "test"}},
		"context.Synth":         {ID: "context.Synth", Kind: "context", Declaration: map[string]string{"purpose": "synth"}},
		"assetKind.source_file": {ID: "assetKind.source_file", Kind: "assetKind", Declaration: map[string]string{"desc": "go file"}},
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate", Declaration: map[string]string{"purpose": "voice"}},
	}
	reg, g := buildPlanInputs(resources)
	p := New(newFakeStore(), newFakeFS())
	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "aggregate.Synth.Voice", actions[0].ResourceID)
}

func TestPlan_DestroysFirst(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate", Declaration: map[string]string{"purpose": "test"}},
	}
	reg, g := buildPlanInputs(resources)
	st := newFakeStore()
	st.resources["aggregate.Synth.Old"] = store.Resource{
		ID: "aggregate.Synth.Old", Kind: "aggregate",
		DeclarationHash: "h", EffectiveHash: "e", Model: "opus", SettledAt: time.Now(),
	}
	p := New(st, newFakeFS())
	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	require.Len(t, actions, 2)
	assert.Equal(t, ActionDestroy, actions[0].Kind)
	assert.Equal(t, ActionCreate, actions[1].Kind)
}

func TestPlan_AmendmentChangesDeclarationHash(t *testing.T) {
	vo := cuepkg.ValueObject{From: "f64", Invariants: []string{"finite"}}
	before := declHashForTest(vo)

	voAmended := vo
	voAmended.Meta.Amendments = []cuepkg.Amendment{{Name: "validate", Prompt: "reject NaN"}}
	after := declHashForTest(voAmended)

	if before == after {
		t.Fatalf("adding an amendment must change the declaration hash (before==after==%s)", before)
	}
}

func declHashForTest(decl any) string { return declHash(decl) }
