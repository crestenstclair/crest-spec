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
)

// Predicate is one typed assertion over a named field of an Observation.
type Predicate struct {
	Field  string  `json:"field"`
	Op     Op      `json:"op"`
	Value  float64 `json:"value,omitempty"`  // eq/gt/gte/lt/lte/count/distinct_count
	Min    float64 `json:"min,omitempty"`    // range
	Max    float64 `json:"max,omitempty"`    // range
	Member any     `json:"member,omitempty"` // set_contains
}

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
}

// Evaluate reports whether every predicate in c holds for obs.
func Evaluate(c Check, obs Observation) Result {
	res := Result{Passed: true}
	for _, p := range c.Predicates {
		ok, msg := evalPredicate(p, obs)
		if !ok {
			res.Passed = false
			res.Failures = append(res.Failures, msg)
		}
	}
	return res
}

func evalPredicate(p Predicate, obs Observation) (bool, string) {
	raw, ok := obs[p.Field]
	if !ok {
		return false, fmt.Sprintf("%s: field %q absent from observation", p.Op, p.Field)
	}
	switch p.Op {
	case OpEq, OpGt, OpGte, OpLt, OpLte:
		n, ok := asNumber(raw)
		if !ok {
			return false, fmt.Sprintf("%s: field %q is not numeric", p.Op, p.Field)
		}
		return cmpNumber(p.Op, n, p.Value)
	case OpRange:
		n, ok := asNumber(raw)
		if !ok {
			return false, fmt.Sprintf("range: field %q is not numeric", p.Field)
		}
		if n < p.Min || n > p.Max {
			return false, fmt.Sprintf("range: %q=%v not in [%v,%v]", p.Field, n, p.Min, p.Max)
		}
		return true, ""
	case OpCount:
		l, ok := asList(raw)
		if !ok {
			return false, fmt.Sprintf("count: field %q is not a list", p.Field)
		}
		if float64(len(l)) != p.Value {
			return false, fmt.Sprintf("count: %q has %d items, want %v", p.Field, len(l), p.Value)
		}
		return true, ""
	case OpDistinctCount:
		l, ok := asList(raw)
		if !ok {
			return false, fmt.Sprintf("distinct_count: field %q is not a list", p.Field)
		}
		if d := distinct(l); float64(d) < p.Value {
			return false, fmt.Sprintf("distinct_count: %q has %d distinct, want >= %v", p.Field, d, p.Value)
		}
		return true, ""
	case OpDiffer:
		l, ok := asList(raw)
		if !ok {
			return false, fmt.Sprintf("differ: field %q is not a list", p.Field)
		}
		if distinct(l) < 2 {
			return false, fmt.Sprintf("differ: %q values are all identical", p.Field)
		}
		return true, ""
	default:
		return false, fmt.Sprintf("unknown op %q", p.Op)
	}
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

// _ keeps math imported until range/monotonic ops land in later tasks.
var _ = math.Inf
