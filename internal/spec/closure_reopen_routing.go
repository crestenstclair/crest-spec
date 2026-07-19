package spec

import (
	"context"
	"fmt"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func (s *Spec) routeClosureValidationOwners(
	ctx context.Context,
	sessionID string,
	resourceIDs []string,
	reason string,
	resources []store.SessionResource,
	result *WaveVerifyResult,
) bool {
	if len(resourceIDs) == 0 {
		return true
	}
	maxRepairRetries := 0
	if s.cfg != nil {
		maxRepairRetries = s.cfg.WaveMaxRetries
	}
	routed, err := s.store.RouteClosureValidationFailures(ctx, store.ClosureValidationFailureRoutingInput{
		SessionID: sessionID, ResourceIDs: resourceIDs, Reason: reason,
		Category: execution.FailureIntegrationError, MaxRepairRetries: maxRepairRetries,
	})
	if err != nil {
		// The transaction rolls back before any owner becomes context-eligible.
		// Preserve committed state and make the provenance defect explicit.
		result.Errors = append(result.Errors, waveErrorForResources(
			resourceIDs, "failure_provenance",
			fmt.Sprintf("route affected-closure repairs atomically: %v", err),
		))
		s.setIntegrationValidationPhase(resources, result, "integration_failed")
		return false
	}
	result.ReopenedResources = append(result.ReopenedResources, routed.ReopenedResources...)
	for _, count := range routed.RetryCounts {
		if count > result.RetryCount {
			result.RetryCount = count
		}
	}
	for _, resourceID := range routed.ExhaustedResources {
		count := routed.RetryCounts[resourceID]
		result.Errors = append(result.Errors, waveErrorForResources(
			[]string{resourceID}, "repair_retries_exhausted",
			fmt.Sprintf(
				"affected-closure repair retries exhausted: retry_count=%d max_retries=%d; corrective_action=triage_or_amend_spec",
				count, maxRepairRetries,
			),
		))
	}
	return true
}
