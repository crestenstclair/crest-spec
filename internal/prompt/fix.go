package prompt

import (
	"strings"
)

func BuildFixPrompt(resourcePrompt string, previousOutput string, errorMsg string) string {
	var b strings.Builder

	b.WriteString("# Fix Required\n\n")
	b.WriteString("The previous generation had errors. Fix them while keeping the same requirements.\n\n")

	b.WriteString("## Original Requirements\n\n")
	b.WriteString(resourcePrompt)
	b.WriteString("\n\n")

	if previousOutput != "" {
		b.WriteString("## Previous Output\n\n")
		b.WriteString(previousOutput)
		b.WriteString("\n\n")
	}

	b.WriteString("## Error to Fix\n\n")
	b.WriteString(errorMsg)
	b.WriteString("\n\n")

	b.WriteString("Generate corrected code that addresses the error above.\n")

	return b.String()
}
