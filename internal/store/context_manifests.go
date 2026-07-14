package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/db"
	"github.com/crestenstclair/crest-spec/internal/execution"
)

// GenerationAttempt identifies one request for an agent to implement a
// resource. Context is prepared before the host runs so even blocked attempts
// remain inspectable.
type GenerationAttempt struct {
	ID                string
	SessionID         string
	ApplyID           string
	ResourceID        string
	PlanOperationID   string
	ParentAttemptID   string
	RetryNumber       int
	Role              string
	RolePolicyVersion string
	Status            string
	CreatedAt         time.Time
}

// ContextManifest is the immutable record of the exact context selected for a
// generation attempt. Large content is reconstructed from content-addressed
// SQLite blobs rather than copied into every manifest row.
type ContextManifest struct {
	ID                string
	Attempt           GenerationAttempt
	SelectorVersion   string
	EstimatorVersion  string
	SelectionStrategy string
	BudgetTokens      int
	EstimatedTokens   int
	OriginalBytes     int
	SelectedBytes     int
	TemplateHashes    map[string]string
	SystemPrompt      string
	RenderedPrompt    string
	ContextHash       string
	Blocked           bool
	BlockedReason     string
	CreatedAt         time.Time
	Sections          []ContextManifestSection
}

type ContextManifestSection struct {
	ID              string
	Ordinal         int
	Kind            string
	Title           string
	SourceKind      string
	SourceID        string
	SourcePath      string
	Priority        int
	Mandatory       bool
	Decision        string
	Reason          string
	OriginalHash    string
	ContentHash     string
	OriginalBytes   int
	SelectedBytes   int
	EstimatedTokens int
	Content         string
}

type ContextManifestSummary struct {
	ID                string
	Attempt           GenerationAttempt
	SelectorVersion   string
	EstimatorVersion  string
	SelectionStrategy string
	BudgetTokens      int
	EstimatedTokens   int
	OriginalBytes     int
	SelectedBytes     int
	ContextHash       string
	Blocked           bool
	BlockedReason     string
	CreatedAt         time.Time
}

// ContextManifestWrite is persisted as one transaction with its attempt,
// sections, blobs, and session dispatch transition.
type ContextManifestWrite struct {
	Manifest ContextManifest
	Dispatch bool
}

func (s *Store) CreateContextManifest(ctx context.Context, input ContextManifestWrite) (_ *ContextManifest, err error) {
	m := input.Manifest
	if err := validateContextManifest(m, input.Dispatch); err != nil {
		return nil, err
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create context manifest: begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	q := s.queries.WithTx(tx)
	timestamp := now()

	attemptCount, err := q.CountGenerationAttempts(ctx, db.CountGenerationAttemptsParams{
		SessionID: m.Attempt.SessionID, ResourceID: m.Attempt.ResourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("create context manifest: count attempts: %w", err)
	}
	m.Attempt.RetryNumber = int(attemptCount) + 1

	var parentID *string
	if m.Attempt.ParentAttemptID != "" {
		parent, getErr := q.GetGenerationAttempt(ctx, m.Attempt.ParentAttemptID)
		if getErr != nil {
			return nil, fmt.Errorf("create context manifest: parent attempt: %w", mapNotFound(getErr))
		}
		if parent.SessionID != m.Attempt.SessionID || parent.ResourceID != m.Attempt.ResourceID {
			return nil, fmt.Errorf("create context manifest: parent attempt must belong to the same session and resource")
		}
		parentID = &m.Attempt.ParentAttemptID
	} else if attemptCount > 0 {
		parent, getErr := q.GetLatestGenerationAttempt(ctx, db.GetLatestGenerationAttemptParams{
			SessionID: m.Attempt.SessionID, ResourceID: m.Attempt.ResourceID,
		})
		if getErr != nil {
			return nil, fmt.Errorf("create context manifest: latest attempt: %w", getErr)
		}
		m.Attempt.ParentAttemptID = parent.ID
		parentID = &m.Attempt.ParentAttemptID
	}

	status := "context_prepared"
	if m.Blocked {
		status = "context_blocked"
	}
	m.Attempt.Status = status
	m.Attempt.CreatedAt = parseTime(timestamp)
	if err = q.CreateGenerationAttempt(ctx, db.CreateGenerationAttemptParams{
		ID: m.Attempt.ID, SessionID: m.Attempt.SessionID, ApplyID: m.Attempt.ApplyID,
		ResourceID: m.Attempt.ResourceID, PlanOperationID: m.Attempt.PlanOperationID,
		ParentAttemptID: parentID, RetryNumber: int64(m.Attempt.RetryNumber),
		Role: m.Attempt.Role, Status: status, CreatedAt: timestamp,
		RolePolicyVersion: m.Attempt.RolePolicyVersion,
	}); err != nil {
		return nil, fmt.Errorf("create context manifest: insert attempt: %w", err)
	}

	putBlob := func(content string) (string, error) {
		hash := contextmanifest.Hash(content)
		if putErr := q.PutContentBlob(ctx, db.PutContentBlobParams{
			Hash: hash, Content: []byte(content), ByteSize: int64(len([]byte(content))), CreatedAt: timestamp,
		}); putErr != nil {
			return "", putErr
		}
		return hash, nil
	}
	systemHash, err := putBlob(m.SystemPrompt)
	if err != nil {
		return nil, fmt.Errorf("create context manifest: store system prompt: %w", err)
	}
	promptHash, err := putBlob(m.RenderedPrompt)
	if err != nil {
		return nil, fmt.Errorf("create context manifest: store rendered prompt: %w", err)
	}

	templateHashes, err := json.Marshal(m.TemplateHashes)
	if err != nil {
		return nil, fmt.Errorf("create context manifest: encode template hashes: %w", err)
	}
	m.CreatedAt = parseTime(timestamp)
	if err = q.CreateContextManifest(ctx, db.CreateContextManifestParams{
		ID: m.ID, AttemptID: m.Attempt.ID, SelectorVersion: m.SelectorVersion,
		EstimatorVersion: m.EstimatorVersion, SelectionStrategy: m.SelectionStrategy,
		BudgetTokens: int64(m.BudgetTokens), EstimatedTokens: int64(m.EstimatedTokens),
		OriginalBytes: int64(m.OriginalBytes), SelectedBytes: int64(m.SelectedBytes),
		TemplateHashesJson: string(templateHashes), SystemPromptBlob: systemHash,
		RenderedPromptBlob: promptHash, ContextHash: m.ContextHash, Blocked: boolInt(m.Blocked),
		BlockedReason: m.BlockedReason, CreatedAt: timestamp,
	}); err != nil {
		return nil, fmt.Errorf("create context manifest: insert manifest: %w", err)
	}

	sections := append([]ContextManifestSection(nil), m.Sections...)
	sort.Slice(sections, func(i, j int) bool { return sections[i].Ordinal < sections[j].Ordinal })
	for _, section := range sections {
		var contentHash *string
		if section.Decision == string(contextmanifest.Included) || section.Decision == string(contextmanifest.Truncated) {
			hash, putErr := putBlob(section.Content)
			if putErr != nil {
				return nil, fmt.Errorf("create context manifest: store section %q: %w", section.ID, putErr)
			}
			section.ContentHash = hash
			contentHash = &section.ContentHash
		}
		if err = q.CreateContextSection(ctx, db.CreateContextSectionParams{
			ID: section.ID, ManifestID: m.ID, Ordinal: int64(section.Ordinal),
			SectionKind: section.Kind, Title: section.Title, SourceKind: section.SourceKind,
			SourceID: section.SourceID, SourcePath: section.SourcePath, Priority: int64(section.Priority),
			Mandatory: boolInt(section.Mandatory), Decision: section.Decision, Reason: section.Reason,
			OriginalHash: section.OriginalHash, ContentBlob: contentHash,
			OriginalBytes: int64(section.OriginalBytes), SelectedBytes: int64(section.SelectedBytes),
			EstimatedTokens: int64(section.EstimatedTokens),
		}); err != nil {
			return nil, fmt.Errorf("create context manifest: insert section %q: %w", section.ID, err)
		}
	}

	if input.Dispatch {
		var affected int64
		affected, err = q.SetSessionResourceDispatchedTx(ctx, db.SetSessionResourceDispatchedTxParams{
			DispatchedAt: timestamp, UpdatedAt: timestamp,
			SessionID: m.Attempt.SessionID, ResourceID: m.Attempt.ResourceID,
		})
		if err != nil {
			return nil, fmt.Errorf("create context manifest: dispatch resource: %w", err)
		}
		if affected != 1 {
			return nil, fmt.Errorf("create context manifest: dispatch resource: session resource not found")
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("create context manifest: commit: %w", err)
	}
	m.Sections = sections
	return &m, nil
}

func validateContextManifest(m ContextManifest, dispatch bool) error {
	if m.ID == "" || m.Attempt.ID == "" || m.Attempt.SessionID == "" || m.Attempt.ApplyID == "" || m.Attempt.ResourceID == "" {
		return fmt.Errorf("create context manifest: manifest, attempt, session, apply, and resource identifiers are required")
	}
	if m.Attempt.Role == "" || m.Attempt.RolePolicyVersion == "" || m.SelectorVersion == "" || m.EstimatorVersion == "" || m.SelectionStrategy == "" || m.ContextHash == "" {
		return fmt.Errorf("create context manifest: role, role policy, selector, estimator, strategy, and context hash are required")
	}
	policy, err := execution.LookupRole(m.Attempt.Role)
	if err != nil || policy.Version != m.Attempt.RolePolicyVersion {
		return fmt.Errorf("create context manifest: role policy is not supported")
	}
	if m.BudgetTokens <= 0 || m.EstimatedTokens < 0 || m.OriginalBytes < 0 || m.SelectedBytes < 0 {
		return fmt.Errorf("create context manifest: context sizes are invalid")
	}
	if dispatch == m.Blocked {
		return fmt.Errorf("create context manifest: blocked manifests cannot dispatch and prepared manifests must dispatch")
	}
	for _, section := range m.Sections {
		if section.ID == "" || section.Kind == "" || section.SourceKind == "" || section.SourceID == "" || section.OriginalHash == "" {
			return fmt.Errorf("create context manifest: section identity and source fields are required")
		}
		if section.Decision != string(contextmanifest.Included) && section.Decision != string(contextmanifest.Truncated) && section.Decision != string(contextmanifest.Omitted) {
			return fmt.Errorf("create context manifest: invalid section decision %q", section.Decision)
		}
		if section.Decision == string(contextmanifest.Omitted) && section.Content != "" {
			return fmt.Errorf("create context manifest: omitted section %q cannot have selected content", section.ID)
		}
	}
	return nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func (s *Store) GetContextManifest(ctx context.Context, id string) (*ContextManifest, error) {
	row, err := s.queries.GetContextManifest(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateContextManifest(ctx, row)
}

func (s *Store) GetContextManifestByAttempt(ctx context.Context, attemptID string) (*ContextManifest, error) {
	row, err := s.queries.GetContextManifestByAttempt(ctx, attemptID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateContextManifest(ctx, row)
}

func (s *Store) ListContextManifests(ctx context.Context, limit int) ([]ContextManifest, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListRecentContextManifests(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	result := make([]ContextManifest, 0, len(rows))
	for _, row := range rows {
		manifest, hydrateErr := s.hydrateContextManifest(ctx, row)
		if hydrateErr != nil {
			return nil, hydrateErr
		}
		result = append(result, *manifest)
	}
	return result, nil
}

func (s *Store) ListContextManifestSummaries(ctx context.Context, limit int) ([]ContextManifestSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListContextManifestSummaries(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	result := make([]ContextManifestSummary, len(rows))
	for i, row := range rows {
		result[i] = ContextManifestSummary{
			ID: row.ID,
			Attempt: GenerationAttempt{
				ID: row.AttemptID, SessionID: row.SessionID, ApplyID: row.ApplyID,
				ResourceID: row.ResourceID, PlanOperationID: row.PlanOperationID,
				ParentAttemptID: stringVal(row.ParentAttemptID), RetryNumber: int(row.RetryNumber),
				Role: row.Role, RolePolicyVersion: row.RolePolicyVersion,
				Status: row.Status, CreatedAt: parseTime(row.CreatedAt),
			},
			SelectorVersion: row.SelectorVersion, EstimatorVersion: row.EstimatorVersion,
			SelectionStrategy: row.SelectionStrategy, BudgetTokens: int(row.BudgetTokens),
			EstimatedTokens: int(row.EstimatedTokens), OriginalBytes: int(row.OriginalBytes),
			SelectedBytes: int(row.SelectedBytes), ContextHash: row.ContextHash,
			Blocked: row.Blocked == 1, BlockedReason: row.BlockedReason, CreatedAt: parseTime(row.CreatedAt),
		}
	}
	return result, nil
}

func (s *Store) ListGenerationAttemptsBySession(ctx context.Context, sessionID string) ([]GenerationAttempt, error) {
	rows, err := s.queries.ListGenerationAttemptsBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]GenerationAttempt, len(rows))
	for i, row := range rows {
		result[i] = dbGenerationAttempt(row)
	}
	return result, nil
}

func (s *Store) ListGenerationAttemptsByResource(ctx context.Context, resourceID string, limit int) ([]GenerationAttempt, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListGenerationAttemptsByResource(ctx, db.ListGenerationAttemptsByResourceParams{ResourceID: resourceID, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	result := make([]GenerationAttempt, len(rows))
	for i, row := range rows {
		result[i] = dbGenerationAttempt(row)
	}
	return result, nil
}

func (s *Store) hydrateContextManifest(ctx context.Context, row db.ContextManifest) (*ContextManifest, error) {
	attemptRow, err := s.queries.GetGenerationAttempt(ctx, row.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("hydrate context manifest: attempt: %w", err)
	}
	systemPrompt, err := s.queries.GetContentBlob(ctx, row.SystemPromptBlob)
	if err != nil {
		return nil, fmt.Errorf("hydrate context manifest: system prompt: %w", err)
	}
	renderedPrompt, err := s.queries.GetContentBlob(ctx, row.RenderedPromptBlob)
	if err != nil {
		return nil, fmt.Errorf("hydrate context manifest: rendered prompt: %w", err)
	}
	sectionRows, err := s.queries.ListContextSections(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("hydrate context manifest: sections: %w", err)
	}
	sections := make([]ContextManifestSection, 0, len(sectionRows))
	for _, sectionRow := range sectionRows {
		section := ContextManifestSection{
			ID: sectionRow.ID, Ordinal: int(sectionRow.Ordinal), Kind: sectionRow.SectionKind,
			Title: sectionRow.Title, SourceKind: sectionRow.SourceKind, SourceID: sectionRow.SourceID,
			SourcePath: sectionRow.SourcePath, Priority: int(sectionRow.Priority), Mandatory: sectionRow.Mandatory == 1,
			Decision: sectionRow.Decision, Reason: sectionRow.Reason, OriginalHash: sectionRow.OriginalHash,
			OriginalBytes: int(sectionRow.OriginalBytes), SelectedBytes: int(sectionRow.SelectedBytes),
			EstimatedTokens: int(sectionRow.EstimatedTokens),
		}
		if sectionRow.ContentBlob != nil {
			blob, blobErr := s.queries.GetContentBlob(ctx, *sectionRow.ContentBlob)
			if blobErr != nil {
				return nil, fmt.Errorf("hydrate context manifest: section %q: %w", section.ID, blobErr)
			}
			section.ContentHash = blob.Hash
			section.Content = string(blob.Content)
		}
		sections = append(sections, section)
	}
	templateHashes := map[string]string{}
	if err := json.Unmarshal([]byte(row.TemplateHashesJson), &templateHashes); err != nil {
		return nil, fmt.Errorf("hydrate context manifest: template hashes: %w", err)
	}
	return &ContextManifest{
		ID: row.ID, Attempt: dbGenerationAttempt(attemptRow), SelectorVersion: row.SelectorVersion,
		EstimatorVersion: row.EstimatorVersion, SelectionStrategy: row.SelectionStrategy,
		BudgetTokens: int(row.BudgetTokens), EstimatedTokens: int(row.EstimatedTokens),
		OriginalBytes: int(row.OriginalBytes), SelectedBytes: int(row.SelectedBytes),
		TemplateHashes: templateHashes, SystemPrompt: string(systemPrompt.Content), RenderedPrompt: string(renderedPrompt.Content),
		ContextHash: row.ContextHash, Blocked: row.Blocked == 1, BlockedReason: row.BlockedReason,
		CreatedAt: parseTime(row.CreatedAt), Sections: sections,
	}, nil
}

func dbGenerationAttempt(row db.GenerationAttempt) GenerationAttempt {
	return GenerationAttempt{
		ID: row.ID, SessionID: row.SessionID, ApplyID: row.ApplyID, ResourceID: row.ResourceID,
		PlanOperationID: row.PlanOperationID, ParentAttemptID: stringVal(row.ParentAttemptID),
		RetryNumber: int(row.RetryNumber), Role: row.Role, RolePolicyVersion: row.RolePolicyVersion,
		Status: row.Status, CreatedAt: parseTime(row.CreatedAt),
	}
}

// CountContentBlobs is used by integrity tests and operational diagnostics to
// show that identical context material is stored once.
func (s *Store) CountContentBlobs(ctx context.Context) (int64, error) {
	return s.queries.CountContentBlobs(ctx)
}
