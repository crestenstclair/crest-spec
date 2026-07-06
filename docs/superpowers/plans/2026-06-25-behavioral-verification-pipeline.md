# Behavioral Verification Pipeline — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the crest-spec engine a deterministic, falsification-gated behavioral-check evaluator so a verdict stops being one lenient LLM boolean and becomes a typed predicate the engine runs itself.

**Architecture:** A new pure `internal/check` package evaluates **typed predicates** (A) over a structured **observation**, and a **falsification gate** (C) passes a check only if it holds on the real observation *and* fails on a stub. No LLM, no I/O, fully unit-testable. Later plans (2–4) persist these, derive them per resource, generate the witnesses that produce observations, and graduate proven checks into the terse spec.

**Tech Stack:** Go 1.x, `github.com/crestenstclair/crest-spec`, `testify/require` + `testify/assert`, `sqlc` v1.31.1, sqlite (`migrations/` + `sql/queries/`, generated to `internal/db`).

## Global Constraints

- Module path: `github.com/crestenstclair/crest-spec` — all imports use this prefix.
- `internal/check` is **pure**: no `database/sql`, no `os`, no `exec`, no network. It takes data in, returns verdicts out. This is what makes it the trustworthy core.
- Tests: `testify/require` for fatal assertions, `testify/assert` for non-fatal, plain `func TestXxx(t *testing.T)` (match `internal/spec/validate_test.go`).
- Observations arrive as JSON in production, so values are `float64`, `string`, `bool`, `[]any` — the evaluator must coerce these, never assume Go-native `int`.
- A check that passes on a no-op stub is **theater** and must be rejected by the engine, not trusted.
- Run `go test ./internal/check/...` after each task; run `go build ./...` before each commit.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/check/check.go` | Types (`Op`, `Predicate`, `Check`, `Observation`, `Result`, `Verdict`), `Evaluate`, `Verify`, coercion + op helpers |
| `internal/check/check_test.go` | All unit tests for the evaluator and falsification gate |

Everything in Plan 1 lives in these two files. The package has one responsibility: **decide whether a structured observation exhibits a claimed behavior, with teeth.**

---

### Task 1: Package skeleton, types, numeric ops, and aggregation

**Files:**
- Create: `internal/check/check.go`
- Test: `internal/check/check_test.go`

**Interfaces:**
- Consumes: nothing (leaf package).
- Produces:
  - `type Op string` with consts `OpEq, OpDiffer, OpGt, OpGte, OpLt, OpLte, OpRange, OpCount, OpDistinctCount, OpMonotonic, OpSetContains`.
  - `type Predicate struct { Field string; Op Op; Value float64; Min float64; Max float64; Member any }`
  - `type Check struct { Behavior string; Predicates []Predicate }`
  - `type Observation map[string]any`
  - `type Result struct { Passed bool; Failures []string }`
  - `func Evaluate(c Check, obs Observation) Result`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/check/...`
Expected: FAIL — `undefined: Observation` / package has no Go files.

- [ ] **Step 3: Write minimal implementation**

```go
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

// _ keeps math imported until range/monotonic ops land in later tasks.
var _ = math.Inf
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/check/...`
Expected: PASS (both tests, all sub-cases).

- [ ] **Step 5: Commit**

```bash
git add internal/check/check.go internal/check/check_test.go
git commit -m "feat(check): behavioral predicate evaluator with numeric ops"
```

---

### Task 2: Range op

**Files:**
- Modify: `internal/check/check.go` (add `OpRange` case to `evalPredicate`)
- Test: `internal/check/check_test.go`

**Interfaces:**
- Consumes: `Predicate.Min`, `Predicate.Max` (defined in Task 1).
- Produces: `range` semantics — field is numeric and within `[Min, Max]` inclusive.

- [ ] **Step 1: Write the failing test**

```go
func TestEvaluate_Range(t *testing.T) {
	obs := Observation{"cutoff": 12000.0}
	pass := Evaluate(Check{Predicates: []Predicate{
		{Field: "cutoff", Op: OpRange, Min: 20, Max: 20000},
	}}, obs)
	assert.True(t, pass.Passed)

	fail := Evaluate(Check{Predicates: []Predicate{
		{Field: "cutoff", Op: OpRange, Min: 20, Max: 1000},
	}}, obs)
	assert.False(t, fail.Passed)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/check/... -run TestEvaluate_Range`
Expected: FAIL — `range` falls into the `unknown op` default, so the pass case returns false.

- [ ] **Step 3: Write minimal implementation**

Add this case to the `switch p.Op` in `evalPredicate`, immediately after the numeric-ops case:

```go
	case OpRange:
		n, ok := asNumber(raw)
		if !ok {
			return false, fmt.Sprintf("range: field %q is not numeric", p.Field)
		}
		if n < p.Min || n > p.Max {
			return false, fmt.Sprintf("range: %q=%v not in [%v,%v]", p.Field, n, p.Min, p.Max)
		}
		return true, ""
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/check/...`
Expected: PASS (all tasks' tests).

- [ ] **Step 5: Commit**

```bash
git add internal/check/check.go internal/check/check_test.go
git commit -m "feat(check): range predicate"
```

---

### Task 3: List ops — count, distinct_count, differ

**Files:**
- Modify: `internal/check/check.go` (add list cases + `asList`, `distinct` helpers)
- Test: `internal/check/check_test.go`

**Interfaces:**
- Produces:
  - `count`: field is a list whose length equals `Value`.
  - `distinct_count`: field is a list with **at least** `Value` distinct elements.
  - `differ`: field is a list whose elements are not all identical (sugar for distinct ≥ 2).
  - `func asList(v any) ([]any, bool)`, `func distinct(l []any) int` (used by later tasks too).

- [ ] **Step 1: Write the failing test**

```go
func TestEvaluate_ListOps(t *testing.T) {
	// "two different key/velocity inputs resolve to different sample zones"
	differObs := Observation{"zone_ids": []any{"zoneA", "zoneB"}}
	sameObs := Observation{"zone_ids": []any{"zoneA", "zoneA"}}

	differ := []Predicate{{Field: "zone_ids", Op: OpDiffer}}
	assert.True(t, Evaluate(Check{Predicates: differ}, differObs).Passed)
	assert.False(t, Evaluate(Check{Predicates: differ}, sameObs).Passed)

	distinct2 := []Predicate{{Field: "zone_ids", Op: OpDistinctCount, Value: 2}}
	assert.True(t, Evaluate(Check{Predicates: distinct2}, differObs).Passed)
	assert.False(t, Evaluate(Check{Predicates: distinct2}, sameObs).Passed)

	count2 := []Predicate{{Field: "zone_ids", Op: OpCount, Value: 2}}
	assert.True(t, Evaluate(Check{Predicates: count2}, sameObs).Passed)

	notAList := Observation{"zone_ids": 3.0}
	assert.False(t, Evaluate(Check{Predicates: differ}, notAList).Passed)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/check/... -run TestEvaluate_ListOps`
Expected: FAIL — list ops hit the `unknown op` default.

- [ ] **Step 3: Write minimal implementation**

Add these cases to the `switch p.Op`:

```go
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
```

Add these helpers at the bottom of the file:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/check/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/check/check.go internal/check/check_test.go
git commit -m "feat(check): list predicates count/distinct_count/differ"
```

---

### Task 4: monotonic and set_contains

**Files:**
- Modify: `internal/check/check.go` (add cases + `monotonicIncreasing`, `equalValues` helpers; remove the `var _ = math.Inf` placeholder from Task 1)
- Test: `internal/check/check_test.go`

**Interfaces:**
- Produces:
  - `monotonic`: field is a list of numbers, strictly increasing.
  - `set_contains`: field is a list containing `Member` (string-compared).

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/check/... -run TestEvaluate_MonotonicAndSetContains`
Expected: FAIL — both ops hit `unknown op`.

- [ ] **Step 3: Write minimal implementation**

Remove the placeholder line `var _ = math.Inf` from the bottom of the file (it's now used for real). Add these cases to the `switch p.Op`:

```go
	case OpMonotonic:
		l, ok := asList(raw)
		if !ok {
			return false, fmt.Sprintf("monotonic: field %q is not a list", p.Field)
		}
		return monotonicIncreasing(l, p.Field)
	case OpSetContains:
		l, ok := asList(raw)
		if !ok {
			return false, fmt.Sprintf("set_contains: field %q is not a list", p.Field)
		}
		for _, e := range l {
			if equalValues(e, p.Member) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("set_contains: %q does not contain %v", p.Field, p.Member)
```

Add these helpers:

```go
func monotonicIncreasing(l []any, field string) (bool, string) {
	prev := math.Inf(-1)
	for i, e := range l {
		n, ok := asNumber(e)
		if !ok {
			return false, fmt.Sprintf("monotonic: %q[%d] is not numeric", field, i)
		}
		if n <= prev {
			return false, fmt.Sprintf("monotonic: %q not strictly increasing at index %d (%v <= %v)", field, i, n, prev)
		}
		prev = n
	}
	return true, ""
}

func equalValues(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/check/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/check/check.go internal/check/check_test.go
git commit -m "feat(check): monotonic and set_contains predicates"
```

---

### Task 5: The falsification gate (`Verify`)

**Files:**
- Modify: `internal/check/check.go` (add `Verdict` type + `Verify`)
- Test: `internal/check/check_test.go`

**Interfaces:**
- Consumes: `Evaluate`, `Result`.
- Produces:
  - `type Verdict struct { Passed bool; RealResult Result; StubResult Result; Theater bool; Reason string }`
  - `func Verify(c Check, real, stub Observation) Verdict` — passes **only if** the check holds on `real` AND fails on `stub`. If it holds on `stub`, `Theater=true` and `Passed=false`.

This is the anti-theater core: a check a no-op can satisfy has no teeth and is rejected even if the real observation passes.

- [ ] **Step 1: Write the failing test**

```go
func TestVerify_FalsificationGate(t *testing.T) {
	check := Check{
		Behavior:   "past the polyphony limit, voice stealing occurs",
		Predicates: []Predicate{{Field: "steals", Op: OpGt, Value: 0}},
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
	toothless := Check{Predicates: []Predicate{{Field: "steals", Op: OpGte, Value: 0}}}
	theater := Verify(toothless, real, stub)
	assert.False(t, theater.Passed)
	assert.True(t, theater.Theater)
	assert.Contains(t, theater.Reason, "teeth")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/check/... -run TestVerify_FalsificationGate`
Expected: FAIL — `undefined: Verify` / `undefined: Verdict`.

- [ ] **Step 3: Write minimal implementation**

```go
// Verdict is the outcome of a falsification-gated check: the behavior must be
// exhibited by the real observation AND absent from the stub observation.
type Verdict struct {
	Passed     bool
	RealResult Result
	StubResult Result
	Theater    bool // true if the check also passed on the stub (no teeth)
	Reason     string
}

// Verify gates c against both observations. A check that the stub passes is
// theater and is rejected regardless of the real result.
func Verify(c Check, real, stub Observation) Verdict {
	rr := Evaluate(c, real)
	sr := Evaluate(c, stub)
	v := Verdict{RealResult: rr, StubResult: sr}
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/check/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/check/check.go internal/check/check_test.go
git commit -m "feat(check): falsification gate (must-fail-on-stub)"
```

---

### Task 6: JSON-sourced observations (production fidelity)

**Files:**
- Test: `internal/check/check_test.go`
- Modify: `internal/check/check.go` only if the test surfaces a coercion gap (it should not — `encoding/json` yields `float64` and `[]any`, both already handled).

**Interfaces:**
- Consumes: everything above.
- Produces: a guarantee that an `Observation` decoded from a real JSON blob (as a witness will emit) evaluates identically to a hand-built one.

- [ ] **Step 1: Write the failing test**

```go
import "encoding/json"

func TestEvaluate_FromJSON(t *testing.T) {
	// Shape a witness would emit on stdout / in a file.
	blob := []byte(`{"steals": 4, "zone_ids": ["a","b"], "meter": [0.1, 0.5, 0.9]}`)
	var obs Observation
	require.NoError(t, json.Unmarshal(blob, &obs))

	c := Check{
		Behavior: "stealing occurs, zones differ, meter rises",
		Predicates: []Predicate{
			{Field: "steals", Op: OpGt, Value: 0},
			{Field: "zone_ids", Op: OpDiffer},
			{Field: "meter", Op: OpMonotonic},
		},
	}
	res := Evaluate(c, obs)
	assert.True(t, res.Passed, "json-decoded observation must evaluate like a native one: %v", res.Failures)
}
```

- [ ] **Step 2: Run test to verify it fails or passes**

Run: `go test ./internal/check/... -run TestEvaluate_FromJSON`
Expected: PASS immediately (coercion already handles `float64`/`[]any`). If it FAILS, the failure message names the field — extend `asNumber`/`asList` to cover the offending type, then rerun.

- [ ] **Step 3: (Only if Step 2 failed) widen coercion**

If a numeric field decoded as `json.Number`, add to `asNumber`:

```go
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
```

and `import "encoding/json"` in `check.go`. Skip this step entirely if Step 2 passed.

- [ ] **Step 4: Run the full package suite**

Run: `go test ./internal/check/... && go build ./...`
Expected: PASS and clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/check/check_test.go internal/check/check.go
git commit -m "test(check): json-sourced observations evaluate identically"
```

---

## Plan 1 Self-Review

- **Coverage:** numeric ops (T1), aggregation (T1), range (T2), count/distinct_count/differ (T3), monotonic/set_contains (T4), falsification gate (T5), JSON fidelity (T6) — every op in the design's vocabulary (A) plus the falsification gate (C) has a task. ✅
- **Placeholders:** none — every step has real code and a real command. The one deliberate temporary (`var _ = math.Inf` in T1) is explicitly removed in T4. ✅
- **Type consistency:** `Predicate`, `Check`, `Observation`, `Result`, `Verdict`, `Evaluate`, `Verify`, `asNumber`, `asList`, `distinct` are named identically everywhere they appear. ✅

---

## Subsequent Plans (roadmap — detailed after Plan 1 lands)

Plan 1 delivers the trustworthy primitive. These build on it and each produces working, testable software. They are sequenced, not yet task-decomposed, because they depend on the open questions in `docs/design-behavioral-verification-pipeline.md` §9 — those get resolved before each plan is written.

### Plan 2 — Persistence & state tools (engine-side, no LLM)
- Migration `014_behavioral_pipeline.sql`: tables `designs` (per bounded context), `tasks` (per resource), `checks` (per task: behavior + predicates JSON + stub observation), `verifications` (per generate attempt: real/stub observation + `Verdict`).
- `sql/queries/behavioral.sql` + `sqlc generate` → `internal/db`; store wrappers in `internal/store` matching the `UpsertSessionResource`/`GetSessionResource` pattern.
- New MCP state tools mirroring `specTool[A]` registration in `internal/mcp/tools.go`: `spec/design_commit`, `spec/tasks_commit`, `spec/verify` (the engine runs `check.Verify` against supplied observations and is **fail-closed**), `spec/graduate` (stub).
- Deliverable: the engine can store designs/tasks/checks and render a fail-closed verdict from real+stub observations — testable with fixtures, still no orchestration.

### Plan 3 — Orchestration (Claude-side)
- Rewrite `.claude/workflows/spec-generate.js` into `design → tasks → generate → verify`: a sub-agent derives a contract per bounded context; another derives tasks + behavioral checks per resource; the generator implements; a **separate** agent generates the witness (B) that emits the observation, and the stub (C) that must fail; artifact observers (D) for audio/UI behaviors.
- Resolves §9 open Qs: falsification mechanics (how the stub is synthesized per behavior class) and witness ownership (without per-file scope validations).
- Deliverable: a real end-to-end run where verdicts come from witnessed observations, not a lenient judge.

### Plan 4 — Graduation (anti-bloat)
- Promote a proven behavioral check from sqlite into the terse spec as a **structured behavioral validation**, extending the existing amendments `GRADUATED` path (`013_amendments.sql`). Resolves §9 open Q: the graduation CUE schema.
- Deliverable: maturing a component *replaces* prose prompts with proven behavioral checks — the spec converges toward structure instead of accreting paragraphs.

---

## Execution Handoff

Plan 1 is complete and self-contained. Plans 2–4 are roadmap and will be written once Plan 1 lands and their open questions are resolved.
