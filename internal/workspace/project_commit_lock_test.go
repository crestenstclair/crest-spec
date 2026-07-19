package workspace

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProjectCommitLockSerializesIndependentProjectValuesAndCancels(t *testing.T) {
	projectOne := FromRoot(t.TempDir())
	projectTwo := FromRoot(projectOne.Root())
	releaseFirst, err := projectOne.AcquireCommit(context.Background())
	require.NoError(t, err)
	defer releaseFirst()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = projectTwo.AcquireCommit(ctx)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded))

	releaseFirst()
	releaseSecond, err := projectTwo.AcquireCommit(context.Background())
	require.NoError(t, err)
	releaseSecond()
}

func TestProjectCommitLockBlocksAnotherProcess(t *testing.T) {
	root := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestProjectCommitLockSubprocessHolder$")
	command.Env = append(os.Environ(),
		"CREST_SPEC_PROJECT_LOCK_HELPER=holder",
		"CREST_SPEC_PROJECT_LOCK_ROOT="+root,
	)
	stdin, err := command.StdinPipe()
	require.NoError(t, err)
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	var stderr strings.Builder
	command.Stderr = &stderr
	require.NoError(t, command.Start())
	waited := false
	defer func() {
		if waited {
			return
		}
		_ = stdin.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
	}()

	ready, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err, "lock holder did not signal readiness: %s", stderr.String())
	require.Equal(t, "ready", strings.TrimSpace(ready))

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	_, err = FromRoot(root).AcquireCommit(ctx)
	cancel()
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded), err)

	require.NoError(t, command.Process.Kill())
	require.NoError(t, stdin.Close())
	require.Error(t, command.Wait(), "a deliberately killed lock holder must report abnormal exit: %s", stderr.String())
	waited = true
	release, err := FromRoot(root).AcquireCommit(context.Background())
	require.NoError(t, err, "the OS lock must be released automatically when the holder process crashes")
	release()
}

func TestProjectCommitLockSubprocessHolder(t *testing.T) {
	if os.Getenv("CREST_SPEC_PROJECT_LOCK_HELPER") != "holder" {
		return
	}
	release, err := FromRoot(os.Getenv("CREST_SPEC_PROJECT_LOCK_ROOT")).AcquireCommit(context.Background())
	require.NoError(t, err)
	_, err = fmt.Fprintln(os.Stdout, "ready")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, os.Stdin)
	require.NoError(t, err)
	release()
}

func TestProjectCommitLockAllowsIndependentProjectRoots(t *testing.T) {
	first := FromRoot(t.TempDir())
	second := FromRoot(t.TempDir())
	releaseFirst, err := first.AcquireCommit(context.Background())
	require.NoError(t, err)
	defer releaseFirst()

	releaseSecond, err := second.AcquireCommit(context.Background())
	require.NoError(t, err)
	releaseSecond()
}
