package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/db"
)

// ContextBatch records the one shared preparation interval for a context
// batch. Resource-specific selection and persistence use DispatchLifecycle.
type ContextBatch struct {
	ID          string
	SessionID   string
	WaveIndex   int
	Status      string
	StartedAt   time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (s *Store) StartContextBatch(ctx context.Context, sessionID string, waveIndex int) (*ContextBatch, error) {
	if sessionID == "" || waveIndex < 0 {
		return nil, fmt.Errorf("start context batch: session_id and nonnegative wave_index are required")
	}
	timestamp := now()
	id := uuid.NewString()
	if err := s.queries.CreateContextBatch(ctx, db.CreateContextBatchParams{
		ID: id, SessionID: sessionID, WaveIndex: int64(waveIndex),
		StartedAt: timestamp, CreatedAt: timestamp, UpdatedAt: timestamp,
	}); err != nil {
		return nil, fmt.Errorf("start context batch: %w", err)
	}
	return s.GetContextBatch(ctx, id)
}

func (s *Store) FinishContextBatch(ctx context.Context, id string, failed bool) (*ContextBatch, error) {
	status := "completed"
	if failed {
		status = "failed"
	}
	timestamp := now()
	changed, err := s.queries.CompleteContextBatch(ctx, db.CompleteContextBatchParams{
		Status: status, CompletedAt: timestamp, UpdatedAt: timestamp, ID: id,
	})
	if err != nil {
		return nil, fmt.Errorf("finish context batch: %w", err)
	}
	if changed != 1 {
		return nil, fmt.Errorf("finish context batch: batch is missing or already terminal")
	}
	return s.GetContextBatch(ctx, id)
}

func (s *Store) GetContextBatch(ctx context.Context, id string) (*ContextBatch, error) {
	row, err := s.queries.GetContextBatch(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	result := dbContextBatch(row)
	return &result, nil
}

func (s *Store) ListContextBatchesBySession(ctx context.Context, sessionID string) ([]ContextBatch, error) {
	rows, err := s.queries.ListContextBatchesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]ContextBatch, len(rows))
	for index, row := range rows {
		result[index] = dbContextBatch(row)
	}
	return result, nil
}

func dbContextBatch(row db.ContextBatch) ContextBatch {
	return ContextBatch{
		ID: row.ID, SessionID: row.SessionID, WaveIndex: int(row.WaveIndex), Status: row.Status,
		StartedAt: parseTime(row.StartedAt), CompletedAt: optionalStoreTime(row.CompletedAt),
		CreatedAt: parseTime(row.CreatedAt), UpdatedAt: parseTime(row.UpdatedAt),
	}
}
