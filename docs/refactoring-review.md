# Refactoring Review — crest-spec Codebase

**Date:** 2026-06-07
**Scope:** Full codebase review (not a PR diff) — ~14k LOC of production Go
**Method:** 7 parallel domain reviewers (one per cohesive package group) evaluating against the
refactoring.guru smell catalog, SOLID, dependency-injection rules, clean-code, and design-pattern fit,
followed by a synthesis pass.
**Reviewer focus:** structural improvements that **do not change behavior**. Two behavioral concerns
surfaced incidentally and are flagged separately as bugs.

| Domain | Packages / files |
|---|---|
| spec-core | `internal/spec`: session.go, loop.go, dispatch.go, state.go, spec.go, fs.go, runtime.go, observability.go |
| spec-ops | `internal/spec`: importer.go, query.go, validate.go, review.go, resolve.go, bootstrap.go, apply.go, parse.go, errorparse.go |
| mcp-tools | `internal/mcp`: tools.go, handlers.go, process.go, recursion.go |
| mcp-server | `internal/mcp`: server.go, metrics.go |
| persistence | `internal/store/store.go`, `internal/db` (db/*.sql.go treated as sqlc-generated, not reviewed for smells) |
| engine-cue | `internal/engine`, `internal/cue` |
| support-cmd | `internal/prompt`, `internal/agent`, `internal/graph`, `internal/plan`, `internal/config`, `internal/errors`, `cmd/crest-spec` |

---

## 1. Executive Summary

The crest-spec codebase is in **good overall architectural health**. Dependencies are mostly injected
via interfaces (`specEngine`, `specStore`, `fileSystem`, `runner`, `RunPrompt`), methods decompose into
focused helpers, and dispatch tables are already used in several places. Of seven domains, only **spec-ops**
reported issues severe enough to call critical, and those are dependency-injection/OCP gaps — not behavioral
defects.

Two themes recur across nearly every domain:

1. **Duplication that has crossed the Rule of Three.** The same unmarshal→dispatch, error-exit,
   list-mapping, progress-notification, and prompt-building shapes are re-spelled many times. Several copies
   have already drifted apart — a classic shotgun-surgery hazard.
2. **Stringly-typed type-code switches (Primitive Obsession + OCP).** Resource states, assertion/validation
   kinds, edge/resource kinds, job statuses, and apply outcomes are bare strings switched on in multiple
   places, closed for extension and drifting silently.

A handful of genuine **DI/DIP gaps** remain where command execution, filesystem access, and process/environment
probing bypass already-injected abstractions, leaving validation, bootstrap, and commit paths untestable.

**Highest-leverage fixes (in priority order):**

1. Close the command-runner / filesystem / environment DI gaps in `spec` and `engine`.
2. Collapse the duplicated stub catalog and inline handlers in `mcp-tools`.
3. Extract shared JSON-RPC / SSE / progress / error-exit scaffolding.
4. Split the `Store` and `Spec` god-objects behind narrow interfaces.
5. Promote recurring magic strings to named constants / typed enums.

Two items are flagged to the owner as **potential correctness bugs**, not refactors:
`disallowedTools` is permanently nil yet documented as a security control, and `mergeMeta` silently drops
`Meta.Mode`. See Section 6.

---

## 2. Behavioral Flags (review these first)

These are not refactoring items — they may change runtime behavior and should be triaged before any cleanup.

### 2.1 `disallowedTools` is a no-op security control
**`internal/engine/engine.go:64-98,121-139,215-221`**

`var disallowedTools []string` is declared and never assigned (always `nil`). It is threaded into every
`RunOpts{DisallowedTools: disallowedTools}` and the surrounding doc comments claim "all filesystem and web
tools are blocked." In `agent.go` the disallow flag is only appended when the slice is non-empty, so **nothing
is blocked**. A stated safety constraint is silently unenforced.

**Action:** populate the slice with real blocked tool names (or source it from config), or remove the variable
and the false comments. Do not leave a nil slice masquerading as a security control.

### 2.2 `mergeMeta` drops `Meta.Mode`
**`internal/engine/registry.go:346-372` (field at `types.go:114`)**

`mergeMeta` merges child meta into parent but never copies `Meta.Mode`. A child's `Mode` value is silently
discarded.

**Action:** confirm whether `Mode` should propagate; if so, add it to the merge. If the omission is
intentional, document why.

---

## 3. Cross-Cutting Themes

These patterns appear in multiple domains. Each is described once here; per-domain sections reference them.

### Theme A — MCP progress-notification payload hand-built in 3+ places
The `map[string]any{"progressToken", "progress", "total":100, "message"}` shape is re-spelled inline in
`mcp-tools` (`tools.go:710-723`, `progressSender`) and twice in `mcp-server`
(`server.go:436-443` started, `server.go:475-486` finished).
**Fix:** a typed `progressNotification` struct plus a single `emitProgress(token, progress, message)` helper
(no-op when token is empty) owning the shape and the `total=100` constant.

### Theme B — Stringly-typed type-code switches (every domain)
- **spec-core / spec-ops:** `ResourceState` bucketing duplicated and divergent across `session.go`,
  `observability.go`, `dispatch.go`; `Apply.Outcome` and review levels (`"light"/"full"`) as bare strings.
- **mcp-tools:** terminal job-status strings (`"completed"/"failed"/"cancelled"`) inline; recursion
  process-name substrings.
- **engine-cue:** `Edge.Kind` and `Resource.Kind` free-form strings.
- **support-cmd:** subcommand dispatch switch; `resource.Kind == "asset"` branch.
- **persistence:** `strings.Contains(err, "UNIQUE constraint failed")` driver-text matching.

**Fix:** promote each closed set to named string-constant groups (or typed enums with a single classification
method) and prefer registry/map dispatch over `switch`.

### Theme C — Duplicated unmarshal → dispatch → error-construct scaffolding (mcp-tools, mcp-server)
- **mcp-tools:** inline anonymous handlers re-implement the unmarshal+dispatch pattern that the generic
  `specTool`/`specToolStrict` helpers exist to eliminate; `"invalid arguments: "` repeated ~12 times.
- **mcp-server:** `handleLine` (stdio) and `ServeHTTP` (HTTP) duplicate JSON-RPC unmarshal + `dispatch`
  lookup + `-32700`/`-32601` error construction — and have already diverged (HTTP omits notification/WaitGroup
  handling).

**Fix:** extract a shared `resolveHandler(req)` and an `invalidArgs(err)` helper; name the JSON-RPC error codes.

### Theme D — God objects / Large Classes with Divergent Change (spec-core, persistence)
- **spec-core:** `Spec` hosts session lifecycle + wave dispatch + observability + planning.
- **persistence:** `Store` is a single ~1380-line type with 50+ methods over 13 aggregates and no interface.

**Fix:** split along the existing section seams into per-responsibility collaborators sharing the same
store/engine/fs, each behind a narrow interface (ISP/DIP). Methods already delegate, so the move is low-risk.

### Theme E — "Print to stderr then exit" / boilerplate envelopes that ignore existing helpers (support-cmd)
A `fatal` helper already exists (`dashboard.go:503`) but `cli.go`/`main.go` open-code the 3-line exit a
dozen-plus times; dashboard read-handlers and SSE handlers repeat their scaffolding.

### Theme F — Swallowed errors (spec-ops, persistence, spec-core)
Store mutations (`SetNote`, `UpdateSessionResourceState`, `CreateApplyAction`, `SetGeneratedFile`) discard
returns; invariant checks `continue` on engine error (treated as a pass); `parseTime`/`parseOptionalTime`
swallow parse errors.
**Fix:** make best-effort intent explicit (named, logged `_`), or capture and wrap/log.

---

## 4. Critical Issues

Only **spec-ops** reported critical issues; all other domains reported none.

### C1 — `validate.go` bypasses the injected `fileSystem` abstraction
**`internal/spec/validate.go:71-83,130-134` (exec at line 26)** — DI / DIP violation

`CheckAssertions` calls `os.Stat`/`os.ReadFile` directly, and `RunCommand`/`RunValidations` create processes
via `exec.CommandContext`. `Spec` already injects a `fileSystem` (`spec.go:71`, `fs.go:8`), but these are
package-level functions that cannot see `s.fs`, so file/exec access has leaked out of the abstraction boundary.
The paths are untestable without touching the real disk and spawning subprocesses.

**Fix:** make `CheckAssertions`/`RunValidations` methods on `*Spec` (or a `Validator` constructed with the
fileSystem + a `CommandRunner` interface) so file I/O routes through `s.fs`; inject a `CommandRunner` so `exec`
sits behind an abstraction.

### C2 — `bootstrap.go` reaches directly to `os`/`exec`
**`internal/spec/bootstrap.go:76-82,86-103`** — DI / DIP violation

`bootstrapClaudeCLI` calls `exec.LookPath`, `bootstrapMCPConfig` calls `os.Executable`, and `claudeConfigPath`
calls `os.UserHomeDir` directly, while file I/O in the same file correctly goes through `s.fs`. The
inconsistency makes `Bootstrap` depend on the real PATH/home/executable and untestable.

**Fix:** introduce an injected `Environment`/`SystemProbe` interface (`LookPath`, `Executable`,
`UserHomeDir`) on `Spec`, with an OS-backed default in `New`, mirroring `fileSystem`.

### C3 — Type-code switches on assertion/validation kind
**`internal/spec/validate.go:54-106,139-189`** — OO Abuser (switch on type code) / OCP

`CheckAssertions` switches on 7 string `a.Kind` cases; `RunValidations` switches on `v.Kind`. Adding a kind
forces edits to existing functions. `RunValidations` also triplicates the
`"failed (exit %d): stdout/stderr"` message.

**Fix:** replace with registries — `map[string]AssertionChecker` / `map[string]ValidationRunner`; extract one
`failureMessage(kind, exitCode, stdout, stderr)` helper.

---

## 5. Refactoring Opportunities by Domain

### spec-core
| Title | Location | Smell | Recommendation |
|---|---|---|---|
| Invariant checking duplicated | `session.go:694-735`, `loop.go:257-304` | Duplicate Code | Extract `buildInvariantPrompt`/`evaluateInvariant`; have session.go delegate to the loop.go checker so there is one prompt and one PASS/FAIL rule (wording has already drifted). |
| Command/validation execution not injected | `session.go:445,663`; `validate.go:21,130`; `spec.go:93-94` | DIP / hidden I/O | Introduce a `commandRunner` and `specLoader` interface as injected `Spec` fields (OS-backed defaults). Overlaps C1. |
| "mark resource errored" block repeated 5+ times | `dispatch.go:137-139,161-163,191-194`; `session.go:604-610`; `dispatch.go:380-386` | Duplicate Code | Add `s.markErrored(...)` / `s.markErroredFromExisting(...)` helpers preserving `LastOutput`/`JobID`. |
| State-counting switch duplicated in 3 files | `session.go:553-562`, `observability.go:96-107`, `dispatch.go:360-372` | Shotgun Surgery / type-code switch | Give `ResourceState` a single `Category()` method in `state.go`; reconcile the intentional Rejected/TimedOut/Blocked differences explicitly. |
| `Spec` is a god object | `spec.go:68-82`; methods across session/dispatch/observability | Large Class / Divergent Change | Split into `SessionService`, `Dispatcher`, `Observer/StatusReader` sharing store/engine/fs. |

### spec-ops
| Title | Location | Smell | Recommendation |
|---|---|---|---|
| `importer.go` Large Class | `importer.go:148-375` | Bloater / SRP | Split into `sourceScanner`, `classifier`, `cueWriter` (the latter owning the builder/indent). |
| `classifyByName` fragile ordered chain | `importer.go:167-194` | type-code switch / OCP | Model as an ordered slice of `{match, kind}` rules with explicit precedence (current `switch-true` ladder encodes precedence by order). |
| Parallel resource-kind maps in `contextData` | `importer.go:210-296` | Shotgun Surgery / Duplicate Code | Replace 7 near-identical map fields with `map[resourceKind]map[string]classifiedFile`; iterate once. |
| Resolve/Amend/Skip ignore store errors | `resolve.go:26-27,30-33,93-110,124-138`; `query.go:93` | Swallowed errors | Capture and wrap/return or log; make best-effort intent explicit. |
| Apply loop dispatches on string outcome codes | `apply.go:69-76` | Primitive Obsession / type-code switch | Introduce a typed `Outcome` (or reuse `ResourceState`) with named constants. |

### mcp-tools
| Title | Location | Smell | Recommendation |
|---|---|---|---|
| `registerSpecStubs` duplicates the entire real tool catalog | `tools.go:308-350` vs `513-829` | Duplicate Code / Shotgun Surgery | Drive both real and stub registration from a single `toolDef` table; swap the handler for a stub closure when `s.spec == nil`. (~33 names+schemas copy-pasted, already drifted.) |
| Inline anonymous handlers duplicate unmarshal-dispatch | `tools.go:57-95,152-229` | Duplicate Code | Extract named methods (`handleCodeReview`, etc.); add a generic `asyncTool[A]` routing through `runAsync`. |
| `specTool` variants swallow `json.Unmarshal` errors inconsistently | `tools.go:358-396`; handlers at `870,884,926,948` | Inconsistent error handling | Make all variants strict; delete the redundant `specToolStrict`; apply the guard to the 4 hand-written handlers. |
| Resources/prompts read are URI/name switch dispatchers | `handlers.go:142-175,217-257` | OCP / Divergent Change | Register in a map keyed by URI/name → `reader func(ctx)(any,error)`, mirroring the existing `toolFns` table. |
| `progressSender` builds notification payload inline | `tools.go:710-723` | Feature Envy / Primitive Obsession | Typed `progressNotification` struct + `writeNotification`. See Theme A. |

### mcp-server
| Title | Location | Smell | Recommendation |
|---|---|---|---|
| Duplicated JSON-RPC decode + dispatch across transports | `server.go:278-311,373-408` | Duplicate Code / Shotgun Surgery | Extract `resolveHandler(req)` returning handler or ready error response; name the error codes. See Theme C. |
| `runAsync` long method (5 responsibilities) | `server.go:414-490` | Long Method / SRP | Extract `registerCancel`/`unregisterCancel`, `finalizeJob`, `emitProgress`. |
| Two near-identical progress notifications | `server.go:436-443,475-486` | Duplicate Code | `s.emitProgress(token, progress, message)`. See Theme A. |

### persistence
| Title | Location | Smell | Recommendation |
|---|---|---|---|
| `Store` god object (13 domains) | `store.go:139-1381` | Large Class / SRP / Divergent Change | Split into per-domain stores sharing `*db.Queries`; thin `Store` facade. |
| `Store` exposes no interface | `store.go:141-144` | DIP / ISP | Introduce narrow interfaces (`JobReader`/`JobWriter`, `ResourceStore`, `SessionStore`); consumers accept the slice they need. |
| Two redundant DB paths; raw SQL bypasses sqlc | `store.go:142-143,182-239,1302-1329,1338-1381` | Inappropriate Intimacy / Redundant Coupling | Move Vacuum DELETEs into sqlc query files; confine raw `*sql.DB` to dynamic SELECT + DDL in a separate small type. |
| List-mapping loop repeated ~15 times | `store.go` (e.g. `374-384` … `1283-1293`) | Duplicate Code | Generic `mapRows[S,D](rows []S, conv func(S) D) []D`. |
| Empty-string→nullable-pointer duplicated inline | `store.go:544-552,727-733,784-788,802-814,936-943,978-986,1238-1243` | Duplicate Code / Primitive Obsession | Replace inline `if x != "" { p = &x }` guards with the existing `stringPtr` (`1081`). |

### engine-cue
| Title | Location | Smell | Recommendation |
|---|---|---|---|
| Misleading empty `disallowedTools` var | `engine.go:64-98,121-139,215-221` | Dead Code / misleading comments | **See Section 2.1 — behavioral flag.** |
| Repeated `RunOpts` + semaphore guard | `engine.go:88-95,131-136,215-221` (+ guards `78-81,111-114,198-201`) | Duplicate Code | `baseRunOpts(prompt, model)` helper; optional `withSlot(ctx, fn)` for the semaphore. |
| Registry is a fixed pipeline of `register*` steps | `registry.go:34-51,53-294` | Divergent Change / OCP / Large Class | Iterate a slice of registrar funcs; factor the common id/meta/edges/store tail into one helper. |
| Three near-identical `Flex*` `UnmarshalJSON` | `types.go:10-83` | Duplicate Code | Generic helper taking two target pointers + a flatten closure. |
| `Edge.Kind` / `Resource.Kind` stringly-typed | `registry.go:8-22,311-344`; `types.go:220-245` | Primitive Obsession | `type EdgeKind string` / `type ResourceKind string` with named constants. |

### support-cmd
| Title | Location | Smell | Recommendation |
|---|---|---|---|
| Duplicated read-one-store-method HTTP handlers | `dashboard.go:258-342` | Duplicate Code / Shotgun Surgery | `serveJSON(w, func()(any,error))` / `respond(w, data, err)` helper for the ~7 handlers. |
| Duplicated SSE streaming scaffolding | `dashboard.go:392-438,440-501` | Duplicate Code | `streamSSE(w, r, interval, send)` helper. |
| `buildDomainPrompt` long method | `internal/prompt/resource.go:18-120` | Long Method / SRP | Extract `writeHeader`/`writeDeclaration`/`writeMeta`/`writeAggregate`/`writePortContract`/`writeDependencies` (mirrors review.go). |
| Repeated stderr-print-then-exit | `cli.go:228-414`, `main.go` | Duplicate Code | Standardize on `fatalf(format, args...)`; route all CLI exits through it. See Theme E. |
| `openStore` CLI commands share run/error/exit skeleton | `cli.go:235-415` | Duplicate Code | Add a `withStore(func(*store.Store) error)` wrapper. |
| `runSubcommand` dispatch closed for extension | `main.go:73-119` | OCP | `map[string]func(args []string)` registry; each command owns its arg validation. |
| Two alternative prompt builders by kind type-code | `resource.go:11-16,122-190` | Switch on type code / Alternative Classes | Extract `writeJSONDepBlock`; strategy map keyed by kind. |

---

## 6. Minor Suggestions

**spec-core**
- Large instruction string literals (`orchestratorInstructions`, `dispatchInstructions`, `session.go:789-860`) → move to `instructions.go`/`embed`.
- `checkInvariant`/`checkInvariants` swallow engine errors (`loop.go:269-271`, `session.go:719-721`) — `continue` treats failure as pass; decide the policy once in the shared checker.
- Hardcoded `ReviewLevel: "light"` (`dispatch.go:230`) → named constants.
- `promptHash` (`loop.go:321-323`) + inline `sha256` idiom in session.go (4+ sites) → `hashHex([]byte) string`.
- `max := s.engine.MaxConcurrency()` shadows builtin `max` (`observability.go:64`) → rename to `maxConc`.

**spec-ops**
- Magic default limit `20` duplicated (`query.go:42,49`) → `defaultHistoryLimit`.
- `fmt.Sprintf` with no verbs (`importer.go:372`) → plain `WriteString`; vet flags it.
- Hardcoded cwd `"."` in `ValidateResource` (`validate.go:144-145`, `query.go:144`) → pass project/spec dir from cfg.
- `reviewResource` swallows per-resource review errors (`review.go:40-47`) → accumulate failed IDs.
- `DriftAction` "revert" unimplemented (`query.go:104-105`) → track the dead branch.

**mcp-tools**
- Magic `6`/`SELECT` length in spec/sql guard (`tools.go:973-976`) → `const selectKw`.
- Terminal job-status strings inline (`tools.go:173`) → `isTerminal(status)` / constants.
- Spec-workflow guide duplicated as prose in `handleAbout` and `handleInitialize` (`tools.go:283-304`, `handlers.go:27-54`) → single package-level const.
- `DetectRecursion` magic threshold + process substrings (`recursion.go:25-33`) → named consts.
- `"invalid arguments: "` repeated ~12 times → `invalidArgs(err)` helper.

**mcp-server**
- `recursion` field is a Temporary Field (`server.go:182,215-218`) → make it a local in `New()`.
- Magic JSON-RPC codes `-32700`/`-32601` (4 sites) → `codeParseError`/`codeMethodNotFound`.
- `Metrics.Record` min/max CAS loops duplicated (`metrics.go:78-90`) → `storeMin`/`storeMax`.
- `store` consumer interface is ~22 methods wide (`server.go:41-59`) → split into role interfaces (ISP); low priority.

**persistence**
- `context.Background()` hard-coded in every query call (pervasive) → thread `context.Context` for cancellation.
- `parseTime` swallows parse errors (`store.go:261-264`) → document or log.
- `UpdateGeneration` 8-param signature (`store.go:802`) → pass a `GenerationResult` struct.
- Magic constraint-violation substrings (`store.go:456-457`) → `isUniqueViolation(err)` helper.
- `ReadOnlyQuery` shallow 6-char SELECT guard (`store.go:1338-1342`) → document limitation or use a read-only connection.

**engine-cue**
- `fanOut` output order nondeterministic (`engine.go:203-242`) → pre-size `results`, write by index, drop the mutex.
- `fanOut` doc comment duplicates `CodeReview` verbatim (`engine.go:153-162,195-243`).
- `mergeMeta` does not merge `Meta.Mode` (`registry.go:346-372`) — **see Section 2.2, behavioral flag.**
- `Load` only inspects `instances[0]` (`loader.go:22-37`) → document the single-instance assumption.

**support-cmd**
- `dbPath()` has a hidden `os.MkdirAll` side effect (`main.go:211-215`) → rename `ensureDBPath()` or split.
- `RunOpts` 15-field parameter object (`agent.go:18-34`); `buildArgs` grows per field → consider a flag-builder slice.
- Magic stdin threshold `8192` (`agent.go:279`) → `const maxArgPromptBytes`.
- Recent-generations handler builds SQL via `fmt.Sprintf` (`dashboard.go:352-356`) → parameterized store method (limit is validated, but prefer it anyway).
- Inline anonymous response structs duplicated (`dashboard.go:128-147,198-227,244-247`) → name the stable payload types.
- `InjectRuntimeContext` repeats sorted-keys map-section pattern (`context.go:25-57`) → extract `writeMapSection` if a fourth section appears.

---

## 7. Per-Domain Health Summaries

**spec-core** — Well-structured: `Spec` depends on injected interfaces, methods decompose into focused helpers,
and the constraint loop is cleanly staged. Most significant issues: duplicate invariant-checking logic
(session.go and loop.go, wording already drifted) and a DI gap where validation/command execution and
`cuepkg.Load` are called as package-level functions. The duplication and the command-runner DI gap are the two
worth acting on.

**spec-ops** — Mostly thin, well-factored orchestration methods. Dominant problems: DI violations where
validate.go and bootstrap.go bypass the injected fileSystem; recurring type-code switches with duplicated
failure-message construction; several swallowed errors. `importer.go` is a Large Class mixing scanning,
classification, and CUE serialization. Refactoring opportunities, not behavioral bugs.

**mcp-tools** — Reasonably well-factored; generic `specTool` helpers remove most registration boilerplate.
Remaining hazards: inline anonymous handlers re-implementing unmarshal+dispatch, and `registerSpecStubs`
duplicating the entire ~33-tool catalog (schemas already drifted). Two of three `specTool` variants swallow
`json.Unmarshal` errors.

**mcp-server** — Good DI/SOLID shape; the one self-instantiated collaborator (`NewMetrics()`) is a pure
in-memory counter, not a DI violation. Refactoring value concentrates in server.go: stdio and HTTP transports
duplicate JSON-RPC decode + dispatch + error construction, and `runAsync` is a long method interleaving
cancel-registry bookkeeping, job-store transitions, metrics, and two near-identical progress notifications.

**persistence** — `store.go` wraps sqlc's `db.Queries` over ~13 aggregates. Mechanically clean but two
structural problems: `Store` is a 50+-method god-object, and it holds two redundant DB paths (`sqlDB` and
`queries`) plus hand-written raw SQL that duplicates schema knowledge. No strict DI violation, but `Store` is a
concrete type with no interface. Remaining items are pervasive-but-low-severity converter/pointer-mapping
duplication.

**engine-cue** — Generally clean, small, well-factored; `Engine`'s dependencies are properly injected via the
`runner` interface and `config.Config`. No critical DI/SOLID violations. The most actionable issue is the
misleading `disallowedTools` variable (behavioral flag). Secondary opportunities are duplication-driven: three
copy-pasted `Flex*` coercions, repeated RunOpts/semaphore boilerplate, ten structurally identical `register*`
methods, and stringly-typed Edge/Resource kinds.

**support-cmd** — Generally clean, small, well-factored; the prompt/graph/plan/config/errors packages mostly do
one thing each, and DI is respected. No critical issues. The strongest opportunities concentrate in
cmd/crest-spec: dashboard.go's duplicate read-handler and SSE scaffolding, and the CLI's repeated
print-to-stderr-then-exit envelope despite an existing `fatal` helper, plus a closed-for-extension subcommand
switch. In prompt/, `buildDomainPrompt` is a long multi-section method and `BuildResourcePrompt` selects between
two alternative builders via a kind type-code.

---

## 8. Suggested Remediation Roadmap

Ordered by leverage and risk. Each step is behavior-preserving except the two flags in Section 2, which should
be triaged first.

| # | Work | Domains | Risk | Payoff |
|---|---|---|---|---|
| 0 | Triage the two behavioral flags (`disallowedTools`, `mergeMeta`) | engine-cue | n/a | correctness |
| 1 | Inject `CommandRunner` + `Environment`/`SystemProbe`; route validate.go & bootstrap.go through them (C1, C2) | spec-ops, spec-core | low–med | unlocks testability |
| 2 | Replace assertion/validation/apply/classify switches with registries + named constants (C3, Theme B) | spec-ops, engine-cue | low | OCP, fewer silent drifts |
| 3 | Extract shared scaffolding: `emitProgress`, `resolveHandler`, `invalidArgs`, `fatalf`, `serveJSON`, `streamSSE` (Themes A, C, E) | mcp-*, support-cmd | low | kills the worst duplication |
| 4 | Collapse `registerSpecStubs` into a single `toolDef` table | mcp-tools | low | removes shotgun-surgery hazard |
| 5 | Make `specTool` variants strict about unmarshal errors | mcp-tools | low | hardening |
| 6 | Split `Store` behind narrow interfaces; move raw SQL into sqlc; add `mapRows`/`stringPtr` reuse (Theme D) | persistence | med | testability, SRP |
| 7 | Split `Spec` into SessionService/Dispatcher/Observer (Theme D) | spec-core | med | SRP, smaller files |
| 8 | Sweep remaining minor suggestions (Section 6) opportunistically | all | low | polish |

Steps 1–5 are independent and parallelizable; 6 and 7 are larger and should follow once the DI seams from
step 1 are in place.
