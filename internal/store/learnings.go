package store

import (
	"context"
	"time"

	"github.com/crestenstclair/crest-spec/internal/db"
)

// Learning is the store's domain type for a craft-level learning.
type Learning struct {
	ID                 string
	ScopeLang          string
	ScopeKind          string
	Text               string
	Rationale          string
	SourceGenerationID string
	SourceApplyID      string
	Confidence         float64
	Status             string
	TimesApplied       int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func dbLearningToLearning(learning db.Learning) Learning {
	return Learning{
		ID:                 learning.ID,
		ScopeLang:          learning.ScopeLang,
		ScopeKind:          learning.ScopeKind,
		Text:               learning.Text,
		Rationale:          learning.Rationale,
		SourceGenerationID: stringVal(learning.SourceGenerationID),
		SourceApplyID:      stringVal(learning.SourceApplyID),
		Confidence:         learning.Confidence,
		Status:             learning.Status,
		TimesApplied:       int(learning.TimesApplied),
		CreatedAt:          parseTime(learning.CreatedAt),
		UpdatedAt:          parseTime(learning.UpdatedAt),
	}
}

func (s *Store) CreateLearning(learning Learning) error {
	if learning.Status == "" {
		learning.Status = "active"
	}
	return s.queries.CreateLearning(context.Background(), db.CreateLearningParams{
		ID:                 learning.ID,
		ScopeLang:          learning.ScopeLang,
		ScopeKind:          learning.ScopeKind,
		Text:               learning.Text,
		Rationale:          learning.Rationale,
		SourceGenerationID: stringPtr(learning.SourceGenerationID),
		SourceApplyID:      stringPtr(learning.SourceApplyID),
		Confidence:         learning.Confidence,
		Status:             learning.Status,
		TimesApplied:       int64(learning.TimesApplied),
		CreatedAt:          learning.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:          learning.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
}

// ListActiveLearnings returns active learnings matching the language and kind
// (empty scope = applies to any), highest confidence first, capped at limit.
func (s *Store) ListActiveLearnings(lang, kind string, limit int) ([]Learning, error) {
	rows, err := s.queries.ListActiveLearnings(context.Background(), db.ListActiveLearningsParams{
		ScopeLang: lang,
		ScopeKind: kind,
		Limit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	result := make([]Learning, len(rows))
	for index, row := range rows {
		result[index] = dbLearningToLearning(row)
	}
	return result, nil
}

func (s *Store) ListLearnings(status string) ([]Learning, error) {
	rows, err := s.queries.ListLearningsByStatus(context.Background(), status)
	if err != nil {
		return nil, err
	}
	result := make([]Learning, len(rows))
	for index, row := range rows {
		result[index] = dbLearningToLearning(row)
	}
	return result, nil
}

func (s *Store) UpdateLearningStatus(id, status string) error {
	return s.queries.UpdateLearningStatus(context.Background(), db.UpdateLearningStatusParams{
		Status:    status,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ID:        id,
	})
}

func (s *Store) IncrementLearningApplied(id string) error {
	return s.queries.IncrementLearningApplied(context.Background(), db.IncrementLearningAppliedParams{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ID:        id,
	})
}
