package spec

import (
	"context"
	"fmt"
)

func (s *Spec) Resolve(ctx context.Context, sessionID, resourceID, answer string, model string) error {
	_, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	if err := s.store.SetNote(resourceID, sessionID, answer); err != nil {
		return fmt.Errorf("set note: %w", err)
	}

	return nil
}

func (s *Spec) Amend(ctx context.Context, sessionID, resourceID string) error {
	_, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	_, planErr := s.Plan(ctx)
	if planErr != nil {
		return fmt.Errorf("re-plan after amend: %w", planErr)
	}

	return nil
}

func (s *Spec) Skip(ctx context.Context, sessionID, resourceID, reason string) error {
	_, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	if reason != "" {
		s.store.SetNote(resourceID, sessionID, fmt.Sprintf("SKIPPED: %s", reason))
	}

	return nil
}
