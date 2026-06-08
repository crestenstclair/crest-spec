package prompt

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed templates/*.md templates/learned/*.md
var templateFS embed.FS

// htmlCommentRE matches HTML comments (including multi-line) so that
// placeholder learned templates contribute no real content.
var htmlCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)

// renderTemplate loads an embedded markdown template by file name and
// substitutes {{lang}} and {{ext}} placeholders. The returned text is the
// section body with exactly one trailing newline (the section separator blank
// line is added by the assembler in BuildSystemPrompt). Panics if the template
// is missing — templates are embedded at compile time, so absence is a bug.
func renderTemplate(name, lang, ext string) string {
	data, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		panic(fmt.Sprintf("prompt: embedded template %q not found: %v", name, err))
	}
	body := strings.TrimRight(string(data), "\n") + "\n"
	r := strings.NewReplacer("{{lang}}", lang, "{{ext}}", ext)
	return r.Replace(body)
}

// formatLearned returns the "# Learned Practices" section for the given
// raw learned-template content, or "" when the content has no real text
// (blank or only HTML comments). The empty case guarantees a placeholder
// learned file contributes nothing to the system prompt.
func formatLearned(content string) string {
	stripped := strings.TrimSpace(htmlCommentRE.ReplaceAllString(content, ""))
	if stripped == "" {
		return ""
	}
	return "# Learned Practices\n\n" + stripped + "\n"
}

// renderLearned loads templates/learned/<lang>.md (tolerating absence) and
// returns formatLearned of its content. Returns "" if the file is missing.
func renderLearned(lang string) string {
	data, err := templateFS.ReadFile("templates/learned/" + lang + ".md")
	if err != nil {
		return ""
	}
	return formatLearned(string(data))
}
