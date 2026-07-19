package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	WorkspaceCommitPrepared   = "prepared"
	WorkspaceCommitApplied    = "workspace_applied"
	WorkspaceCommitCommitted  = "committed"
	WorkspaceCommitRolledBack = "rolled_back"
)

// WorkspaceCommitJournal is the durable bridge between an immutable SQLite
// candidate and its non-transactional project-file installation. Pending
// journals are recovered before another scheduling or commit operation may
// inspect the workspace.
type WorkspaceCommitJournal struct {
	ID          string
	ExecutionID string
	CandidateID string
	ProjectRoot string
	Status      string
	Resolution  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ResolvedAt  *time.Time
	Files       []WorkspaceCommitJournalFile
}

// WorkspaceCommitJournalFile carries both legal versions of a governed path.
// Recovery uses them as a compare-and-swap boundary: a third value is never
// overwritten as though it came from the interrupted commit.
type WorkspaceCommitJournalFile struct {
	Path                 string
	OriginalExists       bool
	OriginalContent      []byte
	OriginalContentHash  string
	OriginalMode         uint32
	CandidateContent     []byte
	CandidateContentHash string
	CandidateMode        uint32
	WriteIntent          string
	Ordinal              int
}

type WorkspaceCommitJournalInput struct {
	ExecutionID string
	ProjectRoot string
	Files       []WorkspaceCommitJournalFile
}

// PrepareWorkspaceCommitJournal atomically stores every pre-commit byte before
// candidate submission changes the execution state and before the caller
// performs its first workspace write. Candidate bytes are content-addressed in
// the journal itself, closing the crash window before candidate_sets exists.
func (s *Store) PrepareWorkspaceCommitJournal(
	ctx context.Context,
	input WorkspaceCommitJournalInput,
) (*WorkspaceCommitJournal, error) {
	input.ExecutionID = strings.TrimSpace(input.ExecutionID)
	input.ProjectRoot = filepath.Clean(strings.TrimSpace(input.ProjectRoot))
	if input.ExecutionID == "" || input.ProjectRoot == "" || input.ProjectRoot == "." {
		return nil, fmt.Errorf("prepare workspace commit journal: execution and project root are required")
	}
	for _, file := range input.Files {
		path := filepath.ToSlash(filepath.Clean(file.Path))
		if err := ValidateCandidateRepositoryBytes(path, file.CandidateContent); err != nil {
			return nil, fmt.Errorf("prepare workspace commit journal: %w", err)
		}
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("prepare workspace commit journal: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existingID, existingRoot string
	err = tx.QueryRowContext(ctx, `
		SELECT id, project_root
		FROM workspace_commit_journals WHERE execution_id = ?`, input.ExecutionID,
	).Scan(&existingID, &existingRoot)
	if err == nil {
		if filepath.Clean(existingRoot) != input.ProjectRoot {
			return nil, fmt.Errorf("prepare workspace commit journal: execution already has a different journal")
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("prepare workspace commit journal: commit replay: %w", err)
		}
		existing, err := s.GetWorkspaceCommitJournal(ctx, existingID)
		if err != nil {
			return nil, err
		}
		if !sameWorkspaceJournalInput(existing, input.Files) {
			return nil, fmt.Errorf("prepare workspace commit journal: replay file set differs")
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("prepare workspace commit journal: inspect replay: %w", err)
	}

	var executionStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT status FROM execution_manifests WHERE id = ?`, input.ExecutionID,
	).Scan(&executionStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("prepare workspace commit journal: execution not found")
	}
	if err != nil {
		return nil, fmt.Errorf("prepare workspace commit journal: inspect execution: %w", err)
	}
	if executionStatus != "executing" {
		return nil, fmt.Errorf("prepare workspace commit journal: execution must be executing, got %s", executionStatus)
	}

	files := append([]WorkspaceCommitJournalFile(nil), input.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	seenPaths := make(map[string]struct{}, len(files))
	seenOrdinals := make(map[int]struct{}, len(files))
	for index := range files {
		file := &files[index]
		path := filepath.ToSlash(filepath.Clean(file.Path))
		if file.Path == "" || filepath.IsAbs(file.Path) || path == "." || path == ".." || strings.HasPrefix(path, "../") {
			return nil, fmt.Errorf("prepare workspace commit journal: path %q must be project-relative", file.Path)
		}
		if _, duplicate := seenPaths[path]; duplicate {
			return nil, fmt.Errorf("prepare workspace commit journal: duplicate snapshot path %q", path)
		}
		seenPaths[path] = struct{}{}
		if _, duplicate := seenOrdinals[file.Ordinal]; duplicate || file.Ordinal < 0 {
			return nil, fmt.Errorf("prepare workspace commit journal: invalid duplicate ordinal %d", file.Ordinal)
		}
		seenOrdinals[file.Ordinal] = struct{}{}
		file.Path = path
		if file.OriginalExists {
			file.OriginalContentHash = hashWorkspaceBytes(file.OriginalContent)
		} else if len(file.OriginalContent) != 0 || file.OriginalMode != 0 {
			return nil, fmt.Errorf("prepare workspace commit journal: absent original %s cannot carry content or mode", path)
		}
		if file.CandidateContentHash == "" {
			file.CandidateContentHash = hashWorkspaceBytes(file.CandidateContent)
		}
		if hashWorkspaceBytes(file.CandidateContent) != file.CandidateContentHash {
			return nil, fmt.Errorf("prepare workspace commit journal: candidate content hash mismatch for %s", path)
		}
		if file.WriteIntent != "create" && file.WriteIntent != "modify" && file.WriteIntent != "preserve" {
			return nil, fmt.Errorf("prepare workspace commit journal: invalid write intent %q", file.WriteIntent)
		}
	}

	journalID := uuid.NewString()
	timestamp := now()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_commit_journals (
			id, execution_id, candidate_id, project_root, status,
			resolution, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'prepared', '', ?, ?)`,
		journalID, input.ExecutionID, nil, input.ProjectRoot, timestamp, timestamp,
	); err != nil {
		return nil, fmt.Errorf("prepare workspace commit journal: insert journal: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Ordinal < files[j].Ordinal })
	for _, file := range files {
		var originalBlob any
		if file.OriginalExists {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO content_blobs (hash, content, byte_size, created_at)
				VALUES (?, ?, ?, ?) ON CONFLICT(hash) DO NOTHING`,
				file.OriginalContentHash, file.OriginalContent, len(file.OriginalContent), timestamp,
			); err != nil {
				return nil, fmt.Errorf("prepare workspace commit journal: store original %s: %w", file.Path, err)
			}
			originalBlob = file.OriginalContentHash
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO content_blobs (hash, content, byte_size, created_at)
			VALUES (?, ?, ?, ?) ON CONFLICT(hash) DO NOTHING`,
			file.CandidateContentHash, file.CandidateContent, len(file.CandidateContent), timestamp,
		); err != nil {
			return nil, fmt.Errorf("prepare workspace commit journal: store candidate %s: %w", file.Path, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_commit_journal_files (
				journal_id, path, original_exists, original_content_blob,
				original_content_hash, original_mode, candidate_content_blob,
				candidate_content_hash, candidate_mode, write_intent, ordinal
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			journalID, file.Path, boolInt(file.OriginalExists), originalBlob,
			file.OriginalContentHash, file.OriginalMode, file.CandidateContentHash,
			file.CandidateContentHash, file.CandidateMode, file.WriteIntent, file.Ordinal,
		); err != nil {
			return nil, fmt.Errorf("prepare workspace commit journal: insert snapshot %s: %w", file.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("prepare workspace commit journal: commit: %w", err)
	}
	return s.GetWorkspaceCommitJournal(ctx, journalID)
}

// BindWorkspaceCommitCandidate links the pre-submission journal to the exact
// immutable candidate before any workspace write. A crash on either side of
// this call remains recoverable because execution_id and both file versions
// were already durable.
func (s *Store) BindWorkspaceCommitCandidate(ctx context.Context, journalID, candidateID string) error {
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("bind workspace commit candidate: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var executionID string
	var currentCandidate sql.NullString
	var journalStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT execution_id, candidate_id, status
		FROM workspace_commit_journals WHERE id = ?`, journalID,
	).Scan(&executionID, &currentCandidate, &journalStatus); err != nil {
		return fmt.Errorf("bind workspace commit candidate: journal: %w", mapNotFound(err))
	}
	if currentCandidate.Valid {
		if currentCandidate.String != candidateID {
			return fmt.Errorf("bind workspace commit candidate: journal already names another candidate")
		}
		return tx.Commit()
	}
	if journalStatus != WorkspaceCommitPrepared {
		return fmt.Errorf("bind workspace commit candidate: journal has status %s", journalStatus)
	}
	var candidateExecution, candidateStatus, executionStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT candidate.execution_id, candidate.status, execution.status
		FROM candidate_sets AS candidate
		JOIN execution_manifests AS execution ON execution.id = candidate.execution_id
		WHERE candidate.id = ?`, candidateID,
	).Scan(&candidateExecution, &candidateStatus, &executionStatus); err != nil {
		return fmt.Errorf("bind workspace commit candidate: candidate: %w", mapNotFound(err))
	}
	if candidateExecution != executionID || candidateStatus != "submitted" || executionStatus != "candidate_submitted" {
		return fmt.Errorf("bind workspace commit candidate: candidate/execution state does not match journal")
	}
	var mismatches int
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM candidate_files WHERE candidate_id = ?) !=
		  (SELECT COUNT(*) FROM workspace_commit_journal_files WHERE journal_id = ?)
		  OR EXISTS (
		    SELECT 1 FROM candidate_files AS candidate_file
		    LEFT JOIN workspace_commit_journal_files AS journal_file
		      ON journal_file.journal_id = ? AND journal_file.path = candidate_file.path
		    WHERE candidate_file.candidate_id = ?
		      AND (journal_file.path IS NULL
		           OR journal_file.candidate_content_hash != candidate_file.content_hash
		           OR journal_file.write_intent != candidate_file.write_intent
		           OR journal_file.ordinal != candidate_file.ordinal)
		  )`, candidateID, journalID, journalID, candidateID,
	).Scan(&mismatches); err != nil {
		return fmt.Errorf("bind workspace commit candidate: compare file set: %w", err)
	}
	if mismatches != 0 {
		return fmt.Errorf("bind workspace commit candidate: candidate file set differs from journal")
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE workspace_commit_journals SET candidate_id = ?, updated_at = ?
		WHERE id = ? AND candidate_id IS NULL AND status = 'prepared'`, candidateID, now(), journalID)
	if err != nil {
		return fmt.Errorf("bind workspace commit candidate: update: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("bind workspace commit candidate: affected=%d: %w", changed, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("bind workspace commit candidate: commit: %w", err)
	}
	return nil
}

func (s *Store) GetWorkspaceCommitJournal(ctx context.Context, id string) (*WorkspaceCommitJournal, error) {
	var row WorkspaceCommitJournal
	var createdAt, updatedAt string
	var resolvedAt *string
	var candidateID sql.NullString
	err := s.sqlDB.QueryRowContext(ctx, `
		SELECT id, execution_id, candidate_id, project_root, status, resolution,
		       created_at, updated_at, resolved_at
		FROM workspace_commit_journals WHERE id = ?`, id,
	).Scan(
		&row.ID, &row.ExecutionID, &candidateID, &row.ProjectRoot, &row.Status,
		&row.Resolution, &createdAt, &updatedAt, &resolvedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get workspace commit journal: %w", mapNotFound(err))
	}
	row.CreatedAt = parseTime(createdAt)
	row.UpdatedAt = parseTime(updatedAt)
	if candidateID.Valid {
		row.CandidateID = candidateID.String
	}
	if resolvedAt != nil {
		parsed := parseTime(*resolvedAt)
		row.ResolvedAt = &parsed
	}

	rows, err := s.sqlDB.QueryContext(ctx, `
		SELECT journal_file.path, journal_file.original_exists,
		       COALESCE(original_blob.content, x''), journal_file.original_content_hash,
		       journal_file.original_mode, candidate_blob.content,
		       journal_file.candidate_content_hash, journal_file.candidate_mode,
		       journal_file.write_intent, journal_file.ordinal
		FROM workspace_commit_journal_files AS journal_file
		JOIN content_blobs AS candidate_blob ON candidate_blob.hash = journal_file.candidate_content_blob
		LEFT JOIN content_blobs AS original_blob ON original_blob.hash = journal_file.original_content_blob
		WHERE journal_file.journal_id = ?
		ORDER BY journal_file.ordinal`, id)
	if err != nil {
		return nil, fmt.Errorf("get workspace commit journal: list files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var file WorkspaceCommitJournalFile
		var originalExists int
		var originalMode, candidateMode int64
		if err := rows.Scan(
			&file.Path, &originalExists, &file.OriginalContent, &file.OriginalContentHash,
			&originalMode, &file.CandidateContent, &file.CandidateContentHash,
			&candidateMode, &file.WriteIntent, &file.Ordinal,
		); err != nil {
			return nil, fmt.Errorf("get workspace commit journal: scan file: %w", err)
		}
		file.OriginalExists = originalExists == 1
		file.OriginalMode = uint32(originalMode)
		file.CandidateMode = uint32(candidateMode)
		if hashWorkspaceBytes(file.CandidateContent) != file.CandidateContentHash ||
			(file.OriginalExists && hashWorkspaceBytes(file.OriginalContent) != file.OriginalContentHash) {
			return nil, fmt.Errorf("get workspace commit journal: content provenance mismatch for %s", file.Path)
		}
		row.Files = append(row.Files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get workspace commit journal: iterate files: %w", err)
	}
	return &row, nil
}

func sameWorkspaceJournalInput(existing *WorkspaceCommitJournal, files []WorkspaceCommitJournalFile) bool {
	if existing == nil || len(existing.Files) != len(files) {
		return false
	}
	byPath := make(map[string]WorkspaceCommitJournalFile, len(files))
	for _, file := range files {
		file.Path = filepath.ToSlash(filepath.Clean(file.Path))
		byPath[file.Path] = file
	}
	for _, persisted := range existing.Files {
		input, ok := byPath[persisted.Path]
		expectedOriginalHash := ""
		if input.OriginalExists {
			expectedOriginalHash = hashWorkspaceBytes(input.OriginalContent)
		}
		if !ok || persisted.OriginalExists != input.OriginalExists ||
			persisted.OriginalContentHash != expectedOriginalHash ||
			persisted.OriginalMode != input.OriginalMode ||
			persisted.CandidateContentHash != hashWorkspaceBytes(input.CandidateContent) ||
			persisted.CandidateMode != input.CandidateMode ||
			persisted.WriteIntent != input.WriteIntent || persisted.Ordinal != input.Ordinal {
			return false
		}
	}
	return true
}

func (s *Store) ListPendingWorkspaceCommitJournals(ctx context.Context) ([]WorkspaceCommitJournal, error) {
	rows, err := s.sqlDB.QueryContext(ctx, `
		SELECT id FROM workspace_commit_journals
		WHERE status IN ('prepared','workspace_applied')
		ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list pending workspace commit journals: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("list pending workspace commit journals: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("list pending workspace commit journals: close: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending workspace commit journals: iterate: %w", err)
	}
	result := make([]WorkspaceCommitJournal, 0, len(ids))
	for _, id := range ids {
		journal, err := s.GetWorkspaceCommitJournal(ctx, id)
		if err != nil {
			return nil, err
		}
		if journal.Status == WorkspaceCommitPrepared || journal.Status == WorkspaceCommitApplied {
			result = append(result, *journal)
		}
	}
	return result, nil
}

func (s *Store) MarkWorkspaceCommitApplied(ctx context.Context, id string) error {
	return s.transitionWorkspaceCommitJournal(ctx, id, WorkspaceCommitApplied, "candidate files written to workspace")
}

func (s *Store) ResolveWorkspaceCommitJournal(ctx context.Context, id, status, resolution string) error {
	if status != WorkspaceCommitCommitted && status != WorkspaceCommitRolledBack {
		return fmt.Errorf("resolve workspace commit journal: invalid terminal status %q", status)
	}
	return s.transitionWorkspaceCommitJournal(ctx, id, status, resolution)
}

func (s *Store) transitionWorkspaceCommitJournal(ctx context.Context, id, status, resolution string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("transition workspace commit journal: id is required")
	}
	timestamp := now()
	var result sql.Result
	var err error
	if status == WorkspaceCommitApplied {
		result, err = s.sqlDB.ExecContext(ctx, `
			UPDATE workspace_commit_journals
			SET status = 'workspace_applied', resolution = ?, updated_at = ?
			WHERE id = ? AND status = 'prepared'`, resolution, timestamp, id)
	} else {
		result, err = s.sqlDB.ExecContext(ctx, `
			UPDATE workspace_commit_journals
			SET status = ?, resolution = ?, updated_at = ?, resolved_at = ?
			WHERE id = ? AND status IN ('prepared','workspace_applied')`,
			status, resolution, timestamp, timestamp, id)
	}
	if err != nil {
		return fmt.Errorf("transition workspace commit journal: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("transition workspace commit journal: rows affected: %w", err)
	}
	if changed == 1 {
		return nil
	}
	var current string
	if err := s.sqlDB.QueryRowContext(ctx, `SELECT status FROM workspace_commit_journals WHERE id = ?`, id).Scan(&current); err != nil {
		return fmt.Errorf("transition workspace commit journal: inspect current status: %w", mapNotFound(err))
	}
	if current == status || (status == WorkspaceCommitApplied && current == WorkspaceCommitCommitted) {
		return nil
	}
	return fmt.Errorf("transition workspace commit journal: cannot transition %s to %s", current, status)
}

func hashWorkspaceBytes(content []byte) string {
	hash := sha256.Sum256(content)
	return fmt.Sprintf("%x", hash)
}
