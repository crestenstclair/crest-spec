package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// processBuildIdentity binds one MCP process to the exact executable image
// that started it. Rebuilding a binary does not replace already-running MCP
// processes, so without this guard an agent can silently continue through an
// old engine after a framework upgrade.
type processBuildIdentity struct {
	Path         string
	SHA256       string
	Size         int64
	ModTime      time.Time
	FileInfo     os.FileInfo
	CapturedAt   time.Time
	CaptureError string
}

var (
	runtimeBuildIdentityOnce sync.Once
	runtimeBuildIdentity     processBuildIdentity
)

func currentProcessBuildIdentity() processBuildIdentity {
	runtimeBuildIdentityOnce.Do(func() {
		runtimeBuildIdentity = captureProcessBuildIdentity()
	})
	return runtimeBuildIdentity
}

func captureProcessBuildIdentity() processBuildIdentity {
	path, err := os.Executable()
	if err != nil {
		return processBuildIdentity{CapturedAt: time.Now().UTC(), CaptureError: fmt.Sprintf("resolve executable: %v", err)}
	}
	return captureBuildIdentity(path)
}

func captureBuildIdentity(path string) processBuildIdentity {
	identity := processBuildIdentity{Path: path, CapturedAt: time.Now().UTC()}
	info, err := os.Stat(path)
	if err != nil {
		identity.CaptureError = fmt.Sprintf("stat executable: %v", err)
		return identity
	}
	identity.FileInfo = info
	identity.Size = info.Size()
	identity.ModTime = info.ModTime()
	file, err := os.Open(path)
	if err != nil {
		identity.CaptureError = fmt.Sprintf("open executable: %v", err)
		return identity
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		identity.CaptureError = fmt.Sprintf("hash executable: %v", err)
		return identity
	}
	identity.SHA256 = hex.EncodeToString(digest.Sum(nil))
	return identity
}

func (identity processBuildIdentity) stale() (bool, string) {
	if identity.CaptureError != "" {
		return true, "the CrestSpec process could not establish its executable identity: " + identity.CaptureError
	}
	if identity.Path == "" || identity.FileInfo == nil {
		return true, "the CrestSpec process has no executable identity"
	}
	current, err := os.Stat(identity.Path)
	if err != nil {
		return true, fmt.Sprintf("configured executable is no longer readable: %v", err)
	}
	if !os.SameFile(identity.FileInfo, current) || current.Size() != identity.Size || !current.ModTime().Equal(identity.ModTime) {
		return true, "the crest-spec executable changed after this MCP process started"
	}
	return false, ""
}
