package prompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderTemplate_Substitutes(t *testing.T) {
	out := renderTemplate("role.md", "rust", ".rs")
	assert.Contains(t, out, "rust code generator")
	assert.NotContains(t, out, "{{lang}}")
}

func TestRenderTemplate_OutputFormatExt(t *testing.T) {
	out := renderTemplate("output_format.md", "go", ".go")
	assert.Contains(t, out, ".go")
	assert.NotContains(t, out, "{{ext}}")
}

func TestRenderTemplate_MissingPanics(t *testing.T) {
	require.Panics(t, func() { renderTemplate("does-not-exist.md", "go", ".go") })
}

func TestRenderTemplate_EndsWithSingleTrailingNewline(t *testing.T) {
	out := renderTemplate("solid.md", "go", ".go")
	assert.True(t, strings.HasSuffix(out, "\n"))
	assert.False(t, strings.HasSuffix(out, "\n\n"), "renderTemplate must not include the section separator")
}

func TestRenderTemplate_AllTemplatesClean(t *testing.T) {
	names := []string{
		"role.md",
		"output_format.md",
		"folder_structure_rust.md",
		"folder_structure_default.md",
		"solid.md",
		"output_requirements.md",
		"output_requirements_rust.md",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			out := renderTemplate(name, "rust", ".rs")
			assert.NotContains(t, out, "{{", "unsubstituted placeholder leaked in %s", name)
			assert.True(t, strings.HasSuffix(out, "\n"), "%s must end with a newline", name)
			assert.False(t, strings.HasSuffix(out, "\n\n"), "%s must not end with a blank line (separator is added by the assembler)", name)
			assert.NotEmpty(t, strings.TrimSpace(out), "%s rendered empty", name)
		})
	}
}
