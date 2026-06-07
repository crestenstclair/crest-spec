package spec

import (
	"context"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
)

// memFS is an in-memory filesystem for testing.
type memFS struct {
	files map[string][]byte
	dirs  map[string]bool
}

func newMemFS() *memFS {
	return &memFS{
		files: make(map[string][]byte),
		dirs:  make(map[string]bool),
	}
}

func (m *memFS) ReadFile(path string) ([]byte, error) {
	data, ok := m.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (m *memFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	m.files[path] = data
	return nil
}

func (m *memFS) MkdirAll(path string, perm fs.FileMode) error {
	m.dirs[path] = true
	return nil
}

func (m *memFS) Remove(path string) error {
	delete(m.files, path)
	delete(m.dirs, path)
	return nil
}

func (m *memFS) ReadDir(path string) ([]os.DirEntry, error) {
	var entries []os.DirEntry
	seen := make(map[string]bool)

	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	for p := range m.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		parts := strings.SplitN(rest, "/", 2)
		name := parts[0]
		if seen[name] {
			continue
		}
		seen[name] = true

		isDir := len(parts) > 1
		entries = append(entries, memDirEntry{name: name, dir: isDir})
	}

	// Also check explicit directories
	for d := range m.dirs {
		if !strings.HasPrefix(d, prefix) {
			continue
		}
		rest := strings.TrimPrefix(d, prefix)
		parts := strings.SplitN(rest, "/", 2)
		name := parts[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, memDirEntry{name: name, dir: true})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}

func (m *memFS) Stat(path string) (fs.FileInfo, error) {
	if _, ok := m.files[path]; ok {
		return memFileInfo{name: path, dir: false}, nil
	}
	if m.dirs[path] {
		return memFileInfo{name: path, dir: true}, nil
	}
	return nil, os.ErrNotExist
}

type memDirEntry struct {
	name string
	dir  bool
}

func (e memDirEntry) Name() string               { return e.name }
func (e memDirEntry) IsDir() bool                 { return e.dir }
func (e memDirEntry) Type() fs.FileMode           { if e.dir { return fs.ModeDir }; return 0 }
func (e memDirEntry) Info() (fs.FileInfo, error)   { return memFileInfo{name: e.name, dir: e.dir}, nil }

type memFileInfo struct {
	name string
	dir  bool
}

func (i memFileInfo) Name() string      { return i.name }
func (i memFileInfo) Size() int64       { return 0 }
func (i memFileInfo) Mode() fs.FileMode { if i.dir { return fs.ModeDir | 0o755 }; return 0o644 }
func (i memFileInfo) ModTime() time.Time { return time.Time{} }
func (i memFileInfo) IsDir() bool       { return i.dir }
func (i memFileInfo) Sys() any          { return nil }

func newTestSpec(mfs *memFS) *Spec {
	return &Spec{
		fs:  mfs,
		cfg: &config.Config{},
	}
}

// --- Classification tests ---

func TestClassifyByName(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected resourceKind
	}{
		{"user_repository", "domain/user_repository.go", kindRepository},
		{"UserRepo", "repos/UserRepo.go", kindRepository},
		{"order_service", "domain/order_service.go", kindDomainService},
		{"PaymentDomainService", "payment/PaymentDomainService.go", kindDomainService},
		{"db_adapter", "infra/db_adapter.go", kindAdapter},
		{"order_handler", "api/order_handler.go", kindApplicationService},
		{"UserController", "controllers/UserController.go", kindApplicationService},
		{"user_model", "models/user_model.go", kindAggregate},
		{"Order", "domain/Order.go", kindAggregate},
		{"money_value", "domain/money_value.go", kindValueObject},
		{"vo_address", "domain/vo_address.go", kindValueObject},
		{"price_vo", "domain/price_vo.go", kindValueObject},
		{"user_port", "ports/user_port.go", kindPort},
		{"EventInterface", "domain/EventInterface.go", kindPort},
		{"something_random", "lib/something_random.go", kindAggregate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyByName(tt.name, tt.path)
			assert.Equal(t, tt.expected, got, "classifyByName(%q, %q)", tt.name, tt.path)
		})
	}
}

func TestNormalizeLanguage(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"go", "go"},
		{"Go", "go"},
		{"golang", "go"},
		{"rust", "rust"},
		{"rs", "rust"},
		{"typescript", "typescript"},
		{"ts", "typescript"},
		{"python", "python"},
		{"py", "python"},
		{"", "go"},
		{"java", "java"},
		{"csharp", "csharp"},
		{"c#", "csharp"},
		{"ruby", "ruby"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeLanguage(tt.input))
		})
	}
}

func TestLanguageExtensions(t *testing.T) {
	assert.True(t, languageExtensions("go")[".go"])
	assert.True(t, languageExtensions("rust")[".rs"])
	assert.True(t, languageExtensions("typescript")[".ts"])
	assert.True(t, languageExtensions("typescript")[".tsx"])
	assert.True(t, languageExtensions("python")[".py"])
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user_repository", "UserRepository"},
		{"order-handler", "OrderHandler"},
		{"simple", "Simple"},
		{"already_Pascal", "AlreadyPascal"},
		{"multi_word_name", "MultiWordName"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, toPascalCase(tt.input))
		})
	}
}

func TestInferContext(t *testing.T) {
	tests := []struct {
		relPath  string
		expected string
	}{
		{"user.go", "Default"},
		{"domain/user.go", "Domain"},
		{"order/models/order.go", "Order"},
		{"payment_service/handler.go", "PaymentService"},
	}

	for _, tt := range tests {
		t.Run(tt.relPath, func(t *testing.T) {
			assert.Equal(t, tt.expected, inferContext(tt.relPath, "/base"))
		})
	}
}

func TestShouldSkipDir(t *testing.T) {
	assert.True(t, shouldSkipDir(".git"))
	assert.True(t, shouldSkipDir("node_modules"))
	assert.True(t, shouldSkipDir("vendor"))
	assert.True(t, shouldSkipDir(".hidden"))
	assert.False(t, shouldSkipDir("src"))
	assert.False(t, shouldSkipDir("internal"))
}

// --- Integration tests ---

func TestImport_DryRun(t *testing.T) {
	mfs := newMemFS()
	mfs.files["/project/domain/user.go"] = []byte("package domain")
	mfs.files["/project/domain/user_repository.go"] = []byte("package domain")
	mfs.files["/project/api/order_handler.go"] = []byte("package api")

	s := newTestSpec(mfs)
	result, err := s.Import(context.Background(), ImportOpts{
		Directory: "/project",
		Language:  "go",
		DryRun:    true,
	})

	require.NoError(t, err)
	assert.Equal(t, 3, result.FilesScanned)
	assert.Greater(t, result.ResourceCount, 0)
	assert.Contains(t, result.CueOutput, "package spec")
	assert.Contains(t, result.CueOutput, "project:")
	assert.Contains(t, result.CueOutput, "contexts:")
	assert.Contains(t, result.CueOutput, `language: "go"`)

	// Verify no file was written in dry run
	_, exists := mfs.files[result.OutputPath]
	assert.False(t, exists, "dry run should not write files")
}

func TestImport_WritesFile(t *testing.T) {
	mfs := newMemFS()
	mfs.files["/project/domain/user.go"] = []byte("package domain")

	s := newTestSpec(mfs)
	result, err := s.Import(context.Background(), ImportOpts{
		Directory:  "/project",
		Language:   "go",
		OutputFile: "/output/spec.cue",
	})

	require.NoError(t, err)
	assert.Equal(t, "/output/spec.cue", result.OutputPath)

	written, ok := mfs.files["/output/spec.cue"]
	require.True(t, ok, "file should have been written")
	assert.Equal(t, result.CueOutput, string(written))
}

func TestImport_EmptyDirectory(t *testing.T) {
	mfs := newMemFS()
	mfs.dirs["/empty"] = true

	s := newTestSpec(mfs)
	_, err := s.Import(context.Background(), ImportOpts{
		Directory: "/empty",
		Language:  "go",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no source files found")
}

func TestImport_MissingDirectory(t *testing.T) {
	mfs := newMemFS()
	s := newTestSpec(mfs)

	_, err := s.Import(context.Background(), ImportOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "directory is required")
}

func TestImport_MultipleContexts(t *testing.T) {
	mfs := newMemFS()
	mfs.files["/project/orders/order.go"] = []byte("package orders")
	mfs.files["/project/orders/order_repository.go"] = []byte("package orders")
	mfs.files["/project/payments/payment_service.go"] = []byte("package payments")
	mfs.files["/project/payments/payment_adapter.go"] = []byte("package payments")

	s := newTestSpec(mfs)
	result, err := s.Import(context.Background(), ImportOpts{
		Directory: "/project",
		Language:  "go",
		DryRun:    true,
	})

	require.NoError(t, err)
	assert.Equal(t, 4, result.FilesScanned)

	// Both contexts should appear
	assert.Contains(t, result.CueOutput, "Orders:")
	assert.Contains(t, result.CueOutput, "Payments:")

	// Repositories should show up under Orders
	assert.Contains(t, result.CueOutput, "repositories:")
	assert.Contains(t, result.CueOutput, "OrderRepository:")

	// Adapters should show up at project level
	assert.Contains(t, result.CueOutput, "adapters:")
	assert.Contains(t, result.CueOutput, "PaymentAdapter:")
}

func TestImport_TypescriptExtensions(t *testing.T) {
	mfs := newMemFS()
	mfs.files["/project/src/user.ts"] = []byte("export class User {}")
	mfs.files["/project/src/user.tsx"] = []byte("export function UserView() {}")
	mfs.files["/project/src/user.go"] = []byte("package main") // should be ignored

	s := newTestSpec(mfs)
	result, err := s.Import(context.Background(), ImportOpts{
		Directory: "/project",
		Language:  "typescript",
		DryRun:    true,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, result.FilesScanned)
	assert.Contains(t, result.CueOutput, `language: "typescript"`)
}

func TestImport_SkipsHiddenDirs(t *testing.T) {
	mfs := newMemFS()
	mfs.files["/project/src/user.go"] = []byte("package src")
	mfs.files["/project/.git/config"] = []byte("config")
	mfs.files["/project/vendor/dep.go"] = []byte("package vendor")

	s := newTestSpec(mfs)
	result, err := s.Import(context.Background(), ImportOpts{
		Directory: "/project",
		Language:  "go",
		DryRun:    true,
	})

	require.NoError(t, err)
	// Only src/user.go should be scanned, not .git/config or vendor/dep.go
	assert.Equal(t, 1, result.FilesScanned)
}

func TestImport_DefaultOutputPath(t *testing.T) {
	mfs := newMemFS()
	mfs.files["/project/main.go"] = []byte("package main")

	s := newTestSpec(mfs)
	result, err := s.Import(context.Background(), ImportOpts{
		Directory: "/project",
		Language:  "go",
		DryRun:    true,
	})

	require.NoError(t, err)
	assert.Equal(t, "spec/imported.cue", result.OutputPath)
}

func TestGenerateCUE_ValidStructure(t *testing.T) {
	files := []classifiedFile{
		{path: "domain/user.go", name: "user", kind: kindAggregate, context: "Auth"},
		{path: "domain/user_repository.go", name: "user_repository", kind: kindRepository, context: "Auth"},
		{path: "domain/email_value.go", name: "email_value", kind: kindValueObject, context: "Auth"},
		{path: "domain/auth_service.go", name: "auth_service", kind: kindDomainService, context: "Auth"},
		{path: "api/login_handler.go", name: "login_handler", kind: kindApplicationService, context: "Api"},
		{path: "infra/db_adapter.go", name: "db_adapter", kind: kindAdapter, context: "Infra"},
		{path: "domain/event_port.go", name: "event_port", kind: kindPort, context: "Auth"},
	}

	output := generateCUE(files, "go")

	// Verify structure
	assert.Contains(t, output, "package spec")
	assert.Contains(t, output, `name: "imported-project"`)
	assert.Contains(t, output, `layers: ["domain", "application", "infrastructure"]`)
	assert.Contains(t, output, `language: "go"`)

	// Verify contexts
	assert.Contains(t, output, "Auth:")
	assert.Contains(t, output, "Api:")

	// Verify resource sections
	assert.Contains(t, output, "aggregates:")
	assert.Contains(t, output, "User:")
	assert.Contains(t, output, "valueObjects:")
	assert.Contains(t, output, "EmailValue:")
	assert.Contains(t, output, "domainServices:")
	assert.Contains(t, output, "AuthService:")
	assert.Contains(t, output, "applicationServices:")
	assert.Contains(t, output, "LoginHandler:")
	assert.Contains(t, output, "repositories:")
	assert.Contains(t, output, "UserRepository:")
	assert.Contains(t, output, "ports:")
	assert.Contains(t, output, "EventPort:")

	// Verify adapters at project level
	assert.Contains(t, output, "adapters:")
	assert.Contains(t, output, "DbAdapter:")
	assert.Contains(t, output, `implements: "Infra.TODO_PORT"`)

	// Verify repository has "of" field
	assert.Contains(t, output, `of: "Auth.User"`)
}
