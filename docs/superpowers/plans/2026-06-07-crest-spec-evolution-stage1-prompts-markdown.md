# Evolution Pillar — Stage 1: Prompts as Embedded Markdown — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the hardcoded system-prompt scaffolding out of Go string literals in `internal/prompt/system.go` into markdown files embedded with `//go:embed`, so prompts become editable data — behavior preserved byte-for-byte.

**Architecture:** A new `internal/prompt/templates/` directory holds one markdown file per scaffolding section. `system.go` keeps the exact same assembly order but reads section text from the embedded files (with `{{lang}}`/`{{ext}}` substitution) instead of inline `b.WriteString(...)`. The dynamic parts driven by `project.Meta` (Style/Rules/Avoid) stay as Go code. This sets up Stage 5 (promotion of learnings into a `templates/learned/<lang>.md` file).

**Tech Stack:** Go, `embed` (stdlib), `strings.NewReplacer` for `{{lang}}`/`{{ext}}` substitution (no `text/template` — avoids escaping surprises in markdown). Tests use testify.

**Reference:** Design doc `docs/superpowers/specs/2026-06-07-crest-spec-iterative-evolution-design.md` (Component 1).

---

## File Structure

- Create: `internal/prompt/templates/role.md` — the `# Role` section (uses `{{lang}}`).
- Create: `internal/prompt/templates/output_format.md` — the `# Output Format` section (uses `{{ext}}`).
- Create: `internal/prompt/templates/folder_structure_rust.md` — Rust `# Folder Structure` (module declarations + cargo deps).
- Create: `internal/prompt/templates/folder_structure_default.md` — non-Rust `# Folder Structure`.
- Create: `internal/prompt/templates/solid.md` — the `# SOLID Principles` section (static).
- Create: `internal/prompt/templates/output_requirements.md` — the base `# Output Requirements` line (static).
- Create: `internal/prompt/templates/output_requirements_rust.md` — the two extra Rust-only lines.
- Create: `internal/prompt/templates.go` — `//go:embed templates/*.md`, an `embed.FS`, and a `renderTemplate(name string, lang, ext string) string` helper.
- Modify: `internal/prompt/system.go` — `BuildSystemPrompt` reads sections from templates instead of inline strings; same order, same output.
- Create: `internal/prompt/system_golden_test.go` — golden test asserting the rendered prompt exactly equals the captured pre-refactor output for both a Rust and a non-Rust project.
- Modify (only if a captured string needs storing): none beyond the golden test file.

**Key invariant for this stage:** `BuildSystemPrompt` output must be **identical** before and after. The golden test is written FIRST against the current implementation to lock the bytes, then the refactor must keep it green.

---

## Task 1: Capture current output as a golden test (lock the bytes)

**Files:**
- Create: `internal/prompt/system_golden_test.go`

- [ ] **Step 1: Write a test that captures current output to golden files on first run, compares after**

Create `internal/prompt/system_golden_test.go`:

```go
package prompt

import (
	"os"
	"path/filepath"
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/stretchr/testify/require"
)

// updateGolden controls whether golden files are (re)written. Run once with
// -update to capture the current BuildSystemPrompt output, then never again —
// the refactor must keep these bytes identical.
var updateGolden = os.Getenv("UPDATE_GOLDEN") == "1"

func goldenCases() map[string]*cuepkg.Project {
	return map[string]*cuepkg.Project{
		"rust": {
			Name: "rust-project",
			Meta: cuepkg.Meta{
				Language: "rust",
				Style:    "idiomatic Rust; lock-free audio thread",
				Rules:    []string{"Use interfaces for all dependencies"},
				Avoid:    []string{"heap allocation on audio thread"},
			},
		},
		"go-minimal": {
			Name: "minimal",
			Meta: cuepkg.Meta{Language: "go"},
		},
		"unset-lang": {
			Name: "nolang",
			Meta: cuepkg.Meta{},
		},
	}
}

func TestBuildSystemPrompt_Golden(t *testing.T) {
	dir := filepath.Join("testdata", "golden")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	for name, project := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			got := BuildSystemPrompt(project)
			path := filepath.Join(dir, name+".md")

			if updateGolden {
				require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
				return
			}

			want, err := os.ReadFile(path)
			require.NoError(t, err, "golden missing; run UPDATE_GOLDEN=1 go test ./internal/prompt/ -run Golden")
			require.Equal(t, string(want), got, "BuildSystemPrompt output changed; refactor must be byte-identical")
		})
	}
}
```

- [ ] **Step 2: Capture the golden files against the CURRENT (pre-refactor) implementation**

Run: `UPDATE_GOLDEN=1 go test ./internal/prompt/ -run TestBuildSystemPrompt_Golden`
Expected: PASS, and three files created: `internal/prompt/testdata/golden/{rust,go-minimal,unset-lang}.md`.

- [ ] **Step 3: Verify the golden test now passes in compare mode**

Run: `go test ./internal/prompt/ -run TestBuildSystemPrompt_Golden`
Expected: PASS (3 subtests).

- [ ] **Step 4: Commit**

```bash
git add internal/prompt/system_golden_test.go internal/prompt/testdata/golden/
git commit -m "test(prompt): golden-lock BuildSystemPrompt output before markdown refactor"
```

---

## Task 2: Create the embedded markdown templates

**Files:**
- Create: `internal/prompt/templates/role.md`
- Create: `internal/prompt/templates/output_format.md`
- Create: `internal/prompt/templates/folder_structure_rust.md`
- Create: `internal/prompt/templates/folder_structure_default.md`
- Create: `internal/prompt/templates/solid.md`
- Create: `internal/prompt/templates/output_requirements.md`
- Create: `internal/prompt/templates/output_requirements_rust.md`

> **CRITICAL — exact whitespace.** Each template's content must reproduce the exact bytes the current code emits for that section, including the trailing blank line(s). The current code ends each section with `\n\n` (one blank line). Author each file so its content ends with exactly one trailing newline at end-of-file, and the renderer adds the section separator (see Task 3). To get this exactly right, derive each file's content from the captured golden output in `testdata/golden/rust.md` (Rust sections) and `go-minimal.md` (default folder structure), not by retyping.

- [ ] **Step 1: Create `role.md`**

Content (note `{{lang}}` placeholder; the `# Role` heading and the trailing blank line are part of the section):

```
# Role

You are a {{lang}} code generator following strict SOLID principles.
```

- [ ] **Step 2: Create `output_format.md`**

```
# Output Format

Return code in fenced code blocks with path annotations:
```
` ``\n// path: src/{context}/{resource}{{ext}}\n`` `

> The literal triple-backtick fence and the `// path:` line are content. Reproduce exactly from `testdata/golden/rust.md` lines under `# Output Format`. The `{{ext}}` placeholder replaces `.rs`/`.go`/etc.

- [ ] **Step 3: Create `folder_structure_rust.md`** — copy the exact Rust `# Folder Structure` block (snake_case line, placement line, `## Module Declarations (CRITICAL)` subsection, `## Cargo Dependencies (CRITICAL)` subsection) verbatim from `testdata/golden/rust.md`.

- [ ] **Step 4: Create `folder_structure_default.md`**

```
# Folder Structure

All code goes in src/{ContextName}/{ResourceName}/ — grouped by resource, not by architectural layer.
```

- [ ] **Step 5: Create `solid.md`** — copy the exact `# SOLID Principles` block verbatim from `testdata/golden/rust.md`.

- [ ] **Step 6: Create `output_requirements.md`**

```
# Output Requirements

Generate both implementation files and unit tests.
```

- [ ] **Step 7: Create `output_requirements_rust.md`** (the two Rust-only extra lines; no heading — appended after the base output_requirements line):

```
Use `crate::` paths to reference types from other modules (e.g., `use crate::kernel::note_id::NoteId;`).
Only reference types that exist in the Module Tree or Existing Dependencies shown below. If a type is not yet available, define it locally.
```

- [ ] **Step 8: Commit**

```bash
git add internal/prompt/templates/
git commit -m "feat(prompt): add markdown templates for system-prompt scaffolding"
```

---

## Task 3: Embed the templates and add the renderer

**Files:**
- Create: `internal/prompt/templates.go`
- Test: `internal/prompt/templates_test.go`

- [ ] **Step 1: Write the failing test for the renderer**

Create `internal/prompt/templates_test.go`:

```go
package prompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderTemplate_Substitutes(t *testing.T) {
	out := renderTemplate("role.md", "rust", ".rs")
	assert.Contains(t, out, "rust code generator")
	assert.NotContains(t, out, "{{lang}}")
}

func TestRenderTemplate_OutputFormatExt(t *testing.T) {
	out := renderTemplate("output_format.md", "go", ".go")
	assert.Contains(t, out, ".go")
	assert.NotContains(t, out, "{{ext}}")
}

func TestRenderTemplate_MissingPanics(t *testing.T) {
	// A missing template is a programming error (embedded at compile time),
	// so it must fail loudly rather than silently returning "".
	require.Panics(t, func() { renderTemplate("does-not-exist.md", "go", ".go") })
}

func TestRenderTemplate_EndsWithSingleTrailingNewline(t *testing.T) {
	out := renderTemplate("solid.md", "go", ".go")
	assert.True(t, strings.HasSuffix(out, "\n"))
	assert.False(t, strings.HasSuffix(out, "\n\n"), "renderTemplate must not include the section separator")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/prompt/ -run TestRenderTemplate -v`
Expected: FAIL — `undefined: renderTemplate`.

- [ ] **Step 3: Implement `templates.go`**

Create `internal/prompt/templates.go`:

```go
package prompt

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed templates/*.md
var templateFS embed.FS

// renderTemplate loads an embedded markdown template by file name and
// substitutes {{lang}} and {{ext}} placeholders. The returned text is the
// section body with exactly one trailing newline (the section separator blank
// line is added by the assembler in BuildSystemPrompt). Panics if the template
// is missing — templates are embedded at compile time, so absence is a bug.
func renderTemplate(name, lang, ext string) string {
	data, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		panic(fmt.Sprintf("prompt: embedded template %q not found: %v", name, err))
	}
	body := strings.TrimRight(string(data), "\n") + "\n"
	r := strings.NewReplacer("{{lang}}", lang, "{{ext}}", ext)
	return r.Replace(body)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/prompt/ -run TestRenderTemplate -v`
Expected: PASS (4 subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/templates.go internal/prompt/templates_test.go
git commit -m "feat(prompt): embed markdown templates with renderTemplate helper"
```

---

## Task 4: Rewire `BuildSystemPrompt` to assemble from templates

**Files:**
- Modify: `internal/prompt/system.go`

> **Approach:** Keep `langExtensions`, the `lang`/`ext` setup, and the `project.Meta` (Style/Rules/Avoid) blocks exactly as they are. Replace ONLY the inline `b.WriteString(...)` scaffolding for Role, Output Format, Folder Structure, SOLID, and Output Requirements with `b.WriteString(renderTemplate(...))` followed by the section separator `"\n"`. Each section currently ends in `\n\n`; `renderTemplate` returns a body ending in one `\n`, so the assembler appends one more `\n` per section to reproduce the blank line.

- [ ] **Step 1: Replace the Role + Output Format sections**

In `internal/prompt/system.go`, replace:

```go
	b.WriteString("# Role\n\n")
	b.WriteString(fmt.Sprintf("You are a %s code generator following strict SOLID principles.\n\n", lang))

	b.WriteString("# Output Format\n\n")
	b.WriteString("Return code in fenced code blocks with path annotations:\n")
	b.WriteString(fmt.Sprintf("```\n// path: src/{context}/{resource}%s\n```\n\n", ext))
```

with:

```go
	b.WriteString(renderTemplate("role.md", lang, ext))
	b.WriteString("\n")

	b.WriteString(renderTemplate("output_format.md", lang, ext))
	b.WriteString("\n")
```

- [ ] **Step 2: Replace the Folder Structure switch**

Replace the entire `b.WriteString("# Folder Structure\n\n")` + `switch lang { case "rust": ... default: ... }` block with:

```go
	if lang == "rust" {
		b.WriteString(renderTemplate("folder_structure_rust.md", lang, ext))
	} else {
		b.WriteString(renderTemplate("folder_structure_default.md", lang, ext))
	}
	b.WriteString("\n")
```

- [ ] **Step 3: Replace the SOLID Principles block**

Replace the `# SOLID Principles` block (the heading line plus the five bullet `b.WriteString` calls) with:

```go
	b.WriteString(renderTemplate("solid.md", lang, ext))
	b.WriteString("\n")
```

- [ ] **Step 4: Replace the Output Requirements block**

Replace:

```go
	b.WriteString("# Output Requirements\n\n")
	b.WriteString("Generate both implementation files and unit tests.\n")
	if lang == "rust" {
		b.WriteString("Use `crate::` paths to reference types from other modules (e.g., `use crate::kernel::note_id::NoteId;`).\n")
		b.WriteString("Only reference types that exist in the Module Tree or Existing Dependencies shown below. If a type is not yet available, define it locally.\n")
	}
```

with:

```go
	b.WriteString(renderTemplate("output_requirements.md", lang, ext))
	if lang == "rust" {
		b.WriteString(renderTemplate("output_requirements_rust.md", lang, ext))
	}
```

> Note: the base `output_requirements.md` ends with one `\n` and there is no trailing blank line after this final section in the original output (the original ends with the last requirement line + `\n`). Do NOT append a separator `"\n"` after the output-requirements section. Confirm against `testdata/golden/rust.md` (file ends right after the last `define it locally.` line).

- [ ] **Step 5: Remove the now-unused `fmt` import if it is no longer used**

Check whether `fmt` is still referenced in `system.go`. After this refactor the `Style`/`Rules`/`Avoid` blocks use `b.WriteString` with `+` concatenation, not `fmt`. If `fmt` is unused, remove it from the import block. Run `go build ./internal/prompt/` to confirm no unused-import error.

- [ ] **Step 6: Run the golden test — output must be byte-identical**

Run: `go test ./internal/prompt/ -run TestBuildSystemPrompt_Golden -v`
Expected: PASS (3 subtests). If any subtest fails on a whitespace diff, fix the corresponding template file's trailing newlines until identical. Do NOT update the golden files.

- [ ] **Step 7: Run the full prompt package tests**

Run: `go test ./internal/prompt/ -v`
Expected: PASS — including the pre-existing `TestBuildSystemPrompt_Full`, `_Minimal`, `_RustLanguage`.

- [ ] **Step 8: Commit**

```bash
git add internal/prompt/system.go
git commit -m "refactor(prompt): assemble BuildSystemPrompt from embedded markdown templates"
```

---

## Task 5: Full build + test sweep

**Files:** none (verification only)

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: success (the embedded templates ship in the binary).

- [ ] **Step 2: Run the whole suite**

Run: `go test ./...`
Expected: all packages pass (currently 355 tests; this stage adds the golden + renderer tests and changes none of the others).

- [ ] **Step 3: Rebuild the binary**

Run: `make build`
Expected: `bin/crest-spec` produced.

- [ ] **Step 4: Commit any remaining changes (if the build produced none, skip)**

```bash
git status   # confirm clean working tree for this stage
```

---

## Self-Review (run before handing off)

1. **Spec coverage (Component 1):** role/output-format/folder-structure/SOLID/output-requirements all moved to `templates/*.md`; `Meta.Style/Rules/Avoid` stay in Go; `//go:embed` keeps the binary self-contained; behavior preserved via golden test. ✔
2. **No placeholders:** every step has exact file paths, code, commands, and expected output. ✔
3. **Type/name consistency:** `renderTemplate(name, lang, ext string) string` is defined in Task 3 and used identically in Task 4; `templateFS` embed var name matches. ✔
4. **Byte-identical guarantee:** golden files captured in Task 1 against the pre-refactor code; Task 4 Step 6 enforces equality. ✔

## Notes for the implementer

- This stage is intentionally **behavior-preserving** — if `BuildSystemPrompt` output changes at all, the refactor is wrong, not the golden file. Never run `UPDATE_GOLDEN=1` after Task 1.
- The trickiest part is whitespace in the templates. Derive template contents from the captured `testdata/golden/*.md`, not from memory.
- Stage 5 (promotion) will add `templates/learned/<lang>.md` and append it to the system prompt; leave a clear seam but do NOT build that here (YAGNI).
