package store_test

import (
	"path/filepath"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/store"
)

func TestUpdateGenerationOutcomePersists(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateApply("a1", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGeneration(store.Generation{ID: "g1", ApplyID: "a1", ResourceID: "r1", Model: "sonnet"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateGeneration("g1", "", "accepted", "", 0, 0, 0, 0); err != nil {
		t.Fatalf("update: %v", err)
	}
	rows, err := s.ReadOnlyQuery("SELECT outcome FROM generations WHERE id = 'g1'")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("raw outcome: %#v", rows[0]["outcome"])
	if rows[0]["outcome"] != "accepted" {
		t.Fatalf("outcome not persisted: %#v", rows[0]["outcome"])
	}
}
