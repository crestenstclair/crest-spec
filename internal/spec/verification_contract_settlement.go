package spec

import (
	"context"
	"fmt"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// settlePassedWitnessRepairContracts advances only explicit repair-owner
// verification fingerprints, and only after the session's mechanical closure
// is verified and every repair witness in the owner's current contract has a
// current passing run. A failed, malformed, theater, evidence-only, or partial
// witness run never stamps implementation state.
func (s *Spec) settlePassedWitnessRepairContracts(
	ctx context.Context,
	plan *PlanResult,
	witness cuepkg.Witness,
	sessionID string,
	sourceTreeHash string,
) error {
	if plan == nil || plan.Registry == nil || plan.Registry.Project == nil ||
		sessionID == "" || len(witness.RepairResources) == 0 {
		return nil
	}
	session, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("verify witness: inspect settlement session: %w", err)
	}
	if session.Status != "active" {
		return nil
	}
	facts, err := s.store.GetVerificationCompletionFacts(ctx, plan.Registry.Project.Name)
	if err != nil {
		return fmt.Errorf("verify witness: load current repair evidence: %w", err)
	}

	for _, resourceID := range witness.RepairResources {
		sessionResource, err := s.store.GetSessionResource(sessionID, resourceID)
		if err != nil || ResourceState(sessionResource.State) != StateCommitted || sessionResource.Phase != "verified" {
			continue
		}
		contract := plan.Registry.VerificationContractForResource(resourceID)
		if len(contract.RepairWitnesses) == 0 || !allRepairWitnessesPassed(contract, facts.LatestRuns, sourceTreeHash) {
			continue
		}
		verificationHash := plan.VerificationContractHashes[resourceID]
		if verificationHash == "" {
			continue
		}
		persisted, err := s.store.GetResource(resourceID)
		if err != nil {
			return fmt.Errorf("verify witness: load repair owner %s: %w", resourceID, err)
		}
		// VerifyWave must have settled every implementation-facing fingerprint
		// before a witness can certify the accepted tree.
		if persisted.CodeDeclarationHash != plan.CodeDeclarationHashes[resourceID] ||
			persisted.GenerationInputHash != plan.GenerationInputHashes[resourceID] ||
			persisted.DependencyContractHash != plan.DependencyContractHashes[resourceID] {
			continue
		}
		persisted.DeclarationHash = plan.DeclarationHashes[resourceID]
		persisted.VerificationContractHash = verificationHash
		if err := s.store.SetResource(*persisted); err != nil {
			return fmt.Errorf("verify witness: settle repair owner %s: %w", resourceID, err)
		}
	}
	return nil
}

func allRepairWitnessesPassed(
	contract cuepkg.VerificationContract,
	latest map[string]store.LatestValidationResult,
	sourceTreeHash string,
) bool {
	for witnessID := range contract.RepairWitnesses {
		result, exists := latest[witnessID]
		if !exists || result.Classification != "passed" || result.SourceTreeHash != sourceTreeHash {
			return false
		}
	}
	return true
}
