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
