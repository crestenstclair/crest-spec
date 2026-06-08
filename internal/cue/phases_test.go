package cue

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// phasesDir returns the crest-synth phased fixture directory.
func phasesDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "crest-synth", "phases")
}

var overrideFileRE = regexp.MustCompile(`^phase-(\d+)\.override-(.+)\.cue$`)

// assemblePhaseDir mirrors scripts/run-phased-agent.sh: it copies base.cue +
// phase-1..n.cue into dst, then resolves per-asset overrides by copying only the
// highest-numbered phase-N.override-<Asset>.cue with N <= n. Returns the set of
// override assets that were applied.
func assemblePhaseDir(t *testing.T, phasesDir, dst string, n int) map[string]bool {
	t.Helper()
	copyFixture(t, filepath.Join(phasesDir, "base.cue"), filepath.Join(dst, "base.cue"))
	for p := 1; p <= n; p++ {
		name := "phase-" + strconv.Itoa(p) + ".cue"
		copyFixture(t, filepath.Join(phasesDir, name), filepath.Join(dst, name))
	}

	entries, err := os.ReadDir(phasesDir)
	require.NoError(t, err)
	winners := map[string]string{} // asset -> winning source path
	for _, e := range entries {
		m := overrideFileRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		p, _ := strconv.Atoi(m[1])
		if p > n {
			continue
		}
		// ascending file order isn't guaranteed; keep the highest p
		asset := m[2]
		if cur, ok := winners[asset]; ok {
			cm := overrideFileRE.FindStringSubmatch(filepath.Base(cur))
			cp, _ := strconv.Atoi(cm[1])
			if p < cp {
				continue
			}
		}
		winners[asset] = filepath.Join(phasesDir, e.Name())
	}

	applied := map[string]bool{}
	for asset, src := range winners {
		copyFixture(t, src, filepath.Join(dst, "override-"+asset+".cue"))
		applied[asset] = true
	}
	return applied
}

// TestLoad_PhasedFixture loads every incremental subset the way
// scripts/run-phased-agent.sh assembles each spec dir (base + phase-1..N plus
// resolved per-asset overrides). CUE unifies all files in a dir as one package,
// so a concrete field redeclared with a different value across phases (e.g. a
// validations list) fails the whole load. This guards against that regression
// for every phase boundary.
func TestLoad_PhasedFixture(t *testing.T) {
	dir := phasesDir()
	if _, err := os.Stat(filepath.Join(dir, "base.cue")); err != nil {
		t.Skipf("phased fixture not present: %v", err)
	}

	for n := 1; n <= 10; n++ {
		t.Run("phase-"+strconv.Itoa(n), func(t *testing.T) {
			tmp := t.TempDir()
			assemblePhaseDir(t, dir, tmp, n)
			proj, err := Load(tmp)
			require.NoError(t, err, "phase %d must load without CUE conflicts", n)
			require.Equal(t, "crest-synth", proj.Name)
		})
	}
}

// TestPhasedFixture_ToneTestValidation verifies the override copy behavior gives
// every phase a ToneTestMain validation (so reruns actually verify the build),
// and that phase 3+ picks up the --wav variant rather than phase 1's make-run.
func TestPhasedFixture_ToneTestValidation(t *testing.T) {
	dir := phasesDir()
	if _, err := os.Stat(filepath.Join(dir, "base.cue")); err != nil {
		t.Skipf("phased fixture not present: %v", err)
	}

	cases := []struct {
		phase       int
		wantCommand string // a substring expected in some validation command
	}{
		{1, "make"},
		{2, "make"},
		{3, "--wav"},
		{10, "--wav"},
	}
	for _, tc := range cases {
		t.Run("phase-"+strconv.Itoa(tc.phase), func(t *testing.T) {
			tmp := t.TempDir()
			assemblePhaseDir(t, dir, tmp, tc.phase)
			proj, err := Load(tmp)
			require.NoError(t, err)

			tone, ok := proj.Assets["ToneTestMain"]
			require.True(t, ok, "ToneTestMain asset must exist")
			require.NotEmpty(t, tone.Validations, "phase %d must have a ToneTestMain validation", tc.phase)

			found := false
			for _, v := range tone.Validations {
				for _, arg := range v.Command {
					if arg == tc.wantCommand || containsArg(v.Command, tc.wantCommand) {
						found = true
					}
				}
			}
			require.True(t, found, "phase %d ToneTestMain validation should use %q; got %+v", tc.phase, tc.wantCommand, tone.Validations)
		})
	}
}

func TestPhasedFixture_HasDefaultValidations(t *testing.T) {
	dir := phasesDir()
	if _, err := os.Stat(filepath.Join(dir, "base.cue")); err != nil {
		t.Skipf("phased fixture not present: %v", err)
	}
	tmp := t.TempDir()
	assemblePhaseDir(t, dir, tmp, 1)
	proj, err := Load(tmp)
	require.NoError(t, err)
	require.NotEmpty(t, proj.Validations, "base.cue should declare default project validations")
	var cmds []string
	for _, v := range proj.Validations {
		cmds = append(cmds, strings.Join(v.Command, " "))
	}
	joined := strings.Join(cmds, " | ")
	assert.Contains(t, joined, "clippy")
	assert.Contains(t, joined, "fmt")
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func copyFixture(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, b, 0o644))
}
