package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/observability"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func goalOrientedDashboard(t *testing.T) *dashboard {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	specDir, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "crest-synth", "spec"))
	require.NoError(t, err)
	cfg := &config.Config{SpecDir: specDir}
	return &dashboard{
		store: st, spec: specmod.New(st, specmod.OSFileSystem{}, cfg),
		views: observability.NewService(st), cfg: cfg, log: zerolog.Nop(),
	}
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
