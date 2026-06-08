# Evolution Pillar — Stage 2: Default Project-Level Validations — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add a project-level `validations` concept to the CUE spec, run those whole-crate checks (clippy, fmt, build, test) at wave verification, and declare them as defaults in the crest-synth fixture — so generated Rust is held to lint/format/build/test standards and those failures become learning signal.

**Architecture:** Add `Validations []Validation` to the CUE `Project` type. `VerifyWave` already runs whole-project commands every wave (the config-driven `TypeCheckCommand`/`TestCommand`); we add a sibling step that runs `registry.Project.Validations` via the existing `RunValidations` helper (which already truncates command output). The fixture's `base.cue` declares the defaults and the Makefile gains `lint`/`fmt` targets.

**Tech Stack:** Go, CUE, existing `internal/spec/validate.go` (`RunValidations`, `Validation`), testify.

**Reference:** Design doc `docs/superpowers/specs/2026-06-07-crest-spec-iterative-evolution-design.md` (Component 2).

---

## File Structure

- Modify: `internal/cue/types.go` — add `Validations []Validation` to `Project`.
- Modify: `internal/cue/loader_test.go` or `internal/cue/registry_test.go` — assert project validations load (small test).
- Modify: `internal/spec/session.go` — `VerifyWave` runs project validations; add `runProjectValidations` helper.
- Test: `internal/spec/session_test.go` (or a new `verify_test.go`) — `runProjectValidations` records failures.
- Modify: `fixtures/crest-synth/phases/base.cue` — add `project: validations: [...]` and `lint`/`fmt` Makefile targets.

---

## Task 1: Add `Validations` to the CUE `Project` type

**Files:**
- Modify: `internal/cue/types.go`
- Test: `internal/cue/loader_test.go`

- [ ] **Step 1: Write a failing test that project-level validations load**

Add to `internal/cue/loader_test.go`:

```go
func TestLoad_ProjectValidations(t *testing.T) {
	dir := t.TempDir()
	cue := `package p
project: name: "v"
project: validations: [
	{kind: "compiles", command: ["cargo", "build"], description: "builds"},
	{kind: "test", command: ["cargo", "test"]},
]
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "p.cue"), []byte(cue), 0o644))
	p, err := Load(dir)
	require.NoError(t, err)
	require.Len(t, p.Validations, 2)
	assert.Equal(t, "compiles", p.Validations[0].Kind)
	assert.Equal(t, []string{"cargo", "build"}, p.Validations[0].Command)
}
```

Ensure `os` and `path/filepath` are imported in the test file (they may already be; add if missing).

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/cue/ -run TestLoad_ProjectValidations -v`
Expected: FAIL — `p.Validations` undefined (Project has no such field).

- [ ] **Step 3: Add the field**

In `internal/cue/types.go`, in the `Project` struct, add a `Validations` field (place it after `Invariants`):

```go
type Project struct {
	Name       string                `json:"name"`
	Layers     []string              `json:"layers"`
	LayerRules map[string]LayerRule  `json:"layerRules"`
	Meta       Meta                  `json:"meta"`
	Contexts   map[string]Context    `json:"contexts"`
	Adapters   map[string]Adapter    `json:"adapters"`
	AssetKinds map[string]AssetKind  `json:"assetKinds"`
	Assets     map[string]Asset      `json:"assets"`
	Invariants FlexInvariants        `json:"invariants"`
	Validations []Validation         `json:"validations,omitempty"`
	ContextMap FlexContextMap        `json:"contextMap"`
}
```

(The `Validation` type already exists in `types.go`.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cue/ -run TestLoad_ProjectValidations -v`
Expected: PASS.

- [ ] **Step 5: Run the cue package tests (nothing else broke)**

Run: `go test ./internal/cue/`
Expected: PASS (including the existing `TestLoad_*` and the phased-fixture loader test).

- [ ] **Step 6: Commit**

```bash
git add internal/cue/types.go internal/cue/loader_test.go
git commit -m "feat(cue): add project-level validations field"
```

---

## Task 2: Run project validations in `VerifyWave`

**Files:**
- Modify: `internal/spec/session.go`
- Test: `internal/spec/session_test.go`

> **Context:** `VerifyWave(ctx, sessionID, waveIndex)` already appends `WaveError`s for errored/rejected resources and runs `s.cfg.TypeCheckCommand` / `TestCommand` via `runVerificationCommand`. `s.Plan(ctx)` returns a `*PlanResult` with `.Registry.Project.Validations`. `RunValidations(ctx, []cuepkg.Validation, cwd) ([]ValidationResult, error)` (in `internal/spec/validate.go`) runs structured validations and returns per-validation results with `.Passed`, `.Kind`, `.Message` (already truncated). Project root cwd = `filepath.Dir(s.cfg.SpecDir)`.

- [ ] **Step 1: Write a failing test for the helper**

Add to `internal/spec/session_test.go`:

```go
func TestRunProjectValidations_RecordsFailures(t *testing.T) {
	s := &Spec{cfg: config.Config{SpecDir: t.TempDir() + "/spec"}}
	result := &WaveVerifyResult{Passed: true}

	vals := []cuepkg.Validation{
		{Kind: "compiles", Command: []string{"sh", "-c", "exit 0"}},
		{Kind: "test", Command: []string{"sh", "-c", "echo boom >&2; exit 1"}},
	}

	s.runProjectValidations(context.Background(), vals, nil, result)

	assert.False(t, result.Passed)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "project_validation", result.Errors[0].Kind)
	assert.Contains(t, result.Errors[0].Message, "test")
}

func TestRunProjectValidations_AllPass(t *testing.T) {
	s := &Spec{cfg: config.Config{SpecDir: t.TempDir() + "/spec"}}
	result := &WaveVerifyResult{Passed: true}
	s.runProjectValidations(context.Background(), []cuepkg.Validation{
		{Kind: "compiles", Command: []string{"sh", "-c", "exit 0"}},
	}, nil, result)
	assert.True(t, result.Passed)
	assert.Empty(t, result.Errors)
}
```

Check the test file's imports include `context`, `config` (`github.com/crestenstclair/crest-spec/internal/config`), `cuepkg` (`github.com/crestenstclair/crest-spec/internal/cue`), and testify `assert`/`require`. Add any missing. If constructing `Spec{cfg: ...}` directly doesn't compile (e.g. `cfg` is unexported but the test is in `package spec`, so it is accessible), keep it; the test is in-package.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/spec/ -run TestRunProjectValidations -v`
Expected: FAIL — `s.runProjectValidations` undefined.

- [ ] **Step 3: Implement the helper**

In `internal/spec/session.go`, add:

```go
// runProjectValidations runs whole-crate validations declared at project level
// (e.g. clippy/fmt/build/test) in the project root and records any failure as a
// WaveError. Command output is already truncated by RunValidations.
func (s *Spec) runProjectValidations(ctx context.Context, validations []cuepkg.Validation, resources []store.SessionResource, result *WaveVerifyResult) {
	if len(validations) == 0 {
		return
	}
	cwd := filepath.Dir(s.cfg.SpecDir)
	results, err := RunValidations(ctx, validations, cwd)
	if err != nil {
		result.Passed = false
		result.Errors = append(result.Errors, WaveError{
			Kind:    "project_validation",
			Message: fmt.Sprintf("project validation error: %v", err),
		})
		return
	}
	for _, r := range results {
		if r.Passed {
			continue
		}
		result.Passed = false
		result.Errors = append(result.Errors, WaveError{
			ResourceID: s.attributeErrorToResource(r.Message, resources),
			Kind:       "project_validation",
			Message:    fmt.Sprintf("%s: %s", r.Kind, r.Message),
		})
	}
}
```

Confirm `internal/spec/session.go` already imports `cuepkg "github.com/crestenstclair/crest-spec/internal/cue"`, `fmt`, `path/filepath`, and `store`. They are used elsewhere in the file; add any that is missing.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/spec/ -run TestRunProjectValidations -v`
Expected: PASS (2 subtests).

- [ ] **Step 5: Wire it into `VerifyWave`**

In `VerifyWave`, immediately after the two existing `s.runVerificationCommand(...)` calls and before `return result`, add:

```go
	if plan, err := s.Plan(ctx); err == nil && plan != nil {
		s.runProjectValidations(ctx, plan.Registry.Project.Validations, resources, result)
	}
```

- [ ] **Step 6: Run the spec package tests**

Run: `go test ./internal/spec/`
Expected: PASS (the new tests plus all existing). If any existing `VerifyWave` test now fails because `s.Plan` is called, inspect: `Plan` should no-op gracefully when there's no spec dir in that test's `Spec`. If a test constructs `Spec` without a usable spec dir and `Plan` errors, the `err == nil` guard skips project validations — so behavior is unchanged. Confirm.

- [ ] **Step 7: Commit**

```bash
git add internal/spec/session.go internal/spec/session_test.go
git commit -m "feat(spec): run project-level validations at wave verification"
```

---

## Task 3: Declare default validations + Makefile targets in the fixture

**Files:**
- Modify: `fixtures/crest-synth/phases/base.cue`

- [ ] **Step 1: Add `project: validations` to `base.cue`**

In `fixtures/crest-synth/phases/base.cue`, after the `project: meta: {...}` block (near the top, before or after the Kernel section — anywhere at top level is fine), add:

```cue
// ── Default whole-crate validations (run at wave verification) ──
project: validations: [
	{kind: "compiles", command: ["cargo", "fmt", "--", "--check"], description: "rustfmt clean"},
	{kind: "compiles", command: ["cargo", "clippy", "--", "-D", "warnings"], description: "clippy clean"},
	{kind: "compiles", command: ["cargo", "build"], description: "crate builds"},
	{kind: "test", command: ["cargo", "test"], description: "tests pass"},
]
```

- [ ] **Step 2: Add `lint` and `fmt` targets to the `BuildMakefile` asset prompt**

In `base.cue`, find the `BuildMakefile` asset:

```cue
project: assets: BuildMakefile: {
	kind:        "makefile"
	description: "Build automation for the crest-synth project"
	prompts: ["Default target: build", "test: cargo test", "check: cargo check", "clean: cargo clean", "run: cargo run"]
}
```

Add two prompts to the `prompts` list so it becomes:

```cue
project: assets: BuildMakefile: {
	kind:        "makefile"
	description: "Build automation for the crest-synth project"
	prompts: ["Default target: build", "test: cargo test", "check: cargo check", "clean: cargo clean", "run: cargo run", "lint: cargo clippy -- -D warnings", "fmt: cargo fmt -- --check"]
}
```

- [ ] **Step 3: Verify all phases still load (the phased-fixture loader test)**

Run: `go test ./internal/cue/ -run TestLoad_PhasedFixture -v`
Expected: PASS (10 subtests) — `project.validations` in base.cue must not break any phase load.

- [ ] **Step 4: Verify the validations are present after loading phase 1**

This is covered structurally by Task 1's load test for the field. Additionally add a quick assertion to the phased fixture test family — add to `internal/cue/phases_test.go`:

```go
func TestPhasedFixture_HasDefaultValidations(t *testing.T) {
	dir := phasesDir()
	if _, err := os.Stat(filepath.Join(dir, "base.cue")); err != nil {
		t.Skipf("phased fixture not present: %v", err)
	}
	tmp := t.TempDir()
	assemblePhaseDir(t, dir, tmp, 1)
	proj, err := Load(tmp)
	require.NoError(t, err)
	require.NotEmpty(t, proj.Validations, "base.cue should declare default project validations")
	var kinds []string
	for _, v := range proj.Validations {
		kinds = append(kinds, strings.Join(v.Command, " "))
	}
	joined := strings.Join(kinds, " | ")
	assert.Contains(t, joined, "clippy")
	assert.Contains(t, joined, "fmt")
}
```

Ensure `strings` is imported in `phases_test.go` (it already is).

- [ ] **Step 5: Run it**

Run: `go test ./internal/cue/ -run TestPhasedFixture_HasDefaultValidations -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add fixtures/crest-synth/phases/base.cue internal/cue/phases_test.go
git commit -m "feat(fixture): declare default clippy/fmt/build/test validations + Makefile lint/fmt targets"
```

---

## Task 4: Full sweep

- [ ] **Step 1:** `go build ./...` → success.
- [ ] **Step 2:** `go test ./...` → all pass.
- [ ] **Step 3:** `make build` → `bin/crest-spec` produced.
- [ ] **Step 4:** Confirm clean tree: `git status`.

---

## Self-Review

1. **Spec coverage (Component 2):** project-level `validations` added to CUE ✔; run at wave verification via `runProjectValidations` ✔; base.cue declares clippy/fmt/build/test defaults ✔; Makefile gains lint/fmt ✔; whole-crate (run in project root, not per-resource) ✔.
2. **No placeholders:** exact code, paths, commands, expected output throughout. ✔
3. **Type consistency:** `runProjectValidations(ctx, []cuepkg.Validation, []store.SessionResource, *WaveVerifyResult)` defined in Task 2 Step 3 and used identically in Step 5; `Project.Validations` field name matches base.cue `project: validations`. ✔
4. **Behavior safety:** `s.Plan(ctx)` failure is guarded (`err == nil`) so existing `VerifyWave` tests with no real spec dir are unaffected. ✔

## Notes
- Running clippy/build every wave means early waves (incomplete crate) will fail these checks — this is consistent with the existing `TypeCheckCommand`/`TestCommand` behavior and feeds the retry/resolve/learning path. The design accepts this (Component 2).
- Do not run validations per-resource — they are whole-crate and belong only at wave verification.
