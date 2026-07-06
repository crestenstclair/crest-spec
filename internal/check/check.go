// Package check evaluates whether a structured observation exhibits a claimed
// behavior, using typed predicates and a falsification gate. It is pure: data
// in, verdict out — no I/O, no LLM.
package check

import (
	"fmt"
	"math"
)

// Op is a typed predicate operator over an Observation field.
type Op string

const (
	OpEq            Op = "eq"
	OpDiffer        Op = "differ"
	OpGt            Op = "gt"
	OpGte           Op = "gte"
	OpLt            Op = "lt"
	OpLte           Op = "lte"
	OpRange         Op = "range"
	OpCount         Op = "count"
	OpDistinctCount Op = "distinct_count"
	OpMonotonic     Op = "monotonic"
	OpSetContains   Op = "set_contains"
	OpEquals        Op = "equals"
)

// Predicate is one typed assertion over a named field of an Observation.
type Predicate struct {
	Field string `json:"field"`
	Op    Op     `json:"op"`
	// Value/Min/Max are pointers so a MISSING key is distinguishable from an
	// explicit 0 — a predicate whose required bound is absent is a structural
	// authoring error (it would otherwise silently compare against 0).
	Value  *float64 `json:"value,omitempty"`  // eq/gt/gte/lt/lte/count/distinct_count
	Min    *float64 `json:"min,omitempty"`    // range
	Max    *float64 `json:"max,omitempty"`    // range
	Member any      `json:"member"` // set_contains/equals (no omitempty: a zero-valued member must survive round-trip)
}

// F is a convenience for constructing predicate bounds in code and tests.
func F(v float64) *float64 { return &v }

// Check is a behavioral claim plus the predicates that must all hold for the
// observation to count as exhibiting it.
type Check struct {
	Behavior   string      `json:"behavior"`
	Predicates []Predicate `json:"predicates"`
}

// Observation is a structured record of measured values. In production it is
// decoded from JSON, so values are float64/string/bool/[]any.
type Observation map[string]any

// Result is the outcome of evaluating a Check against one Observation.
type Result struct {
	Passed   bool
	Failures []string
	// StructuralFailures holds the subset of Failures that can never pass
	// regardless of implementation: a type mismatch (field present but the
	// wrong shape), an unknown op, or an eq predicate on a non-numeric field
	// with no member to compare against. These are authoring mistakes in the
	// predicate itself, not something a regenerated implementation can fix.
	StructuralFailures []string
}

// Evaluate reports whether every predicate in c holds for obs.
func Evaluate(c Check, obs Observation) Result {
	res := Result{Passed: true}
	for _, p := range c.Predicates {
		ok, msg, structural := evalPredicate(p, obs)
		if !ok {
			res.Passed = false
			res.Failures = append(res.Failures, msg)
			if structural {
				res.StructuralFailures = append(res.StructuralFailures, msg)
			}
		}
	}
	return res
}

// Verdict is the outcome of a falsification-gated check: the behavior must be
// exhibited by the real observation AND absent from the stub observation.
type Verdict struct {
	Passed     bool
	RealResult Result
	StubResult Result
	Theater    bool // true if the check also passed on the stub (no teeth)
	Structural bool // true if the real observation hit a structural (unfixable-by-regeneration) failure
	Reason     string
}

// Verify gates c against both observations. A check that the stub passes is
// theater and is rejected regardless of the real result.
func Verify(c Check, real, stub Observation) Verdict {
	rr := Evaluate(c, real)
	sr := Evaluate(c, stub)
	v := Verdict{RealResult: rr, StubResult: sr, Structural: len(rr.StructuralFailures) > 0}
	switch {
	case sr.Passed:
		v.Theater = true
		v.Reason = "check passes on the stub observation: it has no teeth"
	case !rr.Passed:
		v.Reason = fmt.Sprintf("behavior not exhibited: %v", rr.Failures)
	default:
		v.Passed = true
		v.Reason = "behavior exhibited; check rejects the stub"
	}
	return v
}

// evalPredicate reports (ok, message, structural) for a single predicate
// against obs. structural is true only when the failure can never pass
// regardless of implementation (wrong type, unknown op, or an eq predicate
// that has no way to compare a non-numeric field).
// FieldNameValid reports whether a predicate field name is a plausible
// measurable key. A witness must emit the field verbatim as a JSON key, so a
// prose description (long, many words) is an authoring error the check can
// never recover from.
func FieldNameValid(field string) bool {
	if len(field) == 0 || len(field) > 64 {
		return false
	}
	spaces := 0
	for _, r := range field {
		switch r {
		case '(', ')', '[', ']', '{', '}', ':', '"', '\'', '`', ',', ';':
			// Expression syntax — "ParameterId::new(42).value()" is code,
			// not a key a witness can emit.
			return false
		case ' ':
			spaces++
		}
	}
	return spaces < 4
}

func evalPredicate(p Predicate, obs Observation) (bool, string, bool) {
	if !FieldNameValid(p.Field) {
		return false, fmt.Sprintf("field name %q is a description, not a measurable key (structural)", p.Field), true
	}
	raw, ok := obs[p.Field]
	if !ok {
		return false, fmt.Sprintf("%s: field %q absent from observation", p.Op, p.Field), false
	}
	switch p.Op {
	case OpEq:
		return evalEq(p, raw)
	case OpGt, OpGte, OpLt, OpLte:
		if p.Value == nil {
			return false, fmt.Sprintf("%s: predicate is missing its required value (structural)", p.Op), true
		}
		n, ok := asNumber(raw)
		if !ok {
			return false, fmt.Sprintf("%s: field %q is not numeric", p.Op, p.Field), true
		}
		ok2, msg := cmpNumber(p.Op, n, *p.Value)
		return ok2, msg, false
	case OpRange:
		if p.Min == nil || p.Max == nil {
			return false, "range: predicate is missing its required min/max bounds (structural)", true
		}
		if *p.Min > *p.Max {
			return false, fmt.Sprintf("range: min %v > max %v (structural)", *p.Min, *p.Max), true
		}
		n, ok := asNumber(raw)
		if !ok {
			return false, fmt.Sprintf("range: field %q is not numeric", p.Field), true
		}
		if n < *p.Min || n > *p.Max {
			return false, fmt.Sprintf("range: %q=%v not in [%v,%v]", p.Field, n, *p.Min, *p.Max), false
		}
		return true, "", false
	case OpCount:
		if p.Value == nil {
			return false, "count: predicate is missing its required value (structural)", true
		}
		l, ok := asList(raw)
		if !ok {
			return false, fmt.Sprintf("count: field %q is not a list", p.Field), true
		}
		if float64(len(l)) != *p.Value {
			return false, fmt.Sprintf("count: %q has %d items, want %v", p.Field, len(l), *p.Value), false
		}
		return true, "", false
	case OpDistinctCount:
		if p.Value == nil {
			return false, "distinct_count: predicate is missing its required value (structural)", true
		}
		l, ok := asList(raw)
		if !ok {
			return false, fmt.Sprintf("distinct_count: field %q is not a list", p.Field), true
		}
		if d := distinct(l); float64(d) < *p.Value {
			return false, fmt.Sprintf("distinct_count: %q has %d distinct, want >= %v", p.Field, d, *p.Value), false
		}
		return true, "", false
	case OpDiffer:
		l, ok := asList(raw)
		if !ok {
			return false, fmt.Sprintf("differ: field %q is not a list", p.Field), true
		}
		if distinct(l) < 2 {
			return false, fmt.Sprintf("differ: %q values are all identical", p.Field), false
		}
		return true, "", false
	case OpMonotonic:
		l, ok := asList(raw)
		if !ok {
			return false, fmt.Sprintf("monotonic: field %q is not a list", p.Field), true
		}
		return monotonicIncreasing(l, p.Field)
	case OpSetContains:
		l, ok := asList(raw)
		if !ok {
			return false, fmt.Sprintf("set_contains: field %q is not a list", p.Field), true
		}
		for _, e := range l {
			if equalValues(e, p.Member) {
				return true, "", false
			}
		}
		return false, fmt.Sprintf("set_contains: %q does not contain %v", p.Field, p.Member), false
	case OpEquals:
		if p.Member == nil {
			return false, "equals: predicate is missing its required member (structural)", true
		}
		return evalEqualsLike("equals", p.Field, raw, p.Member)
	default:
		return false, fmt.Sprintf("unknown op %q", p.Op), true
	}
}

// evalEq implements the type-tolerant eq operator (Fix A). A numeric field is
// compared numerically as before. A non-numeric field falls back to
// equals-style comparison IF a member was supplied — this is the common case
// of an eq predicate emitted against a string/bool/enum field. If no member
// was supplied there is nothing to compare against and the predicate can
// never pass: that is a structural (authoring) error, not a behavioral one.
func evalEq(p Predicate, raw any) (bool, string, bool) {
	if n, ok := asNumber(raw); ok {
		if p.Value == nil {
			if p.Member != nil {
				return evalEqualsLike("eq", p.Field, raw, p.Member)
			}
			return false, "eq: predicate is missing its required value (structural)", true
		}
		ok2, msg := cmpNumber(OpEq, n, *p.Value)
		return ok2, msg, false
	}
	if p.Member != nil {
		return evalEqualsLike("eq", p.Field, raw, p.Member)
	}
	return false, fmt.Sprintf("eq: field %q is not numeric and no member provided (structural)", p.Field), true
}

// evalEqualsLike is the shared two-branch comparison used by both "equals"
// and type-tolerant "eq": numeric if both sides coerce to a number, otherwise
// a string comparison via fmt formatting (handles bool/string/enum alike).
func evalEqualsLike(label, field string, raw, member any) (bool, string, bool) {
	nField, fieldIsNum := asNumber(raw)
	nMember, memberIsNum := asNumber(member)
	if fieldIsNum && memberIsNum {
		if nField != nMember {
			return false, fmt.Sprintf("%s: %q got %v, want %v", label, field, raw, member), false
		}
		return true, "", false
	}
	// Exactly one side numeric = type mismatch. The classic case is a
	// symbolic member ("SR") back-referencing a witness argument and being
	// compared literally against the measured number — the CHECK's authoring
	// fault, never the code's.
	if fieldIsNum != memberIsNum {
		return false, fmt.Sprintf("%s: %q observed %v but member %v is a different type — likely a symbolic member instead of a concrete literal (structural)", label, field, raw, member), true
	}
	if fmt.Sprintf("%v", raw) != fmt.Sprintf("%v", member) {
		return false, fmt.Sprintf("%s: %q got %v, want %v", label, field, raw, member), false
	}
	return true, "", false
}

func cmpNumber(op Op, got, want float64) (bool, string) {
	var ok bool
	switch op {
	case OpEq:
		ok = got == want
	case OpGt:
		ok = got > want
	case OpGte:
		ok = got >= want
	case OpLt:
		ok = got < want
	case OpLte:
		ok = got <= want
	}
	if !ok {
		return false, fmt.Sprintf("%s: got %v, want %s %v", op, got, op, want)
	}
	return true, ""
}

func asNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func asList(v any) ([]any, bool) {
	l, ok := v.([]any)
	return l, ok
}

func distinct(l []any) int {
	seen := map[string]struct{}{}
	for _, e := range l {
		seen[fmt.Sprintf("%v", e)] = struct{}{}
	}
	return len(seen)
}

func monotonicIncreasing(l []any, field string) (bool, string, bool) {
	prev := math.Inf(-1)
	for i, e := range l {
		n, ok := asNumber(e)
		if !ok {
			return false, fmt.Sprintf("monotonic: %q[%d] is not numeric", field, i), true
		}
		if n <= prev {
			return false, fmt.Sprintf("monotonic: %q not strictly increasing at index %d (%v <= %v)", field, i, n, prev), false
		}
		prev = n
	}
	return true, "", false
}

func equalValues(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}
