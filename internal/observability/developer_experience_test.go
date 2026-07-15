package observability

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEveryDeveloperQuestionHasAgentAndHumanInspectionRoutes(t *testing.T) {
	require.Len(t, DeveloperQuestionRoutes, 11)
	seen := make(map[string]bool)
	for _, route := range DeveloperQuestionRoutes {
		require.NotEmpty(t, route.Question)
		assert.False(t, seen[route.Question], route.Question)
		seen[route.Question] = true
		assert.Contains(t, route.API, "/api/v1/", route.Question)
		assert.Contains(t, route.MCP, "spec/", route.Question)
		assert.True(t, strings.HasPrefix(route.CLI, "crest-spec "), route.Question)
		assert.Contains(t, route.Dashboard, "?view=", route.Question)
	}
}
