package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/cue"
)

func TestRenderAmendmentOverride(t *testing.T) {
	out := renderAmendmentOverride("crestsynth", "EqualTemperament", "valueObject", "Audio", []amendmentEntry{
		{Name: "validate-reference-pitch", Prompt: "reject 0.0/NaN/inf", Origin: "deep_review"},
	})
	for _, want := range []string{
		"package crestsynth",
		"// Amendment",
		"EqualTemperament",
		"meta:",
		"amendments:",
		"validate-reference-pitch",
		"reject 0.0/NaN/inf",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("override missing %q:\n%s", want, out)
		}
	}
}

func TestCuePath(t *testing.T) {
	if got := cuePath("valueObject", "Audio", "EqualTemperament"); got != "project: contexts: Audio: valueObjects: EqualTemperament" {
		t.Fatalf("valueObject path wrong: %s", got)
	}
	if got := cuePath("asset", "", "ToneTestMain"); got != "project: assets: ToneTestMain" {
		t.Fatalf("asset path wrong: %s", got)
	}
}

// TestRenderAmendmentOverride_LoadsBack proves the rendered override is valid
// CUE that the real loader + registry read back into the resource's amendments
// via cue.ResourceAmendments. This is the critical guarantee: the renderer's
// output must actually attach to meta.amendments in production loading.
func TestRenderAmendmentOverride_LoadsBack(t *testing.T) {
	dir := t.TempDir()

	base := `package crestsynth

project: name: "rt"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: EqualTemperament: {from: "f64"}
`
	if err := os.WriteFile(filepath.Join(dir, "base.cue"), []byte(base), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}

	override := renderAmendmentOverride("crestsynth", "EqualTemperament", "valueObject", "Audio", []amendmentEntry{
		{
			Name:   "validate-reference-pitch",
			Prompt: "reject 0.0/NaN/inf",
			Origin: "deep_review",
			Finding: &findingEntry{
				Severity: "major",
				File:     "src/audio/equal_temperament.rs",
				Line:     17,
				Text:     "accepts invalid reference pitches",
			},
		},
	})
	if err := os.WriteFile(filepath.Join(dir, "override-EqualTemperament.cue"), []byte(override), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	project, err := cue.Load(dir)
	if err != nil {
		t.Fatalf("load failed (rendered CUE may be malformed):\n%s\nerr: %v", override, err)
	}

	reg, err := cue.NewRegistry(project)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	r, ok := reg.Resources["valueObject.Audio.EqualTemperament"]
	if !ok {
		t.Fatalf("resource not found in registry; have: %v", keys(reg.Resources))
	}

	ams := cue.ResourceAmendments(r)
	if len(ams) != 1 {
		t.Fatalf("expected 1 amendment, got %d: %+v", len(ams), ams)
	}
	if ams[0].Name != "validate-reference-pitch" {
		t.Fatalf("amendment name wrong: %q", ams[0].Name)
	}
	if ams[0].Finding == nil || ams[0].Finding.Line != 17 {
		t.Fatalf("finding not loaded back: %+v", ams[0].Finding)
	}
}

func keys(m map[string]cue.Resource) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
