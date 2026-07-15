package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Project owns conversion between persisted project-relative paths and paths
// used at the filesystem boundary.
type Project struct {
	root string
}

// FromSpecDir derives the project root from the directory containing the CUE
// specification. Relative configuration is anchored when the Project is
// created so later working-directory changes cannot alter path interpretation.
func FromSpecDir(specDir string) Project {
	root := filepath.Dir(specDir)
	absolute, err := filepath.Abs(root)
	if err == nil {
		root = absolute
	}
	return Project{root: filepath.Clean(root)}
}

// FromRoot creates a Project for callers that already know the project root.
func FromRoot(root string) Project {
	absolute, err := filepath.Abs(root)
	if err == nil {
		root = absolute
	}
	return Project{root: filepath.Clean(root)}
}

func (p Project) Root() string {
	return p.root
}

// Resolve returns an absolute, contained filesystem path. Existing symlinked
// prefixes are checked against the real project root so a project-relative
// path cannot escape through a symlink.
func (p Project) Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("workspace path is required")
	}
	root := p.root
	if root == "" {
		root = FromRoot(".").root
	}

	candidate := filepath.Clean(filepath.FromSlash(path))
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	if !contains(root, candidate) || candidate == root {
		return "", fmt.Errorf("workspace path %q is outside project root", path)
	}
	if err := ensureExistingPrefixContained(root, candidate); err != nil {
		return "", fmt.Errorf("workspace path %q: %w", path, err)
	}
	return candidate, nil
}

// Relative normalizes a contained path for canonical SQLite persistence.
func (p Project) Relative(path string) (string, error) {
	resolved, err := p.Resolve(path)
	if err != nil {
		return "", err
	}
	root := p.root
	if root == "" {
		root = FromRoot(".").root
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", fmt.Errorf("make workspace path relative: %w", err)
	}
	return filepath.ToSlash(relative), nil
}

func contains(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ensureExistingPrefixContained(root, candidate string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("resolve project root: %w", err)
	}

	prefix := candidate
	for {
		_, statErr := os.Lstat(prefix)
		if statErr == nil {
			break
		}
		if !os.IsNotExist(statErr) {
			return fmt.Errorf("inspect path prefix: %w", statErr)
		}
		parent := filepath.Dir(prefix)
		if parent == prefix {
			return nil
		}
		prefix = parent
	}
	realPrefix, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		return fmt.Errorf("resolve path prefix: %w", err)
	}
	if !contains(realRoot, realPrefix) && realPrefix != realRoot {
		return fmt.Errorf("resolves outside project root")
	}
	return nil
}
