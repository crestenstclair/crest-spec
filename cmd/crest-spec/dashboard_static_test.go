package main

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDashboardURLHandlesPortOnlyAndExplicitHosts(t *testing.T) {
	require.Equal(t, "http://localhost:8080", dashboardURL(":8080"))
	require.Equal(t, "http://127.0.0.1:8080", dashboardURL("127.0.0.1:8080"))
}

func TestDashboardEmbedsModularAccessibleApplication(t *testing.T) {
	for _, path := range []string{
		"static/index.html", "static/styles.css", "static/js/app.js", "static/js/api.js",
		"static/js/components.js", "static/js/router.js",
		"static/js/features/project.js", "static/js/features/resources.js",
		"static/js/features/plan.js", "static/js/features/failures.js", "static/js/features/attempts.js",
	} {
		content, err := fs.ReadFile(staticFiles, path)
		require.NoError(t, err, path)
		require.NotEmpty(t, content, path)
	}
	html, err := fs.ReadFile(staticFiles, "static/index.html")
	require.NoError(t, err)
	require.Contains(t, string(html), `<main class="main"`)
	require.Contains(t, string(html), `aria-live="polite"`)
	require.Contains(t, string(html), `aria-controls="tab-project"`)
	router, err := fs.ReadFile(staticFiles, "static/js/router.js")
	require.NoError(t, err)
	require.Contains(t, string(router), "ArrowRight")
	require.Contains(t, string(router), "popstate")
	require.Contains(t, string(router), "searchParams")
	components, err := fs.ReadFile(staticFiles, "static/js/components.js")
	require.NoError(t, err)
	require.Contains(t, string(components), "recordStateNotice")
	app, err := fs.ReadFile(staticFiles, "static/js/app.js")
	require.NoError(t, err)
	for _, identity := range []string{"contextID", "executionID", "verificationID", "runID"} {
		require.Contains(t, string(app), identity)
	}
}
