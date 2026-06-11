package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type StatusResult struct {
	Resources  []store.Resource
	ActiveLock *store.Lock
	Session    *store.Session
	Mode       string
}

func (s *Spec) Status(ctx context.Context) (*StatusResult, error) {
	resources, err := s.store.ListResources()
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	lock, _ := s.store.GetLock()
	session, _ := s.store.GetActiveSession()

	mode := s.cfg.Mode

	return &StatusResult{
		Resources:  resources,
		ActiveLock: lock,
		Session:    session,
		Mode:       mode,
	}, nil
}

func (s *Spec) Log(ctx context.Context, limit int) ([]store.Apply, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.store.ListApplies(limit)
}

func (s *Spec) History(ctx context.Context, resourceID string, limit int) ([]store.Generation, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.store.ListGenerations(resourceID, limit)
}

type GraphResult struct {
	Nodes []string
	Waves [][]string
}

func (s *Spec) GraphInfo(ctx context.Context) (*GraphResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, err
	}

	topo, err := planResult.Graph.TopologicalSort()
	if err != nil {
		return nil, err
	}

	return &GraphResult{
		Nodes: topo,
		Waves: planResult.Waves,
	}, nil
}

func (s *Spec) Unlock(ctx context.Context) error {
	return s.store.ReleaseLock()
}

func (s *Spec) Validate(ctx context.Context) (*ValidateResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, err
	}

	return &ValidateResult{
		Valid:         true,
		ResourceCount: len(planResult.Registry.Resources),
	}, nil
}

type ValidateResult struct {
	Valid         bool
	ResourceCount int
	Errors        []string
}

func (s *Spec) ValidateResource(ctx context.Context, resourceID string) (*ValidateResourceResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, err
	}

	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}

	var results []ValidationResult
	var runErr error
	if len(resource.Validations) > 0 {
		cwd := "."
		results, runErr = RunValidations(ctx, resource.Validations, cwd)
		if runErr != nil {
			return nil, runErr
		}
	}

	return &ValidateResourceResult{
		ResourceID:  resourceID,
		Validations: results,
	}, nil
}

type ValidateResourceResult struct {
	ResourceID  string
	Validations []ValidationResult
}

// ---------------------------------------------------------------------------
// Inspect — full debug view of a resource
// ---------------------------------------------------------------------------

type InspectResult struct {
	ResourceID      string              `json:"resource_id"`
	Kind            string              `json:"kind"`
	ContextName     string              `json:"context_name"`
	DeclarationHash string              `json:"declaration_hash"`
	EffectiveHash   string              `json:"effective_hash"`
	StoredHash      string              `json:"stored_hash,omitempty"`
	HashChanged     bool                `json:"hash_changed"`
	Dependencies    []InspectDep        `json:"dependencies,omitempty"`
	GeneratedFiles  []store.GeneratedFile `json:"generated_files,omitempty"`
	SystemPrompt    string              `json:"system_prompt"`
	ResourcePrompt  string              `json:"resource_prompt"`
	Wave            int                 `json:"wave"`
}

type InspectDep struct {
	TargetID string `json:"target_id"`
	Kind     string `json:"kind"`
}

func (s *Spec) Inspect(ctx context.Context, resourceID string) (*InspectResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}

	declBytes, _ := json.Marshal(resource.Declaration)
	declHash := fmt.Sprintf("%x", sha256.Sum256(declBytes))
	effHash := planResult.Hashes[resourceID]

	var storedHash string
	var hashChanged bool
	stored, err := s.store.GetResource(resourceID)
	if err == nil && stored != nil {
		storedHash = stored.EffectiveHash
		hashChanged = storedHash != effHash
	} else {
		hashChanged = true
	}

	var deps []InspectDep
	for _, d := range resource.Dependencies {
		deps = append(deps, InspectDep{TargetID: d.TargetID, Kind: d.Kind})
	}

	files, _ := s.store.GetGeneratedFiles(resourceID)

	systemPrompt := promptpkg.BuildSystemPrompt(planResult.Registry.Project)
	resourcePrompt := promptpkg.BuildResourcePrompt(resource, planResult.Registry)

	wave := -1
	for i, w := range planResult.Waves {
		for _, id := range w {
			if id == resourceID {
				wave = i
				break
			}
		}
	}

	return &InspectResult{
		ResourceID:      resourceID,
		Kind:            resource.Kind,
		ContextName:     resource.ContextName,
		DeclarationHash: declHash,
		EffectiveHash:   effHash,
		StoredHash:      storedHash,
		HashChanged:     hashChanged,
		Dependencies:    deps,
		GeneratedFiles:  files,
		SystemPrompt:    systemPrompt,
		ResourcePrompt:  resourcePrompt,
		Wave:            wave,
	}, nil
}

// ---------------------------------------------------------------------------
// DiffApplies — compare two applies
// ---------------------------------------------------------------------------

// DiffResult holds the delta between two applies.
type DiffResult struct {
	ApplyIDA string              `json:"apply_id_a"`
	ApplyIDB string              `json:"apply_id_b"`
	OnlyInA  []DiffAction        `json:"only_in_a"`
	OnlyInB  []DiffAction        `json:"only_in_b"`
	Changed  []DiffResourceDelta `json:"changed"`
}

// DiffAction describes a single apply action entry.
type DiffAction struct {
	ResourceID string `json:"resource_id"`
	Action     string `json:"action"`
	Outcome    string `json:"outcome"`
}

// DiffResourceDelta describes a resource that appears in both applies but
// with different actions or outcomes.
type DiffResourceDelta struct {
	ResourceID string `json:"resource_id"`
	ActionA    string `json:"action_a"`
	OutcomeA   string `json:"outcome_a"`
	ActionB    string `json:"action_b"`
	OutcomeB   string `json:"outcome_b"`
}

// DiffApplies compares the actions taken in two apply runs and returns a diff.
func (s *Spec) DiffApplies(ctx context.Context, applyIDA, applyIDB string) (*DiffResult, error) {
	actionsA, err := s.store.ListApplyActions(applyIDA)
	if err != nil {
		return nil, fmt.Errorf("list actions for apply %s: %w", applyIDA, err)
	}
	actionsB, err := s.store.ListApplyActions(applyIDB)
	if err != nil {
		return nil, fmt.Errorf("list actions for apply %s: %w", applyIDB, err)
	}

	mapA := make(map[string]store.ApplyAction, len(actionsA))
	for _, a := range actionsA {
		mapA[a.ResourceID] = a
	}
	mapB := make(map[string]store.ApplyAction, len(actionsB))
	for _, a := range actionsB {
		mapB[a.ResourceID] = a
	}

	result := &DiffResult{
		ApplyIDA: applyIDA,
		ApplyIDB: applyIDB,
	}

	for _, a := range actionsA {
		b, inB := mapB[a.ResourceID]
		if !inB {
			result.OnlyInA = append(result.OnlyInA, DiffAction{
				ResourceID: a.ResourceID,
				Action:     a.Action,
				Outcome:    a.Outcome,
			})
		} else if a.Action != b.Action || a.Outcome != b.Outcome {
			result.Changed = append(result.Changed, DiffResourceDelta{
				ResourceID: a.ResourceID,
				ActionA:    a.Action,
				OutcomeA:   a.Outcome,
				ActionB:    b.Action,
				OutcomeB:   b.Outcome,
			})
		}
	}

	for _, b := range actionsB {
		if _, inA := mapA[b.ResourceID]; !inA {
			result.OnlyInB = append(result.OnlyInB, DiffAction{
				ResourceID: b.ResourceID,
				Action:     b.Action,
				Outcome:    b.Outcome,
			})
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Vacuum — delete old history
// ---------------------------------------------------------------------------

// Vacuum deletes old generations, apply_actions, and applies older than the
// given threshold. Returns the total count of deleted records.
func (s *Spec) Vacuum(ctx context.Context, before time.Time) (int, error) {
	return s.store.Vacuum(before)
}

// ---------------------------------------------------------------------------
// ReadOnlyQuery — execute arbitrary SELECT
// ---------------------------------------------------------------------------

// ReadOnlyQuery executes a read-only SQL query and returns the results as
// a slice of column-to-value maps.
func (s *Spec) ReadOnlyQuery(ctx context.Context, query string) ([]map[string]interface{}, error) {
	return s.store.ReadOnlyQuery(query)
}

// ---------------------------------------------------------------------------
// RemoveResource — delete a resource and its related data
// ---------------------------------------------------------------------------

// RemoveResource deletes a resource from state along with its generated files
// and dependency edges.
func (s *Spec) RemoveResource(ctx context.Context, resourceID string) error {
	if err := s.store.DeleteGeneratedFiles(resourceID); err != nil {
		return fmt.Errorf("delete generated files for %s: %w", resourceID, err)
	}
	if err := s.store.DeleteDependencies(resourceID); err != nil {
		return fmt.Errorf("delete dependencies for %s: %w", resourceID, err)
	}
	if err := s.store.DeleteResource(resourceID); err != nil {
		return fmt.Errorf("delete resource %s: %w", resourceID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Prompt — build and return the prompt WITHOUT dispatching
// ---------------------------------------------------------------------------

type PromptResult struct {
	ResourceID   string `json:"resource_id"`
	SystemPrompt string `json:"system_prompt"`
	Prompt       string `json:"prompt"`
}

func (s *Spec) Prompt(ctx context.Context, resourceID string) (*PromptResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}

	systemPrompt := promptpkg.BuildSystemPrompt(planResult.Registry.Project)
	resourcePrompt := promptpkg.BuildResourcePrompt(resource, planResult.Registry)

	return &PromptResult{
		ResourceID:   resourceID,
		SystemPrompt: systemPrompt,
		Prompt:       resourcePrompt,
	}, nil
}
