package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// amendmentContentHash is the stable identity of an amendment: hash of the
// fields a human authored (name, prompt, finding).
func amendmentContentHash(a cuepkg.Amendment) string {
	payload, _ := json.Marshal(struct {
		Name    string          `json:"name"`
		Prompt  string          `json:"prompt"`
		Finding *cuepkg.Finding `json:"finding"`
	}{a.Name, a.Prompt, a.Finding})
	return fmt.Sprintf("%x", sha256.Sum256(payload))
}

// ReconcileAmendments rewrites the materialized amendments table from the CUE
// source of truth. State is DERIVED: APPLIED iff the resource's stored
// declaration hash equals the current declaration hash; otherwise PENDING.
// GRADUATED if the amendment is marked graduated. A prior VERIFIED state is
// preserved while the amendment is still present and committed. The table is a
// cache — this is the only writer of derived state during plan/begin.
func (s *Spec) ReconcileAmendments(ctx context.Context) error {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return fmt.Errorf("plan for amendment reconcile: %w", err)
	}
	for id, r := range planResult.Registry.Resources {
		ams := cuepkg.ResourceAmendments(r)
		if len(ams) == 0 {
			continue
		}
		declData, _ := json.Marshal(r.Declaration)
		currentDeclHash := fmt.Sprintf("%x", sha256.Sum256(declData))

		stored, _ := s.store.GetResource(id)
		committed := stored != nil && stored.DeclarationHash == currentDeclHash

		for _, a := range ams {
			findingJSON := ""
			if a.Finding != nil {
				b, _ := json.Marshal(a.Finding)
				findingJSON = string(b)
			}
			validationJSON := ""
			if a.Validation != nil {
				b, _ := json.Marshal(a.Validation)
				validationJSON = string(b)
			}
			state := "PENDING"
			appliedHash := ""
			appliedAt := time.Time{}
			if committed {
				state = "APPLIED"
				appliedHash = currentDeclHash
				appliedAt = time.Now().UTC()
			}
			if a.Graduated {
				state = "GRADUATED"
			}
			if prior, _ := s.store.GetAmendment(id, a.Name); prior != nil &&
				prior.State == "VERIFIED" && committed {
				state = "VERIFIED"
			}
			if err := s.store.UpsertAmendment(store.Amendment{
				ID:              id + "::" + a.Name,
				ResourceID:      id,
				Name:            a.Name,
				ContentHash:     amendmentContentHash(a),
				Origin:          a.Origin,
				Prompt:          a.Prompt,
				FindingJSON:     findingJSON,
				ValidationJSON:  validationJSON,
				State:           state,
				AppliedSpecHash: appliedHash,
				CreatedAt:       parseAmendmentCreatedAt(a.CreatedAt),
				AppliedAt:       appliedAt,
			}); err != nil {
				return fmt.Errorf("upsert amendment %s/%s: %w", id, a.Name, err)
			}
		}
	}
	return nil
}

func parseAmendmentCreatedAt(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}
