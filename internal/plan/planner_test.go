package plan

import (
	"context"
	"testing"
	"time"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/graph"
	"github.com/crestenstclair/crest-spec/internal/store"

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
	hashes := graph.ComputeEffectiveHashes(resources, g, "opus", "default")
	st := newFakeStore()
	st.resources["aggregate.Synth.Voice"] = store.Resource{
		ID: "aggregate.Synth.Voice", Kind: "aggregate",
		DeclarationHash: declHash(resources["aggregate.Synth.Voice"].Declaration),
		EffectiveHash: hashes["aggregate.Synth.Voice"], Model: "opus", SettledAt: time.Now(),
	}
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
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Declaration: map[string]string{"purpose": "voice"},
			Dependencies: []cuepkg.Edge{{TargetID: "aggregate.Synth.Osc", Kind: "uses"}}},
		"aggregate.Synth.Osc": {ID: "aggregate.Synth.Osc", Kind: "aggregate",
			Declaration: map[string]string{"purpose": "osc-updated"}},
	}
	reg, g := buildPlanInputs(resources)
	voiceDeclHash := declHash(resources["aggregate.Synth.Voice"].Declaration)
	st := newFakeStore()
	st.resources["aggregate.Synth.Voice"] = store.Resource{
		ID: "aggregate.Synth.Voice", Kind: "aggregate",
		DeclarationHash: voiceDeclHash, EffectiveHash: "old-effective-hash", Model: "opus", SettledAt: time.Now(),
	}
	st.resources["aggregate.Synth.Osc"] = store.Resource{
		ID: "aggregate.Synth.Osc", Kind: "aggregate",
		DeclarationHash: "old-osc-decl", EffectiveHash: "old-osc-effective", Model: "opus", SettledAt: time.Now(),
	}
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
	assert.Contains(t, voiceAction.Reason, "dependency changed")
	assert.Equal(t, "aggregate.Synth.Osc", voiceAction.CascadedFrom)
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

func TestPlan_DriftDetection(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"aggregate.Synth.Voice": {ID: "aggregate.Synth.Voice", Kind: "aggregate", Declaration: map[string]string{"purpose": "test"}},
	}
	reg, g := buildPlanInputs(resources)
	hashes := graph.ComputeEffectiveHashes(resources, g, "opus", "default")
	st := newFakeStore()
	st.resources["aggregate.Synth.Voice"] = store.Resource{
		ID: "aggregate.Synth.Voice", Kind: "aggregate",
		DeclarationHash: declHash(resources["aggregate.Synth.Voice"].Declaration),
		EffectiveHash: hashes["aggregate.Synth.Voice"], Model: "opus", SettledAt: time.Now(),
	}
	st.files["aggregate.Synth.Voice"] = []store.GeneratedFile{
		{Path: "src/voice.go", ResourceID: "aggregate.Synth.Voice", ContentHash: "original-content-hash"},
	}
	fs := newFakeFS()
	fs.files["src/voice.go"] = []byte("modified content on disk")
	p := New(st, fs)
	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, ActionDrift, actions[0].Kind)
	assert.Contains(t, actions[0].Reason, "file modified on disk")
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
