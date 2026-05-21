# crest-spec Implementation Design

## Overview

crest-spec is a declarative DSL for software architecture using DDD vocabulary. Users declare resources (bounded contexts, aggregates, commands, events, ports, adapters) in a TypeScript spec file. A planner diffs the spec against persisted state, and an apply engine uses Claude to generate implementation code, validated through a full constraint pipeline.

This document describes the implementation architecture of the crest-spec tool itself.

## Decisions

- **Runtime:** Bun (native TS execution, built-in SQLite via `bun:sqlite`, fast startup)
- **LLM provider:** Anthropic Claude (single provider for v1)
- **Spec loading:** Import-and-run. The spec file imports from the `crest-spec` package and calls builder functions. The CLI imports the spec file, which populates a registry as a side effect.
- **Code generation:** All LLM. No deterministic template generation — Claude generates all output files, guided by declarations and constraints.
- **Validation:** Full pipeline. Generated code is type-checked (`tsc`), contract-matched, invariant-checked, and test-run before acceptance.
- **Tool code style:** Classes with dependency injection, interfaces for testability. SOLID principles throughout.
- **Architecture:** Single package with internal module boundaries enforced by interfaces.

## Module Structure

```
src/
  dsl/           — builder API (project, context, aggregate, etc.)
  registry/      — in-memory resource graph
  state/         — SQLite schema, read/write, lock, history
  planner/       — effective_hash computation, spec-vs-state diffing
  engine/        — apply execution, LLM prompt construction, constraint loop
  invariants/    — rule definitions and checker
  cli/           — command implementations
```

## Section 1: DSL Builder API & Resource Registry

### Builder Classes

- **`ProjectBuilder`** — returned by `project("name", config)`. Entry point. Owns the `ResourceRegistry`. Exposes `.context()`, `.adapter()`, `.contextMap()`, `.invariants()`, `.meta()`.
- **`ContextBuilder`** — returned by `project.context("name", config)`. Scoped to a bounded context. Exposes `.aggregate()`, `.valueObject()`, `.entity()`, `.port()`, `.repository()`, `.applicationService()`, `.domainService()`.
- **`AggregateBuilder`** — returned by `context.aggregate("name", config)`. Exposes `.entity()` for child entities within the aggregate.

Each builder method creates a `ResourceDescriptor` and registers it with the `ResourceRegistry`.

### Helper Functions

`command()`, `event()`, `operation()`, `invariant()`, `relationship()`, `layer()` — factory functions exported from the package. They return typed descriptors used inside builder method calls.

### ResourceDescriptor

```ts
interface ResourceDescriptor {
  id: string;                    // e.g. "aggregate.Composition.Song"
  kind: ResourceKind;            // "context" | "aggregate" | "valueObject" | ...
  name: string;
  context: string | null;        // bounded context ID, null for project-level
  layer: string | null;
  declaration: Record<string, unknown>;
  meta: Meta;                    // merged meta (project -> context -> resource)
  dependencies: DependencyRef[];
  commands?: CommandDescriptor[];
  events?: EventDescriptor[];
  invariants?: string[];
}
```

### ResourceRegistry

Holds all descriptors and computes the dependency graph.

```ts
interface IResourceRegistry {
  getAll(): ResourceDescriptor[];
  getById(id: string): ResourceDescriptor | null;
  getByKind(kind: ResourceKind): ResourceDescriptor[];
  getByContext(contextId: string): ResourceDescriptor[];
  getDependents(id: string): ResourceDescriptor[];
  getDependencies(id: string): ResourceDescriptor[];
  topologicalOrder(): ResourceDescriptor[];
  getContextMap(): ContextRelationship[];
}
```

The registry validates basic structural rules at registration time: no duplicate IDs, no dangling dependency references.

### Spec Loading

The CLI calls `await import(specFilePath)`. The spec file imports `project`, `command`, `event`, etc. from `crest-spec`. The `project()` call creates a `ProjectBuilder` and registers it on a module-level singleton. After import, the CLI retrieves the registry from the singleton.

## Section 2: SQLite State Layer

### StateDatabase

Takes a file path, opens/creates the SQLite database via `bun:sqlite`, runs migrations if needed. All public methods are behind an `IStateDatabase` interface.

```ts
interface IStateDatabase {
  // Resource state
  getResource(id: string): StoredResource | null;
  getAllResources(): StoredResource[];
  upsertResource(resource: StoredResource): void;
  deleteResource(id: string): void;

  // Generated files
  getGeneratedFile(path: string): StoredFile | null;
  getFilesForResource(resourceId: string): StoredFile[];
  upsertGeneratedFile(file: StoredFile): void;
  deleteGeneratedFile(path: string): void;

  // Dependencies & relationships
  setDependencies(resourceId: string, deps: StoredDependency[]): void;
  getDependencies(resourceId: string): StoredDependency[];
  getDependents(resourceId: string): StoredDependency[];
  setContextRelationships(relationships: StoredContextRelationship[]): void;

  // Applies (history)
  beginApply(specHash: string): ApplyRecord;
  recordAction(applyId: number, resourceId: string, action: string, outcome: string): void;
  finishApply(applyId: number, status: string): void;

  // Generations (LLM audit trail)
  recordGeneration(gen: GenerationRecord): void;
  getGenerationsForResource(resourceId: string): GenerationRecord[];

  // Invariant checks
  recordInvariantCheck(check: InvariantCheckRecord): void;

  // Coordination lock
  acquireLock(holder: string): boolean;
  releaseLock(): void;
  getLock(): LockRecord | null;
  forceClearLock(): void;

  // Querying
  getApplies(limit?: number): ApplyRecord[];
  getApplyActions(applyId: number): ActionRecord[];
}
```

### Schema

Matches the schema sketch from the crest-spec specification: `resources`, `generated_files`, `dependencies`, `context_relationships`, `applies`, `apply_actions`, `generations`, `invariant_checks`, `lock`.

### Transactions

`apply` wraps its operation in `BEGIN IMMEDIATE`. This provides atomicity and serves as the coordination lock — concurrent applies get `SQLITE_BUSY` and can report the lock holder from the `lock` table.

### Migrations

Schema versioning via a `schema_version` pragma. On open, `StateDatabase` checks the version and applies pending migrations. For v1 there is only the initial schema.

### History

`applies`, `apply_actions`, `generations`, and `invariant_checks` are append-only. The `vacuum` command is the only thing that prunes history.

## Section 3: Planner / Differ

### Planner

Read-only. Takes the resource registry and state database, produces a `Plan`.

```ts
interface IPlanner {
  plan(registry: IResourceRegistry, state: IStateDatabase): Plan;
}

interface Plan {
  actions: PlannedAction[];
  invariantViolations: InvariantViolation[];
  display(): string;
}

interface PlannedAction {
  resourceId: string;
  action: "create" | "modify" | "destroy" | "refresh";
  reason: string;
  affectedFiles: string[];
  cascadedFrom?: string;
}
```

### Effective Hash Computation

`HashComputer` walks the registry in topological order and computes a SHA-256 for each resource by folding:

1. The resource's declaration (deterministic serialization — sorted keys, stable JSON)
2. The merged meta (project -> context -> resource; lists concatenate, scalars override)
3. Content hashes of files from `meta.references` and `meta.examples`
4. The `effective_hash` of every dependency (already computed due to topological ordering)
5. Applicable invariants (project-level and context-level)
6. The model identifier string

### Diff Logic

For each resource in the registry:
- Not in state -> `create`
- In state, `effective_hash` differs -> `modify` (with reason: declaration changed, meta changed, dependency changed, reference file changed, model changed)
- In state, `effective_hash` matches, but disk file hash differs from state -> `refresh` (hand-edit detected)

For each resource in state but not in the registry -> `destroy`.

### Cascade Tracking

When a resource is `modify`, the planner checks dependents. If a dependent's `effective_hash` changes (because it folds in the dependency's hash), it is also `modify` with `cascadedFrom` pointing to the root cause. The reason traces back to the original change.

## Section 4: Apply Engine & LLM Integration

### ApplyEngine

Executes a plan by processing actions in topological order.

```ts
interface IApplyEngine {
  apply(plan: Plan, registry: IResourceRegistry, state: IStateDatabase, options: ApplyOptions): Promise<ApplyResult>;
}

interface ApplyOptions {
  target?: string;
  force?: boolean;
  maxRetries?: number;  // default 3
  dryRun?: boolean;
}
```

### Execution Flow Per Resource

1. **Construct the LLM prompt** — `PromptBuilder` assembles the resource's declaration, merged meta, relevant invariants, port contracts, referenced documents, and state/commands/events of consumed aggregates. The output is a structured document with labeled sections.

2. **Call the LLM** — via `ILlmClient` wrapping the Anthropic SDK. The system prompt instructs Claude to return output as fenced code blocks annotated with file paths (e.g., ` ```ts // path: src/composition/domain/song.ts `). The engine parses these blocks into a `Map<string, string>` of path to content.

3. **Validate the output** — the `ConstraintLoop` runs four gates:
   - **must_compile:** Write output to a temp directory, run `tsc --noEmit`. Parse errors.
   - **must_match_contract:** If the resource implements a port, verify the declared method signatures are present. Uses regex matching against the contract strings declared on the port — checking that method names and type signatures appear in the generated source.
   - **must_satisfy_invariants:** Run applicable invariant rules against the generated code.
   - **must_pass_tests:** If tests were generated, run them via `bun test`.

4. **Retry on failure** — feed the violation back to the LLM as context. Re-invoke with the original prompt plus failure feedback. Up to `maxRetries` attempts.

5. **Write to disk** — hash new content, compare against disk. Skip identical files.

6. **Record in state** — update `resources`, insert into `generated_files`, append to `generations` and `apply_actions`.

### Coordination and Incremental Progress

The coordination lock (`BEGIN IMMEDIATE` + `lock` table) is held for the entire apply to prevent concurrent applies. However, individual resource state updates are committed incrementally — each resource's state is written after its output is validated. If a later resource fails, earlier successes are preserved. A re-run sees the succeeded resources' hashes as matching and skips them, picking up where the previous run left off. This matches the spec's "idempotent re-run" guarantee.

### LLM Client

```ts
interface ILlmClient {
  generate(prompt: string, systemPrompt: string): Promise<string>;
}
```

Thin wrapper over the Anthropic SDK. Handles rate limiting and API-level error retries (distinct from constraint loop retries).

### PromptBuilder

Assembles a focused prompt per resource. Resolves `meta.references` by reading files, collects contracts from implemented ports, gathers commands/events from consumed aggregates, applies meta inheritance. Produces a structured document, not chat-flavored.

## Section 5: Invariant Checker

### InvariantChecker

Validates architectural rules at two levels: structural (against declarations at plan time) and code-level (against generated output during apply).

```ts
interface IInvariantChecker {
  checkStructural(registry: IResourceRegistry): InvariantResult[];
  checkGenerated(resourceId: string, files: Map<string, string>, registry: IResourceRegistry): InvariantResult[];
}

interface InvariantResult {
  invariant: string;
  resourceId: string | null;
  status: "ok" | "violated";
  detail: string | null;
  rationale: string | null;
}
```

### Invariant Rules

Each invariant is a class implementing `IInvariantRule`:

```ts
interface IInvariantRule {
  name: string;
  appliesTo(resource: ResourceDescriptor): boolean;
  checkStructural?(resource: ResourceDescriptor, registry: IResourceRegistry): InvariantResult;
  checkGenerated?(resource: ResourceDescriptor, fileContents: Map<string, string>, registry: IResourceRegistry): InvariantResult;
}
```

New invariants are added by implementing a new class — the checker itself is not modified.

### Pre-canned Rules (v1)

**Structural:**
- Every aggregate root has a repository
- Context boundaries respected (dependencies only through declared relationships)
- Dependency rule (layer import restrictions per project config)
- No duplicate resource IDs
- Mutations route through ApplicationServices

**Code-level:**
- Domain layer has no infrastructure imports
- Contract compliance (implemented port methods present)
- Context isolation in imports

### Rationale Surfacing

Invariants declared with `meta.rationale` include that rationale in violation messages.

## Section 6: CLI Surface

### Command Interface

```ts
interface ICommand {
  name: string;
  description: string;
  run(args: ParsedArgs): Promise<number>;
}
```

### Commands

| Command | Subsystem |
|---|---|
| `init` | Creates scaffold `crest-spec.ts` and empty `crest-spec.db` |
| `plan` | `Planner.plan()` — display diff |
| `apply` | `Planner.plan()` then `ApplyEngine.apply()` — acquires lock |
| `apply -target X` | Filtered plan + apply for one resource and dependents |
| `apply -target X --force` | Invalidates stored hash for X before planning |
| `refresh` | Compares disk hashes against state, prompts to accept or revert |
| `graph` | Renders dependency graph as text or DOT format |
| `contextmap` | Renders context relationships as text or DOT format |
| `validate` | `InvariantChecker.checkStructural()` without diffing |
| `log` | Queries `applies` table |
| `history <resource>` | Queries `apply_actions` and `generations` for one resource |
| `diff <a> <b>` | Reconstructs state delta between two apply IDs |
| `state list` | Queries `resources` table |
| `state rm X` | Deletes resource from state (does not delete files) |
| `unlock` | `StateDatabase.forceClearLock()` |
| `vacuum --before DATE` | Prunes history older than DATE |
| `sql` | Spawns `sqlite3 crest-spec.db` as a child process |

### Arg Parsing

Lightweight library (`citty`) or hand-rolled minimal parser. The command surface is flat.

### Output Formatting

Structured text with ANSI colors for create/modify/destroy. `graph` and `contextmap` output DOT format (pipeable to Graphviz) with a plain-text fallback.

### Error Handling

Commands catch subsystem errors and present structured messages with exit codes. Lock contention reports the holder. Invariant violations list the rule, resource, and rationale. LLM failures show the last unresolved constraint violation.
