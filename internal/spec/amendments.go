package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/engine"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
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

// ProposeAmendments runs deep_review over the target and asks the LLM to draft
// amendments from the findings. Writes nothing — the result is a proposal for
// human review, fed to ApplyAmendments on approval. Drafting is per-finding
// (one LLM call each) so the finding→amendment mapping stays explicit.
func (s *Spec) ProposeAmendments(ctx context.Context, sessionID, resourceID string) ([]ProposedAmendment, error) {
	review, err := s.DeepReview(ctx, DeepReviewOpts{SessionID: sessionID, Target: resourceID})
	if err != nil {
		return nil, fmt.Errorf("deep review for proposal: %w", err)
	}
	var proposals []ProposedAmendment
	for _, out := range review.Findings {
		for _, f := range out.Findings {
			prompt := promptpkg.RenderProposeAmendments(formatFinding(f))
			res, err := s.engine.CodeReview(ctx, engine.CodeReviewOpts{Prompt: prompt})
			if err != nil {
				continue
			}
			proposals = append(proposals, parseProposedAmendments(res.Output, resourceID)...)
		}
	}
	return proposals, nil
}

// formatFinding renders a single review finding into a compact text block for
// the amendment-proposer prompt.
func formatFinding(f ReviewFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- severity: %s\n", f.Severity)
	if f.File != "" {
		fmt.Fprintf(&b, "  file: %s\n", f.File)
	}
	if f.Line != 0 {
		fmt.Fprintf(&b, "  line: %d\n", f.Line)
	}
	fmt.Fprintf(&b, "  description: %s", f.Description)
	return b.String()
}

// parseProposedAmendments tolerantly decodes the LLM's drafting output (a JSON
// array of amendments, possibly wrapped in prose or ```json fences) into
// ProposedAmendments. The passed resourceID is stamped on each draft, and an
// empty Origin defaults to "deep_review". Parse failure returns nil (no error):
// a bad draft is dropped, not fatal.
func parseProposedAmendments(output, resourceID string) []ProposedAmendment {
	start := strings.Index(output, "[")
	end := strings.LastIndex(output, "]")
	if start < 0 || end <= start {
		return nil
	}
	var raw []struct {
		Name    string          `json:"name"`
		Prompt  string          `json:"prompt"`
		Origin  string          `json:"origin"`
		Finding *cuepkg.Finding `json:"finding"`
	}
	if err := json.Unmarshal([]byte(output[start:end+1]), &raw); err != nil {
		return nil
	}
	var out []ProposedAmendment
	for _, a := range raw {
		origin := a.Origin
		if origin == "" {
			origin = "deep_review"
		}
		out = append(out, ProposedAmendment{
			ResourceID: resourceID,
			Name:       a.Name,
			Prompt:     a.Prompt,
			Origin:     origin,
			Finding:    a.Finding,
		})
	}
	return out
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
