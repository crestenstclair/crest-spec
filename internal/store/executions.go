package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/db"
	"github.com/crestenstclair/crest-spec/internal/execution"
)

// ExecutionManifest records the external host configuration that interpreted
// one immutable context manifest. Sensitive structured values are redacted
// before persistence and the redacted representation is content-hashed.
type ExecutionManifest struct {
	ID                     string
	AttemptID              string
	SessionID              string
	ResourceID             string
	RetryNumber            int
	ContextManifestID      string
	ProtocolVersion        string
	IdempotencyKey         string
	PlanOperationID        string
	Role                   string
	RolePolicyVersion      string
	ContextPolicy          string
	HostName               string
	HostVersion            string
	Provider               string
	Model                  string
	InferenceConfig        map[string]any
	InferenceConfigJSON    string
	InferenceConfigHash    string
	AgentConfig            map[string]any
	AgentConfigJSON        string
	AgentConfigHash        string
	ToolPermissionsJSON    string
	ToolPermissionsHash    string
	TemplateHashes         map[string]string
	TemplateHashesJSON     string
	TemplateHashesHash     string
	SystemInstructions     string
	SystemInstructionsHash string
	ContextHash            string
	HostSessionID          string
	HostCommitRef          string
	GoalIDs                []string
	CapabilityIDs          []string
	GoalProgress           map[string]any
	GoalProgressJSON       string
	GoalProgressHash       string
	Status                 string
	DispositionReason      string
	StartedAt              time.Time
	CompletedAt            *time.Time
	DurationMS             int64
	InputTokens            int64
	OutputTokens           int64
	CostUSD                float64
	CreatedAt              time.Time
	UpdatedAt              time.Time
	Tools                  []ExecutionTool
	Events                 []ExecutionEvent
}

type ExecutionTool struct {
	Name       string `json:"name"`
	Permission string `json:"permission"`
	Ordinal    int    `json:"ordinal"`
}

type ExecutionEvent struct {
	ID          string
	FromStatus  string
	ToStatus    string
	Kind        string
	DetailsJSON string
	DetailsHash string
	CreatedAt   time.Time
}

type ExecutionTransition struct {
	ExecutionID       string
	ToStatus          execution.Status
	Reason            string
	Details           map[string]any
	DurationMS        int64
	InputTokens       int64
	OutputTokens      int64
	CostUSD           float64
	Failure           *FailureClassificationWrite
	HandoffTargetRole string
	HostCommitRef     string
	GoalProgress      map[string]any
}

type CandidateDisposition struct {
	ExecutionID   string
	Accepted      bool
	Reason        string
	Details       map[string]any
	Resource      *Resource
	Files         []GeneratedFile
	Dependencies  []Dependency
	Notes         string
	Attempts      int
	DurationMS    int64
	InputTokens   int64
	OutputTokens  int64
	CostUSD       float64
	Failure       *FailureClassificationWrite
	HandoffRole   string
	HostCommitRef string
	GoalProgress  map[string]any
}

type CandidateSet struct {
	ID                string
	ExecutionID       string
	AttemptID         string
	CandidateHash     string
	Status            string
	DispositionReason string
	CreatedAt         time.Time
	DisposedAt        *time.Time
	Files             []CandidateFile
}

type CandidateFile struct {
	ID          string
	Path        string
	Content     string
	ContentHash string
	ByteSize    int
	WriteIntent string
	Ordinal     int
}

type FailureClassification struct {
	ID                string
	AttemptID         string
	ExecutionID       string
	ResourceID        string
	Category          string
	Origin            string
	Confidence        float64
	EvidenceSource    string
	EvidenceReference string
	Evidence          string
	EvidenceHash      string
	CorrectiveAction  string
	NextRole          string
	BlocksRetry       bool
	OverrideOf        string
	Resolution        string
	ResolvedByAttempt string
	GoalIDs           []string
	CreatedAt         time.Time
	ResolvedAt        *time.Time
}

type FailureClassificationWrite struct {
	ID                string
	AttemptID         string
	ExecutionID       string
	Category          execution.FailureCategory
	Origin            string
	Confidence        float64
	EvidenceSource    string
	EvidenceReference string
	Evidence          string
	OverrideOf        string
	GoalIDs           []string
}

type AttemptHandoff struct {
	ID              string
	SourceAttemptID string
	TargetAttemptID string
	SourceRole      string
	TargetRole      string
	Reason          string
	ExpectedOutcome string
	FailureID       string
	Status          string
	CreatedAt       time.Time
	AcceptedAt      *time.Time
}

// StartExecution atomically validates an attempt/context pair and persists the
// immutable, redacted host manifest. Replaying an identical idempotency key is
// safe; changing any governed input under that key is rejected.
func (s *Store) StartExecution(ctx context.Context, manifest ExecutionManifest) (*ExecutionManifest, error) {
	prepared, err := prepareExecutionManifest(manifest)
	if err != nil {
		return nil, err
	}
	manifest = prepared

	if existing, getErr := s.queries.GetExecutionManifestByIdempotencyKey(ctx, manifest.IdempotencyKey); getErr == nil {
		if err := sameExecutionRequest(existing, manifest); err != nil {
			return nil, err
		}
		hydrated, hydrateErr := s.GetExecutionManifest(ctx, existing.ID)
		if hydrateErr != nil {
			return nil, hydrateErr
		}
		if !equalStrings(hydrated.GoalIDs, manifest.GoalIDs) || !equalStrings(hydrated.CapabilityIDs, manifest.CapabilityIDs) {
			return nil, fmt.Errorf("start execution: idempotency key %q was already used for different goal impact", manifest.IdempotencyKey)
		}
		return hydrated, nil
	} else if !errors.Is(getErr, sql.ErrNoRows) {
		return nil, fmt.Errorf("start execution: inspect idempotency key: %w", getErr)
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("start execution: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)

	attempt, err := q.GetGenerationAttempt(ctx, manifest.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("start execution: attempt: %w", mapNotFound(err))
	}
	contextRow, err := q.GetContextManifest(ctx, manifest.ContextManifestID)
	if err != nil {
		return nil, fmt.Errorf("start execution: context manifest: %w", mapNotFound(err))
	}
	if contextRow.AttemptID != attempt.ID || contextRow.Blocked == 1 {
		return nil, fmt.Errorf("start execution: context manifest must be an unblocked manifest for attempt %s", attempt.ID)
	}
	if attempt.Status != "context_prepared" {
		return nil, fmt.Errorf("start execution: attempt %s has status %s", attempt.ID, attempt.Status)
	}
	if contextRow.ContextHash != manifest.ContextHash {
		return nil, fmt.Errorf("start execution: context hash mismatch")
	}
	var selectedTemplateHashes map[string]string
	if err := json.Unmarshal([]byte(contextRow.TemplateHashesJson), &selectedTemplateHashes); err != nil {
		return nil, fmt.Errorf("start execution: decode context template hashes: %w", err)
	}
	_, selectedTemplateHash, err := execution.CanonicalRedacted(selectedTemplateHashes)
	if err != nil {
		return nil, fmt.Errorf("start execution: hash context templates: %w", err)
	}
	if selectedTemplateHash != manifest.TemplateHashesHash {
		return nil, fmt.Errorf("start execution: template hashes do not match prepared context")
	}
	if attempt.Role != manifest.Role || attempt.RolePolicyVersion != manifest.RolePolicyVersion {
		return nil, fmt.Errorf("start execution: role or role policy does not match prepared attempt")
	}
	if attempt.PlanOperationID != manifest.PlanOperationID {
		return nil, fmt.Errorf("start execution: plan operation does not match prepared attempt")
	}

	timestamp := now()
	if err := q.PutContentBlob(ctx, db.PutContentBlobParams{
		Hash: manifest.SystemInstructionsHash, Content: []byte(manifest.SystemInstructions),
		ByteSize: int64(len([]byte(manifest.SystemInstructions))), CreatedAt: timestamp,
	}); err != nil {
		return nil, fmt.Errorf("start execution: store system instructions: %w", err)
	}
	if manifest.ID == "" {
		manifest.ID = uuid.NewString()
	}
	manifest.Status = string(execution.StatusExecuting)
	manifest.StartedAt = parseTime(timestamp)
	manifest.CreatedAt = manifest.StartedAt
	manifest.UpdatedAt = manifest.StartedAt
	if err := q.CreateExecutionManifest(ctx, executionManifestParams(manifest, timestamp)); err != nil {
		return nil, fmt.Errorf("start execution: insert manifest: %w", err)
	}
	for ordinal, tool := range manifest.Tools {
		if err := q.CreateExecutionTool(ctx, db.CreateExecutionToolParams{
			ExecutionID: manifest.ID, Name: tool.Name, Permission: tool.Permission, Ordinal: int64(ordinal),
		}); err != nil {
			return nil, fmt.Errorf("start execution: insert tool %q: %w", tool.Name, err)
		}
	}
	for _, goalID := range manifest.GoalIDs {
		if err := q.AddExecutionGoal(ctx, db.AddExecutionGoalParams{ExecutionID: manifest.ID, GoalID: goalID}); err != nil {
			return nil, fmt.Errorf("start execution: link goal %s: %w", goalID, err)
		}
	}
	for _, capabilityID := range manifest.CapabilityIDs {
		if err := q.AddExecutionCapability(ctx, db.AddExecutionCapabilityParams{ExecutionID: manifest.ID, CapabilityID: capabilityID}); err != nil {
			return nil, fmt.Errorf("start execution: link capability %s: %w", capabilityID, err)
		}
	}
	if err := createExecutionEvent(ctx, q, manifest.ID, "", manifest.Status, "execution_started", map[string]any{
		"attempt_id": manifest.AttemptID, "role": manifest.Role, "protocol_version": manifest.ProtocolVersion,
	}, timestamp); err != nil {
		return nil, fmt.Errorf("start execution: event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("start execution: commit: %w", err)
	}
	return s.GetExecutionManifest(ctx, manifest.ID)
}

func prepareExecutionManifest(manifest ExecutionManifest) (ExecutionManifest, error) {
	if manifest.AttemptID == "" || manifest.ContextManifestID == "" || manifest.IdempotencyKey == "" || manifest.PlanOperationID == "" {
		return manifest, fmt.Errorf("start execution: attempt, context manifest, idempotency key, and plan operation are required")
	}
	if manifest.ProtocolVersion != execution.ProtocolVersion {
		return manifest, fmt.Errorf("start execution: unsupported protocol version %q", manifest.ProtocolVersion)
	}
	policy, err := execution.LookupRole(manifest.Role)
	if err != nil {
		return manifest, fmt.Errorf("start execution: %w", err)
	}
	if !policy.Allows(execution.ActionStartExecution) || manifest.RolePolicyVersion != policy.Version || manifest.ContextPolicy != policy.ContextPolicy {
		return manifest, fmt.Errorf("start execution: role policy does not match %s", policy.Role)
	}
	if manifest.HostName == "" || manifest.Model == "" || manifest.ContextHash == "" {
		return manifest, fmt.Errorf("start execution: host, model, and context hash are required")
	}
	manifest.InferenceConfigJSON, manifest.InferenceConfigHash, err = execution.CanonicalRedacted(nonNilMap(manifest.InferenceConfig))
	if err != nil {
		return manifest, fmt.Errorf("start execution: inference configuration: %w", err)
	}
	manifest.AgentConfigJSON, manifest.AgentConfigHash, err = execution.CanonicalRedacted(nonNilMap(manifest.AgentConfig))
	if err != nil {
		return manifest, fmt.Errorf("start execution: agent configuration: %w", err)
	}
	sort.Slice(manifest.Tools, func(i, j int) bool { return manifest.Tools[i].Name < manifest.Tools[j].Name })
	permissions := make(map[string]string, len(manifest.Tools))
	for index := range manifest.Tools {
		tool := &manifest.Tools[index]
		if tool.Name == "" || tool.Permission == "" {
			return manifest, fmt.Errorf("start execution: tool name and permission are required")
		}
		if _, duplicate := permissions[tool.Name]; duplicate {
			return manifest, fmt.Errorf("start execution: duplicate tool %q", tool.Name)
		}
		tool.Ordinal = index
		permissions[tool.Name] = tool.Permission
	}
	manifest.ToolPermissionsJSON, manifest.ToolPermissionsHash, err = execution.CanonicalRedacted(permissions)
	if err != nil {
		return manifest, fmt.Errorf("start execution: tool permissions: %w", err)
	}
	manifest.TemplateHashesJSON, manifest.TemplateHashesHash, err = execution.CanonicalRedacted(nonNilStringMap(manifest.TemplateHashes))
	if err != nil {
		return manifest, fmt.Errorf("start execution: template hashes: %w", err)
	}
	manifest.SystemInstructions = execution.RedactText(manifest.SystemInstructions)
	manifest.SystemInstructionsHash = contextmanifest.Hash(manifest.SystemInstructions)
	sort.Strings(manifest.GoalIDs)
	manifest.GoalIDs = dedupeStoreStrings(manifest.GoalIDs)
	sort.Strings(manifest.CapabilityIDs)
	manifest.CapabilityIDs = dedupeStoreStrings(manifest.CapabilityIDs)
	manifest.GoalProgressJSON, manifest.GoalProgressHash, err = execution.CanonicalRedacted(map[string]any{})
	if err != nil {
		return manifest, fmt.Errorf("start execution: initial goal progress: %w", err)
	}
	manifest.GoalProgress = map[string]any{}
	return manifest, nil
}

func executionManifestParams(manifest ExecutionManifest, timestamp string) db.CreateExecutionManifestParams {
	return db.CreateExecutionManifestParams{
		ID: manifest.ID, AttemptID: manifest.AttemptID, ContextManifestID: manifest.ContextManifestID,
		ProtocolVersion: manifest.ProtocolVersion, IdempotencyKey: manifest.IdempotencyKey,
		PlanOperationID: manifest.PlanOperationID, Role: manifest.Role,
		RolePolicyVersion: manifest.RolePolicyVersion, ContextPolicy: manifest.ContextPolicy,
		HostName: manifest.HostName, HostVersion: manifest.HostVersion, Provider: manifest.Provider, Model: manifest.Model,
		InferenceConfigJson: manifest.InferenceConfigJSON, InferenceConfigHash: manifest.InferenceConfigHash,
		AgentConfigJson: manifest.AgentConfigJSON, AgentConfigHash: manifest.AgentConfigHash,
		ToolPermissionsJson: manifest.ToolPermissionsJSON, ToolPermissionsHash: manifest.ToolPermissionsHash,
		TemplateHashesJson: manifest.TemplateHashesJSON, TemplateHashesHash: manifest.TemplateHashesHash,
		SystemInstructionsBlob: manifest.SystemInstructionsHash, ContextHash: manifest.ContextHash,
		HostSessionID: manifest.HostSessionID, Status: string(execution.StatusExecuting),
		DispositionReason: "", StartedAt: timestamp, DurationMs: 0, InputTokens: 0, OutputTokens: 0,
		CostUsd: 0, CreatedAt: timestamp, UpdatedAt: timestamp,
		HostCommitRef: "", GoalProgressJson: manifest.GoalProgressJSON, GoalProgressHash: manifest.GoalProgressHash,
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameExecutionRequest(row db.ExecutionManifest, manifest ExecutionManifest) error {
	if row.AttemptID != manifest.AttemptID || row.ContextManifestID != manifest.ContextManifestID ||
		row.ProtocolVersion != manifest.ProtocolVersion || row.PlanOperationID != manifest.PlanOperationID ||
		row.Role != manifest.Role || row.RolePolicyVersion != manifest.RolePolicyVersion ||
		row.ContextPolicy != manifest.ContextPolicy || row.HostName != manifest.HostName ||
		row.HostVersion != manifest.HostVersion || row.Provider != manifest.Provider || row.Model != manifest.Model ||
		row.InferenceConfigHash != manifest.InferenceConfigHash || row.AgentConfigHash != manifest.AgentConfigHash ||
		row.ToolPermissionsHash != manifest.ToolPermissionsHash || row.TemplateHashesHash != manifest.TemplateHashesHash ||
		row.SystemInstructionsBlob != manifest.SystemInstructionsHash || row.ContextHash != manifest.ContextHash ||
		row.HostSessionID != manifest.HostSessionID {
		return fmt.Errorf("start execution: idempotency key %q was already used for different governed inputs", manifest.IdempotencyKey)
	}
	return nil
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func nonNilStringMap(value map[string]string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	return value
}

// SubmitCandidate snapshots candidate bytes before the mutable project tree is
// touched. The exact rejected implementation therefore remains available to a
// retry without being confused with accepted generated-file ownership.
func (s *Store) SubmitCandidate(ctx context.Context, executionID string, files []CandidateFile) (*CandidateSet, error) {
	manifestRow, err := s.queries.GetExecutionManifest(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("submit candidate: execution: %w", mapNotFound(err))
	}
	policy, err := execution.LookupRole(manifestRow.Role)
	if err != nil || !policy.Allows(execution.ActionSubmitCandidate) {
		return nil, fmt.Errorf("submit candidate: role %s cannot submit candidate files", manifestRow.Role)
	}
	if manifestRow.Status != string(execution.StatusExecuting) {
		if existing, getErr := s.queries.GetCandidateSetByExecution(ctx, executionID); getErr == nil {
			candidate, hydrateErr := s.hydrateCandidateSet(ctx, existing)
			if hydrateErr != nil {
				return nil, hydrateErr
			}
			prepared, prepareErr := prepareCandidate(manifestRow.AttemptID, executionID, files)
			if prepareErr == nil && prepared.CandidateHash == candidate.CandidateHash {
				return candidate, nil
			}
		}
		return nil, fmt.Errorf("submit candidate: execution %s has status %s", executionID, manifestRow.Status)
	}
	candidate, err := prepareCandidate(manifestRow.AttemptID, executionID, files)
	if err != nil {
		return nil, err
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("submit candidate: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	timestamp := now()
	candidate.ID = uuid.NewString()
	candidate.Status = "submitted"
	candidate.CreatedAt = parseTime(timestamp)
	if err := q.CreateCandidateSet(ctx, db.CreateCandidateSetParams{
		ID: candidate.ID, ExecutionID: executionID, AttemptID: candidate.AttemptID,
		CandidateHash: candidate.CandidateHash, Status: candidate.Status,
		DispositionReason: "", CreatedAt: timestamp,
	}); err != nil {
		return nil, fmt.Errorf("submit candidate: insert set: %w", err)
	}
	for index := range candidate.Files {
		file := &candidate.Files[index]
		file.ID = uuid.NewString()
		if err := q.PutContentBlob(ctx, db.PutContentBlobParams{
			Hash: file.ContentHash, Content: []byte(file.Content), ByteSize: int64(file.ByteSize), CreatedAt: timestamp,
		}); err != nil {
			return nil, fmt.Errorf("submit candidate: store %s: %w", file.Path, err)
		}
		if err := q.CreateCandidateFile(ctx, db.CreateCandidateFileParams{
			ID: file.ID, CandidateID: candidate.ID, Path: file.Path, ContentBlob: file.ContentHash,
			ContentHash: file.ContentHash, ByteSize: int64(file.ByteSize), WriteIntent: file.WriteIntent, Ordinal: int64(index),
		}); err != nil {
			return nil, fmt.Errorf("submit candidate: insert %s: %w", file.Path, err)
		}
	}
	if affected, updateErr := q.UpdateExecutionManifestStatus(ctx, db.UpdateExecutionManifestStatusParams{
		Status: string(execution.StatusCandidateSubmitted), DispositionReason: "", DurationMs: 0,
		InputTokens: 0, OutputTokens: 0, CostUsd: 0, UpdatedAt: timestamp,
		HostCommitRef: manifestRow.HostCommitRef, GoalProgressJson: manifestRow.GoalProgressJson,
		GoalProgressHash: manifestRow.GoalProgressHash,
		ID:               executionID, Status_2: string(execution.StatusExecuting),
	}); updateErr != nil || affected != 1 {
		return nil, fmt.Errorf("submit candidate: transition execution: affected=%d: %w", affected, updateErr)
	}
	if affected, updateErr := q.UpdateGenerationAttemptStatus(ctx, db.UpdateGenerationAttemptStatusParams{
		Status: "candidate_submitted", ID: candidate.AttemptID, Status_2: "context_prepared",
	}); updateErr != nil || affected != 1 {
		return nil, fmt.Errorf("submit candidate: transition attempt: affected=%d: %w", affected, updateErr)
	}
	if err := createExecutionEvent(ctx, q, executionID, string(execution.StatusExecuting), string(execution.StatusCandidateSubmitted), "candidate_submitted", map[string]any{
		"candidate_id": candidate.ID, "candidate_hash": candidate.CandidateHash, "file_count": len(candidate.Files),
	}, timestamp); err != nil {
		return nil, fmt.Errorf("submit candidate: event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("submit candidate: commit: %w", err)
	}
	return &candidate, nil
}

func prepareCandidate(attemptID, executionID string, files []CandidateFile) (CandidateSet, error) {
	prepared := append([]CandidateFile(nil), files...)
	sort.Slice(prepared, func(i, j int) bool { return prepared[i].Path < prepared[j].Path })
	identity := make([]map[string]any, 0, len(prepared))
	seen := make(map[string]bool, len(prepared))
	for index := range prepared {
		file := &prepared[index]
		clean := filepath.ToSlash(filepath.Clean(file.Path))
		if file.Path == "" || filepath.IsAbs(file.Path) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return CandidateSet{}, fmt.Errorf("submit candidate: path %q must be project-relative", file.Path)
		}
		if seen[clean] {
			return CandidateSet{}, fmt.Errorf("submit candidate: duplicate path %q", clean)
		}
		seen[clean] = true
		file.Path = clean
		if file.WriteIntent == "" {
			file.WriteIntent = "modify"
		}
		if file.WriteIntent != "create" && file.WriteIntent != "modify" && file.WriteIntent != "preserve" {
			return CandidateSet{}, fmt.Errorf("submit candidate: invalid write intent %q", file.WriteIntent)
		}
		file.ContentHash = contextmanifest.Hash(file.Content)
		file.ByteSize = len([]byte(file.Content))
		file.Ordinal = index
		identity = append(identity, map[string]any{"path": clean, "content_hash": file.ContentHash, "write_intent": file.WriteIntent})
	}
	_, candidateHash, err := execution.CanonicalRedacted(identity)
	if err != nil {
		return CandidateSet{}, fmt.Errorf("submit candidate: hash: %w", err)
	}
	return CandidateSet{ExecutionID: executionID, AttemptID: attemptID, CandidateHash: candidateHash, Files: prepared}, nil
}

// TransitionExecution applies one legal state transition and records the
// event, candidate disposition, attempt disposition, optional failure, and
// role handoff in one transaction.
func (s *Store) TransitionExecution(ctx context.Context, input ExecutionTransition) (*ExecutionManifest, error) {
	row, err := s.queries.GetExecutionManifest(ctx, input.ExecutionID)
	if err != nil {
		return nil, fmt.Errorf("transition execution: manifest: %w", mapNotFound(err))
	}
	from := execution.Status(row.Status)
	if err := execution.ValidateTransition(from, input.ToStatus); err != nil {
		return nil, fmt.Errorf("transition execution: %w", err)
	}
	if input.DurationMS < 0 || input.InputTokens < 0 || input.OutputTokens < 0 || input.CostUSD < 0 {
		return nil, fmt.Errorf("transition execution: metrics cannot be negative")
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("transition execution: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	timestamp := now()
	var completedAt *string
	if input.ToStatus.Terminal() {
		completedAt = &timestamp
	}
	hostCommitRef := row.HostCommitRef
	if input.HostCommitRef != "" {
		hostCommitRef = input.HostCommitRef
	}
	progressJSON, progressHash := row.GoalProgressJson, row.GoalProgressHash
	if input.GoalProgress != nil {
		progressJSON, progressHash, err = execution.CanonicalRedacted(input.GoalProgress)
		if err != nil {
			return nil, fmt.Errorf("transition execution: goal progress: %w", err)
		}
	}
	affected, err := q.UpdateExecutionManifestStatus(ctx, db.UpdateExecutionManifestStatusParams{
		Status: string(input.ToStatus), DispositionReason: input.Reason, CompletedAt: completedAt,
		DurationMs: input.DurationMS, InputTokens: input.InputTokens, OutputTokens: input.OutputTokens,
		CostUsd: input.CostUSD, UpdatedAt: timestamp, HostCommitRef: hostCommitRef,
		GoalProgressJson: progressJSON, GoalProgressHash: progressHash,
		ID: input.ExecutionID, Status_2: row.Status,
	})
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("transition execution: concurrent transition: affected=%d: %w", affected, err)
	}

	if input.ToStatus == execution.StatusAccepted || input.ToStatus == execution.StatusRejected {
		candidate, getErr := q.GetCandidateSetByExecution(ctx, input.ExecutionID)
		if getErr != nil {
			return nil, fmt.Errorf("transition execution: terminal candidate disposition requires a candidate: %w", mapNotFound(getErr))
		}
		disposition := "accepted"
		attemptStatus := "accepted"
		if input.ToStatus == execution.StatusRejected {
			disposition = "rejected"
			attemptStatus = "rejected"
		}
		if changed, updateErr := q.UpdateCandidateSetDisposition(ctx, db.UpdateCandidateSetDispositionParams{
			Status: disposition, DispositionReason: input.Reason, DisposedAt: &timestamp, ID: candidate.ID,
		}); updateErr != nil || changed != 1 {
			return nil, fmt.Errorf("transition execution: candidate disposition: affected=%d: %w", changed, updateErr)
		}
		if changed, updateErr := q.UpdateGenerationAttemptStatus(ctx, db.UpdateGenerationAttemptStatusParams{
			Status: attemptStatus, ID: row.AttemptID, Status_2: "candidate_submitted",
		}); updateErr != nil || changed != 1 {
			return nil, fmt.Errorf("transition execution: attempt disposition: affected=%d: %w", changed, updateErr)
		}
	}
	if input.ToStatus == execution.StatusCompleted {
		if changed, updateErr := q.UpdateGenerationAttemptStatus(ctx, db.UpdateGenerationAttemptStatusParams{
			Status: "accepted", ID: row.AttemptID, Status_2: "context_prepared",
		}); updateErr != nil || changed != 1 {
			return nil, fmt.Errorf("transition execution: advisory attempt disposition: affected=%d: %w", changed, updateErr)
		}
	}
	if input.ToStatus == execution.StatusCancelled || input.ToStatus == execution.StatusFailed || input.ToStatus == execution.StatusTimedOut {
		expectedAttemptStatus := "context_prepared"
		if from == execution.StatusCandidateSubmitted || from == execution.StatusValidating {
			expectedAttemptStatus = "candidate_submitted"
			if candidate, getErr := q.GetCandidateSetByExecution(ctx, input.ExecutionID); getErr == nil {
				_, _ = q.UpdateCandidateSetDisposition(ctx, db.UpdateCandidateSetDispositionParams{
					Status: "rejected", DispositionReason: input.Reason, DisposedAt: &timestamp, ID: candidate.ID,
				})
			}
		}
		if changed, updateErr := q.UpdateGenerationAttemptStatus(ctx, db.UpdateGenerationAttemptStatusParams{
			Status: "abandoned", ID: row.AttemptID, Status_2: expectedAttemptStatus,
		}); updateErr != nil || changed != 1 {
			return nil, fmt.Errorf("transition execution: abandon attempt: affected=%d: %w", changed, updateErr)
		}
	}

	var failure *FailureClassification
	if input.Failure != nil {
		write := *input.Failure
		write.AttemptID = row.AttemptID
		write.ExecutionID = row.ID
		failure, err = createFailureClassificationTx(ctx, q, write, timestamp)
		if err != nil {
			return nil, fmt.Errorf("transition execution: classify failure: %w", err)
		}
	}
	if input.HandoffTargetRole != "" {
		failureID := ""
		if failure != nil {
			failureID = failure.ID
		}
		if _, err := createAttemptHandoffTx(ctx, q, AttemptHandoff{
			SourceAttemptID: row.AttemptID, SourceRole: row.Role, TargetRole: input.HandoffTargetRole,
			Reason: input.Reason, ExpectedOutcome: "resolve " + input.Reason, FailureID: failureID,
		}, timestamp); err != nil {
			return nil, fmt.Errorf("transition execution: handoff: %w", err)
		}
	}
	eventKind := "execution_" + string(input.ToStatus)
	if err := createExecutionEvent(ctx, q, input.ExecutionID, row.Status, string(input.ToStatus), eventKind, input.Details, timestamp); err != nil {
		return nil, fmt.Errorf("transition execution: event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("transition execution: commit: %w", err)
	}
	return s.GetExecutionManifest(ctx, input.ExecutionID)
}

// FinalizeCandidate commits the complete governance disposition as one SQLite
// transaction. For acceptance that includes resource/file ownership and
// dependency state; for rejection it includes failure routing and handoff.
func (s *Store) FinalizeCandidate(ctx context.Context, input CandidateDisposition) (*ExecutionManifest, error) {
	if input.DurationMS < 0 || input.InputTokens < 0 || input.OutputTokens < 0 || input.CostUSD < 0 {
		return nil, fmt.Errorf("finalize candidate: metrics cannot be negative")
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("finalize candidate: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	manifest, err := q.GetExecutionManifest(ctx, input.ExecutionID)
	if err != nil {
		return nil, fmt.Errorf("finalize candidate: execution: %w", mapNotFound(err))
	}
	if manifest.Status != string(execution.StatusValidating) {
		return nil, fmt.Errorf("finalize candidate: execution %s must be validating, got %s", manifest.ID, manifest.Status)
	}
	attempt, err := q.GetGenerationAttempt(ctx, manifest.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("finalize candidate: attempt: %w", err)
	}
	candidate, err := q.GetCandidateSetByExecution(ctx, manifest.ID)
	if err != nil {
		return nil, fmt.Errorf("finalize candidate: candidate: %w", mapNotFound(err))
	}
	candidateFiles, err := q.ListCandidateFiles(ctx, candidate.ID)
	if err != nil {
		return nil, fmt.Errorf("finalize candidate: files: %w", err)
	}
	timestamp := now()
	toStatus := execution.StatusRejected
	disposition := "rejected"
	attemptStatus := "rejected"
	sessionState := "rejected"
	actionOutcome := "rejected"
	if input.Accepted {
		toStatus = execution.StatusAccepted
		disposition = "accepted"
		attemptStatus = "accepted"
		sessionState = "committed"
		actionOutcome = "committed"
		if input.Resource == nil || input.Resource.ID != attempt.ResourceID {
			return nil, fmt.Errorf("finalize candidate: accepted disposition requires the attempt resource")
		}
		if err := validateAcceptedFiles(candidateFiles, input.Files, attempt.ResourceID); err != nil {
			return nil, err
		}
	}
	completedAt := timestamp
	progressJSON, progressHash := manifest.GoalProgressJson, manifest.GoalProgressHash
	if input.GoalProgress != nil {
		progressJSON, progressHash, err = execution.CanonicalRedacted(input.GoalProgress)
		if err != nil {
			return nil, fmt.Errorf("finalize candidate: goal progress: %w", err)
		}
	}
	if changed, updateErr := q.UpdateExecutionManifestStatus(ctx, db.UpdateExecutionManifestStatusParams{
		Status: string(toStatus), DispositionReason: input.Reason, CompletedAt: &completedAt,
		DurationMs: input.DurationMS, InputTokens: input.InputTokens, OutputTokens: input.OutputTokens,
		CostUsd: input.CostUSD, UpdatedAt: timestamp, HostCommitRef: input.HostCommitRef,
		GoalProgressJson: progressJSON, GoalProgressHash: progressHash,
		ID: manifest.ID, Status_2: manifest.Status,
	}); updateErr != nil || changed != 1 {
		return nil, fmt.Errorf("finalize candidate: execution transition: affected=%d: %w", changed, updateErr)
	}
	if changed, updateErr := q.UpdateCandidateSetDisposition(ctx, db.UpdateCandidateSetDispositionParams{
		Status: disposition, DispositionReason: input.Reason, DisposedAt: &timestamp, ID: candidate.ID,
	}); updateErr != nil || changed != 1 {
		return nil, fmt.Errorf("finalize candidate: candidate disposition: affected=%d: %w", changed, updateErr)
	}
	if changed, updateErr := q.UpdateGenerationAttemptStatus(ctx, db.UpdateGenerationAttemptStatusParams{
		Status: attemptStatus, ID: attempt.ID, Status_2: "candidate_submitted",
	}); updateErr != nil || changed != 1 {
		return nil, fmt.Errorf("finalize candidate: attempt disposition: affected=%d: %w", changed, updateErr)
	}

	if input.Accepted {
		resource := *input.Resource
		if err := q.SetResource(ctx, db.SetResourceParams{
			ID: resource.ID, Kind: resource.Kind, ContextName: stringPtr(resource.ContextName),
			DeclarationHash: resource.DeclarationHash, EffectiveHash: resource.EffectiveHash,
			Model: stringPtr(resource.Model), SettledAt: timestamp,
		}); err != nil {
			return nil, fmt.Errorf("finalize candidate: resource: %w", err)
		}
		if len(input.Files) > 0 {
			if err := q.DeleteGeneratedFiles(ctx, attempt.ResourceID); err != nil {
				return nil, fmt.Errorf("finalize candidate: replace file ownership: %w", err)
			}
			for _, file := range input.Files {
				if err := q.SetGeneratedFile(ctx, db.SetGeneratedFileParams{
					Path: file.Path, ResourceID: attempt.ResourceID, ContentHash: file.ContentHash,
					PromptHash: file.PromptHash, Model: file.Model, CreatedAt: timestamp,
				}); err != nil {
					return nil, fmt.Errorf("finalize candidate: own file %s: %w", file.Path, err)
				}
			}
		}
		for _, file := range candidateFiles {
			if err := q.AcceptCandidateFile(ctx, db.AcceptCandidateFileParams{
				CandidateFileID: file.ID, ResourceID: attempt.ResourceID, Path: file.Path, AcceptedAt: timestamp,
			}); err != nil {
				return nil, fmt.Errorf("finalize candidate: accept file %s: %w", file.Path, err)
			}
		}
		if err := q.DeleteDependencies(ctx, attempt.ResourceID); err != nil {
			return nil, fmt.Errorf("finalize candidate: clear dependencies: %w", err)
		}
		for _, dependency := range input.Dependencies {
			if dependency.SourceID != attempt.ResourceID {
				return nil, fmt.Errorf("finalize candidate: dependency source must be %s", attempt.ResourceID)
			}
			if err := q.SetDependency(ctx, db.SetDependencyParams{
				SourceID: dependency.SourceID, TargetID: dependency.TargetID, Kind: dependency.Kind,
			}); err != nil {
				return nil, fmt.Errorf("finalize candidate: dependency: %w", err)
			}
		}
		if input.Notes != "" {
			if err := q.CreateNote(ctx, db.CreateNoteParams{
				ResourceID: attempt.ResourceID, ApplyID: attempt.ApplyID, Content: input.Notes, CreatedAt: timestamp,
			}); err != nil {
				return nil, fmt.Errorf("finalize candidate: note: %w", err)
			}
		}
		ancestorID := stringVal(attempt.ParentAttemptID)
		for ancestorID != "" {
			failures, listErr := q.ListFailureClassificationsByAttempt(ctx, ancestorID)
			if listErr != nil {
				return nil, fmt.Errorf("finalize candidate: list ancestor failures: %w", listErr)
			}
			for _, failure := range failures {
				if failure.ResolvedAt != nil {
					continue
				}
				if _, resolveErr := q.ResolveFailureClassification(ctx, db.ResolveFailureClassificationParams{
					Resolution: "resolved by accepted retry", ResolvedByAttempt: &attempt.ID,
					ResolvedAt: &timestamp, ID: failure.ID,
				}); resolveErr != nil {
					return nil, fmt.Errorf("finalize candidate: resolve ancestor failure: %w", resolveErr)
				}
			}
			ancestor, getErr := q.GetGenerationAttempt(ctx, ancestorID)
			if getErr != nil {
				return nil, fmt.Errorf("finalize candidate: ancestor attempt: %w", getErr)
			}
			ancestorID = stringVal(ancestor.ParentAttemptID)
		}
	}

	var failure *FailureClassification
	if input.Failure != nil {
		write := *input.Failure
		write.AttemptID = attempt.ID
		write.ExecutionID = manifest.ID
		failure, err = createFailureClassificationTx(ctx, q, write, timestamp)
		if err != nil {
			return nil, fmt.Errorf("finalize candidate: failure: %w", err)
		}
	}
	if input.HandoffRole != "" {
		failureID := ""
		if failure != nil {
			failureID = failure.ID
		}
		if _, err := createAttemptHandoffTx(ctx, q, AttemptHandoff{
			SourceAttemptID: attempt.ID, SourceRole: attempt.Role, TargetRole: input.HandoffRole,
			Reason: input.Reason, ExpectedOutcome: "produce a corrected candidate", FailureID: failureID,
		}, timestamp); err != nil {
			return nil, fmt.Errorf("finalize candidate: handoff: %w", err)
		}
	}
	lastError := ""
	if !input.Accepted {
		lastError = input.Reason
	}
	if err := q.UpdateSessionResourceState(ctx, db.UpdateSessionResourceStateParams{
		State: sessionState, LastError: stringPtr(lastError), Attempts: int64(input.Attempts),
		UpdatedAt: timestamp, SessionID: attempt.SessionID, ResourceID: attempt.ResourceID,
	}); err != nil {
		return nil, fmt.Errorf("finalize candidate: session resource: %w", err)
	}
	actionID := uuid.NewString()
	if err := q.CreateApplyAction(ctx, db.CreateApplyActionParams{
		ID: actionID, ApplyID: attempt.ApplyID, ResourceID: attempt.ResourceID, Action: "commit", StartedAt: timestamp,
	}); err != nil {
		return nil, fmt.Errorf("finalize candidate: apply action: %w", err)
	}
	var rejectionReason *string
	if !input.Accepted && input.Reason != "" {
		rejectionReason = &input.Reason
	}
	if err := q.UpdateApplyAction(ctx, db.UpdateApplyActionParams{
		Outcome: &actionOutcome, Error: rejectionReason, DoneAt: &timestamp, ID: actionID,
	}); err != nil {
		return nil, fmt.Errorf("finalize candidate: finish apply action: %w", err)
	}
	if err := q.CreateExecutionGeneration(ctx, db.CreateExecutionGenerationParams{
		ID: uuid.NewString(), ApplyID: stringPtr(attempt.ApplyID), ResourceID: attempt.ResourceID,
		PromptHash: manifest.ContextHash, Model: manifest.Model, Outcome: &disposition,
		RejectionReason: rejectionReason, RetryCount: attempt.RetryNumber - 1,
		DurationMs: &input.DurationMS, InputTokens: &input.InputTokens, OutputTokens: &input.OutputTokens,
		CostUsd: &input.CostUSD, CreatedAt: timestamp, ExecutionID: &manifest.ID,
	}); err != nil {
		return nil, fmt.Errorf("finalize candidate: generation projection: %w", err)
	}
	if err := createExecutionEvent(ctx, q, manifest.ID, manifest.Status, string(toStatus), "candidate_"+disposition, input.Details, timestamp); err != nil {
		return nil, fmt.Errorf("finalize candidate: event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("finalize candidate: commit: %w", err)
	}
	return s.GetExecutionManifest(ctx, manifest.ID)
}

func validateAcceptedFiles(candidateFiles []db.CandidateFile, files []GeneratedFile, resourceID string) error {
	if len(candidateFiles) != len(files) {
		return fmt.Errorf("finalize candidate: accepted file set differs from immutable candidate")
	}
	accepted := make(map[string]GeneratedFile, len(files))
	for _, file := range files {
		if file.ResourceID != resourceID {
			return fmt.Errorf("finalize candidate: file %s has wrong resource owner", file.Path)
		}
		accepted[file.Path] = file
	}
	for _, candidate := range candidateFiles {
		file, ok := accepted[candidate.Path]
		if !ok || file.ContentHash != candidate.ContentHash {
			return fmt.Errorf("finalize candidate: accepted file %s differs from immutable candidate", candidate.Path)
		}
	}
	return nil
}

func createExecutionEvent(ctx context.Context, q *db.Queries, executionID, from, to, kind string, details map[string]any, timestamp string) error {
	detailsJSON, detailsHash, err := execution.CanonicalRedacted(nonNilMap(details))
	if err != nil {
		return err
	}
	return q.CreateExecutionEvent(ctx, db.CreateExecutionEventParams{
		ID: uuid.NewString(), ExecutionID: executionID, FromStatus: from, ToStatus: to,
		EventKind: kind, DetailsJson: detailsJSON, DetailsHash: detailsHash, CreatedAt: timestamp,
	})
}

func (s *Store) GetExecutionManifest(ctx context.Context, id string) (*ExecutionManifest, error) {
	row, err := s.queries.GetExecutionManifest(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateExecutionManifest(ctx, row)
}

func (s *Store) GetExecutionManifestByAttempt(ctx context.Context, attemptID string) (*ExecutionManifest, error) {
	row, err := s.queries.GetExecutionManifestByAttempt(ctx, attemptID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateExecutionManifest(ctx, row)
}

func (s *Store) ListExecutionManifests(ctx context.Context, limit int) ([]ExecutionManifest, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListRecentExecutionManifests(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	result := make([]ExecutionManifest, 0, len(rows))
	for _, row := range rows {
		manifest, hydrateErr := s.hydrateExecutionManifest(ctx, row)
		if hydrateErr != nil {
			return nil, hydrateErr
		}
		result = append(result, *manifest)
	}
	return result, nil
}

func (s *Store) ListStaleExecutionManifests(ctx context.Context, before time.Time) ([]ExecutionManifest, error) {
	rows, err := s.queries.ListStaleExecutionManifests(ctx, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	result := make([]ExecutionManifest, 0, len(rows))
	for _, row := range rows {
		manifest, hydrateErr := s.hydrateExecutionManifest(ctx, row)
		if hydrateErr != nil {
			return nil, hydrateErr
		}
		result = append(result, *manifest)
	}
	return result, nil
}

func (s *Store) hydrateExecutionManifest(ctx context.Context, row db.ExecutionManifest) (*ExecutionManifest, error) {
	attempt, err := s.queries.GetGenerationAttempt(ctx, row.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("hydrate execution: attempt: %w", err)
	}
	manifest := ExecutionManifest{
		ID: row.ID, AttemptID: row.AttemptID, ContextManifestID: row.ContextManifestID,
		SessionID: attempt.SessionID, ResourceID: attempt.ResourceID, RetryNumber: int(attempt.RetryNumber),
		ProtocolVersion: row.ProtocolVersion, IdempotencyKey: row.IdempotencyKey,
		PlanOperationID: row.PlanOperationID, Role: row.Role, RolePolicyVersion: row.RolePolicyVersion,
		ContextPolicy: row.ContextPolicy, HostName: row.HostName, HostVersion: row.HostVersion,
		Provider: row.Provider, Model: row.Model, InferenceConfigJSON: row.InferenceConfigJson,
		InferenceConfigHash: row.InferenceConfigHash, AgentConfigJSON: row.AgentConfigJson,
		AgentConfigHash: row.AgentConfigHash, ToolPermissionsJSON: row.ToolPermissionsJson,
		ToolPermissionsHash: row.ToolPermissionsHash, TemplateHashesJSON: row.TemplateHashesJson,
		TemplateHashesHash: row.TemplateHashesHash, SystemInstructionsHash: row.SystemInstructionsBlob,
		ContextHash: row.ContextHash, HostSessionID: row.HostSessionID, Status: row.Status,
		HostCommitRef: row.HostCommitRef, GoalProgressJSON: row.GoalProgressJson,
		GoalProgressHash:  row.GoalProgressHash,
		DispositionReason: row.DispositionReason, StartedAt: parseTime(row.StartedAt),
		CompletedAt: parseOptionalTime(row.CompletedAt), DurationMS: row.DurationMs,
		InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, CostUSD: row.CostUsd,
		CreatedAt: parseTime(row.CreatedAt), UpdatedAt: parseTime(row.UpdatedAt),
	}
	if err := json.Unmarshal([]byte(row.InferenceConfigJson), &manifest.InferenceConfig); err != nil {
		return nil, fmt.Errorf("hydrate execution: inference configuration: %w", err)
	}
	if err := json.Unmarshal([]byte(row.AgentConfigJson), &manifest.AgentConfig); err != nil {
		return nil, fmt.Errorf("hydrate execution: agent configuration: %w", err)
	}
	if err := json.Unmarshal([]byte(row.TemplateHashesJson), &manifest.TemplateHashes); err != nil {
		return nil, fmt.Errorf("hydrate execution: template hashes: %w", err)
	}
	if err := json.Unmarshal([]byte(row.GoalProgressJson), &manifest.GoalProgress); err != nil {
		return nil, fmt.Errorf("hydrate execution: goal progress: %w", err)
	}
	manifest.GoalIDs, err = s.queries.ListExecutionGoals(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("hydrate execution: goals: %w", err)
	}
	manifest.CapabilityIDs, err = s.queries.ListExecutionCapabilities(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("hydrate execution: capabilities: %w", err)
	}
	blob, err := s.queries.GetContentBlob(ctx, row.SystemInstructionsBlob)
	if err != nil {
		return nil, fmt.Errorf("hydrate execution: system instructions: %w", err)
	}
	manifest.SystemInstructions = string(blob.Content)
	toolRows, err := s.queries.ListExecutionTools(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("hydrate execution: tools: %w", err)
	}
	for _, tool := range toolRows {
		manifest.Tools = append(manifest.Tools, ExecutionTool{Name: tool.Name, Permission: tool.Permission, Ordinal: int(tool.Ordinal)})
	}
	eventRows, err := s.queries.ListExecutionEvents(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("hydrate execution: events: %w", err)
	}
	for _, event := range eventRows {
		manifest.Events = append(manifest.Events, ExecutionEvent{
			ID: event.ID, FromStatus: event.FromStatus, ToStatus: event.ToStatus, Kind: event.EventKind,
			DetailsJSON: event.DetailsJson, DetailsHash: event.DetailsHash, CreatedAt: parseTime(event.CreatedAt),
		})
	}
	return &manifest, nil
}

func (s *Store) GetCandidateSetByAttempt(ctx context.Context, attemptID string) (*CandidateSet, error) {
	row, err := s.queries.GetCandidateSetByAttempt(ctx, attemptID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateCandidateSet(ctx, row)
}

func (s *Store) GetLatestRejectedCandidate(ctx context.Context, sessionID, resourceID string) (*CandidateSet, error) {
	row, err := s.queries.GetLatestRejectedCandidateForResource(ctx, db.GetLatestRejectedCandidateForResourceParams{
		SessionID: sessionID, ResourceID: resourceID,
	})
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateCandidateSet(ctx, row)
}

func (s *Store) hydrateCandidateSet(ctx context.Context, row db.CandidateSet) (*CandidateSet, error) {
	candidate := CandidateSet{
		ID: row.ID, ExecutionID: row.ExecutionID, AttemptID: row.AttemptID, CandidateHash: row.CandidateHash,
		Status: row.Status, DispositionReason: row.DispositionReason, CreatedAt: parseTime(row.CreatedAt),
		DisposedAt: parseOptionalTime(row.DisposedAt),
	}
	rows, err := s.queries.ListCandidateFiles(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("hydrate candidate: files: %w", err)
	}
	for _, row := range rows {
		blob, blobErr := s.queries.GetContentBlob(ctx, row.ContentBlob)
		if blobErr != nil {
			return nil, fmt.Errorf("hydrate candidate: %s: %w", row.Path, blobErr)
		}
		candidate.Files = append(candidate.Files, CandidateFile{
			ID: row.ID, Path: row.Path, Content: string(blob.Content), ContentHash: row.ContentHash,
			ByteSize: int(row.ByteSize), WriteIntent: row.WriteIntent, Ordinal: int(row.Ordinal),
		})
	}
	return &candidate, nil
}

func (s *Store) AcceptCandidateFiles(ctx context.Context, candidateID, resourceID string) error {
	candidate, err := s.queries.GetCandidateSet(ctx, candidateID)
	if err != nil {
		return mapNotFound(err)
	}
	if candidate.Status != "accepted" {
		return fmt.Errorf("accept candidate files: candidate %s is %s", candidateID, candidate.Status)
	}
	rows, err := s.queries.ListCandidateFiles(ctx, candidateID)
	if err != nil {
		return err
	}
	timestamp := now()
	for _, row := range rows {
		if err := s.queries.AcceptCandidateFile(ctx, db.AcceptCandidateFileParams{
			CandidateFileID: row.ID, ResourceID: resourceID, Path: row.Path, AcceptedAt: timestamp,
		}); err != nil {
			return fmt.Errorf("accept candidate files: %s: %w", row.Path, err)
		}
	}
	return nil
}

func (s *Store) CreateFailureClassification(ctx context.Context, input FailureClassificationWrite) (*FailureClassification, error) {
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("classify failure: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	failure, err := createFailureClassificationTx(ctx, s.queries.WithTx(tx), input, now())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("classify failure: commit: %w", err)
	}
	return failure, nil
}

func createFailureClassificationTx(ctx context.Context, q *db.Queries, input FailureClassificationWrite, timestamp string) (*FailureClassification, error) {
	if input.AttemptID == "" || input.EvidenceSource == "" {
		return nil, fmt.Errorf("classify failure: attempt and evidence source are required")
	}
	if err := execution.ValidateFailureCategory(input.Category); err != nil {
		return nil, fmt.Errorf("classify failure: %w", err)
	}
	if input.Origin != "engine" && input.Origin != "host" && input.Origin != "human" {
		return nil, fmt.Errorf("classify failure: origin must be engine, host, or human")
	}
	if input.Confidence < 0 || input.Confidence > 1 {
		return nil, fmt.Errorf("classify failure: confidence must be between zero and one")
	}
	attempt, err := q.GetGenerationAttempt(ctx, input.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("classify failure: attempt: %w", mapNotFound(err))
	}
	if input.ExecutionID != "" {
		manifest, getErr := q.GetExecutionManifest(ctx, input.ExecutionID)
		if getErr != nil || manifest.AttemptID != attempt.ID {
			return nil, fmt.Errorf("classify failure: execution must belong to attempt %s", attempt.ID)
		}
	}
	route := execution.RouteFailure(input.Category)
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	evidence := execution.RedactText(input.Evidence)
	var evidenceHash *string
	if evidence != "" {
		hash := contextmanifest.Hash(evidence)
		if err := q.PutContentBlob(ctx, db.PutContentBlobParams{
			Hash: hash, Content: []byte(evidence), ByteSize: int64(len([]byte(evidence))), CreatedAt: timestamp,
		}); err != nil {
			return nil, fmt.Errorf("classify failure: evidence: %w", err)
		}
		evidenceHash = &hash
	}
	var executionID *string
	if input.ExecutionID != "" {
		executionID = &input.ExecutionID
	}
	if err := q.CreateFailureClassification(ctx, db.CreateFailureClassificationParams{
		ID: input.ID, AttemptID: input.AttemptID, ExecutionID: executionID, ResourceID: attempt.ResourceID,
		Category: string(input.Category), Origin: input.Origin, Confidence: input.Confidence,
		EvidenceSource: input.EvidenceSource, EvidenceReference: input.EvidenceReference, EvidenceBlob: evidenceHash,
		CorrectiveAction: route.CorrectiveAction, NextRole: string(route.NextRole), BlocksRetry: boolInt(route.BlocksRetry),
		OverrideOf: stringPtr(input.OverrideOf), Resolution: "", CreatedAt: timestamp,
	}); err != nil {
		return nil, fmt.Errorf("classify failure: insert: %w", err)
	}
	goals := append([]string(nil), input.GoalIDs...)
	sort.Strings(goals)
	for _, goalID := range dedupeStoreStrings(goals) {
		if err := q.AddFailureGoal(ctx, db.AddFailureGoalParams{FailureID: input.ID, GoalID: goalID}); err != nil {
			return nil, fmt.Errorf("classify failure: link goal %s: %w", goalID, err)
		}
	}
	return &FailureClassification{
		ID: input.ID, AttemptID: attempt.ID, ExecutionID: input.ExecutionID, ResourceID: attempt.ResourceID,
		Category: string(input.Category), Origin: input.Origin, Confidence: input.Confidence,
		EvidenceSource: input.EvidenceSource, EvidenceReference: input.EvidenceReference,
		Evidence: evidence, EvidenceHash: stringVal(evidenceHash), CorrectiveAction: route.CorrectiveAction,
		NextRole: string(route.NextRole), BlocksRetry: route.BlocksRetry, OverrideOf: input.OverrideOf,
		GoalIDs: dedupeStoreStrings(goals), CreatedAt: parseTime(timestamp),
	}, nil
}

func (s *Store) ListFailureClassificationsByAttempt(ctx context.Context, attemptID string) ([]FailureClassification, error) {
	rows, err := s.queries.ListFailureClassificationsByAttempt(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	return s.hydrateFailures(ctx, rows)
}

func (s *Store) ListFailureClassifications(ctx context.Context, limit int) ([]FailureClassification, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListRecentFailureClassifications(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	return s.hydrateFailures(ctx, rows)
}

func (s *Store) hydrateFailures(ctx context.Context, rows []db.FailureClassification) ([]FailureClassification, error) {
	result := make([]FailureClassification, 0, len(rows))
	for _, row := range rows {
		failure := FailureClassification{
			ID: row.ID, AttemptID: row.AttemptID, ExecutionID: stringVal(row.ExecutionID),
			ResourceID: row.ResourceID, Category: row.Category, Origin: row.Origin, Confidence: row.Confidence,
			EvidenceSource: row.EvidenceSource, EvidenceReference: row.EvidenceReference,
			CorrectiveAction: row.CorrectiveAction, NextRole: row.NextRole, BlocksRetry: row.BlocksRetry == 1,
			OverrideOf: stringVal(row.OverrideOf), Resolution: row.Resolution,
			ResolvedByAttempt: stringVal(row.ResolvedByAttempt), CreatedAt: parseTime(row.CreatedAt),
			ResolvedAt: parseOptionalTime(row.ResolvedAt),
		}
		if row.EvidenceBlob != nil {
			blob, blobErr := s.queries.GetContentBlob(ctx, *row.EvidenceBlob)
			if blobErr != nil {
				return nil, fmt.Errorf("hydrate failure %s evidence: %w", row.ID, blobErr)
			}
			failure.Evidence = string(blob.Content)
			failure.EvidenceHash = blob.Hash
		}
		goals, goalErr := s.queries.ListFailureGoals(ctx, row.ID)
		if goalErr != nil {
			return nil, fmt.Errorf("hydrate failure %s goals: %w", row.ID, goalErr)
		}
		failure.GoalIDs = goals
		result = append(result, failure)
	}
	return result, nil
}

func (s *Store) ResolveFailureClassification(ctx context.Context, id, resolution, resolvedByAttempt string) error {
	if resolution == "" {
		return fmt.Errorf("resolve failure: resolution is required")
	}
	timestamp := now()
	affected, err := s.queries.ResolveFailureClassification(ctx, db.ResolveFailureClassificationParams{
		Resolution: resolution, ResolvedByAttempt: stringPtr(resolvedByAttempt), ResolvedAt: &timestamp, ID: id,
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("resolve failure: classification not found or already resolved")
	}
	return nil
}

func (s *Store) CreateAttemptHandoff(ctx context.Context, handoff AttemptHandoff) (*AttemptHandoff, error) {
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create handoff: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	created, err := createAttemptHandoffTx(ctx, s.queries.WithTx(tx), handoff, now())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create handoff: commit: %w", err)
	}
	return created, nil
}

func createAttemptHandoffTx(ctx context.Context, q *db.Queries, handoff AttemptHandoff, timestamp string) (*AttemptHandoff, error) {
	if handoff.SourceAttemptID == "" || handoff.TargetRole == "" || handoff.Reason == "" || handoff.ExpectedOutcome == "" {
		return nil, fmt.Errorf("create handoff: source attempt, target role, reason, and expected outcome are required")
	}
	attempt, err := q.GetGenerationAttempt(ctx, handoff.SourceAttemptID)
	if err != nil {
		return nil, fmt.Errorf("create handoff: source attempt: %w", mapNotFound(err))
	}
	if handoff.SourceRole == "" {
		handoff.SourceRole = attempt.Role
	}
	if handoff.SourceRole != attempt.Role {
		return nil, fmt.Errorf("create handoff: source role does not match attempt")
	}
	policy, err := execution.LookupRole(handoff.SourceRole)
	if err != nil {
		return nil, fmt.Errorf("create handoff: %w", err)
	}
	targetPolicy, err := execution.LookupRole(handoff.TargetRole)
	if err != nil {
		return nil, fmt.Errorf("create handoff: %w", err)
	}
	if !policy.Allows(execution.ActionCreateHandoff) || !policy.AllowsHandoff(targetPolicy.Role) {
		return nil, fmt.Errorf("create handoff: role %s cannot hand off to %s", policy.Role, targetPolicy.Role)
	}
	if handoff.FailureID != "" {
		failure, getErr := q.GetFailureClassification(ctx, handoff.FailureID)
		if getErr != nil || failure.AttemptID != attempt.ID {
			return nil, fmt.Errorf("create handoff: failure must belong to source attempt")
		}
	}
	if handoff.ID == "" {
		handoff.ID = uuid.NewString()
	}
	handoff.Status = "pending"
	handoff.CreatedAt = parseTime(timestamp)
	if err := q.CreateAttemptHandoff(ctx, db.CreateAttemptHandoffParams{
		ID: handoff.ID, SourceAttemptID: handoff.SourceAttemptID, SourceRole: handoff.SourceRole,
		TargetRole: handoff.TargetRole, Reason: handoff.Reason, ExpectedOutcome: handoff.ExpectedOutcome,
		FailureID: stringPtr(handoff.FailureID), Status: handoff.Status, CreatedAt: timestamp,
	}); err != nil {
		return nil, fmt.Errorf("create handoff: insert: %w", err)
	}
	return &handoff, nil
}

func (s *Store) ListPendingAttemptHandoffs(ctx context.Context, limit int) ([]AttemptHandoff, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListPendingAttemptHandoffs(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	result := make([]AttemptHandoff, len(rows))
	for index, row := range rows {
		result[index] = dbAttemptHandoff(row)
	}
	return result, nil
}

func (s *Store) ListAttemptHandoffsBySource(ctx context.Context, attemptID string) ([]AttemptHandoff, error) {
	rows, err := s.queries.ListAttemptHandoffsBySource(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	result := make([]AttemptHandoff, len(rows))
	for index, row := range rows {
		result[index] = dbAttemptHandoff(row)
	}
	return result, nil
}

func (s *Store) AcceptAttemptHandoff(ctx context.Context, id, targetAttemptID string) error {
	if targetAttemptID == "" {
		return fmt.Errorf("accept handoff: target attempt is required")
	}
	handoff, err := s.queries.GetAttemptHandoff(ctx, id)
	if err != nil {
		return mapNotFound(err)
	}
	target, err := s.queries.GetGenerationAttempt(ctx, targetAttemptID)
	if err != nil {
		return fmt.Errorf("accept handoff: target attempt: %w", mapNotFound(err))
	}
	source, err := s.queries.GetGenerationAttempt(ctx, handoff.SourceAttemptID)
	if err != nil {
		return fmt.Errorf("accept handoff: source attempt: %w", mapNotFound(err))
	}
	if target.Role != handoff.TargetRole || target.SessionID != source.SessionID || target.ResourceID != source.ResourceID {
		return fmt.Errorf("accept handoff: target attempt must have the requested role and source session/resource")
	}
	timestamp := now()
	affected, err := s.queries.AcceptAttemptHandoff(ctx, db.AcceptAttemptHandoffParams{
		TargetAttemptID: &targetAttemptID, AcceptedAt: &timestamp, ID: id,
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("accept handoff: handoff is not pending")
	}
	return nil
}

func dbAttemptHandoff(row db.AttemptHandoff) AttemptHandoff {
	return AttemptHandoff{
		ID: row.ID, SourceAttemptID: row.SourceAttemptID, TargetAttemptID: stringVal(row.TargetAttemptID),
		SourceRole: row.SourceRole, TargetRole: row.TargetRole, Reason: row.Reason,
		ExpectedOutcome: row.ExpectedOutcome, FailureID: stringVal(row.FailureID), Status: row.Status,
		CreatedAt: parseTime(row.CreatedAt), AcceptedAt: parseOptionalTime(row.AcceptedAt),
	}
}

func dedupeStoreStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
