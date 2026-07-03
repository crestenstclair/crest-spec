package spec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

var discoverExtensions = map[string]string{
	"rust":       ".rs",
	"go":         ".go",
	"python":     ".py",
	"typescript": ".ts",
	"javascript": ".js",
}

// DiscoverResourceFiles maps a resource with no tracked files to on-disk
// files by naming convention: the resource's short name, snake_cased, with
// the language's extension, anywhere under srcDir. Exactly one candidate is
// a claim; zero or multiple candidates return nothing — ambiguity is for the
// caller to surface, never to guess through.
func DiscoverResourceFiles(fs fileSystem, srcDir string, r cuepkg.Resource, lang string) ([]string, error) {
	ext, ok := discoverExtensions[lang]
	if !ok {
		return nil, nil
	}
	parts := strings.Split(r.ID, ".")
	short := parts[len(parts)-1]
	want := snakeCase(short) + ext

	var matches []string
	walk(fs, srcDir, func(path string, entry os.DirEntry) {
		if !entry.IsDir() && entry.Name() == want {
			matches = append(matches, path)
		}
	})
	if len(matches) != 1 {
		return nil, nil
	}
	return matches, nil
}

// DiscoverClaims returns path-only CommitFiles ("claims") for a resource that
// has no tracked files yet, mapping it to on-disk code by naming convention.
// Used by `crest-spec adopt` so adopted resources keep UPDATE-mode iteration.
func (s *Spec) DiscoverClaims(ctx context.Context, resourceID string) []CommitFile {
	if existing, err := s.store.GetGeneratedFiles(resourceID); err == nil && len(existing) > 0 {
		return nil
	}
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil
	}
	r, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil
	}
	srcDir := filepath.Join(filepath.Dir(s.cfg.SpecDir), "src")
	found, _ := DiscoverResourceFiles(s.fs, srcDir, r, planResult.Registry.Project.Meta.Language)
	claims := make([]CommitFile, 0, len(found))
	for _, p := range found {
		claims = append(claims, CommitFile{Path: p})
	}
	return claims
}

func walk(fs fileSystem, dir string, fn func(path string, entry os.DirEntry)) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		fn(full, e)
		if e.IsDir() {
			walk(fs, full, fn)
		}
	}
}

func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
