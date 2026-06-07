# SP3: CUE Loader, Resource Graph, and Planner — Design Spec

## Goal

Add the spec-layer data pipeline: load CUE spec files into typed Go structs, build a dependency graph with topological ordering and wave computation, and diff against SQLite state to produce a plan of create/modify/destroy/drift actions. No LLM calls — this is pure data processing.

## Architecture

```
CUE files (.cue)
    │ cuelang.org/go: load, unify, validate
    v
Project (typed Go struct tree)
    │ cue.NewRegistry(): walk + flatten
    v
Registry (map[string]Resource, flat ID-indexed)
    │ graph.Build(): extract edges
    v
Graph (DAG with topo sort, waves, hash propagation)
    │ plan.Plan(): diff against store
    v
[]PlannedAction (create/modify/destroy/drift)
```

Data flows left-to-right through four packages: `cue` → `graph` → `plan`, with `store` providing the persisted state for diffing.

## Approach

**CUE → JSON → Go structs.** Load CUE files with `cuelang.org/go/cue`, evaluate to a single unified value, validate constraints, marshal to JSON, then `json.Unmarshal` into fully typed Go structs. The Go structs mirror the CUE schema exactly. After loading, no CUE values are retained — the Go struct tree is the single source of truth.

---

## 1. Package: `internal/cue/`

### 1.1 Types (`types.go`)

Fully typed Go structs for every DSL object from SPEC.md section 1.4.

```go
type Project struct {
    Name       string                 `json:"name"`
    Layers     []string               `json:"layers"`
    LayerRules map[string]LayerRule   `json:"layerRules"`
    Meta       Meta                   `json:"meta"`
    Contexts   map[string]Context     `json:"contexts"`
    Adapters   map[string]Adapter     `json:"adapters"`
    AssetKinds map[string]AssetKind   `json:"assetKinds"`
    Assets     map[string]Asset       `json:"assets"`
    Invariants []Invariant            `json:"invariants"`
    ContextMap []ContextRelationship  `json:"contextMap"`
}

type LayerRule struct {
    DependsOn []string `json:"dependsOn"`
}

type Meta struct {
    Language    string   `json:"language,omitempty"`
    Style       string   `json:"style,omitempty"`
    Rules       []string `json:"rules,omitempty"`
    Prompts     []string `json:"prompts,omitempty"`
    References  []string `json:"references,omitempty"`
    Examples    []string `json:"examples,omitempty"`
    Avoid       []string `json:"avoid,omitempty"`
    Notes       string   `json:"notes,omitempty"`
    Rationale   string   `json:"rationale,omitempty"`
    ReviewLevel string   `json:"reviewLevel,omitempty"`
    Framework   string   `json:"framework,omitempty"`
}

type Context struct {
    Purpose              string                        `json:"purpose"`
    UbiquitousLanguage   map[string]string             `json:"ubiquitousLanguage,omitempty"`
    Meta                 Meta                          `json:"meta,omitempty"`
    Aggregates           map[string]Aggregate          `json:"aggregates,omitempty"`
    ValueObjects         map[string]ValueObject        `json:"valueObjects,omitempty"`
    DomainServices       map[string]DomainService      `json:"domainServices,omitempty"`
    ApplicationServices  map[string]ApplicationService `json:"applicationServices,omitempty"`
    Repositories         map[string]Repository         `json:"repositories,omitempty"`
    Ports                map[string]Port               `json:"ports,omitempty"`
    Assets               map[string]Asset              `json:"assets,omitempty"`
}

type Aggregate struct {
    Root         bool                         `json:"root,omitempty"`
    Purpose      string                       `json:"purpose,omitempty"`
    State        map[string]string            `json:"state,omitempty"`
    Commands     map[string]map[string]string `json:"commands,omitempty"`
    Events       map[string]map[string]string `json:"events,omitempty"`
    Invariants   []string                     `json:"invariants,omitempty"`
    Implements   string                       `json:"implements,omitempty"`
    Meta         Meta                         `json:"meta,omitempty"`
    Entities     map[string]Entity            `json:"entities,omitempty"`
    ValueObjects map[string]ValueObject       `json:"valueObjects,omitempty"`
    Validations  []Validation                 `json:"validations,omitempty"`
    Assets       map[string]Asset             `json:"assets,omitempty"`
}

type Entity struct {
    State       map[string]string `json:"state,omitempty"`
    Meta        Meta              `json:"meta,omitempty"`
    Validations []Validation      `json:"validations,omitempty"`
}

type ValueObject struct {
    From        string            `json:"from,omitempty"`
    State       map[string]string `json:"state,omitempty"`
    Description string            `json:"description,omitempty"`
    Invariants  []string          `json:"invariants,omitempty"`
    Meta        Meta              `json:"meta,omitempty"`
    Validations []Validation      `json:"validations,omitempty"`
}

type Port struct {
    Contract map[string]string `json:"contract,omitempty"`
    Meta     Meta              `json:"meta,omitempty"`
}

type Adapter struct {
    Implements  string       `json:"implements"`
    Layer       string       `json:"layer,omitempty"`
    Meta        Meta         `json:"meta,omitempty"`
    Validations []Validation `json:"validations,omitempty"`
}

type Repository struct {
    Of          string            `json:"of"`
    Contract    map[string]string `json:"contract,omitempty"`
    Meta        Meta              `json:"meta,omitempty"`
    Validations []Validation      `json:"validations,omitempty"`
}

type DomainService struct {
    Purpose     string       `json:"purpose,omitempty"`
    Uses        []string     `json:"uses,omitempty"`
    Meta        Meta         `json:"meta,omitempty"`
    Validations []Validation `json:"validations,omitempty"`
}

type ApplicationService struct {
    Purpose     string               `json:"purpose,omitempty"`
    Uses        []string             `json:"uses,omitempty"`
    Operations  map[string]Operation `json:"operations,omitempty"`
    Meta        Meta                 `json:"meta,omitempty"`
    Validations []Validation         `json:"validations,omitempty"`
}

type Operation struct {
    Input  map[string]string `json:"input,omitempty"`
    Output map[string]string `json:"output,omitempty"`
}

type AssetKind struct {
    Description string   `json:"description"`
    FilePattern string   `json:"filePattern,omitempty"`
    Prompts     []string `json:"prompts,omitempty"`
    References  []string `json:"references,omitempty"`
    Meta        Meta     `json:"meta,omitempty"`
}

type Asset struct {
    Kind        string       `json:"kind"`
    Description string       `json:"description,omitempty"`
    Prompts     []string     `json:"prompts,omitempty"`
    Targets     []string     `json:"targets,omitempty"`
    Meta        Meta         `json:"meta,omitempty"`
    Validations []Validation `json:"validations,omitempty"`
}

type Validation struct {
    Kind        string      `json:"kind"`
    Command     []string    `json:"command"`
    Description string      `json:"description,omitempty"`
    Assertions  []Assertion `json:"assertions,omitempty"`
}

type Assertion struct {
    Kind     string `json:"kind"`
    Expected int    `json:"expected,omitempty"`
    Path     string `json:"path,omitempty"`
    Pattern  string `json:"pattern,omitempty"`
    Regex    string `json:"regex,omitempty"`
}

type Invariant struct {
    Text string `json:"text"`
    Meta Meta   `json:"meta,omitempty"`
}

type ContextRelationship struct {
    From      string `json:"from"`
    To        string `json:"to"`
    Kind      string `json:"kind"`
    Direction string `json:"direction,omitempty"`
}
```

### 1.2 Loader (`loader.go`)

```go
func Load(specDir string) (*Project, error)
```

Steps:
1. Use `cuelang.org/go/cue/load` to load all `.cue` files in `specDir`
2. CUE unifies them into a single value
3. Validate CUE constraints (catches type errors, range violations, missing required fields)
4. Look up `project` field in the unified value
5. Marshal `project` to JSON via `cue.Value.MarshalJSON()`
6. `json.Unmarshal` into `*Project`
7. Return the populated struct (or error with CUE source position info)

Dependencies: `cuelang.org/go/cue`, `cuelang.org/go/cue/cuecontext`, `cuelang.org/go/cue/load`

### 1.3 Registry (`registry.go`)

```go
type Resource struct {
    ID           string
    Kind         string       // "project", "context", "aggregate", "entity", etc.
    ContextName  string       // empty for project-level resources
    ParentID     string       // e.g. "context.Synth" for aggregate.Synth.Voice
    Declaration  any          // the typed struct (Aggregate, ValueObject, etc.)
    Meta         Meta         // merged meta (project → context → resource)
    Dependencies []Edge
    Validations  []Validation
}

type Edge struct {
    TargetID string
    Kind     string // "uses", "implements", "of", "targets"
}

type Registry struct {
    Project   *Project
    Resources map[string]Resource
}

func NewRegistry(project *Project) (*Registry, error)
```

`NewRegistry` walks the Project tree and:
1. Creates `Resource` entries with computed IDs per SPEC.md section 1.6:
   - `project.{name}`
   - `context.{name}`
   - `aggregate.{context}.{name}`
   - `entity.{context}.{aggregate}.{name}`
   - `valueObject.{context}.{name}` (context-level) or nested under aggregate
   - `port.{context}.{name}`
   - `adapter.{name}`
   - `repository.{context}.{name}`
   - `domainService.{context}.{name}`
   - `applicationService.{context}.{name}`
   - `assetKind.{name}`
   - `asset.{name}` (project-level), `asset.{context}.{name}`, or `asset.{context}.{aggregate}.{name}`
2. Merges meta hierarchically: project meta → context meta → resource meta. List fields concatenate; scalar fields from more-specific levels override.
3. Extracts dependency edges:
   - `uses` field → Edge with Kind="uses"
   - `implements` field → Edge with Kind="implements"
   - `of` field → Edge with Kind="of"
   - `targets` field → Edge with Kind="targets" (assets)
   - `kind` field on assets → Edge with Kind="uses" targeting the assetKind
4. Validates all dependency target IDs exist in the registry — returns error if any dangling reference

All resource kinds are included in the registry, including structural kinds (project, context, assetKind). The planner filters structural kinds when building PlannedActions — the registry is the complete, unfiltered view.

---

## 2. Package: `internal/graph/`

Pure data structure — no CUE, no store, no I/O.

### 2.1 Graph (`graph.go`)

```go
type Graph struct {
    nodes   map[string]bool
    edges   map[string][]Edge  // source → targets (adjacency list)
    reverse map[string][]string // target → sources (reverse adjacency)
}

type Edge struct {
    TargetID string
    Kind     string
}

func Build(resources map[string]Resource) (*Graph, error)
func (g *Graph) TopologicalSort() ([]string, error)   // error on cycle
func (g *Graph) Waves() ([][]string, error)            // groups by dependency depth
func (g *Graph) Ancestors(id string) []string           // transitive deps
func (g *Graph) Dependents(id string) []string          // transitive reverse deps
func (g *Graph) Has(id string) bool
```

`Build` takes the registry's resource map and constructs the DAG from each resource's `Dependencies` edges. Returns error if a cycle is detected.

`TopologicalSort` returns resource IDs in dependency order (dependencies before dependents). Uses Kahn's algorithm. Returns error if the graph contains a cycle.

`Waves` groups resources into ordered batches where all dependencies of wave N are satisfied by waves 0..N-1. Wave 0 contains resources with no dependencies. This is computed from the topological sort by assigning each node a depth = 1 + max(depth of dependencies).

`Ancestors(id)` returns all transitive dependencies (recursive). `Dependents(id)` returns all transitive reverse dependencies (what depends on this).

### 2.2 Hash (`hash.go`)

```go
func ComputeEffectiveHashes(
    resources map[string]Resource,
    graph *Graph,
    model string,
) map[string]string
```

Computes effective hashes for all resources. For each resource:

```
effective_hash = SHA256(
    canonical_json(declaration) +
    sorted(effective_hash(dep) for dep in dependencies) +
    model
)
```

The computation uses memoization — each resource's hash is computed once and cached. Processing order follows topological sort so dependencies are always computed first.

The `model` parameter is the generation model from config — if the model changes, all resources get new effective hashes and are regenerated.

`canonical_json` is `json.Marshal` with sorted keys (Go's `encoding/json` sorts struct fields by tag order and map keys alphabetically, which is deterministic).

---

## 3. Package: `internal/plan/`

### 3.1 PlannedAction (`action.go`)

```go
type ActionKind string

const (
    ActionCreate  ActionKind = "create"
    ActionModify  ActionKind = "modify"
    ActionDestroy ActionKind = "destroy"
    ActionDrift   ActionKind = "drift"
)

type PlannedAction struct {
    ResourceID   string
    Kind         ActionKind
    Reason       string   // human-readable: "new resource", "declaration changed", etc.
    CascadedFrom string   // upstream resource ID that triggered this, empty if direct
    Files        []string // existing files affected (from generated_files table)
}
```

### 3.2 Planner (`planner.go`)

```go
type planStore interface {
    GetResource(id string) (*store.Resource, error)
    ListResources() ([]store.Resource, error)
    GetGeneratedFiles(resourceID string) ([]store.GeneratedFile, error)
}

type fileReader interface {
    ReadFile(path string) ([]byte, error)
}

type Planner struct {
    store planStore
    fs    fileReader // os-level file reads for drift detection
}

func New(store planStore, fs fileReader) *Planner
func (p *Planner) Plan(ctx context.Context, registry *cue.Registry, graph *graph.Graph, model string) ([]PlannedAction, error)
```

`Plan()` logic:

1. **Compute effective hashes** for all resources in the registry via `graph.ComputeEffectiveHashes`
2. **Load stored state** from SQLite via `store.ListResources()`
3. **Build stored hash map**: `storedID → effective_hash`
4. **Diff** — for each resource in the registry (excluding structural kinds: project, context, assetKind):
   - **Not in store** → `create` with reason "new resource"
   - **In store, declaration hash differs** → `modify` with reason "declaration changed"
   - **In store, declaration hash same, effective hash differs** → `modify` with reason "dependency changed ({upstream_id})". Walk ancestors to find which upstream resource changed, set `CascadedFrom`.
   - **In store, effective hash matches** → candidate for drift check (see step 6)
5. **Destroys** — for each resource in store but not in registry → `destroy` with reason "removed from spec". Load `GetGeneratedFiles` to populate `Files`.
6. **Drift detection** — for resources with matching effective hashes (no spec change), load `GetGeneratedFiles` and compare `content_hash` against actual file SHA256 on disk (via `fileReader`). If any file differs → `drift` with reason "refresh needed (file modified on disk)".
7. **Sort actions**: destroys first, then creates/modifies in topological order.

The `fileReader` interface is injected for testability — production uses `os.ReadFile`, tests use an in-memory map.

---

## 4. Store Extensions

### 4.1 New Domain Types (`store.go`)

```go
type Resource struct {
    ID              string
    Kind            string
    ContextName     string
    DeclarationHash string
    EffectiveHash   string
    Model           string
    SettledAt       time.Time
}

type GeneratedFile struct {
    Path        string
    ResourceID  string
    ContentHash string
    PromptHash  string
    Model       string
    CreatedAt   time.Time
}

type Dependency struct {
    SourceID string
    TargetID string
    Kind     string
}
```

### 4.2 New sqlc Queries (`sql/queries/resources.sql`)

```sql
-- Resource state
-- name: GetResource :one
SELECT * FROM resources WHERE id = ?;

-- name: ListResources :many
SELECT * FROM resources ORDER BY id;

-- name: SetResource :exec
INSERT INTO resources (id, kind, context_name, declaration_hash, effective_hash, model, settled_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    kind = excluded.kind,
    context_name = excluded.context_name,
    declaration_hash = excluded.declaration_hash,
    effective_hash = excluded.effective_hash,
    model = excluded.model,
    settled_at = excluded.settled_at;

-- name: DeleteResource :exec
DELETE FROM resources WHERE id = ?;

-- Generated files
-- name: GetGeneratedFiles :many
SELECT * FROM generated_files WHERE resource_id = ?;

-- name: SetGeneratedFile :exec
INSERT INTO generated_files (path, resource_id, content_hash, prompt_hash, model, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
    resource_id = excluded.resource_id,
    content_hash = excluded.content_hash,
    prompt_hash = excluded.prompt_hash,
    model = excluded.model,
    created_at = excluded.created_at;

-- name: DeleteGeneratedFiles :exec
DELETE FROM generated_files WHERE resource_id = ?;

-- Dependencies
-- name: SetDependency :exec
INSERT INTO dependencies (source_id, target_id, kind)
VALUES (?, ?, ?)
ON CONFLICT(source_id, target_id, kind) DO NOTHING;

-- name: GetDependencies :many
SELECT * FROM dependencies WHERE source_id = ?;

-- name: DeleteDependencies :exec
DELETE FROM dependencies WHERE source_id = ?;
```

### 4.3 New Store Methods

Thin wrappers around sqlc with domain type conversions:

```go
func (s *Store) GetResource(id string) (*Resource, error)
func (s *Store) ListResources() ([]Resource, error)
func (s *Store) SetResource(r Resource) error
func (s *Store) DeleteResource(id string) error

func (s *Store) GetGeneratedFiles(resourceID string) ([]GeneratedFile, error)
func (s *Store) SetGeneratedFile(f GeneratedFile) error
func (s *Store) DeleteGeneratedFiles(resourceID string) error

func (s *Store) SetDependency(sourceID, targetID, kind string) error
func (s *Store) GetDependencies(sourceID string) ([]Dependency, error)
func (s *Store) DeleteDependencies(sourceID string) error
```

---

## 5. Testing Strategy

### 5.1 CUE Loader Tests (`internal/cue/`)
- Load a minimal CUE fixture with one context, one aggregate, one value object, one asset
- Verify all struct fields populate correctly through JSON round-trip
- Test multi-file unification (two `.cue` files that merge)
- Test CUE constraint violations produce errors
- Test missing required fields produce errors

### 5.2 Registry Tests (`internal/cue/`)
- Verify ID computation for every resource kind
- Verify meta merging (project → context → resource)
- Verify dependency edge extraction from uses/implements/of/targets/kind
- Verify dangling reference detection (dependency to nonexistent ID)

### 5.3 Graph Tests (`internal/graph/`)
- Topological sort with known graph
- Cycle detection
- Wave computation (diamond dependency)
- Ancestors and Dependents
- Empty graph

### 5.4 Hash Tests (`internal/graph/`)
- Effective hash stability (same input → same hash)
- Hash cascading (changing a dependency changes all downstream hashes)
- Model change cascades all hashes

### 5.5 Planner Tests (`internal/plan/`)
- Empty store + new registry → all creates
- Matching hashes → no actions
- Changed declaration → modify
- Cascading dependency change → modify with CascadedFrom
- Resource removed from spec → destroy
- Drift detection (file hash differs from stored)
- Structural kinds excluded from actions
- Actions sorted correctly (destroys first, then topo order)

### 5.6 Store Tests (`internal/store/`)
- CRUD for resources, generated_files, dependencies
- Upsert behavior (SetResource updates existing)
- Cascade delete (DeleteResource cascades to generated_files via FK)

### 5.7 CUE Test Fixtures
Test CUE files live in `testdata/` directories within each package that needs them. Minimal but representative of real specs.

---

## 6. Dependencies

New Go dependency: `cuelang.org/go` (CUE Go API for loading, evaluating, and marshaling CUE files).

No other new dependencies. The graph and plan packages use only the standard library plus the internal packages.

---

## 7. What SP3 Does NOT Include

- **Prompt construction** — SP4 (Prompt Builder)
- **Apply execution** — SP5 (Spec Engine + Constraint Loop)
- **LLM calls** — none in SP3
- **Apply/generation/session store operations** — deferred to SP5
- **MCP tool handler wiring** — the spec/* tool stubs remain stubs; SP5 wires them
