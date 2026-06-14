package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// ProposedAmendment is a draft amendment (not yet written to the spec).
type ProposedAmendment struct {
	ResourceID string          `json:"resource_id"`
	Name       string          `json:"name"`
	Prompt     string          `json:"prompt"`
	Origin     string          `json:"origin"`
	Finding    *cuepkg.Finding `json:"finding,omitempty"`
}

// AmendmentApplyResult is the outcome of a (preview or applied) write-back.
type AmendmentApplyResult struct {
	OverridePath string `json:"override_path"`
	Diff         string `json:"diff"`
	Applied      bool   `json:"applied"`
	Count        int    `json:"count"`
}

// ApplyAmendments writes approved amendments for a resource into a CUE override
// file. Human-gated: apply=false returns the rendered override for review and
// mutates nothing; apply=true writes the file and materializes PENDING rows.
func (s *Spec) ApplyAmendments(ctx context.Context, resourceID string, proposals []ProposedAmendment, apply bool) (*AmendmentApplyResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan for apply_amendments: %w", err)
	}
	r, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}
	// Merge existing amendments + approved proposals, keyed by name (proposal wins).
	merged := map[string]amendmentEntry{}
	for _, a := range cuepkg.ResourceAmendments(r) {
		merged[a.Name] = toEntry(a)
	}
	for _, p := range proposals {
		merged[p.Name] = amendmentEntry{Name: p.Name, Prompt: p.Prompt, Origin: p.Origin, Finding: toFindingEntry(p.Finding)}
	}
	entries := make([]amendmentEntry, 0, len(merged))
	for _, e := range merged {
		entries = append(entries, e)
	}
	sortEntriesByName(entries)

	pkg := s.cuePackageName()
	shortName := resourceShortName(resourceID)
	overridePath := s.amendmentOverridePath(shortName)
	body := renderAmendmentOverride(pkg, shortName, r.Kind, r.ContextName, entries)

	result := &AmendmentApplyResult{OverridePath: overridePath, Diff: body, Count: len(proposals)}
	if !apply {
		return result, nil
	}
	if err := s.fs.WriteFile(overridePath, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("write override: %w", err)
	}
	for _, p := range proposals {
		findingJSON := ""
		if p.Finding != nil {
			b, _ := json.Marshal(p.Finding)
			findingJSON = string(b)
		}
		if err := s.store.UpsertAmendment(store.Amendment{
			ID:          resourceID + "::" + p.Name,
			ResourceID:  resourceID,
			Name:        p.Name,
			ContentHash: amendmentContentHash(cuepkg.Amendment{Name: p.Name, Prompt: p.Prompt, Finding: p.Finding}),
			Origin:      p.Origin,
			Prompt:      p.Prompt,
			FindingJSON: findingJSON,
			State:       "PENDING",
			CreatedAt:   time.Now().UTC(),
		}); err != nil {
			return nil, fmt.Errorf("upsert amendment %s/%s: %w", resourceID, p.Name, err)
		}
	}
	result.Applied = true
	return result, nil
}

// ListAmendments returns materialized amendments, optionally filtered by
// resource and/or state (empty string = no filter on that dimension).
func (s *Spec) ListAmendments(ctx context.Context, resourceID, state string) ([]store.Amendment, error) {
	var rows []store.Amendment
	var err error
	switch {
	case resourceID != "":
		rows, err = s.store.ListAmendmentsByResource(resourceID)
	case state != "":
		return s.store.ListAmendmentsByState(state)
	default:
		rows, err = s.store.ListAllAmendments()
	}
	if err != nil {
		return nil, err
	}
	if state == "" {
		return rows, nil
	}
	out := make([]store.Amendment, 0, len(rows))
	for _, r := range rows {
		if r.State == state {
			out = append(out, r)
		}
	}
	return out, nil
}

// GraduationResult is the outcome of a (preview or applied) graduation.
type GraduationResult struct {
	OverridePath string `json:"override_path"`
	Diff         string `json:"diff"`
	Applied      bool   `json:"applied"`
}

// GraduateAmendment folds a VERIFIED amendment's intent into the resource's
// canonical invariants and removes the amendment. Human-gated: apply=false
// previews the CUE diff; apply=true writes it and deletes the amendment row.
func (s *Spec) GraduateAmendment(ctx context.Context, resourceID, name string, apply bool) (*GraduationResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan for graduate: %w", err)
	}
	r, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}
	target, err := s.store.GetAmendment(resourceID, name)
	if err != nil || target == nil {
		return nil, fmt.Errorf("amendment not found: %s/%s", resourceID, name)
	}
	if target.State != "VERIFIED" {
		return nil, fmt.Errorf("amendment %s is %s, not VERIFIED; cannot graduate", name, target.State)
	}
	// Rebuild amendments WITHOUT the graduated one; fold its prompt into invariants.
	var remaining []amendmentEntry
	for _, a := range cuepkg.ResourceAmendments(r) {
		if a.Name == name {
			continue
		}
		remaining = append(remaining, toEntry(a))
	}
	sortEntriesByName(remaining)
	invariants := append(existingInvariants(r), "graduated: "+target.Prompt)
	pkg := s.cuePackageName()
	shortName := resourceShortName(resourceID)
	overridePath := s.amendmentOverridePath(shortName)
	body := renderGraduationOverride(pkg, shortName, r.Kind, r.ContextName, invariants, remaining)

	result := &GraduationResult{OverridePath: overridePath, Diff: body}
	if !apply {
		return result, nil
	}
	if err := s.fs.WriteFile(overridePath, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("write graduation override: %w", err)
	}
	if err := s.store.DeleteAmendment(resourceID, name); err != nil {
		return nil, fmt.Errorf("delete graduated amendment: %w", err)
	}
	result.Applied = true
	return result, nil
}

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
			if prior, _ := s.store.GetAmendment(id, a.Name); prior != nil {
				if prior.State == "VERIFIED" && committed {
					state = "VERIFIED"
				}
				if prior.State == "FAILED" && !committed {
					state = "FAILED"
				}
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

// markAmendmentVerification updates VERIFIED/FAILED state for a resource's
// amendments that declare a validation, after a commit's validation gate ran.
// applied_spec_hash/applied_at are preserved from the existing row.
func (s *Spec) markAmendmentVerification(resourceID string, resource cuepkg.Resource, passed bool) {
	state := "VERIFIED"
	if !passed {
		state = "FAILED"
	}
	for _, a := range cuepkg.ResourceAmendments(resource) {
		if a.Validation == nil {
			continue
		}
		existing, _ := s.store.GetAmendment(resourceID, a.Name)
		appliedHash := ""
		var appliedAt, gradAt time.Time
		if existing != nil {
			appliedHash = existing.AppliedSpecHash
			appliedAt = existing.AppliedAt
			gradAt = existing.GraduatedAt
		}
		id := resourceID + "::" + a.Name
		_ = s.store.UpdateAmendmentState(id, state, appliedHash, appliedAt, gradAt)
	}
}

// pendingAmendmentChanges renders the prompts of all PENDING/FAILED amendments
// for a resource into a single "changes to make" block. Empty when none.
func (s *Spec) pendingAmendmentChanges(resourceID string) string {
	ams, err := s.store.ListAmendmentsByResource(resourceID)
	if err != nil || len(ams) == 0 {
		return ""
	}
	var b strings.Builder
	for _, a := range ams {
		if a.State != "PENDING" && a.State != "FAILED" {
			continue
		}
		fmt.Fprintf(&b, "- **%s**: %s\n", a.Name, a.Prompt)
	}
	return strings.TrimRight(b.String(), "\n")
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
