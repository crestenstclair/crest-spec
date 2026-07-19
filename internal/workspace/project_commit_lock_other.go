//go:build (!unix && !windows) || aix

package workspace

import (
	"fmt"
	"os"
)

func tryProjectFileLock(_ *os.File) (bool, error) {
	return false, fmt.Errorf("cross-process project commit locking is unsupported on this platform")
}

func unlockProjectFile(_ *os.File) error { return nil }
