package spec

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Fake filesystem for bootstrap tests
// ---------------------------------------------------------------------------

type fakeFS struct {
	files    map[string][]byte
	dirs     map[string]bool
	statErr  map[string]error
	writeErr map[string]error
}

func newFakeFS() *fakeFS {
	return &fakeFS{
		files:    make(map[string][]byte),
		dirs:     make(map[string]bool),
		statErr:  make(map[string]error),
		writeErr: make(map[string]error),
	}
}

func (f *fakeFS) ReadFile(path string) ([]byte, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (f *fakeFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	if err, ok := f.writeErr[path]; ok {
		return err
	}
	f.files[path] = data
	return nil
}

func (f *fakeFS) MkdirAll(path string, perm fs.FileMode) error {
	f.dirs[path] = true
	return nil
}

func (f *fakeFS) Remove(path string) error                   { return nil }
func (f *fakeFS) ReadDir(path string) ([]os.DirEntry, error) { return nil, nil }

func (f *fakeFS) Stat(path string) (fs.FileInfo, error) {
	if err, ok := f.statErr[path]; ok {
		return nil, err
	}
	if f.dirs[path] {
		return fakeFileInfo{name: path, isDir: true}, nil
	}
	if _, ok := f.files[path]; ok {
		return fakeFileInfo{name: path, isDir: false}, nil
	}
	return nil, os.ErrNotExist
}

type fakeFileInfo struct {
	name  string
	isDir bool
}

func (fi fakeFileInfo) Name() string       { return fi.name }
func (fi fakeFileInfo) Size() int64        { return 0 }
func (fi fakeFileInfo) Mode() fs.FileMode  { return 0o755 }
func (fi fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fi fakeFileInfo) IsDir() bool        { return fi.isDir }
func (fi fakeFileInfo) Sys() any           { return nil }

// ---------------------------------------------------------------------------
// Fake store for bootstrap tests — only ListResources is exercised
// ---------------------------------------------------------------------------

type stubStore struct {
	listResourcesErr error
}

func (s *stubStore) GetResource(id string) (*store.Resource, error) { return nil, nil }
func (s *stubStore) ListResources() ([]store.Resource, error)       { return nil, s.listResourcesErr }
func (s *stubStore) SetResource(r store.Resource) error             { return nil }
func (s *stubStore) DeleteResource(id string) error                 { return nil }
func (s *stubStore) GetGeneratedFiles(resourceID string) ([]store.GeneratedFile, error) {
	return nil, nil
}
func (s *stubStore) SetGeneratedFile(f store.GeneratedFile) error                   { return nil }
func (s *stubStore) DeleteGeneratedFiles(resourceID string) error                   { return nil }
func (s *stubStore) SetDependency(sourceID, targetID, kind string) error            { return nil }
func (s *stubStore) DeleteDependencies(sourceID string) error                       { return nil }
func (s *stubStore) AcquireLock(holder string, pid int) error                       { return nil }
func (s *stubStore) ReleaseLock() error                                             { return nil }
func (s *stubStore) GetLock() (*store.Lock, error)                                  { return nil, nil }
func (s *stubStore) CreateApply(id, specHash string) error                          { return nil }
func (s *stubStore) CompleteApply(id string) error                                  { return nil }
func (s *stubStore) ListApplies(limit int) ([]store.Apply, error)                   { return nil, nil }
func (s *stubStore) CreateApplyAction(id, applyID, resourceID, action string) error { return nil }
func (s *stubStore) UpdateApplyAction(id, outcome, errMsg string) error             { return nil }
func (s *stubStore) ListApplyActions(applyID string) ([]store.ApplyAction, error)   { return nil, nil }
func (s *stubStore) CreateGeneration(g store.Generation) error                      { return nil }
func (s *stubStore) UpdateGeneration(id, outputText, outcome, rejectionReason string, durationMS, inputTokens, outputTokens int64, costUSD float64) error {
	return nil
}
func (s *stubStore) ListGenerations(resourceID string, limit int) ([]store.Generation, error) {
	return nil, nil
}
func (s *stubStore) CreateSession(sess store.Session) error                 { return nil }
func (s *stubStore) GetSession(id string) (*store.Session, error)           { return nil, nil }
func (s *stubStore) GetActiveSession() (*store.Session, error)              { return nil, nil }
func (s *stubStore) UpdateSession(id, status string, currentWave int) error { return nil }
func (s *stubStore) SetNote(resourceID, applyID, content string) error      { return nil }
func (s *stubStore) GetNote(resourceID, applyID string) (string, error)     { return "", nil }
func (s *stubStore) ListNotes(applyID string) ([]store.AgentNote, error)    { return nil, nil }
func (s *stubStore) UpsertSessionResource(r store.SessionResource) error    { return nil }
func (s *stubStore) GetSessionResource(sessionID, resourceID string) (*store.SessionResource, error) {
	return nil, nil
}
func (s *stubStore) ListSessionResources(sessionID string) ([]store.SessionResource, error) {
	return nil, nil
}
func (s *stubStore) ListSessionResourcesByWave(sessionID string, wave int) ([]store.SessionResource, error) {
	return nil, nil
}
func (s *stubStore) UpdateSessionResourceState(sessionID, resourceID, state, lastError, lastOutput string, attempts int, jobID string) error {
	return nil
}
func (s *stubStore) GetGeneration(id string) (*store.Generation, error) { return nil, nil }
func (s *stubStore) RecordInvariantCheck(ic store.InvariantCheck) error { return nil }
func (s *stubStore) ListInvariantChecks(applyID string) ([]store.InvariantCheck, error) {
	return nil, nil
}
func (s *stubStore) Vacuum(before time.Time) (int, error)                         { return 0, nil }
func (s *stubStore) ReadOnlyQuery(query string) ([]map[string]interface{}, error) { return nil, nil }
func (s *stubStore) UpdateSessionResourcePhase(sessionID, resourceID, phase string, attempts int) error {
	return nil
}
func (s *stubStore) SetSessionResourceDispatched(sessionID, resourceID string) error { return nil }
func (s *stubStore) ListActiveLearnings(lang, kind string, limit int) ([]store.Learning, error) {
	return nil, nil
}
func (s *stubStore) IncrementLearningApplied(id string) error { return nil }
func (s *stubStore) ListLearnings(status string) ([]store.Learning, error) {
	return nil, nil
}
func (s *stubStore) CreateLearning(l store.Learning) error        { return nil }
func (s *stubStore) UpdateLearningStatus(id, status string) error { return nil }
func (s *stubStore) UpsertAmendment(a store.Amendment) error      { return nil }
func (s *stubStore) GetAmendment(resourceID, name string) (*store.Amendment, error) {
	return nil, nil
}
func (s *stubStore) ListAmendmentsByResource(resourceID string) ([]store.Amendment, error) {
	return nil, nil
}
func (s *stubStore) ListAmendmentsByState(state string) ([]store.Amendment, error) {
	return nil, nil
}
func (s *stubStore) ListAllAmendments() ([]store.Amendment, error) { return nil, nil }
func (s *stubStore) UpdateAmendmentState(id, state, appliedSpecHash string, appliedAt, graduatedAt time.Time) error {
	return nil
}
func (s *stubStore) DeleteAmendment(resourceID, name string) error { return nil }

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestBootstrap_SpecDirExists(t *testing.T) {
	ffs := newFakeFS()
	ffs.dirs["./spec"] = true

	s := &Spec{fs: ffs, store: &stubStore{}, cfg: &config.Config{SpecDir: "./spec"}}

	result, err := s.Bootstrap(context.Background(), BootstrapOpts{})
	require.NoError(t, err)

	step := findStep(result.Steps, "spec_directory")
	require.NotNil(t, step)
	assert.Equal(t, "ok", step.Status)
}

func TestBootstrap_SpecDirCreated(t *testing.T) {
	ffs := newFakeFS()

	s := &Spec{fs: ffs, store: &stubStore{}, cfg: &config.Config{SpecDir: "./new-spec"}}

	result, err := s.Bootstrap(context.Background(), BootstrapOpts{})
	require.NoError(t, err)

	step := findStep(result.Steps, "spec_directory")
	require.NotNil(t, step)
	assert.Equal(t, "created", step.Status)
	assert.True(t, ffs.dirs["./new-spec"])
}

func TestBootstrap_SpecDirOverride(t *testing.T) {
	ffs := newFakeFS()

	s := &Spec{fs: ffs, store: &stubStore{}, cfg: &config.Config{SpecDir: "./default-spec"}}

	result, err := s.Bootstrap(context.Background(), BootstrapOpts{SpecDir: "./custom-spec"})
	require.NoError(t, err)

	step := findStep(result.Steps, "spec_directory")
	require.NotNil(t, step)
	assert.Contains(t, step.Message, "custom-spec")
}

func TestBootstrap_DatabaseAccessible(t *testing.T) {
	ffs := newFakeFS()
	ffs.dirs["./spec"] = true

	s := &Spec{fs: ffs, store: &stubStore{}, cfg: &config.Config{SpecDir: "./spec"}}

	result, err := s.Bootstrap(context.Background(), BootstrapOpts{})
	require.NoError(t, err)

	step := findStep(result.Steps, "database")
	require.NotNil(t, step)
	assert.Equal(t, "ok", step.Status)
}

func TestBootstrap_DatabaseError(t *testing.T) {
	ffs := newFakeFS()
	ffs.dirs["./spec"] = true

	s := &Spec{fs: ffs, store: &stubStore{listResourcesErr: assert.AnError}, cfg: &config.Config{SpecDir: "./spec"}}

	result, err := s.Bootstrap(context.Background(), BootstrapOpts{})
	require.NoError(t, err)

	step := findStep(result.Steps, "database")
	require.NotNil(t, step)
	assert.Equal(t, "error", step.Status)
	assert.False(t, result.Ready)
}

func TestBootstrap_MCPConfigCreated(t *testing.T) {
	ffs := newFakeFS()
	ffs.dirs["./spec"] = true
	configPath := claudeConfigPath()

	s := &Spec{fs: ffs, store: &stubStore{}, cfg: &config.Config{SpecDir: "./spec"}}

	result, err := s.Bootstrap(context.Background(), BootstrapOpts{})
	require.NoError(t, err)

	step := findStep(result.Steps, "mcp_config")
	require.NotNil(t, step)
	assert.Equal(t, "created", step.Status)

	data, exists := ffs.files[configPath]
	require.True(t, exists)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg))
	servers, ok := cfg["mcpServers"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, servers, "crest-spec")
}

func TestBootstrap_MCPConfigAlreadyRegistered(t *testing.T) {
	ffs := newFakeFS()
	ffs.dirs["./spec"] = true
	configPath := claudeConfigPath()

	existingConfig := map[string]any{
		"mcpServers": map[string]any{
			"crest-spec": map[string]any{
				"command": "/usr/local/bin/crest-spec",
				"args":    []string{},
			},
		},
	}
	data, _ := json.Marshal(existingConfig)
	ffs.files[configPath] = data

	s := &Spec{fs: ffs, store: &stubStore{}, cfg: &config.Config{SpecDir: "./spec"}}

	result, err := s.Bootstrap(context.Background(), BootstrapOpts{})
	require.NoError(t, err)

	step := findStep(result.Steps, "mcp_config")
	require.NotNil(t, step)
	assert.Equal(t, "ok", step.Status)
}

func TestBootstrap_MCPConfigAddsToExisting(t *testing.T) {
	ffs := newFakeFS()
	ffs.dirs["./spec"] = true
	configPath := claudeConfigPath()

	existingConfig := map[string]any{
		"mcpServers": map[string]any{
			"other-server": map[string]any{
				"command": "/usr/bin/other",
			},
		},
	}
	data, _ := json.Marshal(existingConfig)
	ffs.files[configPath] = data

	s := &Spec{fs: ffs, store: &stubStore{}, cfg: &config.Config{SpecDir: "./spec"}}

	result, err := s.Bootstrap(context.Background(), BootstrapOpts{})
	require.NoError(t, err)

	step := findStep(result.Steps, "mcp_config")
	require.NotNil(t, step)
	assert.Equal(t, "created", step.Status)

	updatedData := ffs.files[configPath]
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(updatedData, &cfg))
	servers, ok := cfg["mcpServers"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, servers, "crest-spec")
	assert.Contains(t, servers, "other-server")
}

func TestBootstrap_Idempotent(t *testing.T) {
	ffs := newFakeFS()
	ffs.dirs["./spec"] = true
	configPath := claudeConfigPath()

	existingConfig := map[string]any{
		"mcpServers": map[string]any{
			"crest-spec": map[string]any{
				"command": "/usr/local/bin/crest-spec",
			},
		},
	}
	data, _ := json.Marshal(existingConfig)
	ffs.files[configPath] = data

	s := &Spec{fs: ffs, store: &stubStore{}, cfg: &config.Config{SpecDir: "./spec"}}

	result1, err := s.Bootstrap(context.Background(), BootstrapOpts{})
	require.NoError(t, err)
	result2, err := s.Bootstrap(context.Background(), BootstrapOpts{})
	require.NoError(t, err)

	step1 := findStep(result1.Steps, "mcp_config")
	step2 := findStep(result2.Steps, "mcp_config")
	assert.Equal(t, step1.Status, step2.Status)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func findStep(steps []BootstrapStep, name string) *BootstrapStep {
	for _, s := range steps {
		if s.Name == name {
			return &s
		}
	}
	return nil
}
