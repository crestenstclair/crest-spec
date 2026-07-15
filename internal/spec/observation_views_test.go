package spec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/observability"
)

func TestObservationViewsReuseCanonicalProjectAndPlanState(t *testing.T) {
	specification, _ := newExecutableWitnessSpec(t, "printf '%s\\n' 'CREST_OBSERVATION {\"peak\":0,\"clipped\":false}'")
	ctx := context.Background()
	project, err := specification.ObserveProject(ctx)
	require.NoError(t, err)
	assert.Equal(t, observability.APIVersion, project.Version)
	assert.Equal(t, "witness-project", project.Project.Name)
	assert.Equal(t, "goal.audible", project.Project.Goals[0].ID)

	goal, err := specification.ObserveGoal(ctx, "goal.audible")
	require.NoError(t, err)
	require.Len(t, goal.Acceptance, 1)
	require.Len(t, goal.Evidence, 1)
	assert.Equal(t, "missing", goal.Evidence[0].Currency)

	capability, err := specification.ObserveCapability(ctx, "capability.render")
	require.NoError(t, err)
	assert.Equal(t, "capability.render", capability.Capability.ID)
	require.Len(t, capability.Goals, 1)

	plan, err := specification.ObservePlan(ctx)
	require.NoError(t, err)
	assert.Equal(t, "witness-project", plan.ProjectName)
	assert.Equal(t, observability.APIVersion, plan.Version)
}
