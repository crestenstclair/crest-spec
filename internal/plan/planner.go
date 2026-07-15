package plan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/execution"
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

type pathResolver interface {
	Resolve(path string) (string, error)
}

type Planner struct {
	store     planStore
	fs        fileReader
	workspace pathResolver
}

func New(store planStore, fs fileReader) *Planner {
	return &Planner{store: store, fs: fs}
}

// NewInWorkspace makes persisted generated-file paths independent of the
// process working directory while preserving New for embedders whose reader
// already interprets project-relative paths.
func NewInWorkspace(store planStore, fs fileReader, workspace pathResolver) *Planner {
	return &Planner{store: store, fs: fs, workspace: workspace}
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
	annotateGoalImpact(result, registry)

	return result, nil
}

func annotateGoalImpact(actions []PlannedAction, registry *cuepkg.Registry) {
	for index := range actions {
		action := &actions[index]
		action.OperationID = operationID(*action)
		action.Category = classifyCategory(*action, registry.Resources[action.ResourceID])
		action.RecommendedRole = string(execution.RecommendedRole(action.Category))
		resource, exists := registry.Resources[action.ResourceID]
		if !exists || len(resource.Contributions) == 0 {
			action.SharedInfrastructure = true
			continue
		}
		goalSet := make(map[string]bool)
		action.Contributions = make(map[string]string)
		for _, contribution := range resource.Contributions {
			action.Capabilities = append(action.Capabilities, contribution.Capability)
			action.Contributions[contribution.Capability] = contribution.Contribution
			capability := registry.Project.Capabilities[strings.TrimPrefix(contribution.Capability, "capability.")]
			for _, goalID := range capability.Goals {
				goalSet[goalID] = true
			}
			for _, acceptance := range capability.Acceptance {
				action.ExpectedBehavior = append(action.ExpectedBehavior, acceptance.Description)
				action.ExpectedEvidence = append(action.ExpectedEvidence, acceptance.Evidence...)
			}
		}
		for goalID := range goalSet {
			action.Goals = append(action.Goals, goalID)
		}
		sort.Strings(action.Capabilities)
		sort.Strings(action.Goals)
		sort.Strings(action.ExpectedBehavior)
		sort.Strings(action.ExpectedEvidence)
		action.ExpectedEvidence = dedupeStrings(action.ExpectedEvidence)
	}
}

func BuildCapabilitySlices(actions []PlannedAction, registry *cuepkg.Registry) []CapabilitySlice {
	byCapability := make(map[string]*CapabilitySlice)
	for _, action := range actions {
		for _, capabilityID := range action.Capabilities {
			slice := byCapability[capabilityID]
			if slice == nil {
				capability := registry.Project.Capabilities[strings.TrimPrefix(capabilityID, "capability.")]
				slice = &CapabilitySlice{Capability: capabilityID, Goals: append([]string(nil), capability.Goals...), CurrentGap: "resource implementation or verification is not current"}
				for _, goalID := range capability.Goals {
					goal := registry.Project.Goals[strings.TrimPrefix(goalID, "goal.")]
					if goal.Priority == "required" {
						slice.Required = true
					}
				}
				for _, acceptance := range capability.Acceptance {
					slice.ExpectedBehavior = append(slice.ExpectedBehavior, acceptance.Description)
					slice.ExpectedEvidence = append(slice.ExpectedEvidence, acceptance.Evidence...)
				}
				byCapability[capabilityID] = slice
			}
			slice.OperationIDs = append(slice.OperationIDs, action.OperationID)
		}
	}
	result := make([]CapabilitySlice, 0, len(byCapability))
	for _, slice := range byCapability {
		sort.Strings(slice.Goals)
		sort.Strings(slice.ExpectedBehavior)
		sort.Strings(slice.ExpectedEvidence)
		slice.ExpectedEvidence = dedupeStrings(slice.ExpectedEvidence)
		result = append(result, *slice)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Required != result[j].Required {
			return result[i].Required
		}
		return result[i].Capability < result[j].Capability
	})
	return result
}

func operationID(action PlannedAction) string {
	value := fmt.Sprintf("%s\x00%s\x00%s", action.ResourceID, action.Kind, action.Reason)
	return fmt.Sprintf("op.%x", sha256.Sum256([]byte(value)))
}

func classifyCategory(action PlannedAction, resource cuepkg.Resource) string {
	if action.Kind == ActionDestroy || action.Kind == ActionCreate {
		return "structural"
	}
	if action.CascadedFrom != "" {
		return "integrative"
	}
	if asset, ok := resource.Declaration.(cuepkg.Asset); ok && asset.Profile.Kind == "verification_harness" {
		return "verification"
	}
	return "corrective"
}

func dedupeStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
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
		path := f.Path
		if p.workspace != nil {
			path, err = p.workspace.Resolve(f.Path)
			if err != nil {
				return nil, fmt.Errorf("resolve generated file %s: %w", f.Path, err)
			}
		}
		if _, err := p.fs.ReadFile(path); err != nil {
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
