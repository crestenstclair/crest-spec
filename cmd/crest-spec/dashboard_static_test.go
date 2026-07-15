package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDashboardURLHandlesPortOnlyAndExplicitHosts(t *testing.T) {
	require.Equal(t, "http://localhost:8080", dashboardURL(":8080"))
	require.Equal(t, "http://127.0.0.1:8080", dashboardURL("127.0.0.1:8080"))
}

func TestDashboardUsesEscapedDataAttributesAndDelegatedHandlers(t *testing.T) {
	appBytes, err := fs.ReadFile(staticFiles, "static/js/app.js")
	require.NoError(t, err)
	app := string(appBytes)
	evaluationBytes, err := fs.ReadFile(staticFiles, "static/js/features/evaluations.js")
	require.NoError(t, err)
	delegatedHandlers := app + string(evaluationBytes)
	componentsBytes, err := fs.ReadFile(staticFiles, "static/js/components.js")
	require.NoError(t, err)
	components := string(componentsBytes)

	err = fs.WalkDir(staticFiles, "static", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".js") && !strings.HasSuffix(path, ".html") {
			return walkErr
		}
		content, readErr := fs.ReadFile(staticFiles, path)
		if readErr != nil {
			return readErr
		}
		for _, inlineHandler := range []string{"onclick=", "onchange=", "oninput=", "onsubmit="} {
			require.NotContains(t, strings.ToLower(string(content)), inlineHandler, path)
		}
		return nil
	})
	require.NoError(t, err)
	require.NotContains(t, app, "Object.assign(window")
	require.Contains(t, app, "document.addEventListener('click', handleDashboardAction)")
	for _, action := range []string{
		"toggle-evaluation-run", "show-evaluation-case", "toggle-evaluation-case",
		"inspect-evaluation-record", "toggle-evaluation-promotion", "toggle-wave",
		"toggle-collapsible", "toggle-generation", "toggle-context",
		"toggle-context-content", "toggle-execution", "toggle-verification",
	} {
		require.Contains(t, delegatedHandlers, `data-action="`+action+`"`)
	}

	// Identifiers can originate in specifications and persisted records. These
	// substitutions keep apostrophes, quotes, and markup inside data attributes
	// instead of allowing them to create elements or executable attributes.
	hostileIdentifier := `goal'"><img src=x onerror="alert(1)">`
	require.Contains(t, hostileIdentifier, "'")
	require.Contains(t, hostileIdentifier, `"`)
	require.Contains(t, hostileIdentifier, "<img")
	for _, substitution := range []string{
		`'&': '&amp;'`, `'<': '&lt;'`, `'>': '&gt;'`, `'"': '&quot;'`, `"'": '&#39;'`,
	} {
		require.Contains(t, components, substitution)
	}
	require.Contains(t, components, `replace(/[&<>"']/g`)
	for _, dynamicAttribute := range []string{
		`data-record-id="' + esc(`,
		`data-resource-id="' + esc(`,
		`data-record-url="' + esc(`,
	} {
		require.Contains(t, delegatedHandlers, dynamicAttribute)
	}
}

func TestDashboardEmbedsModularAccessibleApplication(t *testing.T) {
	for _, path := range []string{
		"static/index.html", "static/styles.css", "static/js/app.js", "static/js/api.js",
		"static/js/components.js", "static/js/router.js",
		"static/js/features/project.js", "static/js/features/resources.js",
		"static/js/features/plan.js", "static/js/features/failures.js", "static/js/features/attempts.js",
		"static/js/features/evaluations.js",
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
