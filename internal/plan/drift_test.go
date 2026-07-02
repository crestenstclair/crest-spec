package plan

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/store"
)

// Drift: generated files edited out-of-band. Stored content hashes were
// previously write-only — an edited (or corrupted) generated file was
// invisible to the planner. DetectDrift surfaces the mismatch as an advisory;
// it does NOT regenerate (once generated, the file is the user's to modify),
// but the user must SEE that spec-state and disk have diverged.

func TestDetectDriftReportsEditedFile(t *testing.T) {
	st := newFakeStore()
	fs := newFakeFS()

	original := []byte("pub struct Frequency(f64);\n")
	edited := []byte("pub struct Frequency(f32); // hand-edited\n")

	st.resources["valueObject.Kernel.Frequency"] = store.Resource{ID: "valueObject.Kernel.Frequency", Kind: "valueObject"}
	st.files["valueObject.Kernel.Frequency"] = []store.GeneratedFile{{
		Path:        "src/kernel/frequency.rs",
		ResourceID:  "valueObject.Kernel.Frequency",
		ContentHash: fmt.Sprintf("%x", sha256.Sum256(original)),
	}}
	fs.files["src/kernel/frequency.rs"] = edited

	p := New(st, fs)
	drift, err := p.DetectDrift()
	require.NoError(t, err)
	require.Len(t, drift, 1)
	assert.Equal(t, "valueObject.Kernel.Frequency", drift[0].ResourceID)
	assert.Equal(t, "src/kernel/frequency.rs", drift[0].Path)
}

func TestDetectDriftIgnoresUntouchedAndMissingFiles(t *testing.T) {
	st := newFakeStore()
	fs := newFakeFS()

	content := []byte("pub struct Pan(f64);\n")
	st.resources["valueObject.Kernel.Pan"] = store.Resource{ID: "valueObject.Kernel.Pan", Kind: "valueObject"}
	st.files["valueObject.Kernel.Pan"] = []store.GeneratedFile{
		{Path: "src/kernel/pan.rs", ResourceID: "valueObject.Kernel.Pan", ContentHash: fmt.Sprintf("%x", sha256.Sum256(content))},
		{Path: "src/kernel/deleted.rs", ResourceID: "valueObject.Kernel.Pan", ContentHash: "abc"},
	}
	fs.files["src/kernel/pan.rs"] = content
	// deleted.rs absent on disk: that's the "missing file" regen path, not drift.

	p := New(st, fs)
	drift, err := p.DetectDrift()
	require.NoError(t, err)
	assert.Empty(t, drift)
}
