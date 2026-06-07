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
	b.WriteString(fmt.Sprintf("```\n// path: src/{context}/{resource}%s\n```\n\n", ext))

	b.WriteString("# Folder Structure\n\n")
	switch lang {
	case "rust":
		b.WriteString("Use snake_case for all file and directory names.\n")
		b.WriteString("Place code in src/{context}/{resource}.rs — one file per type/resource.\n\n")
		b.WriteString("## Module Declarations (CRITICAL)\n\n")
		b.WriteString("You MUST include updated module declaration files in your output:\n")
		b.WriteString("- `src/lib.rs` — must declare `pub mod {context};` for every context directory under src/\n")
		b.WriteString("- `src/{context}/mod.rs` — must declare `pub mod {resource};` for every .rs file in that context directory\n\n")
		b.WriteString("If these files already exist (shown in Module Tree or Existing Dependencies), include them in your output with any new modules ADDED to the existing declarations. Never remove existing declarations.\n\n")
		b.WriteString("## Cargo Dependencies (CRITICAL)\n\n")
		b.WriteString("If your code uses an external crate (e.g. `cpal`, `midir`, `rtrb`, `gilrs`, `egui`), you MUST include an updated `Cargo.toml` in your output that ADDS the dependency under `[dependencies]` with a version.\n")
		b.WriteString("If a `Cargo.toml` already exists (shown in Existing Module Declarations), include it in your output with your new dependencies ADDED — never remove existing dependencies, `[lib]`, or `[[bin]]` sections.\n")
		b.WriteString("Only add crates your code actually imports. A build that fails on an unresolved import means the crate is missing from `Cargo.toml`.\n\n")
	default:
		b.WriteString("All code goes in src/{ContextName}/{ResourceName}/ — grouped by resource, not by architectural layer.\n\n")
	}

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
	if lang == "rust" {
		b.WriteString("Use `crate::` paths to reference types from other modules (e.g., `use crate::kernel::note_id::NoteId;`).\n")
		b.WriteString("Only reference types that exist in the Module Tree or Existing Dependencies shown below. If a type is not yet available, define it locally.\n")
	}

	return b.String()
}
