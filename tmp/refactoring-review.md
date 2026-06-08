# Refactoring Review: crest-spec codebase (full review)

**Date:** 2026-06-07
**Scope:** General codebase review (not a PR diff) — 7 domains, ~14k LOC production Go
**Domains reviewed:** spec-core, spec-ops, mcp-tools, mcp-server, persistence, engine-cue, support-cmd

## Executive Summary

The crest-spec codebase is in good overall architectural health: dependencies are mostly injected via interfaces, methods are decomposed into focused helpers, and dispatch tables are already used in several places. The dominant recurring theme across nearly every domain is **duplication that has crossed the Rule of Three** — the same unmarshal/dispatch, error-exit, list-mapping, progress-notification, and prompt-building shapes are re-spelled many times, creating shotgun-surgery hazards. The second pervasive theme is **stringly-typed type-code switches** (resource states, assertion/validation kinds, edge/resource kinds, job statuses, outcome codes) that are closed for extension and drift apart silently. A handful of genuine **DI/DIP gaps** remain where command execution, filesystem, and process/environment probing bypass already-injected abstractions, leaving validation/bootstrap/commit paths untestable. The highest-leverage fixes are: closing the command-runner / filesystem / environment DI gaps in `spec` and `engine`, collapsing the duplicated stub catalog and inline handlers in `mcp-tools`, extracting shared JSON-RPC/SSE/error-exit scaffolding, splitting the `Store` and `Spec` god-objects, and promoting the recurring magic strings to named constants. One latent correctness concern (`disallowedTools` is permanently nil yet documented as a security control) and one possible bug (`mergeMeta` drops `Meta.Mode`) are flagged for the owner.

## Cross-Cutting Themes

These patterns appear in **multiple domains**; each is noted once here and referenced from the per-domain sections only where domain-specific detail matters.

### 1. The MCP progress-notification payload is hand-built in 3+ places
The `map[string]any{"progressToken","progress","total":100,"message"}` shape is re-spelled inline in **mcp-tools** (`tools.go:710-723`, `progressSender`) and twice in **mcp-server** (`server.go:436-443` started, `server.go:475-486` finished). A typed `progressNotification` struct + a single `emitProgress(token, progress, message)` helper (no-op when token is empty) should own the shape and the `total=100` constant for all call sites.

### 2. Stringly-typed type-code switches / Primitive Obsession (every domain)
The same OCP/type-code smell recurs:
- **spec-core / spec-ops:** `ResourceState` bucketing duplicated and divergent across `session.go`, `observability.go`, `dispatch.go`; `Apply.Outcome` and review levels (`"light"/"full"/...`) as bare strings.
- **mcp-tools:** terminal job-status strings (`"completed"/"failed"/"cancelled"/...`) inline; recursion process-name substrings.
- **engine-cue:** `Edge.Kind` and `Resource.Kind` free-form strings.
- **support-cmd:** subcommand dispatch switch, `resource.Kind == "asset"` branch.
- **persistence:** `strings.Contains(err, "UNIQUE constraint failed")` driver-text matching.

Recommendation across the board: promote each closed set to named string-constant groups (or typed enums with a single classification method), and prefer registry/map dispatch over `switch`.

### 3. Duplicated unmarshal → dispatch → error-construct scaffolding (mcp-tools, mcp-server)
- **mcp-tools:** inline anonymous handlers re-implement the unmarshal+dispatch pattern the generic `specTool`/`specToolStrict` helpers exist to eliminate; `"invalid arguments: "` repeated ~12 times.
- **mcp-server:** `handleLine` (stdio) and `ServeHTTP` (HTTP) duplicate JSON-RPC unmarshal + `dispatch` lookup + `-32700`/`-32601` error construction, and have already diverged.

Recommendation: extract shared resolver/handler helpers and an `invalidArgs(err)` helper; name the JSON-RPC error codes.

### 4. God objects / Large Classes with Divergent Change (spec-core, persistence)
- **spec-core:** `Spec` hosts session lifecycle + wave dispatch + observability + planning.
- **persistence:** `Store` is a single ~1380-line type with 50+ methods over 13 aggregates and no interface.

Both should be split along their existing section seams into per-responsibility collaborators sharing the same store/engine/fs, each behind a narrow interface (ISP/DIP).

### 5. "Print to stderr then exit" / boilerplate envelopes that ignore existing helpers (support-cmd)
A `fatal` helper already exists but `cli.go`/`main.go` open-code the 3-line exit a dozen-plus times; dashboard read-handlers and SSE handlers repeat their scaffolding. Same family of duplication as themes 1 and 3.

### 6. Swallowed errors (spec-ops, persistence, spec-core)
Store mutations (`SetNote`, `UpdateSessionResourceState`, `CreateApplyAction`, `SetGeneratedFile`) discard returns; invariant checks `continue` on engine error (treated as pass); `parseTime`/`parseOptionalTime` swallow parse errors. Make best-effort intent explicit, or capture and wrap/log.

## Critical Issues

Only **spec-ops** reported critical issues; all other domains reported none.

### spec-ops

| Title | Location | Smell | Problem | Recommendation |
|---|---|---|---|---|
| validate.go bypasses the injected fileSystem abstraction | `internal/spec/validate.go:71-83,130-134` | DI / DIP violation | `CheckAssertions` calls `os.Stat`/`os.ReadFile` directly and `RunCommand`/`RunValidations` create processes via `exec.CommandContext` (line 26). `Spec` already injects a `fileSystem` (spec.go:71, fs.go:8), but these are package-level functions that cannot see `s.fs`, so file/exec access leaked out of the abstraction boundary — untestable. | Make `CheckAssertions`/`RunValidations` methods on `*Spec` (or a `Validator` constructed with the fileSystem + a `CommandRunner` interface) so I/O routes through `s.fs`; inject a `CommandRunner` so `exec` is behind an abstraction. |
| bootstrap.go reaches directly to os/exec | `internal/spec/bootstrap.go:76-82,86-103` | DI / DIP violation | `bootstrapClaudeCLI` calls `exec.LookPath`, `bootstrapMCPConfig` calls `os.Executable`, `claudeConfigPath` calls `os.UserHomeDir` directly while file I/O correctly goes through `s.fs` — inconsistent. Makes `Bootstrap` depend on real PATH/home/executable, untestable. | Introduce an injected `Environment`/`SystemProbe` interface (`LookPath`, `Executable`, `UserHomeDir`) on `Spec`, OS-backed default in `New`, mirroring `fileSystem`. |
| Type-code switches on assertion/validation kind (OCP) | `internal/spec/validate.go:54-106,139-189` | OO Abuser: switch on type code / OCP | `CheckAssertions` switches on 7 string `a.Kind` cases; `RunValidations` switches on `v.Kind`. New kinds force edits to existing functions. `RunValidations` also triplicates the `"failed (exit %d): stdout/stderr"` message. | Replace with registries: `map[string]AssertionChecker` / `map[string]ValidationRunner`; extract one `failureMessage(kind, exitCode, stdout, stderr)` helper. |

## Refactoring Opportunities

### spec-core

| Title | Location | Smell | Problem (condensed) | Recommendation |
|---|---|---|---|---|
| Invariant checking duplicated | `session.go:694-735`, `loop.go:257-304` | Duplicate Code | `checkInvariants` and `runInvariantStep`+`checkInvariant` build the SAME review prompt and PASS/FAIL rule; wording has drifted (`"or FAIL with explanation if it violates it."` vs `"or FAIL with explanation."`). | Extract `buildInvariantPrompt`/`evaluateInvariant` (+ code-blob helpers); have `session.go` delegate to the `loop.go` checker so there is one prompt and one rule. |
| Command/validation execution not injected | `session.go:445,663`; `validate.go:21,130`; `spec.go:93-94` | DIP / hidden I/O coupling | `runCommitValidations`/`runVerificationCommand` call package-level `RunValidations`/`RunCommand` (exec); `Plan` calls `cuepkg.Load` directly (fs). Commit/VerifyWave/Plan untestable without subprocesses/CUE files. | Introduce a `commandRunner` and `specLoader` interface as injected `Spec` fields (OS-backed defaults), mirroring `fileSystem`. *(Overlaps spec-ops critical DI gap.)* |
| "mark resource errored" block repeated 5+ times | `dispatch.go:137-139,161-163,191-194`; `session.go:604-610`; `dispatch.go:380-386` | Duplicate Code | `UpdateSessionResourceState(...StateErrored...)` and the GetSessionResource-then-update shape repeated verbatim. | Add `s.markErrored(...)` / `s.markErroredFromExisting(...)` helpers preserving `LastOutput`/`JobID`. |
| State-counting switch duplicated in 3 files | `session.go:553-562`, `observability.go:96-107`, `dispatch.go:360-372` | Shotgun Surgery / type-code switch | Three ladders map `ResourceState`→buckets and disagree subtly (Rejected/TimedOut/Blocked handled differently per site). | Give `ResourceState` a single `Category()` method in `state.go`; reconcile the intentional differences explicitly. |
| Spec is a god object | `spec.go:68-82`; methods across `session.go`, `dispatch.go`, `observability.go` | Large Class / Divergent Change | `Spec` owns Begin/Commit/Dispatch/RunWave/VerifyWave/SessionStatus/Plan — unrelated reasons to change. | Split into `SessionService`, `Dispatcher`, `Observer/StatusReader` sharing store/engine/fs; methods already delegate, so the move is low-risk. |

### spec-ops

| Title | Location | Smell | Problem (condensed) | Recommendation |
|---|---|---|---|---|
| importer.go Large Class | `internal/spec/importer.go:148-375` | Bloater / SRP | Mixes scan (`collectSourceFiles`), classify (`classifyFiles`), and CUE serialization (`generateCUE`+`write*Map`) — three reasons to change. | Split into `sourceScanner`, `classifier`, `cueWriter` (the latter owning the builder/indent). |
| classifyByName fragile ordered chain | `importer.go:167-194` | type-code switch / OCP | `switch-true` ladder of `strings.Contains` whose ORDER encodes precedence; reordering silently reclassifies. | Model as an ordered slice of `{match, kind}` rules with explicit precedence. |
| Parallel resource-kind maps in contextData | `importer.go:210-296` | Shotgun Surgery / Duplicate Code | 7 near-identical map fields each init'd, populated by a switch case, and written by an almost-identical `writeResourceMap`; new kind = 4 coordinated edits. | Replace with `map[resourceKind]map[string]classifiedFile` or section descriptors; iterate once. |
| Resolve/Amend/Skip ignore store errors | `resolve.go:26-27,30-33,93-110,124-138`; also `query.go:93` | Swallowed errors | Many store mutations discard their error return; failed audit/state writes silently "succeed." | Capture and wrap/return or log; make best-effort intent explicit with a named, logged `_`. |
| Apply loop dispatches on string outcome codes | `apply.go:69-76` | Primitive Obsession / type-code switch | Switches on `Outcome` string literals while the codebase elsewhere uses typed `ResourceState`; a new/typo'd status silently falls into `Errored` default. | Introduce a typed `Outcome` (or reuse `ResourceState`) with named constants. |

### mcp-tools

| Title | Location | Smell | Problem (condensed) | Recommendation |
|---|---|---|---|---|
| registerSpecStubs duplicates the entire real tool catalog | `tools.go:308-350` (stubs) vs `513-829` (real) | Duplicate Code / Shotgun Surgery | ~33 tool names+schemas copy-pasted; already drifted (e.g. spec/apply description, spec/state action docs). | Drive both real and stub registration from a single `toolDef` table; swap handler for a stub closure when `s.spec == nil`. |
| Inline anonymous handlers duplicate unmarshal-dispatch | `tools.go:57-95`, `152-229` | Duplicate Code / Inconsistent Abstraction | `code_review`/`bugbot` and the job tools embed ~30-line closures repeating the unmarshal+dispatch shape the generic helpers solve. | Extract named methods (`handleCodeReview`, etc.); add a generic `asyncTool[A]` routing through `runAsync`. |
| specTool variants swallow json.Unmarshal errors inconsistently | `tools.go:358-396`; handlers at `870,884,926,948` | Inconsistent error handling | `specTool`/`specToolErr` discard the unmarshal error; `specToolStrict` checks it — malformed JSON silently proceeds with zero-value structs. | Make all variants strict; delete the now-redundant `specToolStrict`; apply the guard to the 4 hand-written handlers. |
| Resources/prompts read are URI/name switch dispatchers | `handlers.go:142-175`, `217-257` | OCP / Divergent Change | Adding a resource/prompt needs two synchronized edits (List array + Read switch). | Register in a map keyed by URI/name → `reader func(ctx)(any,error)`, mirroring the existing `toolFns` table. |
| progressSender builds notification payload inline | `tools.go:710-723` | Feature Envy / Primitive Obsession | Hand-marshals the MCP progress payload + double-encodes update as a string. | Typed `progressNotification` struct + `writeNotification` accepting it. *(See Cross-Cutting #1.)* |

### mcp-server

| Title | Location | Smell | Problem (condensed) | Recommendation |
|---|---|---|---|---|
| Duplicated JSON-RPC decode + dispatch across transports | `server.go:278-311`, `373-408` | Duplicate Code / Shotgun Surgery | `handleLine` and `ServeHTTP` duplicate unmarshal + `dispatch` lookup + `-32700`/`-32601` errors; already diverged (HTTP omits notification/WaitGroup handling). | Extract `resolveHandler(req)` returning handler or ready error response; name the error codes. |
| runAsync long method (5 responsibilities) | `server.go:414-490` | Long Method / SRP | Cancel-registry bookkeeping (duplicated happy/error), job-store create, started notification, fn+metrics, terminal mapping. | Extract `registerCancel`/`unregisterCancel`, `finalizeJob`, `emitProgress`. |
| Two near-identical progress notifications | `server.go:436-443`, `475-486` | Duplicate Code | Same payload shape + `total:100` + `if token != ""` guard repeated. | `s.emitProgress(token, progress, message)`. *(See Cross-Cutting #1.)* |

### persistence

| Title | Location | Smell | Problem (condensed) | Recommendation |
|---|---|---|---|---|
| Store god object (13 domains) | `store.go:139-1381` | Large Class / SRP / Divergent Change | 50+ methods over Jobs/Locks/Resources/.../AgentEvents; any new domain edits this one file. | Split into per-domain stores sharing `*db.Queries`; thin `Store` facade; section banners mark the seams. |
| Store exposes no interface | `store.go:141-144` | DIP / ISP | Consumers couple to a 50-method concretion; cannot mock without a real SQLite DB. | Introduce narrow interfaces (`JobReader`/`JobWriter`, `ResourceStore`, `SessionStore`); consumers accept the slice they need. |
| Two redundant DB paths; raw SQL bypasses sqlc | `store.go:142-143,182-239,1302-1329,1338-1381` | Inappropriate Intimacy / Redundant Coupling | `Store` holds both `sqlDB` and `queries`; `migrate`/`Vacuum`/`ReadOnlyQuery` run hand-written SQL that duplicates schema knowledge (7 hard-coded tables in Vacuum) that rots silently on rename. | Move Vacuum DELETEs into sqlc query files; confine raw `*sql.DB` to dynamic SELECT + DDL in a separate small type. |
| List-mapping loop repeated ~15 times | `store.go` (15 sites, e.g. `374-384`...`1283-1293`) | Duplicate Code | Identical `make+for+convert` shape per `List*`. | Generic `mapRows[S,D](rows []S, conv func(S) D) []D`. |
| Empty-string→nullable-pointer duplicated inline | `store.go:544-552,727-733,784-788,802-814,936-943,978-986,1238-1243` | Duplicate Code / Primitive Obsession | `if x != "" { p = &x }` reinvented inline though `stringPtr` already exists at `1081`. | Replace all inline guards with `stringPtr`. |

### engine-cue

| Title | Location | Smell | Problem (condensed) | Recommendation |
|---|---|---|---|---|
| Misleading empty disallowedTools var | `engine.go:64-98,121-139,215-221` | Dead Code / misleading comments | `var disallowedTools []string` is never assigned (always nil), yet docs claim "all filesystem and web tools are blocked"; agent.go only appends when non-empty, so nothing is blocked. **Latent correctness/security concern.** | Populate with real blocked tool names (or source from config), or remove the var and the false comments. Do not leave a nil slice masquerading as a security control. |
| Repeated RunOpts + semaphore guard | `engine.go:88-95,131-136,215-221` (+ guards `78-81,111-114,198-201`) | Duplicate Code | Same `RunOpts{DisallowedTools, NoSessionPersistence:true}` and acquire/release across Generate/Review/fanOut. | `baseRunOpts(prompt, model)` helper; optional `withSlot(ctx, fn)` for the semaphore. |
| Registry is a fixed pipeline of register* steps | `registry.go:34-51,53-294` | Divergent Change / OCP / Large Class | New resource kind = edit `NewRegistry` + add a method; 10 methods share a near-identical id/meta/edges/store tail. | Iterate a slice of registrar funcs/values; factor the common tail into one helper. |
| Three near-identical Flex* UnmarshalJSON | `types.go:10-83` | Duplicate Code | `FlexMap`/`FlexInvariants`/`FlexContextMap` all do "try form A else form B + flatten." | Generic helper taking two target pointers + a flatten closure. |
| Edge.Kind / Resource.Kind stringly-typed | `registry.go:8-22,311-344`; `types.go:220-245` | Primitive Obsession | Free-form kind strings scattered through edge builders + register methods; valid set only in a comment. | `type EdgeKind string` / `type ResourceKind string` with named constants. |

### support-cmd

| Title | Location | Smell | Problem (condensed) | Recommendation |
|---|---|---|---|---|
| Duplicated read-one-store-method HTTP handlers | `dashboard.go:258-342` | Duplicate Code / Shotgun Surgery | ~7 handlers repeat read-PathValue → one store call → writeError(500)/writeJSON. | `serveJSON(w, func()(any,error))` or `respond(w, data, err)` helper. |
| Duplicated SSE streaming scaffolding | `dashboard.go:392-438,440-501` | Duplicate Code | Both stream handlers repeat headers + flusher assert + ticker + select loop. | `streamSSE(w, r, interval, send)` helper; consolidate de-dup bookkeeping. |
| buildDomainPrompt long method | `internal/prompt/resource.go:18-120` | Long Method / SRP | ~100 lines assembling header/decl/meta/aggregate/port/flow/deps with nested loops. | Extract `writeHeader`/`writeDeclaration`/`writeMeta`/`writeAggregate`/`writePortContract`/`writeDependencies`; orchestrator lists them (mirrors review.go). |
| Repeated stderr-print-then-exit | `cli.go:228-414`, `main.go` | Duplicate Code / Divergent Change | `fatal` helper exists (dashboard.go:503) but 12+ sites open-code `Fprintf(os.Stderr,...);os.Exit(1)`. | Standardize on `fatalf(format, args...)`; route all CLI exits through it. |
| openStore CLI commands share run/error/exit skeleton | `cli.go:235-415` | Duplicate Code | `cmdStateList/Rm/Diff/Vacuum/SQL` copy open/defer-Close/error-exit envelope. | After `fatalf`, add `withStore(func(*store.Store) error)` wrapper. |
| runSubcommand dispatch closed for extension | `main.go:73-119` | OCP / inconsistent control flow | New subcommand edits the switch; cases return inconsistently; manual `os.Args` index handling. | `map[string]func(args []string)` registry; lookup + invoke; each command owns its arg validation. |
| Two alternative prompt builders by kind type-code | `resource.go:11-16,122-190` | Switch on type code / Alternative Classes | `BuildResourcePrompt` branches `Kind=="asset"`; both builders duplicate header/ID/dependency-JSON emission. | Extract `writeJSONDepBlock`; strategy map keyed by kind. |

## Minor Suggestions

**spec-core**
- Large instruction string literals (`orchestratorInstructions`, `dispatchInstructions`) baked into `session.go:789-860` — move to `instructions.go`/`embed`.
- `checkInvariant`/`checkInvariants` swallow engine errors (`loop.go:269-271`, `session.go:719-721`) — `continue` treats engine failure as pass; decide the policy once in the shared checker.
- Hardcoded `ReviewLevel: "light"` magic string at `dispatch.go:230` — promote review levels to named constants.
- `promptHash` (`loop.go:321-323`) and inline `sha256` idiom in `session.go` repeated 4+ times — add `hashHex([]byte) string`.
- `max := s.engine.MaxConcurrency()` shadows builtin `max` at `observability.go:64` — rename to `maxConc`.

**spec-ops**
- Magic default limit `20` duplicated (`query.go:42,49`) — extract `defaultHistoryLimit`.
- `fmt.Sprintf` with no verbs at `importer.go:372` — use plain `WriteString`; vet/lint flags it.
- Hardcoded cwd `"."` in `ValidateResource` (`validate.go:144-145`, `query.go:144`) — pass project/spec dir from cfg.
- `reviewResource` swallows per-resource review errors (`review.go:40-47`) — accumulate failed IDs.
- `DriftAction` "revert" unimplemented (`query.go:104-105`) — track the dead branch.

**mcp-tools**
- Magic `6`/`SELECT` length in spec/sql guard (`tools.go:973-976`) — `const selectKw`.
- Terminal job-status strings inline (`tools.go:173`) — `isTerminal(status)` / constants.
- Spec-workflow guide duplicated as prose in `handleAbout` and `handleInitialize` (`tools.go:283-304`, `handlers.go:27-54`) — single package-level const.
- `DetectRecursion` magic threshold + process substrings (`recursion.go:25-33`) — named consts (DIP otherwise good).
- `"invalid arguments: "` repeated ~12 times — `invalidArgs(err)` helper.

**mcp-server**
- `recursion` field is a Temporary Field (`server.go:182,215-218`) — make it a local in `New()`.
- Magic JSON-RPC codes `-32700`/`-32601` at 4 sites — `codeParseError`/`codeMethodNotFound`.
- `Metrics.Record` min/max CAS loops duplicated (`metrics.go:78-90`) — `storeMin`/`storeMax`.
- `store` consumer interface is ~22 methods wide (`server.go:41-59`) — split into role interfaces (ISP), low priority.

**persistence**
- `context.Background()` hard-coded in every query call (pervasive) — thread `context.Context` for cancellation.
- `parseTime` swallows parse errors (`store.go:261-264`) — document or log.
- `UpdateGeneration` 8-param signature (`store.go:802`) — pass a `GenerationResult` struct.
- Magic constraint-violation substrings (`store.go:456-457`) — `isUniqueViolation(err)` helper.
- `ReadOnlyQuery` shallow 6-char SELECT guard (`store.go:1338-1342`) — document limitation or use a read-only connection.

**engine-cue**
- `fanOut` output order nondeterministic (`engine.go:203-242`) — pre-size `results` and write by index to drop the mutex.
- `fanOut` doc comment duplicates `CodeReview` verbatim (`engine.go:153-162,195-243`).
- **`mergeMeta` does not merge `Meta.Mode`** (`registry.go:346-372`; `types.go:114`) — child Mode silently dropped. **Flag to owner as a possible bug.**
- `Load` only inspects `instances[0]` (`loader.go:22-37`) — document the single-instance assumption.

**support-cmd**
- `dbPath()` has a hidden `os.MkdirAll` side effect (`main.go:211-215`) — rename `ensureDBPath()` or split.
- `RunOpts` 15-field parameter object (`agent.go:18-34`); `buildArgs` grows per field — consider flag-builder slice.
- Magic stdin threshold `8192` (`agent.go:279`) — `const maxArgPromptBytes`.
- Recent-generations handler builds SQL via `fmt.Sprintf` (`dashboard.go:352-356`) — limit is validated but prefer a parameterized store method.
- Inline anonymous response structs duplicated (`dashboard.go:128-147,198-227,244-247`) — name the stable payload types.
- `InjectRuntimeContext` repeats sorted-keys map-section pattern (`context.go:25-57`) — extract `writeMapSection` if a fourth section appears.

## Per-Domain Summaries

**spec-core** — The orchestration package is generally well-structured: `Spec` depends on injected interfaces (specEngine, specStore, fileSystem), methods decompose into focused helpers, and the constraint loop is cleanly staged. The most significant issues are (1) genuine duplicate code — invariant checking and its PASS/FAIL prompt implemented twice (session.go and loop.go) with near-identical wording, and (2) a DI gap where validation/command execution and `cuepkg.Load` are called as package-level functions rather than injected behind interfaces, so several `Spec` methods can't be tested without real subprocesses. Secondary smells: repeated "errored" update blocks, repeated state-counting switches across three files, and a `Spec` god-object (Divergent Change). The duplication and the command-runner DI gap are the two worth acting on.

**spec-ops** — Mostly thin, well-factored orchestration methods on `Spec`, which correctly injects engine/store/fs. Dominant problems: (1) DI violations where validate.go and bootstrap.go bypass the injected fileSystem and reach for OS/process directly, making those paths untestable; (2) recurring type-code switches (CheckAssertions, RunValidations, classifyByName, generateCUE) that must be edited to add a case, with duplicated failure-message construction; (3) several swallowed errors that hide failures. importer.go is a Large Class mixing scanning, classification, and CUE serialization with shotgun-surgery-prone parallel structures. These are refactoring opportunities, not behavioral bugs.

**mcp-tools** — Reasonably well-factored — generic `specTool` helpers remove most registration boilerplate and registration is split into cohesive groups. Remaining problems: (1) duplicated inline anonymous handlers that re-implement the unmarshal+dispatch pattern; (2) `registerSpecStubs` duplicates the entire ~33-tool catalog, a shotgun-surgery hazard where schemas have already drifted; (3) the three `specTool` variants swallow `json.Unmarshal` errors in two of three cases; (4) minor redundant coupling and primitive obsession. The stub duplication and inline-handler duplication are the real hazards.

**mcp-server** — In good architectural shape for DI/SOLID: `Server` depends on injected abstractions, and the one self-instantiated collaborator (`NewMetrics()`) is a pure in-memory counter, not a DI violation. metrics.go is clean. Refactoring value concentrates in server.go: the stdio and HTTP transports duplicate JSON-RPC decode + dispatch + error construction, and `runAsync` is a long method interleaving cancel-registry bookkeeping (itself duplicated), job-store transitions, metrics, and two near-identical progress notifications. Extracting shared helpers and naming the error-code magic numbers removes the duplication without changing behavior.

**persistence** — `store.go` is a single ~1380-line file wrapping sqlc's `db.Queries` over ~13 aggregates. The wrapping is mechanically clean but has two structural problems: (1) `Store` is a god-object with 50+ methods (SRP/Divergent Change), and (2) it holds two redundant DB paths (`sqlDB` and `queries`) and bypasses sqlc with hand-written raw SQL in migrate/Vacuum/ReadOnlyQuery (Inappropriate Intimacy with the schema). No strict DI violation (the only concretion is `*sql.DB` inside the `New` factory), but `Store` is a concrete type with no interface, so every consumer depends on a 50-method concretion. Remaining items are pervasive-but-low-severity converter/pointer-mapping duplication.

**engine-cue** — Generally clean, small, and well-factored — functions are short and `Engine`'s dependencies are properly injected via the `runner` interface and `config.Config`. No critical DI/SOLID violations. The most actionable issue is the misleading `disallowedTools` package variable: permanently nil yet documented as blocking filesystem/web tools, so a stated safety constraint is silently not enforced. Secondary opportunities are duplication-driven: three copy-pasted Flex* UnmarshalJSON coercions, repeated RunOpts/semaphore boilerplate, and ten structurally identical register* methods (OCP). Stringly-typed Edge/Resource Kind codes and a latent gap where `Meta.Mode` is not merged in `mergeMeta` round out the findings.

**support-cmd** — Generally clean, small, well-factored; prompt/, graph/, plan/, config/, and errors/ packages mostly do one thing each, and DI is respected (Agent consumed via a RunPrompt interface; Planner depends on planStore/fileReader abstractions). No critical issues. The strongest opportunities concentrate in cmd/crest-spec: dashboard.go has substantial duplicate-code smell across its ~10 read-one-store-method handlers and two SSE handlers, and the CLI repeats a print-to-stderr-then-exit envelope a dozen-plus times despite an existing `fatal` helper, plus a closed-for-extension subcommand switch. In prompt/, `buildDomainPrompt` is a long multi-section method and `BuildResourcePrompt` selects between two alternative builders via a kind type-code with duplicated JSON-block emission.
