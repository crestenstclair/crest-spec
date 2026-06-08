package spec

import (
	"context"
	"time"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/engine"
	graphpkg "github.com/crestenstclair/crest-spec/internal/graph"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type specEngine interface {
	Generate(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error)
	Review(ctx context.Context, opts engine.ReviewOpts) (*agent.RunResult, error)
	CodeReview(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error)
	Bugbot(ctx context.Context, opts engine.BugbotOpts) (*agent.RunResult, error)
	ActiveCount() int
	MaxConcurrency() int
}

type specStore interface {
	GetResource(id string) (*store.Resource, error)
	ListResources() ([]store.Resource, error)
	SetResource(r store.Resource) error
	DeleteResource(id string) error
	GetGeneratedFiles(resourceID string) ([]store.GeneratedFile, error)
	SetGeneratedFile(f store.GeneratedFile) error
	DeleteGeneratedFiles(resourceID string) error
	SetDependency(sourceID, targetID, kind string) error
	DeleteDependencies(sourceID string) error
	AcquireLock(holder string, pid int) error
	ReleaseLock() error
	GetLock() (*store.Lock, error)
	CreateApply(id, specHash string) error
	CompleteApply(id string) error
	ListApplies(limit int) ([]store.Apply, error)
	CreateApplyAction(id, applyID, resourceID, action string) error
	UpdateApplyAction(id, outcome, errMsg string) error
	ListApplyActions(applyID string) ([]store.ApplyAction, error)
	CreateGeneration(g store.Generation) error
	UpdateGeneration(id, outputText, outcome, rejectionReason string, durationMS, inputTokens, outputTokens int64, costUSD float64) error
	ListGenerations(resourceID string, limit int) ([]store.Generation, error)
	CreateSession(sess store.Session) error
	GetSession(id string) (*store.Session, error)
	GetActiveSession() (*store.Session, error)
	UpdateSession(id, status string, currentWave int) error
	SetNote(resourceID, applyID, content string) error
	GetNote(resourceID, applyID string) (string, error)
	ListNotes(applyID string) ([]store.AgentNote, error)
	UpsertSessionResource(r store.SessionResource) error
	GetSessionResource(sessionID, resourceID string) (*store.SessionResource, error)
	ListSessionResources(sessionID string) ([]store.SessionResource, error)
	ListSessionResourcesByWave(sessionID string, wave int) ([]store.SessionResource, error)
	UpdateSessionResourceState(sessionID, resourceID, state, lastError, lastOutput string, attempts int, jobID string) error
	UpdateSessionResourcePhase(sessionID, resourceID, phase string, attempts int) error
	SetSessionResourceDispatched(sessionID, resourceID string) error
	GetGeneration(id string) (*store.Generation, error)
	RecordInvariantCheck(ic store.InvariantCheck) error
	ListInvariantChecks(applyID string) ([]store.InvariantCheck, error)
	Vacuum(before time.Time) (int, error)
	ReadOnlyQuery(query string) ([]map[string]interface{}, error)
	ListActiveLearnings(lang, kind string, limit int) ([]store.Learning, error)
	IncrementLearningApplied(id string) error
}

type Spec struct {
	engine specEngine
	store  specStore
	fs     fileSystem
	cfg    *config.Config
}

func New(eng specEngine, st specStore, fs fileSystem, cfg *config.Config) *Spec {
	return &Spec{
		engine: eng,
		store:  st,
		fs:     fs,
		cfg:    cfg,
	}
}

type PlanResult struct {
	Actions  []planpkg.PlannedAction
	Registry *cuepkg.Registry
	Graph    *graphpkg.Graph
	Waves    [][]string
	Hashes   map[string]string
	Mode     string
}

func (s *Spec) Plan(ctx context.Context) (*PlanResult, error) {
	project, err := cuepkg.Load(s.cfg.SpecDir)
	if err != nil {
		return nil, err
	}

	registry, err := cuepkg.NewRegistry(project)
	if err != nil {
		return nil, err
	}

	g, err := graphpkg.Build(registry.Resources)
	if err != nil {
		return nil, err
	}

	model := s.cfg.GenerateModel
	mode := s.cfg.Mode
	if project.Meta.Mode != "" {
		mode = project.Meta.Mode
	}
	hashes := graphpkg.ComputeEffectiveHashes(registry.Resources, g, model, mode)

	planner := planpkg.New(s.store, s.fs)
	actions, err := planner.Plan(ctx, registry, g, model, mode)
	if err != nil {
		return nil, err
	}

	waves, err := g.Waves()
	if err != nil {
		return nil, err
	}

	waveStrings := make([][]string, len(waves))
	for i, wave := range waves {
		waveStrings[i] = wave
	}

	return &PlanResult{
		Actions:  actions,
		Registry: registry,
		Graph:    g,
		Waves:    waveStrings,
		Hashes:   hashes,
		Mode:     mode,
	}, nil
}
