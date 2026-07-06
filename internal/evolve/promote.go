package evolve

import (
	"strings"

	"github.com/crestenstclair/crest-spec/internal/store"
)

// SelectPromotable filters learnings down to those eligible for human-gated
// promotion into a learned template. A learning qualifies when it is still
// active, its confidence meets minConfidence, and it has been applied at least
// minTimesApplied times. The order of the input is preserved.
//
// This is a PURE function: it reads only its arguments and allocates no shared
// state, so it can be tested in isolation.
func SelectPromotable(learnings []store.Learning, minConfidence float64, minTimesApplied int) []store.Learning {
	var out []store.Learning
	for _, l := range learnings {
		if l.Status != "active" {
			continue
		}
		if l.Confidence < minConfidence {
			continue
		}
		if l.TimesApplied < minTimesApplied {
			continue
		}
		out = append(out, l)
	}
	return out
}

// RenderPromotionBlock renders a markdown block to APPEND to a learned template:
// one bullet per learning, with the rationale (when present) as an indented
// sub-note beneath the bullet. Returns "" for empty input so callers can append
// unconditionally without introducing stray whitespace.
//
// This is a PURE function with no side effects.
func RenderPromotionBlock(ls []store.Learning) string {
	if len(ls) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range ls {
		text := strings.TrimSpace(l.Text)
		if text == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(text)
		b.WriteString("\n")
		if rationale := strings.TrimSpace(l.Rationale); rationale != "" {
			b.WriteString("  - ")
			b.WriteString(rationale)
			b.WriteString("\n")
		}
	}
	return b.String()
}
