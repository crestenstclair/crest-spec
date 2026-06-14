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

	// Collect existing module declaration files so the model can update them
	modFiles := collectModuleFiles(s.fs, srcDir, registry.Project.Meta.Language)
	if len(modFiles) > 0 {
		ctx.ModuleFiles = modFiles
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

	// Inject craft-level learnings scoped to this language + resource kind.
	const learningsInjectionCap = 10
	lang := registry.Project.Meta.Language
	if lang != "" {
		learnings, err := s.store.ListActiveLearnings(lang, resource.Kind, learningsInjectionCap)
		if err == nil && len(learnings) > 0 {
			texts := make([]string, len(learnings))
			for i, l := range learnings {
				texts[i] = l.Text
				_ = s.store.IncrementLearningApplied(l.ID)
			}
			ctx.Learnings = texts
		}
	}

	// UPDATE mode: if this resource already has committed output, feed the
	// existing files back and flag any PENDING amendments as the changes to make.
	committed, _ := s.store.GetGeneratedFiles(resource.ID)
	if len(committed) > 0 {
		existing := make(map[string]string, len(committed))
		for _, f := range committed {
			data, err := s.fs.ReadFile(f.Path)
			if err != nil {
				continue
			}
			existing[f.Path] = string(data)
		}
		if len(existing) > 0 {
			ctx.ExistingFiles = existing
		}
		if changes := s.pendingAmendmentChanges(resource.ID); changes != "" {
			ctx.ChangesRequired = changes
		}
	}

	return ctx, nil
}

// collectModuleFiles reads existing files the model must keep in sync when it
// adds new code: module declarations from the src/ tree (lib.rs, mod.rs, ...)
// and the project manifest from the project root (Cargo.toml, package.json).
// These are passed to the LLM so it can include them in its output with new
// modules and crate dependencies ADDED rather than clobbering them.
func collectModuleFiles(fs fileSystem, srcDir, lang string) map[string]string {
	if lang == "" {
		return nil
	}

	var (
		modulePatterns   []string // searched recursively under src/
		manifestPatterns []string // searched in the project root only
	)
	switch lang {
	case "rust":
		modulePatterns = []string{"lib.rs", "mod.rs"}
		manifestPatterns = []string{"Cargo.toml"}
	case "python":
		modulePatterns = []string{"__init__.py"}
	case "typescript", "javascript":
		modulePatterns = []string{"index.ts", "index.js"}
		manifestPatterns = []string{"package.json"}
	default:
		return nil
	}

	files := make(map[string]string)
	collectModuleFilesRecursive(fs, srcDir, srcDir, modulePatterns, files)
	if len(manifestPatterns) > 0 {
		collectFilesInDir(fs, filepath.Dir(srcDir), srcDir, manifestPatterns, files)
	}
	if len(files) == 0 {
		return nil
	}
	return files
}

// collectFilesInDir reads matching files directly in dir (non-recursively),
// keying them by their path relative to filepath.Dir(baseDir) — matching the
// keys produced by collectModuleFilesRecursive.
func collectFilesInDir(fs fileSystem, dir, baseDir string, patterns []string, files map[string]string) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		for _, p := range patterns {
			if e.Name() != p {
				continue
			}
			fullPath := filepath.Join(dir, e.Name())
			data, err := fs.ReadFile(fullPath)
			if err != nil {
				continue
			}
			if content := strings.TrimSpace(string(data)); content != "" {
				relPath, _ := filepath.Rel(filepath.Dir(baseDir), fullPath)
				files[relPath] = content
			}
		}
	}
}

func collectModuleFilesRecursive(fs fileSystem, baseDir, dir string, patterns []string, files map[string]string) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if e.IsDir() {
			collectModuleFilesRecursive(fs, baseDir, filepath.Join(dir, e.Name()), patterns, files)
			continue
		}
		for _, p := range patterns {
			if e.Name() == p {
				fullPath := filepath.Join(dir, e.Name())
				data, err := fs.ReadFile(fullPath)
				if err != nil {
					continue
				}
				content := strings.TrimSpace(string(data))
				if content != "" {
					relPath, _ := filepath.Rel(filepath.Dir(baseDir), fullPath)
					files[relPath] = content
				}
			}
		}
	}
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
