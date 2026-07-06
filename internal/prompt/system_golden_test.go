package prompt

import (
	"os"
	"path/filepath"
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/stretchr/testify/require"
)

// updateGolden controls whether golden files are (re)written. Run once with
// UPDATE_GOLDEN=1 to capture the current BuildSystemPrompt output, then never
// again — the refactor must keep these bytes identical.
var updateGolden = os.Getenv("UPDATE_GOLDEN") == "1"

func goldenCases() map[string]*cuepkg.Project {
	return map[string]*cuepkg.Project{
		"rust": {
			Name: "rust-project",
			Meta: cuepkg.Meta{
				Language: "rust",
				Style:    "idiomatic Rust; lock-free audio thread",
				Rules:    []string{"Use interfaces for all dependencies"},
				Avoid:    []string{"heap allocation on audio thread"},
			},
		},
		"go-minimal": {
			Name: "minimal",
			Meta: cuepkg.Meta{Language: "go"},
		},
		"unset-lang": {
			Name: "nolang",
			Meta: cuepkg.Meta{},
		},
	}
}

func TestBuildSystemPrompt_Golden(t *testing.T) {
	dir := filepath.Join("testdata", "golden")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	for name, project := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			got := BuildSystemPrompt(project)
			path := filepath.Join(dir, name+".md")

			if updateGolden {
				require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
				return
			}

			want, err := os.ReadFile(path)
			require.NoError(t, err, "golden missing; run UPDATE_GOLDEN=1 go test ./internal/prompt/ -run Golden")
			require.Equal(t, string(want), got, "BuildSystemPrompt output changed; refactor must be byte-identical")
		})
	}
}
