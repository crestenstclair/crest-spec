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
	SetResource(resource store.Resource) error
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
	storedMap, err := p.loadStoredMap()
	if err != nil {
		return nil, err
	}
	generationInputHashes := graphpkg.ComputeGenerationInputHashes(registry.Resources, registry.Project)
	legacyGenerationInputHashesV2 := graphpkg.ComputeLegacyGenerationInputHashesV2(registry.Resources, registry.Project)
	codeDeclarationHashes := graphpkg.ComputeCodeDeclarationHashes(registry.Resources)
	verificationContractHashes := graphpkg.ComputeVerificationContractHashes(registry.Resources)
	exportedContractHashes := graphpkg.ComputeExportedContractHashes(registry.Resources)
	dependencyContractHashes := graphpkg.ComputeDependencyContractHashes(registry.Resources, exportedContractHashes)
	provisionalVerification, err := p.adoptLegacyPlanningHashes(
		registry, storedMap, codeDeclarationHashes, generationInputHashes,
		legacyGenerationInputHashesV2, verificationContractHashes,
		exportedContractHashes, dependencyContractHashes,
	)
	if err != nil {
		return nil, err
	}
	exportedContractChanges := findExportedContractChanges(
		registry.Resources,
		storedMap,
		exportedContractHashes,
	)

	topoIndex, err := buildTopoIndex(g)
	if err != nil {
		return nil, err
	}

	createModify, err := p.planCreateModify(
		registry,
		codeDeclarationHashes,
		generationInputHashes,
		verificationContractHashes,
		dependencyContractHashes,
		exportedContractChanges,
		provisionalVerification,
		storedMap,
	)
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
	annotateGoalImpact(result, registry, generationInputHashes, dependencyContractHashes, verificationContractHashes)

	return result, nil
}

// adoptLegacyPlanningHashes upgrades fingerprints without manufacturing work.
// The full declaration hash proves an unchanged legacy row; after migration,
// the code-declaration hash proves that a raw declaration edit touched only
// separately reconciled verification definitions. In either case it is safe
// to stamp v3 generation/export hashes without a model or resource operation.
func (p *Planner) adoptLegacyPlanningHashes(
	registry *cuepkg.Registry,
	storedMap map[string]store.Resource,
	codeDeclarationHashes map[string]string,
	generationInputHashes map[string]string,
	legacyGenerationInputHashesV2 map[string]string,
	verificationContractHashes map[string]string,
	exportedContractHashes map[string]string,
	dependencyContractHashes map[string]string,
) (map[string]bool, error) {
	provisionalVerification := make(map[string]bool)
	// First settle each resource-local representation. This pass is independent
	// of map iteration order so dependency adoption below sees a complete view.
	for id, resource := range registry.Resources {
		if structuralKinds[resource.Kind] {
			continue
		}
		stored, exists := storedMap[id]
		if !exists {
			continue
		}
		currentDeclarationHash := declHash(resource.Declaration)
		exactCurrentV2 := stored.GenerationInputHash != "" &&
			stored.GenerationInputHash == legacyGenerationInputHashesV2[id]
		codeUnchanged := stored.DeclarationHash == currentDeclarationHash ||
			(stored.CodeDeclarationHash != "" && stored.CodeDeclarationHash == codeDeclarationHashes[id])
		if !codeUnchanged {
			// A legacy row cannot separate an embedded validation edit from its
			// full declaration hash. Route the explicit repair owner through
			// validation, while using an in-memory v3 structural projection to
			// avoid speculatively scheduling its consumers. Durable hashes remain
			// untouched until the closure gate passes.
			if stored.CodeDeclarationHash == "" && verificationContractHashes[id] != "" &&
				stored.VerificationContractHash != verificationContractHashes[id] {
				stored.CodeDeclarationHash = codeDeclarationHashes[id]
				stored.ExportedContractHash = exportedContractHashes[id]
				storedMap[id] = stored
				provisionalVerification[id] = true
			}
			continue
		}
		changed := false
		verificationCurrent := stored.VerificationContractHash == verificationContractHashes[id]
		if stored.DeclarationHash != currentDeclarationHash &&
			(verificationContractHashes[id] == "" || verificationCurrent || exactCurrentV2) {
			stored.DeclarationHash = currentDeclarationHash
			changed = true
		}
		if stored.CodeDeclarationHash != codeDeclarationHashes[id] {
			stored.CodeDeclarationHash = codeDeclarationHashes[id]
			changed = true
		}
		if stored.GenerationInputHash != generationInputHashes[id] && exactCurrentV2 {
			stored.GenerationInputHash = generationInputHashes[id]
			changed = true
		}
		if stored.VerificationContractHash != verificationContractHashes[id] &&
			(verificationContractHashes[id] == "" || exactCurrentV2) {
			stored.VerificationContractHash = verificationContractHashes[id]
			changed = true
		}
		if stored.ExportedContractHash != exportedContractHashes[id] {
			stored.ExportedContractHash = exportedContractHashes[id]
			changed = true
		}
		if !changed {
			continue
		}
		if err := p.store.SetResource(stored); err != nil {
			return nil, fmt.Errorf("adopt planning hashes for %s: %w", id, err)
		}
		storedMap[id] = stored
	}

	// Then upgrade dependency fingerprints only when every direct provider's
	// code contract is current. A real provider edit leaves its consumers on
	// the validation frontier; a hash-algorithm or verification-only edit does
	// not create an artificial whole-project wave.
	for id, resource := range registry.Resources {
		if structuralKinds[resource.Kind] || !dependencyBaselineSafe(
			resource, registry.Resources, storedMap, codeDeclarationHashes,
		) {
			continue
		}
		stored, exists := storedMap[id]
		if !exists || stored.DependencyContractHash == dependencyContractHashes[id] {
			continue
		}
		stored.DependencyContractHash = dependencyContractHashes[id]
		provisionalDependency := provisionalVerification[id]
		for _, dependency := range resource.Dependencies {
			provisionalDependency = provisionalDependency || provisionalVerification[dependency.TargetID]
		}
		if !provisionalDependency {
			if err := p.store.SetResource(stored); err != nil {
				return nil, fmt.Errorf("adopt dependency planning hash for %s: %w", id, err)
			}
		}
		storedMap[id] = stored
	}
	return provisionalVerification, nil
}

func dependencyBaselineSafe(
	resource cuepkg.Resource,
	resources map[string]cuepkg.Resource,
	storedMap map[string]store.Resource,
	codeDeclarationHashes map[string]string,
) bool {
	for _, dependency := range resource.Dependencies {
		if _, exists := resources[dependency.TargetID]; !exists {
			return false
		}
		stored, exists := storedMap[dependency.TargetID]
		if !exists || stored.CodeDeclarationHash != codeDeclarationHashes[dependency.TargetID] {
			return false
		}
	}
	return true
}

func annotateGoalImpact(
	actions []PlannedAction,
	registry *cuepkg.Registry,
	hashMaps ...map[string]string,
) {
	for index := range actions {
		action := &actions[index]
		var generationInputHash, dependencyContractHash, verificationContractHash string
		if len(hashMaps) > 0 {
			generationInputHash = hashMaps[0][action.ResourceID]
		}
		if len(hashMaps) > 1 {
			dependencyContractHash = hashMaps[1][action.ResourceID]
		}
		if len(hashMaps) > 2 {
			verificationContractHash = hashMaps[2][action.ResourceID]
		}
		AssignOperationIdentity(
			action,
			generationInputHash,
			dependencyContractHash,
			verificationContractHash,
		)
		action.Category = classifyCategory(*action, registry.Resources[action.ResourceID])
		action.RecommendedRole = string(execution.RecommendedRole(action.Category))
		if action.ExecutionStrategy == "" {
			action.ExecutionStrategy = ExecutionStrategyGenerate
		}
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

// AssignOperationIdentity binds an operation to the exact generation and
// dependency inputs from which its immutable worker context will be built.
// It is exported for synthetic force actions, which are created after normal
// planner annotation.
func AssignOperationIdentity(action *PlannedAction, generationInputHash, dependencyContractHash string, verificationContractHash ...string) {
	action.GenerationInputHash = generationInputHash
	action.DependencyContractHash = dependencyContractHash
	if len(verificationContractHash) > 0 {
		action.VerificationContractHash = verificationContractHash[0]
	}
	action.OperationID = operationID(*action)
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
	value := fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		action.ResourceID,
		action.Kind,
		action.Reason,
		action.GenerationInputHash,
		action.DependencyContractHash,
		action.VerificationContractHash,
	)
	return fmt.Sprintf("op.%x", sha256.Sum256([]byte(value)))
}

// HasLegacyOperationIdentity identifies only the intermediate operation ID
// that cryptographically bound generation+dependency before repair
// verification was added. The older three-field identity cannot prove hashes
// injected later and is deliberately ineligible, as is the current six-field
// identity (even when its verification hash is empty).
func HasLegacyOperationIdentity(action PlannedAction) bool {
	if action.OperationID == "" || action.VerificationContractHash != "" ||
		action.GenerationInputHash == "" || action.DependencyContractHash == "" {
		return false
	}
	intermediateValue := fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%s",
		action.ResourceID,
		action.Kind,
		action.Reason,
		action.GenerationInputHash,
		action.DependencyContractHash,
	)
	intermediateID := fmt.Sprintf("op.%x", sha256.Sum256([]byte(intermediateValue)))
	return action.OperationID == intermediateID
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
	codeDeclarationHashes map[string]string,
	generationInputHashes map[string]string,
	verificationContractHashes map[string]string,
	dependencyContractHashes map[string]string,
	exportedContractChanges map[string]bool,
	provisionalVerification map[string]bool,
	storedMap map[string]store.Resource,
) ([]PlannedAction, error) {
	var actions []PlannedAction

	for id, r := range registry.Resources {
		if structuralKinds[r.Kind] {
			continue
		}

		action, err := p.classifyResource(
			id,
			r,
			codeDeclarationHashes,
			generationInputHashes,
			verificationContractHashes,
			dependencyContractHashes,
			exportedContractChanges,
			provisionalVerification,
			storedMap,
		)
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
	codeDeclarationHashes map[string]string,
	generationInputHashes map[string]string,
	verificationContractHashes map[string]string,
	dependencyContractHashes map[string]string,
	exportedContractChanges map[string]bool,
	provisionalVerification map[string]bool,
	storedMap map[string]store.Resource,
) (*PlannedAction, error) {
	stored, exists := storedMap[id]
	if !exists {
		return &PlannedAction{ResourceID: id, Kind: ActionCreate, Reason: "new resource"}, nil
	}

	// Missing owned output always requires generation. Dependency-only
	// validation cannot certify a file that is not present in the workspace.
	missing, err := p.checkMissing(id)
	if err != nil || missing != nil {
		return missing, err
	}

	if provisionalVerification[id] {
		return &PlannedAction{
			ResourceID: id, Kind: ActionModify,
			Reason:            "legacy verification contract requires validation",
			ExecutionStrategy: ExecutionStrategyValidateExisting,
		}, nil
	}

	if stored.CodeDeclarationHash != codeDeclarationHashes[id] {
		return &PlannedAction{ResourceID: id, Kind: ActionModify, Reason: "declaration changed"}, nil
	}

	// Once the code declaration is current, any older generation fingerprint
	// is an algorithm/bookkeeping mismatch. Adoption above normally settles it;
	// fail closed to validation rather than turning it into model work if a
	// custom store withheld that update.
	if stored.GenerationInputHash != generationInputHashes[id] {
		return &PlannedAction{
			ResourceID: id, Kind: ActionModify,
			Reason:            "generation fingerprint requires bookkeeping validation",
			ExecutionStrategy: ExecutionStrategyValidateExisting,
		}, nil
	}

	if verificationContractHashes[id] != "" &&
		stored.VerificationContractHash != verificationContractHashes[id] {
		return &PlannedAction{
			ResourceID: id, Kind: ActionModify,
			Reason:            "verification contract requires validation",
			ExecutionStrategy: ExecutionStrategyValidateExisting,
		}, nil
	}

	if stored.DependencyContractHash != dependencyContractHashes[id] {
		cascadedFrom := findChangedDependency(r, exportedContractChanges)
		if cascadedFrom == "" {
			cascadedFrom = firstDependency(r)
		}
		return &PlannedAction{
			ResourceID:        id,
			Kind:              ActionModify,
			Reason:            fmt.Sprintf("dependency compatibility requires validation (%s)", cascadedFrom),
			CascadedFrom:      cascadedFrom,
			ExecutionStrategy: ExecutionStrategyValidateExisting,
		}, nil
	}

	if strings.HasPrefix(stored.EffectiveHash, "abandoned:") {
		return &PlannedAction{
			ResourceID: id, Kind: ActionModify,
			Reason: "accepted candidate was invalidated by an abandoned session",
		}, nil
	}

	return nil, nil
}

func firstDependency(resource cuepkg.Resource) string {
	ids := make([]string, 0, len(resource.Dependencies))
	for _, dependency := range resource.Dependencies {
		ids = append(ids, dependency.TargetID)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "unknown"
	}
	return ids[0]
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

func findExportedContractChanges(
	resources map[string]cuepkg.Resource,
	storedMap map[string]store.Resource,
	exportedContractHashes map[string]string,
) map[string]bool {
	changes := make(map[string]bool)
	for id, resource := range resources {
		stored, exists := storedMap[id]
		if !exists {
			changes[id] = true
			continue
		}
		if stored.ExportedContractHash == "" {
			// Legacy rows with changed declarations have no reconstructable old
			// contract. Conservatively expose a dirty frontier for direct users.
			if stored.DeclarationHash != declHash(resource.Declaration) {
				changes[id] = true
			}
			continue
		}
		if stored.ExportedContractHash != exportedContractHashes[id] {
			changes[id] = true
		}
	}
	return changes
}

func findChangedDependency(resource cuepkg.Resource, exportedContractChanges map[string]bool) string {
	var changed []string
	for _, dependency := range resource.Dependencies {
		if exportedContractChanges[dependency.TargetID] {
			changed = append(changed, dependency.TargetID)
		}
	}
	sort.Strings(changed)
	if len(changed) == 0 {
		return ""
	}
	return changed[0]
}
