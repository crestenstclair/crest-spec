package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/store"
)

func (s *Spec) Resolve(ctx context.Context, sessionID, resourceID, answer string, model string) error {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	if err := s.store.SetNote(resourceID, sess.ApplyID, answer); err != nil {
		return fmt.Errorf("set note: %w", err)
	}

	if model != "" {
		s.store.SetNote(resourceID+"#model", sess.ApplyID, model)
	}

	// Transition resource back to pending for re-dispatch
	sr, _ := s.store.GetSessionResource(sessionID, resourceID)
	if sr != nil {
		s.store.UpdateSessionResourceState(sessionID, resourceID, "pending", "", "", sr.Attempts, "")
	}

	return nil
}

func (s *Spec) Note(ctx context.Context, sessionID, resourceID, content string) error {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	return s.store.SetNote(resourceID, sess.ApplyID, content)
}

func (s *Spec) Amend(ctx context.Context, sessionID, resourceID string) error {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	// Re-plan to get fresh hashes, registry, and graph
	planResult, planErr := s.Plan(ctx)
	if planErr != nil {
		return fmt.Errorf("re-plan after amend: %w", planErr)
	}

	// 1. Recompute and persist the amended resource's hashes
	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return fmt.Errorf("resource not found in registry: %s", resourceID)
	}

	declData, _ := json.Marshal(resource.Declaration)
	newDeclHash := fmt.Sprintf("%x", sha256.Sum256(declData))
	newEffectiveHash := planResult.Hashes[resourceID]

	if err := s.store.SetResource(store.Resource{
		ID:              resourceID,
		Kind:            resource.Kind,
		ContextName:     resource.ContextName,
		DeclarationHash: newDeclHash,
		EffectiveHash:   newEffectiveHash,
		Model:           s.cfg.GenerateModel,
		SettledAt:       time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("update resource hash: %w", err)
	}

	// 2. Cascade to dependents: mark all downstream resources for regeneration
	//    by invalidating their stored effective hash (set to empty so the planner
	//    detects a mismatch on the next plan).
	dependents := planResult.Graph.Dependents(resourceID)
	for _, depID := range dependents {
		stored, err := s.store.GetResource(depID)
		if err != nil {
			continue // resource not yet generated, nothing to invalidate
		}
		stored.EffectiveHash = "" // force planner to detect mismatch
		stored.SettledAt = time.Now().UTC()
		if err := s.store.SetResource(*stored); err != nil {
			return fmt.Errorf("invalidate dependent %s: %w", depID, err)
		}

		// If the dependent is tracked in the session, reset it to pending
		sr, _ := s.store.GetSessionResource(sessionID, depID)
		if sr != nil && !ResourceState(sr.State).IsTerminal() {
			s.store.UpdateSessionResourceState(sessionID, depID, string(StatePending), "", "", sr.Attempts, "")
		}
	}

	// Also reset the amended resource itself to pending in the session
	sr, _ := s.store.GetSessionResource(sessionID, resourceID)
	if sr != nil {
		s.store.UpdateSessionResourceState(sessionID, resourceID, string(StatePending), "", "", sr.Attempts, "")
	}

	// 3. Record audit trail
	actionID := uuid.NewString()
	s.store.CreateApplyAction(actionID, sess.ApplyID, resourceID, "amend")

	detail := fmt.Sprintf("amended resource; %d dependents invalidated", len(dependents))
	s.store.UpdateApplyAction(actionID, "success", detail)

	return nil
}

func (s *Spec) Skip(ctx context.Context, sessionID, resourceID, reason string) error {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	actionID := uuid.NewString()
	s.store.CreateApplyAction(actionID, sess.ApplyID, resourceID, "skip")
	s.store.UpdateApplyAction(actionID, "skipped", reason)

	// Update session_resources: skipped
	sr, _ := s.store.GetSessionResource(sessionID, resourceID)
	attempts := 0
	if sr != nil {
		attempts = sr.Attempts
	}
	s.store.UpdateSessionResourceState(sessionID, resourceID, "skipped", reason, "", attempts, "")

	if reason != "" {
		s.store.SetNote(resourceID, sess.ApplyID, fmt.Sprintf("SKIPPED: %s", reason))
	}

	return nil
}
