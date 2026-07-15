package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	executionpkg "github.com/crestenstclair/crest-spec/internal/execution"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func TestDashboardContextInspectorAPIsUseCanonicalStore(t *testing.T) {
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	require.NoError(t, st.CreateApply("apply-dashboard", "spec-hash"))
	require.NoError(t, st.CreateSession(store.Session{ID: "session-dashboard", ApplyID: "apply-dashboard", PlanJSON: "[]", WavesJSON: "[]", HashesJSON: "{}"}))
	require.NoError(t, st.UpsertSessionResource(store.SessionResource{SessionID: "session-dashboard", ResourceID: "resource.dashboard", State: "pending", MaxRetries: 3}))
	manifest, err := st.CreateContextManifest(context.Background(), store.ContextManifestWrite{
		Dispatch: true,
		Manifest: store.ContextManifest{
			ID: "manifest-dashboard",
			Attempt: store.GenerationAttempt{
				ID: "attempt-dashboard", SessionID: "session-dashboard", ApplyID: "apply-dashboard",
				ResourceID: "resource.dashboard", PlanOperationID: "operation-dashboard", Role: "resource_implementer",
				RolePolicyVersion: "role-policy-v1",
			},
			SelectorVersion: contextmanifest.SelectorVersion, EstimatorVersion: contextmanifest.EstimatorVersion,
			SelectionStrategy: "goal-directed-priority-v1", BudgetTokens: 4096, EstimatedTokens: 4,
			OriginalBytes: 15, SelectedBytes: 15, TemplateHashes: map[string]string{"system": "template-hash"},
			SystemPrompt: "system snapshot", RenderedPrompt: "prompt snapshot", ContextHash: contextmanifest.Hash("dashboard-context"),
			Sections: []store.ContextManifestSection{{
				ID: "section-dashboard", Kind: "task", Title: "Task", SourceKind: "plan_operation",
				SourceID: "operation-dashboard", Priority: int(contextmanifest.PriorityTask), Mandatory: true,
				Decision: string(contextmanifest.Included), Reason: "required task", OriginalHash: contextmanifest.Hash("prompt snapshot"),
				OriginalBytes: 15, SelectedBytes: 15, EstimatedTokens: 4, Content: "prompt snapshot",
			}},
		},
	})
	require.NoError(t, err)
	executionManifest, err := st.StartExecution(context.Background(), store.ExecutionManifest{
		AttemptID: "attempt-dashboard", ContextManifestID: "manifest-dashboard",
		ProtocolVersion: executionpkg.ProtocolVersion, IdempotencyKey: "dashboard-request",
		PlanOperationID: "operation-dashboard", Role: "resource_implementer",
		RolePolicyVersion: executionpkg.RolePolicyVersion, ContextPolicy: "resource-implementation-v1",
		HostName: "dashboard-host", Provider: "test", Model: "test-model",
		TemplateHashes:     map[string]string{"system": "template-hash"},
		SystemInstructions: "system snapshot", ContextHash: manifest.ContextHash,
		Tools: []store.ExecutionTool{{Name: "filesystem", Permission: "project"}},
	})
	require.NoError(t, err)
	_, err = st.SubmitCandidate(context.Background(), executionManifest.ID, []store.CandidateFile{{
		Path: "src/dashboard.go", Content: "package dashboard\n", WriteIntent: "create",
	}})
	require.NoError(t, err)

	d := &dashboard{store: st, spec: specmod.New(st, specmod.OSFileSystem{}, &config.Config{}), cfg: &config.Config{}, log: zerolog.Nop()}

	listRecorder := httptest.NewRecorder()
	d.handleContextAttempts(listRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/contexts", nil))
	require.Equal(t, http.StatusOK, listRecorder.Code)
	var summaries []store.ContextManifestSummary
	require.NoError(t, json.Unmarshal(listRecorder.Body.Bytes(), &summaries))
	require.Len(t, summaries, 1)
	require.Equal(t, manifest.ID, summaries[0].ID)

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/v1/contexts/manifest-dashboard", nil)
	detailRequest.SetPathValue("id", "manifest-dashboard")
	detailRecorder := httptest.NewRecorder()
	d.handleContextManifest(detailRecorder, detailRequest)
	require.Equal(t, http.StatusOK, detailRecorder.Code)
	require.Contains(t, detailRecorder.Body.String(), "prompt snapshot")

	compareRecorder := httptest.NewRecorder()
	d.handleContextComparison(compareRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/context-comparison?left=manifest-dashboard&right=manifest-dashboard", nil))
	require.Equal(t, http.StatusOK, compareRecorder.Code)
	require.Contains(t, compareRecorder.Body.String(), `"SameContext":true`)

	executionListRecorder := httptest.NewRecorder()
	d.handleExecutions(executionListRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/executions", nil))
	require.Equal(t, http.StatusOK, executionListRecorder.Code)
	require.Contains(t, executionListRecorder.Body.String(), "dashboard-host")

	executionRequest := httptest.NewRequest(http.MethodGet, "/api/v1/executions/"+executionManifest.ID, nil)
	executionRequest.SetPathValue("id", executionManifest.ID)
	executionRecorder := httptest.NewRecorder()
	d.handleExecutionTrace(executionRecorder, executionRequest)
	require.Equal(t, http.StatusOK, executionRecorder.Code)
	require.Contains(t, executionRecorder.Body.String(), "src/dashboard.go")
	require.Contains(t, executionRecorder.Body.String(), "prompt snapshot")
}

func TestDashboardIncludesContextInspectorInterface(t *testing.T) {
	content, err := fs.ReadFile(staticFiles, "static/index.html")
	require.NoError(t, err)
	html := string(content)
	require.True(t, strings.Contains(html, `data-tab="contexts"`))
	require.Contains(t, html, "Selection decisions")
	require.Contains(t, html, "compareSelectedContexts")
	require.Contains(t, html, `data-tab="executions"`)
	require.Contains(t, html, "Execution Inspector")
	require.Contains(t, html, "compareSelectedExecutions")
	require.Contains(t, html, "/api/v1/executions")
	require.Contains(t, html, "goal_progress")
	require.Contains(t, html, "host_commit_ref")
}
