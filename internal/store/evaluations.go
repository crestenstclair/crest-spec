package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/db"
	"github.com/crestenstclair/crest-spec/internal/execution"
)

type EvaluationCaseAssessment struct {
	SourceAttemptID string          `json:"source_attempt_id"`
	Eligible        bool            `json:"eligible"`
	Reason          string          `json:"reason"`
	Case            *EvaluationCase `json:"case,omitempty"`
	AssessedAt      time.Time       `json:"assessed_at"`
}

type EvaluationCase struct {
	ID                      string         `json:"id"`
	IdentityHash            string         `json:"identity_hash"`
	Provenance              string         `json:"provenance"`
	SourceAttemptID         string         `json:"source_attempt_id,omitempty"`
	ProjectName             string         `json:"project_name"`
	GoalID                  string         `json:"goal_id,omitempty"`
	CapabilityID            string         `json:"capability_id,omitempty"`
	ResourceID              string         `json:"resource_id"`
	SpecHash                string         `json:"spec_hash"`
	RepositoryHash          string         `json:"repository_hash"`
	ResourceDeclarationHash string         `json:"resource_declaration_hash"`
	PlanOperationID         string         `json:"plan_operation_id,omitempty"`
	ContextManifestID       string         `json:"context_manifest_id,omitempty"`
	ExecutionID             string         `json:"execution_id,omitempty"`
	CandidateID             string         `json:"candidate_id,omitempty"`
	Payload                 map[string]any `json:"payload"`
	PayloadHash             string         `json:"payload_hash"`
	ExpectedOutcome         map[string]any `json:"expected_outcome"`
	ExpectedOutcomeHash     string         `json:"expected_outcome_hash"`
	CreatedAt               time.Time      `json:"created_at"`
}

type CuratedEvaluationCase struct {
	Provenance              string         `json:"provenance,omitempty"`
	ProjectName             string         `json:"project_name"`
	GoalID                  string         `json:"goal_id,omitempty"`
	CapabilityID            string         `json:"capability_id,omitempty"`
	ResourceID              string         `json:"resource_id"`
	SpecHash                string         `json:"spec_hash"`
	RepositoryHash          string         `json:"repository_hash"`
	ResourceDeclarationHash string         `json:"resource_declaration_hash"`
	PlanOperationID         string         `json:"plan_operation_id,omitempty"`
	Payload                 map[string]any `json:"payload"`
	ExpectedOutcome         map[string]any `json:"expected_outcome"`
}

type EvaluationDataset struct {
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	Description  string                    `json:"description,omitempty"`
	IdentityHash string                    `json:"identity_hash,omitempty"`
	Status       string                    `json:"status"`
	CreatedAt    time.Time                 `json:"created_at"`
	SealedAt     *time.Time                `json:"sealed_at,omitempty"`
	Cases        []EvaluationDatasetMember `json:"cases"`
}

type EvaluationDatasetMember struct {
	CaseID            string `json:"case_id"`
	CaseIdentityHash  string `json:"case_identity_hash"`
	Split             string `json:"split"`
	Ordinal           int    `json:"ordinal"`
	Provenance        string `json:"provenance"`
	ProjectName       string `json:"project_name"`
	GoalID            string `json:"goal_id,omitempty"`
	CapabilityID      string `json:"capability_id,omitempty"`
	ResourceID        string `json:"resource_id"`
	ContextManifestID string `json:"context_manifest_id,omitempty"`
	ExecutionID       string `json:"execution_id,omitempty"`
	CandidateID       string `json:"candidate_id,omitempty"`
}

type EvaluationConfiguration struct {
	ID                     string            `json:"id"`
	Name                   string            `json:"name"`
	IdentityHash           string            `json:"identity_hash"`
	PlannerVersion         string            `json:"planner_version"`
	PlannerPolicy          map[string]any    `json:"planner_policy"`
	PlannerPolicyHash      string            `json:"planner_policy_hash"`
	ContextSelectorVersion string            `json:"context_selector_version"`
	ContextBudgetTokens    int               `json:"context_budget_tokens"`
	TemplateHashes         map[string]string `json:"template_hashes"`
	RolePolicyVersion      string            `json:"role_policy_version"`
	HostName               string            `json:"host_name"`
	HostVersion            string            `json:"host_version,omitempty"`
	Provider               string            `json:"provider,omitempty"`
	Model                  string            `json:"model"`
	InferenceConfig        map[string]any    `json:"inference_config"`
	ValidationVersion      string            `json:"validation_version"`
	LearningIDs            []string          `json:"learning_ids"`
	PayloadHash            string            `json:"payload_hash"`
	CreatedAt              time.Time         `json:"created_at"`
}

func (s *Store) CreateHistoricalEvaluationCase(ctx context.Context, attemptID, projectName string) (*EvaluationCaseAssessment, error) {
	assessedAt := time.Now().UTC()
	ineligible := func(reason string) (*EvaluationCaseAssessment, error) {
		if putErr := s.queries.PutEvaluationCaseAssessment(ctx, db.PutEvaluationCaseAssessmentParams{
			SourceAttemptID: attemptID, Eligible: 0, Reason: reason, AssessedAt: formatTime(assessedAt),
		}); putErr != nil {
			return nil, putErr
		}
		return &EvaluationCaseAssessment{SourceAttemptID: attemptID, Reason: reason, AssessedAt: assessedAt}, nil
	}
	if attemptID == "" || projectName == "" {
		return nil, fmt.Errorf("create historical evaluation case: attempt and project are required")
	}
	manifest, err := s.GetContextManifestByAttempt(ctx, attemptID)
	if err != nil || manifest.Blocked {
		return ineligible("attempt has no complete unblocked context manifest")
	}
	executionManifest, err := s.GetExecutionManifestByAttempt(ctx, attemptID)
	if err != nil || !evaluationTerminalExecution(executionManifest.Status) {
		return ineligible("attempt has no terminal execution manifest")
	}
	candidate, err := s.GetCandidateSetByAttempt(ctx, attemptID)
	if err != nil || (candidate.Status != "accepted" && candidate.Status != "rejected") {
		return ineligible("attempt has no terminal candidate snapshot")
	}
	validationRuns, err := s.queries.ListEvaluationSourceValidationRuns(ctx, stringPtr(attemptID))
	if err != nil || len(validationRuns) == 0 {
		return ineligible("attempt has no persisted validation provenance")
	}
	resourceDeclarationHash := ""
	for _, section := range manifest.Sections {
		if section.Kind == "resource_contract" {
			resourceDeclarationHash = section.OriginalHash
			break
		}
	}
	if resourceDeclarationHash == "" {
		return ineligible("context manifest has no resource contract snapshot")
	}
	project, err := s.GetProjectIntent(ctx, projectName)
	if err != nil {
		return ineligible("project intent is not available in SQLite")
	}
	apply, err := s.GetApply(manifest.Attempt.ApplyID)
	if err != nil {
		return ineligible("source apply is not available")
	}
	repositoryHash := validationRuns[0].SourceTreeHash
	if repositoryHash == "" {
		return ineligible("validation provenance has no source tree hash")
	}
	goalID, capabilityID := "", ""
	if len(executionManifest.GoalIDs) > 0 {
		goalID = executionManifest.GoalIDs[0]
	}
	if len(executionManifest.CapabilityIDs) > 0 {
		capabilityID = executionManifest.CapabilityIDs[0]
	}
	validations := make([]map[string]any, 0, len(validationRuns))
	for _, run := range validationRuns {
		validations = append(validations, map[string]any{
			"run_id": run.ID, "definition_id": run.DefinitionID,
			"classification": run.Classification, "source_tree_hash": run.SourceTreeHash,
		})
	}
	payload := map[string]any{
		"project_intent": project, "project_spec_hash": apply.SpecHash,
		"goal_id": goalID, "capability_id": capabilityID,
		"resource_id": manifest.Attempt.ResourceID, "resource_declaration_hash": resourceDeclarationHash,
		"plan_operation_id":   manifest.Attempt.PlanOperationID,
		"context_manifest_id": manifest.ID, "context_hash": manifest.ContextHash,
		"execution_id": executionManifest.ID, "candidate_id": candidate.ID,
		"candidate_hash": candidate.CandidateHash, "repository_hash": repositoryHash,
	}
	expected := map[string]any{
		"execution_status": executionManifest.Status, "candidate_status": candidate.Status,
		"goal_progress": executionManifest.GoalProgress, "validations": validations,
	}
	created, err := s.createEvaluationCase(ctx, CuratedEvaluationCase{
		Provenance: "historical", ProjectName: projectName, GoalID: goalID, CapabilityID: capabilityID,
		ResourceID: manifest.Attempt.ResourceID, SpecHash: apply.SpecHash, RepositoryHash: repositoryHash,
		ResourceDeclarationHash: resourceDeclarationHash, PlanOperationID: manifest.Attempt.PlanOperationID,
		Payload: payload, ExpectedOutcome: expected,
	}, attemptID, manifest.ID, executionManifest.ID, candidate.ID)
	if err != nil {
		return nil, err
	}
	if err := s.queries.PutEvaluationCaseAssessment(ctx, db.PutEvaluationCaseAssessmentParams{
		SourceAttemptID: attemptID, Eligible: 1, Reason: "complete immutable attempt chain",
		CaseID: &created.ID, AssessedAt: formatTime(assessedAt),
	}); err != nil {
		return nil, err
	}
	return &EvaluationCaseAssessment{
		SourceAttemptID: attemptID, Eligible: true, Reason: "complete immutable attempt chain",
		Case: created, AssessedAt: assessedAt,
	}, nil
}

func (s *Store) CreateCuratedEvaluationCase(ctx context.Context, input CuratedEvaluationCase) (*EvaluationCase, error) {
	if input.Provenance == "" {
		input.Provenance = "curated"
	}
	if input.Provenance != "curated" && input.Provenance != "imported" {
		return nil, fmt.Errorf("create curated evaluation case: provenance must be curated or imported")
	}
	return s.createEvaluationCase(ctx, input, "", "", "", "")
}

func (s *Store) createEvaluationCase(ctx context.Context, input CuratedEvaluationCase, attemptID, contextID, executionID, candidateID string) (*EvaluationCase, error) {
	if input.ProjectName == "" || input.ResourceID == "" || input.SpecHash == "" || input.RepositoryHash == "" || input.ResourceDeclarationHash == "" {
		return nil, fmt.Errorf("create evaluation case: project, resource, spec, repository, and declaration hashes are required")
	}
	payloadJSON, payloadHash, err := execution.CanonicalRedacted(input.Payload)
	if err != nil {
		return nil, fmt.Errorf("create evaluation case: payload: %w", err)
	}
	expectedJSON, expectedHash, err := execution.CanonicalRedacted(input.ExpectedOutcome)
	if err != nil {
		return nil, fmt.Errorf("create evaluation case: expected outcome: %w", err)
	}
	identityInput := map[string]any{
		"provenance": input.Provenance, "project_name": input.ProjectName,
		"goal_id": input.GoalID, "capability_id": input.CapabilityID, "resource_id": input.ResourceID,
		"spec_hash": input.SpecHash, "repository_hash": input.RepositoryHash,
		"resource_declaration_hash": input.ResourceDeclarationHash, "plan_operation_id": input.PlanOperationID,
		"payload_hash": payloadHash, "expected_outcome_hash": expectedHash,
	}
	_, identityHash, err := execution.CanonicalRedacted(identityInput)
	if err != nil {
		return nil, err
	}
	if row, getErr := s.queries.GetEvaluationCaseByIdentity(ctx, identityHash); getErr == nil {
		return s.hydrateEvaluationCase(ctx, row)
	} else if !errors.Is(getErr, sql.ErrNoRows) {
		return nil, getErr
	}
	timestamp := now()
	if err := s.putEvaluationBlob(ctx, payloadHash, payloadJSON, timestamp); err != nil {
		return nil, err
	}
	if err := s.putEvaluationBlob(ctx, expectedHash, expectedJSON, timestamp); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	if err := s.queries.PutEvaluationCase(ctx, db.PutEvaluationCaseParams{
		ID: id, IdentityHash: identityHash, Provenance: input.Provenance,
		SourceAttemptID: optionalString(attemptID), ProjectName: input.ProjectName,
		GoalID: input.GoalID, CapabilityID: input.CapabilityID, ResourceID: input.ResourceID,
		SpecHash: input.SpecHash, RepositoryHash: input.RepositoryHash,
		ResourceDeclarationHash: input.ResourceDeclarationHash, PlanOperationID: input.PlanOperationID,
		ContextManifestID: optionalString(contextID), ExecutionID: optionalString(executionID),
		CandidateID: optionalString(candidateID), PayloadBlob: payloadHash,
		ExpectedOutcomeBlob: expectedHash, CreatedAt: timestamp,
	}); err != nil {
		return nil, err
	}
	return s.GetEvaluationCase(ctx, id)
}

func (s *Store) GetEvaluationCase(ctx context.Context, id string) (*EvaluationCase, error) {
	row, err := s.queries.GetEvaluationCase(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateEvaluationCase(ctx, row)
}

func (s *Store) ListEvaluationCases(ctx context.Context, limit int) ([]EvaluationCase, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListEvaluationCases(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	result := make([]EvaluationCase, 0, len(rows))
	for _, row := range rows {
		item, hydrateErr := s.hydrateEvaluationCase(ctx, row)
		if hydrateErr != nil {
			return nil, hydrateErr
		}
		result = append(result, *item)
	}
	return result, nil
}

func (s *Store) hydrateEvaluationCase(ctx context.Context, row db.EvaluationCase) (*EvaluationCase, error) {
	payload, err := s.queries.GetContentBlob(ctx, row.PayloadBlob)
	if err != nil {
		return nil, err
	}
	expected, err := s.queries.GetContentBlob(ctx, row.ExpectedOutcomeBlob)
	if err != nil {
		return nil, err
	}
	result := &EvaluationCase{
		ID: row.ID, IdentityHash: row.IdentityHash, Provenance: row.Provenance,
		SourceAttemptID: stringVal(row.SourceAttemptID), ProjectName: row.ProjectName,
		GoalID: row.GoalID, CapabilityID: row.CapabilityID, ResourceID: row.ResourceID,
		SpecHash: row.SpecHash, RepositoryHash: row.RepositoryHash,
		ResourceDeclarationHash: row.ResourceDeclarationHash, PlanOperationID: row.PlanOperationID,
		ContextManifestID: stringVal(row.ContextManifestID), ExecutionID: stringVal(row.ExecutionID),
		CandidateID: stringVal(row.CandidateID), PayloadHash: row.PayloadBlob,
		ExpectedOutcomeHash: row.ExpectedOutcomeBlob, CreatedAt: parseTime(row.CreatedAt),
	}
	if err := json.Unmarshal(payload.Content, &result.Payload); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(expected.Content, &result.ExpectedOutcome); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) CreateEvaluationDataset(ctx context.Context, name, description string) (*EvaluationDataset, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("create evaluation dataset: name is required")
	}
	id, timestamp := uuid.NewString(), now()
	if err := s.queries.PutEvaluationDataset(ctx, db.PutEvaluationDatasetParams{
		ID: id, Name: name, Description: description, Status: "draft", CreatedAt: timestamp,
	}); err != nil {
		return nil, err
	}
	return s.GetEvaluationDataset(ctx, id)
}

func (s *Store) AddEvaluationDatasetCase(ctx context.Context, datasetID, caseID, split string) error {
	if split != "training" && split != "development" && split != "held_out" {
		return fmt.Errorf("add evaluation dataset case: invalid split %q", split)
	}
	dataset, err := s.queries.GetEvaluationDataset(ctx, datasetID)
	if err != nil {
		return mapNotFound(err)
	}
	if dataset.Status != "draft" {
		return fmt.Errorf("add evaluation dataset case: dataset is %s", dataset.Status)
	}
	members, err := s.queries.ListEvaluationDatasetCases(ctx, datasetID)
	if err != nil {
		return err
	}
	return s.queries.AddEvaluationDatasetCase(ctx, db.AddEvaluationDatasetCaseParams{
		DatasetID: datasetID, CaseID: caseID, Split: split, Ordinal: int64(len(members)),
	})
}

func (s *Store) SealEvaluationDataset(ctx context.Context, datasetID string) (*EvaluationDataset, error) {
	dataset, err := s.GetEvaluationDataset(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	if dataset.Status == "sealed" {
		return dataset, nil
	}
	if len(dataset.Cases) == 0 {
		return nil, fmt.Errorf("seal evaluation dataset: at least one case is required")
	}
	identity := make([]map[string]any, 0, len(dataset.Cases))
	for _, member := range dataset.Cases {
		identity = append(identity, map[string]any{
			"case_identity_hash": member.CaseIdentityHash, "split": member.Split, "ordinal": member.Ordinal,
		})
	}
	_, identityHash, err := execution.CanonicalRedacted(identity)
	if err != nil {
		return nil, err
	}
	timestamp := now()
	affected, err := s.queries.SealEvaluationDataset(ctx, db.SealEvaluationDatasetParams{
		IdentityHash: identityHash, SealedAt: &timestamp, ID: datasetID,
	})
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("seal evaluation dataset: affected=%d: %w", affected, err)
	}
	return s.GetEvaluationDataset(ctx, datasetID)
}

func (s *Store) GetEvaluationDataset(ctx context.Context, id string) (*EvaluationDataset, error) {
	row, err := s.queries.GetEvaluationDataset(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	result := &EvaluationDataset{
		ID: row.ID, Name: row.Name, Description: row.Description, IdentityHash: row.IdentityHash,
		Status: row.Status, CreatedAt: parseTime(row.CreatedAt), SealedAt: parseOptionalTime(row.SealedAt),
	}
	members, err := s.queries.ListEvaluationDatasetCases(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, member := range members {
		result.Cases = append(result.Cases, EvaluationDatasetMember{
			CaseID: member.CaseID, CaseIdentityHash: member.IdentityHash, Split: member.Split,
			Ordinal: int(member.Ordinal), Provenance: member.Provenance, ProjectName: member.ProjectName,
			GoalID: member.GoalID, CapabilityID: member.CapabilityID, ResourceID: member.ResourceID,
			ContextManifestID: stringVal(member.ContextManifestID), ExecutionID: stringVal(member.ExecutionID),
			CandidateID: stringVal(member.CandidateID),
		})
	}
	return result, nil
}

func (s *Store) ListEvaluationDatasets(ctx context.Context, limit int) ([]EvaluationDataset, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListEvaluationDatasets(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	result := make([]EvaluationDataset, 0, len(rows))
	for _, row := range rows {
		dataset, getErr := s.GetEvaluationDataset(ctx, row.ID)
		if getErr != nil {
			return nil, getErr
		}
		result = append(result, *dataset)
	}
	return result, nil
}

func (s *Store) CreateEvaluationConfiguration(ctx context.Context, input EvaluationConfiguration) (*EvaluationConfiguration, error) {
	if input.Name == "" || input.PlannerVersion == "" || input.ContextSelectorVersion == "" || input.ContextBudgetTokens <= 0 ||
		input.RolePolicyVersion == "" || input.HostName == "" || input.Model == "" || input.ValidationVersion == "" {
		return nil, fmt.Errorf("create evaluation configuration: name, versions, budget, host, model, and validation version are required")
	}
	input.LearningIDs = sortedUnique(input.LearningIDs)
	payload := map[string]any{
		"planner_version": input.PlannerVersion, "planner_policy": input.PlannerPolicy,
		"context_selector_version": input.ContextSelectorVersion, "context_budget_tokens": input.ContextBudgetTokens,
		"template_hashes": input.TemplateHashes, "role_policy_version": input.RolePolicyVersion,
		"host_name": input.HostName, "host_version": input.HostVersion, "provider": input.Provider,
		"model": input.Model, "inference_config": input.InferenceConfig,
		"validation_version": input.ValidationVersion, "learning_ids": input.LearningIDs,
	}
	payloadJSON, identityHash, err := execution.CanonicalRedacted(payload)
	if err != nil {
		return nil, err
	}
	if row, getErr := s.queries.GetEvaluationConfigurationByIdentity(ctx, identityHash); getErr == nil {
		return s.hydrateEvaluationConfiguration(ctx, row)
	} else if !errors.Is(getErr, sql.ErrNoRows) {
		return nil, getErr
	}
	plannerJSON, plannerHash, err := execution.CanonicalRedacted(input.PlannerPolicy)
	if err != nil {
		return nil, err
	}
	_ = plannerJSON
	templateJSON, _, err := execution.CanonicalRedacted(input.TemplateHashes)
	if err != nil {
		return nil, err
	}
	inferenceJSON, _, err := execution.CanonicalRedacted(input.InferenceConfig)
	if err != nil {
		return nil, err
	}
	learningJSON, _ := json.Marshal(input.LearningIDs)
	timestamp := now()
	if err := s.putEvaluationBlob(ctx, identityHash, payloadJSON, timestamp); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	if err := s.queries.PutEvaluationConfiguration(ctx, db.PutEvaluationConfigurationParams{
		ID: id, Name: input.Name, IdentityHash: identityHash, PlannerVersion: input.PlannerVersion,
		PlannerPolicyHash: plannerHash, ContextSelectorVersion: input.ContextSelectorVersion,
		ContextBudgetTokens: int64(input.ContextBudgetTokens), TemplateHashesJson: templateJSON,
		RolePolicyVersion: input.RolePolicyVersion, HostName: input.HostName, HostVersion: input.HostVersion,
		Provider: input.Provider, Model: input.Model, InferenceConfigJson: inferenceJSON,
		ValidationVersion: input.ValidationVersion, LearningIdsJson: string(learningJSON),
		PayloadBlob: identityHash, CreatedAt: timestamp,
	}); err != nil {
		return nil, err
	}
	return s.GetEvaluationConfiguration(ctx, id)
}

func (s *Store) GetEvaluationConfiguration(ctx context.Context, id string) (*EvaluationConfiguration, error) {
	row, err := s.queries.GetEvaluationConfiguration(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateEvaluationConfiguration(ctx, row)
}

func (s *Store) ListEvaluationConfigurations(ctx context.Context, limit int) ([]EvaluationConfiguration, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListEvaluationConfigurations(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	result := make([]EvaluationConfiguration, 0, len(rows))
	for _, row := range rows {
		configuration, hydrateErr := s.hydrateEvaluationConfiguration(ctx, row)
		if hydrateErr != nil {
			return nil, hydrateErr
		}
		result = append(result, *configuration)
	}
	return result, nil
}

func (s *Store) hydrateEvaluationConfiguration(ctx context.Context, row db.EvaluationConfiguration) (*EvaluationConfiguration, error) {
	payload, err := s.queries.GetContentBlob(ctx, row.PayloadBlob)
	if err != nil {
		return nil, err
	}
	var canonical map[string]any
	if err := json.Unmarshal(payload.Content, &canonical); err != nil {
		return nil, err
	}
	result := &EvaluationConfiguration{
		ID: row.ID, Name: row.Name, IdentityHash: row.IdentityHash, PlannerVersion: row.PlannerVersion,
		PlannerPolicyHash: row.PlannerPolicyHash, ContextSelectorVersion: row.ContextSelectorVersion,
		ContextBudgetTokens: int(row.ContextBudgetTokens), RolePolicyVersion: row.RolePolicyVersion,
		HostName: row.HostName, HostVersion: row.HostVersion, Provider: row.Provider, Model: row.Model,
		ValidationVersion: row.ValidationVersion, PayloadHash: row.PayloadBlob, CreatedAt: parseTime(row.CreatedAt),
	}
	result.PlannerPolicy, _ = canonical["planner_policy"].(map[string]any)
	result.InferenceConfig, _ = canonical["inference_config"].(map[string]any)
	_ = json.Unmarshal([]byte(row.TemplateHashesJson), &result.TemplateHashes)
	_ = json.Unmarshal([]byte(row.LearningIdsJson), &result.LearningIDs)
	return result, nil
}

func (s *Store) putEvaluationBlob(ctx context.Context, hash, content, timestamp string) error {
	return s.queries.PutContentBlob(ctx, db.PutContentBlobParams{
		Hash: hash, Content: []byte(content), ByteSize: int64(len([]byte(content))), CreatedAt: timestamp,
	})
}

func evaluationTerminalExecution(status string) bool {
	switch status {
	case "accepted", "rejected", "completed", "cancelled", "failed", "timed_out":
		return true
	default:
		return false
	}
}

func sortedUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
