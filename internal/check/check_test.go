package check

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluate_NumericOps(t *testing.T) {
	obs := Observation{"steals": 3.0, "voices": 8.0, "label": "x"}

	cases := []struct {
		name string
		pred Predicate
		want bool
	}{
		{"gt pass", Predicate{Field: "steals", Op: OpGt, Value: F(0)}, true},
		{"gt fail", Predicate{Field: "steals", Op: OpGt, Value: F(5)}, false},
		{"gte boundary", Predicate{Field: "voices", Op: OpGte, Value: F(8)}, true},
		{"eq pass", Predicate{Field: "voices", Op: OpEq, Value: F(8)}, true},
		{"lt pass", Predicate{Field: "steals", Op: OpLt, Value: F(4)}, true},
		{"lte fail", Predicate{Field: "voices", Op: OpLte, Value: F(7)}, false},
		{"absent field fails", Predicate{Field: "missing", Op: OpGt, Value: F(0)}, false},
		{"non-numeric field fails", Predicate{Field: "label", Op: OpGt, Value: F(0)}, false},
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
		{Field: "a", Op: OpEq, Value: F(1)}, // pass
		{Field: "b", Op: OpEq, Value: F(9)}, // fail
	}}
	res := Evaluate(c, obs)
	assert.False(t, res.Passed)
	assert.Len(t, res.Failures, 1)
}

func TestEvaluate_Range(t *testing.T) {
	obs := Observation{"cutoff": 12000.0}
	pass := Evaluate(Check{Predicates: []Predicate{
		{Field: "cutoff", Op: OpRange, Min: F(20), Max: F(20000)},
	}}, obs)
	assert.True(t, pass.Passed)

	fail := Evaluate(Check{Predicates: []Predicate{
		{Field: "cutoff", Op: OpRange, Min: F(20), Max: F(1000)},
	}}, obs)
	assert.False(t, fail.Passed)
}

func TestEvaluate_ListOps(t *testing.T) {
	// "two different key/velocity inputs resolve to different sample zones"
	differObs := Observation{"zone_ids": []any{"zoneA", "zoneB"}}
	sameObs := Observation{"zone_ids": []any{"zoneA", "zoneA"}}

	differ := []Predicate{{Field: "zone_ids", Op: OpDiffer}}
	assert.True(t, Evaluate(Check{Predicates: differ}, differObs).Passed)
	assert.False(t, Evaluate(Check{Predicates: differ}, sameObs).Passed)

	distinct2 := []Predicate{{Field: "zone_ids", Op: OpDistinctCount, Value: F(2)}}
	assert.True(t, Evaluate(Check{Predicates: distinct2}, differObs).Passed)
	assert.False(t, Evaluate(Check{Predicates: distinct2}, sameObs).Passed)

	count2 := []Predicate{{Field: "zone_ids", Op: OpCount, Value: F(2)}}
	assert.True(t, Evaluate(Check{Predicates: count2}, sameObs).Passed)

	notAList := Observation{"zone_ids": 3.0}
	assert.False(t, Evaluate(Check{Predicates: differ}, notAList).Passed)
}

func TestEvaluate_MonotonicAndSetContains(t *testing.T) {
	rising := Observation{"meter": []any{0.1, 0.4, 0.9}}
	flat := Observation{"meter": []any{0.4, 0.4, 0.4}}
	mono := []Predicate{{Field: "meter", Op: OpMonotonic}}
	assert.True(t, Evaluate(Check{Predicates: mono}, rising).Passed)
	assert.False(t, Evaluate(Check{Predicates: mono}, flat).Passed)

	waveforms := Observation{"waveforms": []any{"sine", "saw", "square"}}
	has := []Predicate{{Field: "waveforms", Op: OpSetContains, Member: "saw"}}
	missing := []Predicate{{Field: "waveforms", Op: OpSetContains, Member: "noise"}}
	assert.True(t, Evaluate(Check{Predicates: has}, waveforms).Passed)
	assert.False(t, Evaluate(Check{Predicates: missing}, waveforms).Passed)
}

func TestVerify_FalsificationGate(t *testing.T) {
	check := Check{
		Behavior:   "past the polyphony limit, voice stealing occurs",
		Predicates: []Predicate{{Field: "steals", Op: OpGt, Value: F(0)}},
	}
	real := Observation{"steals": 4.0} // real impl steals
	stub := Observation{"steals": 0.0} // no-op never steals

	// Real exhibits the behavior; stub does not → genuine pass.
	good := Verify(check, real, stub)
	assert.True(t, good.Passed)
	assert.False(t, good.Theater)

	// Real fails to exhibit the behavior → fail (not theater).
	notExhibited := Verify(check, Observation{"steals": 0.0}, stub)
	assert.False(t, notExhibited.Passed)
	assert.False(t, notExhibited.Theater)

	// A toothless check the stub also passes → theater, rejected even though real passes.
	toothless := Check{Predicates: []Predicate{{Field: "steals", Op: OpGte, Value: F(0)}}}
	theater := Verify(toothless, real, stub)
	assert.False(t, theater.Passed)
	assert.True(t, theater.Theater)
	assert.Contains(t, theater.Reason, "teeth")
}

func TestEvaluate_FromJSON(t *testing.T) {
	// Shape a witness would emit on stdout / in a file.
	blob := []byte(`{"steals": 4, "zone_ids": ["a","b"], "meter": [0.1, 0.5, 0.9]}`)
	var obs Observation
	require.NoError(t, json.Unmarshal(blob, &obs))

	c := Check{
		Behavior: "stealing occurs, zones differ, meter rises",
		Predicates: []Predicate{
			{Field: "steals", Op: OpGt, Value: F(0)},
			{Field: "zone_ids", Op: OpDiffer},
			{Field: "meter", Op: OpMonotonic},
		},
	}
	res := Evaluate(c, obs)
	assert.True(t, res.Passed, "json-decoded observation must evaluate like a native one: %v", res.Failures)
}

func TestEvaluate_Equals(t *testing.T) {
	obs := Observation{
		"is_active":    true,
		"return_value": "Ok",
		"status":       200.0,
	}

	cases := []struct {
		name string
		pred Predicate
		want bool
	}{
		{"bool true pass", Predicate{Field: "is_active", Op: OpEquals, Member: true}, true},
		{"bool false fail", Predicate{Field: "is_active", Op: OpEquals, Member: false}, false},
		{"string match pass", Predicate{Field: "return_value", Op: OpEquals, Member: "Ok"}, true},
		{"string mismatch fail", Predicate{Field: "return_value", Op: OpEquals, Member: "Err"}, false},
		{"numeric match pass", Predicate{Field: "status", Op: OpEquals, Member: 200.0}, true},
		{"absent field fail", Predicate{Field: "missing", Op: OpEquals, Member: "x"}, false},
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

	// JSON-decoded case: bool and enum string from a JSON blob.
	t.Run("json decoded bool and enum", func(t *testing.T) {
		blob := []byte(`{"is_active": true, "code": "NoteOn"}`)
		var jsonObs Observation
		require.NoError(t, json.Unmarshal(blob, &jsonObs))
		pass := Evaluate(Check{Predicates: []Predicate{
			{Field: "is_active", Op: OpEquals, Member: true},
			{Field: "code", Op: OpEquals, Member: "NoteOn"},
		}}, jsonObs)
		assert.True(t, pass.Passed, "json-decoded equals must work: %v", pass.Failures)
	})
}

func TestEvaluate_EqTypeTolerant(t *testing.T) {
	obs := Observation{
		"result": "Ok",
		"active": true,
		"count":  3.0,
	}

	// Numeric eq is unchanged.
	res := Evaluate(Check{Predicates: []Predicate{{Field: "count", Op: OpEq, Value: F(3)}}}, obs)
	assert.True(t, res.Passed)
	assert.Empty(t, res.StructuralFailures)

	// Non-numeric field WITH a Member falls back to equals-style comparison.
	res = Evaluate(Check{Predicates: []Predicate{{Field: "result", Op: OpEq, Member: "Ok"}}}, obs)
	assert.True(t, res.Passed, "eq on string field with matching member must pass")
	assert.Empty(t, res.StructuralFailures)

	res = Evaluate(Check{Predicates: []Predicate{{Field: "result", Op: OpEq, Member: "Err"}}}, obs)
	assert.False(t, res.Passed, "eq on string field with mismatched member must fail")
	require.NotEmpty(t, res.Failures)
	assert.Empty(t, res.StructuralFailures, "a value mismatch is behavioral, not structural")

	res = Evaluate(Check{Predicates: []Predicate{{Field: "active", Op: OpEq, Member: true}}}, obs)
	assert.True(t, res.Passed, "eq on bool field with matching member must pass")

	res = Evaluate(Check{Predicates: []Predicate{{Field: "active", Op: OpEq, Member: false}}}, obs)
	assert.False(t, res.Passed, "eq on bool field with mismatched member must fail")
	assert.Empty(t, res.StructuralFailures)

	// Non-numeric field with NO Member cannot ever be compared: structural.
	res = Evaluate(Check{Predicates: []Predicate{{Field: "result", Op: OpEq, Value: F(0)}}}, obs)
	assert.False(t, res.Passed)
	require.NotEmpty(t, res.Failures)
	require.NotEmpty(t, res.StructuralFailures, "eq on non-numeric field with no member must be structural")
	assert.Contains(t, res.StructuralFailures[0], "structural")
}

func TestEvaluate_StructuralClassification(t *testing.T) {
	obs := Observation{
		"num":        3.0,
		"str":        "hello",
		"list":       []any{1.0, 2.0, 3.0},
		"mixed_list": []any{1.0, "x"},
		"short_list": []any{1.0},
	}

	structuralCases := []Predicate{
		{Field: "str", Op: OpGt, Value: F(0)},
		{Field: "str", Op: OpRange, Min: F(0), Max: F(10)},
		{Field: "num", Op: OpCount, Value: F(1)},
		{Field: "num", Op: OpDistinctCount, Value: F(1)},
		{Field: "num", Op: OpDiffer},
		{Field: "num", Op: OpMonotonic},
		{Field: "mixed_list", Op: OpMonotonic},
		{Field: "num", Op: OpSetContains, Member: 1.0},
		{Field: "num", Op: Op("bogus")},
		{Field: "str", Op: OpEq, Value: F(0)},
	}
	for _, p := range structuralCases {
		t.Run(string(p.Op)+" structural", func(t *testing.T) {
			res := Evaluate(Check{Predicates: []Predicate{p}}, obs)
			assert.False(t, res.Passed)
			assert.NotEmpty(t, res.StructuralFailures, "expected structural classification for %+v", p)
		})
	}

	behavioralCases := []Predicate{
		{Field: "short_list", Op: OpCount, Value: F(5)},
		{Field: "list", Op: OpDistinctCount, Value: F(99)},
	}
	for _, p := range behavioralCases {
		t.Run(string(p.Op)+" behavioral", func(t *testing.T) {
			res := Evaluate(Check{Predicates: []Predicate{p}}, obs)
			assert.False(t, res.Passed)
			assert.Empty(t, res.StructuralFailures, "expected behavioral (non-structural) classification for %+v", p)
		})
	}
}

func TestVerify_StructuralVerdict(t *testing.T) {
	c := Check{Predicates: []Predicate{{Field: "result", Op: OpEq, Value: F(0)}}}
	real := Observation{"result": "Ok"}
	stub := Observation{"result": "Ok"}

	v := Verify(c, real, stub)
	assert.False(t, v.Passed)
	assert.True(t, v.Structural, "eq on non-numeric field with no member must mark the verdict structural")
}

func TestVerify_NonStructuralVerdictUnaffected(t *testing.T) {
	c := Check{Predicates: []Predicate{{Field: "steals", Op: OpGt, Value: F(0)}}}
	v := Verify(c, Observation{"steals": 0.0}, Observation{"steals": 0.0})
	assert.False(t, v.Structural)
}

func TestEvaluate_TypeErrors(t *testing.T) {
	obs := Observation{
		"num":        3.0,
		"str":        "hello",
		"list":       []any{1.0, 2.0, 3.0},
		"mixed_list": []any{1.0, "x"},
		"short_list": []any{1.0},
	}

	cases := []struct {
		name string
		pred Predicate
	}{
		{"eq on string field", Predicate{Field: "str", Op: OpEq, Value: F(0)}},
		{"range on string field", Predicate{Field: "str", Op: OpRange, Min: F(0), Max: F(10)}},
		{"count on non-list field", Predicate{Field: "num", Op: OpCount, Value: F(1)}},
		{"distinct_count on non-list field", Predicate{Field: "num", Op: OpDistinctCount, Value: F(1)}},
		{"differ on non-list field", Predicate{Field: "num", Op: OpDiffer}},
		{"monotonic on non-list field", Predicate{Field: "num", Op: OpMonotonic}},
		{"monotonic with non-numeric element in list", Predicate{Field: "mixed_list", Op: OpMonotonic}},
		{"set_contains on non-list field", Predicate{Field: "num", Op: OpSetContains, Member: 1.0}},
		{"count length mismatch", Predicate{Field: "short_list", Op: OpCount, Value: F(5)}},
		{"unknown op", Predicate{Field: "num", Op: Op("bogus")}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Evaluate(Check{Predicates: []Predicate{c.pred}}, obs)
			assert.False(t, res.Passed, "expected predicate to fail")
			require.NotEmpty(t, res.Failures, "a failing predicate must record a failure message")
		})
	}
}
