package spec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type candidateReuseStore interface {
	FindReusableAcceptedCandidate(ctx context.Context, resourceID, generationInputHash, dependencyContractHash string) (*store.CandidateSet, error)
	ApplyCandidateReuse(ctx context.Context, input store.CandidateReuseInput) (*store.CandidateReuse, error)
}

// requireClosureValidationForFastPaths turns every shortcut back into normal
// generation when the affected closure has no executable mechanical check.
// A no-op validation batch is not evidence of compatibility.
func (s *Spec) requireClosureValidationForFastPaths(plan *PlanResult) {
	if plan == nil {
		return
	}
	resourceIDs := make([]string, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		if action.ExecutionStrategy == planpkg.ExecutionStrategyValidateExisting ||
			action.ExecutionStrategy == planpkg.ExecutionStrategyReuseCandidate {
			resourceIDs = append(resourceIDs, action.ResourceID)
		}
	}
	if s.closureValidationAvailable(plan, resourceIDs) {
		return
	}
	for index := range plan.Actions {
		action := &plan.Actions[index]
		if action.ExecutionStrategy != planpkg.ExecutionStrategyValidateExisting &&
			action.ExecutionStrategy != planpkg.ExecutionStrategyReuseCandidate {
			continue
		}
		action.ExecutionStrategy = planpkg.ExecutionStrategyGenerate
		action.SourceCandidateID = ""
	}
}

func (s *Spec) closureValidationAvailable(plan *PlanResult, resourceIDs []string) bool {
	if plan == nil || plan.Registry == nil || plan.Registry.Project == nil {
		return false
	}
	if len(resourceIDs) == 0 {
		return true
	}
	if strings.TrimSpace(s.cfg.TypeCheckCommand) != "" || strings.TrimSpace(s.cfg.TestCommand) != "" {
		return true
	}
	covered := make(map[string]bool, len(resourceIDs))
	for _, validation := range plan.Registry.Project.Validations {
		if !executableValidation(validation) {
			continue
		}
		if len(validation.Resources) == 0 {
			// A project validation with no owner restriction observes the whole
			// affected closure.
			return true
		}
		for _, resourceID := range validation.Resources {
			covered[resourceID] = true
		}
	}
	for _, resourceID := range resourceIDs {
		resource, exists := plan.Registry.Resources[resourceID]
		if !exists {
			return false
		}
		if contract := plan.Registry.VerificationContractForResource(resourceID); len(contract.RepairWitnesses) > 0 {
			// The accepted tree is intentionally held for the declared witness.
			// A failure reopens only RepairResources; a pass stamps the pending
			// verification fingerprint.
			covered[resourceID] = true
		}
		for _, validation := range resourceValidations(resource) {
			if executableValidation(validation) {
				covered[resourceID] = true
				break
			}
		}
	}
	for _, resourceID := range resourceIDs {
		if !covered[resourceID] {
			return false
		}
	}
	return true
}

func executableValidation(validation cuepkg.Validation) bool {
	return len(validation.Command) > 0 && strings.TrimSpace(validation.Command[0]) != ""
}

func committedFastPathResourceIDs(resources []store.SessionResource) []string {
	ids := make([]string, 0, len(resources))
	for _, resource := range resources {
		if ResourceState(resource.State) != StateCommitted ||
			(resource.ExecutionStrategy != planpkg.ExecutionStrategyValidateExisting &&
				resource.ExecutionStrategy != planpkg.ExecutionStrategyReuseCandidate) {
			continue
		}
		ids = append(ids, resource.ResourceID)
	}
	sort.Strings(ids)
	return ids
}

// sessionOperationFingerprintErrors verifies the immutable operation bindings
// captured at Begin against the current CUE plan immediately before closure
// validation. Validation must never certify one operation and then settle a
// resource using fingerprints loaded from a newer operation.
func (s *Spec) sessionOperationFingerprintErrors(
	sessionID string,
	current *PlanResult,
	resources []store.SessionResource,
) []WaveError {
	resourceIDs := make([]string, 0, len(resources))
	resourceSet := make(map[string]bool, len(resources))
	resourcesByID := make(map[string]store.SessionResource, len(resources))
	for _, resource := range resources {
		resourceIDs = append(resourceIDs, resource.ResourceID)
		resourceSet[resource.ResourceID] = true
		resourcesByID[resource.ResourceID] = resource
	}
	sort.Strings(resourceIDs)

	session, err := s.store.GetSession(sessionID)
	if err != nil {
		return []WaveError{waveErrorForResources(
			resourceIDs, "operation_fingerprint_drift",
			fmt.Sprintf("load immutable session operations: %v", err),
		)}
	}
	var actions []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(session.PlanJSON), &actions); err != nil {
		return []WaveError{waveErrorForResources(
			resourceIDs, "operation_fingerprint_drift",
			fmt.Sprintf("decode immutable session operations: %v", err),
		)}
	}

	actionsByResource := make(map[string]planpkg.PlannedAction, len(actions))
	for _, action := range actions {
		if action.Kind != planpkg.ActionDestroy {
			actionsByResource[action.ResourceID] = action
		}
	}
	var result []WaveError
	for _, resourceID := range resourceIDs {
		action, exists := actionsByResource[resourceID]
		if !exists {
			result = append(result, WaveError{
				ResourceID: resourceID, Kind: "operation_fingerprint_drift",
				Message: "immutable session operation is missing; regenerate from the current plan",
			})
			continue
		}
		verificationMatches := current != nil &&
			action.VerificationContractHash == current.VerificationContractHashes[resourceID]
		persisted, persistedErr := s.store.GetResource(resourceID)
		legacyVerificationBridge := current != nil && session.Status == "active" &&
			persistedErr == nil && legacyCommittedOperationCanValidate(action, current, resourcesByID[resourceID], persisted)
		generationMatches := current != nil && action.GenerationInputHash != "" &&
			action.GenerationInputHash == current.GenerationInputHashes[resourceID]
		dependencyMatches := current != nil && action.DependencyContractHash != "" &&
			action.DependencyContractHash == current.DependencyContractHashes[resourceID]
		persistedGenerationMatches := persistedErr == nil && persisted.GenerationInputHash == action.GenerationInputHash
		persistedDependencyMatches := persistedErr == nil && persisted.DependencyContractHash == action.DependencyContractHash
		declarationMatches := current != nil && persistedErr == nil &&
			persisted.DeclarationHash != "" && persisted.DeclarationHash == current.DeclarationHashes[resourceID]
		legacyGenerationMatches := current != nil &&
			action.GenerationInputHash == current.LegacyGenerationInputHashesV2[resourceID]
		legacyDependencyMatches := current != nil &&
			action.DependencyContractHash == current.LegacyDependencyContractHashesV2[resourceID]
		if current == nil || action.GenerationInputHash == "" || action.DependencyContractHash == "" ||
			current.GenerationInputHashes[resourceID] == "" || current.DependencyContractHashes[resourceID] == "" ||
			((!generationMatches || !dependencyMatches || !verificationMatches) && !legacyVerificationBridge) {
			result = append(result, WaveError{
				ResourceID: resourceID, Kind: "operation_fingerprint_drift",
				Message: fmt.Sprintf(
					"operation inputs changed after the session operation was bound: generation_match=%t legacy_generation_match=%t bound_generation=%s current_generation=%s dependency_match=%t legacy_dependency_match=%t bound_dependency=%s current_dependency=%s verification_match=%t legacy_verification_bridge=%t bound_verification=%s current_verification=%s state=%s legacy_identity=%t persisted_generation_match=%t persisted_dependency_match=%t declaration_match=%t persisted_error=%q; regenerate from the current plan",
					generationMatches, legacyGenerationMatches, shortFingerprint(action.GenerationInputHash), shortFingerprint(currentFingerprint(current, resourceID, "generation")),
					dependencyMatches, legacyDependencyMatches, shortFingerprint(action.DependencyContractHash), shortFingerprint(currentFingerprint(current, resourceID, "dependency")),
					verificationMatches, legacyVerificationBridge,
					shortFingerprint(action.VerificationContractHash), shortFingerprint(currentFingerprint(current, resourceID, "verification")),
					resourcesByID[resourceID].State, planpkg.HasLegacyOperationIdentity(action),
					persistedGenerationMatches, persistedDependencyMatches, declarationMatches, fmt.Sprint(persistedErr),
				),
			})
		}
	}
	// A non-destroy action missing from session_resources is also unsafe. It
	// cannot silently disappear from the validation closure.
	for _, action := range actions {
		if action.Kind == planpkg.ActionDestroy || resourceSet[action.ResourceID] {
			continue
		}
		result = append(result, WaveError{
			ResourceID: action.ResourceID, Kind: "operation_fingerprint_drift",
			Message: "immutable session operation has no session resource; regenerate from the current plan",
		})
	}
	return result
}

func currentFingerprint(current *PlanResult, resourceID, kind string) string {
	if current == nil {
		return ""
	}
	switch kind {
	case "generation":
		return current.GenerationInputHashes[resourceID]
	case "dependency":
		return current.DependencyContractHashes[resourceID]
	case "verification":
		return current.VerificationContractHashes[resourceID]
	default:
		return ""
	}
}

func shortFingerprint(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

// legacyCommittedOperationCanValidate is the deliberately narrow bridge for
// accepted sessions created before verification_contract_hash existed. Code
// and dependency identity must already match exactly; only a terminal
// committed operation with an authentic historical operation ID may defer its
// missing verification binding to the current mechanical closure. A non-empty
// mismatch is never bridged.
func legacyCommittedOperationCanValidate(
	action planpkg.PlannedAction,
	current *PlanResult,
	resource store.SessionResource,
	persisted *store.Resource,
) bool {
	if current == nil || persisted == nil || ResourceState(resource.State) != StateCommitted ||
		action.VerificationContractHash != "" ||
		!planpkg.HasLegacyOperationIdentity(action) {
		return false
	}
	generationProjectionMatches := action.GenerationInputHash == current.GenerationInputHashes[action.ResourceID] ||
		(current.LegacyGenerationInputHashesV2[action.ResourceID] != "" &&
			action.GenerationInputHash == current.LegacyGenerationInputHashesV2[action.ResourceID])
	dependencyProjectionMatches := action.DependencyContractHash == current.DependencyContractHashes[action.ResourceID] ||
		(current.LegacyDependencyContractHashesV2[action.ResourceID] != "" &&
			action.DependencyContractHash == current.LegacyDependencyContractHashesV2[action.ResourceID])
	return action.GenerationInputHash != "" && action.DependencyContractHash != "" &&
		current.GenerationInputHashes[action.ResourceID] != "" &&
		current.DependencyContractHashes[action.ResourceID] != "" &&
		generationProjectionMatches && dependencyProjectionMatches &&
		persisted.DeclarationHash != "" &&
		persisted.DeclarationHash == current.DeclarationHashes[action.ResourceID]
}

// settleLegacyVerificationContracts records the missing binding only after the
// current closure has passed. Repair-witness owners remain deliberately
// unstamped until every current witness passes against the same accepted tree.
func (s *Spec) settleLegacyVerificationContracts(
	sessionID string,
	plan *PlanResult,
	resources []store.SessionResource,
	result *WaveVerifyResult,
) {
	if result == nil || !result.Passed || plan == nil || plan.Registry == nil {
		return
	}
	session, err := s.store.GetSession(sessionID)
	if err != nil || session.Status != "active" {
		if err != nil {
			result.Passed = false
			result.Errors = append(result.Errors, WaveError{
				Kind: "legacy_verification_settlement", Message: fmt.Sprintf("load immutable session operations: %v", err),
			})
		}
		return
	}
	var actions []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(session.PlanJSON), &actions); err != nil {
		result.Passed = false
		result.Errors = append(result.Errors, WaveError{
			Kind: "legacy_verification_settlement", Message: fmt.Sprintf("decode immutable session operations: %v", err),
		})
		return
	}
	actionsByResource := make(map[string]planpkg.PlannedAction, len(actions))
	for _, action := range actions {
		if action.Kind != planpkg.ActionDestroy {
			actionsByResource[action.ResourceID] = action
		}
	}
	for _, sessionResource := range resources {
		action, exists := actionsByResource[sessionResource.ResourceID]
		if !exists {
			continue
		}
		persisted, err := s.store.GetResource(sessionResource.ResourceID)
		if err != nil {
			result.Passed = false
			result.Errors = append(result.Errors, WaveError{
				ResourceID: sessionResource.ResourceID, Kind: "legacy_verification_settlement", Message: err.Error(),
			})
			continue
		}
		if !legacyCommittedOperationCanValidate(action, plan, sessionResource, persisted) {
			continue
		}
		persisted.CodeDeclarationHash = plan.CodeDeclarationHashes[sessionResource.ResourceID]
		persisted.GenerationInputHash = plan.GenerationInputHashes[sessionResource.ResourceID]
		persisted.ExportedContractHash = plan.ExportedContractHashes[sessionResource.ResourceID]
		persisted.DependencyContractHash = plan.DependencyContractHashes[sessionResource.ResourceID]
		if contract := plan.Registry.VerificationContractForResource(sessionResource.ResourceID); len(contract.RepairWitnesses) == 0 {
			persisted.EffectiveHash = plan.Hashes[sessionResource.ResourceID]
			persisted.DeclarationHash = plan.DeclarationHashes[sessionResource.ResourceID]
			persisted.VerificationContractHash = plan.VerificationContractHashes[sessionResource.ResourceID]
		}
		persisted.SettledAt = time.Now().UTC()
		if err := s.store.SetResource(*persisted); err != nil {
			result.Passed = false
			result.Errors = append(result.Errors, WaveError{
				ResourceID: sessionResource.ResourceID, Kind: "legacy_verification_settlement", Message: err.Error(),
			})
		}
	}
}

// annotateReusableCandidates upgrades a generate action only when immutable
// candidate provenance, accepted ownership, and current workspace bytes all
// agree. Any uncertainty is a cache miss, never an error that weakens safety.
func (s *Spec) annotateReusableCandidates(ctx context.Context, plan *PlanResult) error {
	reuseStore, ok := s.store.(candidateReuseStore)
	if !ok {
		return nil
	}
	for index := range plan.Actions {
		action := &plan.Actions[index]
		if action.Kind == planpkg.ActionDestroy || action.ExecutionStrategy != planpkg.ExecutionStrategyGenerate {
			continue
		}
		candidate, err := reuseStore.FindReusableAcceptedCandidate(
			ctx, action.ResourceID, plan.GenerationInputHashes[action.ResourceID],
			plan.DependencyContractHashes[action.ResourceID],
		)
		if errors.Is(err, cserrors.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect reusable candidate for %s: %w", action.ResourceID, err)
		}
		matches, err := s.candidateMatchesOwnedWorkspace(action.ResourceID, candidate)
		if err != nil {
			return fmt.Errorf("verify reusable candidate for %s: %w", action.ResourceID, err)
		}
		if !matches {
			continue
		}
		action.ExecutionStrategy = planpkg.ExecutionStrategyReuseCandidate
		action.SourceCandidateID = candidate.ID
	}
	return nil
}

func (s *Spec) candidateMatchesOwnedWorkspace(resourceID string, candidate *store.CandidateSet) (bool, error) {
	if candidate == nil || candidate.ID == "" || candidate.Status != "accepted" ||
		candidate.GenerationInputHash == "" || candidate.DependencyContractHash == "" {
		return false, nil
	}
	owned, err := s.store.GetGeneratedFiles(resourceID)
	if err != nil {
		return false, err
	}
	if len(owned) != len(candidate.Files) {
		return false, nil
	}
	ownedHashes := make(map[string]string, len(owned))
	for _, file := range owned {
		ownedHashes[file.Path] = file.ContentHash
	}
	for _, file := range candidate.Files {
		if ownedHashes[file.Path] != file.ContentHash {
			return false, nil
		}
		path, err := s.projectFilePath(file.Path)
		if err != nil {
			return false, err
		}
		content, err := s.fs.ReadFile(path)
		if err != nil || contextmanifest.Hash(string(content)) != file.ContentHash {
			return false, nil
		}
	}
	return true, nil
}

func (s *Spec) applyCandidateReuseFastPaths(ctx context.Context, sessionID string, actions []planpkg.PlannedAction) error {
	reuseStore, ok := s.store.(candidateReuseStore)
	if !ok {
		for _, action := range actions {
			if action.ExecutionStrategy == planpkg.ExecutionStrategyReuseCandidate {
				return fmt.Errorf("candidate reuse store is unavailable")
			}
		}
		return nil
	}
	plan, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	for _, action := range actions {
		if action.ExecutionStrategy != planpkg.ExecutionStrategyReuseCandidate {
			continue
		}
		candidate, err := reuseStore.FindReusableAcceptedCandidate(
			ctx, action.ResourceID, plan.GenerationInputHashes[action.ResourceID],
			plan.DependencyContractHashes[action.ResourceID],
		)
		if err != nil || candidate.ID != action.SourceCandidateID {
			return fmt.Errorf("candidate reuse for %s is no longer valid", action.ResourceID)
		}
		paths := make([]string, 0, len(candidate.Files))
		for _, file := range candidate.Files {
			paths = append(paths, file.Path)
		}
		sort.Strings(paths)
		release, err := s.commitCoordinator().Acquire(ctx, paths)
		if err != nil {
			return err
		}
		matches, matchErr := s.candidateMatchesOwnedWorkspace(action.ResourceID, candidate)
		if matchErr == nil && !matches {
			matchErr = fmt.Errorf("owned workspace files diverged from candidate %s", candidate.ID)
		}
		if matchErr == nil {
			_, matchErr = reuseStore.ApplyCandidateReuse(ctx, store.CandidateReuseInput{
				SessionID: sessionID, ResourceID: action.ResourceID,
				SourceCandidateID: candidate.ID, CandidateHash: candidate.CandidateHash,
				GenerationInputHash:    plan.GenerationInputHashes[action.ResourceID],
				DependencyContractHash: plan.DependencyContractHashes[action.ResourceID],
			})
		}
		release()
		if matchErr != nil {
			return fmt.Errorf("apply candidate reuse for %s: %w", action.ResourceID, matchErr)
		}
	}
	return nil
}

// settleFastPathResources advances the resource projection only after the
// affected closure passes. Before that point the old dependency fingerprint
// remains durable proof that compatibility is still unverified.
func (s *Spec) settleFastPathResources(plan *PlanResult, resources []store.SessionResource, result *WaveVerifyResult) {
	for _, sessionResource := range resources {
		if ResourceState(sessionResource.State) != StateCommitted ||
			(sessionResource.ExecutionStrategy != planpkg.ExecutionStrategyValidateExisting &&
				sessionResource.ExecutionStrategy != planpkg.ExecutionStrategyReuseCandidate) {
			continue
		}
		persisted, err := s.store.GetResource(sessionResource.ResourceID)
		if err != nil {
			result.Passed = false
			result.Errors = append(result.Errors, WaveError{ResourceID: sessionResource.ResourceID, Kind: "fast_path_settlement", Message: err.Error()})
			continue
		}
		persisted.EffectiveHash = plan.Hashes[sessionResource.ResourceID]
		persisted.DeclarationHash = plan.DeclarationHashes[sessionResource.ResourceID]
		persisted.CodeDeclarationHash = plan.CodeDeclarationHashes[sessionResource.ResourceID]
		persisted.GenerationInputHash = plan.GenerationInputHashes[sessionResource.ResourceID]
		persisted.ExportedContractHash = plan.ExportedContractHashes[sessionResource.ResourceID]
		persisted.DependencyContractHash = plan.DependencyContractHashes[sessionResource.ResourceID]
		if contract := plan.Registry.VerificationContractForResource(sessionResource.ResourceID); len(contract.RepairWitnesses) == 0 {
			persisted.VerificationContractHash = plan.VerificationContractHashes[sessionResource.ResourceID]
		}
		persisted.SettledAt = time.Now().UTC()
		if err := s.store.SetResource(*persisted); err != nil {
			result.Passed = false
			result.Errors = append(result.Errors, WaveError{ResourceID: sessionResource.ResourceID, Kind: "fast_path_settlement", Message: err.Error()})
		}
	}
}
