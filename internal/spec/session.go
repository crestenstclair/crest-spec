package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type BeginOpts struct {
	Target string
	Force  bool
	Model  string
}

type BeginResult struct {
	SessionID    string
	ApplyID      string
	Plan         []planpkg.PlannedAction
	Waves        [][]string
	Instructions string
	DriftActions []planpkg.PlannedAction
}

type NextResult struct {
	Done      bool
	WaveIndex int
	Resources []ResourceStatus
}

type ContextResult struct {
	SystemPrompt    string
	Prompt          string
	DependencyNotes map[string]string
	Instructions    string
}

type CommitFile struct {
	Path    string
	Content string
}

type FinishResult struct {
	Committed int
	Skipped   int
	Errored   int
}

func (s *Spec) Begin(ctx context.Context, opts BeginOpts) (*BeginResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	if len(planResult.Actions) == 0 {
		return &BeginResult{
			Instructions: "No changes detected. The spec is up to date.",
		}, nil
	}

	applyID := uuid.NewString()
	sessionID := uuid.NewString()

	specJSON, _ := json.Marshal(planResult.Actions)
	specHash := fmt.Sprintf("%x", sha256.Sum256(specJSON))

	if err := s.store.CreateApply(applyID, specHash); err != nil {
		return nil, fmt.Errorf("create apply: %w", err)
	}

	if err := s.store.AcquireLock(sessionID, os.Getpid()); err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	planJSON, _ := json.Marshal(planResult.Actions)
	wavesJSON, _ := json.Marshal(planResult.Waves)
	hashesJSON, _ := json.Marshal(planResult.Hashes)

	if err := s.store.CreateSession(store.Session{
		ID:         sessionID,
		PlanJSON:   string(planJSON),
		WavesJSON:  string(wavesJSON),
		HashesJSON: string(hashesJSON),
	}); err != nil {
		s.store.ReleaseLock()
		return nil, fmt.Errorf("create session: %w", err)
	}

	var driftActions []planpkg.PlannedAction
	var otherActions []planpkg.PlannedAction
	for _, a := range planResult.Actions {
		if a.Kind == planpkg.ActionDrift {
			driftActions = append(driftActions, a)
		} else {
			otherActions = append(otherActions, a)
		}
	}

	instructions := `You are a dispatcher, not a code generator. Do not write code yourself.
For each resource: call spec/context to get its prompt, then call run_prompt with that prompt (using --disallowedTools for constrained output), parse the output, write files, call spec/note with design decisions, call spec/commit.
Use poll_result to collect run_prompt results (they're async).
Resources within the same wave can be dispatched in parallel (multiple run_prompt calls).
Waves must be processed sequentially.`

	return &BeginResult{
		SessionID:    sessionID,
		ApplyID:      applyID,
		Plan:         otherActions,
		Waves:        planResult.Waves,
		Instructions: instructions,
		DriftActions: driftActions,
	}, nil
}

func (s *Spec) Next(ctx context.Context, sessionID string) (*NextResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var waves [][]string
	if err := json.Unmarshal([]byte(sess.WavesJSON), &waves); err != nil {
		return nil, fmt.Errorf("unmarshal waves: %w", err)
	}

	if sess.CurrentWave >= len(waves) {
		return &NextResult{Done: true, WaveIndex: sess.CurrentWave}, nil
	}

	wave := waves[sess.CurrentWave]

	var plan []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(sess.PlanJSON), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}

	planSet := make(map[string]bool)
	for _, a := range plan {
		planSet[a.ResourceID] = true
	}

	var resources []ResourceStatus
	for _, id := range wave {
		if !planSet[id] {
			continue
		}
		resources = append(resources, ResourceStatus{
			ResourceID: id,
			State:      StatePending,
			WaveIndex:  sess.CurrentWave,
			MaxRetries: s.cfg.MaxRetries,
		})
	}

	if len(resources) == 0 {
		if err := s.store.UpdateSession(sessionID, sess.Status, sess.CurrentWave+1); err != nil {
			return nil, fmt.Errorf("advance wave: %w", err)
		}
		return s.Next(ctx, sessionID)
	}

	return &NextResult{
		Done:      false,
		WaveIndex: sess.CurrentWave,
		Resources: resources,
	}, nil
}

func (s *Spec) Context(ctx context.Context, sessionID, resourceID string) (*ContextResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	// We need the plan to exist, just to validate the session
	var plan []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(sess.PlanJSON), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}

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

	runtimeCtx, _ := s.buildRuntimeContext(resource, planResult.Registry, "")
	fullPrompt := promptpkg.InjectRuntimeContext(resourcePrompt, runtimeCtx)

	var depNotes map[string]string
	if len(resource.Dependencies) > 0 {
		depNotes = make(map[string]string)
		for _, dep := range resource.Dependencies {
			content, err := s.store.GetNote(dep.TargetID, "")
			if err == nil && content != "" {
				depNotes[dep.TargetID] = content
			}
		}
	}

	return &ContextResult{
		SystemPrompt:    systemPrompt,
		Prompt:          fullPrompt,
		DependencyNotes: depNotes,
		Instructions:    fmt.Sprintf("Generate code for resource %s. Use --disallowedTools to prevent tool access. Return pure code blocks with // path: annotations.", resourceID),
	}, nil
}

func (s *Spec) Commit(ctx context.Context, sessionID, resourceID string, files []CommitFile, notes string) error {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	for _, f := range files {
		dir := filepath.Dir(f.Path)
		if err := s.fs.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}

		contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(f.Content)))

		existing, readErr := s.fs.ReadFile(f.Path)
		if readErr == nil {
			existingHash := fmt.Sprintf("%x", sha256.Sum256(existing))
			if existingHash == contentHash {
				continue
			}
		}

		if err := s.fs.WriteFile(f.Path, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	}

	var plan []planpkg.PlannedAction
	json.Unmarshal([]byte(sess.PlanJSON), &plan)

	var hashes map[string]string
	json.Unmarshal([]byte(sess.HashesJSON), &hashes)

	planResult, err := s.Plan(ctx)
	if err != nil {
		return fmt.Errorf("plan for commit: %w", err)
	}

	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return fmt.Errorf("resource not found: %s", resourceID)
	}

	declHash := fmt.Sprintf("%x", sha256.Sum256(func() []byte { b, _ := json.Marshal(resource.Declaration); return b }()))

	if err := s.store.SetResource(store.Resource{
		ID:              resourceID,
		Kind:            resource.Kind,
		ContextName:     resource.ContextName,
		DeclarationHash: declHash,
		EffectiveHash:   hashes[resourceID],
		Model:           s.cfg.GenerateModel,
		SettledAt:       time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("set resource: %w", err)
	}

	s.store.DeleteGeneratedFiles(resourceID)
	for _, f := range files {
		contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(f.Content)))
		s.store.SetGeneratedFile(store.GeneratedFile{
			Path:        f.Path,
			ResourceID:  resourceID,
			ContentHash: contentHash,
			Model:       s.cfg.GenerateModel,
			CreatedAt:   time.Now().UTC(),
		})
	}

	s.store.DeleteDependencies(resourceID)
	for _, dep := range resource.Dependencies {
		s.store.SetDependency(resourceID, dep.TargetID, dep.Kind)
	}

	if notes != "" {
		s.store.SetNote(resourceID, sessionID, notes)
	}

	return nil
}

func (s *Spec) Finish(ctx context.Context, sessionID string, force bool) (*FinishResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	if err := s.store.UpdateSession(sessionID, "completed", sess.CurrentWave); err != nil {
		return nil, fmt.Errorf("update session: %w", err)
	}

	if err := s.store.ReleaseLock(); err != nil {
		return nil, fmt.Errorf("release lock: %w", err)
	}

	return &FinishResult{}, nil
}

func (s *Spec) AdvanceWave(ctx context.Context, sessionID string) error {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	return s.store.UpdateSession(sessionID, sess.Status, sess.CurrentWave+1)
}
