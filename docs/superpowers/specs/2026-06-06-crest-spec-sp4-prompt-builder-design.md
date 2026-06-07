# SP4: Prompt Builder — Design Spec

## Goal

Build the prompt construction layer: assemble system prompts and resource prompts from the typed Go structs produced by SP3. Pure string building — no LLM calls, no I/O, no store access. Runtime context injection (module tree scanning, dependency file reading, agent notes) is wired in SP5.

## Architecture

```
Registry (from SP3)
    │ prompt.BuildSystemPrompt(project)
    v
System Prompt (string)

Registry + Resource
    │ prompt.BuildResourcePrompt(resource, registry)
    v
Resource Prompt (string)

Resource Prompt + Previous Output + Error
    │ prompt.BuildFixPrompt(resourcePrompt, previousOutput, error)
    v
Fix Prompt (string)
```

Single package: `internal/prompt/`

---

## 1. Package: `internal/prompt/`

### 1.1 System Prompt (`system.go`)

```go
func BuildSystemPrompt(project *cue.Project) string
```

Builds a system prompt from project-level meta. Output structure:

```
# Role

You are a {meta.language} code generator following strict SOLID principles.

# Output Format

Return code in fenced code blocks with path annotations:
```{lang}
// path: src/{ContextName}/{ResourceName}.{ext}
```

# Folder Structure

All code goes in src/{ContextName}/{ResourceName}/ — grouped by resource, not by architectural layer.

# SOLID Principles

[Fixed text block: DI, SRP, DIP, ISP, OCP rules]

# Code Style

{meta.style}

# Rules

{meta.rules joined with newlines}

# Avoid

{meta.avoid joined with newlines}

# Output Requirements

Generate both implementation files and unit tests.
```

Language extension mapping: `go` → `.go`, `rust` → `.rs`, `python` → `.py`, `typescript` → `.ts`, etc. Default to empty extension if unknown.

### 1.2 Resource Prompt (`resource.go`)

```go
func BuildResourcePrompt(resource cue.Resource, registry *cue.Registry) string
```

Builds a per-resource prompt. Two shapes:

**Domain resources** (aggregate, entity, valueObject, domainService, applicationService, repository, port, adapter):

```
# Resource: {Kind} — {Name}

ID: {resource.ID}
Context: {resource.ContextName}

## Declaration

```json
{declaration as JSON}
```

## Commands
{if aggregate with commands, list each with payload fields}

## Events
{if aggregate with events, list each with payload fields}

## Invariants
{if aggregate with invariants, list each}

## Port Contract
{if resource implements a port, include the port's contract}

## Dependencies

{for each dependency edge, include the target resource's declaration}
```

**Assets**:

```
# Asset: {Name}

ID: {resource.ID}
Kind: {asset.Kind}

## Asset Kind

{assetKind description, filePattern, prompts}

## Description

{asset.Description}

## Prompts

{asset.Prompts joined with newlines}

## Targets

{for each target, include the target resource's declaration}
```

### 1.3 Fix Prompt (`fix.go`)

```go
func BuildFixPrompt(resourcePrompt string, previousOutput string, errorMsg string) string
```

Builds a retry prompt:

```
# Fix Required

The previous generation had errors. Fix them while keeping the same requirements.

## Original Requirements

{resourcePrompt}

## Previous Output

{previousOutput}

## Error to Fix

{errorMsg}

Generate corrected code that addresses the error above.
```

### 1.4 Runtime Context (`context.go`)

```go
type RuntimeContext struct {
    ModuleTree     string            // directory tree listing
    DependencyFiles map[string]string // resourceID → file contents
    AgentNotes     map[string]string // resourceID → notes
    WaveErrors     string            // error output from wave verification
    UserGuidance   string            // from spec/resolve
}

func InjectRuntimeContext(prompt string, ctx RuntimeContext) string
```

Appends runtime context sections to a prompt. Each section only appears if the corresponding field is non-empty. This function is pure — it doesn't read files or query the store. SP5 is responsible for populating the `RuntimeContext` struct.

```
{prompt}

## Module Tree

{ctx.ModuleTree}

## Existing Dependencies

{for each entry in DependencyFiles: ### {resourceID}\n```\n{content}\n```}

## Notes from Dependencies

{for each entry in AgentNotes: ### {resourceID}\n{notes}}

## Previous Errors

{ctx.WaveErrors}

## User Guidance

{ctx.UserGuidance}
```

---

## 2. Testing Strategy

### 2.1 System Prompt Tests (`system_test.go`)
- Verify role definition includes language
- Verify style is included when set
- Verify rules are listed
- Verify avoid list is included
- Verify empty meta produces a minimal valid prompt

### 2.2 Resource Prompt Tests (`resource_test.go`)
- Aggregate prompt includes declaration, commands, events, invariants
- Adapter prompt includes implements target's contract
- Domain service prompt includes uses dependencies
- Repository prompt includes the target aggregate declaration
- Asset prompt includes asset kind info and target declarations
- Resource with no dependencies produces a prompt without dependency section

### 2.3 Fix Prompt Tests (`fix_test.go`)
- Verify all three sections present
- Verify empty previous output still produces valid prompt

### 2.4 Runtime Context Tests (`context_test.go`)
- Verify only non-empty sections are injected
- Verify all sections when all fields populated
- Verify empty RuntimeContext returns prompt unchanged

---

## 3. What SP4 Does NOT Include

- **File I/O** — no reading module trees or dependency files (SP5 wires this)
- **Store access** — no querying agent notes or generated files (SP5)
- **LLM calls** — none in SP4
- **Parsing LLM output** — SP5 (code block extraction)
- **MCP tool wiring** — SP5 (spec/context handler)
