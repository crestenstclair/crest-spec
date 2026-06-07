package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_Defaults(t *testing.T) {
	cfg, err := New()
	require.NoError(t, err)

	assert.Equal(t, "claude", cfg.AgentPath)
	assert.Equal(t, "claude-sonnet-4-6", cfg.DefaultModel)
	assert.Equal(t, "default", cfg.PermissionMode)
	assert.Equal(t, 5, cfg.MaxConcurrency)
	assert.Equal(t, "claude-sonnet-4-6", cfg.GenerateModel)
	assert.Equal(t, "claude-sonnet-4-6", cfg.VerifyModel)
	assert.Equal(t, 3, cfg.MaxRetries)
	assert.Equal(t, 2, cfg.WaveMaxRetries)
	assert.Equal(t, "./spec", cfg.SpecDir)
	assert.Empty(t, cfg.APIKey)
	assert.Empty(t, cfg.HTTPAddr)
	assert.Empty(t, cfg.TypeCheckCommand)
	assert.Empty(t, cfg.TestCommand)
}

func TestNew_EnvOverrides(t *testing.T) {
	t.Setenv("CREST_SPEC_AGENT_PATH", "/usr/local/bin/claude")
	t.Setenv("CREST_SPEC_DEFAULT_MODEL", "claude-opus-4-8")
	t.Setenv("CREST_SPEC_MAX_CONCURRENCY", "10")
	t.Setenv("CREST_SPEC_MAX_RETRIES", "5")
	t.Setenv("CREST_SPEC_SPEC_DIR", "/tmp/specs")

	cfg, err := New()
	require.NoError(t, err)

	assert.Equal(t, "/usr/local/bin/claude", cfg.AgentPath)
	assert.Equal(t, "claude-opus-4-8", cfg.DefaultModel)
	assert.Equal(t, 10, cfg.MaxConcurrency)
	assert.Equal(t, 5, cfg.MaxRetries)
	assert.Equal(t, "/tmp/specs", cfg.SpecDir)
}
