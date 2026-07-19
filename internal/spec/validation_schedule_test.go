package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/require"
)

func TestScheduleIntegrationValidationWaitsForFinalAffectedWave(t *testing.T) {
	resources := []store.SessionResource{
		{ResourceID: "provider", State: "committed", WaveIndex: 0},
		{ResourceID: "consumer", State: "pending", WaveIndex: 1},
	}

	providerWave := scheduleIntegrationValidation(resources, 0)
	require.False(t, providerWave.Ready)
	require.Equal(t, 1, providerWave.GateWave)
	require.Equal(t, []string{"consumer"}, providerWave.PendingResources)
	require.Contains(t, providerWave.Reason, "downstream consumers may still be stale")

	resources[1].State = "committed"
	consumerWave := scheduleIntegrationValidation(resources, 1)
	require.True(t, consumerWave.Ready)
	require.Empty(t, consumerWave.PendingResources)
}

func TestScheduleIntegrationValidationIgnoresLaterTerminalFastPathWaves(t *testing.T) {
	resources := []store.SessionResource{
		{ResourceID: "provider", State: "committed", WaveIndex: 0, ExecutionStrategy: "generate"},
		{ResourceID: "consumer", State: "committed", WaveIndex: 1, ExecutionStrategy: "validate_existing"},
		{ResourceID: "shell", State: "committed", WaveIndex: 2, ExecutionStrategy: "validate_existing"},
	}

	schedule := scheduleIntegrationValidation(resources, 0)
	require.True(t, schedule.Ready)
	require.Equal(t, 0, schedule.GateWave,
		"a terminal optimistic-only wave must not require an unreachable model dispatch")
}

func TestNextSurfacesValidationReadyForAllFastPathSession(t *testing.T) {
	s, sessionID, _, _ := newTestSpecWithTwoWaveSessionCUE(t, twoWaveSpecCUE)
	resources, err := s.store.ListSessionResources(sessionID)
	require.NoError(t, err)
	for _, resource := range resources {
		resource.State = "committed"
		resource.Phase = "integration_pending"
		resource.ExecutionStrategy = "validate_existing"
		require.NoError(t, s.store.UpsertSessionResource(resource))
	}

	next, err := s.Next(context.Background(), sessionID)
	require.NoError(t, err)
	require.False(t, next.Done)
	require.True(t, next.ValidationReady)
	require.Equal(t, 1, next.ValidationWaveIndex)
	require.Empty(t, next.Resources, "closure validation must not require a dummy worker")
}

func TestVerifyWaveDefersProjectGatesUntilAffectedBatchIsMigrated(t *testing.T) {
	const projectValidation = `
project: contexts: Audio: valueObjects: Tone: validations: [{
	kind: "custom"
	command: ["sh", "-c", "test -f src/consumer.go && printf resource >> integration-gates.log"]
}]
project: contexts: Audio: domainServices: Mixer: validations: [{
	kind: "custom"
	command: ["sh", "-c", "test -f src/consumer.go && printf resource >> integration-gates.log"]
}]
project: validations: [{
	kind: "custom"
	command: ["sh", "-c", "printf project >> integration-gates.log"]
}]
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(t, twoWaveSpecCUE+projectValidation)
	ctx := context.Background()
	projectRoot := filepath.Dir(s.cfg.SpecDir)
	gateLog := filepath.Join(projectRoot, "integration-gates.log")
	s.cfg.TypeCheckCommand = "printf typecheck >> integration-gates.log"

	providerCommit := commitValidationResource(t, s, sessionID, providerID, "provider.go")
	require.True(t, providerCommit.ValidationDeferred)
	require.Empty(t, providerCommit.Validations)
	providerResult := s.VerifyWave(ctx, sessionID, 0)
	require.True(t, providerResult.Passed)
	require.True(t, providerResult.Deferred)
	require.False(t, providerResult.IntegrationReady)
	require.False(t, providerResult.IntegrationValidated)
	require.Equal(t, 1, providerResult.IntegrationGateWave)
	require.Equal(t, []string{consumerID}, providerResult.PendingResources)
	_, err := os.Stat(gateLog)
	require.ErrorIs(t, err, os.ErrNotExist, "project gates must not run against stale downstream consumers")

	providerStatus, err := s.WaveStatus(ctx, sessionID, 0)
	require.NoError(t, err)
	require.Equal(t, "integration_pending", providerStatus.Resources[0].Phase)

	consumerCommit := commitValidationResource(t, s, sessionID, consumerID, "consumer.go")
	require.True(t, consumerCommit.ValidationDeferred)
	consumerResult := s.VerifyWave(ctx, sessionID, 1)
	require.True(t, consumerResult.Passed)
	require.False(t, consumerResult.Deferred)
	require.True(t, consumerResult.IntegrationReady)
	require.True(t, consumerResult.IntegrationValidated)
	require.Empty(t, consumerResult.PendingResources)
	require.Equal(t, 2, consumerResult.ResourceValidationDefinitions)
	require.Equal(t, 1, consumerResult.ResourceValidationCommands, "identical resource commands should execute once")

	gateOutput, err := os.ReadFile(gateLog)
	require.NoError(t, err)
	require.Equal(t, "resourcetypecheckproject", string(gateOutput))
	for wave := 0; wave <= 1; wave++ {
		status, statusErr := s.WaveStatus(ctx, sessionID, wave)
		require.NoError(t, statusErr)
		for _, resource := range status.Resources {
			require.Equal(t, "verified", resource.Phase)
		}
	}
}

func TestAffectedClosureDeduplicatesEquivalentResourceValidationsAndFansOutEvidence(t *testing.T) {
	const equivalentValidations = `
project: contexts: Audio: valueObjects: Tone: validations: [
	{
		kind: "custom"
		command: ["sh", "-c", "printf A >> validation-batch.log"]
		workingDirectory: "."
		timeout: "300s"
		environment: ["CREST_BATCH_B", "CREST_BATCH_A"]
	},
	{
		kind: "custom"
		command: ["sh", "-c", "printf B >> validation-batch.log"]
	},
]
project: contexts: Audio: domainServices: Mixer: validations: [
	{
		kind: "custom"
		command: ["sh", "-c", "printf A >> validation-batch.log"]
		workingDirectory: ""
		timeout: "5m"
		environment: ["CREST_BATCH_A", "CREST_BATCH_B"]
	},
	{
		kind: "test"
		command: ["sh", "-c", "printf A >> validation-batch.log"]
		workingDirectory: "."
		timeout: "5m"
		environment: ["CREST_BATCH_A", "CREST_BATCH_B"]
	},
]
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(t, twoWaveSpecCUE+equivalentValidations)
	ctx := context.Background()
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(ctx, sessionID, 1)
	require.True(t, result.Passed)
	require.Equal(t, 4, result.ResourceValidationDefinitions)
	require.Equal(t, 3, result.ResourceValidationCommands,
		"equivalent commands should run once while a distinct command and validation kind remain separate")

	batchLog, err := os.ReadFile(filepath.Join(filepath.Dir(s.cfg.SpecDir), "validation-batch.log"))
	require.NoError(t, err)
	require.Equal(t, "ABA", string(batchLog),
		"the equivalent custom command must produce its side effect exactly once")

	plan, err := s.Plan(ctx)
	require.NoError(t, err)
	providerValidations := resourceValidations(plan.Registry.Resources[providerID])
	consumerValidations := resourceValidations(plan.Registry.Resources[consumerID])
	require.Len(t, providerValidations, 2)
	require.Len(t, consumerValidations, 2)

	runs, err := s.store.ListValidationRuns(ctx, 20)
	require.NoError(t, err)
	require.Len(t, runs, 4, "every declaring definition needs independently queryable evidence")
	runsByDefinition := make(map[string]store.ValidationRun, len(runs))
	for _, run := range runs {
		runsByDefinition[run.DefinitionID] = run
		require.Len(t, run.Executions, 1)
	}
	providerRun, ok := runsByDefinition[providerValidations[0].ID]
	require.True(t, ok)
	consumerRun, ok := runsByDefinition[consumerValidations[0].ID]
	require.True(t, ok)
	require.Equal(t, providerRun.Executions[0].CommandHash, consumerRun.Executions[0].CommandHash)
	require.Equal(t, providerRun.Executions[0].SourceTreeHash, consumerRun.Executions[0].SourceTreeHash)
	require.Equal(t, providerRun.Executions[0].StartedAt, consumerRun.Executions[0].StartedAt,
		"alias evidence must point at the same physical command execution")
	require.Equal(t, providerRun.Executions[0].CompletedAt, consumerRun.Executions[0].CompletedAt)
}

func TestVerifyWaveDeduplicatesEquivalentCommandsAcrossEveryValidationSource(t *testing.T) {
	const sharedCommand = "printf X >> unified-validation.log"
	const sharedValidations = `
project: contexts: Audio: valueObjects: Tone: validations: [{
	kind: "compiles"
	command: ["sh", "-c", "printf X >> unified-validation.log"]
}]
project: contexts: Audio: domainServices: Mixer: validations: [{
	kind: "compiles"
	command: ["sh", "-c", "printf X >> unified-validation.log"]
}]
project: validations: {
	shared_compile: {
		scope: "project"
		kind: "compiles"
		command: ["sh", "-c", "printf X >> unified-validation.log"]
	}
	shared_test: {
		scope: "project"
		kind: "test"
		command: ["sh", "-c", "printf X >> unified-validation.log"]
	}
}
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(t, twoWaveSpecCUE+sharedValidations)
	s.cfg.TypeCheckCommand = sharedCommand
	s.cfg.TestCommand = sharedCommand
	ctx := context.Background()
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(ctx, sessionID, 1)
	require.True(t, result.Passed)
	require.Equal(t, 2, result.ResourceValidationDefinitions)
	require.Equal(t, 1, result.ResourceValidationCommands)
	require.Equal(t, 6, result.ValidationAliases)
	require.Equal(t, 2, result.ValidationCommands)
	batchLog, err := os.ReadFile(filepath.Join(filepath.Dir(s.cfg.SpecDir), "unified-validation.log"))
	require.NoError(t, err)
	require.Equal(t, "XX", string(batchLog),
		"resource, configured, and project aliases should share one execution per validation kind")

	plan, err := s.Plan(ctx)
	require.NoError(t, err)
	providerDefinition := resourceValidations(plan.Registry.Resources[providerID])[0]
	consumerDefinition := resourceValidations(plan.Registry.Resources[consumerID])[0]
	var projectCompileDefinition, projectTestDefinition string
	for _, definition := range plan.Registry.Project.Validations {
		switch definition.Kind {
		case "compiles":
			projectCompileDefinition = definition.ID
		case "test":
			projectTestDefinition = definition.ID
		}
	}
	require.NotEmpty(t, projectCompileDefinition)
	require.NotEmpty(t, projectTestDefinition)

	runs, err := s.store.ListValidationRuns(ctx, 20)
	require.NoError(t, err)
	require.Len(t, runs, 4, "every CUE definition retains evidence even when its execution is shared")
	runsByDefinition := make(map[string]store.ValidationRun, len(runs))
	for _, run := range runs {
		runsByDefinition[run.DefinitionID] = run
		require.Len(t, run.Executions, 1)
	}
	compileRuns := []store.ValidationRun{
		runsByDefinition[providerDefinition.ID],
		runsByDefinition[consumerDefinition.ID],
		runsByDefinition[projectCompileDefinition],
	}
	for _, run := range compileRuns {
		require.Equal(t, compileRuns[0].Executions[0].CommandHash, run.Executions[0].CommandHash)
		require.Equal(t, compileRuns[0].Executions[0].SourceTreeHash, run.Executions[0].SourceTreeHash)
		require.Equal(t, compileRuns[0].Executions[0].StartedAt, run.Executions[0].StartedAt)
		require.Equal(t, compileRuns[0].Executions[0].CompletedAt, run.Executions[0].CompletedAt)
	}
	require.NotEqual(t,
		compileRuns[0].Executions[0].StartedAt,
		runsByDefinition[projectTestDefinition].Executions[0].StartedAt,
		"different validation kinds remain distinct execution contracts",
	)
}

func TestUnifiedValidationSchedulePreservesInfrastructureClassificationAcrossAliases(t *testing.T) {
	const missingToolCommand = "printf I >> unified-infrastructure.log; crest-unified-missing-tool"
	const sharedInfrastructureValidation = `
project: contexts: Audio: valueObjects: Tone: validations: [{
	kind: "compiles"
	command: ["sh", "-c", "printf I >> unified-infrastructure.log; crest-unified-missing-tool"]
}]
project: contexts: Audio: domainServices: Mixer: validations: [{
	kind: "compiles"
	command: ["sh", "-c", "printf I >> unified-infrastructure.log; crest-unified-missing-tool"]
}]
project: validations: shared_infrastructure: {
	scope: "project"
	kind: "compiles"
	command: ["sh", "-c", "printf I >> unified-infrastructure.log; crest-unified-missing-tool"]
}
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(t, twoWaveSpecCUE+sharedInfrastructureValidation)
	s.cfg.TypeCheckCommand = missingToolCommand
	ctx := context.Background()
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(ctx, sessionID, 1)
	require.False(t, result.Passed)
	require.Equal(t, 4, result.ValidationAliases)
	require.Equal(t, 1, result.ValidationCommands)
	require.Empty(t, result.ReopenedResources, "an unavailable validator must never charge a source repair")
	require.Len(t, result.Errors, 1)
	for _, validationError := range result.Errors {
		require.Equal(t, "validation_infrastructure", validationError.Kind)
		require.Len(t, validationError.ValidationIDs, 4)
		require.Equal(t, []string{"compiles"}, validationError.ValidationKinds)
	}

	batchLog, err := os.ReadFile(filepath.Join(filepath.Dir(s.cfg.SpecDir), "unified-infrastructure.log"))
	require.NoError(t, err)
	require.Equal(t, "I", string(batchLog), "all aliases must share the one failed launch")

	runs, err := s.store.ListValidationRuns(ctx, 20)
	require.NoError(t, err)
	require.Len(t, runs, 3)
	for _, run := range runs {
		require.Equal(t, "error", run.Classification)
		require.Len(t, run.Executions, 1)
	}
}

func TestUnifiedValidationFailureFansOutAliasCategoriesAndOwnersWithoutRerunning(t *testing.T) {
	const failingCommand = "printf F >> unified-failure.log; echo shared failure >&2; exit 1"
	const sharedFailureValidation = `
project: contexts: Audio: valueObjects: Tone: validations: [{
	kind: "compiles"
	command: ["sh", "-c", "printf F >> unified-failure.log; echo shared failure >&2; exit 1"]
}]
project: contexts: Audio: domainServices: Mixer: validations: [{
	kind: "compiles"
	command: ["sh", "-c", "printf F >> unified-failure.log; echo shared failure >&2; exit 1"]
}]
project: validations: shared_failure: {
	scope: "project"
	kind: "compiles"
	command: ["sh", "-c", "printf F >> unified-failure.log; echo shared failure >&2; exit 1"]
	resources: ["valueObject.Audio.Tone"]
}
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(t, twoWaveSpecCUE+sharedFailureValidation)
	s.cfg.TypeCheckCommand = failingCommand
	ctx := context.Background()
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(ctx, sessionID, 1)
	require.False(t, result.Passed)
	require.Equal(t, 4, result.ValidationAliases)
	require.Equal(t, 1, result.ValidationCommands)
	require.Len(t, result.Errors, 3, "one physical diagnostic fans out to each legacy error category")
	errorsByKind := make(map[string]WaveError, len(result.Errors))
	for _, validationError := range result.Errors {
		errorsByKind[validationError.Kind] = validationError
	}
	require.ElementsMatch(t, []string{providerID, consumerID}, errorsByKind["resource_validation"].ResourceIDs)
	require.Empty(t, errorsByKind["type_check"].ResourceIDs,
		"a configured gate with no diagnostic span or declared repair owners must not duplicate the resource aliases' charges")
	require.Equal(t, []string{providerID}, errorsByKind["project_validation"].ResourceIDs)
	require.Len(t, errorsByKind["resource_validation"].ValidationIDs, 2)
	require.Equal(t, []string{"configuration.type_check"}, errorsByKind["type_check"].ValidationIDs)
	require.Len(t, errorsByKind["project_validation"].ValidationIDs, 1)
	require.ElementsMatch(t, []string{providerID, consumerID}, result.ReopenedResources)

	batchLog, err := os.ReadFile(filepath.Join(filepath.Dir(s.cfg.SpecDir), "unified-failure.log"))
	require.NoError(t, err)
	require.Equal(t, "F", string(batchLog))
	runs, err := s.store.ListValidationRuns(ctx, 20)
	require.NoError(t, err)
	require.Len(t, runs, 3)
	for _, run := range runs {
		require.Equal(t, "failed", run.Classification)
	}
}

func TestAffectedClosureDeduplicatedFailureFansOutToEveryDeclaringOwner(t *testing.T) {
	const duplicatedFailure = `
project: contexts: Audio: valueObjects: Tone: validations: [{
	kind: "custom"
	command: ["sh", "-c", "printf failed >> validation-failure.log; echo shared failure >&2; exit 1"]
}]
project: contexts: Audio: domainServices: Mixer: validations: [{
	kind: "custom"
	command: ["sh", "-c", "printf failed >> validation-failure.log; echo shared failure >&2; exit 1"]
}]
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(t, twoWaveSpecCUE+duplicatedFailure)
	ctx := context.Background()
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(ctx, sessionID, 1)
	require.False(t, result.Passed)
	require.Equal(t, 2, result.ResourceValidationDefinitions)
	require.Equal(t, 1, result.ResourceValidationCommands)
	require.Len(t, result.Errors, 1)
	require.ElementsMatch(t, []string{providerID, consumerID}, result.Errors[0].ResourceIDs)
	require.ElementsMatch(t, []string{providerID, consumerID}, result.ReopenedResources)

	batchLog, err := os.ReadFile(filepath.Join(filepath.Dir(s.cfg.SpecDir), "validation-failure.log"))
	require.NoError(t, err)
	require.Equal(t, "failed", string(batchLog), "the shared failing command must execute exactly once")
	runs, err := s.store.ListValidationRuns(ctx, 20)
	require.NoError(t, err)
	require.Len(t, runs, 2, "both owners retain provenance for the shared execution")
	for _, run := range runs {
		require.Equal(t, sessionID, run.SessionID)
		require.Equal(t, "failed", run.Classification)
		require.Len(t, run.Executions, 1)
	}
}

func TestAffectedClosureFailureReopensEveryMatchedOwnerTogether(t *testing.T) {
	const failingValidation = `
project: contexts: Audio: valueObjects: Tone: validations: [{
	kind: "custom"
	command: ["sh", "-c", "printf './provider.go:1:1: failed\\n./consumer.go:1:1: failed\\n' >&2; exit 1"]
}]
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(t, twoWaveSpecCUE+failingValidation)
	ctx := context.Background()
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(ctx, sessionID, 1)
	require.False(t, result.Passed)
	require.True(t, result.IntegrationValidated)
	require.ElementsMatch(t, []string{providerID, consumerID}, result.ReopenedResources)
	require.NotEmpty(t, result.Errors)
	require.ElementsMatch(t, []string{providerID, consumerID}, result.Errors[0].ResourceIDs)

	for _, resourceID := range []string{providerID, consumerID} {
		resource, err := s.store.GetSessionResource(sessionID, resourceID)
		require.NoError(t, err)
		require.Equal(t, "pending", resource.State)
		require.Equal(t, "integration_failed", resource.Phase)
		require.Contains(t, resource.LastError, "./provider.go:1:1")
		require.Contains(t, resource.LastError, "./consumer.go:1:1")
	}
	next, err := s.Next(ctx, sessionID)
	require.NoError(t, err)
	require.False(t, next.Done)
	require.Equal(t, 0, next.WaveIndex, "the complete failed owner set should re-enter dependency order")
}

func TestAffectedClosureRunsDistinctFailingDefinitionsAndReopensAllOwners(t *testing.T) {
	const failingValidations = `
project: contexts: Audio: valueObjects: Tone: validations: [{
	kind: "custom"
	command: ["sh", "-c", "echo provider failure >&2; exit 1"]
}]
project: contexts: Audio: domainServices: Mixer: validations: [{
	kind: "custom"
	command: ["sh", "-c", "echo consumer failure >&2; exit 1"]
}]
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(t, twoWaveSpecCUE+failingValidations)
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(context.Background(), sessionID, 1)
	require.False(t, result.Passed)
	require.Len(t, result.Errors, 2, "all distinct failing commands should report in one closure batch")
	require.ElementsMatch(t, []string{providerID, consumerID}, result.ReopenedResources)
	require.Equal(t, 2, result.ResourceValidationCommands)
}

func TestProjectValidationUsesDeclaredRepairOwnersWhenOutputIsUnattributed(t *testing.T) {
	const failingValidation = `
project: validations: [{
	kind: "custom"
	command: ["false"]
	resources: ["valueObject.Audio.Tone"]
}]
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(t, twoWaveSpecCUE+failingValidation)
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(context.Background(), sessionID, 1)
	require.False(t, result.Passed)
	require.Equal(t, []string{providerID}, result.ReopenedResources)
	require.Equal(t, []string{providerID}, result.Errors[0].ResourceIDs)
}

func TestAssertionOnlyProjectFailureUnionsDeclaredAndDiagnosticOwnersWithoutClosureFanout(t *testing.T) {
	const assertionOnlyValidation = `
project: contexts: Audio: valueObjects: Unrelated: {from: "f64"}
project: validations: assertion_owner_routing: {
	scope: "project"
	kind: "integration"
	command: ["sh", "-c", "printf actual; printf 'src/consumer.go:7:3: diagnostic\\n' >&2"]
	assertions: [{kind: "stdout_contains", pattern: "expected"}]
	resources: ["valueObject.Audio.Tone"]
}
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(
		t, twoWaveSpecCUE+assertionOnlyValidation,
	)
	unrelatedID := "valueObject.Audio.Unrelated"
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, unrelatedID, "unrelated.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(context.Background(), sessionID, 1)
	require.False(t, result.Passed)
	require.True(t, result.IntegrationValidated)
	require.ElementsMatch(t, []string{providerID, consumerID}, result.ReopenedResources,
		"an assertion-only failure needs both its explicit repair contract and any structured diagnostic owner")
	require.Len(t, result.Errors, 1)
	require.Equal(t, "project_validation", result.Errors[0].Kind)
	require.ElementsMatch(t, []string{providerID, consumerID}, result.Errors[0].ResourceIDs)

	unrelated, err := s.store.GetSessionResource(sessionID, unrelatedID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), unrelated.State,
		"declared repair owners and structured diagnostics must not fan out to the entire project closure")
	require.Equal(t, "integration_pending", unrelated.Phase,
		"the untouched closure participant remains committed without being classified as a failed owner")
}

func TestUnattributedTypeCheckFailureRequiresTriageWithoutReopeningClosure(t *testing.T) {
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSession(t)
	s.cfg.TypeCheckCommand = "false"
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(context.Background(), sessionID, 1)
	require.False(t, result.Passed)
	require.Empty(t, result.ReopenedResources,
		"a validator with no diagnostic span or declared repair owner must not charge the whole closure")
	require.Empty(t, result.Errors[0].ResourceIDs)
	for _, resourceID := range []string{providerID, consumerID} {
		resource, err := s.store.GetSessionResource(sessionID, resourceID)
		require.NoError(t, err)
		require.Equal(t, string(StateCommitted), resource.State)
		require.Equal(t, "integration_failed", resource.Phase)
	}
}

func TestStructuredRustPanicSpanReopensOnlyItsUniqueOwner(t *testing.T) {
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSession(t)
	s.cfg.TestCommand = `printf "Running unittests src/lib.rs\nthread 'demo' panicked at src/provider.rs:9:3:\n" >&2; exit 1`
	commitValidationResource(t, s, sessionID, providerID, "src/provider.rs")
	commitValidationResource(t, s, sessionID, consumerID, "src/consumer.rs")

	result := s.VerifyWave(context.Background(), sessionID, 1)
	require.False(t, result.Passed)
	require.Equal(t, []string{providerID}, result.ReopenedResources,
		"the test executable progress path must not fan a unique panic out to every lib.rs participant")
	require.Equal(t, []string{providerID}, result.Errors[0].ResourceIDs)
}

func TestUnattributedBehavioralFailureUsesResourceOwnerWithoutChargingProjectClosure(t *testing.T) {
	const sharedBehavioralFailure = `
project: contexts: Audio: domainServices: Mixer: validations: [{
	kind: "test"
	command: ["sh", "-c", "echo 'Running unittests src/lib.rs' >&2; echo 'test composed_demo ... FAILED' >&2; exit 1"]
}]
project: validations: behavioral: {
	scope: "project"
	kind: "test"
	command: ["sh", "-c", "echo 'Running unittests src/lib.rs' >&2; echo 'test composed_demo ... FAILED' >&2; exit 1"]
}
`
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSessionCUE(
		t, twoWaveSpecCUE+sharedBehavioralFailure,
	)
	commitValidationResource(t, s, sessionID, providerID, "src/provider.rs")
	commitValidationResource(t, s, sessionID, consumerID, "src/consumer.rs")

	result := s.VerifyWave(context.Background(), sessionID, 1)
	require.False(t, result.Passed)
	require.Equal(t, []string{consumerID}, result.ReopenedResources,
		"the declaring resource may repair its own behavioral test, but the unowned project alias must not charge the closure")
	require.Len(t, result.Errors, 2)
	errorsByKind := make(map[string]WaveError, len(result.Errors))
	for _, waveErr := range result.Errors {
		errorsByKind[waveErr.Kind] = waveErr
	}
	require.Equal(t, []string{consumerID}, errorsByKind["resource_validation"].ResourceIDs)
	require.Empty(t, errorsByKind["project_validation"].ResourceIDs)
}

func TestUnrunnableTypeCheckRetriesValidationWithoutReopeningSource(t *testing.T) {
	s, sessionID, providerID, consumerID := newTestSpecWithTwoWaveSession(t)
	s.cfg.TypeCheckCommand = "crest-definitely-missing-typecheck"
	commitValidationResource(t, s, sessionID, providerID, "provider.go")
	commitValidationResource(t, s, sessionID, consumerID, "consumer.go")

	result := s.VerifyWave(context.Background(), sessionID, 1)
	require.False(t, result.Passed)
	require.Empty(t, result.ReopenedResources)
	require.NotEmpty(t, result.Errors)
	require.Equal(t, "validation_infrastructure", result.Errors[0].Kind)
	for _, resourceID := range []string{providerID, consumerID} {
		resource, err := s.store.GetSessionResource(sessionID, resourceID)
		require.NoError(t, err)
		require.Equal(t, string(StateCommitted), resource.State)
		require.Equal(t, "integration_failed", resource.Phase)
	}
	next, err := s.Next(context.Background(), sessionID)
	require.NoError(t, err)
	require.True(t, next.ValidationReady)
	require.Empty(t, next.Resources)
}

func TestFinishCannotForcePastDeferredClosureValidation(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	commit := commitValidationResource(t, s, state.sessionID, state.resourceID, "staged.go")
	require.True(t, commit.ValidationDeferred)

	finished, err := s.Finish(context.Background(), state.sessionID, true)
	require.NoError(t, err)
	require.True(t, finished.Blocked)
	require.Empty(t, finished.ValidationPending)
	require.Equal(t, []string{state.resourceID}, finished.ValidationUnverified)

	session, err := s.store.GetSession(state.sessionID)
	require.NoError(t, err)
	require.Equal(t, "active", session.Status)
}

func commitValidationResource(t *testing.T, s *Spec, sessionID, resourceID, fileName string) *CommitResult {
	t.Helper()
	result, err := commitTestAttempt(t, s, context.Background(), sessionID, resourceID, []CommitFile{{
		Path: filepath.Join("src", fileName), Content: "package generated\n",
	}}, "", "test-model")
	require.NoError(t, err)
	require.True(t, result.Committed)
	return result
}
