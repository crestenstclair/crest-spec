package spec

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	"github.com/crestenstclair/crest-spec/internal/execution"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type validateExistingRecoveryStore interface {
	RestoreValidateExistingSessionResource(
		ctx context.Context,
		input store.ValidateExistingReinstatement,
	) (bool, error)
}

type validationStateReconciliation struct {
	changed     bool
	candidateID string
	handoffID   string
	strategy    string
}

func recoverableValidationProjection(resource *store.SessionResource) bool {
	if resource == nil || ResourceState(resource.State) != StatePending || resource.JobID != "" {
		return false
	}
	return (resource.Phase == "integration_failed" && resource.LastError == "") ||
		resource.Phase == "pre_execution_recovered"
}

// reconcilePoisonedValidationResource repairs durable projections written by
// the old invalid-validation reopen loop. It has two deliberately narrow
// paths: restore an immutable validate_existing operation that never had an
// attempt, or reinstate an accepted byte-identical candidate whose validation
// diagnostic was explicitly resolved. Neither path creates model work.
func (s *Spec) reconcilePoisonedValidationResource(
	ctx context.Context,
	resource *store.SessionResource,
	action *planpkg.PlannedAction,
	current *PlanResult,
) (validationStateReconciliation, error) {
	if !recoverableValidationProjection(resource) || action == nil ||
		!operationInputsMatchCurrent(*action, current) {
		return validationStateReconciliation{}, nil
	}

	if action.ExecutionStrategy == planpkg.ExecutionStrategyValidateExisting {
		if resource.Phase != "integration_failed" || resource.LastError != "" ||
			resource.Attempts != 0 || resource.ExecutionStrategy != planpkg.ExecutionStrategyGenerate ||
			resource.SourceCandidateID != "" {
			return validationStateReconciliation{}, nil
		}
		recoveryStore, ok := s.store.(validateExistingRecoveryStore)
		if !ok {
			return validationStateReconciliation{}, nil
		}
		restored, err := recoveryStore.RestoreValidateExistingSessionResource(
			ctx,
			store.ValidateExistingReinstatement{
				SessionID: resource.SessionID, ResourceID: resource.ResourceID,
				PlanOperationID:        action.OperationID,
				GenerationInputHash:    action.GenerationInputHash,
				DependencyContractHash: action.DependencyContractHash,
				Reason:                 "restore immutable validate_existing operation after resolved validation-infrastructure failure",
			},
		)
		if err != nil {
			return validationStateReconciliation{}, fmt.Errorf("restore validate-existing resource: %w", err)
		}
		return validationStateReconciliation{
			changed: restored, strategy: planpkg.ExecutionStrategyValidateExisting,
		}, nil
	}
	if action.ExecutionStrategy != planpkg.ExecutionStrategyGenerate {
		return validationStateReconciliation{}, nil
	}

	candidateID, handoffID, reinstated, err := s.reinstateResolvedAcceptedCandidate(
		ctx, resource, action, current,
	)
	if err != nil {
		return validationStateReconciliation{}, err
	}
	return validationStateReconciliation{
		changed: reinstated, candidateID: candidateID, handoffID: handoffID,
		strategy: planpkg.ExecutionStrategyGenerate,
	}, nil
}

// reinstateResolvedAcceptedCandidate recognizes a resolved, misattributed
// affected-closure failure as a no-model transition. A handoff-backed repair
// uses its exact source. A handoff accepted into a context that was never
// dispatched is still recoverable: the store retires that exact prepared
// context in the same transaction as reinstatement. The handoffless paths locate the newest accepted
// candidate with matching immutable identity: either the legacy
// invalid_validation bug or an explicitly resolved integration_error whose
// repair budget was exhausted before a handoff could be created. The store
// transaction requires zero handoffs for both paths and permits newer history
// only when it is proven abandoned before execution or candidate submission.
func (s *Spec) reinstateResolvedAcceptedCandidate(
	ctx context.Context,
	resource *store.SessionResource,
	action *planpkg.PlannedAction,
	current *PlanResult,
) (string, string, bool, error) {
	if !recoverableValidationProjection(resource) || action == nil || current == nil {
		return "", "", false, nil
	}
	attempts, err := s.store.ListGenerationAttemptsBySession(ctx, resource.SessionID)
	if err != nil {
		return "", "", false, fmt.Errorf("list validation candidate attempts: %w", err)
	}
	attemptByID := make(map[string]store.GenerationAttempt, len(attempts))
	for _, attempt := range attempts {
		attemptByID[attempt.ID] = attempt
	}
	handoff, err := s.reinstatementContextHandoff(
		ctx, resource.SessionID, resource.ResourceID, attempts, attemptByID,
	)
	if err != nil {
		return "", "", false, err
	}

	var (
		source    store.GenerationAttempt
		candidate *store.CandidateSet
		failure   *store.FailureClassification
	)
	if handoff != nil {
		if (handoff.Status != "pending" && handoff.Status != "accepted") || handoff.FailureID == "" {
			return "", "", false, nil
		}
		source = attemptByID[handoff.SourceAttemptID]
		if source.ID == "" {
			return "", "", false, nil
		}
		failure, err = s.store.GetFailureClassification(ctx, handoff.FailureID)
		if err != nil {
			return "", "", false, fmt.Errorf("inspect resolved validation failure: %w", err)
		}
		candidate, err = s.store.GetCandidateSetByAttempt(ctx, source.ID)
		if err != nil {
			return "", "", false, fmt.Errorf("inspect accepted validation candidate: %w", err)
		}
	} else {
		// Retry number is the durable per-resource order; timestamps are not safe
		// when a fast store assigns equal clock values. Scan newest-first for the
		// accepted candidate that still belongs to this immutable
		// operation. A newer abandoned pre-execution context is provenance, not a
		// replacement candidate.
		ordered := append([]store.GenerationAttempt(nil), attempts...)
		sort.SliceStable(ordered, func(left, right int) bool {
			return ordered[left].RetryNumber > ordered[right].RetryNumber
		})
		for _, attempt := range ordered {
			if attempt.ResourceID != resource.ResourceID || attempt.Status != "accepted" ||
				attempt.PlanOperationID != action.OperationID {
				continue
			}
			accepted, candidateErr := s.store.GetCandidateSetByAttempt(ctx, attempt.ID)
			if errors.Is(candidateErr, cserrors.ErrNotFound) {
				continue
			}
			if candidateErr != nil {
				return "", "", false, fmt.Errorf("inspect accepted validation candidate: %w", candidateErr)
			}
			if accepted.Status != "accepted" ||
				accepted.GenerationInputHash != action.GenerationInputHash ||
				accepted.DependencyContractHash != action.DependencyContractHash {
				continue
			}
			failures, failureErr := s.store.ListFailureClassificationsByAttempt(ctx, attempt.ID)
			if failureErr != nil {
				return "", "", false, fmt.Errorf("inspect handoffless validation failure: %w", failureErr)
			}
			for failureIndex := len(failures) - 1; failureIndex >= 0; failureIndex-- {
				classified := failures[failureIndex]
				handofflessCategory :=
					(classified.Category == string(execution.FailureInvalidValidation) && classified.BlocksRetry) ||
						classified.Category == string(execution.FailureIntegrationError)
				if !handofflessCategory || classified.EvidenceSource != "affected_closure_validation" ||
					classified.ResolvedAt == nil ||
					classified.ResolvedByAttempt != "" || classified.Resolution == "" {
					continue
				}
				source = attempt
				candidate = accepted
				failure = &classified
				break
			}
			if candidate != nil && failure != nil {
				break
			}
		}
	}
	if source.ID == "" || candidate == nil || failure == nil {
		return "", "", false, nil
	}
	resolvedByEligible := failure.ResolvedByAttempt == ""
	if failure.ResolvedByAttempt != "" {
		resolvedByEligible = handoff != nil && s.acceptedCrossOwnerResolver(
			ctx, resource.SessionID, resource.ResourceID, failure.ResolvedByAttempt, attemptByID,
		)
	}
	if source.PlanOperationID != action.OperationID ||
		failure.AttemptID != source.ID || failure.EvidenceSource != "affected_closure_validation" ||
		failure.ResolvedAt == nil || !resolvedByEligible || failure.Resolution == "" ||
		!reinstatementCandidateMatchesOperation(candidate, action, current) {
		return "", "", false, nil
	}

	paths := make([]string, 0, len(candidate.Files))
	for _, file := range candidate.Files {
		paths = append(paths, file.Path)
	}
	sort.Strings(paths)
	release, err := s.commitCoordinator().Acquire(ctx, paths)
	if err != nil {
		return "", "", false, fmt.Errorf("coordinate accepted candidate reinstatement: %w", err)
	}
	defer release()
	// Match CommitAttempt's lock order: the fine-grained in-process path
	// coordinator is always acquired before the coarse OS-backed project lock.
	// The project lock must span both repository/ownership verification and the
	// durable reinstatement transaction; otherwise another CrestSpec process
	// can publish a shared-file commit in the gap between those two operations.
	releaseProject, err := s.projectWorkspace().AcquireCommit(ctx)
	if err != nil {
		return "", "", false, fmt.Errorf("lock project for accepted candidate reinstatement: %w", err)
	}
	defer releaseProject()
	if _, err := s.recoverWorkspaceCommitsLocked(ctx); err != nil {
		return "", "", false, fmt.Errorf("recover interrupted workspace commits before accepted candidate reinstatement: %w", err)
	}
	matches, supersededFiles, err := s.candidateMatchesReconciledWorkspace(
		ctx, resource.SessionID, resource.ResourceID, candidate, attempts,
	)
	if err != nil {
		return "", "", false, fmt.Errorf("verify accepted candidate reinstatement: %w", err)
	}
	if !matches {
		return "", "", false, nil
	}
	handoffID := ""
	if handoff != nil {
		handoffID = handoff.ID
	}
	reinstated, err := s.store.ReinstateAcceptedSessionResource(ctx, store.AcceptedCandidateReinstatement{
		SessionID: resource.SessionID, ResourceID: resource.ResourceID,
		SourceAttemptID: source.ID, PlanOperationID: source.PlanOperationID,
		CandidateID: candidate.ID, CandidateHash: candidate.CandidateHash,
		GenerationInputHash:    candidate.GenerationInputHash,
		DependencyContractHash: candidate.DependencyContractHash,
		HandoffID:              handoffID, FailureID: failure.ID,
		FailureCategory: failure.Category, Reason: failure.Resolution,
		ResolverAttemptID: failure.ResolvedByAttempt,
		SupersededFiles:   supersededFiles,
	})
	if err != nil {
		return "", "", false, err
	}
	return candidate.ID, handoffID, reinstated, nil
}

// reinstatementCandidateMatchesOperation proves the candidate against the
// immutable session operation first, then proves that operation against the
// current registry. This preserves exact accepted legacy-V2 provenance across
// the hash-projection migration without treating a legacy candidate hash as a
// current V3 hash. Forged, tampered, or genuinely stale operations still fail
// through operationInputsMatchCurrent.
func reinstatementCandidateMatchesOperation(
	candidate *store.CandidateSet,
	action *planpkg.PlannedAction,
	current *PlanResult,
) bool {
	if candidate == nil || action == nil || candidate.Status != "accepted" ||
		candidate.GenerationInputHash == "" || candidate.DependencyContractHash == "" {
		return false
	}
	return candidate.GenerationInputHash == action.GenerationInputHash &&
		candidate.DependencyContractHash == action.DependencyContractHash &&
		operationInputsMatchCurrent(*action, current)
}

// reinstatementContextHandoff preserves the ordinary pending repair path and
// additionally recognizes one exact accepted handoff whose target never
// progressed beyond pre-execution context preparation. The accepted path is
// deliberately limited to an explicit cross-owner resolution; all atomic
// target-lifecycle checks are repeated by the store transaction.
func (s *Spec) reinstatementContextHandoff(
	ctx context.Context,
	sessionID string,
	resourceID string,
	attempts []store.GenerationAttempt,
	attemptByID map[string]store.GenerationAttempt,
) (*store.AttemptHandoff, error) {
	pending, err := s.pendingContextHandoff(ctx, sessionID, resourceID)
	if err != nil || pending != nil {
		return pending, err
	}

	var eligible []store.AttemptHandoff
	for _, source := range attempts {
		if source.SessionID != sessionID || source.ResourceID != resourceID || source.Status != "accepted" {
			continue
		}
		handoffs, listErr := s.store.ListAttemptHandoffsBySource(ctx, source.ID)
		if listErr != nil {
			return nil, fmt.Errorf("list accepted validation handoffs: %w", listErr)
		}
		for _, handoff := range handoffs {
			if handoff.Status != "accepted" || handoff.FailureID == "" || handoff.TargetAttemptID == "" {
				continue
			}
			target := attemptByID[handoff.TargetAttemptID]
			if target.ID == "" || target.SessionID != sessionID || target.ResourceID != resourceID ||
				(target.Status != "context_prepared" && target.Status != "abandoned") {
				continue
			}
			failure, failureErr := s.store.GetFailureClassification(ctx, handoff.FailureID)
			if failureErr != nil {
				return nil, fmt.Errorf("inspect accepted validation handoff failure: %w", failureErr)
			}
			if failure.AttemptID != source.ID || failure.ResolvedAt == nil || failure.Resolution == "" ||
				failure.ResolvedByAttempt == "" ||
				!s.acceptedCrossOwnerResolver(ctx, sessionID, resourceID, failure.ResolvedByAttempt, attemptByID) {
				continue
			}
			eligible = append(eligible, handoff)
		}
	}
	if len(eligible) > 1 {
		return nil, fmt.Errorf(
			"reinstate accepted candidate: ambiguous accepted handoff provenance for resource %s: matches=%d",
			resourceID, len(eligible),
		)
	}
	if len(eligible) == 1 {
		return &eligible[0], nil
	}
	return nil, nil
}

func (s *Spec) acceptedCrossOwnerResolver(
	ctx context.Context,
	sessionID string,
	resourceID string,
	resolverAttemptID string,
	attemptByID map[string]store.GenerationAttempt,
) bool {
	resolver := attemptByID[resolverAttemptID]
	if resolver.ID == "" || resolver.SessionID != sessionID || resolver.ResourceID == resourceID ||
		resolver.Status != "accepted" {
		return false
	}
	executionManifest, err := s.store.GetExecutionManifestByAttempt(ctx, resolver.ID)
	if err != nil || executionManifest.Status != string(execution.StatusAccepted) {
		return false
	}
	candidate, err := s.store.GetCandidateSetByAttempt(ctx, resolver.ID)
	return err == nil && candidate.Status == "accepted"
}

// candidateMatchesReconciledWorkspace is intentionally narrower than the
// normal candidate-reuse check. It exists only for repairing an already-active
// session poisoned by the legacy validation reopen loop. Historical extra
// generated_files rows do not invalidate an otherwise exact candidate. A
// candidate file that differs in the current workspace is accepted only when
// those exact current bytes occur in a later accepted candidate from this same
// session; the store transaction rechecks that exception before reinstatement.
func (s *Spec) candidateMatchesReconciledWorkspace(
	ctx context.Context,
	sessionID string,
	resourceID string,
	candidate *store.CandidateSet,
	attempts []store.GenerationAttempt,
) (bool, map[string]string, error) {
	if sessionID == "" || candidate == nil || candidate.ID == "" || candidate.Status != "accepted" ||
		candidate.GenerationInputHash == "" || candidate.DependencyContractHash == "" ||
		candidate.DisposedAt == nil {
		return false, nil, nil
	}
	owned, err := s.store.GetGeneratedFiles(resourceID)
	if err != nil {
		return false, nil, err
	}
	ownedHashes := make(map[string]string, len(owned))
	for _, file := range owned {
		ownedHashes[file.Path] = file.ContentHash
	}

	// Build immutable later-acceptance evidence once. Missing candidate sets are
	// normal for prepared/abandoned attempts and are ignored.
	laterHashes := make(map[string]map[string]bool)
	for _, attempt := range attempts {
		if attempt.SessionID != sessionID || attempt.ID == candidate.AttemptID || attempt.Status != "accepted" {
			continue
		}
		later, candidateErr := s.store.GetCandidateSetByAttempt(ctx, attempt.ID)
		if errors.Is(candidateErr, cserrors.ErrNotFound) {
			continue
		}
		if candidateErr != nil {
			return false, nil, candidateErr
		}
		if later.Status != "accepted" || later.DisposedAt == nil ||
			!later.DisposedAt.After(*candidate.DisposedAt) {
			continue
		}
		for _, file := range later.Files {
			if laterHashes[file.Path] == nil {
				laterHashes[file.Path] = make(map[string]bool)
			}
			laterHashes[file.Path][file.ContentHash] = true
		}
	}

	superseded := make(map[string]string)
	for _, file := range candidate.Files {
		// The candidate-file acceptance is immutable provenance, while this row
		// proves the resource still holds the projected ownership. Extra rows are
		// harmless legacy bookkeeping and are deliberately ignored.
		if ownedHashes[file.Path] != file.ContentHash {
			return false, nil, nil
		}
		path, err := s.projectFilePath(file.Path)
		if err != nil {
			return false, nil, err
		}
		content, err := s.fs.ReadFile(path)
		if err != nil {
			return false, nil, nil
		}
		currentHash := contextmanifest.Hash(string(content))
		if currentHash == file.ContentHash {
			continue
		}
		if !laterHashes[file.Path][currentHash] {
			return false, nil, nil
		}
		superseded[file.Path] = currentHash
	}
	return true, superseded, nil
}

func resolvedReinstatementCandidate(resources []store.SessionResource) bool {
	for index := range resources {
		if recoverableValidationProjection(&resources[index]) {
			return true
		}
	}
	return false
}

func (s *Spec) reconcilePoisonedValidationResources(
	ctx context.Context,
	resources []store.SessionResource,
	actions []planpkg.PlannedAction,
) (bool, error) {
	if !resolvedReinstatementCandidate(resources) {
		return false, nil
	}
	current, err := s.Plan(ctx)
	if err != nil {
		return false, fmt.Errorf("reconcile poisoned validation resources: plan: %w", err)
	}
	actionByResource := make(map[string]*planpkg.PlannedAction, len(actions))
	for index := range actions {
		actionByResource[actions[index].ResourceID] = &actions[index]
	}
	changed := false
	for index := range resources {
		reconciled, reconcileErr := s.reconcilePoisonedValidationResource(
			ctx, &resources[index], actionByResource[resources[index].ResourceID], current,
		)
		if reconcileErr != nil {
			return false, reconcileErr
		}
		changed = changed || reconciled.changed
	}
	return changed, nil
}
