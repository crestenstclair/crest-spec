package evolve

import (
	"context"
	"errors"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/store"
)

// fakeGenerator is a test double for the Generator interface.
type fakeGenerator struct {
	output string
	err    error
	calls  int
}

func (f *fakeGenerator) Generate(_ context.Context, _, _ string) (string, error) {
	f.calls++
	return f.output, f.err
}

// fakeStore is a test double for the Store interface. It records created
// learnings and returns canned read data.
type fakeStore struct {
	sessionResources []store.SessionResource
	resources        map[string]*store.Resource
	generations      map[string][]store.Generation
	invariantChecks  []store.InvariantCheck
	existing         []store.Learning

	created []store.Learning
}

func (f *fakeStore) ListSessionResources(string) ([]store.SessionResource, error) {
	return f.sessionResources, nil
}

func (f *fakeStore) ListSessionResourcesByWave(string, int) ([]store.SessionResource, error) {
	return f.sessionResources, nil
}

func (f *fakeStore) GetResource(id string) (*store.Resource, error) {
	if r, ok := f.resources[id]; ok {
		return r, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeStore) ListGenerations(resourceID string, _ int) ([]store.Generation, error) {
	return f.generations[resourceID], nil
}

func (f *fakeStore) ListInvariantChecks(string) ([]store.InvariantCheck, error) {
	return f.invariantChecks, nil
}

func (f *fakeStore) ListLearnings(string) ([]store.Learning, error) {
	return f.existing, nil
}

func (f *fakeStore) CreateLearning(l store.Learning) error {
	f.created = append(f.created, l)
	return nil
}

// storeWithFailureSignal returns a fakeStore whose single resource carries a
// rejection so reflection has something to extract from.
func storeWithFailureSignal() *fakeStore {
	return &fakeStore{
		sessionResources: []store.SessionResource{
			{SessionID: "s1", ResourceID: "r1", WaveIndex: 0, LastError: "build failed"},
		},
		resources: map[string]*store.Resource{
			"r1": {ID: "r1", Kind: "adapter"},
		},
		generations: map[string][]store.Generation{
			"r1": {{ID: "g1", ResourceID: "r1", RejectionReason: "try_send dropped frames"}},
		},
	}
}

func TestReflectSession_ParsesMultipleLearnings(t *testing.T) {
	gen := &fakeGenerator{output: learningsBegin + "\n" + `[
		{"scope_lang": "rust", "scope_kind": "adapter", "text": "prefer blocking send over try_send", "rationale": "avoids dropped frames", "confidence": 0.9},
		{"scope_lang": "rust", "scope_kind": "aggregate", "text": "derive PartialEq manually for NaN-carrying f32 types", "rationale": "auto-derive is wrong for NaN", "confidence": 0.8}
	]` + "\n" + learningsEnd}

	st := storeWithFailureSignal()
	r := New(gen, st, "")

	added, err := r.ReflectSession(context.Background(), "s1", "a1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if added != 2 {
		t.Fatalf("expected 2 learnings added, got %d", added)
	}
	if len(st.created) != 2 {
		t.Fatalf("expected 2 CreateLearning calls, got %d", len(st.created))
	}

	first := st.created[0]
	if first.ScopeLang != "rust" || first.ScopeKind != "adapter" {
		t.Errorf("unexpected scope on first learning: lang=%q kind=%q", first.ScopeLang, first.ScopeKind)
	}
	if first.Confidence != 0.9 {
		t.Errorf("expected confidence 0.9, got %v", first.Confidence)
	}
	if first.Status != "active" {
		t.Errorf("expected status active, got %q", first.Status)
	}
	if first.SourceApplyID != "a1" {
		t.Errorf("expected source apply id a1, got %q", first.SourceApplyID)
	}
	if first.ID == "" {
		t.Error("expected a generated learning ID")
	}
	if st.created[1].ScopeKind != "aggregate" {
		t.Errorf("expected second learning kind aggregate, got %q", st.created[1].ScopeKind)
	}
}

func TestReflectSession_GeneratorError_NonBlocking(t *testing.T) {
	gen := &fakeGenerator{err: errors.New("model exploded")}
	st := storeWithFailureSignal()
	r := New(gen, st, "")

	added, err := r.ReflectSession(context.Background(), "s1", "a1")
	if err != nil {
		t.Fatalf("reflection must never propagate an error, got: %v", err)
	}
	if added != 0 {
		t.Fatalf("expected 0 added on generator error, got %d", added)
	}
	if len(st.created) != 0 {
		t.Fatalf("expected nothing created on generator error, got %d", len(st.created))
	}
}

func TestReflectSession_UnparseableOutput_NoError(t *testing.T) {
	gen := &fakeGenerator{output: "I could not produce structured output, sorry."}
	st := storeWithFailureSignal()
	r := New(gen, st, "")

	added, err := r.ReflectSession(context.Background(), "s1", "a1")
	if err != nil {
		t.Fatalf("unparseable output must not error, got: %v", err)
	}
	if added != 0 {
		t.Fatalf("expected 0 added on unparseable output, got %d", added)
	}
	if len(st.created) != 0 {
		t.Fatalf("expected nothing created on unparseable output, got %d", len(st.created))
	}
}

func TestReflectSession_SkipsDuplicateOfExisting(t *testing.T) {
	const dupText = "prefer blocking send over try_send"
	gen := &fakeGenerator{output: learningsBegin + "\n" + `[
		{"scope_lang": "rust", "scope_kind": "adapter", "text": "Prefer blocking send over try_send", "confidence": 0.9},
		{"scope_lang": "rust", "scope_kind": "adapter", "text": "use checked arithmetic in financial code", "confidence": 0.7}
	]` + "\n" + learningsEnd}

	st := storeWithFailureSignal()
	st.existing = []store.Learning{
		{ID: "L1", ScopeLang: "rust", ScopeKind: "adapter", Text: dupText, Status: "active"},
	}
	r := New(gen, st, "")

	added, err := r.ReflectSession(context.Background(), "s1", "a1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if added != 1 {
		t.Fatalf("expected 1 added (duplicate skipped), got %d", added)
	}
	if len(st.created) != 1 {
		t.Fatalf("expected 1 CreateLearning call, got %d", len(st.created))
	}
	if st.created[0].Text != "use checked arithmetic in financial code" {
		t.Errorf("expected the non-duplicate learning, got %q", st.created[0].Text)
	}
}

func TestReflect_NoFailureSignal_SkipsGenerator(t *testing.T) {
	gen := &fakeGenerator{output: learningsBegin + "\n[]\n" + learningsEnd}
	st := &fakeStore{
		sessionResources: []store.SessionResource{
			{SessionID: "s1", ResourceID: "r1"}, // no last error, no rejections
		},
		resources: map[string]*store.Resource{"r1": {ID: "r1", Kind: "adapter"}},
	}
	r := New(gen, st, "")

	added, err := r.ReflectSession(context.Background(), "s1", "a1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if added != 0 {
		t.Fatalf("expected 0 added with no signal, got %d", added)
	}
	if gen.calls != 0 {
		t.Fatalf("expected generator NOT called with no failure signal, got %d calls", gen.calls)
	}
}

func TestReflectWave_UsesWaveSignal(t *testing.T) {
	gen := &fakeGenerator{output: learningsBegin + "\n" + `[
		{"scope_lang": "rust", "scope_kind": "adapter", "text": "validate input lengths", "confidence": 0.6}
	]` + "\n" + learningsEnd}
	st := storeWithFailureSignal()
	r := New(gen, st, "")

	added, err := r.ReflectWave(context.Background(), "s1", "a1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if added != 1 {
		t.Fatalf("expected 1 added, got %d", added)
	}
}

func TestNew_DefaultsModelToSonnet(t *testing.T) {
	r := New(&fakeGenerator{}, &fakeStore{}, "")
	if r.model != "claude-sonnet-4-6" {
		t.Fatalf("expected default model claude-sonnet-4-6, got %q", r.model)
	}
}
