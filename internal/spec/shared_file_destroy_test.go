package spec

import (
	"context"
	"testing"

	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/stretchr/testify/require"
)

// src/lib.rs is committed by many generators over time. Destroying whichever
// resource happened to commit it LAST must not delete it from disk while
// other live resources still own it. (Bit us for real: destroying the Plugin
// entity deleted src/lib.rs.)
const twoResourceSpecCUE = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {from: "f64"}
project: contexts: Audio: valueObjects: Beep: {from: "f64"}
project: invariants: [{text: "no global state"}]
`

func TestDestroySparesFilesWithOtherOwners(t *testing.T) {
	s, st := newTestSpecWithSessionCUE(t, twoResourceSpecCUE)
	ctx := context.Background()

	next, err := s.Next(ctx, st.sessionID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(next.Resources), 2, "need two resources in the wave")
	resA, resB := next.Resources[0].ResourceID, next.Resources[1].ResourceID

	dir := t.TempDir()
	shared := dir + "/lib.rs"
	own := dir + "/plugin_only.rs"

	// resource A commits the shared file first...
	_, err = s.Commit(ctx, st.sessionID, resA,
		[]CommitFile{{Path: shared, Content: "pub mod a;\n"}}, "", "claude-sonnet-4-6")
	require.NoError(t, err)

	// ...then resource B commits BOTH the shared file (becoming its latest
	// committer) and its own private file.
	_, err = s.Commit(ctx, st.sessionID, resB,
		[]CommitFile{{Path: shared, Content: "pub mod a;\npub mod b;\n"}, {Path: own, Content: "pub struct B;\n"}}, "", "claude-sonnet-4-6")
	require.NoError(t, err)
	otherID := resB

	sess, err := s.store.GetSession(st.sessionID)
	require.NoError(t, err)
	destroyed := s.executeDestroys([]planpkg.PlannedAction{{ResourceID: otherID, Kind: planpkg.ActionDestroy}}, sess.ApplyID)
	require.Len(t, destroyed, 1)

	// B's private file is gone; the shared file survives because A owns it too.
	require.NoFileExists(t, own, "sole-owned file must be deleted")
	require.FileExists(t, shared, "shared file must survive while another owner lives")

	// A's ownership row for the shared file is intact.
	files, err := s.store.GetGeneratedFiles(resA)
	require.NoError(t, err)
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
	}
	require.True(t, paths[shared], "surviving owner keeps its row for the shared file")
}
