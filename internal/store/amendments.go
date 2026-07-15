package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/crestenstclair/crest-spec/internal/db"
)

type Amendment struct {
	ID              string
	ResourceID      string
	Name            string
	ContentHash     string
	Origin          string
	Prompt          string
	FindingJSON     string
	ValidationJSON  string
	State           string
	AppliedSpecHash string
	CreatedAt       time.Time
	AppliedAt       time.Time
	GraduatedAt     time.Time
}

func fmtTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func dbAmendmentToAmendment(row db.Amendment) Amendment {
	return Amendment{
		ID:              row.ID,
		ResourceID:      row.ResourceID,
		Name:            row.Name,
		ContentHash:     row.ContentHash,
		Origin:          row.Origin,
		Prompt:          row.Prompt,
		FindingJSON:     row.FindingJson,
		ValidationJSON:  row.ValidationJson,
		State:           row.State,
		AppliedSpecHash: row.AppliedSpecHash,
		CreatedAt:       parseTime(row.CreatedAt),
		AppliedAt:       parseTime(row.AppliedAt),
		GraduatedAt:     parseTime(row.GraduatedAt),
	}
}

func (s *Store) UpsertAmendment(amendment Amendment) error {
	if amendment.State == "" {
		amendment.State = "PENDING"
	}
	if amendment.Origin == "" {
		amendment.Origin = "manual"
	}
	return s.queries.UpsertAmendment(context.Background(), db.UpsertAmendmentParams{
		ID:              amendment.ID,
		ResourceID:      amendment.ResourceID,
		Name:            amendment.Name,
		ContentHash:     amendment.ContentHash,
		Origin:          amendment.Origin,
		Prompt:          amendment.Prompt,
		FindingJson:     amendment.FindingJSON,
		ValidationJson:  amendment.ValidationJSON,
		State:           amendment.State,
		AppliedSpecHash: amendment.AppliedSpecHash,
		CreatedAt:       fmtTime(amendment.CreatedAt),
		AppliedAt:       fmtTime(amendment.AppliedAt),
		GraduatedAt:     fmtTime(amendment.GraduatedAt),
	})
}

func (s *Store) ListAmendmentsByResource(resourceID string) ([]Amendment, error) {
	rows, err := s.queries.ListAmendmentsByResource(context.Background(), resourceID)
	if err != nil {
		return nil, err
	}
	return dbAmendments(rows), nil
}

func (s *Store) ListAmendmentsByState(state string) ([]Amendment, error) {
	rows, err := s.queries.ListAmendmentsByState(context.Background(), state)
	if err != nil {
		return nil, err
	}
	return dbAmendments(rows), nil
}

func (s *Store) ListAllAmendments() ([]Amendment, error) {
	rows, err := s.queries.ListAllAmendments(context.Background())
	if err != nil {
		return nil, err
	}
	return dbAmendments(rows), nil
}

func dbAmendments(rows []db.Amendment) []Amendment {
	result := make([]Amendment, len(rows))
	for index, row := range rows {
		result[index] = dbAmendmentToAmendment(row)
	}
	return result
}

func (s *Store) GetAmendment(resourceID, name string) (*Amendment, error) {
	row, err := s.queries.GetAmendment(context.Background(), db.GetAmendmentParams{
		ResourceID: resourceID,
		Name:       name,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	amendment := dbAmendmentToAmendment(row)
	return &amendment, nil
}

func (s *Store) UpdateAmendmentState(id, state, appliedSpecHash string, appliedAt, graduatedAt time.Time) error {
	return s.queries.UpdateAmendmentState(context.Background(), db.UpdateAmendmentStateParams{
		State:           state,
		AppliedSpecHash: appliedSpecHash,
		AppliedAt:       fmtTime(appliedAt),
		GraduatedAt:     fmtTime(graduatedAt),
		ID:              id,
	})
}

func (s *Store) DeleteAmendment(resourceID, name string) error {
	return s.queries.DeleteAmendment(context.Background(), db.DeleteAmendmentParams{
		ResourceID: resourceID,
		Name:       name,
	})
}
