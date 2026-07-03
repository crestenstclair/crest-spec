package check

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Predicate field names are JSON keys a witness must emit verbatim. A field
// name that is a prose description ("renderBlocks field of the SceneStep
// obtained by deserializing...") can never be emitted sanely — it is a
// structural authoring error, caught at evaluation as a backstop and at
// tasks-commit as the loud early gate.
func TestDescriptionFieldNamesAreStructural(t *testing.T) {
	bad := Predicate{Field: "renderBlocks field of the SceneStep obtained by deserializing the serialized form", Op: OpEq, Value: F(1)}
	v := Verify(Check{Behavior: "b", Predicates: []Predicate{bad}},
		Observation{"x": 1.0}, Observation{"x": 0.0})
	assert.True(t, v.Structural, "sentence-length field names are authoring errors")
	assert.False(t, v.Passed)
}

func TestReasonableFieldNamesPass(t *testing.T) {
	for _, f := range []string{"renderBlocks_roundtrip", "peak", "events applied count", "steal_policy.victim_id"} {
		p := Predicate{Field: f, Op: OpEq, Value: F(1)}
		v := Verify(Check{Behavior: "b", Predicates: []Predicate{p}},
			Observation{f: 1.0}, Observation{f: 0.0})
		assert.True(t, v.Passed, "field %q should be acceptable", f)
	}
}

func TestFieldNameValid(t *testing.T) {
	assert.False(t, FieldNameValid("the value returned by calling try_new with zero and unwrapping the error result"))
	assert.True(t, FieldNameValid("try_new_zero_rejected"))
}
