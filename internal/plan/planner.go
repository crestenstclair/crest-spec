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

	storedResources, err := p.store.ListResources()
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	storedMap := make(map[string]store.Resource, len(storedResources))
	for _, sr := range storedResources {
		storedMap[sr.ID] = sr
	}

	var destroys []PlannedAction
	var createModify []PlannedAction

	topoOrder, err := g.TopologicalSort()
	if err != nil {
		return nil, fmt.Errorf("topo sort: %w", err)
	}
	topoIndex := make(map[string]int, len(topoOrder))
	for i, id := range topoOrder {
		topoIndex[id] = i
	}

	for id, r := range registry.Resources {
		if structuralKinds[r.Kind] {
			continue
		}

		stored, exists := storedMap[id]
		if !exists {
			createModify = append(createModify, PlannedAction{
				ResourceID: id,
				Kind:       ActionCreate,
				Reason:     "new resource",
			})
			continue
		}

		newDeclHash := declHash(r.Declaration)
		if stored.DeclarationHash != newDeclHash {
			createModify = append(createModify, PlannedAction{
				ResourceID: id,
				Kind:       ActionModify,
				Reason:     "declaration changed",
			})
			continue
		}

		if stored.EffectiveHash != effectiveHashes[id] {
			cascadedFrom := findChangedAncestor(id, g, effectiveHashes, storedMap)
			createModify = append(createModify, PlannedAction{
				ResourceID:   id,
				Kind:         ActionModify,
				Reason:       fmt.Sprintf("dependency changed (%s)", cascadedFrom),
				CascadedFrom: cascadedFrom,
			})
			continue
		}

		// Effective hash matches — check for drift
		files, err := p.store.GetGeneratedFiles(id)
		if err != nil {
			return nil, fmt.Errorf("get generated files for %s: %w", id, err)
		}

		drifted := false
		for _, f := range files {
			data, err := p.fs.ReadFile(f.Path)
			if err != nil {
				drifted = true
				break
			}
			diskHash := fmt.Sprintf("%x", sha256.Sum256(data))
			if diskHash != f.ContentHash {
				drifted = true
				break
			}
		}

		if drifted {
			var filePaths []string
			for _, f := range files {
				filePaths = append(filePaths, f.Path)
			}
			createModify = append(createModify, PlannedAction{
				ResourceID: id,
				Kind:       ActionDrift,
				Reason:     "file modified on disk",
				Files:      filePaths,
			})
		}
	}

	// Destroys: resources in store but not in registry
	for id, sr := range storedMap {
		if structuralKinds[sr.Kind] {
			continue
		}
		if _, exists := registry.Resources[id]; !exists {
			files, _ := p.store.GetGeneratedFiles(id)
			var filePaths []string
			for _, f := range files {
				filePaths = append(filePaths, f.Path)
			}
			destroys = append(destroys, PlannedAction{
				ResourceID: id,
				Kind:       ActionDestroy,
				Reason:     "removed from spec",
				Files:      filePaths,
			})
		}
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
