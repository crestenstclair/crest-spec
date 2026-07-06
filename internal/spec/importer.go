package spec

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ImportOpts configures a spec import operation.
type ImportOpts struct {
	Directory  string // directory to scan
	Language   string // language hint (go, rust, typescript, python)
	OutputFile string // where to write the CUE output (default: spec/imported.cue)
	DryRun     bool   // if true, return the CUE without writing
}

// ImportResult holds the output of a spec import.
type ImportResult struct {
	CueOutput     string `json:"cue_output"`
	FilesScanned  int    `json:"files_scanned"`
	ResourceCount int    `json:"resource_count"`
	OutputPath    string `json:"output_path"`
}

// resourceKind is the classification of a source file.
type resourceKind string

const (
	kindAggregate          resourceKind = "aggregate"
	kindValueObject        resourceKind = "valueObject"
	kindDomainService      resourceKind = "domainService"
	kindApplicationService resourceKind = "applicationService"
	kindRepository         resourceKind = "repository"
	kindPort               resourceKind = "port"
	kindAdapter            resourceKind = "adapter"
)

// classifiedFile holds a source file and its inferred resource classification.
type classifiedFile struct {
	path    string
	name    string
	kind    resourceKind
	context string // bounded context inferred from directory
}

// Import scans a directory of source files and generates a skeleton CUE spec.
func (s *Spec) Import(ctx context.Context, opts ImportOpts) (*ImportResult, error) {
	if opts.Directory == "" {
		return nil, fmt.Errorf("directory is required")
	}

	if opts.OutputFile == "" {
		opts.OutputFile = filepath.Join("spec", "imported.cue")
	}

	lang := normalizeLanguage(opts.Language)
	extensions := languageExtensions(lang)

	files, err := s.collectSourceFiles(opts.Directory, extensions)
	if err != nil {
		return nil, fmt.Errorf("scanning directory: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no source files found in %s for language %q", opts.Directory, lang)
	}

	classified := classifyFiles(files, opts.Directory)
	cueOutput := generateCUE(classified, lang)
	resourceCount := countResources(classified)

	result := &ImportResult{
		CueOutput:     cueOutput,
		FilesScanned:  len(files),
		ResourceCount: resourceCount,
		OutputPath:    opts.OutputFile,
	}

	if !opts.DryRun {
		dir := filepath.Dir(opts.OutputFile)
		if err := s.fs.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating output directory: %w", err)
		}
		if err := s.fs.WriteFile(opts.OutputFile, []byte(cueOutput), 0o644); err != nil {
			return nil, fmt.Errorf("writing output: %w", err)
		}
	}

	return result, nil
}

// collectSourceFiles walks the directory tree and returns paths matching the given extensions.
func (s *Spec) collectSourceFiles(dir string, extensions map[string]bool) ([]string, error) {
	var files []string

	var walkDir func(path string) error
	walkDir = func(path string) error {
		entries, err := s.fs.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			fullPath := filepath.Join(path, entry.Name())
			if entry.IsDir() {
				// Skip hidden directories and common non-source directories
				if shouldSkipDir(entry.Name()) {
					continue
				}
				if err := walkDir(fullPath); err != nil {
					return err
				}
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if extensions[ext] {
				files = append(files, fullPath)
			}
		}
		return nil
	}

	if err := walkDir(dir); err != nil {
		return nil, err
	}
	return files, nil
}

// shouldSkipDir returns true for directories that should not be scanned.
func shouldSkipDir(name string) bool {
	skip := map[string]bool{
		".git":         true,
		".svn":         true,
		"node_modules": true,
		"vendor":       true,
		"target":       true,
		"build":        true,
		"dist":         true,
		"__pycache__":  true,
		".idea":        true,
		".vscode":      true,
	}
	return strings.HasPrefix(name, ".") || skip[name]
}

// classifyFiles classifies each file by its name/path heuristics and groups by directory.
func classifyFiles(files []string, baseDir string) []classifiedFile {
	var classified []classifiedFile
	for _, f := range files {
		relPath, _ := filepath.Rel(baseDir, f)
		name := fileNameWithoutExt(filepath.Base(f))
		ctxName := inferContext(relPath, baseDir)
		kind := classifyByName(name, relPath)

		classified = append(classified, classifiedFile{
			path:    relPath,
			name:    name,
			kind:    kind,
			context: ctxName,
		})
	}
	return classified
}

// classifyByName determines the resource kind based on filename/path heuristics.
func classifyByName(name, path string) resourceKind {
	lower := strings.ToLower(name)
	lowerPath := strings.ToLower(path)

	// Check path components too, not just the filename
	combined := lower + " " + lowerPath

	switch {
	case strings.Contains(combined, "repository") || strings.Contains(combined, "repo"):
		return kindRepository
	case strings.Contains(combined, "service") && (strings.Contains(combined, "domain") || strings.Contains(combined, "svc")):
		return kindDomainService
	case strings.Contains(combined, "adapter"):
		return kindAdapter
	case strings.Contains(combined, "handler") || strings.Contains(combined, "controller"):
		return kindApplicationService
	case strings.Contains(combined, "service"):
		return kindDomainService
	case strings.Contains(combined, "model") || strings.Contains(combined, "entity") || strings.Contains(combined, "aggregate"):
		return kindAggregate
	case strings.Contains(combined, "value") || strings.Contains(lower, "vo_") || strings.HasSuffix(lower, "_vo"):
		return kindValueObject
	case strings.Contains(combined, "port") || strings.Contains(combined, "interface"):
		return kindPort
	default:
		return kindAggregate
	}
}

// inferContext derives a bounded context name from the directory structure.
func inferContext(relPath, baseDir string) string {
	dir := filepath.Dir(relPath)
	if dir == "." || dir == "" {
		return "Default"
	}

	// Use the first directory component as the context name
	parts := strings.Split(filepath.ToSlash(dir), "/")
	name := parts[0]

	return toPascalCase(name)
}

type contextData struct {
	aggregates   map[string]classifiedFile
	valueObjects map[string]classifiedFile
	domainSvcs   map[string]classifiedFile
	appSvcs      map[string]classifiedFile
	repositories map[string]classifiedFile
	ports        map[string]classifiedFile
	adapters     map[string]classifiedFile
}

// generateCUE produces valid CUE content from classified files.
func generateCUE(files []classifiedFile, language string) string {
	contexts := make(map[string]*contextData)
	for _, f := range files {
		cd, ok := contexts[f.context]
		if !ok {
			cd = &contextData{
				aggregates:   make(map[string]classifiedFile),
				valueObjects: make(map[string]classifiedFile),
				domainSvcs:   make(map[string]classifiedFile),
				appSvcs:      make(map[string]classifiedFile),
				repositories: make(map[string]classifiedFile),
				ports:        make(map[string]classifiedFile),
				adapters:     make(map[string]classifiedFile),
			}
			contexts[f.context] = cd
		}

		resourceName := toPascalCase(f.name)
		switch f.kind {
		case kindAggregate:
			cd.aggregates[resourceName] = f
		case kindValueObject:
			cd.valueObjects[resourceName] = f
		case kindDomainService:
			cd.domainSvcs[resourceName] = f
		case kindApplicationService:
			cd.appSvcs[resourceName] = f
		case kindRepository:
			cd.repositories[resourceName] = f
		case kindPort:
			cd.ports[resourceName] = f
		case kindAdapter:
			cd.adapters[resourceName] = f
		}
	}

	var b strings.Builder
	b.WriteString("package spec\n\n")
	b.WriteString("project: {\n")
	b.WriteString("\tname: \"imported-project\"\n")
	b.WriteString("\tlayers: [\"domain\", \"application\", \"infrastructure\"]\n")
	b.WriteString("\tmeta: {\n")
	b.WriteString(fmt.Sprintf("\t\tlanguage: %q\n", language))
	b.WriteString("\t}\n")
	b.WriteString("\tcontexts: {\n")

	// Sort context names for deterministic output
	contextNames := make([]string, 0, len(contexts))
	for name := range contexts {
		contextNames = append(contextNames, name)
	}
	sort.Strings(contextNames)

	for _, ctxName := range contextNames {
		cd := contexts[ctxName]
		b.WriteString(fmt.Sprintf("\t\t%s: {\n", ctxName))
		b.WriteString(fmt.Sprintf("\t\t\tpurpose: \"Imported from %s\"\n", strings.ToLower(ctxName)))

		writeResourceMap(&b, "aggregates", cd.aggregates, 3)
		writeResourceMap(&b, "valueObjects", cd.valueObjects, 3)
		writeResourceMap(&b, "domainServices", cd.domainSvcs, 3)
		writeResourceMap(&b, "applicationServices", cd.appSvcs, 3)
		writeRepositoryMap(&b, cd.repositories, 3)
		writeResourceMap(&b, "ports", cd.ports, 3)

		b.WriteString("\t\t}\n")
	}

	b.WriteString("\t}\n")

	// Write adapters at project level (they're outside contexts)
	writeAdapterMap(&b, contexts)

	b.WriteString("}\n")

	return b.String()
}

// writeResourceMap writes a CUE map block for a set of resources.
func writeResourceMap(b *strings.Builder, section string, resources map[string]classifiedFile, indent int) {
	if len(resources) == 0 {
		return
	}

	tabs := strings.Repeat("\t", indent)
	b.WriteString(fmt.Sprintf("%s%s: {\n", tabs, section))

	names := sortedKeys(resources)
	for _, name := range names {
		f := resources[name]
		b.WriteString(fmt.Sprintf("%s\t%s: {\n", tabs, name))
		b.WriteString(fmt.Sprintf("%s\t\tpurpose: \"Imported from %s\"\n", tabs, f.path))
		b.WriteString(fmt.Sprintf("%s\t}\n", tabs))
	}

	b.WriteString(fmt.Sprintf("%s}\n", tabs))
}

// writeRepositoryMap writes a CUE map block for repositories with the "of" field.
func writeRepositoryMap(b *strings.Builder, resources map[string]classifiedFile, indent int) {
	if len(resources) == 0 {
		return
	}

	tabs := strings.Repeat("\t", indent)
	b.WriteString(fmt.Sprintf("%srepositories: {\n", tabs))

	names := sortedKeys(resources)
	for _, name := range names {
		f := resources[name]
		// Infer the aggregate name from the repository name
		aggName := strings.TrimSuffix(name, "Repository")
		aggName = strings.TrimSuffix(aggName, "Repo")
		if aggName == "" {
			aggName = name
		}
		b.WriteString(fmt.Sprintf("%s\t%s: {\n", tabs, name))
		b.WriteString(fmt.Sprintf("%s\t\tof: \"%s.%s\"\n", tabs, f.context, aggName))
		b.WriteString(fmt.Sprintf("%s\t}\n", tabs))
	}

	b.WriteString(fmt.Sprintf("%s}\n", tabs))
}

// writeAdapterMap writes adapters at the project level.
func writeAdapterMap(b *strings.Builder, contexts map[string]*contextData) {
	// Collect all adapters across contexts
	type adapterEntry struct {
		name    string
		file    classifiedFile
		ctxName string
	}
	var adapters []adapterEntry
	for ctxName, cd := range contexts {
		for name, f := range cd.adapters {
			adapters = append(adapters, adapterEntry{name: name, file: f, ctxName: ctxName})
		}
	}

	if len(adapters) == 0 {
		return
	}

	sort.Slice(adapters, func(i, j int) bool {
		return adapters[i].name < adapters[j].name
	})

	b.WriteString("\tadapters: {\n")
	for _, a := range adapters {
		b.WriteString(fmt.Sprintf("\t\t%s: {\n", a.name))
		b.WriteString(fmt.Sprintf("\t\t\timplements: \"%s.TODO_PORT\"\n", a.ctxName))
		b.WriteString(fmt.Sprintf("\t\t}\n"))
	}
	b.WriteString("\t}\n")
}

// countResources counts the total number of classified resources (deduplicated by name+context).
func countResources(files []classifiedFile) int {
	seen := make(map[string]bool)
	for _, f := range files {
		key := f.context + "." + toPascalCase(f.name) + "." + string(f.kind)
		seen[key] = true
	}
	return len(seen)
}

// normalizeLanguage maps language aliases to canonical names.
func normalizeLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "go", "golang":
		return "go"
	case "rust", "rs":
		return "rust"
	case "typescript", "ts":
		return "typescript"
	case "python", "py":
		return "python"
	case "java":
		return "java"
	case "csharp", "c#", "cs":
		return "csharp"
	case "":
		return "go"
	default:
		return strings.ToLower(strings.TrimSpace(lang))
	}
}

// languageExtensions returns the file extensions for a language.
func languageExtensions(lang string) map[string]bool {
	switch lang {
	case "go":
		return map[string]bool{".go": true}
	case "rust":
		return map[string]bool{".rs": true}
	case "typescript":
		return map[string]bool{".ts": true, ".tsx": true}
	case "python":
		return map[string]bool{".py": true}
	case "java":
		return map[string]bool{".java": true}
	case "csharp":
		return map[string]bool{".cs": true}
	default:
		return map[string]bool{".go": true}
	}
}

// fileNameWithoutExt returns the filename without its extension.
func fileNameWithoutExt(name string) string {
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext)
}

// toPascalCase converts a snake_case or kebab-case string to PascalCase.
func toPascalCase(s string) string {
	// Split on underscores, hyphens, and dots
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})

	var result strings.Builder
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		result.WriteString(strings.ToUpper(part[:1]))
		if len(part) > 1 {
			result.WriteString(part[1:])
		}
	}

	out := result.String()
	if out == "" {
		return s
	}
	return out
}

// sortedKeys returns the keys of a map sorted alphabetically.
func sortedKeys(m map[string]classifiedFile) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
