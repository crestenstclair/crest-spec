package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
)

// BehavioralWitnessRepairRoutingInput is one engine-owned routing decision
// made after a failed or counterexample-theater, fully persisted behavioral
// witness run. The immutable witness snapshot remains the authority for which
// resources may be repaired.
type BehavioralWitnessRepairRoutingInput struct {
	SessionID        string
	ValidationRunID  string
	ResourceIDs      []string
	Reason           string
	MaxRepairRetries int
}

type BehavioralWitnessRepairEpoch struct {
	ValidationRunID string `json:"validation_run_id"`
	DefinitionHash  string `json:"definition_hash"`
	SourceKind      string `json:"source_kind"`
	SourceHash      string `json:"source_hash"`
	RetryCount      int    `json:"retry_count"`
	MaxRetries      int    `json:"max_retries"`
	Disposition     string `json:"disposition"`
}

type BehavioralWitnessRepairRoutingResult struct {
	ReopenedResources  []string
	ExhaustedResources []string
	Epochs             map[string]BehavioralWitnessRepairEpoch
}

type BehavioralWitnessRepairExhaustion struct {
	ResourceID      string
	WitnessID       string
	DefinitionHash  string
	SourceKind      string
	SourceHash      string
	RetryCount      int
	MaxRetries      int
	ValidationRunID string
	Reason          string
}

// RouteBehavioralWitnessFailure atomically charges and routes the exact repair
// owners declared by a failed witness outcome. The retry budget is scoped to the
// content-addressed source and immutable witness definition: replaying that
// same epoch cannot schedule forever, while an accepted source change or a
// changed witness definition starts a deliberately distinct epoch.
func (s *Store) RouteBehavioralWitnessFailure(
	ctx context.Context,
	input BehavioralWitnessRepairRoutingInput,
) (*BehavioralWitnessRepairRoutingResult, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.ValidationRunID = strings.TrimSpace(input.ValidationRunID)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.SessionID == "" || input.ValidationRunID == "" || input.Reason == "" {
		return nil, fmt.Errorf("route behavioral witness failure: session, validation run, and reason are required")
	}
	if input.MaxRepairRetries < 0 {
		return nil, fmt.Errorf("route behavioral witness failure: max repair retries cannot be negative")
	}
	resourceIDs := append([]string(nil), input.ResourceIDs...)
	sort.Strings(resourceIDs)
	resourceIDs = uniqueStrings(resourceIDs)
	result := &BehavioralWitnessRepairRoutingResult{
		Epochs: make(map[string]BehavioralWitnessRepairEpoch),
	}
	if len(resourceIDs) == 0 {
		return result, nil
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("route behavioral witness failure: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		sessionStatus, runSessionID, classification, sourceTreeHash string
		definitionSnapshotID, definitionID, definitionHash          string
		definitionJSON                                              string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT session.status, COALESCE(run.session_id, ''), run.classification,
		       run.source_tree_hash, definition.snapshot_id,
		       definition.definition_id, definition.definition_hash,
		       definition.definition_json
		FROM agent_sessions AS session
		JOIN validation_runs AS run ON run.id = ?
		JOIN verification_definitions AS definition
		  ON definition.snapshot_id = run.definition_snapshot_id
		WHERE session.id = ?`, input.ValidationRunID, input.SessionID,
	).Scan(
		&sessionStatus, &runSessionID, &classification, &sourceTreeHash,
		&definitionSnapshotID, &definitionID, &definitionHash, &definitionJSON,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("route behavioral witness failure: session or validation run not found")
	}
	if err != nil {
		return nil, fmt.Errorf("route behavioral witness failure: inspect witness provenance: %w", err)
	}
	if sessionStatus != "active" {
		return result, nil
	}
	if runSessionID != input.SessionID ||
		(classification != "failed" && classification != "theater") ||
		!strings.HasPrefix(definitionID, "witness.") {
		return nil, fmt.Errorf(
			"route behavioral witness failure: run is not a repair-routable failed/theater witness for active session %s",
			input.SessionID,
		)
	}
	var definition struct {
		RepairResources               []string `json:"repairResources"`
		CounterexampleRepairResources []string `json:"counterexampleRepairResources"`
	}
	if err := json.Unmarshal([]byte(definitionJSON), &definition); err != nil {
		return nil, fmt.Errorf("route behavioral witness failure: decode immutable witness definition: %w", err)
	}
	declared := make(map[string]bool, len(definition.RepairResources))
	for _, resourceID := range definition.RepairResources {
		declared[resourceID] = true
	}
	counterexampleOwners := make(map[string]bool, len(definition.CounterexampleRepairResources))
	for _, resourceID := range definition.CounterexampleRepairResources {
		counterexampleOwners[resourceID] = true
	}
	for _, resourceID := range resourceIDs {
		if !declared[resourceID] {
			return nil, fmt.Errorf(
				"route behavioral witness failure: resource %s is not declared in %s repairResources",
				resourceID, definitionID,
			)
		}
		if classification == "theater" && !counterexampleOwners[resourceID] {
			return nil, fmt.Errorf(
				"route behavioral witness failure: theater resource %s is not declared in %s counterexampleRepairResources",
				resourceID, definitionID,
			)
		}
	}

	timestamp := now()
	for _, resourceID := range resourceIDs {
		var existing BehavioralWitnessRepairEpoch
		existingErr := tx.QueryRowContext(ctx, `
			SELECT validation_run_id, definition_hash, source_kind, source_hash,
			       retry_number, max_retries, disposition
			FROM behavioral_witness_repair_cycles
			WHERE validation_run_id = ? AND resource_id = ?`,
			input.ValidationRunID, resourceID,
		).Scan(
			&existing.ValidationRunID, &existing.DefinitionHash,
			&existing.SourceKind, &existing.SourceHash, &existing.RetryCount,
			&existing.MaxRetries, &existing.Disposition,
		)
		if existingErr == nil {
			result.Epochs[resourceID] = existing
			if existing.Disposition == "reopened" {
				result.ReopenedResources = append(result.ReopenedResources, resourceID)
			} else {
				result.ExhaustedResources = append(result.ExhaustedResources, resourceID)
			}
			continue
		}
		if existingErr != sql.ErrNoRows {
			return nil, fmt.Errorf("route behavioral witness failure: inspect replay for %s: %w", resourceID, existingErr)
		}

		var state, executionStrategy, sourceCandidateID string
		resourceErr := tx.QueryRowContext(ctx, `
			SELECT state, execution_strategy, COALESCE(source_candidate_id, '')
			FROM session_resources
			WHERE session_id = ? AND resource_id = ?`, input.SessionID, resourceID,
		).Scan(&state, &executionStrategy, &sourceCandidateID)
		if resourceErr == sql.ErrNoRows {
			return nil, fmt.Errorf(
				"route behavioral witness failure: declared repair owner %s is outside immutable session",
				resourceID,
			)
		}
		if resourceErr != nil {
			return nil, fmt.Errorf("route behavioral witness failure: inspect %s: %w", resourceID, resourceErr)
		}
		if state != "committed" {
			// Concurrent/replayed verification cannot charge an owner already in
			// repair. Only an accepted, committed source is a new repair source.
			continue
		}

		sourceKind, sourceHash, identityErr := behavioralWitnessSourceIdentityTx(
			ctx, tx, input.SessionID, resourceID, executionStrategy,
			sourceCandidateID, sourceTreeHash,
		)
		if identityErr != nil {
			return nil, fmt.Errorf("route behavioral witness failure: identify source for %s: %w", resourceID, identityErr)
		}
		var priorCycles int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM behavioral_witness_repair_cycles
			WHERE session_id = ? AND resource_id = ? AND definition_hash = ?
			  AND source_kind = ? AND source_hash = ?`,
			input.SessionID, resourceID, definitionHash, sourceKind, sourceHash,
		).Scan(&priorCycles); err != nil {
			return nil, fmt.Errorf("route behavioral witness failure: count repair epoch for %s: %w", resourceID, err)
		}
		retryCount := priorCycles + 1
		disposition := "reopened"
		cycleReason := input.Reason
		if input.MaxRepairRetries > 0 && retryCount > input.MaxRepairRetries {
			disposition = "exhausted"
			cycleReason = fmt.Sprintf(
				"behavioral-witness repair retries exhausted: witness=%s retry_count=%d max_retries=%d source_kind=%s source_hash=%s; corrective_action=change_source_candidate_or_witness_definition",
				definitionID, retryCount, input.MaxRepairRetries, sourceKind, sourceHash,
			)
		}
		cycleID := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO behavioral_witness_repair_cycles (
				id, session_id, resource_id, validation_run_id,
				definition_snapshot_id, definition_hash, source_kind, source_hash,
				retry_number, max_retries, disposition, reason, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			cycleID, input.SessionID, resourceID, input.ValidationRunID,
			definitionSnapshotID, definitionHash, sourceKind, sourceHash,
			retryCount, input.MaxRepairRetries, disposition, cycleReason, timestamp,
		); err != nil {
			return nil, fmt.Errorf("route behavioral witness failure: persist repair epoch for %s: %w", resourceID, err)
		}
		epoch := BehavioralWitnessRepairEpoch{
			ValidationRunID: input.ValidationRunID, DefinitionHash: definitionHash,
			SourceKind: sourceKind, SourceHash: sourceHash, RetryCount: retryCount,
			MaxRetries: input.MaxRepairRetries, Disposition: disposition,
		}
		result.Epochs[resourceID] = epoch

		if disposition == "exhausted" {
			updated, updateErr := tx.ExecContext(ctx, `
				UPDATE session_resources
				SET state = 'errored', phase = 'integration_failed', last_error = ?,
				    attempts = 0, job_id = NULL, updated_at = ?
				WHERE session_id = ? AND resource_id = ? AND state = 'committed'`,
				cycleReason, timestamp, input.SessionID, resourceID,
			)
			if updateErr != nil {
				return nil, fmt.Errorf("route behavioral witness failure: exhaust %s: %w", resourceID, updateErr)
			}
			changed, rowsErr := updated.RowsAffected()
			if rowsErr != nil || changed != 1 {
				return nil, fmt.Errorf("route behavioral witness failure: exhaust %s affected=%d: %w", resourceID, changed, rowsErr)
			}
			result.ExhaustedResources = append(result.ExhaustedResources, resourceID)
			continue
		}

		updated, updateErr := tx.ExecContext(ctx, `
			UPDATE session_resources
			SET state = 'pending', phase = 'integration_failed',
			    execution_strategy = 'generate', source_candidate_id = NULL,
			    exhaustion_failure_id = NULL, last_error = ?, attempts = 0,
			    job_id = NULL, updated_at = ?
			WHERE session_id = ? AND resource_id = ? AND state = 'committed'`,
			input.Reason, timestamp, input.SessionID, resourceID,
		)
		if updateErr != nil {
			return nil, fmt.Errorf("route behavioral witness failure: reopen %s: %w", resourceID, updateErr)
		}
		changed, rowsErr := updated.RowsAffected()
		if rowsErr != nil || changed != 1 {
			return nil, fmt.Errorf("route behavioral witness failure: reopen %s affected=%d: %w", resourceID, changed, rowsErr)
		}
		result.ReopenedResources = append(result.ReopenedResources, resourceID)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("route behavioral witness failure: commit: %w", err)
	}
	sort.Strings(result.ReopenedResources)
	sort.Strings(result.ExhaustedResources)
	return result, nil
}

// ActiveBehavioralWitnessRepairExhaustion returns the current content-addressed
// exhaustion, if any. Changed witness definitions are inactive and a genuinely
// changed accepted candidate has a different source identity, so neither can
// be mistaken for the exhausted epoch.
func (s *Store) ActiveBehavioralWitnessRepairExhaustion(
	ctx context.Context,
	sessionID, resourceID string,
) (*BehavioralWitnessRepairExhaustion, error) {
	tx, err := s.sqlDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("inspect behavioral witness exhaustion: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var executionStrategy, sourceCandidateID string
	if err := tx.QueryRowContext(ctx, `
		SELECT execution_strategy, COALESCE(source_candidate_id, '')
		FROM session_resources WHERE session_id = ? AND resource_id = ?`,
		sessionID, resourceID,
	).Scan(&executionStrategy, &sourceCandidateID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect behavioral witness exhaustion: resource: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT cycle.resource_id, definition.definition_id,
		       cycle.definition_hash, cycle.source_kind, cycle.source_hash,
		       cycle.retry_number, cycle.max_retries, cycle.validation_run_id,
		       cycle.reason
		FROM behavioral_witness_repair_cycles AS cycle
		JOIN verification_definitions AS definition
		  ON definition.snapshot_id = cycle.definition_snapshot_id
		WHERE cycle.session_id = ? AND cycle.resource_id = ?
		  AND cycle.disposition = 'exhausted' AND definition.active = 1
		ORDER BY cycle.created_at DESC, cycle.id DESC`, sessionID, resourceID)
	if err != nil {
		return nil, fmt.Errorf("inspect behavioral witness exhaustion: list cycles: %w", err)
	}
	var exhaustions []BehavioralWitnessRepairExhaustion
	for rows.Next() {
		var exhausted BehavioralWitnessRepairExhaustion
		if err := rows.Scan(
			&exhausted.ResourceID, &exhausted.WitnessID,
			&exhausted.DefinitionHash, &exhausted.SourceKind,
			&exhausted.SourceHash, &exhausted.RetryCount,
			&exhausted.MaxRetries, &exhausted.ValidationRunID,
			&exhausted.Reason,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("inspect behavioral witness exhaustion: scan cycle: %w", err)
		}
		exhaustions = append(exhaustions, exhausted)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("inspect behavioral witness exhaustion: iterate cycles: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("inspect behavioral witness exhaustion: close cycles: %w", err)
	}
	for index := range exhaustions {
		exhausted := &exhaustions[index]
		currentKind, currentHash, identityErr := behavioralWitnessSourceIdentityTx(
			ctx, tx, sessionID, resourceID, executionStrategy,
			sourceCandidateID, exhausted.SourceHash,
		)
		if identityErr != nil {
			return nil, fmt.Errorf("inspect behavioral witness exhaustion: current source: %w", identityErr)
		}
		if currentKind == exhausted.SourceKind && currentHash == exhausted.SourceHash {
			return exhausted, nil
		}
	}
	return nil, nil
}

func behavioralWitnessSourceIdentityTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionID, resourceID, executionStrategy, sourceCandidateID, sourceTreeHash string,
) (string, string, error) {
	if executionStrategy == "reuse_candidate" && sourceCandidateID != "" {
		var candidateHash string
		err := tx.QueryRowContext(ctx, `
			SELECT candidate_hash FROM candidate_sets
			WHERE id = ? AND status = 'accepted'`, sourceCandidateID,
		).Scan(&candidateHash)
		if err != nil {
			return "", "", fmt.Errorf("load reused candidate %s: %w", sourceCandidateID, err)
		}
		return "candidate", candidateHash, nil
	}

	var candidateHash string
	err := tx.QueryRowContext(ctx, `
		SELECT candidate.candidate_hash
		FROM candidate_sets AS candidate
		JOIN generation_attempts AS attempt ON attempt.id = candidate.attempt_id
		WHERE attempt.session_id = ? AND attempt.resource_id = ?
		  AND attempt.status = 'accepted' AND candidate.status = 'accepted'
		ORDER BY attempt.retry_number DESC, candidate.created_at DESC, candidate.id DESC
		LIMIT 1`, sessionID, resourceID,
	).Scan(&candidateHash)
	if err == nil {
		return "candidate", candidateHash, nil
	}
	if err != sql.ErrNoRows {
		return "", "", err
	}

	type ownedFile struct {
		Path string `json:"path"`
		Hash string `json:"hash"`
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT path, content_hash FROM generated_files
		WHERE resource_id = ? ORDER BY path`, resourceID)
	if err != nil {
		return "", "", err
	}
	var files []ownedFile
	for rows.Next() {
		var file ownedFile
		if err := rows.Scan(&file.Path, &file.Hash); err != nil {
			_ = rows.Close()
			return "", "", err
		}
		files = append(files, file)
	}
	if err := rows.Close(); err != nil {
		return "", "", err
	}
	if len(files) > 0 {
		canonical, err := json.Marshal(files)
		if err != nil {
			return "", "", err
		}
		return "owned_files", contextmanifest.Hash(string(canonical)), nil
	}
	if strings.TrimSpace(sourceTreeHash) == "" {
		return "", "", fmt.Errorf("no accepted candidate, owned files, or source tree hash")
	}
	return "source_tree", sourceTreeHash, nil
}
