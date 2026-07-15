package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/db"
)

type VerificationDefinition struct {
	SnapshotID     string                     `json:"snapshot_id"`
	DefinitionID   string                     `json:"definition_id"`
	ProjectName    string                     `json:"project_name"`
	DefinitionType string                     `json:"definition_type"`
	Scope          string                     `json:"scope"`
	Kind           string                     `json:"kind"`
	DefinitionHash string                     `json:"definition_hash"`
	DefinitionJSON string                     `json:"definition_json"`
	SpecHash       string                     `json:"spec_hash"`
	Active         bool                       `json:"active"`
	CreatedAt      time.Time                  `json:"created_at"`
	UpdatedAt      time.Time                  `json:"updated_at"`
	Targets        []IntentVerificationTarget `json:"targets"`
}

type ValidationRun struct {
	ID                   string                      `json:"id"`
	DefinitionSnapshotID string                      `json:"definition_snapshot_id"`
	DefinitionID         string                      `json:"definition_id"`
	SessionID            string                      `json:"session_id,omitempty"`
	AttemptID            string                      `json:"attempt_id,omitempty"`
	ExecutionID          string                      `json:"execution_id,omitempty"`
	SourceTreeHash       string                      `json:"source_tree_hash"`
	Classification       string                      `json:"classification"`
	Reason               string                      `json:"reason"`
	StartedAt            time.Time                   `json:"started_at"`
	CompletedAt          time.Time                   `json:"completed_at"`
	DurationMS           int64                       `json:"duration_ms"`
	CreatedAt            time.Time                   `json:"created_at"`
	Definition           *VerificationDefinition     `json:"definition,omitempty"`
	Targets              []IntentVerificationTarget  `json:"targets"`
	Executions           []ValidationExecution       `json:"executions"`
	PredicateResults     []ValidationPredicateResult `json:"predicate_results"`
	Evidence             []VerificationEvidence      `json:"evidence"`
}

type ValidationExecution struct {
	ID               string               `json:"id"`
	Role             string               `json:"role"`
	CommandJSON      string               `json:"command_json"`
	CommandHash      string               `json:"command_hash"`
	WorkingDirectory string               `json:"working_directory"`
	EnvironmentJSON  string               `json:"environment_json"`
	EnvironmentHash  string               `json:"environment_hash"`
	SourceTreeHash   string               `json:"source_tree_hash"`
	ExecutableHash   string               `json:"executable_hash,omitempty"`
	Stdout           string               `json:"stdout"`
	StdoutHash       string               `json:"stdout_hash"`
	StdoutBytes      int64                `json:"stdout_bytes"`
	StdoutTruncated  bool                 `json:"stdout_truncated"`
	Stderr           string               `json:"stderr"`
	StderrHash       string               `json:"stderr_hash"`
	StderrBytes      int64                `json:"stderr_bytes"`
	StderrTruncated  bool                 `json:"stderr_truncated"`
	ExitCode         int                  `json:"exit_code"`
	TimedOut         bool                 `json:"timed_out"`
	LaunchError      string               `json:"launch_error,omitempty"`
	StartedAt        time.Time            `json:"started_at"`
	CompletedAt      time.Time            `json:"completed_at"`
	DurationMS       int64                `json:"duration_ms"`
	Artifacts        []ValidationArtifact `json:"artifacts"`
	Observation      *ParsedObservation   `json:"observation,omitempty"`
}

type ValidationArtifact struct {
	Path          string `json:"path"`
	Content       string `json:"content,omitempty"`
	ContentHash   string `json:"content_hash,omitempty"`
	ByteSize      int    `json:"byte_size"`
	CapturedBytes int    `json:"captured_bytes"`
	Truncated     bool   `json:"truncated"`
	Missing       bool   `json:"missing"`
}

type ParsedObservation struct {
	SchemaJSON      string `json:"schema_json"`
	SchemaHash      string `json:"schema_hash"`
	ObservationJSON string `json:"observation_json"`
	ObservationHash string `json:"observation_hash"`
	ParseError      string `json:"parse_error,omitempty"`
}

type ValidationPredicateResult struct {
	ExecutionID  string `json:"execution_id"`
	Ordinal      int    `json:"ordinal"`
	Field        string `json:"field"`
	Operator     string `json:"operator"`
	ExpectedJSON string `json:"expected_json"`
	ObservedJSON string `json:"observed_json"`
	Passed       bool   `json:"passed"`
	Reason       string `json:"reason"`
}

type VerificationEvidence struct {
	ID             string     `json:"id"`
	EvidenceID     string     `json:"evidence_id"`
	SourceTreeHash string     `json:"source_tree_hash"`
	DefinitionHash string     `json:"definition_hash"`
	Classification string     `json:"classification"`
	Currency       string     `json:"currency"`
	CreatedAt      time.Time  `json:"created_at"`
	InvalidatedAt  *time.Time `json:"invalidated_at,omitempty"`
}

type ValidationRunWrite struct {
	Run         ValidationRun
	ProjectName string
}

type LatestValidationResult struct {
	RunID          string
	Classification string
	SourceTreeHash string
	CreatedAt      time.Time
}

type VerificationCompletionFacts struct {
	LatestRuns      map[string]LatestValidationResult
	CurrentEvidence map[string][]VerificationEvidence
}

func (s *Store) RecordValidationRun(ctx context.Context, input ValidationRunWrite) (_ *ValidationRun, err error) {
	run := input.Run
	if input.ProjectName == "" || run.DefinitionID == "" || run.SourceTreeHash == "" || run.Classification == "" {
		return nil, fmt.Errorf("record validation run: project, definition, source tree hash, and classification are required")
	}
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	definition, err := s.queries.GetActiveVerificationDefinition(ctx, db.GetActiveVerificationDefinitionParams{
		ProjectName: input.ProjectName, DefinitionID: run.DefinitionID,
	})
	if err != nil {
		return nil, fmt.Errorf("record validation run: definition: %w", mapNotFound(err))
	}
	if run.DefinitionSnapshotID != "" && run.DefinitionSnapshotID != definition.SnapshotID {
		return nil, fmt.Errorf("record validation run: definition snapshot is stale")
	}
	run.DefinitionSnapshotID = definition.SnapshotID
	if run.StartedAt.IsZero() || run.CompletedAt.IsZero() || run.CompletedAt.Before(run.StartedAt) {
		return nil, fmt.Errorf("record validation run: valid start and completion times are required")
	}
	if run.DurationMS < 0 {
		return nil, fmt.Errorf("record validation run: duration cannot be negative")
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("record validation run: begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	q := s.queries.WithTx(tx)
	createdAt := now()
	if err = q.PutValidationRun(ctx, db.PutValidationRunParams{
		ID: run.ID, DefinitionSnapshotID: definition.SnapshotID, DefinitionID: run.DefinitionID,
		SessionID: optionalString(run.SessionID), AttemptID: optionalString(run.AttemptID),
		ExecutionID: optionalString(run.ExecutionID), SourceTreeHash: run.SourceTreeHash,
		Classification: run.Classification, Reason: run.Reason,
		StartedAt: formatTime(run.StartedAt), CompletedAt: formatTime(run.CompletedAt),
		DurationMs: run.DurationMS, CreatedAt: createdAt,
	}); err != nil {
		return nil, fmt.Errorf("record validation run: insert run: %w", err)
	}

	targetRows, err := q.ListVerificationDefinitionTargets(ctx, definition.SnapshotID)
	if err != nil {
		return nil, err
	}
	for _, target := range targetRows {
		if target.TargetKind == "completion_check" {
			continue
		}
		if err = q.PutValidationRunTarget(ctx, db.PutValidationRunTargetParams{
			RunID: run.ID, TargetKind: target.TargetKind, TargetID: target.TargetID,
		}); err != nil {
			return nil, err
		}
	}

	putBlob := func(content string) (string, error) {
		hash := contextmanifest.Hash(content)
		err := q.PutContentBlob(ctx, db.PutContentBlobParams{
			Hash: hash, Content: []byte(content), ByteSize: int64(len([]byte(content))), CreatedAt: createdAt,
		})
		return hash, err
	}
	for index := range run.Executions {
		execution := &run.Executions[index]
		if execution.ID == "" {
			execution.ID = uuid.NewString()
		}
		stdoutHash, blobErr := putBlob(execution.Stdout)
		if blobErr != nil {
			return nil, blobErr
		}
		stderrHash, blobErr := putBlob(execution.Stderr)
		if blobErr != nil {
			return nil, blobErr
		}
		if execution.StdoutHash == "" {
			execution.StdoutHash = stdoutHash
		}
		if execution.StderrHash == "" {
			execution.StderrHash = stderrHash
		}
		if execution.StdoutBytes == 0 {
			execution.StdoutBytes = int64(len([]byte(execution.Stdout)))
		}
		if execution.StderrBytes == 0 {
			execution.StderrBytes = int64(len([]byte(execution.Stderr)))
		}
		if err = q.PutValidationExecution(ctx, db.PutValidationExecutionParams{
			ID: execution.ID, RunID: run.ID, ExecutionRole: execution.Role,
			CommandJson: execution.CommandJSON, CommandHash: execution.CommandHash,
			WorkingDirectory: execution.WorkingDirectory, EnvironmentJson: execution.EnvironmentJSON,
			EnvironmentHash: execution.EnvironmentHash, SourceTreeHash: execution.SourceTreeHash,
			ExecutableHash: execution.ExecutableHash, StdoutBlob: stdoutHash, StderrBlob: stderrHash,
			StdoutHash: execution.StdoutHash, StderrHash: execution.StderrHash,
			StdoutBytes: execution.StdoutBytes, StderrBytes: execution.StderrBytes,
			StdoutTruncated: boolInt(execution.StdoutTruncated), StderrTruncated: boolInt(execution.StderrTruncated),
			ExitCode: int64(execution.ExitCode), TimedOut: boolInt(execution.TimedOut),
			LaunchError: execution.LaunchError, StartedAt: formatTime(execution.StartedAt),
			CompletedAt: formatTime(execution.CompletedAt), DurationMs: execution.DurationMS,
		}); err != nil {
			return nil, fmt.Errorf("record validation run: execution %s: %w", execution.Role, err)
		}
		for _, artifact := range execution.Artifacts {
			var contentBlob *string
			if !artifact.Missing {
				hash, artifactErr := putBlob(artifact.Content)
				if artifactErr != nil {
					return nil, artifactErr
				}
				if artifact.ContentHash == "" {
					artifact.ContentHash = hash
				}
				contentBlob = &hash
			}
			if err = q.PutValidationArtifact(ctx, db.PutValidationArtifactParams{
				ExecutionID: execution.ID, Path: artifact.Path, ContentBlob: contentBlob,
				ContentHash: artifact.ContentHash, ByteSize: int64(artifact.ByteSize),
				CapturedBytes: int64(artifact.CapturedBytes), Truncated: boolInt(artifact.Truncated),
				Missing: boolInt(artifact.Missing),
			}); err != nil {
				return nil, err
			}
		}
		if execution.Observation != nil {
			observation := execution.Observation
			if err = q.PutParsedObservation(ctx, db.PutParsedObservationParams{
				ExecutionID: execution.ID, SchemaJson: observation.SchemaJSON, SchemaHash: observation.SchemaHash,
				ObservationJson: observation.ObservationJSON, ObservationHash: observation.ObservationHash,
				ParseError: observation.ParseError,
			}); err != nil {
				return nil, err
			}
		}
	}
	for _, predicate := range run.PredicateResults {
		if err = q.PutValidationPredicateResult(ctx, db.PutValidationPredicateResultParams{
			RunID: run.ID, ExecutionID: predicate.ExecutionID, Ordinal: int64(predicate.Ordinal),
			FieldName: predicate.Field, Operator: predicate.Operator, ExpectedJson: predicate.ExpectedJSON,
			ObservedJson: predicate.ObservedJSON, Passed: boolInt(predicate.Passed), Reason: predicate.Reason,
		}); err != nil {
			return nil, err
		}
	}
	for _, target := range targetRows {
		if target.TargetKind != "evidence" {
			continue
		}
		invalidatedAt := createdAt
		stale, staleErr := q.MarkVerificationEvidenceStale(ctx, db.MarkVerificationEvidenceStaleParams{
			InvalidatedAt: &invalidatedAt, EvidenceID: target.TargetID,
		})
		if staleErr != nil {
			return nil, staleErr
		}
		for _, record := range stale {
			if err = q.PutVerificationEvidenceInvalidation(ctx, db.PutVerificationEvidenceInvalidationParams{
				ID: uuid.NewString(), EvidenceRecordID: record.ID,
				Reason: "superseded by validation run " + run.ID, SourceType: "validation_run",
				SourceID: run.ID, CreatedAt: createdAt,
			}); err != nil {
				return nil, err
			}
		}
		if err = q.PutVerificationEvidenceRecord(ctx, db.PutVerificationEvidenceRecordParams{
			ID: uuid.NewString(), RunID: run.ID, EvidenceID: target.TargetID,
			SourceTreeHash: run.SourceTreeHash, DefinitionHash: definition.DefinitionHash,
			Classification: run.Classification, CreatedAt: createdAt,
		}); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("record validation run: commit: %w", err)
	}
	return s.GetValidationRun(ctx, run.ID)
}

func (s *Store) ListVerificationDefinitions(ctx context.Context, projectName string) ([]VerificationDefinition, error) {
	rows, err := s.queries.ListActiveVerificationDefinitions(ctx, projectName)
	if err != nil {
		return nil, err
	}
	result := make([]VerificationDefinition, 0, len(rows))
	for _, row := range rows {
		definition, hydrateErr := s.hydrateVerificationDefinition(ctx, row)
		if hydrateErr != nil {
			return nil, hydrateErr
		}
		result = append(result, *definition)
	}
	return result, nil
}

func (s *Store) GetValidationRun(ctx context.Context, id string) (*ValidationRun, error) {
	row, err := s.queries.GetValidationRun(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateValidationRun(ctx, row)
}

func (s *Store) ListValidationRuns(ctx context.Context, limit int) ([]ValidationRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListValidationRuns(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	result := make([]ValidationRun, 0, len(rows))
	for _, row := range rows {
		run, hydrateErr := s.hydrateValidationRun(ctx, row)
		if hydrateErr != nil {
			return nil, hydrateErr
		}
		result = append(result, *run)
	}
	return result, nil
}

func (s *Store) GetVerificationCompletionFacts(ctx context.Context, projectName string) (*VerificationCompletionFacts, error) {
	runs, err := s.queries.ListLatestValidationRunsForProject(ctx, projectName)
	if err != nil {
		return nil, err
	}
	evidenceRows, err := s.queries.ListCurrentVerificationEvidenceForProject(ctx, projectName)
	if err != nil {
		return nil, err
	}
	facts := &VerificationCompletionFacts{
		LatestRuns:      make(map[string]LatestValidationResult),
		CurrentEvidence: make(map[string][]VerificationEvidence),
	}
	for _, run := range runs {
		facts.LatestRuns[run.DefinitionID] = LatestValidationResult{
			RunID: run.RunID, Classification: run.Classification,
			SourceTreeHash: run.SourceTreeHash, CreatedAt: parseTime(run.CreatedAt),
		}
	}
	for _, evidence := range evidenceRows {
		facts.CurrentEvidence[evidence.EvidenceID] = append(facts.CurrentEvidence[evidence.EvidenceID], VerificationEvidence{
			ID: evidence.ID, EvidenceID: evidence.EvidenceID, SourceTreeHash: evidence.SourceTreeHash,
			DefinitionHash: evidence.DefinitionHash, Classification: evidence.Classification,
			Currency: evidence.Currency, CreatedAt: parseTime(evidence.CreatedAt),
			InvalidatedAt: parseOptionalTime(evidence.InvalidatedAt),
		})
	}
	return facts, nil
}

func (s *Store) InvalidateVerificationEvidence(ctx context.Context, projectName string, resourceIDs []string, reason string, source TransitionSource) (count int, err error) {
	if projectName == "" || len(resourceIDs) == 0 || reason == "" || source.Type == "" {
		return 0, fmt.Errorf("invalidate verification evidence: project, resources, reason, and source type are required")
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	q := s.queries.WithTx(tx)
	timestamp := now()
	seen := make(map[string]bool)
	for _, resourceID := range resourceIDs {
		records, queryErr := q.ListCurrentVerificationEvidenceForResource(ctx, db.ListCurrentVerificationEvidenceForResourceParams{
			ProjectName: projectName, TargetID: resourceID,
		})
		if queryErr != nil {
			return count, queryErr
		}
		for _, record := range records {
			if seen[record.ID] {
				continue
			}
			seen[record.ID] = true
			affected, updateErr := q.MarkVerificationEvidenceRecordStale(ctx, db.MarkVerificationEvidenceRecordStaleParams{
				InvalidatedAt: &timestamp, ID: record.ID,
			})
			if updateErr != nil {
				return count, updateErr
			}
			if affected == 0 {
				continue
			}
			if err = q.PutVerificationEvidenceInvalidation(ctx, db.PutVerificationEvidenceInvalidationParams{
				ID: uuid.NewString(), EvidenceRecordID: record.ID, Reason: reason,
				SourceType: source.Type, SourceID: source.ID, CreatedAt: timestamp,
			}); err != nil {
				return count, err
			}
			count++
		}
	}
	if err = tx.Commit(); err != nil {
		return count, err
	}
	return count, nil
}

func (s *Store) hydrateVerificationDefinition(ctx context.Context, row db.VerificationDefinition) (*VerificationDefinition, error) {
	targetRows, err := s.queries.ListVerificationDefinitionTargets(ctx, row.SnapshotID)
	if err != nil {
		return nil, err
	}
	definition := &VerificationDefinition{
		SnapshotID: row.SnapshotID, DefinitionID: row.DefinitionID, ProjectName: row.ProjectName,
		DefinitionType: row.DefinitionType, Scope: row.Scope, Kind: row.Kind,
		DefinitionHash: row.DefinitionHash, DefinitionJSON: row.DefinitionJson, SpecHash: row.SpecHash,
		Active: row.Active == 1, CreatedAt: parseTime(row.CreatedAt), UpdatedAt: parseTime(row.UpdatedAt),
	}
	for _, target := range targetRows {
		definition.Targets = append(definition.Targets, IntentVerificationTarget{Kind: target.TargetKind, ID: target.TargetID})
	}
	return definition, nil
}

func (s *Store) hydrateValidationRun(ctx context.Context, row db.ValidationRun) (*ValidationRun, error) {
	definitionRow, err := s.queries.GetVerificationDefinition(ctx, row.DefinitionSnapshotID)
	if err != nil {
		return nil, err
	}
	definition, err := s.hydrateVerificationDefinition(ctx, definitionRow)
	if err != nil {
		return nil, err
	}
	run := &ValidationRun{
		ID: row.ID, DefinitionSnapshotID: row.DefinitionSnapshotID, DefinitionID: row.DefinitionID,
		SessionID: stringVal(row.SessionID), AttemptID: stringVal(row.AttemptID), ExecutionID: stringVal(row.ExecutionID),
		SourceTreeHash: row.SourceTreeHash, Classification: row.Classification, Reason: row.Reason,
		StartedAt: parseTime(row.StartedAt), CompletedAt: parseTime(row.CompletedAt), DurationMS: row.DurationMs,
		CreatedAt: parseTime(row.CreatedAt), Definition: definition,
	}
	targetRows, err := s.queries.ListValidationRunTargets(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	for _, target := range targetRows {
		run.Targets = append(run.Targets, IntentVerificationTarget{Kind: target.TargetKind, ID: target.TargetID})
	}
	executionRows, err := s.queries.ListValidationExecutionsByRun(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	for _, executionRow := range executionRows {
		stdout, getErr := s.queries.GetContentBlob(ctx, executionRow.StdoutBlob)
		if getErr != nil {
			return nil, getErr
		}
		stderr, getErr := s.queries.GetContentBlob(ctx, executionRow.StderrBlob)
		if getErr != nil {
			return nil, getErr
		}
		execution := ValidationExecution{
			ID: executionRow.ID, Role: executionRow.ExecutionRole, CommandJSON: executionRow.CommandJson,
			CommandHash: executionRow.CommandHash, WorkingDirectory: executionRow.WorkingDirectory,
			EnvironmentJSON: executionRow.EnvironmentJson, EnvironmentHash: executionRow.EnvironmentHash,
			SourceTreeHash: executionRow.SourceTreeHash, ExecutableHash: executionRow.ExecutableHash,
			Stdout: string(stdout.Content), StdoutHash: executionRow.StdoutHash,
			StdoutBytes: executionRow.StdoutBytes, StdoutTruncated: executionRow.StdoutTruncated == 1,
			Stderr: string(stderr.Content), StderrHash: executionRow.StderrHash,
			StderrBytes: executionRow.StderrBytes, StderrTruncated: executionRow.StderrTruncated == 1,
			ExitCode: int(executionRow.ExitCode), TimedOut: executionRow.TimedOut == 1,
			LaunchError: executionRow.LaunchError, StartedAt: parseTime(executionRow.StartedAt),
			CompletedAt: parseTime(executionRow.CompletedAt), DurationMS: executionRow.DurationMs,
		}
		artifacts, artifactErr := s.queries.ListValidationArtifactsByExecution(ctx, execution.ID)
		if artifactErr != nil {
			return nil, artifactErr
		}
		for _, artifactRow := range artifacts {
			artifact := ValidationArtifact{
				Path: artifactRow.Path, ContentHash: artifactRow.ContentHash, ByteSize: int(artifactRow.ByteSize),
				CapturedBytes: int(artifactRow.CapturedBytes), Truncated: artifactRow.Truncated == 1,
				Missing: artifactRow.Missing == 1,
			}
			if artifactRow.ContentBlob != nil {
				blob, blobErr := s.queries.GetContentBlob(ctx, *artifactRow.ContentBlob)
				if blobErr != nil {
					return nil, blobErr
				}
				artifact.Content = string(blob.Content)
			}
			execution.Artifacts = append(execution.Artifacts, artifact)
		}
		observationRow, observationErr := s.queries.GetParsedObservation(ctx, execution.ID)
		if observationErr == nil {
			execution.Observation = &ParsedObservation{
				SchemaJSON: observationRow.SchemaJson, SchemaHash: observationRow.SchemaHash,
				ObservationJSON: observationRow.ObservationJson, ObservationHash: observationRow.ObservationHash,
				ParseError: observationRow.ParseError,
			}
		} else if observationErr != sql.ErrNoRows {
			return nil, observationErr
		}
		run.Executions = append(run.Executions, execution)
	}
	predicates, err := s.queries.ListValidationPredicateResults(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	for _, predicate := range predicates {
		run.PredicateResults = append(run.PredicateResults, ValidationPredicateResult{
			ExecutionID: predicate.ExecutionID, Ordinal: int(predicate.Ordinal), Field: predicate.FieldName,
			Operator: predicate.Operator, ExpectedJSON: predicate.ExpectedJson, ObservedJSON: predicate.ObservedJson,
			Passed: predicate.Passed == 1, Reason: predicate.Reason,
		})
	}
	evidenceRows, err := s.queries.ListVerificationEvidenceByRun(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	for _, evidence := range evidenceRows {
		run.Evidence = append(run.Evidence, VerificationEvidence{
			ID: evidence.ID, EvidenceID: evidence.EvidenceID, SourceTreeHash: evidence.SourceTreeHash,
			DefinitionHash: evidence.DefinitionHash, Classification: evidence.Classification,
			Currency: evidence.Currency, CreatedAt: parseTime(evidence.CreatedAt),
			InvalidatedAt: parseOptionalTime(evidence.InvalidatedAt),
		})
	}
	return run, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
