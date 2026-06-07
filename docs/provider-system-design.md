# Provider System Design

How crest-spec can support language/framework-specific resource types through
a plugin-like provider model, analogous to Terraform providers.

---

## Problem

Today, crest-spec's resource vocabulary is hardcoded: aggregates, value objects,
domain services, repositories, ports, adapters. This is DDD-specific. A React
app needs Components, Hooks, Stores. A Go HTTP service needs Handlers,
Middleware, Repositories. A Python ML pipeline needs Models, Transformers,
Pipelines.

The core engine (plan → apply → validate → commit) is generic. The resource
types and their prompt strategies are the domain-specific layer. Separating
these is what makes Terraform's provider model powerful — the core never changes,
providers handle the specifics.

---

## Design

### Provider = CUE Package

A provider is a CUE package that defines:

1. **Resource type definitions** — what types of resources exist and their schema
2. **Prompt templates** — how to build prompts for each resource type
3. **Validation commands** — how to verify generated output
4. **File patterns** — where generated files should live
5. **Folder conventions** — directory structure rules

```cue
package rust_ddd

import "crest-spec.dev/core"

// Resource type definitions
#Aggregate: core.#Resource & {
    kind: "aggregate"
    root: bool | *true
    state: [string]: string
    commands: [string]: [string]: string
    events: [string]: [string]: string
    invariants: [...string]
}

#Repository: core.#Resource & {
    kind: "repository"
    of: string
    contract: [string]: string
}

// Provider metadata
provider: {
    name: "rust-ddd"
    language: "rust"
    framework: "custom"
    filePattern: "src/{context}/{kind}/{name}.rs"
    
    validations: [{
        kind: "compiles"
        command: ["cargo", "check"]
    }, {
        kind: "test"
        command: ["cargo", "test"]
    }]
    
    promptTemplates: {
        aggregate: """
            Generate a Rust aggregate root for {name}.
            Follow the command-event pattern.
            State: {state}
            Commands: {commands}
            Events: {events}
            """
        repository: """
            Generate a Rust repository trait and implementation for {name}.
            Aggregate: {of}
            Contract: {contract}
            """
    }
}
```

### Usage in Project Specs

```cue
package spec

import "crest-spec.dev/providers/rust-ddd"

project: {
    name: "my-synthesizer"
    providers: ["rust-ddd"]
    
    contexts: {
        Synth: {
            aggregates: {
                Voice: rust_ddd.#Aggregate & {
                    state: frequency: "f64"
                    commands: NoteOn: frequency: "f64"
                    events: NoteStarted: frequency: "f64"
                }
            }
        }
    }
}
```

### Core Changes Required

The core engine needs these changes to support providers:

**1. Generic resource type system.**
Currently, `internal/cue/registry.go` hardcodes resource kinds (aggregate,
valueObject, etc.). The registry should instead discover resource kinds from
the loaded CUE:

```go
type ResourceKind struct {
    Name           string
    PromptTemplate string
    FilePattern    string
    Validations    []Validation
}

type Provider struct {
    Name      string
    Language  string
    Kinds     map[string]ResourceKind
}
```

**2. Provider-aware prompt builder.**
`internal/prompt/builder.go` currently has hardcoded prompt logic per resource
kind. It should instead look up the prompt template from the provider and
interpolate resource-specific values.

**3. Provider resolution.**
When loading a CUE spec, the loader needs to resolve provider imports. CUE's
native module system handles this, but we need to:
- Support a local provider directory (e.g., `spec/providers/`)
- Support a registry of community providers (future)

**4. Provider-scoped validation.**
Each provider defines its validation commands (cargo check, go build, npm test).
These should be used as the default validations for resources of that provider's
types, with per-resource overrides taking precedence.

---

## Implementation Phases

### Phase 1: Local Provider Support

- Define the `#Provider` CUE schema
- Support `spec/providers/` directory for local provider packages
- Registry discovers resource kinds from provider definitions
- Prompt builder uses provider templates
- No remote registry

### Phase 2: Built-in Providers

Ship common providers as embedded CUE packages:

| Provider | Language | Resource Types |
|----------|----------|----------------|
| `rust-ddd` | Rust | Aggregate, ValueObject, Entity, Repository, DomainService, Port, Adapter |
| `go-service` | Go | Handler, Middleware, Repository, Service, Model |
| `react-spa` | TypeScript | Component, Hook, Store, Page, Layout, Service |
| `python-ml` | Python | Model, Transformer, Pipeline, Dataset, Evaluator |
| `generic` | Any | Module, Config, Script, Test |

### Phase 3: Provider Registry

- Remote registry for community providers
- Version pinning and lockfiles
- `crest-spec provider add <name>` CLI command
- Dependency resolution between providers

---

## Migration Path

The current DDD vocabulary (aggregates, value objects, etc.) becomes the
`ddd` provider. Existing CUE specs continue to work unchanged — the default
provider is `ddd` when no provider is specified.

```cue
// Before (implicit DDD provider)
project: contexts: Synth: aggregates: Voice: { ... }

// After (explicit provider, but backward compatible)
project: providers: ["ddd"]
project: contexts: Synth: aggregates: Voice: { ... }
```

---

## Open Questions

1. **Should providers define the dependency graph rules?** Currently, dependency
   edges are inferred from field references (e.g., `of: "Voice"` creates an
   edge to the Voice aggregate). Should providers define custom dependency
   inference rules?

2. **Multi-provider projects.** Can a single project use multiple providers?
   E.g., a full-stack app might use `go-service` for the backend and `react-spa`
   for the frontend. This probably requires context-level provider assignment.

3. **Provider-specific state.** Should providers be able to store custom state
   in SQLite? E.g., a Rust provider might want to track which crates are used.

4. **Prompt template language.** Simple string interpolation (`{name}`) or
   something more powerful (Go templates, CUE templating)? CUE string
   interpolation is the natural choice but may not be flexible enough.

---

*Written 2026-06-07 as part of the Terraform-for-code vision implementation.*
