package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/db"
)

// CandidateReuse is append-only proof that an immutable accepted candidate
// was admitted into another session without model execution.
type CandidateReuse struct {
	ID                     string
	SessionID              string
	ResourceID             string
	SourceCandidateID      string
	CandidateHash          string
	GenerationInputHash    string
	DependencyContractHash string
	ReusedAt               time.Time
}

type CandidateReuseInput struct {
	SessionID              string
	ResourceID             string
	SourceCandidateID      string
	CandidateHash          string
	GenerationInputHash    string
	DependencyContractHash string
}

// FindReusableAcceptedCandidate only considers candidates carrying immutable
// acceptance fingerprints from an explicitly aborted source session. Legacy
// candidates have empty fingerprints and are deliberately ineligible.
func (s *Store) FindReusableAcceptedCandidate(
	ctx context.Context,
	resourceID, generationInputHash, dependencyContractHash string,
) (*CandidateSet, error) {
	if resourceID == "" || generationInputHash == "" || dependencyContractHash == "" {
		return nil, fmt.Errorf("find reusable candidate: complete governed identity is required")
	}
	row, err := s.queries.GetReusableAcceptedCandidate(ctx, db.GetReusableAcceptedCandidateParams{
		ResourceID: resourceID, GenerationInputHash: generationInputHash,
		DependencyContractHash: dependencyContractHash,
	})
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateCandidateSet(ctx, row)
}

// ApplyCandidateReuse atomically rechecks immutable candidate identity,
// records provenance, and makes the session resource terminal-but-unverified.
// Workspace bytes are checked by Spec while holding its path coordinator;
// this transaction is the second, database-side half of the fail-closed gate.
func (s *Store) ApplyCandidateReuse(ctx context.Context, input CandidateReuseInput) (*CandidateReuse, error) {
	if input.SessionID == "" || input.ResourceID == "" || input.SourceCandidateID == "" ||
		input.CandidateHash == "" || input.GenerationInputHash == "" || input.DependencyContractHash == "" {
		return nil, fmt.Errorf("apply candidate reuse: complete provenance is required")
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("apply candidate reuse: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)

	session, err := q.GetSession(ctx, input.SessionID)
	if err != nil || session.Status != "active" {
		return nil, fmt.Errorf("apply candidate reuse: active session required: %w", mapNotFound(err))
	}
	source, err := q.GetReusableAcceptedCandidate(ctx, db.GetReusableAcceptedCandidateParams{
		ResourceID: input.ResourceID, GenerationInputHash: input.GenerationInputHash,
		DependencyContractHash: input.DependencyContractHash,
	})
	if err != nil {
		return nil, fmt.Errorf("apply candidate reuse: source: %w", mapNotFound(err))
	}
	if source.ID != input.SourceCandidateID || source.CandidateHash != input.CandidateHash {
		return nil, fmt.Errorf("apply candidate reuse: source candidate identity changed")
	}

	timestamp := now()
	reuse := CandidateReuse{
		ID: uuid.NewString(), SessionID: input.SessionID, ResourceID: input.ResourceID,
		SourceCandidateID: source.ID, CandidateHash: source.CandidateHash,
		GenerationInputHash:    input.GenerationInputHash,
		DependencyContractHash: input.DependencyContractHash,
		ReusedAt:               parseTime(timestamp),
	}
	if err := q.CreateCandidateReuse(ctx, db.CreateCandidateReuseParams{
		ID: reuse.ID, SessionID: reuse.SessionID, ResourceID: reuse.ResourceID,
		SourceCandidateID: reuse.SourceCandidateID, CandidateHash: reuse.CandidateHash,
		GenerationInputHash:    reuse.GenerationInputHash,
		DependencyContractHash: reuse.DependencyContractHash, ReusedAt: timestamp,
	}); err != nil {
		return nil, fmt.Errorf("apply candidate reuse: provenance: %w", err)
	}
	changed, err := q.CommitReusedSessionResource(ctx, db.CommitReusedSessionResourceParams{
		UpdatedAt: timestamp, SessionID: input.SessionID, ResourceID: input.ResourceID,
		SourceCandidateID: &input.SourceCandidateID,
	})
	if err != nil || changed != 1 {
		return nil, fmt.Errorf("apply candidate reuse: session resource: affected=%d: %w", changed, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("apply candidate reuse: commit: %w", err)
	}
	return &reuse, nil
}

func (s *Store) GetCandidateReuse(ctx context.Context, sessionID, resourceID string) (*CandidateReuse, error) {
	row, err := s.queries.GetCandidateReuse(ctx, db.GetCandidateReuseParams{SessionID: sessionID, ResourceID: resourceID})
	if err != nil {
		return nil, mapNotFound(err)
	}
	result := dbCandidateReuse(row)
	return &result, nil
}

func (s *Store) ListCandidateReusesBySession(ctx context.Context, sessionID string) ([]CandidateReuse, error) {
	rows, err := s.queries.ListCandidateReusesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]CandidateReuse, len(rows))
	for index, row := range rows {
		result[index] = dbCandidateReuse(row)
	}
	return result, nil
}

func dbCandidateReuse(row db.CandidateReuse) CandidateReuse {
	return CandidateReuse{
		ID: row.ID, SessionID: row.SessionID, ResourceID: row.ResourceID,
		SourceCandidateID: row.SourceCandidateID, CandidateHash: row.CandidateHash,
		GenerationInputHash:    row.GenerationInputHash,
		DependencyContractHash: row.DependencyContractHash, ReusedAt: parseTime(row.ReusedAt),
	}
}
