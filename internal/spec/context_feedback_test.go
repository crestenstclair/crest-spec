package spec

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The retry loop's contract: after a rejected commit or a wave failure, the
// NEXT spec/context call must carry the failure and any triage guidance back
// to the generator. These tests pin that contract — they are the regression
// guard for the dead-path bug where WaveErrors/UserGuidance existed but were
// never assigned.

func TestContextInjectsLastErrorOnRetry(t *testing.T) {
	s, st := newTestSpecWithSession(t)

	require.NoError(t, s.store.UpdateSessionResourceState(
		st.sessionID, st.resourceID, "rejected",
		"cargo check failed: mismatched types in frequency.rs", "", 1, ""))

	res, err := s.Context(context.Background(), st.sessionID, st.resourceID)
	require.NoError(t, err)

	require.Contains(t, res.Prompt, "## Previous Errors",
		"prompt must carry the previous failure section on retry")
	require.Contains(t, res.Prompt, "cargo check failed: mismatched types in frequency.rs",
		"prompt must contain the actual failure text")
}

func TestContextInjectsOwnResolveGuidance(t *testing.T) {
	s, st := newTestSpecWithSession(t)

	// spec/resolve stores guidance as a note on the resource itself.
	require.NoError(t, s.Resolve(context.Background(), st.sessionID, st.resourceID,
		"use try_new, not new; the constructor must validate", ""))

	res, err := s.Context(context.Background(), st.sessionID, st.resourceID)
	require.NoError(t, err)

	require.Contains(t, res.Prompt, "## Guidance",
		"prompt must carry the guidance section when the resource has a note")
	require.Contains(t, res.Prompt, "use try_new, not new; the constructor must validate",
		"prompt must contain the resolve guidance verbatim")
}

func TestContextInjectsDesignContract(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()

	contract := `{"behaviors":[{"description":"rejects out-of-range tones"}]}`
	require.NoError(t, s.DesignCommit(ctx, "Audio", contract))

	res, err := s.Context(ctx, st.sessionID, st.resourceID)
	require.NoError(t, err)

	require.Contains(t, res.Prompt, "## Bounded-Context Design Contract",
		"the committed design contract for the resource's context must reach the generator")
	require.Contains(t, res.Prompt, "rejects out-of-range tones")
}

func TestContextWithoutFailureHasNoErrorSection(t *testing.T) {
	s, st := newTestSpecWithSession(t)

	res, err := s.Context(context.Background(), st.sessionID, st.resourceID)
	require.NoError(t, err)

	require.False(t, strings.Contains(res.Prompt, "## Previous Errors"),
		"a clean first attempt must not carry an error section")
}
