package plan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	graphpkg "github.com/crestenstclair/crest-spec/internal/graph"
	"github.com/crestenstclair/crest-spec/internal/store"
)

var structuralKinds = map[string]bool{
	"project":   true,
	"context":   true,
	"assetKind": true,
}

type planStore interface {
	GetResource(id string) (*store.Resource, error)
	ListResources() ([]store.Resource, error)
	GetGeneratedFiles(resourceID string) ([]store.GeneratedFile, error)
}

type fileReader interface {
	ReadFile(path string) ([]byte, error)
}

type Planner struct {
	store planStore
	fs    fileReader
}

func New(store planStore, fs fileReader) *Planner {
	return &Planner{store: store, fs: fs}
}

func (p *Planner) Plan(
	ctx context.Context,
	registry *cuepkg.Registry,
	g *graphpkg.Graph,
	model string,
	mode string,
) ([]PlannedAction, error) {
	effectiveHashes := graphpkg.ComputeEffectiveHashes(registry.Resources, g, model, mode)

	storedMap, err := p.loadStoredMap()
	if err != nil {
		return nil, err
	}

	topoIndex, err := buildTopoIndex(g)
	if err != nil {
		return nil, err
	}

	createModify, err := p.planCreateModify(registry, g, effectiveHashes, storedMap)
	if err != nil {
		return nil, err
	}

	destroys, err := p.planDestroys(registry, storedMap)
	if err != nil {
		return nil, err
	}

	sort.Slice(destroys, func(i, j int) bool {
		return destroys[i].ResourceID < destroys[j].ResourceID
	})
	sort.Slice(createModify, func(i, j int) bool {
		ii, iOK := topoIndex[createModify[i].ResourceID]
		jj, jOK := topoIndex[createModify[j].ResourceID]
		if iOK && jOK {
			return ii < jj
		}
		return createModify[i].ResourceID < createModify[j].ResourceID
	})

	result := make([]PlannedAction, 0, len(destroys)+len(createModify))
	result = append(result, destroys...)
	result = append(result, createModify...)

	return result, nil
}

func (p *Planner) loadStoredMap() (map[string]store.Resource, error) {
	storedResources, err := p.store.ListResources()
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	storedMap := make(map[string]store.Resource, len(storedResources))
	for _, sr := range storedResources {
		storedMap[sr.ID] = sr
	}
	return storedMap, nil
}

func buildTopoIndex(g *graphpkg.Graph) (map[string]int, error) {
	topoOrder, err := g.TopologicalSort()
	if err != nil {
		return nil, fmt.Errorf("topo sort: %w", err)
	}
	topoIndex := make(map[string]int, len(topoOrder))
	for i, id := range topoOrder {
		topoIndex[id] = i
	}
	return topoIndex, nil
}

func (p *Planner) planCreateModify(
	registry *cuepkg.Registry,
	g *graphpkg.Graph,
	effectiveHashes map[string]string,
	storedMap map[string]store.Resource,
) ([]PlannedAction, error) {
	var actions []PlannedAction

	for id, r := range registry.Resources {
		if structuralKinds[r.Kind] {
			continue
		}

		action, err := p.classifyResource(id, r, g, effectiveHashes, storedMap)
		if err != nil {
			return nil, err
		}
		if action != nil {
			actions = append(actions, *action)
		}
	}

	return actions, nil
}

func (p *Planner) classifyResource(
	id string,
	r cuepkg.Resource,
	g *graphpkg.Graph,
	effectiveHashes map[string]string,
	storedMap map[string]store.Resource,
) (*PlannedAction, error) {
	stored, exists := storedMap[id]
	if !exists {
		return &PlannedAction{ResourceID: id, Kind: ActionCreate, Reason: "new resource"}, nil
	}

	if stored.DeclarationHash != declHash(r.Declaration) {
		return &PlannedAction{ResourceID: id, Kind: ActionModify, Reason: "declaration changed"}, nil
	}

	if stored.EffectiveHash != effectiveHashes[id] {
		cascadedFrom := findChangedAncestor(id, g, effectiveHashes, storedMap)
		return &PlannedAction{
			ResourceID:   id,
			Kind:         ActionModify,
			Reason:       fmt.Sprintf("dependency changed (%s)", cascadedFrom),
			CascadedFrom: cascadedFrom,
		}, nil
	}

	return p.checkMissing(id)
}

// checkMissing re-renders a resource only when its generated files are gone.
// Content edits are intentionally ignored — once generated, the file is the
// user's to modify. To force a re-render, edit the spec or delete the file.
func (p *Planner) checkMissing(id string) (*PlannedAction, error) {
	files, err := p.store.GetGeneratedFiles(id)
	if err != nil {
		return nil, fmt.Errorf("get generated files for %s: %w", id, err)
	}

	for _, f := range files {
		if _, err := p.fs.ReadFile(f.Path); err != nil {
			return &PlannedAction{
				ResourceID: id, Kind: ActionModify,
				Reason: "generated file missing — regenerating", Files: filePaths(files),
			}, nil
		}
	}

	return nil, nil
}

func (p *Planner) planDestroys(
	registry *cuepkg.Registry,
	storedMap map[string]store.Resource,
) ([]PlannedAction, error) {
	var destroys []PlannedAction
	for id, sr := range storedMap {
		if structuralKinds[sr.Kind] {
			continue
		}
		if _, exists := registry.Resources[id]; !exists {
			files, _ := p.store.GetGeneratedFiles(id)
			destroys = append(destroys, PlannedAction{
				ResourceID: id,
				Kind:       ActionDestroy,
				Reason:     "removed from spec",
				Files:      filePaths(files),
			})
		}
	}
	return destroys, nil
}

func filePaths(files []store.GeneratedFile) []string {
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return paths
}

func declHash(declaration any) string {
	data, _ := json.Marshal(declaration)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func findChangedAncestor(id string, g *graphpkg.Graph, effectiveHashes map[string]string, storedMap map[string]store.Resource) string {
	ancestors := g.Ancestors(id)
	for _, ancestorID := range ancestors {
		stored, ok := storedMap[ancestorID]
		if !ok {
			return ancestorID
		}
		if stored.EffectiveHash != effectiveHashes[ancestorID] {
			return ancestorID
		}
	}
	return ""
}
