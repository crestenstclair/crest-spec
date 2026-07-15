package spec

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type validationProvenance struct {
	SessionID   string
	AttemptID   string
	ExecutionID string
}

func (s *Spec) executeAndRecordValidations(ctx context.Context, projectName string, definitions []cuepkg.Validation, root string, provenance validationProvenance) []ValidationResult {
	results := ExecuteValidations(ctx, definitions, root)
	for index := range results {
		result := &results[index]
		if result.Execution == nil {
			continue
		}
		execution := result.Execution
		executionID := uuid.NewString()
		write := store.ValidationRunWrite{
			ProjectName: projectName,
			Run: store.ValidationRun{
				DefinitionID: result.ID, SessionID: provenance.SessionID, AttemptID: provenance.AttemptID,
				ExecutionID: provenance.ExecutionID, SourceTreeHash: execution.SourceTreeHash,
				Classification: result.Classification, Reason: result.Message,
				StartedAt: execution.StartedAt, CompletedAt: execution.CompletedAt, DurationMS: execution.DurationMS,
				Executions: []store.ValidationExecution{{
					ID: executionID, Role: "validation", CommandJSON: execution.CommandJSON,
					CommandHash: execution.CommandHash, WorkingDirectory: execution.WorkingDirectory,
					EnvironmentJSON: execution.EnvironmentJSON, EnvironmentHash: execution.EnvironmentHash,
					SourceTreeHash: execution.SourceTreeHash, ExecutableHash: execution.ExecutableHash,
					Stdout: execution.Stdout, StdoutHash: execution.StdoutHash,
					StdoutBytes: execution.StdoutBytes, StdoutTruncated: execution.StdoutTruncated,
					Stderr: execution.Stderr, StderrHash: execution.StderrHash,
					StderrBytes: execution.StderrBytes, StderrTruncated: execution.StderrTruncated,
					ExitCode: execution.ExitCode, TimedOut: execution.TimedOut, LaunchError: execution.LaunchError,
					StartedAt: execution.StartedAt, CompletedAt: execution.CompletedAt, DurationMS: execution.DurationMS,
				}},
			},
		}
		for _, artifact := range execution.Artifacts {
			write.Run.Executions[0].Artifacts = append(write.Run.Executions[0].Artifacts, store.ValidationArtifact{
				Path: artifact.Path, Content: artifact.Content, ContentHash: artifact.ContentHash,
				ByteSize: int(artifact.ByteSize), CapturedBytes: int(artifact.CapturedBytes),
				Truncated: artifact.Truncated, Missing: artifact.Missing,
			})
		}
		for ordinal, assertion := range result.Assertions {
			expected, _ := json.Marshal(definitions[index].Assertions[ordinal])
			observed, _ := json.Marshal(map[string]any{
				"exit_code": execution.ExitCode, "stdout_hash": execution.StdoutHash,
				"stderr_hash": execution.StderrHash,
			})
			write.Run.PredicateResults = append(write.Run.PredicateResults, store.ValidationPredicateResult{
				ExecutionID: executionID, Ordinal: ordinal, Field: definitions[index].Assertions[ordinal].Path,
				Operator: assertion.Kind, ExpectedJSON: string(expected), ObservedJSON: string(observed),
				Passed: assertion.Passed, Reason: assertion.Message,
			})
		}
		persisted, err := s.store.RecordValidationRun(ctx, write)
		if err != nil {
			result.Passed = false
			result.Classification = "error"
			result.Message = fmt.Sprintf("validation provenance persistence failed: %v", err)
			continue
		}
		if persisted != nil {
			// A successful write proves the run can be inspected by this stable ID.
			result.RunID = persisted.ID
			result.Message = withRunReference(result.Message, persisted.ID)
		}
	}
	return results
}

func withRunReference(message, runID string) string {
	if message == "" {
		return "validation run " + runID
	}
	return message + "\nvalidation run: " + runID
}

func (s *Spec) ListValidationRuns(ctx context.Context, limit int) ([]store.ValidationRun, error) {
	return s.store.ListValidationRuns(ctx, limit)
}

func (s *Spec) GetValidationRun(ctx context.Context, runID string) (*store.ValidationRun, error) {
	return s.store.GetValidationRun(ctx, runID)
}

func (s *Spec) ListVerificationDefinitions(ctx context.Context, projectName string) ([]store.VerificationDefinition, error) {
	if projectName == "" {
		project, err := s.ProjectOverview(ctx)
		if err != nil {
			return nil, err
		}
		projectName = project.Project.ProjectName
	}
	return s.store.ListVerificationDefinitions(ctx, projectName)
}
