package spec

import (
	"fmt"
	"sort"

	"github.com/crestenstclair/crest-spec/internal/store"
)

// IntegrationValidationSchedule separates resource-local candidate gates from
// project-wide integration gates. Project commands are ready only at the last
// affected wave and after every planned resource has reached a terminal state.
type IntegrationValidationSchedule struct {
	Ready            bool
	GateWave         int
	PendingResources []string
	Reason           string
}

func scheduleIntegrationValidation(resources []store.SessionResource, requestedWave int) IntegrationValidationSchedule {
	schedule := IntegrationValidationSchedule{GateWave: integrationValidationGateWave(resources)}
	for _, resource := range resources {
		if !ResourceState(resource.State).IsTerminal() {
			schedule.PendingResources = append(schedule.PendingResources, resource.ResourceID)
		}
	}
	sort.Strings(schedule.PendingResources)

	switch {
	case requestedWave < schedule.GateWave:
		schedule.Reason = fmt.Sprintf(
			"project-wide validation is deferred until affected wave %d; downstream consumers may still be stale",
			schedule.GateWave,
		)
	case len(schedule.PendingResources) > 0:
		schedule.Reason = "project-wide validation is deferred until every affected resource is committed or skipped"
	default:
		schedule.Ready = true
	}
	return schedule
}

// integrationValidationGateWave is the last wave that can require model
// execution. Terminal validation-only/cache resources in later graph waves are
// still included in closure validation and diagnostic attribution, but cannot
// make the gate unreachable. An all-fast-path session validates at its last
// graph wave through NextResult.ValidationReady.
func integrationValidationGateWave(resources []store.SessionResource) int {
	gateWave := -1
	lastWave := -1
	for _, resource := range resources {
		if resource.WaveIndex > lastWave {
			lastWave = resource.WaveIndex
		}
		strategy := resource.ExecutionStrategy
		if strategy == "" {
			strategy = "generate"
		}
		if strategy == "generate" && resource.WaveIndex > gateWave {
			gateWave = resource.WaveIndex
		}
	}
	if gateWave < 0 {
		return lastWave
	}
	return gateWave
}
