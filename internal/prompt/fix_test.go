package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildFixPrompt(t *testing.T) {
	prompt := BuildFixPrompt(
		"original requirements here",
		"previous generated code",
		"compilation error on line 42",
	)

	assert.Contains(t, prompt, "original requirements here")
	assert.Contains(t, prompt, "previous generated code")
	assert.Contains(t, prompt, "compilation error on line 42")
	assert.Contains(t, prompt, "Fix")
}

func TestBuildFixPrompt_EmptyPrevious(t *testing.T) {
	prompt := BuildFixPrompt("requirements", "", "parse error")

	assert.Contains(t, prompt, "requirements")
	assert.Contains(t, prompt, "parse error")
}
