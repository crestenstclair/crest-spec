package spec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/engine"
	"github.com/crestenstclair/crest-spec/internal/evolve"
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
	ListLearnings(status string) ([]store.Learning, error)
	CreateLearning(l store.Learning) error
	UpdateLearningStatus(id, status string) error
	UpsertAmendment(a store.Amendment) error
	GetAmendment(resourceID, name string) (*store.Amendment, error)
	ListAmendmentsByResource(resourceID string) ([]store.Amendment, error)
	ListAmendmentsByState(state string) ([]store.Amendment, error)
	ListAllAmendments() ([]store.Amendment, error)
	UpdateAmendmentState(id, state, appliedSpecHash string, appliedAt, graduatedAt time.Time) error
	DeleteAmendment(resourceID, name string) error
}

type Spec struct {
	engine    specEngine
	store     specStore
	fs        fileSystem
	cfg       *config.Config
	reflector *evolve.Reflector
}

func New(eng specEngine, st specStore, fs fileSystem, cfg *config.Config) *Spec {
	model := ""
	if cfg != nil {
		model = cfg.GenerateModel
	}
	reflector := evolve.New(
		&engineGenerator{eng: eng},
		&storeReflectorAdapter{st: st},
		model,
	)
	return &Spec{
		engine:    eng,
		store:     st,
		fs:        fs,
		cfg:       cfg,
		reflector: reflector,
	}
}

// engineGenerator adapts a specEngine to evolve.Generator, narrowing the rich
// engine.Generate signature down to the plain (prompt, model) → text contract
// the reflector depends on. A nil engine yields an error, which the reflector
// swallows — reflection must never be able to fail a run.
type engineGenerator struct {
	eng specEngine
}

func (g *engineGenerator) Generate(ctx context.Context, prompt, model string) (string, error) {
	if g.eng == nil {
		return "", fmt.Errorf("evolve: no engine available")
	}
	res, err := g.eng.Generate(ctx, engine.GenerateOpts{Prompt: prompt, Model: model})
	if err != nil {
		return "", err
	}
	if res == nil {
		return "", nil
	}
	return res.Output, nil
}

// storeReflectorAdapter exposes only the read+write methods evolve.Store needs,
// delegating to the broader specStore. It keeps the reflector decoupled from the
// full store surface (Interface Segregation / Dependency Inversion).
type storeReflectorAdapter struct {
	st specStore
}

func (a *storeReflectorAdapter) ListSessionResources(sessionID string) ([]store.SessionResource, error) {
	return a.st.ListSessionResources(sessionID)
}

func (a *storeReflectorAdapter) ListSessionResourcesByWave(sessionID string, wave int) ([]store.SessionResource, error) {
	return a.st.ListSessionResourcesByWave(sessionID, wave)
}

func (a *storeReflectorAdapter) GetResource(id string) (*store.Resource, error) {
	return a.st.GetResource(id)
}

func (a *storeReflectorAdapter) ListGenerations(resourceID string, limit int) ([]store.Generation, error) {
	return a.st.ListGenerations(resourceID, limit)
}

func (a *storeReflectorAdapter) ListInvariantChecks(applyID string) ([]store.InvariantCheck, error) {
	return a.st.ListInvariantChecks(applyID)
}

func (a *storeReflectorAdapter) ListLearnings(status string) ([]store.Learning, error) {
	return a.st.ListLearnings(status)
}

func (a *storeReflectorAdapter) CreateLearning(l store.Learning) error {
	return a.st.CreateLearning(l)
}

// Evolve runs an on-demand session-scoped reflection (Component 6, trigger 2)
// and returns the number of learnings added. It resolves the session's apply ID
// so reflection can read failed invariant checks. Reflection never fails a run,
// so a missing session simply yields zero learnings.
func (s *Spec) Evolve(ctx context.Context, sessionID string) (int, error) {
	if s.reflector == nil {
		return 0, nil
	}
	applyID := ""
	if sess, err := s.store.GetSession(sessionID); err == nil && sess != nil {
		applyID = sess.ApplyID
	}
	return s.reflector.ReflectSession(ctx, sessionID, applyID)
}

// ListLearnings returns learnings with the given status, delegating to the
// store. An empty status defaults to "active".
func (s *Spec) ListLearnings(status string) ([]store.Learning, error) {
	if status == "" {
		status = "active"
	}
	return s.store.ListLearnings(status)
}

// PromoteResult is the outcome of a (preview or applied) promotion run.
type PromoteResult struct {
	// Promotable is the set of active learnings selected for promotion.
	Promotable []store.Learning
	// MarkdownBlock is the rendered block that would be (or was) appended to
	// the target learned template. Empty when nothing qualifies.
	MarkdownBlock string
	// TargetPath is the learned template the block targets.
	TargetPath string
	// Applied reports whether the block was written and the learnings marked
	// promoted (false for a preview).
	Applied bool
}

// PromoteLearnings selects active learnings above the given thresholds and
// either previews (apply=false) or durably writes (apply=true) them into the
// per-language learned template. Promotion is human-gated: with apply=false this
// mutates nothing — it only returns the proposed block — and the caller (a human
// reviewing the diff) opts in by re-invoking with apply=true.
//
//   - lang scopes the selection: a learning matches when its scope_lang is empty
//     (applies to any language) or equals lang. An empty lang selects all
//     learnings and defaults the template path's language segment to "rust".
//   - minConfidence/minTimesApplied default to 0.8 / 3 when the caller passes 0.
//   - templatePath overrides the default
//     "internal/prompt/templates/learned/<lang>.md" target.
//
// On apply, the block is appended (creating parent dirs and ensuring a
// separating newline) and each promoted learning's status is set to "promoted"
// so it is no longer injected at runtime.
func (s *Spec) PromoteLearnings(ctx context.Context, lang string, minConfidence float64, minTimesApplied int, apply bool, templatePath string) (PromoteResult, error) {
	if minConfidence == 0 {
		minConfidence = 0.8
	}
	if minTimesApplied == 0 {
		minTimesApplied = 3
	}

	targetLang := lang
	if targetLang == "" {
		targetLang = "rust"
	}
	if templatePath == "" {
		templatePath = "internal/prompt/templates/learned/" + targetLang + ".md"
	}

	active, err := s.store.ListLearnings("active")
	if err != nil {
		return PromoteResult{}, fmt.Errorf("list active learnings: %w", err)
	}

	// Scope by language: keep cross-language (empty scope) and exact matches.
	var scoped []store.Learning
	for _, l := range active {
		if lang == "" || l.ScopeLang == "" || l.ScopeLang == lang {
			scoped = append(scoped, l)
		}
	}

	promotable := evolve.SelectPromotable(scoped, minConfidence, minTimesApplied)
	block := evolve.RenderPromotionBlock(promotable)

	result := PromoteResult{
		Promotable:    promotable,
		MarkdownBlock: block,
		TargetPath:    templatePath,
		Applied:       false,
	}

	if !apply {
		return result, nil
	}

	if block != "" {
		if err := s.appendLearnedTemplate(templatePath, block); err != nil {
			return result, fmt.Errorf("append learned template: %w", err)
		}
		for _, l := range promotable {
			if err := s.store.UpdateLearningStatus(l.ID, "promoted"); err != nil {
				return result, fmt.Errorf("mark learning %s promoted: %w", l.ID, err)
			}
		}
	}
	result.Applied = true
	return result, nil
}

// appendLearnedTemplate appends block to the learned template at path, creating
// parent directories as needed and ensuring a blank-line separation from any
// existing content.
func (s *Spec) appendLearnedTemplate(path, block string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := s.fs.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	existing, err := s.fs.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var b strings.Builder
	if len(existing) > 0 {
		b.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(block)

	return s.fs.WriteFile(path, []byte(b.String()), 0o644)
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
