package spec

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

const workspaceCommitRecoveryEvidenceSource = "workspace_commit_recovery"

func (s *Spec) prepareWorkspaceCommitJournal(
	ctx context.Context,
	commit *commitAttemptState,
	candidateFiles []store.CandidateFile,
) (*store.WorkspaceCommitJournal, error) {
	if commit == nil || commit.execution == nil {
		return nil, fmt.Errorf("prepare workspace commit journal: execution is required")
	}
	files := make([]store.WorkspaceCommitJournalFile, 0, len(candidateFiles))
	for _, candidate := range candidateFiles {
		if err := s.validateGovernedCommitTopology(candidate.Path); err != nil {
			return nil, fmt.Errorf("prepare workspace commit journal: %w", err)
		}
		path, err := s.projectFilePath(candidate.Path)
		if err != nil {
			return nil, fmt.Errorf("prepare workspace commit journal: resolve %s: %w", candidate.Path, err)
		}
		state, err := s.readWorkspaceFileState(path)
		if err != nil {
			return nil, fmt.Errorf("prepare workspace commit journal: inspect %s: %w", candidate.Path, err)
		}
		if candidate.WriteIntent == "create" && state.exists {
			return nil, fmt.Errorf("prepare workspace commit journal: create path %s appeared after candidate snapshot", candidate.Path)
		}
		if candidate.WriteIntent != "create" && !state.exists {
			return nil, fmt.Errorf("prepare workspace commit journal: existing path %s disappeared after candidate snapshot", candidate.Path)
		}
		candidateMode := uint32(0o644)
		var original []byte
		if state.exists {
			candidateMode = state.mode
			original, err = s.fs.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("prepare workspace commit journal: read %s: %w", candidate.Path, err)
			}
			// The content and metadata reads must describe the same version.
			if hashWorkspaceContent(original) != state.contentHash {
				return nil, fmt.Errorf("prepare workspace commit journal: %s changed while snapshotting", candidate.Path)
			}
		}
		files = append(files, store.WorkspaceCommitJournalFile{
			Path: candidate.Path, OriginalExists: state.exists,
			OriginalContent: original, OriginalContentHash: state.contentHash, OriginalMode: state.mode,
			CandidateContent: []byte(candidate.Content), CandidateContentHash: candidate.ContentHash, CandidateMode: candidateMode,
			WriteIntent: candidate.WriteIntent, Ordinal: candidate.Ordinal,
		})
	}
	return s.store.PrepareWorkspaceCommitJournal(ctx, store.WorkspaceCommitJournalInput{
		ExecutionID: commit.execution.ID,
		ProjectRoot: s.projectWorkspace().Root(),
		Files:       files,
	})
}

func (s *Spec) writeWorkspaceCommitCandidate(journal *store.WorkspaceCommitJournal) error {
	if journal == nil {
		return fmt.Errorf("write workspace commit candidate: journal is required")
	}
	// Validate the complete journal before the first write. This also protects
	// recovery from malformed journals persisted by an older engine version.
	for _, file := range journal.Files {
		if err := store.ValidateCandidateRepositoryBytes(file.Path, file.CandidateContent); err != nil {
			return fmt.Errorf("write workspace commit candidate: %w", err)
		}
	}
	for _, file := range journal.Files {
		if err := s.validateGovernedCommitTopology(file.Path); err != nil {
			return fmt.Errorf("write workspace commit candidate: %w", err)
		}
		path, err := s.projectFilePath(file.Path)
		if err != nil {
			return fmt.Errorf("write workspace commit candidate: resolve %s: %w", file.Path, err)
		}
		state, err := s.readWorkspaceFileState(path)
		if err != nil {
			return fmt.Errorf("write workspace commit candidate: inspect %s: %w", file.Path, err)
		}
		if state.matches(file.CandidateContentHash, file.CandidateMode) {
			continue
		}
		if file.OriginalExists {
			if !state.matches(file.OriginalContentHash, file.OriginalMode) {
				return workspaceCASMismatch("install candidate", file.Path, state)
			}
		} else if state.exists {
			return workspaceCASMismatch("create candidate", file.Path, state)
		}
		if file.WriteIntent == "preserve" {
			return fmt.Errorf("write workspace commit candidate: preserved file %s differs from its immutable candidate", file.Path)
		}
		if err := s.writeWorkspaceVersion(path, file.CandidateContent, file.CandidateMode); err != nil {
			return fmt.Errorf("write workspace commit candidate: publish %s: %w", file.Path, err)
		}
	}
	return nil
}

func (s *Spec) verifyWorkspaceCommitCandidate(journal *store.WorkspaceCommitJournal) error {
	if journal == nil {
		return fmt.Errorf("verify workspace commit candidate: journal is required")
	}
	for _, file := range journal.Files {
		if err := s.validateGovernedCommitTopology(file.Path); err != nil {
			return fmt.Errorf("verify workspace commit candidate: %w", err)
		}
		path, err := s.projectFilePath(file.Path)
		if err != nil {
			return fmt.Errorf("verify workspace commit candidate: resolve %s: %w", file.Path, err)
		}
		state, err := s.readWorkspaceFileState(path)
		if err != nil {
			return fmt.Errorf("verify workspace commit candidate: inspect %s: %w", file.Path, err)
		}
		if !state.matches(file.CandidateContentHash, file.CandidateMode) {
			return workspaceCASMismatch("verify candidate", file.Path, state)
		}
	}
	return nil
}

// RecoverWorkspaceCommits is the process-start and scheduler recovery
// boundary for filesystem commits interrupted between candidate submission and
// durable journal completion. The project-wide lock must be held before even
// inspecting pending journals: another MCP process may currently be between a
// legal candidate write and FinalizeCandidate.
func (s *Spec) RecoverWorkspaceCommits(ctx context.Context) (int, error) {
	releaseProject, err := s.projectWorkspace().AcquireCommit(ctx)
	if err != nil {
		return 0, fmt.Errorf("recover workspace commits: %w", err)
	}
	defer releaseProject()
	return s.recoverWorkspaceCommitsLocked(ctx)
}

func (s *Spec) recoverWorkspaceCommitsLocked(ctx context.Context) (int, error) {
	journals, err := s.store.ListPendingWorkspaceCommitJournals(ctx)
	if err != nil {
		return 0, fmt.Errorf("recover workspace commits: list journals: %w", err)
	}
	recovered := 0
	for index := range journals {
		journal := &journals[index]
		if err := s.recoverWorkspaceCommitJournalLocked(ctx, journal); err != nil {
			return recovered, fmt.Errorf("recover workspace commit %s: %w", journal.ID, err)
		}
		recovered++
	}
	return recovered, nil
}

func (s *Spec) recoverWorkspaceCommitJournalLocked(
	ctx context.Context,
	journal *store.WorkspaceCommitJournal,
) error {
	if journal == nil {
		return fmt.Errorf("journal is required")
	}
	// Reload under the project lock so a replay that listed the journal before
	// another recovery completed becomes a harmless no-op.
	current, err := s.store.GetWorkspaceCommitJournal(ctx, journal.ID)
	if err != nil {
		return err
	}
	if current.Status == store.WorkspaceCommitCommitted || current.Status == store.WorkspaceCommitRolledBack {
		return nil
	}
	projectRoot := filepath.Clean(s.projectWorkspace().Root())
	journalRoot := filepath.Clean(current.ProjectRoot)
	if projectRoot != journalRoot {
		return fmt.Errorf("journal project root %q does not match active project root %q", journalRoot, projectRoot)
	}

	manifest, err := s.store.GetExecutionManifest(ctx, current.ExecutionID)
	if err != nil {
		return fmt.Errorf("load execution: %w", err)
	}
	if manifest.Status == string(execution.StatusAccepted) {
		if err := s.applyAcceptedWorkspaceJournal(current); err != nil {
			return err
		}
		return s.store.ResolveWorkspaceCommitJournal(
			ctx, current.ID, store.WorkspaceCommitCommitted,
			"accepted candidate workspace verified during interrupted-commit recovery",
		)
	}

	switch execution.Status(manifest.Status) {
	case execution.StatusExecuting, execution.StatusCandidateSubmitted, execution.StatusValidating,
		execution.StatusRejected, execution.StatusFailed, execution.StatusCancelled, execution.StatusTimedOut:
		// Every nonaccepted candidate must converge back to its exact journaled
		// predecessor before governance is allowed to schedule another attempt.
	default:
		return fmt.Errorf("execution %s has unsafe recovery status %q", manifest.ID, manifest.Status)
	}
	if err := s.restoreOriginalWorkspaceJournal(current); err != nil {
		return err
	}

	reason := "engine recovered an interrupted workspace commit before candidate finalization"
	if manifest.Status == string(execution.StatusExecuting) ||
		manifest.Status == string(execution.StatusCandidateSubmitted) ||
		manifest.Status == string(execution.StatusValidating) {
		if _, err := s.store.TransitionExecution(ctx, store.ExecutionTransition{
			ExecutionID: manifest.ID,
			ToStatus:    execution.StatusFailed,
			Reason:      reason,
			Details: map[string]any{
				"workspace_commit_journal_id": current.ID,
				"candidate_id":                current.CandidateID,
			},
			Failure: &store.FailureClassificationWrite{
				Category:          execution.FailureToolFailure,
				Origin:            "engine",
				Confidence:        1,
				EvidenceSource:    workspaceCommitRecoveryEvidenceSource,
				EvidenceReference: current.ID,
				Evidence:          reason,
			},
		}); err != nil {
			return fmt.Errorf("fail interrupted execution after workspace restoration: %w", err)
		}
	}
	return s.store.ResolveWorkspaceCommitJournal(ctx, current.ID, store.WorkspaceCommitRolledBack, reason)
}

func (s *Spec) restoreOriginalWorkspaceJournal(journal *store.WorkspaceCommitJournal) error {
	states := make(map[string]workspaceFileState, len(journal.Files))
	for _, file := range journal.Files {
		if err := s.validateGovernedCommitTopology(file.Path); err != nil {
			return fmt.Errorf("rollback topology %s: %w", file.Path, err)
		}
		path, err := s.projectFilePath(file.Path)
		if err != nil {
			return fmt.Errorf("resolve rollback path %s: %w", file.Path, err)
		}
		if err := s.cleanupWorkspaceCommitStaging(path, file); err != nil {
			return fmt.Errorf("clean rollback staging for %s: %w", file.Path, err)
		}
		state, err := s.readWorkspaceFileState(path)
		if err != nil {
			return fmt.Errorf("inspect rollback path %s: %w", file.Path, err)
		}
		states[file.Path] = state
		if file.OriginalExists {
			if !state.matches(file.OriginalContentHash, file.OriginalMode) &&
				!state.matches(file.CandidateContentHash, file.CandidateMode) {
				return workspaceCASMismatch("roll back", file.Path, state)
			}
		} else if state.exists && !state.matches(file.CandidateContentHash, file.CandidateMode) {
			return workspaceCASMismatch("remove created", file.Path, state)
		}
	}

	// Reverse order is conventional rollback behavior and is deterministic for
	// parent/module file pairs, although each path is independently journaled.
	for index := len(journal.Files) - 1; index >= 0; index-- {
		file := journal.Files[index]
		path, err := s.projectFilePath(file.Path)
		if err != nil {
			return fmt.Errorf("resolve rollback path %s: %w", file.Path, err)
		}
		state := states[file.Path]
		if file.OriginalExists {
			if state.matches(file.OriginalContentHash, file.OriginalMode) {
				continue
			}
			if err := s.writeWorkspaceVersion(path, file.OriginalContent, file.OriginalMode); err != nil {
				return fmt.Errorf("restore %s: %w", file.Path, err)
			}
			continue
		}

		if !state.exists {
			continue
		}
		if err := s.fs.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove created file %s: %w", file.Path, err)
		}
	}
	return nil
}

func (s *Spec) applyAcceptedWorkspaceJournal(journal *store.WorkspaceCommitJournal) error {
	for _, file := range journal.Files {
		if err := store.ValidateCandidateRepositoryBytes(file.Path, file.CandidateContent); err != nil {
			return fmt.Errorf("roll forward accepted workspace journal: %w", err)
		}
	}
	states := make(map[string]workspaceFileState, len(journal.Files))
	for _, file := range journal.Files {
		if err := s.validateGovernedCommitTopology(file.Path); err != nil {
			return fmt.Errorf("accepted topology %s: %w", file.Path, err)
		}
		path, err := s.projectFilePath(file.Path)
		if err != nil {
			return fmt.Errorf("resolve accepted path %s: %w", file.Path, err)
		}
		if err := s.cleanupWorkspaceCommitStaging(path, file); err != nil {
			return fmt.Errorf("clean accepted staging for %s: %w", file.Path, err)
		}
		state, err := s.readWorkspaceFileState(path)
		if err != nil {
			return fmt.Errorf("inspect accepted path %s: %w", file.Path, err)
		}
		states[file.Path] = state
		if state.matches(file.CandidateContentHash, file.CandidateMode) {
			continue
		}
		if file.OriginalExists {
			if !state.matches(file.OriginalContentHash, file.OriginalMode) {
				return workspaceCASMismatch("roll forward accepted", file.Path, state)
			}
		} else if state.exists {
			return workspaceCASMismatch("create accepted", file.Path, state)
		}
	}
	for _, file := range journal.Files {
		if states[file.Path].matches(file.CandidateContentHash, file.CandidateMode) {
			continue
		}
		path, err := s.projectFilePath(file.Path)
		if err != nil {
			return fmt.Errorf("resolve accepted path %s: %w", file.Path, err)
		}
		if err := s.writeWorkspaceVersion(path, file.CandidateContent, file.CandidateMode); err != nil {
			return fmt.Errorf("roll forward accepted file %s: %w", file.Path, err)
		}
	}
	return nil
}

type workspaceFileState struct {
	exists      bool
	contentHash string
	mode        uint32
}

func (state workspaceFileState) matches(contentHash string, mode uint32) bool {
	return state.exists && state.contentHash == contentHash && state.mode == mode
}

func (s *Spec) readWorkspaceFileState(path string) (workspaceFileState, error) {
	data, err := s.fs.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return workspaceFileState{}, nil
	}
	if err != nil {
		return workspaceFileState{}, err
	}
	info, err := s.fs.Stat(path)
	if err != nil {
		return workspaceFileState{}, err
	}
	if !info.Mode().IsRegular() {
		return workspaceFileState{}, fmt.Errorf("path is not a regular file")
	}
	return workspaceFileState{
		exists: true, contentHash: hashWorkspaceContent(data), mode: uint32(info.Mode().Perm()),
	}, nil
}

func (s *Spec) writeWorkspaceVersion(path string, content []byte, mode uint32) error {
	if err := s.fs.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	permissions := fs.FileMode(mode).Perm()
	return s.fs.WriteFileAtomic(path, content, permissions)
}

func (s *Spec) cleanupWorkspaceCommitStaging(
	path string,
	file store.WorkspaceCommitJournalFile,
) error {
	hashes := []string{file.CandidateContentHash}
	if file.OriginalExists {
		hashes = append(hashes, file.OriginalContentHash)
	}
	seen := make(map[string]struct{}, len(hashes))
	for _, hash := range hashes {
		if _, duplicate := seen[hash]; duplicate {
			continue
		}
		seen[hash] = struct{}{}
		staging := atomicWorkspaceStagingPath(path, hash)
		if err := s.fs.Remove(staging); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func workspaceCASMismatch(action, path string, state workspaceFileState) error {
	if !state.exists {
		return fmt.Errorf("cannot %s %s: workspace path is unexpectedly absent; refusing to overwrite third-party state", action, path)
	}
	return fmt.Errorf(
		"cannot %s %s: workspace bytes or mode diverged (hash=%s mode=%#o); refusing to overwrite third-party state",
		action, path, state.contentHash, state.mode,
	)
}

func hashWorkspaceContent(content []byte) string {
	hash := sha256.Sum256(content)
	return fmt.Sprintf("%x", hash)
}

func (s *Spec) validateGovernedCommitTopologies(files []CommitFile) error {
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		if err := s.validateGovernedCommitTopology(file.Path); err != nil {
			return err
		}
	}
	return nil
}

// validateGovernedCommitTopology rejects symlink targets and prefixes. The
// byte journal cannot reconstruct link topology, and atomic rename would
// replace the link inode rather than update its target. Failing before journal
// or candidate persistence is therefore the only safe governed behavior.
func (s *Spec) validateGovernedCommitTopology(relativePath string) error {
	resolved, err := s.projectFilePath(relativePath)
	if err != nil {
		return err
	}
	root := filepath.Clean(s.projectWorkspace().Root())
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return fmt.Errorf("inspect topology for %s: %w", relativePath, err)
	}
	components := splitPathComponents(relative)
	cursor := root
	for index, component := range components {
		cursor = filepath.Join(cursor, component)
		info, err := s.fs.Lstat(cursor)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect topology for %s: %w", relativePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("governed path %s contains symlink component %s", relativePath, cursor)
		}
		if index < len(components)-1 && !info.IsDir() {
			return fmt.Errorf("governed path %s has non-directory prefix %s", relativePath, cursor)
		}
		if index == len(components)-1 && !info.Mode().IsRegular() {
			return fmt.Errorf("governed target %s is not a regular file", relativePath)
		}
	}
	return nil
}

func splitPathComponents(path string) []string {
	clean := filepath.Clean(path)
	if clean == "." || clean == "" {
		return nil
	}
	var components []string
	for clean != "." && clean != string(filepath.Separator) {
		dir, base := filepath.Split(clean)
		if base != "" {
			components = append(components, base)
		}
		clean = filepath.Clean(dir)
	}
	for left, right := 0, len(components)-1; left < right; left, right = left+1, right-1 {
		components[left], components[right] = components[right], components[left]
	}
	return components
}
