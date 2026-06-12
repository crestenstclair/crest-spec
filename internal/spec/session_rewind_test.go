package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// twoWaveSpecCUE declares a context-level value object (wave 0) and a domain
// service that `uses` it (wave 1), so a session has two distinct waves and we
// can drive CurrentWave past 0 before resetting a wave-0 resource to pending.
const twoWaveSpecCUE = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {from: "f64"}
project: contexts: Audio: domainServices: Mixer: {
	purpose: "mix tones"
	uses: ["valueObject.Audio.Tone"]
}
`

// newTestSpecWithTwoWaveSession builds a Spec over a real store + a two-wave
// spec and starts a session, returning the value object (wave 0) and domain
// service (wave 1) resource IDs.
func newTestSpecWithTwoWaveSession(t *testing.T) (s *Spec, sessionID, wave0ID, wave1ID string) {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spec.cue"), []byte(twoWaveSpecCUE), 0o644))

	cfg := &config.Config{SpecDir: dir, GenerateModel: "claude-sonnet-4-6", MaxRetries: 3}
	s = New(st, OSFileSystem{}, cfg)

	ctx := context.Background()
	begin, err := s.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.NotEmpty(t, begin.SessionID)

	// Only plan resources (generatable) are served by Next; context/project
	// resources sit in the waves but are filtered out. The value object is the
	// sole wave-0 generatable, the domain service the sole wave-1 generatable.
	wave0ID = "valueObject.Audio.Tone"
	wave1ID = "domainService.Audio.Mixer"
	planIDs := make(map[string]bool)
	for _, a := range begin.Plan {
		planIDs[a.ResourceID] = true
	}
	require.True(t, planIDs[wave0ID], "expected value object in plan")
	require.True(t, planIDs[wave1ID], "expected domain service in plan")
	return s, begin.SessionID, wave0ID, wave1ID
}

// commitResource writes a trivial file and commits it so the resource lands in
// the committed (terminal) state, letting Next advance past its wave.
func commitResource(t *testing.T, s *Spec, sessionID, resourceID string) {
	t.Helper()
	_, err := s.Commit(context.Background(), sessionID, resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "out.go"), Content: "package out\n"}},
		"", nil, "claude-sonnet-4-6")
	require.NoError(t, err)
}

func TestNext_RewindsToEarlierWavePending(t *testing.T) {
	s, sessionID, wave0ID, wave1ID := newTestSpecWithTwoWaveSession(t)
	ctx := context.Background()

	// Drive the session forward: Next serves wave 0, commit it, Next lands on
	// wave 1 (CurrentWave advances to 1).
	n, err := s.Next(ctx, sessionID)
	require.NoError(t, err)
	require.False(t, n.Done)
	require.Equal(t, 0, n.WaveIndex)
	require.Equal(t, wave0ID, n.Resources[0].ResourceID)

	commitResource(t, s, sessionID, wave0ID)

	n, err = s.Next(ctx, sessionID)
	require.NoError(t, err)
	require.False(t, n.Done)
	require.Equal(t, 1, n.WaveIndex, "Next should have advanced CurrentWave to 1")
	require.Equal(t, wave1ID, n.Resources[0].ResourceID)

	// Confirm CurrentWave actually advanced past wave 0 in the persisted session.
	sess, err := s.store.GetSession(sessionID)
	require.NoError(t, err)
	require.Equal(t, 1, sess.CurrentWave)

	// Wave-level verification / resolve resets the wave-0 resource back to
	// pending AFTER CurrentWave has advanced to 1.
	require.NoError(t, s.Resolve(ctx, sessionID, wave0ID, "regenerate with new guidance", ""))

	// Next must rewind and re-serve the wave-0 resource — not skip it forever.
	n, err = s.Next(ctx, sessionID)
	require.NoError(t, err)
	require.False(t, n.Done, "the reset wave-0 resource must be reachable again")
	require.Equal(t, 0, n.WaveIndex, "Next must rewind to wave 0")
	require.Len(t, n.Resources, 1)
	require.Equal(t, wave0ID, n.Resources[0].ResourceID)
}
