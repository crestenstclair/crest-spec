package prompt

import (
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

	b.WriteString(renderTemplate("role.md", lang, ext))
	b.WriteString("\n")
	b.WriteString(renderTemplate("output_format.md", lang, ext))
	b.WriteString("\n")

	if lang == "rust" {
		b.WriteString(renderTemplate("folder_structure_rust.md", lang, ext))
	} else {
		b.WriteString(renderTemplate("folder_structure_default.md", lang, ext))
	}
	b.WriteString("\n")

	b.WriteString(renderTemplate("solid.md", lang, ext))
	b.WriteString("\n")

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

	b.WriteString(renderTemplate("output_requirements.md", lang, ext))
	// No separator here: the rust addendum continues the same section rather than starting a new one.
	if lang == "rust" {
		b.WriteString(renderTemplate("output_requirements_rust.md", lang, ext))
	}

	if s := renderLearned(lang); s != "" {
		b.WriteString("\n")
		b.WriteString(s)
	}

	return b.String()
}
