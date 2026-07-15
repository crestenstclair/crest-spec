package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCoordinatorAllowsDisjointPathsAndBlocksOverlaps(t *testing.T) {
	coordinator := NewCoordinator()
	releaseVoice, err := coordinator.Acquire(context.Background(), []string{"src/voice.go"})
	require.NoError(t, err)

	releaseMixer, err := coordinator.Acquire(context.Background(), []string{"src/mixer.go"})
	require.NoError(t, err, "non-overlapping work should not wait")
	releaseMixer()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = coordinator.Acquire(ctx, []string{"src/voice.go"})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled))

	releaseVoice()
	releaseAgain, err := coordinator.Acquire(context.Background(), []string{"src/voice.go"})
	require.NoError(t, err)
	releaseAgain()
}

func TestCoordinatorReleaseIsIdempotent(t *testing.T) {
	coordinator := NewCoordinator()
	release, err := coordinator.Acquire(context.Background(), []string{"src/voice.go", "src/voice.go"})
	require.NoError(t, err)
	release()
	release()
}
