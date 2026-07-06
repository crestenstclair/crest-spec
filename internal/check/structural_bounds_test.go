package check

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A predicate whose required bound is ABSENT from its JSON must classify as
// structural (an authoring error), never as a behavioral failure — before
// this, a missing value silently compared against 0 and produced plausible
// "behavior not exhibited" noise (4 of the 5 'real findings' in the first
// live behavioral pass were exactly this).
func TestMissingBoundsAreStructural(t *testing.T) {
	cases := []struct{ name, predJSON string }{
		{"eq missing value", `{"field":"n","op":"eq"}`},
		{"gt missing value", `{"field":"n","op":"gt"}`},
		{"count missing value", `{"field":"l","op":"count"}`},
		{"distinct_count missing value", `{"field":"l","op":"distinct_count"}`},
		{"range missing bounds", `{"field":"n","op":"range"}`},
		{"equals missing member", `{"field":"s","op":"equals"}`},
	}
	obs := Observation{"n": 1.0, "l": []any{1.0, 2.0}, "s": "x"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p Predicate
			require.NoError(t, json.Unmarshal([]byte(tc.predJSON), &p))
			c := Check{Behavior: "b", Predicates: []Predicate{p}}
			v := Verify(c, obs, Observation{"n": 0.0, "l": []any{}, "s": ""})
			assert.True(t, v.Structural, "missing required bound must be structural: %s", tc.name)
			assert.False(t, v.Passed)
		})
	}
}

func TestExplicitZeroBoundStillWorks(t *testing.T) {
	var p Predicate
	require.NoError(t, json.Unmarshal([]byte(`{"field":"n","op":"eq","value":0}`), &p))
	v := Verify(Check{Behavior: "b", Predicates: []Predicate{p}},
		Observation{"n": 0.0}, Observation{"n": 5.0})
	assert.True(t, v.Passed, "an explicit value of 0 is a real bound, not a missing one")
	assert.False(t, v.Structural)
}
