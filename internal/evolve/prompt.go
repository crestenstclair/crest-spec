package evolve

import (
	"fmt"
	"strings"

	"github.com/crestenstclair/crest-spec/internal/store"
)

// buildExtractionPrompt composes the single prompt that asks the model to
// distill CRAFT-LEVEL learnings from the gathered failure signal. The prompt
// is explicit that output must generalize across resources of a (language,
// kind) — per-resource fixes belong to the amendments workflow and must be
// discarded. Existing learnings are passed so the model dedupes.
func buildExtractionPrompt(signal []resourceSignal, existing []store.Learning) string {
	var sb strings.Builder

	sb.WriteString("You are improving a code generator's CRAFT. Below is failure history ")
	sb.WriteString("from a code-generation run. Distill it into reusable, GENERAL learnings ")
	sb.WriteString("that will help generate any future resource of the same (language, kind).\n\n")

	sb.WriteString("CRITICAL RULES:\n")
	sb.WriteString("- Produce CRAFT-LEVEL guidance that generalizes across ALL resources of a ")
	sb.WriteString("(language, resource-kind) — e.g. \"for audio-output adapters, prefer blocking ")
	sb.WriteString("send over try_send\".\n")
	sb.WriteString("- DISCARD per-resource-only observations (a fix to one specific file). Those ")
	sb.WriteString("are not learnings.\n")
	sb.WriteString("- Do NOT repeat guidance already present in the EXISTING LEARNINGS section.\n")
	sb.WriteString("- If nothing generalizes, output an empty array.\n\n")

	if len(existing) > 0 {
		sb.WriteString("EXISTING LEARNINGS (do not duplicate these):\n")
		for _, e := range existing {
			fmt.Fprintf(&sb, "- [%s/%s] %s\n", scopeOrAny(e.ScopeLang), scopeOrAny(e.ScopeKind), e.Text)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("FAILURE HISTORY:\n")
	for _, s := range signal {
		fmt.Fprintf(&sb, "\nResource kind: %s\n", scopeOrAny(s.Kind))
		if s.LastError != "" {
			fmt.Fprintf(&sb, "  last error: %s\n", truncate(s.LastError, 800))
		}
		for _, rej := range s.Rejections {
			fmt.Fprintf(&sb, "  rejection: %s\n", truncate(rej, 800))
		}
		for _, fl := range s.Failures {
			fmt.Fprintf(&sb, "  invariant failure: %s\n", truncate(fl, 800))
		}
	}

	sb.WriteString("\n\nOutput the learnings as a single JSON array wrapped EXACTLY between these ")
	sb.WriteString("markers, each marker on its own line:\n")
	sb.WriteString(learningsBegin)
	sb.WriteString("\n")
	sb.WriteString(`[{"scope_lang": "rust", "scope_kind": "adapter", "text": "...", "rationale": "...", "confidence": 0.0}]`)
	sb.WriteString("\n")
	sb.WriteString(learningsEnd)
	sb.WriteString("\n")
	sb.WriteString("confidence is 0..1. scope_lang/scope_kind may be \"\" for guidance that applies ")
	sb.WriteString("to any language/kind. Output nothing after the END marker.")

	return sb.String()
}

func scopeOrAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
