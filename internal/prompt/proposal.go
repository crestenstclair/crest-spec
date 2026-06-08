package prompt

import "strings"

// RenderProposeAmendments renders the amendment-proposer prompt with the given
// findings text substituted in. The proposer asks the LLM to draft one
// amendment per actionable finding.
func RenderProposeAmendments(findings string) string {
	body := renderTemplate("propose_amendments.md", "", "")
	return strings.NewReplacer("{{findings}}", findings).Replace(body)
}
