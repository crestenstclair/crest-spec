package check

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluate_NumericOps(t *testing.T) {
	obs := Observation{"steals": 3.0, "voices": 8.0}

	cases := []struct {
		name string
		pred Predicate
		want bool
	}{
		{"gt pass", Predicate{Field: "steals", Op: OpGt, Value: 0}, true},
		{"gt fail", Predicate{Field: "steals", Op: OpGt, Value: 5}, false},
		{"gte boundary", Predicate{Field: "voices", Op: OpGte, Value: 8}, true},
		{"eq pass", Predicate{Field: "voices", Op: OpEq, Value: 8}, true},
		{"lt pass", Predicate{Field: "steals", Op: OpLt, Value: 4}, true},
		{"lte fail", Predicate{Field: "voices", Op: OpLte, Value: 7}, false},
		{"absent field fails", Predicate{Field: "missing", Op: OpGt, Value: 0}, false},
		{"non-numeric fails", Predicate{Field: "voices", Op: OpGt, Value: 0}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Evaluate(Check{Predicates: []Predicate{c.pred}}, obs)
			assert.Equal(t, c.want, res.Passed)
			if !c.want {
				require.NotEmpty(t, res.Failures, "a failing predicate must record a failure message")
			}
		})
	}
}

func TestEvaluate_AggregatesAllPredicates(t *testing.T) {
	obs := Observation{"a": 1.0, "b": 2.0}
	c := Check{Predicates: []Predicate{
		{Field: "a", Op: OpEq, Value: 1}, // pass
		{Field: "b", Op: OpEq, Value: 9}, // fail
	}}
	res := Evaluate(c, obs)
	assert.False(t, res.Passed)
	assert.Len(t, res.Failures, 1)
}
