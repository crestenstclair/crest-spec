package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/observability"
	planmod "github.com/crestenstclair/crest-spec/internal/plan"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

const counterSpec = `package acceptance

project: {
	name:    "counter-acceptance"
	mission: "Generate and verify a bounded counter through the complete CrestSpec lifecycle"
	layers:  ["domain", "application"]
	meta: {
		language: "go"
		style:    "small idiomatic packages with explicit boundary errors"
	}

	actors: developer: {
		description: "a developer using the bounded counter"
	}
	goals: usable_counter: {
		description:  "A developer can update a counter without crossing its bounds"
		priority:     "required"
		actors:       ["actor.developer"]
		capabilities: ["capability.manage_counter"]
		requirements: ["requirement.counter_bounds"]
	}
	capabilities: manage_counter: {
		description: "Create, increment, decrement, and reset a bounded counter"
		goals:       ["goal.usable_counter"]
		acceptance: bounded_updates: {
			description: "Valid updates succeed and out-of-range updates are rejected without changing state"
			actor:       "actor.developer"
			steps: [
				{action: "increment and decrement within the configured range", observes: "the current value changes by the requested amount"},
				{action: "attempt to cross either boundary", observes: "the operation fails and the prior value is retained"},
			]
			evidence: ["evidence.counter_acceptance", "evidence.counter_behavior"]
		}
	}
	requirements: counter_bounds: {
		kind:         "functional"
		description:  "Counter state always remains between its inclusive minimum and maximum"
		goals:        ["goal.usable_counter"]
		capabilities: ["capability.manage_counter"]
	}
	evidence: counter_acceptance: {
		kind:        "integration_validation"
		description: "A black-box Go test exercises valid updates and both rejected boundaries"
		validations: ["validation.acceptance"]
	}
	evidence: counter_behavior: {
		kind:        "behavioral_witness"
		description: "A real counter run is distinguished from a controlled no-update baseline"
		witnesses:   ["witness.counter_behavior"]
	}
	completion: {
		requiredGoals: ["goal.usable_counter"]
		projectChecks: ["validation.acceptance"]
	}
	witnesses: counter_behavior: {
		scope:      "goal"
		goal:       "goal.usable_counter"
		capability: "capability.manage_counter"
		resources: ["valueObject.Counter.CounterValue", "domainService.Counter.CounterService"]
		repairResources: ["domainService.Counter.CounterService"]
		evidence:  ["evidence.counter_behavior"]
		command:         ["go", "run", "./cmd/counter-witness", "--mode=real"]
		negativeCommand: ["go", "run", "./cmd/counter-witness", "--mode=negative"]
		timeout: "30s"
		observation: {
			kind:   "json_stdout"
			marker: "CREST_OBSERVATION "
			schema: {value: "number", boundary_rejected: "bool", state_preserved: "bool"}
		}
		predicates: [
			{field: "value", op: "eq", value: 3},
			{field: "boundary_rejected", op: "equals", member: true},
			{field: "state_preserved", op: "equals", member: true},
		]
	}

	contexts: Counter: {
		purpose: "bounded counter state and operations"
		valueObjects: CounterValue: {
			state: {value: "int", min: "int", max: "int"}
			invariants: ["min must not exceed max", "value must remain between min and max"]
			contributesTo: [{capability: "capability.manage_counter", contribution: "owns bounded counter state and atomic updates"}]
		}
		domainServices: CounterService: {
			purpose: "expose increment, decrement, reset, and current value operations"
			uses: ["valueObject.Counter.CounterValue"]
			contributesTo: [{capability: "capability.manage_counter", contribution: "provides the complete bounded update workflow"}]
		}
	}

	validations: acceptance: {
		scope:        "project"
		kind:         "test"
		command:      ["go", "test", "./..."]
		description:  "black-box bounded counter behavior passes"
		resources:    ["valueObject.Counter.CounterValue", "domainService.Counter.CounterService"]
		capabilities: ["capability.manage_counter"]
		goals:        ["goal.usable_counter"]
	}
}
`

const counterAcceptanceTest = `package counter_test

import (
	"testing"

	"example.com/crestspec-acceptance/counter"
)

func TestBoundedCounterBehavior(t *testing.T) {
	service, err := counter.NewService(1, 0, 3)
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	if err := service.Increment(2); err != nil || service.Value() != 3 {
		t.Fatalf("increment within bounds: value=%d err=%v", service.Value(), err)
	}
	if err := service.Increment(1); err == nil || service.Value() != 3 {
		t.Fatalf("upper boundary must reject atomically: value=%d err=%v", service.Value(), err)
	}
	if err := service.Decrement(3); err != nil || service.Value() != 0 {
		t.Fatalf("decrement within bounds: value=%d err=%v", service.Value(), err)
	}
	if err := service.Decrement(1); err == nil || service.Value() != 0 {
		t.Fatalf("lower boundary must reject atomically: value=%d err=%v", service.Value(), err)
	}
	if err := service.Reset(2); err != nil || service.Value() != 2 {
		t.Fatalf("reset within bounds: value=%d err=%v", service.Value(), err)
	}
	if err := service.Reset(4); err == nil || service.Value() != 2 {
		t.Fatalf("out-of-range reset must retain state: value=%d err=%v", service.Value(), err)
	}
}
`

const counterDemo = `package main

import (
	"fmt"

	"example.com/crestspec-acceptance/counter"
)

func main() {
	service, err := counter.NewService(1, 0, 3)
	if err != nil {
		panic(err)
	}
	if err := service.Increment(2); err != nil {
		panic(err)
	}
	boundaryErr := service.Increment(1)
	fmt.Printf("value=%d boundary_rejected=%t\n", service.Value(), boundaryErr != nil)
}
`

const counterWitness = `package main

import (
	"fmt"
	"os"

	"example.com/crestspec-acceptance/counter"
)

func main() {
	mode := "real"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	service, err := counter.NewService(1, 0, 3)
	if err != nil {
		panic(err)
	}
	boundaryRejected := false
	statePreserved := true
	if mode == "--mode=real" {
		if err := service.Increment(2); err != nil {
			panic(err)
		}
		beforeBoundary := service.Value()
		boundaryRejected = service.Increment(1) != nil
		statePreserved = service.Value() == beforeBoundary
	} else if mode != "--mode=negative" {
		panic("mode must be --mode=real or --mode=negative")
	}

	fmt.Printf(
		"CREST_OBSERVATION {\"value\":%d,\"boundary_rejected\":%t,\"state_preserved\":%t}\n",
		service.Value(), boundaryRejected, statePreserved,
	)
}
`

const counterValueCandidate = `package counter

import "fmt"

// Value owns one integer whose updates are atomic with respect to its bounds.
type Value struct {
	value int
	min   int
	max   int
}

func NewValue(value, min, max int) (Value, error) {
	if min > max {
		return Value{}, fmt.Errorf("minimum %d exceeds maximum %d", min, max)
	}
	if value < min || value > max {
		return Value{}, fmt.Errorf("value %d is outside [%d, %d]", value, min, max)
	}
	return Value{value: value, min: min, max: max}, nil
}

func (v Value) Int() int { return v.value }

func (v *Value) Add(delta int) error {
	next := v.value + delta
	if next < v.min || next > v.max {
		return fmt.Errorf("value %d is outside [%d, %d]", next, v.min, v.max)
	}
	v.value = next
	return nil
}

func (v *Value) Reset(value int) error {
	if value < v.min || value > v.max {
		return fmt.Errorf("value %d is outside [%d, %d]", value, v.min, v.max)
	}
	v.value = value
	return nil
}
`

const counterServiceCandidate = `package counter

import "fmt"

// Service exposes positive increment/decrement operations over a bounded Value.
type Service struct {
	value Value
}

func NewService(initial, min, max int) (*Service, error) {
	value, err := NewValue(initial, min, max)
	if err != nil {
		return nil, err
	}
	return &Service{value: value}, nil
}

func (s *Service) Value() int { return s.value.Int() }

func (s *Service) Increment(amount int) error {
	if amount <= 0 {
		return fmt.Errorf("increment amount must be positive")
	}
	return s.value.Add(amount)
}

func (s *Service) Decrement(amount int) error {
	if amount <= 0 {
		return fmt.Errorf("decrement amount must be positive")
	}
	return s.value.Add(-amount)
}

func (s *Service) Reset(value int) error { return s.value.Reset(value) }
`

func TestRealBinaryApplySurvivesRestartAndProducesWorkingProject(t *testing.T) {
	repoRoot := repositoryRoot(t)
	binary := buildCrestSpecBinary(t, repoRoot)
	projectRoot := createCounterProject(t)
	hostSessionID := "acceptance-host-session"
	workerID := "deterministic-worker-1"

	server := startCrestSpec(t, binary, projectRoot)
	validation := callTool[specmod.ValidateResult](t, server, "spec/validate", map[string]any{})
	require.True(t, validation.Valid, "fixture spec must validate before applying: %+v", validation)

	begin := callTool[specmod.BeginResult](t, server, "spec/begin", map[string]any{})
	require.NotEmpty(t, begin.SessionID)
	require.Empty(t, begin.PendingDestroys)
	require.Len(t, begin.Plan, 2, "the fixture should plan exactly its two implementation resources")
	require.Len(t, begin.Waves, 2, "the service must be downstream of the value object")

	firstBatch := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": begin.SessionID,
		"limit":      1,
	})
	require.Equal(t, 0, firstBatch.WaveIndex)
	require.Len(t, firstBatch.Contexts, 1)
	require.Equal(t, "valueObject.Counter.CounterValue", firstBatch.Contexts[0].ResourceID)
	commitDeterministicCandidate(t, server, begin.SessionID, hostSessionID, workerID, firstBatch.Contexts[0], []map[string]any{{
		"path": "counter/value.go", "content": counterValueCandidate,
	}})

	firstVerification := callTool[specmod.WaveVerifyResult](t, server, "spec/verify_wave", map[string]any{
		"session_id": begin.SessionID,
		"wave_index": 0,
	})
	require.True(t, firstVerification.Passed)
	require.True(t, firstVerification.Deferred, "whole-project validation must wait for the stale downstream service")
	require.False(t, firstVerification.IntegrationValidated)

	// Simulate the real failure mode: the MCP process disappears while an apply
	// is active. No in-memory server or test store survives this boundary.
	server.crash(t)
	server = startCrestSpec(t, binary, projectRoot)

	resumedStatus := callTool[specmod.SessionStatusResult](t, server, "spec/status", map[string]any{
		"session_id": begin.SessionID,
	})
	require.Equal(t, begin.SessionID, resumedStatus.SessionID)
	require.Equal(t, 1, resumedStatus.CompletedResources)
	require.Equal(t, 1, resumedStatus.QueuedResources)

	secondBatch := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": begin.SessionID,
		"limit":      1,
	})
	require.Equal(t, 1, secondBatch.WaveIndex)
	require.Len(t, secondBatch.Contexts, 1)
	require.Equal(t, "domainService.Counter.CounterService", secondBatch.Contexts[0].ResourceID)
	commitDeterministicCandidate(t, server, begin.SessionID, hostSessionID, workerID, secondBatch.Contexts[0], []map[string]any{{
		"path": "counter/service.go", "content": counterServiceCandidate,
	}})

	finalVerification := callTool[specmod.WaveVerifyResult](t, server, "spec/verify_wave", map[string]any{
		"session_id": begin.SessionID,
		"wave_index": 1,
	})
	require.True(t, finalVerification.Passed, "real go test validation failed: %+v", finalVerification.Errors)
	require.True(t, finalVerification.IntegrationReady)
	require.True(t, finalVerification.IntegrationValidated)
	require.False(t, finalVerification.Deferred)

	completeStatus := callTool[specmod.SessionStatusResult](t, server, "spec/status", map[string]any{
		"session_id": begin.SessionID,
	})
	require.Equal(t, 2, completeStatus.CompletedResources)
	require.Equal(t, 0, completeStatus.QueuedResources)
	require.Equal(t, 1, completeStatus.WorkerPool.UniqueWorkers)
	require.Equal(t, 2, completeStatus.WorkerPool.Assignments)
	require.Equal(t, 1, completeStatus.WorkerPool.ReusedWorkers)
	require.Zero(t, completeStatus.Timing.MissingAcknowledgements)

	blockedBeforeWitness := callTool[specmod.FinishResult](t, server, "spec/finish", map[string]any{
		"session_id": begin.SessionID,
	})
	require.True(t, blockedBeforeWitness.Blocked,
		"mechanical validation alone must not complete a goal that requires behavioral evidence")
	require.Contains(t, blockedBeforeWitness.IncompleteGoals, "goal.usable_counter")
	require.NotEqual(t, "complete", blockedBeforeWitness.ProjectCompletion)

	witness := callTool[specmod.VerifyResult](t, server, "spec/verify", map[string]any{
		"witness_id": "witness.counter_behavior",
		"session_id": begin.SessionID,
	})
	require.True(t, witness.Passed, "real counter behavior did not reject its controlled baseline: %+v", witness)
	require.Equal(t, "passed", witness.Classification)
	require.False(t, witness.Theater)
	require.False(t, witness.Malformed)
	require.NotEmpty(t, witness.ValidationRunID)

	// Prove that completion depends on durable witness evidence rather than
	// process-local state: lose the MCP process after verification, then finish
	// through a fresh compiled-binary process over the same SQLite database.
	server.crash(t)
	server = startCrestSpec(t, binary, projectRoot)

	next := callTool[specmod.NextResult](t, server, "spec/next", map[string]any{"session_id": begin.SessionID})
	require.True(t, next.Done)
	finished := callTool[specmod.FinishResult](t, server, "spec/finish", map[string]any{
		"session_id": begin.SessionID,
	})
	require.False(t, finished.Blocked)
	require.Equal(t, 2, finished.Committed)
	require.Zero(t, finished.Errored)
	require.Equal(t, "complete", finished.ProjectCompletion)

	overview := callTool[observability.ProjectOverview](t, server, "spec/project_overview", map[string]any{})
	require.Equal(t, "complete", overview.Project.CompletionStatus)
	require.Empty(t, overview.Blockers)
	require.Len(t, overview.Project.Goals, 1)
	require.Equal(t, "complete", overview.Project.Goals[0].Status)
	assertDurableApplyState(t, projectRoot, begin.SessionID)

	// Change only the explicit repair-owner witness contract. This is the
	// production regression for validation-first planning: one owner receives a
	// scheduler row, no model context is created, prior evidence becomes stale,
	// and the new witness must pass before the apply can finish.
	specPath := filepath.Join(projectRoot, "spec", "spec.cue")
	specBytes, err := os.ReadFile(specPath)
	require.NoError(t, err)
	changedSpec := strings.Replace(string(specBytes), `timeout: "30s"`, `timeout: "29s"`, 1)
	require.NotEqual(t, string(specBytes), changedSpec)
	require.NoError(t, os.WriteFile(specPath, []byte(changedSpec), 0o644))

	witnessOnlyPlan := callTool[[]planmod.PlannedAction](t, server, "spec/plan", map[string]any{})
	require.Len(t, witnessOnlyPlan, 1)
	require.Equal(t, "domainService.Counter.CounterService", witnessOnlyPlan[0].ResourceID)
	require.Equal(t, planmod.ExecutionStrategyValidateExisting, witnessOnlyPlan[0].ExecutionStrategy)

	verificationBegin := callTool[specmod.BeginResult](t, server, "spec/begin", map[string]any{})
	require.NotEmpty(t, verificationBegin.SessionID)
	require.Len(t, verificationBegin.Plan, 1)
	require.Equal(t, planmod.ExecutionStrategyValidateExisting, verificationBegin.Plan[0].ExecutionStrategy)
	verificationBatch := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": verificationBegin.SessionID,
		"limit":      4,
	})
	require.Empty(t, verificationBatch.Contexts, "witness-only drift must never invoke a model worker")
	require.True(t, verificationBatch.ValidationReady)
	verificationGate := callTool[specmod.WaveVerifyResult](t, server, "spec/verify_wave", map[string]any{
		"session_id": verificationBegin.SessionID,
		"wave_index": verificationBatch.ValidationWaveIndex,
	})
	require.True(t, verificationGate.Passed, "verification-only closure gate failed: %+v", verificationGate.Errors)
	verificationStatus := callTool[specmod.SessionStatusResult](t, server, "spec/status", map[string]any{
		"session_id": verificationBegin.SessionID,
	})
	require.Zero(t, verificationStatus.WorkerPool.Assignments)

	staleEvidenceGate := callTool[specmod.FinishResult](t, server, "spec/finish", map[string]any{
		"session_id": verificationBegin.SessionID,
	})
	require.True(t, staleEvidenceGate.Blocked)
	require.NotEqual(t, "complete", staleEvidenceGate.ProjectCompletion)
	refreshedWitness := callTool[specmod.VerifyResult](t, server, "spec/verify", map[string]any{
		"witness_id": "witness.counter_behavior",
		"session_id": verificationBegin.SessionID,
	})
	require.True(t, refreshedWitness.Passed, "changed repair witness did not pass: %+v", refreshedWitness)

	server.crash(t)
	server = startCrestSpec(t, binary, projectRoot)
	verificationDone := callTool[specmod.NextResult](t, server, "spec/next", map[string]any{
		"session_id": verificationBegin.SessionID,
	})
	require.True(t, verificationDone.Done)
	verificationFinished := callTool[specmod.FinishResult](t, server, "spec/finish", map[string]any{
		"session_id": verificationBegin.SessionID,
	})
	require.False(t, verificationFinished.Blocked)
	require.Equal(t, "complete", verificationFinished.ProjectCompletion)

	server.stop(t)
	assertObservableCounterBehavior(t, projectRoot)
}

func TestRealBinaryStartupReportsDivergentWorkspaceJournalWithoutDeadlockingStdio(t *testing.T) {
	repoRoot := repositoryRoot(t)
	binary := buildCrestSpecBinary(t, repoRoot)
	projectRoot := createCounterProject(t)

	server := startCrestSpec(t, binary, projectRoot)
	validation := callTool[specmod.ValidateResult](t, server, "spec/validate", map[string]any{})
	require.True(t, validation.Valid)
	begin := callTool[specmod.BeginResult](t, server, "spec/begin", map[string]any{})
	batch := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": begin.SessionID,
		"limit":      1,
	})
	require.Len(t, batch.Contexts, 1)
	prepared := batch.Contexts[0].Context
	require.NotNil(t, prepared)

	dispatch := map[string]any{
		"dispatch_id":     prepared.DispatchID,
		"session_id":      begin.SessionID,
		"resource_id":     batch.Contexts[0].ResourceID,
		"host_session_id": "divergence-host",
		"worker_id":       "divergence-worker",
	}
	dispatch["event"] = "dispatch_requested"
	callTool[store.DispatchLifecycle](t, server, "spec/dispatch_event", dispatch)
	dispatch["event"] = "worker_acknowledged"
	callTool[store.DispatchLifecycle](t, server, "spec/dispatch_event", dispatch)
	started := callTool[store.ExecutionManifest](t, server, "spec/execution_start", map[string]any{
		"attempt_id":          prepared.AttemptID,
		"protocol_version":    execution.ProtocolVersion,
		"idempotency_key":     "divergence-" + prepared.AttemptID,
		"context_hash":        prepared.ContextHash,
		"role":                prepared.Role,
		"host_name":           "crest-spec-e2e",
		"host_version":        "1",
		"provider":            "deterministic-fixture",
		"model":               "deterministic-candidate-map",
		"host_session_id":     "divergence-host",
		"template_hashes":     prepared.TemplateHashes,
		"system_instructions": prepared.SystemPrompt,
	})
	server.stop(t)

	modulePath := filepath.Join(projectRoot, "go.mod")
	original, err := os.ReadFile(modulePath)
	require.NoError(t, err)
	moduleInfo, err := os.Stat(modulePath)
	require.NoError(t, err)
	canonicalProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	require.NoError(t, err)
	const candidateModule = "module example.com/crestspec-acceptance-candidate\n\ngo 1.23\n"

	state, err := store.New(filepath.Join(projectRoot, ".crest-spec", "state.db"))
	require.NoError(t, err)
	journal, err := state.PrepareWorkspaceCommitJournal(context.Background(), store.WorkspaceCommitJournalInput{
		ExecutionID: started.ID,
		ProjectRoot: canonicalProjectRoot,
		Files: []store.WorkspaceCommitJournalFile{{
			Path: "go.mod", OriginalExists: true, OriginalContent: original,
			OriginalMode: uint32(moduleInfo.Mode().Perm()), CandidateContent: []byte(candidateModule),
			CandidateMode: uint32(moduleInfo.Mode().Perm()), WriteIntent: "modify", Ordinal: 0,
		}},
	})
	require.NoError(t, err)
	require.NoError(t, state.Close())

	const thirdPartyModule = "module example.com/crestspec-manual-edit\n\ngo 1.23\n"
	require.NoError(t, os.WriteFile(modulePath, []byte(thirdPartyModule), moduleInfo.Mode().Perm()))

	server = startCrestSpec(t, binary, projectRoot)
	serverInfo, ok := server.initialized["serverInfo"].(map[string]any)
	require.True(t, ok)
	initializeBlocker, ok := serverInfo["recoveryBlocker"].(map[string]any)
	require.True(t, ok, "initialize must expose the startup recovery blocker: %+v", serverInfo)
	require.Equal(t, "workspace_commit_recovery", initializeBlocker["stage"])
	require.Contains(t, initializeBlocker["message"], "third-party state")

	about := callTool[map[string]any](t, server, "about", map[string]any{})
	require.Equal(t, true, about["mutation_fail_closed"])
	require.NotNil(t, about["recovery_blocker"])
	status := callTool[map[string]any](t, server, "spec/status", map[string]any{
		"session_id": begin.SessionID,
	})
	require.Equal(t, begin.SessionID, status["session_id"])
	require.NotNil(t, status["recovery_blocker"])

	blockedNext := callToolResult(t, server, "spec/next", map[string]any{
		"session_id": begin.SessionID,
	})
	require.True(t, blockedNext.IsError)
	require.Contains(t, toolResultText(blockedNext), `"error":"recovery_blocked"`)
	require.Contains(t, toolResultText(blockedNext), `"tool":"spec/next"`)
	server.stop(t)

	contents, err := os.ReadFile(modulePath)
	require.NoError(t, err)
	require.Equal(t, thirdPartyModule, string(contents),
		"diagnostic startup and blocked scheduling must never overwrite divergent bytes")
	state, err = store.New(filepath.Join(projectRoot, ".crest-spec", "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, state.Close()) })
	persistedJournal, err := state.GetWorkspaceCommitJournal(context.Background(), journal.ID)
	require.NoError(t, err)
	require.Equal(t, store.WorkspaceCommitPrepared, persistedJournal.Status)
	persistedExecution, err := state.GetExecutionManifest(context.Background(), started.ID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusExecuting), persistedExecution.Status)
}

func commitDeterministicCandidate(
	t *testing.T,
	server *crestSpecProcess,
	sessionID, hostSessionID, workerID string,
	item specmod.WaveContextItem,
	files []map[string]any,
) {
	t.Helper()
	require.Empty(t, item.Error)
	require.NotNil(t, item.Context)
	prepared := item.Context
	require.False(t, prepared.Blocked, prepared.BlockedReason)
	require.False(t, prepared.NoDispatch)
	require.NotEmpty(t, prepared.DispatchID)
	require.NotEmpty(t, prepared.AttemptID)

	dispatchArgs := map[string]any{
		"dispatch_id":     prepared.DispatchID,
		"session_id":      sessionID,
		"resource_id":     item.ResourceID,
		"host_session_id": hostSessionID,
		"worker_id":       workerID,
	}
	dispatchArgs["event"] = "dispatch_requested"
	callTool[store.DispatchLifecycle](t, server, "spec/dispatch_event", dispatchArgs)
	dispatchArgs["event"] = "worker_acknowledged"
	callTool[store.DispatchLifecycle](t, server, "spec/dispatch_event", dispatchArgs)

	manifest := callTool[store.ExecutionManifest](t, server, "spec/execution_start", map[string]any{
		"attempt_id":          prepared.AttemptID,
		"protocol_version":    execution.ProtocolVersion,
		"idempotency_key":     "acceptance-" + prepared.AttemptID,
		"context_hash":        prepared.ContextHash,
		"role":                prepared.Role,
		"host_name":           "crest-spec-e2e",
		"host_version":        "1",
		"provider":            "deterministic-fixture",
		"model":               "deterministic-candidate-map",
		"host_session_id":     hostSessionID,
		"template_hashes":     prepared.TemplateHashes,
		"system_instructions": prepared.SystemPrompt,
		"tools": []map[string]any{{
			"name": "candidate-files", "permission": "fixture-project-only",
		}},
	})
	require.Equal(t, prepared.AttemptID, manifest.AttemptID)
	require.Equal(t, item.ResourceID, manifest.ResourceID)

	committed := callTool[specmod.CommitResult](t, server, "spec/commit", map[string]any{
		"attempt_id":    prepared.AttemptID,
		"files":         files,
		"notes":         "deterministic acceptance fixture candidate",
		"duration_ms":   1,
		"input_tokens":  1,
		"output_tokens": 1,
	})
	require.True(t, committed.Committed)
	require.True(t, committed.ValidationDeferred)
	require.NotEmpty(t, committed.CandidateID)
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type crestSpecProcess struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      *bufio.Reader
	stderr      *synchronizedBuffer
	nextID      int
	stopped     bool
	initialized map[string]any
}

func startCrestSpec(t *testing.T, binary, projectRoot string) *crestSpecProcess {
	t.Helper()
	cmd := exec.Command(binary)
	cmd.Dir = projectRoot
	cmd.Env = crestSpecEnvironment(projectRoot)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stderr := &synchronizedBuffer{}
	cmd.Stderr = stderr
	require.NoError(t, cmd.Start())

	process := &crestSpecProcess{
		cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: stderr,
	}
	var initialized map[string]any
	process.rpc(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name": "crest-spec-e2e", "version": "1",
		},
	}, &initialized)
	require.Equal(t, "2024-11-05", initialized["protocolVersion"])
	serverInfo, ok := initialized["serverInfo"].(map[string]any)
	require.True(t, ok)
	require.NotEmpty(t, serverInfo["buildFingerprint"],
		"a host must be able to prove which executable image its MCP process loaded")
	require.NotEmpty(t, serverInfo["processStartedAt"])
	process.initialized = initialized
	t.Cleanup(func() {
		if !process.stopped {
			process.stop(t)
		}
	})
	return process
}

func (p *crestSpecProcess) rpc(t *testing.T, method string, params any, destination any) {
	t.Helper()
	p.nextID++
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      p.nextID,
		"method":  method,
		"params":  params,
	}
	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	encoded = append(encoded, '\n')
	if _, err := p.stdin.Write(encoded); err != nil {
		t.Fatalf("write %s request: %v\nserver stderr:\n%s", method, err, p.stderr.String())
	}
	line, err := p.stdout.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read %s response: %v\npartial response: %s\nserver stderr:\n%s", method, err, line, p.stderr.String())
	}
	var response rpcResponse
	require.NoError(t, json.Unmarshal(line, &response), "decode response %s", line)
	if response.Error != nil {
		t.Fatalf("%s returned JSON-RPC error %d: %s\nserver stderr:\n%s", method, response.Error.Code, response.Error.Message, p.stderr.String())
	}
	if destination != nil {
		require.NoError(t, json.Unmarshal(response.Result, destination), "decode %s result: %s", method, response.Result)
	}
}

func (p *crestSpecProcess) stop(t *testing.T) {
	t.Helper()
	if p.stopped {
		return
	}
	p.stopped = true
	require.NoError(t, p.stdin.Close())
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		require.NoError(t, err, "server did not shut down cleanly:\n%s", p.stderr.String())
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		t.Fatalf("server did not shut down after stdin closed:\n%s", p.stderr.String())
	}
}

func (p *crestSpecProcess) crash(t *testing.T) {
	t.Helper()
	if p.stopped {
		return
	}
	p.stopped = true
	require.NoError(t, p.cmd.Process.Kill())
	_ = p.stdin.Close()
	_ = p.cmd.Wait()
}

func callTool[T any](t *testing.T, process *crestSpecProcess, name string, arguments any) T {
	t.Helper()
	result := callToolResult(t, process, name, arguments)
	require.False(t, result.IsError, "%s failed: %s", name, toolResultText(result))
	require.NotEmpty(t, result.Content, "%s returned no content", name)
	var value T
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &value),
		"decode %s payload: %s", name, result.Content[0].Text)
	return value
}

func callToolResult(t *testing.T, process *crestSpecProcess, name string, arguments any) toolResult {
	t.Helper()
	var result toolResult
	process.rpc(t, "tools/call", map[string]any{
		"name": name, "arguments": arguments,
	}, &result)
	return result
}

func toolResultText(result toolResult) string {
	var values []string
	for _, block := range result.Content {
		values = append(values, block.Text)
	}
	return strings.Join(values, "\n")
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Dir(filepath.Dir(filename))
}

func buildCrestSpecBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "crest-spec")
	command := exec.Command("go", "build", "-o", binary, "./cmd/crest-spec")
	command.Dir = repoRoot
	command.Env = append(filteredEnvironment("CREST_SPEC_"), "GOWORK=off")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "build real crest-spec binary:\n%s", output)
	return binary
}

func createCounterProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, "go.mod", "module example.com/crestspec-acceptance\n\ngo 1.23\n")
	writeFixtureFile(t, root, "spec/spec.cue", counterSpec)
	writeFixtureFile(t, root, "counter/counter_acceptance_test.go", counterAcceptanceTest)
	writeFixtureFile(t, root, "cmd/counter-demo/main.go", counterDemo)
	writeFixtureFile(t, root, "cmd/counter-witness/main.go", counterWitness)
	return root
}

func writeFixtureFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func crestSpecEnvironment(projectRoot string) []string {
	environment := filteredEnvironment("CREST_SPEC_")
	overrides := []string{
		"CREST_SPEC_SPEC_DIR=./spec",
		"CREST_SPEC_GENERATE_MODEL=deterministic-candidate-map",
		"CREST_SPEC_MAX_RETRIES=3",
		"CREST_SPEC_WAVE_MAX_RETRIES=2",
		"CREST_SPEC_EXECUTION_TIMEOUT=30m",
		// HashSourceTree excludes target as a build-cache directory. Keeping Go's
		// caches there lets real and negative witnesses prove they observed the
		// exact same project-controlled source tree.
		"GOCACHE=" + filepath.Join(projectRoot, "target", "test-cache", "go-build"),
		"GOMODCACHE=" + filepath.Join(projectRoot, "target", "test-cache", "go-mod"),
		"GOWORK=off",
	}
	return replaceEnvironment(environment, overrides)
}

func filteredEnvironment(prefix string) []string {
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return result
}

func replaceEnvironment(base, overrides []string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range append(append([]string(nil), base...), overrides...) {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[name] = value
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+values[name])
	}
	return result
}

func assertObservableCounterBehavior(t *testing.T, projectRoot string) {
	t.Helper()
	goTest := exec.Command("go", "test", "./...")
	goTest.Dir = projectRoot
	goTest.Env = crestSpecEnvironment(projectRoot)
	output, err := goTest.CombinedOutput()
	require.NoError(t, err, "generated project must pass its black-box tests:\n%s", output)

	demo := exec.Command("go", "run", "./cmd/counter-demo")
	demo.Dir = projectRoot
	demo.Env = crestSpecEnvironment(projectRoot)
	output, err = demo.CombinedOutput()
	require.NoError(t, err, "generated project demo must execute:\n%s", output)
	require.Equal(t, "value=3 boundary_rejected=true", strings.TrimSpace(string(output)))

	valueBytes, err := os.ReadFile(filepath.Join(projectRoot, "counter", "value.go"))
	require.NoError(t, err)
	require.Equal(t, counterValueCandidate, string(valueBytes))
	serviceBytes, err := os.ReadFile(filepath.Join(projectRoot, "counter", "service.go"))
	require.NoError(t, err)
	require.Equal(t, counterServiceCandidate, string(serviceBytes))
}

func assertDurableApplyState(t *testing.T, projectRoot, sessionID string) {
	t.Helper()
	ctx := context.Background()
	databasePath := filepath.Join(projectRoot, ".crest-spec", "state.db")
	require.FileExists(t, databasePath)
	state, err := store.New(databasePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, state.Close()) })

	session, err := state.GetSession(sessionID)
	require.NoError(t, err)
	require.Equal(t, "completed", session.Status)

	resources, err := state.ListSessionResources(sessionID)
	require.NoError(t, err)
	require.Len(t, resources, 2)
	for _, resource := range resources {
		assert.Equal(t, "committed", resource.State, resource.ResourceID)
		assert.Equal(t, "verified", resource.Phase, resource.ResourceID)
		assert.Equal(t, 1, resource.Attempts, resource.ResourceID)
		assert.Empty(t, resource.LastError, resource.ResourceID)
	}

	attempts, err := state.ListGenerationAttemptsBySession(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	for _, attempt := range attempts {
		assert.Equal(t, "accepted", attempt.Status, attempt.ResourceID)
		assert.Equal(t, 1, attempt.RetryNumber, attempt.ResourceID)
	}

	lifecycles, err := state.ListDispatchLifecyclesBySession(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, lifecycles, 2)
	for _, lifecycle := range lifecycles {
		assert.Equal(t, "acceptance-host-session", lifecycle.HostSessionID)
		assert.Equal(t, "deterministic-worker-1", lifecycle.WorkerID)
		assert.NotNil(t, lifecycle.ContextRequestedAt, lifecycle.ResourceID)
		assert.NotNil(t, lifecycle.ContextPreparedAt, lifecycle.ResourceID)
		assert.NotNil(t, lifecycle.DispatchRequestedAt, lifecycle.ResourceID)
		assert.NotNil(t, lifecycle.WorkerAcknowledgedAt, lifecycle.ResourceID)
		assert.NotNil(t, lifecycle.ExecutionRegisteredAt, lifecycle.ResourceID)
		assert.NotNil(t, lifecycle.CandidateSubmittedAt, lifecycle.ResourceID)
		assert.NotNil(t, lifecycle.CandidateAcceptedAt, lifecycle.ResourceID)
		assert.Nil(t, lifecycle.CandidateRejectedAt, lifecycle.ResourceID)
	}

	executions, err := state.ListExecutionManifests(ctx, 10)
	require.NoError(t, err)
	var sessionExecutions []store.ExecutionManifest
	for _, manifest := range executions {
		if manifest.SessionID == sessionID {
			sessionExecutions = append(sessionExecutions, manifest)
		}
	}
	require.Len(t, sessionExecutions, 2)
	for _, manifest := range sessionExecutions {
		assert.Equal(t, string(execution.StatusAccepted), manifest.Status, manifest.ResourceID)
		assert.Equal(t, "deterministic-candidate-map", manifest.Model, manifest.ResourceID)
	}

	runs, err := state.ListValidationRuns(ctx, 20)
	require.NoError(t, err)
	var acceptanceRuns []store.ValidationRun
	for _, run := range runs {
		if run.SessionID == sessionID && run.DefinitionID == "validation.acceptance" {
			acceptanceRuns = append(acceptanceRuns, run)
		}
	}
	require.Len(t, acceptanceRuns, 1)
	run := acceptanceRuns[0]
	require.Equal(t, "passed", run.Classification)
	require.NotEmpty(t, run.SourceTreeHash)
	require.Len(t, run.Executions, 1)
	require.Zero(t, run.Executions[0].ExitCode)
	require.Empty(t, run.Executions[0].LaunchError)
	require.NotEmpty(t, run.Executions[0].ExecutableHash)
	require.Contains(t, run.Executions[0].EnvironmentJSON, `"name":"PATH","present":true`)

	var witnessRuns []store.ValidationRun
	for _, candidate := range runs {
		if candidate.SessionID == sessionID && candidate.DefinitionID == "witness.counter_behavior" {
			witnessRuns = append(witnessRuns, candidate)
		}
	}
	require.Len(t, witnessRuns, 1, "the behavioral gate must be one durable engine execution")
	witnessRun := witnessRuns[0]
	require.Equal(t, "passed", witnessRun.Classification)
	require.Contains(t, witnessRun.Reason, "rejects the stub")
	require.NotEmpty(t, witnessRun.SourceTreeHash)
	require.Len(t, witnessRun.Executions, 2)
	executionsByRole := make(map[string]store.ValidationExecution, 2)
	for _, witnessExecution := range witnessRun.Executions {
		executionsByRole[witnessExecution.Role] = witnessExecution
		require.Zero(t, witnessExecution.ExitCode, witnessExecution.Role)
		require.Empty(t, witnessExecution.LaunchError, witnessExecution.Role)
		require.Equal(t, witnessRun.SourceTreeHash, witnessExecution.SourceTreeHash, witnessExecution.Role)
		require.NotNil(t, witnessExecution.Observation, witnessExecution.Role)
		require.Empty(t, witnessExecution.Observation.ParseError, witnessExecution.Role)
		require.Contains(t, witnessExecution.Stdout, "CREST_OBSERVATION ", witnessExecution.Role)
	}
	require.Contains(t, executionsByRole, "real")
	require.Contains(t, executionsByRole, "negative")
	require.Contains(t, executionsByRole["real"].Observation.ObservationJSON, `"value":3`)
	require.Contains(t, executionsByRole["negative"].Observation.ObservationJSON, `"value":1`)
	require.Len(t, witnessRun.PredicateResults, 6,
		"three predicates must be evaluated against both the real and negative observations")
	negativeFailures := 0
	for _, predicate := range witnessRun.PredicateResults {
		if predicate.ExecutionID == executionsByRole["negative"].ID && !predicate.Passed {
			negativeFailures++
		}
	}
	require.Positive(t, negativeFailures, "the controlled negative observation must falsify the behavior")
	require.Len(t, witnessRun.Evidence, 1)
	require.Equal(t, "evidence.counter_behavior", witnessRun.Evidence[0].EvidenceID)
	require.Equal(t, "current", witnessRun.Evidence[0].Currency)
	require.Equal(t, "passed", witnessRun.Evidence[0].Classification)

	_, err = state.GetLock()
	require.Error(t, err, "a completed apply must release the reconciliation lock")
}
