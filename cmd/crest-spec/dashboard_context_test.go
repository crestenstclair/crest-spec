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
}

func TestDashboardIncludesContextInspectorInterface(t *testing.T) {
	content, err := fs.ReadFile(staticFiles, "static/index.html")
	require.NoError(t, err)
	html := string(content)
	require.True(t, strings.Contains(html, `data-tab="contexts"`))
	require.Contains(t, html, "Selection decisions")
	require.Contains(t, html, "compareSelectedContexts")
}
