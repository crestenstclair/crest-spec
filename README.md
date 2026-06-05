# crest-spec

A declarative DSL for software architecture. You describe the system you want using Domain-Driven Design vocabulary — bounded contexts, aggregates, commands, events, ports — and crest-spec generates the code, enforces invariants, and keeps settled work settled.

The architect stays in control of structure. The LLM fills in bodies against typed contracts. Nothing gets generated without a declaration.

## Install

```bash
bun install
```

Requires [Bun](https://bun.sh) and an `ANTHROPIC_API_KEY` environment variable for code generation.

## Quick start

**1. Initialize a project**

```bash
bunx crest-spec init
```

Creates a `crest-spec.ts` spec file and a `crest-spec.db` SQLite state database.

**2. Write your spec**

```ts
import { project, command, event, invariant, layer } from "krusty-spec";

const app = project("my-app", {
  layers: ["domain", "application"],
  rules: [
    layer("domain").dependsOn([]),
    layer("application").dependsOn(["domain"]),
  ],
  meta: {
    style: "functional TypeScript",
    avoid: ["any", "classes with mutable state"],
    typeCheckCommand: ["bun", "x", "tsc", "--noEmit"],
    testCommand: ["bun", "test"],
  },
});

const catalog = app.context("Catalog", {
  purpose: "product catalog management",
});

catalog.aggregate("Product", {
  root: true,
  state: { id: "ProductId", name: "string", price: "number" },
  invariants: ["price must be positive", "name must not be empty"],
  commands: [
    command("CreateProduct", { name: "string", price: "number" }),
    command("UpdatePrice", { newPrice: "number" }),
  ],
  events: [
    event("ProductCreated", { id: "ProductId", name: "string", price: "number" }),
    event("PriceUpdated", { id: "ProductId", oldPrice: "number", newPrice: "number" }),
  ],
});

catalog.valueObject("ProductId", { from: "string" });
```

**3. Preview changes**

```bash
bunx crest-spec plan
```

Shows what will be created, modified, or destroyed — without touching disk or calling the LLM.

**4. Generate**

```bash
bunx crest-spec apply
```

Generates types, command handlers, event types, aggregate scaffolds, and LLM-filled bodies. All writes are transactional via SQLite.

## CLI reference

```
crest-spec init                         Scaffold a new spec and state database
crest-spec plan                         Diff spec vs state, show what would change
crest-spec apply                        Execute the plan (transactional)
crest-spec validate                     Check invariants without generating
crest-spec graph                        Resource dependency graph (DOT format)
crest-spec contextmap                   Context map (DOT format)
crest-spec log                          List past applies
crest-spec history <resource>           Full history of one resource
crest-spec state list                   List resources in state
crest-spec state rm <id>                Remove a resource from state
crest-spec unlock                       Clear a stale coordination lock
crest-spec vacuum --before DATE         Prune old history
crest-spec sql                          Open sqlite3 shell against crest-spec.db
```

### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--spec <file>` | Spec file path | `crest-spec.ts` |
| `--model <id>` | Anthropic model ID | `claude-sonnet-4-6` |
| `--target <resource>` | Apply a single resource and its dependents | all |
| `--force` | Force re-render (ignores stored hash) | off |
| `--retries <n>` | Max LLM retry attempts | 3 |
| `--concurrency <n>` | Parallel generation workers | 2 |
| `--incremental` | Verify builds between dependency waves | off |

## DSL resources

| Resource | Description |
|----------|-------------|
| `project()` | Top-level project with layers and dependency rules |
| `context()` | Bounded context — owns aggregates, ports, services |
| `aggregate()` | Cluster of entities/value objects with a consistency boundary |
| `valueObject()` | Immutable type defined by its attributes |
| `entity()` | Object with identity, lives inside an aggregate |
| `command()` | Intent to change state (imperative: `RenameProduct`) |
| `event()` | Fact that happened (past tense: `ProductRenamed`) |
| `repository()` | Collection-like access to aggregates by identity |
| `applicationService()` | Orchestrates domain to fulfill a request |
| `port()` | Interface the domain needs from the outside world |
| `adapter()` | Concrete implementation of a port |
| `contextMap()` | Relationships between bounded contexts |
| `invariant()` | Architectural rule enforced at plan time |

## Monotonic regeneration

A resource is only regenerated when its declaration, `meta`, referenced files, upstream dependencies, or the model changes. Nothing else triggers a re-render. Settled work stays settled.

```bash
# Force re-render of a specific resource
bunx crest-spec apply --target aggregate.Catalog.Product --force
```

## Development

```bash
# Run tests
make test

# Set up test project from fixtures
make setup

# Plan / apply against the test project
make plan
make apply

# Incremental apply with build verification between waves
make apply-inc

# Apply a single resource
make apply-target T=aggregate.Catalog.Product

# Cleanup
make clean       # remove generated code
make reset       # clean + delete state db
make nuke        # remove test-project/ entirely
```

## Project structure

```
src/
  cli/          CLI commands and formatter
  dsl/          Spec DSL builders (project, context, aggregate)
  engine/       Apply engine, LLM client, constraint loop, prompt builder
  invariants/   Built-in architectural rules
  planner/      Hash computation, diffing, plan generation
  registry/     Resource registry
  state/        SQLite state database
fixtures/       Example specs for testing
tests/          Unit tests
docs/           Design documents and full specification
```

## Further reading

- [Full specification](docs/README.md) — complete design document with DDD vocabulary, state schema, LLM integration details
- [Authoring guide](docs/crest-spec-authoring-guide.md) — how to write specs
- [Design decisions](docs/design-decisions.md) — architectural rationale
