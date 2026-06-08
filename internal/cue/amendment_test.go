package cue

import (
	"encoding/json"
	"testing"
)

func TestAmendment_JSONRoundTrip(t *testing.T) {
	a := Amendment{
		Name:   "validate-reference-pitch",
		Prompt: "EqualTemperament::new must reject 0.0, negative, NaN, and ∞ reference pitches.",
		Origin: "deep_review",
		Finding: &Finding{
			Severity: "major",
			File:     "src/audio/equal_temperament.rs",
			Line:     17,
			Text:     "accepts invalid reference pitches with no validation",
		},
		CreatedAt: "2026-06-07T00:00:00Z",
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Amendment
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != a.Name || got.Finding == nil || got.Finding.Line != 17 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestValueObject_CarriesAmendments(t *testing.T) {
	raw := `{
		"from": "f64",
		"description": "equal temperament tuning",
		"meta": { "amendments": [
			{"name": "validate-reference-pitch", "prompt": "reject 0.0/NaN/inf", "origin": "deep_review"}
		] }
	}`
	var vo ValueObject
	if err := json.Unmarshal([]byte(raw), &vo); err != nil {
		t.Fatalf("unmarshal value object: %v", err)
	}
	if len(vo.Meta.Amendments) != 1 || vo.Meta.Amendments[0].Name != "validate-reference-pitch" {
		t.Fatalf("amendments not threaded: %+v", vo.Meta.Amendments)
	}
}

func TestResourceAmendments_TypeSwitches(t *testing.T) {
	r := Resource{
		ID:   "Audio.EqualTemperament",
		Kind: "valueObject",
		Declaration: ValueObject{Meta: Meta{Amendments: []Amendment{{Name: "a1"}, {Name: "a2"}}}},
	}
	got := ResourceAmendments(r)
	if len(got) != 2 || got[1].Name != "a2" {
		t.Fatalf("expected 2 amendments, got %+v", got)
	}

	empty := Resource{Declaration: Aggregate{}}
	if len(ResourceAmendments(empty)) != 0 {
		t.Fatalf("expected no amendments for empty aggregate")
	}
}
