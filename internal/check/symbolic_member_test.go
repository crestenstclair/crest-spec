package check

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// member:"SR" against an observed 48000 is a symbolic back-reference to a
// witness argument, compared literally. One-side-numeric equals is a type
// mismatch — the CHECK's authoring fault (structural), never a code failure.
func TestEqualsNumericVsSymbolicMemberIsStructural(t *testing.T) {
	p := Predicate{Field: "sample_rate", Op: OpEquals, Member: "SR"}
	v := Verify(Check{Behavior: "b", Predicates: []Predicate{p}},
		Observation{"sample_rate": 48000.0}, Observation{"sample_rate": 0.0})
	assert.True(t, v.Structural, "numeric observation vs non-numeric member must be structural")
	assert.False(t, v.Passed)
}

func TestEqualsStringVsNumericMemberIsStructural(t *testing.T) {
	p := Predicate{Field: "policy", Op: OpEquals, Member: 42.0}
	v := Verify(Check{Behavior: "b", Predicates: []Predicate{p}},
		Observation{"policy": "steal_oldest"}, Observation{"policy": ""})
	assert.True(t, v.Structural)
}

func TestEqualsLegitimateComparisonsUnaffected(t *testing.T) {
	// string vs string: behavioral pass and behavioral fail
	pass := Predicate{Field: "policy", Op: OpEquals, Member: "steal_oldest"}
	v := Verify(Check{Behavior: "b", Predicates: []Predicate{pass}},
		Observation{"policy": "steal_oldest"}, Observation{"policy": ""})
	assert.True(t, v.Passed)

	fail := Predicate{Field: "fmt", Op: OpEquals, Member: "clap"}
	v = Verify(Check{Behavior: "b", Predicates: []Predicate{fail}},
		Observation{"fmt": "vst3"}, Observation{"fmt": ""})
	assert.False(t, v.Passed)
	assert.False(t, v.Structural, "honest string mismatch stays behavioral")

	// number vs number stays numeric
	num := Predicate{Field: "n", Op: OpEquals, Member: 42.0}
	v = Verify(Check{Behavior: "b", Predicates: []Predicate{num}},
		Observation{"n": 42.0}, Observation{"n": 0.0})
	assert.True(t, v.Passed)

	// bool vs bool via tolerant compare
	b := Predicate{Field: "ok", Op: OpEquals, Member: true}
	v = Verify(Check{Behavior: "b", Predicates: []Predicate{b}},
		Observation{"ok": true}, Observation{"ok": false})
	assert.True(t, v.Passed)
}
