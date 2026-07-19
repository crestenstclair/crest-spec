package spec

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type fileSystem interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm fs.FileMode) error
	WriteFileAtomic(path string, data []byte, perm fs.FileMode) error
	Chmod(path string, mode fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Remove(path string) error
	ReadDir(path string) ([]os.DirEntry, error)
	Stat(path string) (fs.FileInfo, error)
	Lstat(path string) (fs.FileInfo, error)
}

type OSFileSystem struct{}

func (OSFileSystem) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (OSFileSystem) WriteFile(path string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// WriteFileAtomic durably publishes one complete governed file version. A
// process crash can leave the old path or the new path, never a truncated
// in-place write that matches neither side of the workspace journal CAS.
func (OSFileSystem) WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	temporaryPath := atomicWorkspaceStagingPath(path, hashWorkspaceContent(data))
	// A previous process may have died before rename. The name is reserved for
	// CrestSpec under the project commit lock, and O_EXCL prevents following a
	// concurrently substituted symlink.
	if err := os.Remove(temporaryPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale atomic temporary: %w", err)
	}
	temporary, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create atomic temporary: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(perm.Perm()); err != nil {
		return fmt.Errorf("chmod atomic temporary: %w", err)
	}
	for offset := 0; offset < len(data); {
		written, writeErr := temporary.Write(data[offset:])
		if writeErr != nil {
			return fmt.Errorf("write atomic temporary: %w", writeErr)
		}
		if written == 0 {
			return fmt.Errorf("write atomic temporary: zero-byte write")
		}
		offset += written
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync atomic temporary: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close atomic temporary: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish atomic file: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open atomic directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync atomic directory: %w", err)
	}
	return nil
}

func atomicWorkspaceStagingPath(path, contentHash string) string {
	if contentHash == "" {
		hash := sha256.Sum256(nil)
		contentHash = fmt.Sprintf("%x", hash)
	}
	return filepath.Join(filepath.Dir(path), ".crest-spec-stage-"+contentHash+".tmp")
}

func (OSFileSystem) Chmod(path string, mode fs.FileMode) error    { return os.Chmod(path, mode) }
func (OSFileSystem) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }
func (OSFileSystem) Remove(path string) error                     { return os.Remove(path) }
func (OSFileSystem) ReadDir(path string) ([]os.DirEntry, error)   { return os.ReadDir(path) }
func (OSFileSystem) Stat(path string) (fs.FileInfo, error)        { return os.Stat(path) }
func (OSFileSystem) Lstat(path string) (fs.FileInfo, error)       { return os.Lstat(path) }
