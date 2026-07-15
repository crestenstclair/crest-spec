package store

import (
	"context"
	"time"

	"github.com/crestenstclair/crest-spec/internal/db"
)

type SessionResource struct {
	SessionID    string
	ResourceID   string
	State        string
	WaveIndex    int
	Attempts     int
	MaxRetries   int
	LastError    string
	LastOutput   string
	JobID        string
	UpdatedAt    time.Time
	Phase        string
	DispatchedAt time.Time
}

func dbSessionResourceToSessionResource(row db.SessionResource) SessionResource {
	return SessionResource{
		SessionID:    row.SessionID,
		ResourceID:   row.ResourceID,
		State:        row.State,
		WaveIndex:    int(row.WaveIndex),
		Attempts:     int(row.Attempts),
		MaxRetries:   int(row.MaxRetries),
		LastError:    stringVal(row.LastError),
		LastOutput:   stringVal(row.LastOutput),
		JobID:        stringVal(row.JobID),
		UpdatedAt:    parseTime(row.UpdatedAt),
		Phase:        row.Phase,
		DispatchedAt: parseTime(row.DispatchedAt),
	}
}

func (s *Store) UpsertSessionResource(resource SessionResource) error {
	dispatchedAt := ""
	if !resource.DispatchedAt.IsZero() {
		dispatchedAt = resource.DispatchedAt.Format(time.RFC3339Nano)
	}
	return s.queries.UpsertSessionResource(context.Background(), db.UpsertSessionResourceParams{
		SessionID:    resource.SessionID,
		ResourceID:   resource.ResourceID,
		State:        resource.State,
		WaveIndex:    int64(resource.WaveIndex),
		Attempts:     int64(resource.Attempts),
		MaxRetries:   int64(resource.MaxRetries),
		LastError:    stringPtr(resource.LastError),
		LastOutput:   stringPtr(resource.LastOutput),
		JobID:        stringPtr(resource.JobID),
		UpdatedAt:    now(),
		Phase:        resource.Phase,
		DispatchedAt: dispatchedAt,
	})
}

func (s *Store) UpdateSessionResourcePhase(sessionID, resourceID, phase string, attempts int) error {
	return s.queries.UpdateSessionResourcePhase(context.Background(), db.UpdateSessionResourcePhaseParams{
		Phase: phase, Attempts: int64(attempts), UpdatedAt: now(), SessionID: sessionID, ResourceID: resourceID,
	})
}

func (s *Store) SetSessionResourceDispatched(sessionID, resourceID string) error {
	return s.queries.SetSessionResourceDispatched(context.Background(), db.SetSessionResourceDispatchedParams{
		DispatchedAt: now(), UpdatedAt: now(), SessionID: sessionID, ResourceID: resourceID,
	})
}

func (s *Store) GetSessionResource(sessionID, resourceID string) (*SessionResource, error) {
	row, err := s.queries.GetSessionResource(context.Background(), db.GetSessionResourceParams{
		SessionID: sessionID, ResourceID: resourceID,
	})
	if err != nil {
		return nil, mapNotFound(err)
	}
	result := dbSessionResourceToSessionResource(row)
	return &result, nil
}

func (s *Store) ListSessionResources(sessionID string) ([]SessionResource, error) {
	rows, err := s.queries.ListSessionResources(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	return dbSessionResources(rows), nil
}

func (s *Store) ListSessionResourcesByWave(sessionID string, wave int) ([]SessionResource, error) {
	rows, err := s.queries.ListSessionResourcesByWave(context.Background(), db.ListSessionResourcesByWaveParams{
		SessionID: sessionID, WaveIndex: int64(wave),
	})
	if err != nil {
		return nil, err
	}
	return dbSessionResources(rows), nil
}

func (s *Store) UpdateSessionResourceState(sessionID, resourceID, state, lastError, lastOutput string, attempts int, jobID string) error {
	return s.queries.UpdateSessionResourceState(context.Background(), db.UpdateSessionResourceStateParams{
		State: state, LastError: stringPtr(lastError), LastOutput: stringPtr(lastOutput),
		Attempts: int64(attempts), JobID: stringPtr(jobID), UpdatedAt: now(),
		SessionID: sessionID, ResourceID: resourceID,
	})
}

func (s *Store) ListSessionResourcesByState(sessionID, state string) ([]SessionResource, error) {
	rows, err := s.queries.ListSessionResourcesByState(context.Background(), db.ListSessionResourcesByStateParams{
		SessionID: sessionID, State: state,
	})
	if err != nil {
		return nil, err
	}
	return dbSessionResources(rows), nil
}

func dbSessionResources(rows []db.SessionResource) []SessionResource {
	result := make([]SessionResource, len(rows))
	for index, row := range rows {
		result[index] = dbSessionResourceToSessionResource(row)
	}
	return result
}

func (s *Store) DeleteSessionResources(sessionID string) error {
	return s.queries.DeleteSessionResources(context.Background(), sessionID)
}
