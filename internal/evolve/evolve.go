// Package evolve implements the reflection engine for the evolution pillar.
//
// Reflection reads a run's failure history (rejected generations, failed
// invariant checks, per-resource last errors) and asks an LLM to distill
// CRAFT-LEVEL learnings — guidance that generalizes across all resources of a
// (language, kind), not a fix for one specific resource. Extracted learnings
// are persisted to the store and later injected into future generation prompts.
//
// SAFETY CONTRACT: reflection must NEVER fail a run. Every extraction, LLM, or
// parse failure is swallowed (logged) and the engine returns (0, nil). A wave
// or session must complete regardless of what reflection does.
package evolve

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/store"
)

// Marker sentinels wrap the extraction JSON so it can be located unambiguously
// amid model prose. This mirrors the hardened pattern from the constraint loop
// (internal/spec/loop.go) — deterministic parse, no keyword heuristics.
const (
	learningsBegin = "===CREST_LEARNINGS_BEGIN==="
	learningsEnd   = "===CREST_LEARNINGS_END==="
)

// Store is the narrow persistence abstraction the reflector depends on. It
// names only the read+write methods reflection needs — nothing more (Interface
// Segregation). The concrete *store.Store satisfies it.
type Store interface {
	// ListSessionResources returns every resource state in a session.
	ListSessionResources(sessionID string) ([]store.SessionResource, error)
	// GetResource resolves a resource's kind (and other metadata) by ID.
	GetResource(id string) (*store.Resource, error)
	// ListGenerations returns recent generations for a resource.
	ListGenerations(resourceID string, limit int) ([]store.Generation, error)
	// ListInvariantChecks returns invariant checks for an apply.
	ListInvariantChecks(applyID string) ([]store.InvariantCheck, error)
	// ListLearnings returns learnings with the given status (used to dedupe).
	ListLearnings(status string) ([]store.Learning, error)
	// CreateLearning persists a new learning.
	CreateLearning(l store.Learning) error
}

// Reflector distills craft-level learnings from a run's failure history.
//
// It holds its collaborators via interfaces injected through the constructor;
// it never instantiates its own dependencies.
type Reflector struct {
	st Store
}

// New builds a Reflector over the given store. The server never calls an LLM
// itself: BuildSessionPrompt emits the extraction prompt for the orchestrator
// to run, and Record ingests the orchestrator's output.
func New(st Store) *Reflector {
	return &Reflector{st: st}
}

// BuildSessionPrompt gathers the session's failure signal and returns the
// extraction prompt the orchestrator should run against an LLM. Returns "" when
// the session has no failure signal (nothing to reflect on). Failures gathering
// signal are swallowed and yield "" — reflection must never fail a run.
func (r *Reflector) BuildSessionPrompt(sessionID, applyID string) (string, error) {
	resources, err := r.st.ListSessionResources(sessionID)
	if err != nil {
		log.Printf("evolve: BuildSessionPrompt list resources failed (swallowed): %v", err)
		return "", nil
	}
	signal := r.gatherSignal(applyID, resources)
	if len(signal) == 0 {
		return "", nil
	}
	return buildExtractionPrompt(signal, r.loadExisting()), nil
}

// Record parses the orchestrator's reflection output (the marker block) and
// persists deduped learnings, tagging each with the source applyID for
// provenance. Returns the count added. Parse failures yield 0.
func (r *Reflector) Record(output, applyID string) (int, error) {
	parsed := parseLearnings(output)
	if len(parsed) == 0 {
		return 0, nil
	}
	return r.persist(parsed, r.loadExisting(), applyID), nil
}

// resourceSignal is the failure evidence gathered for one resource, used both
// to build the extraction prompt and to attach scope (language, kind).
type resourceSignal struct {
	ResourceID string
	Kind       string
	LastError  string
	Rejections []string // rejection_reason from rejected generations
	Failures   []string // details from failed invariant checks
}

// gatherSignal collects the failure evidence for the given resources: rejected
// generations (with rejection_reason), failed invariant checks (with details),
// and per-resource last_error. Resources with no failure signal are skipped.
func (r *Reflector) gatherSignal(applyID string, resources []store.SessionResource) []resourceSignal {
	// Failed invariant checks are keyed per resource for the apply.
	invByResource := map[string][]string{}
	if applyID != "" {
		checks, err := r.st.ListInvariantChecks(applyID)
		if err != nil {
			log.Printf("evolve: list invariant checks failed (swallowed): %v", err)
		} else {
			for _, c := range checks {
				if !c.Passed {
					detail := c.CheckType
					if c.Output != "" {
						detail += ": " + c.Output
					}
					invByResource[c.ResourceID] = append(invByResource[c.ResourceID], detail)
				}
			}
		}
	}

	var out []resourceSignal
	for _, res := range resources {
		sig := resourceSignal{
			ResourceID: res.ResourceID,
			LastError:  res.LastError,
			Failures:   invByResource[res.ResourceID],
		}

		if rsrc, err := r.st.GetResource(res.ResourceID); err == nil && rsrc != nil {
			sig.Kind = rsrc.Kind
		}

		gens, err := r.st.ListGenerations(res.ResourceID, 20)
		if err != nil {
			log.Printf("evolve: list generations failed (swallowed): %v", err)
		} else {
			for _, g := range gens {
				if g.RejectionReason != "" {
					sig.Rejections = append(sig.Rejections, g.RejectionReason)
				}
			}
		}

		if len(sig.Rejections) == 0 && len(sig.Failures) == 0 && sig.LastError == "" {
			continue // no failure signal for this resource
		}
		out = append(out, sig)
	}
	return out
}

// loadExisting returns active learnings so the LLM (and our dedupe) can avoid
// re-emitting guidance we already hold. Failures are swallowed.
func (r *Reflector) loadExisting() []store.Learning {
	existing, err := r.st.ListLearnings("active")
	if err != nil {
		log.Printf("evolve: list existing learnings failed (swallowed): %v", err)
		return nil
	}
	return existing
}

// extractedLearning is the per-learning JSON shape the model emits between the
// markers.
type extractedLearning struct {
	ScopeKind  string  `json:"scope_kind"`
	ScopeLang  string  `json:"scope_lang"`
	Text       string  `json:"text"`
	Rationale  string  `json:"rationale"`
	Confidence float64 `json:"confidence"`
}

// persist turns parsed learnings into store.Learning rows, skipping
// near-duplicates of existing active learnings, and returns the count created.
func (r *Reflector) persist(parsed []extractedLearning, existing []store.Learning, applyID string) int {
	seen := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		seen[normalizeText(e.Text)] = struct{}{}
	}

	added := 0
	for _, p := range parsed {
		text := strings.TrimSpace(p.Text)
		if text == "" {
			continue
		}
		key := normalizeText(text)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		conf := p.Confidence
		if conf <= 0 || conf > 1 {
			conf = 0.5
		}

		now := time.Now()
		l := store.Learning{
			ID:            uuid.NewString(),
			ScopeLang:     strings.TrimSpace(p.ScopeLang),
			ScopeKind:     strings.TrimSpace(p.ScopeKind),
			Text:          text,
			Rationale:     strings.TrimSpace(p.Rationale),
			SourceApplyID: applyID,
			Confidence:    conf,
			Status:        "active",
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := r.st.CreateLearning(l); err != nil {
			log.Printf("evolve: CreateLearning failed (swallowed): %v", err)
			continue
		}
		added++
	}
	return added
}

// normalizeText lowercases and collapses whitespace so exact and trivially
// reformatted duplicates are caught by the dedupe map.
func normalizeText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// parseLearnings extracts the learnings JSON from the marker block. It tolerates
// surrounding prose and accepts either a bare array or an object with a
// "learnings" array. Returns nil when nothing parses.
func parseLearnings(output string) []extractedLearning {
	block := extractMarkerBlock(output)
	if block == "" {
		return nil
	}
	block = strings.TrimSpace(block)

	// Try a bare array first.
	var arr []extractedLearning
	if err := json.Unmarshal([]byte(block), &arr); err == nil {
		return arr
	}
	// Try an object wrapper {"learnings": [...]}.
	var wrapper struct {
		Learnings []extractedLearning `json:"learnings"`
	}
	if err := json.Unmarshal([]byte(block), &wrapper); err == nil && wrapper.Learnings != nil {
		return wrapper.Learnings
	}

	// Last resort: locate the outermost array within the block.
	start := strings.Index(block, "[")
	end := strings.LastIndex(block, "]")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(block[start:end+1]), &arr); err == nil {
			return arr
		}
	}
	return nil
}

// extractMarkerBlock returns the text between the first BEGIN/END marker pair.
// An unterminated block (END missing) yields the remainder so a truncated reply
// is still recoverable.
func extractMarkerBlock(output string) string {
	i := strings.Index(output, learningsBegin)
	if i < 0 {
		return ""
	}
	rest := output[i+len(learningsBegin):]
	if j := strings.Index(rest, learningsEnd); j >= 0 {
		return rest[:j]
	}
	return rest
}
