package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const projectCommitLockPollInterval = 10 * time.Millisecond

// processProjectCommits complements the OS advisory lock for independent
// Project values hosted in one process. The existing per-Spec path coordinator
// still provides fine-grained scheduling before callers reach this coarse
// project-wide commit boundary.
var processProjectCommits = NewCoordinator()

// AcquireCommit serializes the repository mutation/finalization critical
// section across every CrestSpec process using this project root. Generation
// and context preparation remain parallel. The lock file is deliberately left
// in place: deleting a locked inode would let another process lock a different
// inode at the same path.
func (p Project) AcquireCommit(ctx context.Context) (release func(), err error) {
	if ctx == nil {
		return nil, fmt.Errorf("acquire project commit lock: context is required")
	}
	root := p.Root()
	if root == "" {
		root = FromRoot(".").Root()
	}
	lockPath := filepath.Join(root, ".crest-spec", "commit.lock")
	releaseProcess, err := processProjectCommits.Acquire(ctx, []string{lockPath})
	if err != nil {
		return nil, fmt.Errorf("acquire project commit lock: %w", err)
	}
	releaseProcessOnError := true
	defer func() {
		if releaseProcessOnError {
			releaseProcess()
		}
	}()

	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("acquire project commit lock: create state directory: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("acquire project commit lock: open %s: %w", lockPath, err)
	}
	locked := false
	defer func() {
		if !locked {
			_ = file.Close()
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("acquire project commit lock: %w", err)
		}
		acquired, lockErr := tryProjectFileLock(file)
		if lockErr != nil {
			return nil, fmt.Errorf("acquire project commit lock: lock %s: %w", lockPath, lockErr)
		}
		if acquired {
			locked = true
			break
		}
		timer := time.NewTimer(projectCommitLockPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, fmt.Errorf("acquire project commit lock: %w", ctx.Err())
		case <-timer.C:
		}
	}

	releaseProcessOnError = false
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = unlockProjectFile(file)
			_ = file.Close()
			releaseProcess()
		})
	}, nil
}
