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
