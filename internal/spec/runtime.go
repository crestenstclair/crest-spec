package spec

import (
	"os"
	"path/filepath"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/prompt"
)

func (s *Spec) buildRuntimeContext(resource cuepkg.Resource, registry *cuepkg.Registry, applyID string) (prompt.RuntimeContext, error) {
	ctx := prompt.RuntimeContext{}

	srcDir := filepath.Join(filepath.Dir(s.cfg.SpecDir), "src")
	tree, err := buildModuleTree(s.fs, srcDir)
	if err == nil && tree != "" {
		ctx.ModuleTree = tree
	}

	depFiles := make(map[string]string)
	for _, dep := range resource.Dependencies {
		files, err := s.store.GetGeneratedFiles(dep.TargetID)
		if err != nil {
			continue
		}
		for _, f := range files {
			if strings.Contains(f.Path, "_test") || strings.Contains(f.Path, "test_") {
				continue
			}
			data, err := s.fs.ReadFile(f.Path)
			if err != nil {
				continue
			}
			depFiles[dep.TargetID] = string(data)
			break
		}
	}
	if len(depFiles) > 0 {
		ctx.DependencyFiles = depFiles
	}

	if applyID != "" {
		notes := make(map[string]string)
		for _, dep := range resource.Dependencies {
			content, err := s.store.GetNote(dep.TargetID, applyID)
			if err != nil || content == "" {
				continue
			}
			notes[dep.TargetID] = content
		}
		if len(notes) > 0 {
			ctx.AgentNotes = notes
		}
	}

	return ctx, nil
}

func buildModuleTree(fs fileSystem, dir string) (string, error) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return "", nil
	}

	var b strings.Builder
	buildTreeRecursive(fs, dir, "", &b, entries)
	return b.String(), nil
}

func buildTreeRecursive(fsys fileSystem, basePath, prefix string, b *strings.Builder, entries []os.DirEntry) {
	for i, e := range entries {
		connector := "├── "
		if i == len(entries)-1 {
			connector = "└── "
		}
		b.WriteString(prefix + connector + e.Name() + "\n")

		if e.IsDir() {
			childPrefix := prefix + "│   "
			if i == len(entries)-1 {
				childPrefix = prefix + "    "
			}
			childEntries, err := fsys.ReadDir(filepath.Join(basePath, e.Name()))
			if err != nil {
				continue
			}
			buildTreeRecursive(fsys, filepath.Join(basePath, e.Name()), childPrefix, b, childEntries)
		}
	}
}
