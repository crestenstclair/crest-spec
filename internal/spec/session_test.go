package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBeginResult_HasInstructions(t *testing.T) {
	result := &BeginResult{
		SessionID:    "sess-1",
		Instructions: "You are a dispatcher",
	}
	assert.Contains(t, result.Instructions, "dispatcher")
}

func TestNextResult_Done(t *testing.T) {
	result := &NextResult{Done: true, WaveIndex: 3}
	assert.True(t, result.Done)
}

func TestContextResult_HasPrompts(t *testing.T) {
	result := &ContextResult{
		SystemPrompt: "You are a go code generator",
		Prompt:       "# Resource: aggregate",
	}
	assert.NotEmpty(t, result.SystemPrompt)
	assert.NotEmpty(t, result.Prompt)
}
