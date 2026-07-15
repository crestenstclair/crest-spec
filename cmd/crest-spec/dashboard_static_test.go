package main

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDashboardEmbedsModularAccessibleApplication(t *testing.T) {
	for _, path := range []string{
		"static/index.html", "static/styles.css", "static/js/app.js", "static/js/api.js",
		"static/js/components.js", "static/js/router.js",
		"static/js/features/project.js", "static/js/features/resources.js",
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
}
