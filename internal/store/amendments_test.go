package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore_AmendmentRoundTrip(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	a := Amendment{
		ID:          "am1",
		ResourceID:  "Audio.EqualTemperament",
		Name:        "validate-reference-pitch",
		ContentHash: "abc123",
		Origin:      "deep_review",
		Prompt:      "reject NaN",
		State:       "PENDING",
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.UpsertAmendment(a); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.ListAmendmentsByResource("Audio.EqualTemperament")
	if err != nil || len(got) != 1 || got[0].Name != "validate-reference-pitch" {
		t.Fatalf("list mismatch: %v %+v", err, got)
	}
	if err := s.UpsertAmendment(a); err != nil { // idempotent on (resource_id, name)
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = s.ListAmendmentsByResource("Audio.EqualTemperament")
	if len(got) != 1 {
		t.Fatalf("expected idempotent upsert, got %d rows", len(got))
	}
}

func TestStore_AmendmentStateAndDelete(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "jobs.db"))
	a := Amendment{ID: "am1", ResourceID: "R", Name: "n", ContentHash: "h", State: "PENDING", CreatedAt: time.Now().UTC()}
	if err := s.UpsertAmendment(a); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	now := time.Now().UTC()
	if err := s.UpdateAmendmentState("am1", "APPLIED", "spechash", now, time.Time{}); err != nil {
		t.Fatalf("update state: %v", err)
	}
	got, _ := s.GetAmendment("R", "n")
	if got == nil || got.State != "APPLIED" || got.AppliedSpecHash != "spechash" {
		t.Fatalf("state not updated: %+v", got)
	}
	byState, _ := s.ListAmendmentsByState("APPLIED")
	if len(byState) != 1 {
		t.Fatalf("expected 1 APPLIED, got %d", len(byState))
	}
	if err := s.DeleteAmendment("R", "n"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if all, _ := s.ListAllAmendments(); len(all) != 0 {
		t.Fatalf("expected 0 after delete, got %d", len(all))
	}
}
