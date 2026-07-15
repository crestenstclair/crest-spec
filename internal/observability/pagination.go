package observability

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const (
	DefaultPageLimit = 50
	MaximumPageLimit = 200
)

type PageRequest struct {
	Limit        int
	Cursor       string
	Status       string
	Kind         string
	Query        string
	GoalID       string
	CapabilityID string
	AttemptID    string
}

type cursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func NormalizePageRequest(request PageRequest) (PageRequest, error) {
	if request.Limit == 0 {
		request.Limit = DefaultPageLimit
	}
	if request.Limit < 1 || request.Limit > MaximumPageLimit {
		return PageRequest{}, fmt.Errorf("limit must be between 1 and %d", MaximumPageLimit)
	}
	if request.Cursor != "" {
		if _, err := decodeCursor(request.Cursor); err != nil {
			return PageRequest{}, fmt.Errorf("invalid cursor: %w", err)
		}
	}
	return request, nil
}

func encodeCursor(createdAt time.Time, id string) string {
	encoded, _ := json.Marshal(cursor{CreatedAt: createdAt.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeCursor(value string) (cursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor{}, err
	}
	var result cursor
	if err := json.Unmarshal(decoded, &result); err != nil {
		return cursor{}, err
	}
	if result.CreatedAt.IsZero() || result.ID == "" {
		return cursor{}, fmt.Errorf("cursor is incomplete")
	}
	return result, nil
}

func afterCursor(createdAt time.Time, id string, value cursor) bool {
	if createdAt.Before(value.CreatedAt) {
		return true
	}
	return createdAt.Equal(value.CreatedAt) && id < value.ID
}
