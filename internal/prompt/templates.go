package prompt

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed templates/*.md
var templateFS embed.FS

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
