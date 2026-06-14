package prompt

import (
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/stretchr/testify/assert"
)

func TestBuildSystemPrompt_Full(t *testing.T) {
	project := &cuepkg.Project{
		Name: "test-project",
		Meta: cuepkg.Meta{
			Language: "go",
			Style:    "DDD",
			Rules:    []string{"Use interfaces for all dependencies", "Prefer composition over inheritance"},
			Avoid:    []string{"global variables", "init() functions"},
		},
	}

	prompt := BuildSystemPrompt(project)

	assert.Contains(t, prompt, "go code generator")
	assert.Contains(t, prompt, "SOLID")
	assert.Contains(t, prompt, "DDD")
	assert.Contains(t, prompt, "Use interfaces for all dependencies")
	assert.Contains(t, prompt, "Prefer composition over inheritance")
	assert.Contains(t, prompt, "global variables")
	assert.Contains(t, prompt, "init() functions")
	assert.Contains(t, prompt, ".go")
	assert.Contains(t, prompt, "implementation files and unit tests")
}

func TestBuildSystemPrompt_Minimal(t *testing.T) {
	project := &cuepkg.Project{
		Name: "minimal",
		Meta: cuepkg.Meta{},
	}

	prompt := BuildSystemPrompt(project)

	assert.Contains(t, prompt, "code generator")
	assert.Contains(t, prompt, "SOLID")
	assert.NotContains(t, prompt, "# Code Style")
	assert.NotContains(t, prompt, "# Rules")
	assert.NotContains(t, prompt, "# Avoid")
}

func TestBuildSystemPrompt_RustLanguage(t *testing.T) {
	project := &cuepkg.Project{
		Name: "rust-project",
		Meta: cuepkg.Meta{
			Language: "rust",
		},
	}

	prompt := BuildSystemPrompt(project)

	assert.Contains(t, prompt, "rust code generator")
	assert.Contains(t, prompt, ".rs")
}
