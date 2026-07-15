package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	executionpkg "github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/observability"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func goalOrientedDashboard(t *testing.T) *dashboard {
	return dashboardForSpecDir(t, filepath.Join("..", "..", "fixtures", "crest-synth", "spec"))
}

func goalOrientedSampleDashboard(t *testing.T) *dashboard {
	return dashboardForSpecDir(t, filepath.Join("..", "..", "docs", "plans", "goal-oriented-crest-spec", "examples"))
}

func dashboardForSpecDir(t *testing.T, relativeDir string) *dashboard {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	specDir, err := filepath.Abs(relativeDir)
	require.NoError(t, err)
	cfg := &config.Config{SpecDir: specDir}
	return &dashboard{
		store: st, spec: specmod.New(st, specmod.OSFileSystem{}, cfg),
		views: observability.NewService(st), cfg: cfg, log: zerolog.Nop(),
	}
}

func TestDashboardTracesGoalThroughPlanAttemptAndEvidence(t *testing.T) {
	d := goalOrientedSampleDashboard(t)
	ctx := context.Background()
	begin, err := d.spec.Begin(ctx, specmod.BeginOpts{})
	require.NoError(t, err)
	require.NotEmpty(t, begin.SessionID)

	const (
		resourceID   = "domainService.Engine.EngineRenderer"
		goalID       = "goal.play_an_instrument"
		definitionID = "witness.audible_tone"
	)
	var operationID string
	for _, action := range begin.Plan {
		if action.ResourceID == resourceID {
			operationID = action.OperationID
			assert.Contains(t, action.Goals, goalID)
			break
		}
	}
	require.NotEmpty(t, operationID)

	prepared, err := d.spec.PrepareContext(ctx, specmod.ContextOptions{
		SessionID: begin.SessionID, ResourceID: resourceID, BudgetTokens: contextmanifest.MaximumBudget,
	})
	require.NoError(t, err)
	require.False(t, prepared.Blocked, prepared.BlockedReason)
	execution, err := d.spec.StartExecution(ctx, specmod.ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: executionpkg.ProtocolVersion,
		IdempotencyKey: "dashboard-e2e-execution", ContextHash: prepared.ContextHash, Role: prepared.Role,
		HostName: "acceptance-host", HostVersion: "1.0", Provider: "fixture", Model: "fixture-model",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
		Tools: []store.ExecutionTool{{Name: "filesystem", Permission: "project"}},
	})
	require.NoError(t, err)
	candidate, err := d.store.SubmitCandidate(ctx, execution.ID, []store.CandidateFile{{
		Path: "src/engine/renderer.rs", Content: "// immutable acceptance candidate\n", WriteIntent: "create",
	}})
	require.NoError(t, err)

	started := time.Now().UTC().Add(-20 * time.Millisecond)
	run, err := d.store.RecordValidationRun(ctx, store.ValidationRunWrite{
		ProjectName: "crest-synth-goal-oriented-sample",
		Run: store.ValidationRun{
			DefinitionID: definitionID, SessionID: begin.SessionID, AttemptID: prepared.AttemptID,
			ExecutionID: execution.ID, SourceTreeHash: "tree-acceptance", Classification: "passed",
			Reason:    "real witness passed and the negative case failed",
			StartedAt: started, CompletedAt: started.Add(20 * time.Millisecond), DurationMS: 20,
			Executions: []store.ValidationExecution{
				{
					Role: "real", CommandJSON: `["tone-witness","real"]`, CommandHash: "command-real",
					WorkingDirectory: ".", EnvironmentJSON: `{}`, EnvironmentHash: "environment",
					SourceTreeHash: "tree-acceptance", ExecutableHash: "executable-real",
					Stdout: `CREST_OBS {"peak":0.7}`, ExitCode: 0,
					StartedAt: started, CompletedAt: started.Add(10 * time.Millisecond), DurationMS: 10,
				},
				{
					Role: "negative", CommandJSON: `["tone-witness","silent"]`, CommandHash: "command-negative",
					WorkingDirectory: ".", EnvironmentJSON: `{}`, EnvironmentHash: "environment",
					SourceTreeHash: "tree-acceptance", ExecutableHash: "executable-negative",
					Stdout: `CREST_OBS {"peak":0}`, ExitCode: 0,
					StartedAt: started.Add(10 * time.Millisecond), CompletedAt: started.Add(20 * time.Millisecond), DurationMS: 10,
				},
			},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, run.Evidence)

	goalRequest := httptest.NewRequest(http.MethodGet, "/api/v1/goals/"+goalID, nil)
	goalRequest.SetPathValue("id", goalID)
	goalRecorder := httptest.NewRecorder()
	d.handleGoal(goalRecorder, goalRequest)
	require.Equal(t, http.StatusOK, goalRecorder.Code, goalRecorder.Body.String())
	var goal observability.GoalDetail
	require.NoError(t, json.Unmarshal(goalRecorder.Body.Bytes(), &goal))
	assert.Contains(t, goal.Goal.Resources, resourceID)
	var linkedEvidence *observability.EvidenceStatus
	for index := range goal.Evidence {
		if goal.Evidence[index].RunID == run.ID {
			linkedEvidence = &goal.Evidence[index]
			break
		}
	}
	require.NotNil(t, linkedEvidence)
	assert.Equal(t, "tree-acceptance", linkedEvidence.SourceTreeHash)
	assert.Equal(t, "/api/v1/verifications/"+run.ID, linkedEvidence.Links[0].HREF)

	planRecorder := httptest.NewRecorder()
	d.handleV1Plan(planRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/plan", nil))
	require.Equal(t, http.StatusOK, planRecorder.Code, planRecorder.Body.String())
	var plan observability.PlanView
	require.NoError(t, json.Unmarshal(planRecorder.Body.Bytes(), &plan))
	operation, err := observability.PlanOperationByID(&plan, operationID)
	require.NoError(t, err)
	assert.Contains(t, operation.Goals, goalID)
	assert.Contains(t, operation.ExpectedEvidence, "evidence.audible_tone")

	attemptRequest := httptest.NewRequest(http.MethodGet, "/api/v1/attempts/"+prepared.AttemptID, nil)
	attemptRequest.SetPathValue("id", prepared.AttemptID)
	attemptRecorder := httptest.NewRecorder()
	d.handleAttempt(attemptRecorder, attemptRequest)
	require.Equal(t, http.StatusOK, attemptRecorder.Code, attemptRecorder.Body.String())
	var attempt observability.AttemptDetail
	require.NoError(t, json.Unmarshal(attemptRecorder.Body.Bytes(), &attempt))
	require.NotNil(t, attempt.Context)
	require.NotNil(t, attempt.Execution)
	require.NotNil(t, attempt.Candidate)
	assert.Equal(t, prepared.ContextManifestID, attempt.Context.ID)
	assert.Equal(t, execution.ID, attempt.Execution.ID)
	assert.Equal(t, candidate.ID, attempt.Candidate.ID)
	require.Len(t, attempt.Validations, 1)
	assert.Equal(t, run.ID, attempt.Validations[0].ID)

	validationRequest := httptest.NewRequest(http.MethodGet, "/api/v1/verifications/"+run.ID, nil)
	validationRequest.SetPathValue("id", run.ID)
	validationRecorder := httptest.NewRecorder()
	d.handleVerificationRun(validationRecorder, validationRequest)
	require.Equal(t, http.StatusOK, validationRecorder.Code, validationRecorder.Body.String())
	var validation store.ValidationRun
	require.NoError(t, json.Unmarshal(validationRecorder.Body.Bytes(), &validation))
	assert.Equal(t, prepared.AttemptID, validation.AttemptID)
	assert.Equal(t, execution.ID, validation.ExecutionID)
	assert.Equal(t, "tree-acceptance", validation.SourceTreeHash)
	assert.Len(t, validation.Executions, 2)
}

func TestDashboardV1ProjectGoalAndCapabilityContracts(t *testing.T) {
	d := goalOrientedDashboard(t)
	projectRecorder := httptest.NewRecorder()
	d.handleProjectOverview(projectRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/project", nil))
	require.Equal(t, http.StatusOK, projectRecorder.Code, projectRecorder.Body.String())
	var project observability.ProjectOverview
	require.NoError(t, json.Unmarshal(projectRecorder.Body.Bytes(), &project))
	assert.Equal(t, observability.APIVersion, project.Version)
	assert.Equal(t, "crest-synth", project.Project.Name)
	assert.NotEmpty(t, project.Project.Mission)
	require.NotEmpty(t, project.Project.Goals)
	require.NotEmpty(t, project.Project.Capabilities)
	assert.NotEmpty(t, project.Project.Goals[0].Links)

	goalsRecorder := httptest.NewRecorder()
	d.handleGoals(goalsRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/goals?limit=1", nil))
	require.Equal(t, http.StatusOK, goalsRecorder.Code, goalsRecorder.Body.String())
	var goals observability.Page[observability.GoalSummary]
	require.NoError(t, json.Unmarshal(goalsRecorder.Body.Bytes(), &goals))
	require.Len(t, goals.Items, 1)
	assert.Equal(t, 1, goals.Page.Limit)
	assert.True(t, goals.Page.HasMore)

	capabilitiesRecorder := httptest.NewRecorder()
	d.handleCapabilities(capabilitiesRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities?q=audio&limit=10", nil))
	require.Equal(t, http.StatusOK, capabilitiesRecorder.Code, capabilitiesRecorder.Body.String())
	var capabilities observability.Page[observability.CapabilitySummary]
	require.NoError(t, json.Unmarshal(capabilitiesRecorder.Body.Bytes(), &capabilities))
	assert.Equal(t, observability.APIVersion, capabilities.Version)
}

func TestDashboardV1ErrorEnvelopeAndPaginationBounds(t *testing.T) {
	d := goalOrientedDashboard(t)
	recorder := httptest.NewRecorder()
	d.handleGoals(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/goals?limit=201", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var envelope observability.ErrorEnvelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.Equal(t, observability.APIVersion, envelope.Version)
	assert.Equal(t, "invalid_page", envelope.Error.Code)
	assert.False(t, envelope.Error.Retryable)

	missingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/goals/missing", nil)
	missingRequest.SetPathValue("id", "missing")
	missingRecorder := httptest.NewRecorder()
	d.handleGoal(missingRecorder, missingRequest)
	require.Equal(t, http.StatusNotFound, missingRecorder.Code)
	require.NoError(t, json.Unmarshal(missingRecorder.Body.Bytes(), &envelope))
	assert.Equal(t, "not_found", envelope.Error.Code)
}

func TestDashboardV1PlanAndImpactContracts(t *testing.T) {
	d := goalOrientedDashboard(t)
	planRecorder := httptest.NewRecorder()
	d.handleV1Plan(planRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/plan", nil))
	require.Equal(t, http.StatusOK, planRecorder.Code, planRecorder.Body.String())
	var plan observability.PlanView
	require.NoError(t, json.Unmarshal(planRecorder.Body.Bytes(), &plan))
	assert.Equal(t, observability.APIVersion, plan.Version)
	assert.Equal(t, "crest-synth", plan.ProjectName)
	require.NotEmpty(t, plan.Operations)
	require.NotEmpty(t, plan.Slices)
	assert.NotEmpty(t, plan.Operations[0].Reason)
	assert.NotEmpty(t, plan.Operations[0].Links)
	hasBehavioralOperation := false
	for _, operation := range plan.Operations {
		if len(operation.ExpectedBehavior) > 0 && len(operation.ExpectedEvidence) > 0 {
			hasBehavioralOperation = true
			break
		}
	}
	assert.True(t, hasBehavioralOperation)

	operationRequest := httptest.NewRequest(http.MethodGet, "/api/v1/plan/operations/"+plan.Operations[0].ID, nil)
	operationRequest.SetPathValue("id", plan.Operations[0].ID)
	operationRecorder := httptest.NewRecorder()
	d.handleV1PlanOperation(operationRecorder, operationRequest)
	require.Equal(t, http.StatusOK, operationRecorder.Code, operationRecorder.Body.String())
	assert.Contains(t, operationRecorder.Body.String(), plan.Operations[0].ResourceID)

	impactRequest := httptest.NewRequest(http.MethodGet, "/api/v1/resources/"+plan.Operations[0].ResourceID+"/impact", nil)
	impactRequest.SetPathValue("id", plan.Operations[0].ResourceID)
	impactRecorder := httptest.NewRecorder()
	d.handleResourceImpact(impactRecorder, impactRequest)
	require.Equal(t, http.StatusOK, impactRecorder.Code, impactRecorder.Body.String())
	var impact observability.ImpactView
	require.NoError(t, json.Unmarshal(impactRecorder.Body.Bytes(), &impact))
	assert.Equal(t, plan.Operations[0].ResourceID, impact.SourceResource)
	assert.Contains(t, impact.AffectedResources, plan.Operations[0].ResourceID)
}
