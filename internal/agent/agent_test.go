package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFakeClaude(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	err := os.WriteFile(path, []byte(script), 0o755)
	require.NoError(t, err)
	return path
}

func TestRunPrompt_BasicExecution(t *testing.T) {
	fakePath := writeFakeClaude(t, "#!/bin/sh\necho '{\"result\":\"hello world\",\"model\":\"claude-sonnet-4-6\",\"is_error\":false}'\n")

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt: "say hello",
	})
	require.NoError(t, err)
	assert.Equal(t, "hello world", result.Output)
	assert.Equal(t, "claude-sonnet-4-6", result.Model)
	assert.False(t, result.IsError)
}

func TestRunPrompt_IsErrorTrue(t *testing.T) {
	fakePath := writeFakeClaude(t, "#!/bin/sh\necho '{\"result\":\"something went wrong\",\"is_error\":true}'\n")

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt: "fail",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is_error")
	assert.NotNil(t, result)
	assert.True(t, result.IsError)
}

func TestRunPrompt_NonZeroExit(t *testing.T) {
	fakePath := writeFakeClaude(t, "#!/bin/sh\necho '{\"result\":\"partial output\",\"is_error\":false}' >&1\necho \"crash details\" >&2\nexit 1\n")

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt: "crash",
	})
	require.Error(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "partial output", result.Output)
	assert.Contains(t, result.Stderr, "crash details")
}

func TestRunPrompt_ModelOverride(t *testing.T) {
	fakePath := writeFakeClaude(t, "#!/bin/sh\necho '{\"result\":\"ok\"}' >&1\necho \"$@\" >&2\n")

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt: "test",
		Model:  "claude-opus-4-8",
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Output)
	assert.Contains(t, result.Stderr, "--model")
	assert.Contains(t, result.Stderr, "claude-opus-4-8")
}

func TestRunPrompt_LargePromptViaStdin(t *testing.T) {
	fakePath := writeFakeClaude(t, "#!/bin/sh\nINPUT=$(cat)\necho \"{\\\"result\\\":\\\"got ${#INPUT} bytes\\\"}\"\n")

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	largePrompt := make([]byte, 9000)
	for i := range largePrompt {
		largePrompt[i] = 'A'
	}

	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt: string(largePrompt),
	})
	require.NoError(t, err)
	assert.Contains(t, result.Output, "9000")
}

func TestRunPrompt_ContextCancellation(t *testing.T) {
	fakePath := writeFakeClaude(t, "#!/bin/sh\nsleep 30\necho '{\"result\":\"should not reach\"}'\n")

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.RunPrompt(ctx, RunOpts{
		Prompt: "test",
	})
	require.Error(t, err)
}

func TestRunPrompt_DisallowedTools(t *testing.T) {
	fakePath := writeFakeClaude(t, "#!/bin/sh\necho '{\"result\":\"ok\"}' >&1\necho \"$@\" >&2\n")

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt:          "test",
		DisallowedTools: []string{"Bash", "Read", "Edit"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Output)
	assert.Contains(t, result.Stderr, "--disallowedTools")
}

func TestModels(t *testing.T) {
	fakePath := writeFakeClaude(t, "#!/bin/sh\nif [ \"$1\" = \"models\" ]; then\n    echo \"claude-opus-4-8, claude-sonnet-4-6, claude-haiku-4-5\"\n    exit 0\nfi\necho \"unexpected args: $@\" >&2\nexit 1\n")

	a := New(fakePath, "", "", "", 0)
	out, err := a.Models(context.Background())
	require.NoError(t, err)
	assert.Contains(t, out, "claude-sonnet-4-6")
}

func TestAbout(t *testing.T) {
	fakePath := writeFakeClaude(t, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n    echo \"claude-code v1.0.0\"\n    exit 0\nfi\necho \"unexpected args: $@\" >&2\nexit 1\n")

	a := New(fakePath, "", "", "", 0)
	out, err := a.About(context.Background())
	require.NoError(t, err)
	assert.Contains(t, out, "claude-code")
}

func TestStatus(t *testing.T) {
	fakePath := writeFakeClaude(t, "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then\n    echo \"Authenticated as user@example.com\"\n    exit 0\nfi\necho \"unexpected args: $@\" >&2\nexit 1\n")

	a := New(fakePath, "", "", "", 0)
	out, err := a.Status(context.Background())
	require.NoError(t, err)
	assert.Contains(t, out, "Authenticated")
}
