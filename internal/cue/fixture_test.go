package cue

import (
	"fmt"
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

func TestGoalOrientedCrestSynthExampleLoadsAndTraces(t *testing.T) {
	dir := filepath.Join(filepath.Dir(fixtureSpecDir()), "..", "..", "examples")
	project, err := Load(dir)
	require.NoError(t, err)
	registry, err := NewRegistry(project)
	require.NoError(t, err)
	require.Empty(t, registry.MissingCapabilities)
	require.Contains(t, registry.ResourcesForGoal("goal.play_instrument"), "aggregate.Engine.Voice")
	require.Contains(t, registry.ResourcesForGoal("goal.edit_by_gamepad"), "adapter.EguiPatchView")
	require.Equal(t, "outbound", project.Contexts["Shell"].Ports["AudioOutput"].Direction)
	require.Equal(t, "device_output", project.Adapters["CpalAudio"].Profile.Kind)
	require.Equal(t, "verification_harness", project.Assets["ToneWitness"].Profile.Kind)
}

// TestPhasesLoadAndResolve: phases/phase-N/ are the ORIGINAL phased spec,
// hydrated — each directory holds base.cue + the cumulative phase files +
// the winning override per asset, resolved (what the retired
// assemblePhaseDir helper used to build in a temp dir). Every directory is
// standalone: it must load as one CUE package and resolve every
// cross-reference, so any phase can be pointed at directly with
// CREST_SPEC_SPEC_DIR to replay that stage of the design.
func TestPhasesLoadAndResolve(t *testing.T) {
	base := filepath.Join(fixtureSpecDir(), "..", "phases")
	if _, err := os.Stat(base); err != nil {
		t.Skipf("phases fixture not present: %v", err)
	}
	for n := 1; n <= 11; n++ {
		t.Run(fmt.Sprintf("phase-%d", n), func(t *testing.T) {
			dir := filepath.Join(base, fmt.Sprintf("phase-%d", n))
			proj, err := Load(dir)
			require.NoError(t, err, "phase %d must load standalone", n)
			require.Equal(t, "crest-synth", proj.Name)
			_, err = NewRegistry(proj)
			require.NoError(t, err, "phase %d cross-references must resolve", n)
		})
	}
}

func TestPhaseGoalsDescribeTheCrestSynthProduct(t *testing.T) {
	expectedGoal := map[int]string{
		1:  "hear_midi_performance",
		2:  "play_polyphonic_instrument",
		3:  "hear_live_realtime_audio",
		4:  "perform_multitimbral_arrangement",
		5:  "perform_expressive_sound",
		6:  "play_sample_instrument",
		7:  "process_sound_with_effects",
		8:  "preserve_and_recall_sound",
		9:  "operate_standalone_instrument",
		10: "use_in_plugin_host",
		11: "edit_live_instrument",
	}
	base := filepath.Join(fixtureSpecDir(), "..", "phases")
	for phase, goalID := range expectedGoal {
		t.Run(fmt.Sprintf("phase-%d", phase), func(t *testing.T) {
			dir := filepath.Join(base, fmt.Sprintf("phase-%d", phase))
			project, err := Load(dir)
			require.NoError(t, err)
			require.Contains(t, project.Goals, goalID)
			require.NotContains(t, project.Actors, "developer", "product actors must be crest-synth users")

			content, err := os.ReadFile(filepath.Join(dir, "goals.cue"))
			require.NoError(t, err)
			lower := strings.ToLower(string(content))
			for _, generic := range []string{"generate_increment", "phase_complete", "declared synthesizer increment", "developer building"} {
				require.NotContains(t, lower, generic)
			}
		})
	}
}

func TestFullFixtureGoalsUseMusicianActors(t *testing.T) {
	project := loadFixture(t)
	require.NotContains(t, project.Actors, "developer")
	require.Contains(t, project.Actors, "performer")
	require.Contains(t, project.Actors, "sound_designer")
	require.Contains(t, project.Goals, "perform_instrument")
	require.Contains(t, project.Goals, "edit_from_steam_deck")
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
