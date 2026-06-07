package prompt

import (
	"fmt"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

var langExtensions = map[string]string{
	"go":         ".go",
	"rust":       ".rs",
	"python":     ".py",
	"typescript": ".ts",
	"javascript": ".js",
	"java":       ".java",
	"csharp":     ".cs",
	"c":          ".c",
	"cpp":        ".cpp",
}

func BuildSystemPrompt(project *cuepkg.Project) string {
	var b strings.Builder

	lang := project.Meta.Language
	if lang == "" {
		lang = "source"
	}
	ext := langExtensions[lang]

	b.WriteString("# Role\n\n")
	b.WriteString(fmt.Sprintf("You are a %s code generator following strict SOLID principles.\n\n", lang))

	b.WriteString("# Output Format\n\n")
	b.WriteString("Return code in fenced code blocks with path annotations:\n")
	b.WriteString(fmt.Sprintf("```\n// path: src/{ContextName}/{ResourceName}%s\n```\n\n", ext))

	b.WriteString("# Folder Structure\n\n")
	b.WriteString("All code goes in src/{ContextName}/{ResourceName}/ — grouped by resource, not by architectural layer.\n\n")

	b.WriteString("# SOLID Principles\n\n")
	b.WriteString("- **Single Responsibility**: Each type has one reason to change.\n")
	b.WriteString("- **Open/Closed**: Open for extension, closed for modification.\n")
	b.WriteString("- **Liskov Substitution**: Subtypes must be substitutable for their base types.\n")
	b.WriteString("- **Interface Segregation**: Depend on narrow interfaces, not broad ones.\n")
	b.WriteString("- **Dependency Inversion**: Depend on abstractions, not concretions. Accept dependencies via constructor.\n\n")

	if project.Meta.Style != "" {
		b.WriteString("# Code Style\n\n")
		b.WriteString(project.Meta.Style + "\n\n")
	}

	if len(project.Meta.Rules) > 0 {
		b.WriteString("# Rules\n\n")
		for _, rule := range project.Meta.Rules {
			b.WriteString("- " + rule + "\n")
		}
		b.WriteString("\n")
	}

	if len(project.Meta.Avoid) > 0 {
		b.WriteString("# Avoid\n\n")
		for _, avoid := range project.Meta.Avoid {
			b.WriteString("- " + avoid + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("# Output Requirements\n\n")
	b.WriteString("Generate both implementation files and unit tests.\n")

	return b.String()
}
