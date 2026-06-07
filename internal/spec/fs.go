package spec

import (
	"io/fs"
	"os"
)

type fileSystem interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Remove(path string) error
	ReadDir(path string) ([]os.DirEntry, error)
	Stat(path string) (fs.FileInfo, error)
}

type OSFileSystem struct{}

func (OSFileSystem) ReadFile(path string) ([]byte, error)                       { return os.ReadFile(path) }
func (OSFileSystem) WriteFile(path string, data []byte, perm fs.FileMode) error { return os.WriteFile(path, data, perm) }
func (OSFileSystem) MkdirAll(path string, perm fs.FileMode) error              { return os.MkdirAll(path, perm) }
func (OSFileSystem) Remove(path string) error                                  { return os.Remove(path) }
func (OSFileSystem) ReadDir(path string) ([]os.DirEntry, error)                { return os.ReadDir(path) }
func (OSFileSystem) Stat(path string) (fs.FileInfo, error)                     { return os.Stat(path) }
