package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResourceState_IsTerminal(t *testing.T) {
	assert.True(t, StateCommitted.IsTerminal())
	assert.True(t, StateSkipped.IsTerminal())
	assert.False(t, StatePending.IsTerminal())
	assert.False(t, StateDispatched.IsTerminal())
	assert.False(t, StateErrored.IsTerminal())
}

func TestResourceState_NeedsResolution(t *testing.T) {
	assert.True(t, StateBlocked.NeedsResolution())
	assert.True(t, StateErrored.NeedsResolution())
	assert.True(t, StateTimedOut.NeedsResolution())
	assert.True(t, StateRejected.NeedsResolution())
	assert.False(t, StatePending.NeedsResolution())
	assert.False(t, StateCommitted.NeedsResolution())
}
