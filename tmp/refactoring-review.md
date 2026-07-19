# Refactoring Review: crest-spec full project

**Date:** 2026-07-15  
**Branch reviewed:** `main`  
**Implementation branch:** `refactor/safety-boundaries`  
**Base branch:** `main` (full-tree review; not a branch-diff review)  
**Files reviewed:** 380 source, specification, migration, fixture, and test files inventoried; 171 hand-written Go/JavaScript files structurally scanned; high-risk files deep-read  
**Review constraint:** The original review was read-only. The implementation status below records the subsequent safety and structural changes.

## Scope and diagnosis

The concern is justified. crest-spec has not merely accumulated some long files; several architectural boundaries have collapsed as new workstreams were added. Persistence, orchestration, MCP transport, dashboard composition, and frontend rendering each have a growing central object or file, and many of those centers discard errors that should be part of crest-spec's fail-closed governance model.

Generated `internal/db/*.go` files are sqlc output and are not counted as hand-written quality debt. The repeated crest-synth phase fixtures are also intentional test inputs and are not treated as duplicate production design. The maintainable surface is the CUE model, migrations and SQL queries, store adapters, application/domain services, transports, dashboard, and tests around those boundaries.

### Size and coupling baseline

| Surface | Current size / shape | Assessment |
|---|---:|---|
| Hand-written production Go | 86 files, 25,479 lines | 15 files at or above 500 lines contain 12,690 lines, almost half of the production Go code |
| Go tests | 76 files, 12,034 lines, 418 tests | Healthy volume, but mega-interface fakes and happy-path integration setup weaken failure isolation |
| Generated sqlc Go | 10,330 lines | Excluded from debt findings |
| Dashboard JavaScript | 1,867 lines | `app.js` alone is 1,276 lines, 68% of the JavaScript |
| Store surface | About 170 `*Store` methods | Persistence facade spans unrelated aggregates and domain policy |
| `specStore` | 134 methods | Direct Interface Segregation Principle violation |
| MCP `specHandler` | 97 methods | Transport is coupled to nearly the entire application |
| `Spec` | 144 receiver methods | Compatibility facade has become the application architecture |

The largest hand-written files are:

| File | Lines | Main concern |
|---|---:|---|
| `internal/store/store.go` | 1,706 | Database setup, migrations, many aggregates, maintenance, raw SQL |
| `internal/store/executions.go` | 1,417 | DTOs, redaction, state policy, candidate governance, transactions, hydration |
| `cmd/crest-spec/static/js/app.js` | 1,276 | Most dashboard features and rendering remain in one global module |
| `internal/mcp/tools.go` | 1,272 | Tool catalog, duplicated schemas, argument DTOs, registration, handlers |
| `cmd/crest-spec/dashboard.go` | 1,073 | Process bootstrap, routes, API contracts, joins, raw SQL, SSE |
| `internal/store/evaluation_runs.go` | 1,031 | Persistence, leasing, metric aggregation, comparison policy |
| `internal/spec/session.go` | 907 | Session state machine, deletion, wave execution, validation, completion |
| `internal/observability/service.go` | 779 | Read-model joins, filtering, pagination, explanation, N+1 loading |

## Critical Issues

These are correctness, safety, or governance problems and should be addressed before adding another broad workstream.

### C1. Destructive reconciliation is fail-open

**Evidence**

- [`executeDestroys`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:248) cannot return an error and ignores action creation, generated-file lookup, filesystem removal, generated-file deletion, dependency deletion, behavioral-state deletion, resource deletion, and audit-update errors.
- At [`session.go:260`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:260), an error from `CountOtherFileOwners` is treated like zero other owners, so execution falls through to file deletion.
- At [`session.go:276`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:276), the resource is reported as destroyed regardless of all prior failures.
- [`ConfirmDestroys`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:284) therefore cannot tell the caller that a confirmed deletion only partially happened.

**Smells/principles:** Long Method, divergent change, inappropriate intimacy with persistence/filesystem details, SRP violation, missing transaction boundary.

**Impact:** Shared files can be deleted when ownership cannot be established, metadata can be removed while files remain, and the audit trail can claim a successful destructive operation that did not occur. This conflicts directly with crest-spec's human-gated deletion and fail-closed validation guarantees.

**Required direction:** Add fault-injection characterization tests first. Extract a `DestroyResources` use case that preflights every ownership decision, returns per-resource errors, stages filesystem actions, performs metadata changes atomically, and only reports confirmed results. An ownership lookup failure must block deletion.

### C2. Session, validation, completion, and observability errors are converted into success or absence

**Evidence**

- Session resource seeding ignores every upsert at [`session.go:310`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:310).
- `Next` ignores authoritative session-resource and wave-update failures around [`session.go:355`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:355).
- `Finish` ignores the authoritative resource query at [`session.go:496`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:496), then ignores session, apply, and lock finalization writes at [`session.go:565`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:565).
- `VerifyWave` initializes `Passed: true` and returns that result when SQLite fails at [`session.go:640`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:640).
- Command launch failures are returned as success at [`session.go:685`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:685), plan failures omit project validations at [`session.go:678`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:678), and completion-refresh errors are discarded at [`session.go:722`](/Users/crestenstclair/workspace/crest-spec/internal/spec/session.go:722).
- Terminal execution transitions discard candidate lookup/disposition errors at [`executions.go:632`](/Users/crestenstclair/workspace/crest-spec/internal/store/executions.go:632).
- Historical evaluation eligibility converts any provenance read error into an ordinary ineligible case at [`evaluations.go:125`](/Users/crestenstclair/workspace/crest-spec/internal/store/evaluations.go:125).
- Dashboard status ignores all primary repository errors at [`dashboard.go:207`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/dashboard.go:207).
- Goal evidence turns a verification-facts failure into “missing evidence” at [`service.go:286`](/Users/crestenstclair/workspace/crest-spec/internal/observability/service.go:286); plan and attempt views similarly treat any active-session, execution, or candidate error as absence at [`operational_service.go:28`](/Users/crestenstclair/workspace/crest-spec/internal/observability/operational_service.go:28) and [`operational_service.go:227`](/Users/crestenstclair/workspace/crest-spec/internal/observability/operational_service.go:227).

**Smells/principles:** Error swallowing, primitive absence semantics, divergent error policy, long transaction scripts, violation of crest-spec's fail-closed model.

**Impact:** Canonical state and dashboard projections can look complete, passed, or merely absent when persistence or execution actually failed. In an auditability product, an incomplete view without an explicit degraded-state marker is a correctness failure.

**Required direction:** Establish one error policy: only a typed `ErrNotFound` may become domain absence; malformed canonical data and operational failures must propagate. Lifecycle operations need transaction-level repository methods such as `CreateSessionWithResources` and `FinalizeSession`. Observability responses need an explicit unavailable/degraded state rather than silent omission.

### C3. SQLite connection and inspection boundaries are not reliable

**Evidence**

- [`store.New`](/Users/crestenstclair/workspace/crest-spec/internal/store/store.go:150) runs WAL, busy timeout, and foreign-key PRAGMAs through `*sql.DB` once, but `database/sql` owns a connection pool and the connection-scoped PRAGMAs are not guaranteed on later connections.
- `:memory:` stores can create a distinct database per pooled connection; there is no explicit single-connection or shared-memory policy.
- Agent-facing [`ReadOnlyQuery`](/Users/crestenstclair/workspace/crest-spec/internal/store/store.go:1660) treats a six-character `SELECT` prefix as the complete read-only boundary and executes the original SQL without context, timeout, row cap, byte cap, or single-statement enforcement. MCP exposes it at [`tools.go:1223`](/Users/crestenstclair/workspace/crest-spec/internal/mcp/tools.go:1223).

**Smells/principles:** Leaky abstraction, fragile boundary validation, missing centralized database factory.

**Impact:** Foreign-key and locking behavior can vary by connection. The inspection shell's safety depends on SQLite driver behavior rather than an enforced query-only connection.

**Required direction:** Introduce a single `openSQLite` constructor using modernc's DSN/connector facilities so every connection receives the required settings. Give in-memory tests an explicit shared/single-connection policy. Run inspection queries on a query-only connection/transaction, require exactly one statement, use `QueryContext`, and cap rows and bytes. Add multi-connection and adversarial query tests.

### C4. Candidate mutation is not one governed atomic operation

**Evidence**

- [`CommitAttempt`](/Users/crestenstclair/workspace/crest-spec/internal/spec/execution.go:266) is a 139-line transaction script spanning candidate persistence, filesystem mutation, execution state, validation, rejection routing, resource acceptance, completion, and amendments.
- Files are written before the execution enters validating state at [`execution.go:301`](/Users/crestenstclair/workspace/crest-spec/internal/spec/execution.go:301). A later persistence error leaves the worktree mutated while governance state is incomplete.
- Missing plan action, malformed session hashes/declarations, and completion projection errors are ignored at [`execution.go:328`](/Users/crestenstclair/workspace/crest-spec/internal/spec/execution.go:328), [`execution.go:365`](/Users/crestenstclair/workspace/crest-spec/internal/spec/execution.go:365), and [`execution.go:395`](/Users/crestenstclair/workspace/crest-spec/internal/spec/execution.go:395).
- MCP requests are dispatched concurrently at [`server.go:301`](/Users/crestenstclair/workspace/crest-spec/internal/mcp/server.go:301), but there is no workspace coordinator for overlapping candidate paths or shared generated files.
- Project path resolution is inconsistent: the planner reads raw stored paths at [`planner.go:279`](/Users/crestenstclair/workspace/crest-spec/internal/plan/planner.go:279), candidate create detection reads raw relative paths at [`execution.go:432`](/Users/crestenstclair/workspace/crest-spec/internal/spec/execution.go:432), while writes and most validations derive a project root.

**Smells/principles:** Long Method, data clumps, missing command/unit-of-work abstraction, inconsistent boundary ownership.

**Impact:** Filesystem state, candidate state, execution state, and completion state can diverge. Running crest-spec outside the project root can misclassify files or plan unnecessary regeneration. Parallel commits can race on shared files.

**Required direction:** First introduce a single `ProjectWorkspace` abstraction for contained path resolution, reads, writes, staging, and validation cwd. Then split the pipeline into `CandidateWorkspace`, `CandidateValidator`, and `CandidateFinalizer`, while retaining the current public facade. Add a coordinator that serializes overlapping path sets but permits non-overlapping resource work.

### C5. Evaluation creation and sealing contain check-then-act races

**Evidence**

- Evaluation cases pre-read by identity, insert with `ON CONFLICT DO NOTHING`, then fetch by the losing newly generated ID at [`evaluations.go:244`](/Users/crestenstclair/workspace/crest-spec/internal/store/evaluations.go:244).
- Evaluation configurations repeat the pattern at [`evaluations.go:453`](/Users/crestenstclair/workspace/crest-spec/internal/store/evaluations.go:453).
- Dataset ordinals are calculated with `len(members)` outside a transaction at [`evaluations.go:340`](/Users/crestenstclair/workspace/crest-spec/internal/store/evaluations.go:340).
- Dataset membership is read and hashed before a separate sealing update at [`evaluations.go:360`](/Users/crestenstclair/workspace/crest-spec/internal/store/evaluations.go:360).
- Lease expiry and requeue writes are discarded before assignment claim at [`evaluation_runs.go:428`](/Users/crestenstclair/workspace/crest-spec/internal/store/evaluation_runs.go:428).

**Smells/principles:** Check-then-act race, transaction boundary split across repository calls, primitive state transitions.

**Impact:** Concurrent hosts can receive not-found after a logically successful idempotent create, collide on ordinals, seal a hash that does not describe final membership, or claim work against stale lease state.

**Required direction:** Add concurrent characterization tests. Implement transactional get-or-create returning the canonical existing identity, calculate ordinals in SQL within the same transaction, and serialize add/seal and lease-expiry/claim operations with conditional writes.

### C6. Dashboard live updates and frontend event handlers have concrete correctness/security defects

**Evidence**

- SSE change detection hashes only the serialized payload length at [`dashboard.go:1029`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/dashboard.go:1029). Equal-length transitions, such as `pending` to `blocked`, are suppressed indefinitely until payload length changes.
- The same SSE block discards repository and JSON errors, and uses a mutex even though its closure is called serially.
- The frontend emits spec/store-controlled identifiers inside single-quoted inline JavaScript handlers, for example generation/resource data at [`app.js:653`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/static/js/app.js:653). `encodeURIComponent` does not encode apostrophes, and [`escapeHTML`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/static/js/components.js:1) is an HTML-text escaper, not a JavaScript-string escaper.
- The pattern is repeated throughout `app.js`; [`Object.assign(window, ...)`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/static/js/app.js:1269) exists to service these inline handlers.

**Smells/principles:** Wrong-context escaping, global mutable namespace, duplicated string-built DOM, hidden failure path.

**Impact:** Live state can remain stale despite successful backend changes, and a crafted spec-controlled identifier can break out of the inline JavaScript string context.

**Required direction:** Share a `statusSnapshot(ctx) ([]byte, error)` between HTTP status and SSE, compare bytes or a real content hash, and test equal-length transitions. Replace inline handlers with DOM data attributes and delegated `addEventListener`; then remove window globals. Add a DOM/browser test containing apostrophes, quotes, and markup in identifiers.

### C7. MCP's declared contract is duplicated and malformed input is normally accepted

**Evidence**

- Stub tool definitions at [`tools.go:65`](/Users/crestenstclair/workspace/crest-spec/internal/mcp/tools.go:65) duplicate live definitions beginning at [`tools.go:586`](/Users/crestenstclair/workspace/crest-spec/internal/mcp/tools.go:586), with different levels of schema detail.
- Generic `specTool` and `specToolErr` wrappers explicitly discard `json.Unmarshal` failures at [`tools.go:166`](/Users/crestenstclair/workspace/crest-spec/internal/mcp/tools.go:166) and [`tools.go:180`](/Users/crestenstclair/workspace/crest-spec/internal/mcp/tools.go:180). Only two registrations use the strict wrapper.
- MCP's `specHandler` has 97 methods at [`server.go:77`](/Users/crestenstclair/workspace/crest-spec/internal/mcp/server.go:77), so registration tests need a 75-method fake and cannot isolate capability boundaries cleanly.

**Smells/principles:** Duplicate code, divergent change, primitive schema strings, ISP violation.

**Impact:** The advertised MCP schema can drift from the actual handler, and malformed JSON silently becomes a zero-value request instead of a deterministic input error.

**Required direction:** Define each tool once as a typed descriptor containing its schema, handler, availability, and mutation class. Use one strict decoder for every tool. Split tool registration and handler dependencies by capability group.

## Refactoring Opportunities

These are ordered by leverage. The first four establish safety seams; later items then become smaller, reviewable extractions.

### R1. Break the mega-interfaces into consumer-owned ports

`specStore` has 134 methods at [`spec.go:21`](/Users/crestenstclair/workspace/crest-spec/internal/spec/spec.go:21), while tests advertised as needing one behavior either implement dozens of methods or embed a nil interface, as in [`bootstrap_test.go:90`](/Users/crestenstclair/workspace/crest-spec/internal/spec/bootstrap_test.go:90) and [`runtime_test.go:15`](/Users/crestenstclair/workspace/crest-spec/internal/spec/runtime_test.go:15). This makes unexpected calls compile and panic only at runtime.

Extract narrow ports around these use cases:

1. Project intent and completion
2. Reconciliation and sessions
3. Context and attempts
4. Execution and candidates
5. Validation and evidence
6. Amendments and learning
7. Evaluation
8. Inspection/observability

Keep `Store` and `Spec` temporarily as compatibility facades delegating to those services. This is an incremental migration, not a rewrite.

### R2. Move domain policy out of persistence

Evaluation aggregation and winner selection live at [`evaluation_runs.go:834`](/Users/crestenstclair/workspace/crest-spec/internal/store/evaluation_runs.go:834), promotion eligibility at [`evaluation_promotions.go:103`](/Users/crestenstclair/workspace/crest-spec/internal/store/evaluation_promotions.go:103), execution orchestration across [`executions.go:557`](/Users/crestenstclair/workspace/crest-spec/internal/store/executions.go:557), and goal reconciliation at [`goals.go:129`](/Users/crestenstclair/workspace/crest-spec/internal/store/goals.go:129).

Move pure evaluation comparison first into an `internal/evaluation` domain package. Then move transition decisions into application/domain services, leaving repositories responsible for loading and atomically persisting prepared transitions. Temporary type aliases/adapters can keep public callers stable.

### R3. Decompose the large transactional methods without breaking their outer transactions

The main auditability hotspots are:

- `FinalizeCandidate`: 215 lines, [`executions.go:684`](/Users/crestenstclair/workspace/crest-spec/internal/store/executions.go:684)
- `RecordValidationRun`: 184 lines, [`validation_evidence.go:138`](/Users/crestenstclair/workspace/crest-spec/internal/store/validation_evidence.go:138)
- `ReconcileProjectIntent`: 182 lines, [`goals.go:129`](/Users/crestenstclair/workspace/crest-spec/internal/store/goals.go:129)
- `CreateContextManifest`: 159 lines, [`context_manifests.go:99`](/Users/crestenstclair/workspace/crest-spec/internal/store/context_manifests.go:99)
- `TransitionExecution`: 127 lines, [`executions.go:557`](/Users/crestenstclair/workspace/crest-spec/internal/store/executions.go:557)
- `StartExecution`: 110 lines, [`executions.go:203`](/Users/crestenstclair/workspace/crest-spec/internal/store/executions.go:203)

Preserve one outer transaction but extract named validation, accepted/rejected disposition, linkage, event, and projection steps. Fault-injection tests should prove rollback at each step before extraction.

### R4. Complete the dashboard backend and frontend modularization

[`dashboard.go`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/dashboard.go:32) combines process startup, 49 route/static registrations, legacy and v1 endpoints, query composition, evaluation aggregation, execution joins, raw SQL, and SSE. It depends on concrete `*store.Store` and `*spec.Spec`; `cfg` and `log` fields are unused.

Introduce `newDashboardHandler(deps)` and grouped handlers for:

- project/goal/resource/plan
- attempts/context/execution/validation
- evaluations/failures
- legacy endpoints
- live status

On the frontend, `app.js` still owns evaluations, sessions, generations, contexts, executions, verifications, and invariants. Extract one feature module per remaining tab and shared table, drilldown, explanation, and list helpers. Replace 120 inline style fragments with CSS classes as modules move.

### R5. Establish one versioned dashboard API contract

Some `/api/v1` endpoints return observability DTOs with snake_case and versioned errors; context, execution, evaluation, and verification endpoints often expose raw store structs with PascalCase and legacy `{error:string}` responses. The frontend consequently checks `Foo || foo` throughout [`app.js:830`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/static/js/app.js:830).

Create explicit DTOs for every v1 endpoint, one error mapper, and one pagination contract. Normalize at the API boundary, then remove dual-case frontend fallbacks and the transitional `fetchJSON` adapter at [`api.js:32`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/static/js/api.js:32).

### R6. Replace N+1 hydration and hard-cap scans with summary/detail queries

Current examples include:

- Resource detail scans every resource and queries each dependency set at [`service.go:435`](/Users/crestenstclair/workspace/crest-spec/internal/observability/service.go:435), then performs two queries per attempt and loads 1,000 validation aggregates to filter.
- Attempt detail loads 10,000 validation aggregates at [`operational_service.go:259`](/Users/crestenstclair/workspace/crest-spec/internal/observability/operational_service.go:259).
- Failure listing loads 10,000 records and filters/pages in memory at [`operational_service.go:133`](/Users/crestenstclair/workspace/crest-spec/internal/observability/operational_service.go:133).
- Execution/context/validation/evaluation list methods fully hydrate children and large blobs per item in their store files.

Add typed summary projections and targeted repository queries such as `ListConsumers`, `ListValidationsByAttempt`, `ListValidationsByTarget`, filtered failure queries, and aggregate count queries. Load blobs only for detail inspectors. A hard cap must not masquerade as completeness.

### R7. Split context assembly into observable context sources

`contextCandidates` is 181 lines at [`context_manifest.go:196`](/Users/crestenstclair/workspace/crest-spec/internal/spec/context_manifest.go:196). Runtime dependency/file errors are usually skipped at [`runtime.go:27`](/Users/crestenstclair/workspace/crest-spec/internal/spec/runtime.go:27), so the manifest cannot explain that potentially relevant context was unavailable.

Introduce components such as `IntentSource`, `ContractSource`, `RepositoryCodeSource`, `ConsumerSource`, `FailureSource`, and `ConventionSource`. Each source should return candidates plus explicit unavailable/omitted decisions. Keep budgeting and final selection centralized.

Also separate the `IncrementLearningApplied` mutation at [`runtime.go:75`](/Users/crestenstclair/workspace/crest-spec/internal/spec/runtime.go:75) from the context-building query so context construction has no hidden persistence side effect.

### R8. Extract completion fact collection from projection

[`reprojectCompletion`](/Users/crestenstclair/workspace/crest-spec/internal/spec/completion_projection.go:16) is a 220-line method combining repository reads, index construction, graph traversal, evidence interpretation, recursive goal derivation, and persistence.

Create typed immutable indexes and a `CompletionFactCollector`, pass collected facts into the existing pure completion projector, and persist projections in a final step. This keeps completion logic testable without a 134-method repository.

### R9. Unify validation execution and provenance mapping

Legacy `RunCommand` at [`validate.go:30`](/Users/crestenstclair/workspace/crest-spec/internal/spec/validate.go:30) coexists with the controlled runner at [`runner.go:74`](/Users/crestenstclair/workspace/crest-spec/internal/validation/runner.go:74). Timeout parsing errors are discarded, and execution-to-evidence mapping is duplicated between witness and general validation paths.

Route all validations through one injected runner, centralize timeout parsing and persistence mapping, and change `VerifyWave` to return `(result, error)`. Keep behavioral classification in the pure check layer.

### R10. Replace `Resource.Declaration any` and kind switches with a resource descriptor seam

The CUE/DDD model itself is not redundant, but the implementation makes each resource kind expensive to extend. [`cue.Resource`](/Users/crestenstclair/workspace/crest-spec/internal/cue/registry.go:14) stores `Declaration any` and a raw kind string. Adding a recurring non-DDD resource concern can require edits to registration, amendments, profiles, traceability, prompt construction, CUE paths, and validation switches. Examples are [`registry.go:45`](/Users/crestenstclair/workspace/crest-spec/internal/cue/registry.go:45), [`registry.go:476`](/Users/crestenstclair/workspace/crest-spec/internal/cue/registry.go:476), [`goals.go:198`](/Users/crestenstclair/workspace/crest-spec/internal/spec/goals.go:198), and [`cuewrite.go:30`](/Users/crestenstclair/workspace/crest-spec/internal/spec/cuewrite.go:30).

Introduce a `ResourceDescriptor`/registry adapter per resource family that supplies stable kind, metadata, dependencies, validations, contributions, profiles, prompt sections, and amendment behavior. Goals/capabilities should remain orthogonal traceability over resources; they should not replace DDD declarations. This preserves DDD's domain vocabulary while preventing new project-level functionality from causing shotgun surgery.

### R11. Make the CLI a thin testable adapter

[`main.go`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/main.go:28) mixes global argument parsing, process exits, store/config construction, application calls, formatting, HTTP transport, and command implementations. `cmdDiff` duplicates application behavior, and deletion has multiple divergent entry points.

Introduce an `App` with injected input/output and a command registry. Have every mutation command call one governed application use case. Leave `main` responsible only for dependency construction and process exit.

### R12. Improve test seams before splitting files

The raw count of 418 Go tests is healthy, but structural coverage is uneven:

- Dashboard has 48 handlers and only 9 dashboard tests; there is no route-registration, SSE, encoding-failure, or ignored-repository-error test.
- There is no executable JavaScript test setup. Current frontend tests are source substring checks at [`dashboard_static_test.go:15`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/dashboard_static_test.go:15) and [`dashboard_context_test.go:103`](/Users/crestenstclair/workspace/crest-spec/cmd/crest-spec/dashboard_context_test.go:103).
- Large acceptance fixtures directly construct persistence records, which is valuable end-to-end coverage but gives poor failure localization.
- Mega-interface fakes embed nil dependencies, weakening compile-time guarantees.

Add fault-injecting narrow fakes, test builders such as `IntentScenario`, `AttemptScenario`, and `ValidationScenario`, a small DOM/browser suite, and table-driven handler contract/error tests. Retain one full goal-to-evidence dashboard trace as acceptance coverage.

## Minor Suggestions

- Migrate the 129 production `context.Background()` calls in `internal/store` to context-aware repository methods. Keep background-context wrappers only at compatibility edges.
- Replace seven-to-nine-scalar method signatures such as [`UpdateGeneration`](/Users/crestenstclair/workspace/crest-spec/internal/store/store.go:821), [`UpdateSessionResourceState`](/Users/crestenstclair/workspace/crest-spec/internal/store/store.go:1193), and [`CreateEvaluationRun`](/Users/crestenstclair/workspace/crest-spec/internal/store/evaluation_runs.go:176) with parameter objects.
- Standardize absence semantics. The store currently uses `ErrNotFound`, `("", nil)`, and `(nil, nil)` for similar concepts.
- Make all canonical time/JSON converters strict. [`parseTime`](/Users/crestenstclair/workspace/crest-spec/internal/store/store.go:267) currently maps malformed persisted timestamps to zero time.
- Consolidate typed lifecycle statuses and remove scattered raw `"complete"`, `"pending"`, lease, amendment, candidate, and evaluation strings.
- Inject a clock; persistence currently mixes a global `now()` helper with direct `time.Now()` calls.
- Record migration checksums and applied timestamps, not only filenames, at [`store.go:188`](/Users/crestenstclair/workspace/crest-spec/internal/store/store.go:188).
- Break [`Vacuum`](/Users/crestenstclair/workspace/crest-spec/internal/store/store.go:1577) into aggregate retention policies. Every new content-blob owner currently requires manual edits to one central SQL list.
- Remove or formally deprecate dead compatibility paths: `Spec.Commit` always errors, `prompt.InjectRuntimeContext` has no production caller, `runProjectValidations` is test-only, and `AcceptCandidateFiles` appears to be a second unused acceptance path.
- Remove the production default package name `crestsynth` from [`cuewrite.go:178`](/Users/crestenstclair/workspace/crest-spec/internal/spec/cuewrite.go:178). A general framework should derive or require a package name rather than leak a fixture identity.
- Consolidate dashboard limits: legacy `dashboardLimit` silently defaults and permits 500, while v1 pagination rejects invalid input and caps at 200.
- Remove dead frontend polling/cache state and prevent overlapping refreshes with an in-flight or abort policy.
- Add a checked-in `golangci-lint` configuration. The Makefile requires the tool, but the repository does not define a stable ruleset.

## Recommended implementation order

### Phase A — Safety characterization and correctness boundaries

1. Add fault-injection tests for destruction, session finalization, wave verification, completion refresh, observability repository errors, and execution transitions.
2. Add concurrency tests for SQLite connection PRAGMAs, evaluation get-or-create, dataset add/seal, lease claim, and overlapping candidate paths.
3. Add SSE equal-length transition and hostile-identifier DOM tests.
4. Fix fail-open error handling, SQLite initialization, inspection-query enforcement, SSE hashing, inline event handlers, and evaluation races.

### Phase B — Establish seams behind compatibility facades

1. Introduce `ProjectWorkspace` and workspace coordination.
2. Split consumer-owned repository interfaces and transaction/unit-of-work operations.
3. Keep `Store` and `Spec` as delegating facades so MCP/CLI compatibility is preserved.
4. Define the MCP tool catalog once and require strict decoding.

### Phase C — Extract domain and application services

1. Move evaluation comparison/promotion policy out of persistence.
2. Extract session/destruction, context, execution/candidate, validation/evidence, completion, and evaluation services.
3. Decompose long transactional functions while preserving their outer transactions.
4. Introduce typed states, canonical payload value objects, parameter objects, strict converters, and an injected clock.

### Phase D — Operational query and UI cleanup

1. Create summary/detail SQLite queries and remove N+1/hard-cap scans.
2. Split dashboard handlers behind `newDashboardHandler(deps)`.
3. Standardize all `/api/v1` DTOs, errors, and pagination.
4. Extract the remaining JavaScript feature modules and delete legacy field fallbacks/globals.
5. Make the CLI a thin adapter and remove duplicate mutation paths.

### Phase E — Resource-model extensibility and cleanup

1. Introduce the resource descriptor/adapter seam while preserving current CUE syntax and DDD behavior.
2. Prove the seam with one DDD resource and one structured non-DDD asset/boundary resource before migrating the rest.
3. Remove dead compatibility paths only after call-site and database compatibility checks.
4. Split oversized tests and fixtures by subsystem, without changing the intentional combined crest-synth/phase fixture semantics.

## Summary

Code quality has fallen materially. The project has strong concepts and substantial test volume, but feature delivery has concentrated too much behavior in `Store`, `Spec`, MCP registration, dashboard handlers, and `app.js`. The most urgent issue is not line count: it is fail-open error suppression across destructive reconciliation, lifecycle verification, canonical persistence, and observability. The right response is an incremental safety-first refactor: characterize failures, repair the governance boundaries, introduce narrow ports and workspace/transaction seams behind compatibility facades, then split the large files along those real responsibilities. Splitting files before establishing those boundaries would mostly redistribute the same coupling.

## Implementation status

The first safety and decomposition pass is implemented on `refactor/safety-boundaries` in 19 phase-labelled commits (`f213542` through `d9736a0`). It changes 61 files relative to `main` and includes focused fault-injection, concurrency, hostile-input, and race coverage.

### Completed in this pass

- **Lifecycle and deletion safety:** destructive ownership failures, session seeding/finalization failures, wave persistence failures, command-launch failures, and completion projection failures no longer become successful lifecycle results.
- **SQLite governance:** connection-scoped settings are applied through the connection configuration; in-memory databases have an explicit shared policy; agent-facing inspection is context-aware, query-only, single-statement, and bounded by row, byte, and SQL-size limits.
- **Evaluation concurrency and provenance:** identity get-or-create, dataset ordinals, add/seal coordination, lease maintenance, and historical provenance reads now have deterministic transactional/error behavior.
- **Candidate governance:** project-root path resolution and symlink containment are centralized, overlapping candidate paths are coordinated, terminal replay converges, candidate-bearing terminal transitions are atomic in SQLite, and `CommitAttempt` is decomposed into explicit staging, target loading, rejection, and acceptance steps.
- **Observability and dashboard safety:** canonical read errors propagate, status/SSE snapshots fail closed, equal-length state changes are detected, inline JavaScript handlers were replaced with delegated listeners, and live-status handlers now depend on a narrow local port.
- **MCP contracts:** live and stub catalogs share one definition and all tools reject malformed, non-object, duplicate, unknown, and type-invalid arguments through the same strict decoding boundary.
- **Structural extraction:** pure evaluation analysis, learning/amendment repositories, session-resource/agent-event repositories, dashboard status handlers, context sources, completion indexing/derivation, candidate finalization, commit disposition, and the evaluation dashboard feature were moved behind named boundaries without changing public facades.

### Remaining high-value work

- Filesystem writes and SQLite transitions still cannot be truly atomic across a process crash. The new workspace coordinator prevents in-process overlap, but durable recovery/staging remains a separate design problem.
- `specStore` and MCP `specHandler` are still mega-interfaces. Only the dashboard status slice currently demonstrates the intended consumer-owned port pattern.
- `internal/store/executions.go`, `internal/spec/session.go`, `internal/mcp/tools.go`, and dashboard `app.js` remain oversized. Some central files became longer because fail-closed behavior and characterization tests were added before deeper extraction.
- Summary/detail observability queries, versioned dashboard DTO normalization, the remaining frontend feature modules, CLI thinning, and the resource-descriptor seam remain unimplemented.
- Candidate acceptance still treats completion reprojection and amendment marking as post-acceptance follow-up. Their recovery/idempotency semantics need an explicit policy before making those steps stricter.

### Current size checkpoints

| File | Before | After this pass |
|---|---:|---:|
| `internal/store/store.go` | 1,706 | 1,093 |
| `internal/store/executions.go` | 1,434 | 1,196 |
| `internal/store/evaluation_runs.go` | 1,031 | 868 |
| `internal/spec/execution.go` | 532 at extraction start | 438 |
| `internal/spec/context_manifest.go` | 577 at extraction start | 204 |
| `internal/spec/completion_projection.go` | 252 at extraction start | 78 |
| `cmd/crest-spec/dashboard.go` | 1,073 | 946 |
| `cmd/crest-spec/static/js/app.js` | 1,316 at feature extraction start | 1,118 |

These reductions are useful but not the definition of success. The more important result is that safety-critical failures now cross explicit boundaries and are covered by tests.

## Verification performed

- `go test ./... -count=1` passed after implementation, including MCP HTTP transport tests.
- `go test -race ./... -count=1` passed after implementation.
- `go vet ./...` passed after implementation.
- Every implementation phase was committed independently with a phase-labelled commit message.
