package spec

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/crestenstclair/crest-spec/internal/store"
)

type StatusResult struct {
	Resources  []store.Resource
	ActiveLock *store.Lock
	Session    *store.Session
}

func (s *Spec) Status(ctx context.Context) (*StatusResult, error) {
	resources, err := s.store.ListResources()
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	lock, _ := s.store.GetLock()
	session, _ := s.store.GetActiveSession()

	return &StatusResult{
		Resources:  resources,
		ActiveLock: lock,
		Session:    session,
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

func (s *Spec) DriftAction(ctx context.Context, action, resourceID string) error {
	switch action {
	case "accept":
		files, err := s.store.GetGeneratedFiles(resourceID)
		if err != nil {
			return fmt.Errorf("get files: %w", err)
		}
		for _, f := range files {
			data, err := s.fs.ReadFile(f.Path)
			if err != nil {
				continue
			}
			contentHash := fmt.Sprintf("%x", sha256.Sum256(data))
			s.store.SetGeneratedFile(store.GeneratedFile{
				Path:        f.Path,
				ResourceID:  f.ResourceID,
				ContentHash: contentHash,
				PromptHash:  f.PromptHash,
				Model:       f.Model,
				CreatedAt:   f.CreatedAt,
			})
		}
		return nil

	case "revert":
		return fmt.Errorf("revert not yet implemented: need stored file content")

	default:
		return fmt.Errorf("unknown drift action: %s (expected 'accept' or 'revert')", action)
	}
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
