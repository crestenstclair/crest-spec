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

// The mission and the project-level architectural invariants are the context
// every generator needs to make aligned decisions. They were previously
// absent from the system prompt entirely (invariants only flowed out as a
// separate list for self-judging; mission had no schema field at all).
func TestBuildSystemPrompt_MissionAndInvariants(t *testing.T) {
	project := &cuepkg.Project{
		Name:    "crest-synth",
		Mission: "A standalone, gamepad-friendly MIDI synthesizer with a hard real-time audio thread at its core.",
		Meta:    cuepkg.Meta{Language: "rust"},
		Invariants: cuepkg.FlexInvariants{
			{Text: "the audio thread must never allocate heap memory",
				Meta: cuepkg.Meta{Rationale: "the callback has a hard deadline"}},
		},
	}

	prompt := BuildSystemPrompt(project)

	assert.Contains(t, prompt, "# Project Mission")
	assert.Contains(t, prompt, "gamepad-friendly MIDI synthesizer")
	assert.Contains(t, prompt, "# Architectural Invariants")
	assert.Contains(t, prompt, "the audio thread must never allocate heap memory")
	assert.Contains(t, prompt, "the callback has a hard deadline")
}

func TestBuildSystemPrompt_NoMissionNoInvariantSections(t *testing.T) {
	prompt := BuildSystemPrompt(&cuepkg.Project{Name: "minimal", Meta: cuepkg.Meta{}})
	assert.NotContains(t, prompt, "# Project Mission")
	assert.NotContains(t, prompt, "# Architectural Invariants")
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
