package evolve

import (
	"strings"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/store"
)

func TestSelectPromotable_FiltersByConfidenceTimesAppliedAndStatus(t *testing.T) {
	learnings := []store.Learning{
		{ID: "ok", Status: "active", Confidence: 0.9, TimesApplied: 5},           // qualifies
		{ID: "low-conf", Status: "active", Confidence: 0.5, TimesApplied: 9},     // below confidence
		{ID: "few-applied", Status: "active", Confidence: 0.95, TimesApplied: 1}, // below times_applied
		{ID: "retired", Status: "retired", Confidence: 0.99, TimesApplied: 99},   // not active
		{ID: "promoted", Status: "promoted", Confidence: 0.99, TimesApplied: 99}, // not active
		{ID: "edge", Status: "active", Confidence: 0.8, TimesApplied: 3},         // exactly at thresholds
	}

	got := SelectPromotable(learnings, 0.8, 3)

	var ids []string
	for _, l := range got {
		ids = append(ids, l.ID)
	}
	want := []string{"ok", "edge"}
	if len(ids) != len(want) {
		t.Fatalf("SelectPromotable returned %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("SelectPromotable returned %v, want %v", ids, want)
		}
	}
}

func TestSelectPromotable_Empty(t *testing.T) {
	if got := SelectPromotable(nil, 0.8, 3); got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}
}

func TestRenderPromotionBlock_FormatsBulletsWithRationale(t *testing.T) {
	ls := []store.Learning{
		{Text: "Prefer Result over panics", Rationale: "panics abort the process"},
		{Text: "Avoid unwrap in library code"},
		{Text: "  trimmed  ", Rationale: "  note  "},
	}

	got := RenderPromotionBlock(ls)

	want := "- Prefer Result over panics\n" +
		"  - panics abort the process\n" +
		"- Avoid unwrap in library code\n" +
		"- trimmed\n" +
		"  - note\n"
	if got != want {
		t.Fatalf("RenderPromotionBlock mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderPromotionBlock_Empty(t *testing.T) {
	if got := RenderPromotionBlock(nil); got != "" {
		t.Fatalf("expected empty string for nil input, got %q", got)
	}
	if got := RenderPromotionBlock([]store.Learning{}); got != "" {
		t.Fatalf("expected empty string for empty input, got %q", got)
	}
}

func TestRenderPromotionBlock_SkipsBlankText(t *testing.T) {
	got := RenderPromotionBlock([]store.Learning{{Text: "   "}, {Text: "kept"}})
	if strings.Count(got, "- ") != 1 || !strings.Contains(got, "- kept") {
		t.Fatalf("expected only the non-blank bullet, got %q", got)
	}
}
