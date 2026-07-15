package store

import (
	"context"
	"time"

	"github.com/crestenstclair/crest-spec/internal/db"
)

type AgentEvent struct {
	ID           string
	GenerationID string
	ResourceID   string
	ApplyID      string
	EventType    string
	Attempt      int
	Content      string
	CreatedAt    time.Time
}

func dbAgentEventToAgentEvent(row db.AgentEvent) AgentEvent {
	return AgentEvent{
		ID: row.ID, GenerationID: stringVal(row.GenerationID), ResourceID: row.ResourceID,
		ApplyID: stringVal(row.ApplyID), EventType: row.EventType, Attempt: int(int64Val(row.Attempt)),
		Content: stringVal(row.Content), CreatedAt: parseTime(row.CreatedAt),
	}
}

func (s *Store) CreateAgentEvent(event AgentEvent) error {
	var attempt *int64
	if event.Attempt > 0 {
		value := int64(event.Attempt)
		attempt = &value
	}
	return s.queries.CreateAgentEvent(context.Background(), db.CreateAgentEventParams{
		ID: event.ID, GenerationID: stringPtr(event.GenerationID), ResourceID: event.ResourceID,
		ApplyID: stringPtr(event.ApplyID), EventType: event.EventType, Attempt: attempt,
		Content: stringPtr(event.Content), CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *Store) ListAgentEventsByGeneration(generationID string) ([]AgentEvent, error) {
	rows, err := s.queries.ListAgentEventsByGeneration(context.Background(), &generationID)
	if err != nil {
		return nil, err
	}
	return dbAgentEvents(rows), nil
}

func (s *Store) ListAgentEventsByResource(resourceID string) ([]AgentEvent, error) {
	rows, err := s.queries.ListAgentEventsByResource(context.Background(), resourceID)
	if err != nil {
		return nil, err
	}
	return dbAgentEvents(rows), nil
}

func (s *Store) ListRecentAgentEvents(limit int) ([]AgentEvent, error) {
	rows, err := s.queries.ListRecentAgentEvents(context.Background(), int64(limit))
	if err != nil {
		return nil, err
	}
	return dbAgentEvents(rows), nil
}

func dbAgentEvents(rows []db.AgentEvent) []AgentEvent {
	result := make([]AgentEvent, len(rows))
	for index, row := range rows {
		result[index] = dbAgentEventToAgentEvent(row)
	}
	return result
}
