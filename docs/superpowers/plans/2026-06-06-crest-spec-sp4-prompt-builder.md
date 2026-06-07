# SP4: Prompt Builder — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the prompt construction layer: system prompts, resource prompts, fix prompts, and runtime context injection. Pure string building with no I/O.

**Architecture:** Single package `internal/prompt/` with four files. Takes typed Go structs from SP3's `internal/cue/` and produces prompt strings.

**Tech Stack:** Go 1.26.4, `fmt.Sprintf`, `strings.Builder`, `encoding/json`

---

## File Structure

### New files

| File | Responsibility |
|---|---|
| `internal/prompt/system.go` | BuildSystemPrompt from project meta |
| `internal/prompt/system_test.go` | System prompt tests |
| `internal/prompt/resource.go` | BuildResourcePrompt per resource kind |
| `internal/prompt/resource_test.go` | Resource prompt tests |
| `internal/prompt/fix.go` | BuildFixPrompt for constraint loop retries |
| `internal/prompt/fix_test.go` | Fix prompt tests |
| `internal/prompt/context.go` | RuntimeContext struct + InjectRuntimeContext |
| `internal/prompt/context_test.go` | Runtime context injection tests |

---

## Task 1: System prompt builder

**Files:**
- Create: `internal/prompt/system.go`
- Create: `internal/prompt/system_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/prompt/system_test.go`:

```go
package prompt

import (
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/stretchr/testify/assert"
)

func TestBuildSystemPrompt_Full(t *testing.T) {
	project := &cuepkg.Project{
		Name: "test-project",
		Meta: cuepkg.Meta{
			Language: "go",
			Style:    "DDD",
			Rules:    []string{"Use interfaces for all dependencies", "Prefer composition over inheritance"},
			Avoid:    []string{"global variables", "init() functions"},
		},
	}

	prompt := BuildSystemPrompt(project)

	assert.Contains(t, prompt, "go code generator")
	assert.Contains(t, prompt, "SOLID")
	assert.Contains(t, prompt, "DDD")
	assert.Contains(t, prompt, "Use interfaces for all dependencies")
	assert.Contains(t, prompt, "Prefer composition over inheritance")
	assert.Contains(t, prompt, "global variables")
	assert.Contains(t, prompt, "init() functions")
	assert.Contains(t, prompt, ".go")
	assert.Contains(t, prompt, "implementation files and unit tests")
}

func TestBuildSystemPrompt_Minimal(t *testing.T) {
	project := &cuepkg.Project{
		Name: "minimal",
		Meta: cuepkg.Meta{},
	}

	prompt := BuildSystemPrompt(project)

	assert.Contains(t, prompt, "code generator")
	assert.Contains(t, prompt, "SOLID")
	assert.NotContains(t, prompt, "# Code Style")
	assert.NotContains(t, prompt, "# Rules")
	assert.NotContains(t, prompt, "# Avoid")
}

func TestBuildSystemPrompt_RustLanguage(t *testing.T) {
	project := &cuepkg.Project{
		Name: "rust-project",
		Meta: cuepkg.Meta{
			Language: "rust",
		},
	}

	prompt := BuildSystemPrompt(project)

	assert.Contains(t, prompt, "rust code generator")
	assert.Contains(t, prompt, ".rs")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/prompt/ -v
```

Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement system.go**

Create `internal/prompt/system.go`:

```go
package prompt

import (
	"fmt"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

var langExtensions = map[string]string{
	"go":         ".go",
	"rust":       ".rs",
	"python":     ".py",
	"typescript": ".ts",
	"javascript": ".js",
	"java":       ".java",
	"csharp":     ".cs",
	"c":          ".c",
	"cpp":        ".cpp",
}

func BuildSystemPrompt(project *cuepkg.Project) string {
	var b strings.Builder

	lang := project.Meta.Language
	if lang == "" {
		lang = "source"
	}
	ext := langExtensions[lang]

	b.WriteString("# Role\n\n")
	b.WriteString(fmt.Sprintf("You are a %s code generator following strict SOLID principles.\n\n", lang))

	b.WriteString("# Output Format\n\n")
	b.WriteString("Return code in fenced code blocks with path annotations:\n")
	b.WriteString(fmt.Sprintf("```\n// path: src/{ContextName}/{ResourceName}%s\n```\n\n", ext))

	b.WriteString("# Folder Structure\n\n")
	b.WriteString("All code goes in src/{ContextName}/{ResourceName}/ — grouped by resource, not by architectural layer.\n\n")

	b.WriteString("# SOLID Principles\n\n")
	b.WriteString("- **Single Responsibility**: Each type has one reason to change.\n")
	b.WriteString("- **Open/Closed**: Open for extension, closed for modification.\n")
	b.WriteString("- **Liskov Substitution**: Subtypes must be substitutable for their base types.\n")
	b.WriteString("- **Interface Segregation**: Depend on narrow interfaces, not broad ones.\n")
	b.WriteString("- **Dependency Inversion**: Depend on abstractions, not concretions. Accept dependencies via constructor.\n\n")

	if project.Meta.Style != "" {
		b.WriteString("# Code Style\n\n")
		b.WriteString(project.Meta.Style + "\n\n")
	}

	if len(project.Meta.Rules) > 0 {
		b.WriteString("# Rules\n\n")
		for _, rule := range project.Meta.Rules {
			b.WriteString("- " + rule + "\n")
		}
		b.WriteString("\n")
	}

	if len(project.Meta.Avoid) > 0 {
		b.WriteString("# Avoid\n\n")
		for _, avoid := range project.Meta.Avoid {
			b.WriteString("- " + avoid + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("# Output Requirements\n\n")
	b.WriteString("Generate both implementation files and unit tests.\n")

	return b.String()
}
```

- [ ] **Step 4: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/prompt/ -v
```

Expected: All 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/system.go internal/prompt/system_test.go
git commit -m "feat(sp4): implement system prompt builder"
```

---

## Task 2: Resource prompt builder

**Files:**
- Create: `internal/prompt/resource.go`
- Create: `internal/prompt/resource_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/prompt/resource_test.go`:

```go
package prompt

import (
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/stretchr/testify/assert"
)

func makeTestRegistry() *cuepkg.Registry {
	portDecl := cuepkg.Port{
		Contract: map[string]string{"write": "([]float64) -> error"},
	}
	aggDecl := cuepkg.Aggregate{
		Root:    true,
		Purpose: "Manages a single voice",
		State:   map[string]string{"frequency": "float64", "amplitude": "float64"},
		Commands: map[string]map[string]string{
			"NoteOn": {"frequency": "float64", "velocity": "float64"},
		},
		Events: map[string]map[string]string{
			"VoiceStarted": {"frequency": "float64"},
		},
		Invariants: []string{"frequency > 0"},
		Implements: "port.Synth.AudioOutput",
	}
	svcDecl := cuepkg.DomainService{
		Purpose: "Combines voice outputs",
		Uses:    []string{"aggregate.Synth.Voice"},
	}
	repoDecl := cuepkg.Repository{
		Of:       "aggregate.Synth.Voice",
		Contract: map[string]string{"acquire": "() -> Voice"},
	}
	adapterDecl := cuepkg.Adapter{
		Implements: "port.Synth.AudioOutput",
		Layer:      "infrastructure",
	}
	assetKindDecl := cuepkg.AssetKind{
		Description: "Go source file",
		FilePattern: "{{snakeCase .Name}}.go",
		Prompts:     []string{"Follow Go conventions"},
	}
	assetDecl := cuepkg.Asset{
		Kind:        "source_file",
		Description: "Voice implementation",
		Prompts:     []string{"Include comprehensive tests"},
		Targets:     []string{"aggregate.Synth.Voice"},
	}

	return &cuepkg.Registry{
		Project: &cuepkg.Project{Name: "test"},
		Resources: map[string]cuepkg.Resource{
			"aggregate.Synth.Voice": {
				ID: "aggregate.Synth.Voice", Kind: "aggregate", ContextName: "Synth",
				Declaration: aggDecl,
				Dependencies: []cuepkg.Edge{{TargetID: "port.Synth.AudioOutput", Kind: "implements"}},
			},
			"port.Synth.AudioOutput": {
				ID: "port.Synth.AudioOutput", Kind: "port", ContextName: "Synth",
				Declaration: portDecl,
			},
			"domainService.Synth.Mixer": {
				ID: "domainService.Synth.Mixer", Kind: "domainService", ContextName: "Synth",
				Declaration: svcDecl,
				Dependencies: []cuepkg.Edge{{TargetID: "aggregate.Synth.Voice", Kind: "uses"}},
			},
			"repository.Synth.VoicePool": {
				ID: "repository.Synth.VoicePool", Kind: "repository", ContextName: "Synth",
				Declaration: repoDecl,
				Dependencies: []cuepkg.Edge{{TargetID: "aggregate.Synth.Voice", Kind: "of"}},
			},
			"adapter.CoreAudioAdapter": {
				ID: "adapter.CoreAudioAdapter", Kind: "adapter",
				Declaration: adapterDecl,
				Dependencies: []cuepkg.Edge{{TargetID: "port.Synth.AudioOutput", Kind: "implements"}},
			},
			"assetKind.source_file": {
				ID: "assetKind.source_file", Kind: "assetKind",
				Declaration: assetKindDecl,
			},
			"asset.Synth.VoiceImpl": {
				ID: "asset.Synth.VoiceImpl", Kind: "asset", ContextName: "Synth",
				Declaration: assetDecl,
				Dependencies: []cuepkg.Edge{
					{TargetID: "assetKind.source_file", Kind: "uses"},
					{TargetID: "aggregate.Synth.Voice", Kind: "targets"},
				},
			},
		},
	}
}

func TestBuildResourcePrompt_Aggregate(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["aggregate.Synth.Voice"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "aggregate")
	assert.Contains(t, prompt, "Voice")
	assert.Contains(t, prompt, "aggregate.Synth.Voice")
	assert.Contains(t, prompt, "Synth")
	assert.Contains(t, prompt, "Manages a single voice")
	assert.Contains(t, prompt, "NoteOn")
	assert.Contains(t, prompt, "VoiceStarted")
	assert.Contains(t, prompt, "frequency > 0")
	assert.Contains(t, prompt, "port.Synth.AudioOutput")
	assert.Contains(t, prompt, "([]float64) -> error")
}

func TestBuildResourcePrompt_DomainService(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["domainService.Synth.Mixer"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "domainService")
	assert.Contains(t, prompt, "Mixer")
	assert.Contains(t, prompt, "Combines voice outputs")
	assert.Contains(t, prompt, "aggregate.Synth.Voice")
	assert.Contains(t, prompt, "Manages a single voice")
}

func TestBuildResourcePrompt_Adapter(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["adapter.CoreAudioAdapter"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "adapter")
	assert.Contains(t, prompt, "CoreAudioAdapter")
	assert.Contains(t, prompt, "port.Synth.AudioOutput")
	assert.Contains(t, prompt, "([]float64) -> error")
}

func TestBuildResourcePrompt_Repository(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["repository.Synth.VoicePool"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "repository")
	assert.Contains(t, prompt, "VoicePool")
	assert.Contains(t, prompt, "aggregate.Synth.Voice")
}

func TestBuildResourcePrompt_Asset(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["asset.Synth.VoiceImpl"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "Asset")
	assert.Contains(t, prompt, "VoiceImpl")
	assert.Contains(t, prompt, "source_file")
	assert.Contains(t, prompt, "Go source file")
	assert.Contains(t, prompt, "Follow Go conventions")
	assert.Contains(t, prompt, "Voice implementation")
	assert.Contains(t, prompt, "Include comprehensive tests")
	assert.Contains(t, prompt, "aggregate.Synth.Voice")
}

func TestBuildResourcePrompt_NoDependencies(t *testing.T) {
	reg := makeTestRegistry()
	r := reg.Resources["port.Synth.AudioOutput"]

	prompt := BuildResourcePrompt(r, reg)

	assert.Contains(t, prompt, "port")
	assert.Contains(t, prompt, "AudioOutput")
	assert.NotContains(t, prompt, "## Dependencies")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/prompt/ -run "TestBuildResourcePrompt" -v
```

Expected: FAIL.

- [ ] **Step 3: Implement resource.go**

Create `internal/prompt/resource.go`:

```go
package prompt

import (
	"encoding/json"
	"fmt"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

func BuildResourcePrompt(resource cuepkg.Resource, registry *cuepkg.Registry) string {
	if resource.Kind == "asset" {
		return buildAssetPrompt(resource, registry)
	}
	return buildDomainPrompt(resource, registry)
}

func buildDomainPrompt(resource cuepkg.Resource, registry *cuepkg.Registry) string {
	var b strings.Builder

	name := extractName(resource.ID)

	b.WriteString(fmt.Sprintf("# Resource: %s — %s\n\n", resource.Kind, name))
	b.WriteString(fmt.Sprintf("ID: %s\n", resource.ID))
	if resource.ContextName != "" {
		b.WriteString(fmt.Sprintf("Context: %s\n", resource.ContextName))
	}
	b.WriteString("\n")

	b.WriteString("## Declaration\n\n")
	declJSON, _ := json.MarshalIndent(resource.Declaration, "", "  ")
	b.WriteString("```json\n")
	b.WriteString(string(declJSON))
	b.WriteString("\n```\n\n")

	if agg, ok := resource.Declaration.(cuepkg.Aggregate); ok {
		if len(agg.Commands) > 0 {
			b.WriteString("## Commands\n\n")
			for cmdName, fields := range agg.Commands {
				b.WriteString(fmt.Sprintf("### %s\n", cmdName))
				for field, typ := range fields {
					b.WriteString(fmt.Sprintf("- %s: %s\n", field, typ))
				}
				b.WriteString("\n")
			}
		}

		if len(agg.Events) > 0 {
			b.WriteString("## Events\n\n")
			for evtName, fields := range agg.Events {
				b.WriteString(fmt.Sprintf("### %s\n", evtName))
				for field, typ := range fields {
					b.WriteString(fmt.Sprintf("- %s: %s\n", field, typ))
				}
				b.WriteString("\n")
			}
		}

		if len(agg.Invariants) > 0 {
			b.WriteString("## Invariants\n\n")
			for _, inv := range agg.Invariants {
				b.WriteString("- " + inv + "\n")
			}
			b.WriteString("\n")
		}
	}

	// Port contract for resources that implement a port
	for _, dep := range resource.Dependencies {
		if dep.Kind == "implements" {
			if target, ok := registry.Resources[dep.TargetID]; ok {
				if port, ok := target.Declaration.(cuepkg.Port); ok {
					b.WriteString("## Port Contract\n\n")
					b.WriteString(fmt.Sprintf("Implements: %s\n\n", dep.TargetID))
					for method, sig := range port.Contract {
						b.WriteString(fmt.Sprintf("- %s: %s\n", method, sig))
					}
					b.WriteString("\n")
				}
			}
		}
	}

	// Dependencies
	deps := nonImplementsDeps(resource, registry)
	if len(deps) > 0 {
		b.WriteString("## Dependencies\n\n")
		for _, dep := range deps {
			target, ok := registry.Resources[dep.TargetID]
			if !ok {
				continue
			}
			depJSON, _ := json.MarshalIndent(target.Declaration, "", "  ")
			b.WriteString(fmt.Sprintf("### %s (%s)\n\n", dep.TargetID, dep.Kind))
			b.WriteString("```json\n")
			b.WriteString(string(depJSON))
			b.WriteString("\n```\n\n")
		}
	}

	return b.String()
}

func buildAssetPrompt(resource cuepkg.Resource, registry *cuepkg.Registry) string {
	var b strings.Builder

	name := extractName(resource.ID)
	asset, _ := resource.Declaration.(cuepkg.Asset)

	b.WriteString(fmt.Sprintf("# Asset: %s\n\n", name))
	b.WriteString(fmt.Sprintf("ID: %s\n", resource.ID))
	b.WriteString(fmt.Sprintf("Kind: %s\n\n", asset.Kind))

	// Asset kind info
	for _, dep := range resource.Dependencies {
		if dep.Kind == "uses" {
			if target, ok := registry.Resources[dep.TargetID]; ok {
				if ak, ok := target.Declaration.(cuepkg.AssetKind); ok {
					b.WriteString("## Asset Kind\n\n")
					b.WriteString(fmt.Sprintf("Description: %s\n", ak.Description))
					if ak.FilePattern != "" {
						b.WriteString(fmt.Sprintf("File pattern: %s\n", ak.FilePattern))
					}
					if len(ak.Prompts) > 0 {
						b.WriteString("\n")
						for _, p := range ak.Prompts {
							b.WriteString("- " + p + "\n")
						}
					}
					b.WriteString("\n")
				}
			}
		}
	}

	if asset.Description != "" {
		b.WriteString("## Description\n\n")
		b.WriteString(asset.Description + "\n\n")
	}

	if len(asset.Prompts) > 0 {
		b.WriteString("## Prompts\n\n")
		for _, p := range asset.Prompts {
			b.WriteString("- " + p + "\n")
		}
		b.WriteString("\n")
	}

	// Targets
	var targets []cuepkg.Edge
	for _, dep := range resource.Dependencies {
		if dep.Kind == "targets" {
			targets = append(targets, dep)
		}
	}
	if len(targets) > 0 {
		b.WriteString("## Targets\n\n")
		for _, dep := range targets {
			target, ok := registry.Resources[dep.TargetID]
			if !ok {
				continue
			}
			depJSON, _ := json.MarshalIndent(target.Declaration, "", "  ")
			b.WriteString(fmt.Sprintf("### %s\n\n", dep.TargetID))
			b.WriteString("```json\n")
			b.WriteString(string(depJSON))
			b.WriteString("\n```\n\n")
		}
	}

	return b.String()
}

func extractName(id string) string {
	parts := strings.Split(id, ".")
	return parts[len(parts)-1]
}

func nonImplementsDeps(resource cuepkg.Resource, registry *cuepkg.Registry) []cuepkg.Edge {
	var deps []cuepkg.Edge
	for _, dep := range resource.Dependencies {
		if dep.Kind != "implements" {
			deps = append(deps, dep)
		}
	}
	return deps
}
```

- [ ] **Step 4: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/prompt/ -v
```

Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/resource.go internal/prompt/resource_test.go
git commit -m "feat(sp4): implement resource prompt builder"
```

---

## Task 3: Fix prompt and runtime context injection

**Files:**
- Create: `internal/prompt/fix.go`
- Create: `internal/prompt/fix_test.go`
- Create: `internal/prompt/context.go`
- Create: `internal/prompt/context_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/prompt/fix_test.go`:

```go
package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildFixPrompt(t *testing.T) {
	prompt := BuildFixPrompt(
		"original requirements here",
		"previous generated code",
		"compilation error on line 42",
	)

	assert.Contains(t, prompt, "original requirements here")
	assert.Contains(t, prompt, "previous generated code")
	assert.Contains(t, prompt, "compilation error on line 42")
	assert.Contains(t, prompt, "Fix")
}

func TestBuildFixPrompt_EmptyPrevious(t *testing.T) {
	prompt := BuildFixPrompt("requirements", "", "parse error")

	assert.Contains(t, prompt, "requirements")
	assert.Contains(t, prompt, "parse error")
}
```

Create `internal/prompt/context_test.go`:

```go
package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInjectRuntimeContext_AllFields(t *testing.T) {
	base := "base prompt content"
	ctx := RuntimeContext{
		ModuleTree:      "src/\n  Synth/\n    Voice.go",
		DependencyFiles: map[string]string{"aggregate.Synth.Voice": "package voice\n..."},
		AgentNotes:      map[string]string{"aggregate.Synth.Voice": "Used builder pattern for state"},
		WaveErrors:      "error[E0425]: cannot find value `Oscillator`",
		UserGuidance:    "Use try_send for audio thread",
	}

	result := InjectRuntimeContext(base, ctx)

	assert.Contains(t, result, "base prompt content")
	assert.Contains(t, result, "Module Tree")
	assert.Contains(t, result, "src/\n  Synth/\n    Voice.go")
	assert.Contains(t, result, "Existing Dependencies")
	assert.Contains(t, result, "package voice")
	assert.Contains(t, result, "Notes from Dependencies")
	assert.Contains(t, result, "Used builder pattern for state")
	assert.Contains(t, result, "Previous Errors")
	assert.Contains(t, result, "cannot find value")
	assert.Contains(t, result, "User Guidance")
	assert.Contains(t, result, "try_send")
}

func TestInjectRuntimeContext_Empty(t *testing.T) {
	base := "base prompt"
	ctx := RuntimeContext{}

	result := InjectRuntimeContext(base, ctx)

	assert.Equal(t, base, result)
}

func TestInjectRuntimeContext_PartialFields(t *testing.T) {
	base := "base prompt"
	ctx := RuntimeContext{
		ModuleTree: "src/\n  Synth/",
	}

	result := InjectRuntimeContext(base, ctx)

	assert.Contains(t, result, "Module Tree")
	assert.NotContains(t, result, "Existing Dependencies")
	assert.NotContains(t, result, "Notes from Dependencies")
	assert.NotContains(t, result, "Previous Errors")
	assert.NotContains(t, result, "User Guidance")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/prompt/ -run "TestBuildFixPrompt|TestInjectRuntimeContext" -v
```

Expected: FAIL.

- [ ] **Step 3: Implement fix.go**

Create `internal/prompt/fix.go`:

```go
package prompt

import (
	"strings"
)

func BuildFixPrompt(resourcePrompt string, previousOutput string, errorMsg string) string {
	var b strings.Builder

	b.WriteString("# Fix Required\n\n")
	b.WriteString("The previous generation had errors. Fix them while keeping the same requirements.\n\n")

	b.WriteString("## Original Requirements\n\n")
	b.WriteString(resourcePrompt)
	b.WriteString("\n\n")

	if previousOutput != "" {
		b.WriteString("## Previous Output\n\n")
		b.WriteString(previousOutput)
		b.WriteString("\n\n")
	}

	b.WriteString("## Error to Fix\n\n")
	b.WriteString(errorMsg)
	b.WriteString("\n\n")

	b.WriteString("Generate corrected code that addresses the error above.\n")

	return b.String()
}
```

- [ ] **Step 4: Implement context.go**

Create `internal/prompt/context.go`:

```go
package prompt

import (
	"fmt"
	"sort"
	"strings"
)

type RuntimeContext struct {
	ModuleTree      string
	DependencyFiles map[string]string
	AgentNotes      map[string]string
	WaveErrors      string
	UserGuidance    string
}

func InjectRuntimeContext(prompt string, ctx RuntimeContext) string {
	var sections []string

	if ctx.ModuleTree != "" {
		sections = append(sections, fmt.Sprintf("## Module Tree\n\n%s", ctx.ModuleTree))
	}

	if len(ctx.DependencyFiles) > 0 {
		var b strings.Builder
		b.WriteString("## Existing Dependencies\n\n")
		keys := sortedKeys(ctx.DependencyFiles)
		for _, id := range keys {
			content := ctx.DependencyFiles[id]
			b.WriteString(fmt.Sprintf("### %s\n\n```\n%s\n```\n\n", id, content))
		}
		sections = append(sections, b.String())
	}

	if len(ctx.AgentNotes) > 0 {
		var b strings.Builder
		b.WriteString("## Notes from Dependencies\n\n")
		keys := sortedKeys(ctx.AgentNotes)
		for _, id := range keys {
			notes := ctx.AgentNotes[id]
			b.WriteString(fmt.Sprintf("### %s\n\n%s\n\n", id, notes))
		}
		sections = append(sections, b.String())
	}

	if ctx.WaveErrors != "" {
		sections = append(sections, fmt.Sprintf("## Previous Errors\n\nThe previous generation caused build errors. Fix these errors in your output.\n\n%s", ctx.WaveErrors))
	}

	if ctx.UserGuidance != "" {
		sections = append(sections, fmt.Sprintf("## User Guidance\n\n%s", ctx.UserGuidance))
	}

	if len(sections) == 0 {
		return prompt
	}

	return prompt + "\n\n" + strings.Join(sections, "\n\n")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
```

- [ ] **Step 5: Run all tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/prompt/ -v
```

Expected: All tests pass.

- [ ] **Step 6: Run full suite**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./...
```

Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/prompt/fix.go internal/prompt/fix_test.go internal/prompt/context.go internal/prompt/context_test.go
git commit -m "feat(sp4): implement fix prompt and runtime context injection"
```

---

## Summary

| Task | What it does |
|---|---|
| 1 | System prompt builder (from project meta) |
| 2 | Resource prompt builder (domain + asset, with dependencies) |
| 3 | Fix prompt + runtime context injection |
