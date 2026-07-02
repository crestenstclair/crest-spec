package cue

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureSpecDir returns the crest-synth domain-grouped spec directory.
func fixtureSpecDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "crest-synth", "spec")
}

func loadFixture(t *testing.T) *Project {
	t.Helper()
	dir := fixtureSpecDir()
	if _, err := os.Stat(filepath.Join(dir, "project.cue")); err != nil {
		t.Skipf("crest-synth fixture not present: %v", err)
	}
	proj, err := Load(dir)
	require.NoError(t, err, "the fixture spec must load as one CUE package")
	return proj
}

func TestFixtureLoadsAndResolves(t *testing.T) {
	proj := loadFixture(t)

	require.Equal(t, "crest-synth", proj.Name)
	require.NotEmpty(t, proj.Mission, "the fixture must carry a project mission — it is injected into every system prompt")
	require.NotEmpty(t, proj.Invariants, "architectural invariants must be declared at project level")
	require.NotEmpty(t, proj.Validations, "the whole-tree gate must be declared at project level")
	require.NotEmpty(t, proj.ContextMap, "the context map is the architecture-level layer of the spec")

	for _, ctx := range []string{"Kernel", "Engine", "Sample", "Effects", "Mixer", "Modulation", "Patch", "Preset", "RealTime", "Shell"} {
		_, ok := proj.Contexts[ctx]
		assert.True(t, ok, "bounded context %s must be declared", ctx)
	}

	// The registry validates every uses/implements reference — a dangling ID
	// fails here, not at generation time.
	_, err := NewRegistry(proj)
	require.NoError(t, err, "every cross-reference in the fixture must resolve")
}

// TestFixtureConfinesLanguageProfile enforces the language-confinement rule:
// crate names may appear ONLY in manifest.cue (the Cargo.toml asset's
// dependency list) and in adapter `framework:` fields. A crate name anywhere
// else in the spec is leakage — implementation detail bleeding into the
// domain declarations.
func TestFixtureConfinesLanguageProfile(t *testing.T) {
	dir := fixtureSpecDir()
	if _, err := os.Stat(filepath.Join(dir, "project.cue")); err != nil {
		t.Skipf("crest-synth fixture not present: %v", err)
	}

	crates := []string{
		"cpal", "midir", "eframe", "egui", "gilrs", "rtrb",
		"triple_buffer", "basedrop", "serde", "symphonia",
		"midly", "fundsp", "nih-plug", "winit", "objc2", "icrate",
	}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cue") || e.Name() == "manifest.cue" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		for i, line := range strings.Split(string(data), "\n") {
			// Sanctioned homes for a crate name: an adapter's framework field
			// and the adapter's own name (CpalAudioOutput — the adapter IS the
			// tech-specific element in a hexagonal architecture).
			if strings.Contains(line, "framework:") || strings.Contains(line, "adapters:") {
				continue
			}
			lower := strings.ToLower(line)
			for _, crate := range crates {
				assert.NotContains(t, lower, crate,
					"%s:%d leaks crate %q outside manifest.cue / framework fields", e.Name(), i+1, crate)
			}
		}
	}
}
