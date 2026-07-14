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

func TestPhaseFixturesAccumulateProductIntent(t *testing.T) {
	canonical := loadFixture(t)
	goalOrder := []string{
		"produce_audible_synthesis",
		"perform_polyphonic_music",
		"run_realtime_audio",
		"perform_multitimbral_music",
		"perform_expressively",
		"play_sample_based_instruments",
		"shape_sound_with_effects",
		"preserve_and_recall_sounds",
		"operate_standalone_instrument",
		"edit_live_instrument",
	}
	goalCountByPhase := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 9, 10}
	capabilityCountByPhase := []int{0, 1, 2, 4, 5, 6, 7, 8, 9, 11, 11, 12}
	requirementCountByPhase := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 10, 11}

	base := filepath.Join(fixtureSpecDir(), "..", "phases")
	var previous *Project
	for phase := 1; phase <= 11; phase++ {
		t.Run(fmt.Sprintf("phase-%d", phase), func(t *testing.T) {
			dir := filepath.Join(base, fmt.Sprintf("phase-%d", phase))
			project, err := Load(dir)
			require.NoError(t, err)

			// Replay phases are cumulative test inputs, not a crest-spec lifecycle.
			// Each directory represents the intent declared by that design state:
			// later states retain every prior outcome and add newly intended behavior.
			require.Equal(t, canonical.Mission, project.Mission)
			require.Len(t, project.Goals, goalCountByPhase[phase])
			require.Len(t, project.Capabilities, capabilityCountByPhase[phase])
			require.Len(t, project.Requirements, requirementCountByPhase[phase])
			require.Len(t, project.Evidence, capabilityCountByPhase[phase])
			for index, goalID := range goalOrder {
				if index < goalCountByPhase[phase] {
					require.Contains(t, project.Goals, goalID)
				} else {
					require.NotContains(t, project.Goals, goalID)
				}
			}
			if previous != nil {
				for goalID := range previous.Goals {
					require.Contains(t, project.Goals, goalID, "phase %d must retain phase %d goal %s", phase, phase-1, goalID)
				}
				for capabilityID := range previous.Capabilities {
					require.Contains(t, project.Capabilities, capabilityID, "phase %d must retain phase %d capability %s", phase, phase-1, capabilityID)
				}
				for requirementID := range previous.Requirements {
					require.Contains(t, project.Requirements, requirementID, "phase %d must retain phase %d requirement %s", phase, phase-1, requirementID)
				}
				for evidenceID := range previous.Evidence {
					require.Contains(t, project.Evidence, evidenceID, "phase %d must retain phase %d evidence %s", phase, phase-1, evidenceID)
				}
				for _, goalID := range previous.Completion.RequiredGoals {
					require.Contains(t, project.Completion.RequiredGoals, goalID)
				}
				for _, checkID := range previous.Completion.ProjectChecks {
					require.Contains(t, project.Completion.ProjectChecks, checkID)
				}
				if phase == 10 {
					// The legacy plug-in declaration remains useful planner test data,
					// but it is not crest-synth product intent and adds no goal.
					require.Equal(t, previous.Goals, project.Goals)
					require.Equal(t, previous.Capabilities, project.Capabilities)
					require.Equal(t, previous.Requirements, project.Requirements)
					require.Equal(t, previous.Evidence, project.Evidence)
					require.Equal(t, previous.Completion, project.Completion)
				}
			}
			require.NotContains(t, project.Goals, "host_as_plugin")
			require.NotContains(t, project.Capabilities, "run_in_plugin_host")
			previous = project
		})
	}
}

func TestFullFixtureGoalsModelTheStandaloneInstrument(t *testing.T) {
	project := loadFixture(t)
	require.NotContains(t, project.Actors, "developer")
	require.Contains(t, project.Actors, "performer")
	require.Contains(t, project.Actors, "sound_designer")
	require.Contains(t, project.Goals, "perform_live")
	require.Contains(t, project.Goals, "design_playable_sounds")
	require.Contains(t, project.Goals, "preserve_work")
	require.Contains(t, project.Goals, "operate_standalone")
	require.Contains(t, project.NonGoals, "plugin_formats")
	require.NotContains(t, project.Goals, "host_as_plugin")

	registry, err := NewRegistry(project)
	require.NoError(t, err)
	require.Empty(t, registry.MissingCapabilities, "the whole-picture DDD model must implement every declared product capability")
	require.Contains(t, registry.ResourcesForGoal("goal.perform_live"), "domainService.Patch.MidiDispatcher")
	require.Contains(t, registry.ResourcesForGoal("goal.perform_live"), "domainService.Engine.EngineRenderer")
	require.Contains(t, registry.ResourcesForGoal("goal.design_playable_sounds"), "aggregate.Patch.Patch")
	require.Contains(t, registry.ResourcesForGoal("goal.preserve_work"), "aggregate.Preset.Session")
	require.Contains(t, registry.ResourcesForGoal("goal.operate_standalone"), "adapter.GilrsGamepadInput")
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
