# Drop Subprocess Dispatch — Native Workflow Orchestration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove every `claude` CLI subprocess dispatch path from crest-spec; the MCP server becomes a pure spec state engine + mechanical validation loop, and Claude Code's native Workflow/Agent tools orchestrate generation with smaller models (sonnet default — NEVER haiku).

**Architecture:** The server never calls an LLM. Wherever it used to (constraint loop, commit invariant checks, amendment drafting, deep review, evolve reflection), it now either returns a prompt for the orchestrator to run, or accepts a verdict the orchestrator supplies. The core validation loop survives at the commit boundary: `spec_commit` runs mechanical validations server-side and rejects on failure; `spec_context` injects prior failure context into retry prompts; the orchestrator (a Claude Code workflow) drives generate → commit → retry. A new `.claude/workflows/spec-generate.js` + `.claude/skills/spec-generate/SKILL.md` implement the orchestration loop.

**Tech Stack:** Go 1.x (MCP server), CUE specs, SQLite (modernc.org/sqlite), Claude Code Workflow tool (JS orchestration script), markdown skill.

**Repo:** `/Users/crestenstclair/workspace/claude-mcp-server`. All paths below are relative to repo root.

**Compile-state note:** Tasks 1–5 are a coordinated surgery. Each task keeps ITS package's tests green (`go test ./internal/spec/...` etc.), but `go build ./...` only goes fully green at the end of Task 5. Commit once at the end of Task 2 (spec package green), and again at the end of Task 5 (whole repo green). Tasks 6+ each commit individually.

---

## What is being deleted (orientation for every task)

| Component | Path | Why |
|---|---|---|
| Agent wrapper (execs `claude` CLI) | `internal/agent/` (whole package) | subprocess dispatch |
| Engine (concurrency, fan-out) | `internal/engine/` (whole package) | wraps agent |
| Server-side constraint loop | `internal/spec/loop.go`, `loop_test.go` | generation retry moves to workflow |
| Atomic dispatch / wave dispatch | `internal/spec/dispatch.go` | called the loop |
| Unattended apply | `internal/spec/apply.go` | self-contained orchestration |
| Deep review | `internal/spec/review.go` | engine-driven LLM review |
| Output code-block parsing | `internal/spec/parse.go`, `parse_test.go` | orchestrator commits structured files; nothing parses fences server-side anymore |
| Async jobs + tools | `runAsync`, `run_prompt`, `poll_result`, `cancel_job`, `list_jobs`, `code_review`, `bugbot` in `internal/mcp/` | all dispatch-backed |
| Recursion guard | `internal/mcp/recursion.go`, `recursion_test.go`, `process.go` | no subprocesses → no recursion possible |
| `crest-spec run` + `check job` | `cmd/crest-spec/run.go`, `exec_unix.go`, `checkJob` in `main.go` | execs `claude` CLI / waits on jobs |
| Engine config | `APIKey`, `AgentPath`, `DefaultModel`, `Timeout`, `MaxConcurrency`, `VerifyModel` in `internal/config/config.go` | nothing consumes them |

**Kept (the state engine):** `spec/plan|begin|confirm_destroys|next|context|commit|resolve|note|amend|skip|finish|status|wave_status|log|history|graph|diff|state|vacuum|sql|unlock|mode|inspect|import|prompt|bootstrap|validate|validate-resource|apply_amendments|list_amendments|graduate_amendment|learnings|promote_learnings`, plus `about` (rewritten static) and `live_metrics`. `spec/evolve` changes shape (returns a prompt) and a new `spec/record_learnings` is added.

---

### Task 1: Spec package surgery — remove the engine dependency

**Files:**
- Delete: `internal/spec/dispatch.go`, `internal/spec/apply.go`, `internal/spec/loop.go`, `internal/spec/loop_test.go`, `internal/spec/review.go`, `internal/spec/parse.go`, `internal/spec/parse_test.go`
- Modify: `internal/spec/spec.go`, `internal/spec/session.go`, `internal/spec/amendments.go`
- Test: `internal/spec/session_test.go` (new tests), existing `internal/spec/*_test.go` (fix references)

- [ ] **Step 1: Delete the dispatch-layer files**

```bash
git rm internal/spec/dispatch.go internal/spec/apply.go internal/spec/loop.go internal/spec/loop_test.go internal/spec/review.go internal/spec/parse.go internal/spec/parse_test.go
```

- [ ] **Step 2: Strip the engine from `internal/spec/spec.go`**

Remove the `specEngine` interface (lines 21–28), the `engine` field on `Spec`, the `engineGenerator` type and its `Generate` method (lines 112–132), and the `internal/agent` + `internal/engine` imports. New constructor — `evolve.New` loses its generator+model args (Task 2 changes its signature):

```go
type Spec struct {
	store     specStore
	fs        fileSystem
	cfg       *config.Config
	reflector *evolve.Reflector
}

func New(st specStore, fs fileSystem, cfg *config.Config) *Spec {
	return &Spec{
		store:     st,
		fs:        fs,
		cfg:       cfg,
		reflector: evolve.New(&storeReflectorAdapter{st: st}),
	}
}
```

Replace the old `Evolve(ctx, sessionID) (int, error)` method with two methods (they call the Task-2 Reflector API):

```go
// EvolvePrompt builds the reflection prompt for a session's failure history.
// The orchestrator runs it against an LLM and submits the output via
// RecordLearnings. Returns "" when the session has no failure signal.
func (s *Spec) EvolvePrompt(ctx context.Context, sessionID string) (string, error) {
	if s.reflector == nil {
		return "", nil
	}
	applyID := ""
	if sess, err := s.store.GetSession(sessionID); err == nil && sess != nil {
		applyID = sess.ApplyID
	}
	return s.reflector.BuildSessionPrompt(sessionID, applyID)
}

// RecordLearnings parses LLM reflection output (the ===CREST_LEARNINGS===
// marker block) and persists new learnings. Returns the number added.
func (s *Spec) RecordLearnings(ctx context.Context, output string) (int, error) {
	if s.reflector == nil {
		return 0, nil
	}
	return s.reflector.Record(output)
}
```

- [ ] **Step 3: Write failing tests for the new commit contract in `internal/spec/session_test.go`**

Three behaviors: (a) supplied invariant verdicts gate the commit, (b) verdicts are recorded via `RecordInvariantCheck`, (c) every commit records a `Generation` row with the right outcome. Use the package's existing fake store/fs helpers (see how current session tests construct `Spec` — adapt to `New(st, fs, cfg)`):

```go
func TestCommitRejectsOnFailedInvariantVerdict(t *testing.T) {
	s, st := newTestSpecWithSession(t) // helper: adapt from existing session_test setup
	res, err := s.Commit(context.Background(), st.sessionID, st.resourceID,
		[]CommitFile{{Path: "out/a.go", Content: "package out\n"}}, "",
		[]InvariantCheckInput{{Invariant: "no global state", Passed: false, Summary: "uses a package-level var"}},
		"claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if res.Committed {
		t.Fatal("commit should be rejected when a supplied invariant verdict failed")
	}
	found := false
	for _, v := range res.Validations {
		if v.Kind == "invariant" && !v.Passed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a failed invariant ValidationResult")
	}
}

func TestCommitRecordsGenerationOutcome(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	_, err := s.Commit(context.Background(), st.sessionID, st.resourceID,
		[]CommitFile{{Path: "out/a.go", Content: "package out\n"}}, "", nil, "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	gens, _ := s.store.ListGenerations(st.resourceID, 10)
	if len(gens) == 0 {
		t.Fatal("expected a generation record from commit")
	}
}
```

(Confirm exact field names on `store.InvariantCheck` and `store.Generation` in `internal/store` before writing assertions — assert behavior, not field layout.)

- [ ] **Step 4: Run the new tests to verify they fail**

Run: `go test ./internal/spec/ -run 'TestCommitRejectsOnFailedInvariantVerdict|TestCommitRecordsGenerationOutcome' -v`
Expected: FAIL (compile error: `InvariantCheckInput` undefined / wrong arity)

- [ ] **Step 5: Rework `internal/spec/session.go`**

Remove the `internal/engine` import. Delete `checkInvariants` (lines 758–799). Add the verdict-ingestion type and change `Commit`'s signature:

```go
// InvariantCheckInput is an orchestrator-supplied verdict for one project
// invariant, judged against the files being committed. The server records and
// enforces it; the LLM judgment happens outside the server.
type InvariantCheckInput struct {
	Invariant string `json:"invariant"`
	Passed    bool   `json:"passed"`
	Summary   string `json:"summary"`
}

func (s *Spec) Commit(ctx context.Context, sessionID, resourceID string, files []CommitFile, notes string, invariantChecks []InvariantCheckInput, model string) (*CommitResult, error) {
```

Inside `Commit`, default the model and record a generation row whose outcome reflects the validation gate:

```go
	if model == "" {
		model = s.cfg.GenerateModel
	}
	genID := uuid.NewString()
	s.store.CreateGeneration(store.Generation{
		ID: genID, ApplyID: sess.ApplyID, ResourceID: resourceID, Model: model,
	})
```

…then where the rejected path returns (`if rejected != nil { ... }`), before returning add:

```go
		s.store.UpdateGeneration(genID, "", "rejected", firstFailureMessage(rejected.Validations), 0, 0, 0, 0)
```

and on the success path before `return &CommitResult{...}`:

```go
	s.store.UpdateGeneration(genID, "", "success", "", 0, 0, 0, 0)
```

with the helper:

```go
func firstFailureMessage(results []ValidationResult) string {
	for _, v := range results {
		if !v.Passed {
			return v.Message
		}
	}
	return ""
}
```

In `runCommitValidations`, replace the engine-based invariant block (`if s.engine != nil && len(...Invariants) > 0 { ... }`) with verdict ingestion (pass `invariantChecks` through as a new parameter):

```go
	if len(invariantChecks) > 0 {
		invariantResults := s.ingestInvariantChecks(sess.ApplyID, resourceID, invariantChecks)
		if rejection := s.checkForFailure(invariantResults, sess.ApplyID, sessionID, resourceID, "invariant", attempts); rejection != nil {
			rejection.Validations = append(validationResults, invariantResults...)
			return nil, rejection
		}
		validationResults = append(validationResults, invariantResults...)
	}
```

```go
// ingestInvariantChecks converts orchestrator-supplied verdicts into
// ValidationResults and persists them for the audit trail / reflection.
func (s *Spec) ingestInvariantChecks(applyID, resourceID string, checks []InvariantCheckInput) []ValidationResult {
	results := make([]ValidationResult, 0, len(checks))
	for _, c := range checks {
		s.store.RecordInvariantCheck(store.InvariantCheck{
			// match the actual store.InvariantCheck fields — set ID (uuid),
			// ApplyID, ResourceID, the invariant text, passed flag, summary,
			// and CreatedAt if present.
		})
		r := ValidationResult{Kind: "invariant", Passed: c.Passed}
		if !c.Passed {
			r.Message = fmt.Sprintf("Invariant violated: %s\n%s", c.Invariant, c.Summary)
		}
		results = append(results, r)
	}
	return results
}
```

Expose project invariants to the orchestrator — extend `ContextResult` and fill it in `Context`:

```go
type ContextResult struct {
	SystemPrompt string
	Prompt       string
	Instructions string
	Invariants   []InvariantInfo
}

type InvariantInfo struct {
	Text      string `json:"text"`
	Rationale string `json:"rationale,omitempty"`
}
```

in `Context`, before the return:

```go
	var invariants []InvariantInfo
	for _, inv := range planResult.Registry.Project.Invariants {
		invariants = append(invariants, InvariantInfo{Text: inv.Text, Rationale: inv.Meta.Rationale})
	}
```

In `Finish`, replace the synchronous reflection block (`if s.reflector != nil && (s.cfg.Evolve == "finish" || s.cfg.Evolve == "all") { _, _ = s.reflector.ReflectSession(...) }`) with a prompt handed back to the orchestrator:

```go
type FinishResult struct {
	Committed        int
	Skipped          int
	Errored          int
	ReflectionPrompt string `json:"reflection_prompt,omitempty"`
}
```

```go
	reflectionPrompt := ""
	if s.reflector != nil && (s.cfg.Evolve == "finish" || s.cfg.Evolve == "all") {
		reflectionPrompt, _ = s.reflector.BuildSessionPrompt(sessionID, sess.ApplyID)
	}
```

and include `ReflectionPrompt: reflectionPrompt` in the returned struct.

Rewrite `orchestratorInstructions()` and `dispatchInstructions()` (Task 6 has the exact text — implement them here verbatim from Task 6 so this package compiles with the final wording).

- [ ] **Step 6: Strip LLM drafting from `internal/spec/amendments.go`**

Delete `ProposeAmendments`, `formatFinding`, and `parseProposedAmendments` (lines ~179–252) and the `internal/engine` import. Keep `ProposedAmendment`, `ApplyAmendments`, `ListAmendments`, `GraduateAmendment`, `ReconcileAmendments`, and the commit-time `markAmendmentVerification` (it is mechanical). If `ReviewFinding`/`DeepReview` types from the deleted `review.go` are referenced anywhere left, the references go too (`cuepkg.Finding` on `ProposedAmendment` stays — it's a CUE type). Delete the corresponding tests in `amendments_test.go` / `amend_test.go` (only those covering `ProposeAmendments`/`parseProposedAmendments`).

- [ ] **Step 7: Fix remaining spec-package test fallout**

Search and fix: `grep -rn "specEngine\|engine\.\|NewTestEngine\|Dispatch\|RunWave\|DeepReview\|ProposeAmendments\|spec.New(" internal/spec/*_test.go`. Every `spec.New(eng, st, fs, cfg)` becomes `spec.New(st, fs, cfg)`; every `Commit(ctx, sid, rid, files, notes)` call gains `, nil, ""`. Delete tests that only exercised deleted code.

- [ ] **Step 8: Run the spec package tests**

Run: `go vet ./internal/spec/ && go test ./internal/spec/ -v`
Expected: PASS, including the two new commit tests. (Note: `go build ./...` still fails — `internal/mcp` and `cmd` are fixed in Tasks 3–4.)

---

### Task 2: Split the evolve reflector into prompt-out / verdict-in

**Files:**
- Modify: `internal/evolve/evolve.go`
- Test: `internal/evolve/evolve_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestBuildSessionPromptAndRecordRoundTrip(t *testing.T) {
	st := newFakeStore() // reuse the existing fake store in evolve_test.go
	seedFailureSignal(st) // reuse/adapt existing signal-seeding helper
	r := New(st)

	prompt, err := r.BuildSessionPrompt("sess-1", "apply-1")
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}
	if prompt == "" {
		t.Fatal("expected a non-empty reflection prompt for a session with failures")
	}

	output := "===CREST_LEARNINGS===\n" + sampleLearningsJSON + "\n===END_CREST_LEARNINGS==="
	added, err := r.Record(output)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if added == 0 {
		t.Fatal("expected learnings to be persisted")
	}
}
```

(Match `sampleLearningsJSON` and the marker strings to what `parseLearnings`/`extractMarkerBlock` in `evolve.go` actually expect — read them first; existing tests show the exact format.)

- [ ] **Step 2: Run it to verify failure**

Run: `go test ./internal/evolve/ -run TestBuildSessionPromptAndRecordRoundTrip -v`
Expected: FAIL — `New` arity / methods undefined.

- [ ] **Step 3: Rework `evolve.go`**

- Delete the `Generator` interface and the `gen`/`model` fields; `func New(st Store) *Reflector`.
- Split `reflect()` (signal → prompt → **generate** → parse → persist) into:

```go
// BuildSessionPrompt gathers a session's failure signal and renders the
// extraction prompt. Returns "" when there is nothing to reflect on.
func (r *Reflector) BuildSessionPrompt(sessionID, applyID string) (string, error) {
	resources, err := r.store.ListSessionResources(sessionID)
	if err != nil {
		return "", err
	}
	signal := r.gatherSignal(applyID, resources)
	if len(signal) == 0 {
		return "", nil
	}
	return buildExtractionPrompt(signal, r.loadExisting()), nil
}

// Record parses reflection output and persists new learnings, returning the
// count added. Dedup against existing learnings is preserved from the old
// persist path.
func (r *Reflector) Record(output string) (int, error) {
	parsed := parseLearnings(output)
	if len(parsed) == 0 {
		return 0, nil
	}
	return r.persist(parsed, r.loadExisting())
}
```

- Delete `ReflectSession`/`ReflectWave`/`reflect` (their internals are reused by the two methods above; `gatherSignal`, `loadExisting`, `parseLearnings`, `persist`, `buildExtractionPrompt` all stay). Adjust `persist` to return `(int, error)` if it doesn't already.
- Fix `evolve_test.go`: tests that called `ReflectSession` with a fake generator now do the two-step round trip.

- [ ] **Step 4: Run package tests**

Run: `go test ./internal/evolve/ -v`
Expected: PASS

- [ ] **Step 5: Commit the green spec+evolve packages**

```bash
go test ./internal/spec/ ./internal/evolve/ && git add -A && git commit -m "feat(spec)!: remove engine from spec package; commit takes invariant verdicts; evolve splits into prompt-out/record-in"
```

---

### Task 3: MCP package surgery — drop async jobs, engine tools, dispatch tools

**Files:**
- Delete: `internal/mcp/recursion.go`, `internal/mcp/recursion_test.go`, `internal/mcp/process.go`
- Modify: `internal/mcp/server.go`, `internal/mcp/tools.go`, `internal/mcp/handlers.go`
- Test: `internal/mcp/server_test.go`

- [ ] **Step 1: Delete recursion/process files**

```bash
git rm internal/mcp/recursion.go internal/mcp/recursion_test.go internal/mcp/process.go
```

- [ ] **Step 2: Rework `server.go`**

- Delete the package-private `engine` interface (lines 30–38) and the `store` interface (lines 41–59) entirely.
- `Server` struct: remove `eng`, `store`, `pt`, `cancels`, `cancelsMu`, `bgCtx`, `bgCancel`, `recursion`. Keep `spec`, `stdin`, `stdout`, `log`, `cfg`, `metrics`, `asyncWg`, `outMu`, `tools`, `dispatch`, `toolFns`, `startTime`.
- `New(spec specHandler, stdin io.Reader, stdout io.Writer, log zerolog.Logger, cfg *config.Config) *Server` — drop eng/store/pt params and the whole recursion block.
- Delete `runAsync` (lines 416–496) and the `uuid`/`os` imports it needed; `shutdown()` keeps only the `asyncWg.Wait()` logic (no `bgCancel`).
- `specHandler` interface: remove `Apply`, `Dispatch`, `RunWave`, `DeepReview`, `ProposeAmendments`; change `Commit` to
  `Commit(ctx context.Context, sessionID, resourceID string, files []specmod.CommitFile, notes string, invariantChecks []specmod.InvariantCheckInput, model string) (*specmod.CommitResult, error)`;
  replace `Evolve(ctx, sessionID) (int, error)` with `EvolvePrompt(ctx context.Context, sessionID string) (string, error)` and `RecordLearnings(ctx context.Context, output string) (int, error)`.

- [ ] **Step 3: Rework `tools.go`**

- Delete: `registerAsyncTools`, `handleRunPrompt`, `registerJobTools`, `registerSpecDispatchTools`, `handleSpecDispatch`, `handleSpecRunWave`, `handleSpecDeepReview`, `agentEventRecorder`, `progressSender`, `handleSpecApply`, and the `spec/apply` + `spec/propose_amendments` registrations. Remove `enginemod`/`uuid`/`sha256` imports if now unused.
- `registerTools()` becomes:

```go
func (s *Server) registerTools() {
	s.dispatch = map[string]handlerFunc{
		"initialize":                s.handleInitialize,
		"notifications/initialized": s.handleInitialized,
		"tools/list":                s.handleToolsList,
		"tools/call":                s.handleToolCall,
		"resources/list":            s.handleResourcesList,
		"resources/read":            s.handleResourcesRead,
		"prompts/list":              s.handlePromptsList,
		"prompts/get":               s.handlePromptsGet,
	}
	s.registerInfoTools()
	if s.spec != nil {
		s.registerSpecLifecycleTools()
		s.registerSpecQueryTools()
	} else {
		s.registerSpecStubs()
	}
}
```

- `registerInfoTools`: drop `list_models` and `status` (engine-backed). Keep `live_metrics`. Rewrite `about` as a static handler (no engine) — use the Task 6 "about" text.
- `spec/commit` registration: extend `specCommitArgs` and the handler:

```go
type specCommitArgs struct {
	SessionID  string `json:"session_id"`
	ResourceID string `json:"resource_id"`
	Files      []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
	Notes           string                         `json:"notes"`
	Model           string                         `json:"model"`
	InvariantChecks []specmod.InvariantCheckInput  `json:"invariant_checks"`
}
```

```go
func (s *Server) handleSpecCommit(ctx context.Context, args json.RawMessage, _ string) toolResult {
	var p specCommitArgs
	json.Unmarshal(args, &p)
	files := make([]specmod.CommitFile, len(p.Files))
	for i, f := range p.Files {
		files[i] = specmod.CommitFile{Path: f.Path, Content: f.Content}
	}
	result, err := s.spec.Commit(ctx, p.SessionID, p.ResourceID, files, p.Notes, p.InvariantChecks, p.Model)
	if err != nil {
		return errorResult(fmt.Sprintf("commit: %v", err))
	}
	return jsonResult(result)
}
```

Update the `spec/commit` tool description + InputSchema: files are required; add `model` (string, model that generated the files) and `invariant_checks` (`{"type":"array","items":{"type":"object","properties":{"invariant":{"type":"string"},"passed":{"type":"boolean"},"summary":{"type":"string"}},"required":["invariant","passed"]}}`) with description "Orchestrator-judged verdicts for the project invariants returned by spec_context. A failed verdict rejects the commit."
- `spec/evolve` becomes prompt-out, plus a new `spec/record_learnings`:

```go
	s.addTool(toolDef{
		Name: "spec/evolve", Description: "Build the reflection prompt from a session's failure history. Run the returned prompt with an LLM (sonnet), then submit the raw output to spec/record_learnings. Returns an empty prompt when there is nothing to learn from.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID to reflect over"}},"required":["session_id"]}`),
	}, specTool("evolve", func(ctx context.Context, a specEvolveArgs) (any, error) {
		prompt, err := s.spec.EvolvePrompt(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"reflection_prompt": prompt}, nil
	}))

	s.addTool(toolDef{
		Name: "spec/record_learnings", Description: "Persist learnings distilled by a reflection run. Pass the raw LLM output from the spec/evolve reflection prompt (the ===CREST_LEARNINGS=== block).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"output":{"type":"string","description":"Raw reflection LLM output"}},"required":["output"]}`),
	}, specTool("record_learnings", func(ctx context.Context, a specRecordLearningsArgs) (any, error) {
		added, err := s.spec.RecordLearnings(ctx, a.Output)
		if err != nil {
			return nil, err
		}
		return map[string]int{"learnings_added": added}, nil
	}))
```

```go
type specRecordLearningsArgs struct {
	Output string `json:"output"`
}
```

- `registerSpecStubs`: remove the `spec/apply`, `spec/dispatch`, `spec/run_wave`, `spec/deep_review`, `spec/propose_amendments` stub entries; add a `spec/record_learnings` stub; fix the `spec/commit` stub schema to match the new one.

- [ ] **Step 4: Rewrite the initialize instructions in `handlers.go`**

Replace the `instructions` string in `handleInitialize` with the Task 6 "MCP instructions" text verbatim. Also update `handlePromptsGet`'s `orchestrator_instructions` case to: `"You are the orchestrator. For each resource: spec/context → generate with a sub-agent (sonnet) → judge the returned invariants → spec/commit with files + invariant_checks. On Committed=false, re-call spec/context (it includes the failure) and retry."`

- [ ] **Step 5: Fix `server_test.go` and metrics tests**

Remove fake engine/store/processTree fixtures and tests for deleted tools (`run_prompt`, `poll_result`, `cancel_job`, `list_jobs`, `code_review`, `bugbot`, recursion, runAsync). Update `mcp.New(...)` call sites to the new arity, and any fake `specHandler` to the new interface (Commit arity, EvolvePrompt/RecordLearnings). Add one new test:

```go
func TestCommitToolForwardsInvariantChecks(t *testing.T) {
	fake := &fakeSpec{} // existing fake specHandler, extended to capture Commit args
	srv := New(fake, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	args := json.RawMessage(`{"session_id":"s","resource_id":"r","files":[{"path":"a.go","content":"x"}],"model":"claude-sonnet-4-6","invariant_checks":[{"invariant":"no globals","passed":false,"summary":"global var"}]}`)
	srv.toolFns["spec/commit"](context.Background(), args, "")
	if len(fake.lastInvariantChecks) != 1 || fake.lastInvariantChecks[0].Passed {
		t.Fatal("invariant_checks not forwarded to spec.Commit")
	}
}
```

- [ ] **Step 6: Run package tests**

Run: `go vet ./internal/mcp/ && go test ./internal/mcp/ -v`
Expected: PASS (repo-wide build still red until Task 4).

---

### Task 4: cmd surgery — main wiring, drop `run`/`check job`, trim dashboard

**Files:**
- Delete: `cmd/crest-spec/run.go`, `cmd/crest-spec/exec_unix.go`
- Modify: `cmd/crest-spec/main.go`, `cmd/crest-spec/dashboard.go`, `cmd/crest-spec/static/*` (whatever JS/HTML references jobs/agent-events)

- [ ] **Step 1: Delete run command files**

```bash
git rm cmd/crest-spec/run.go cmd/crest-spec/exec_unix.go
```

- [ ] **Step 2: Rework `main.go`**

- Remove imports of `internal/agent` and `internal/engine`.
- Remove the `check job` branch in `main()` (lines 31–34) and the whole `checkJob` function; remove `processAlive` and the `CleanupOrphans` call in `runServer` (jobs are gone).
- Remove the `case "run":` branch in `runSubcommand`.
- `runServer` wiring becomes:

```go
	sp := specmod.New(s, specmod.OSFileSystem{}, cfg)
	srv := mcp.New(sp, os.Stdin, os.Stdout, log.Logger, cfg)
```

- Update `showHelp()`: drop the `run` and `check job` lines; the header line "Use 'crest-spec run' to start a generation session." becomes "Generation is driven by Claude Code via MCP — see the spec-generate skill." and the MCP hint line becomes "Or connect via MCP: spec/begin → spec/next → spec/context → spec/commit → spec/finish".

- [ ] **Step 3: Trim `dashboard.go` + static assets**

Remove the `/api/jobs`, `/api/agent-events/*`, `/api/agent-events-stream/*`, `/api/agent-events-recent` handlers and routes, the `ListJobs`/agent-event store calls inside the summary/websocket payloads (drop those JSON keys), and `running_jobs` from the stats struct. Then grep `cmd/crest-spec/static/` for `jobs`, `agent-events`, `agent_events` and delete the panels/fetches that consumed them (keep session/wave/resource views working). Keep the change minimal — dead UI removal, no redesign.

- [ ] **Step 4: Full build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS across all packages. If `internal/store` has now-unused job methods (`CreateJob`, `CompleteJob`, `FailJob`, `CancelJob`, `GetJob`, `ListJobs`, `DeleteJob`, `UpdateJobProgress`, `CleanupOrphans`, `WaitForCompletion`, `CreateAgentEvent`, `ListAgentEventsByResource`, `ListRecentAgentEvents`): leave the store layer and DB schema intact (no migration churn) — only delete them if `go vet`/lints complain about nothing referencing them; dashboard still uses agent-event reads only if you kept those panels (you didn't — but reads can stay in store harmlessly).

---

### Task 5: Config cleanup + green-repo commit

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Trim the config struct**

```go
type Config struct {
	HTTPAddr string `envconfig:"HTTP_ADDR"`

	GenerateModel    string `envconfig:"GENERATE_MODEL" default:"claude-sonnet-4-6"`
	MaxRetries       int    `envconfig:"MAX_RETRIES" default:"3"`
	WaveMaxRetries   int    `envconfig:"WAVE_MAX_RETRIES" default:"2"`
	SpecDir          string `envconfig:"SPEC_DIR" default:"./spec"`
	TypeCheckCommand string `envconfig:"TYPE_CHECK_CMD"`
	TestCommand      string `envconfig:"TEST_CMD"`
	Mode             string `envconfig:"MODE" default:"default"`
	Evolve           string `envconfig:"EVOLVE" default:"all"`
}
```

(`GenerateModel` survives as the model *label* used in effective hashes and state rows — `graphpkg.ComputeEffectiveHashes` and `persistCommittedResource` read it. Removed: `APIKey`, `AgentPath`, `DefaultModel`, `Timeout`, `MaxConcurrency`, `VerifyModel`.)

- [ ] **Step 2: Fix `config_test.go` and any references**

Run: `grep -rn "APIKey\|AgentPath\|DefaultModel\|MaxConcurrency\|VerifyModel\|cfg.Timeout" --include="*.go" .` — fix every hit (most were deleted with engine/agent).

- [ ] **Step 3: Verify and commit**

```bash
go build ./... && go vet ./... && go test ./... && gofmt -l . | (! grep .)
git add -A
git commit -m "feat!: drop claude-CLI subprocess dispatch — server is a pure spec state engine

Generation orchestration moves to Claude Code native workflows. Removed:
internal/agent, internal/engine, constraint loop, async jobs machinery,
run_prompt/poll_result/code_review/bugbot, spec/apply|dispatch|run_wave|
deep_review|propose_amendments, crest-spec run, recursion guard.
spec/commit now accepts orchestrator-judged invariant_checks and records
generations; spec/evolve returns a reflection prompt; new
spec/record_learnings persists reflection output; spec/finish returns
reflection_prompt.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: New orchestrator-facing instruction text

This task defines the canonical wording referenced by Tasks 1, 3. Implement wherever those tasks point here. The three texts:

**(a) MCP `instructions` in `handlers.go` `handleInitialize`:**

```
crest-spec is a declarative code generation system. The MCP server is a pure
spec state engine — it never calls an LLM and never spawns subprocesses.
YOU (Claude Code) are the orchestrator and run all generation with your own
sub-agents/workflows. Default generation model: sonnet.

## Spec workflow — native orchestration

1. spec_plan            → see what needs generating
2. spec_begin           → start a session (returns session_id, plan, waves, pending destroys)
3. spec_confirm_destroys → if PendingDestroys is non-empty, confirm deletions
4. spec_next            → next wave of resources (dependency order)
5. For each resource in the wave (parallelize across the wave):
   a. spec_context      → scoped prompt + system_prompt + project invariants
   b. Generate with a sub-agent (sonnet) using that prompt
   c. Judge each returned invariant against the generated files (pass/fail + summary)
   d. spec_commit       → files + notes + model + invariant_checks
                          The server writes files and runs the resource's
                          mechanical validations (compile/test/custom).
                          A failed validation or a failed invariant verdict
                          rejects the commit.
   e. If Committed=false: call spec_context again (it now includes the
      failure), regenerate, re-commit — up to max_retries. Then
      spec_resolve (guidance) or spec_skip.
6. Repeat from step 4 until spec_next returns done=true
7. spec_finish          → finalize; if reflection_prompt is non-empty, run it
                          with a sub-agent and submit via spec_record_learnings

The core loop is generate → commit → validate → retry-with-feedback.
Never write implementation code in the orchestrator context — every file
comes from a sub-agent. You are the quality gate: review before committing.

## Self-improvement
- spec_evolve / spec_record_learnings: distill learnings from failures
- spec_learnings / spec_promote_learnings: inspect and promote learnings
```

**(b) `orchestratorInstructions()` in `session.go`** — same content as (a) from "## Spec workflow" down, wrapped in the existing banner style, with the DO-NOT block at the end:

```
DO NOT:
  - Write implementation code directly — every file must come from a sub-agent
  - Skip the sub-agent step for any resource, even simple ones
```

**(c) `dispatchInstructions(resourceID)` in `session.go`:**

```go
func dispatchInstructions(resourceID string) string {
	return fmt.Sprintf(`Generate code for %s with a sub-agent (sonnet by default).

1. Give the sub-agent the system_prompt and prompt from this result.
2. The sub-agent produces the files (path + full content).
3. Judge each invariant in this result against the files (passed + summary).
4. Call spec/commit with session_id, resource_id, files, model, and
   invariant_checks. The server runs mechanical validations and rejects on
   any failure.
5. If Committed=false, call spec/context again — the failure context is
   injected into the new prompt — and retry (respect max_retries).`, resourceID)
}
```

**(d) `about` tool text in `tools.go`** — static, no engine:

```go
func (s *Server) handleAbout(_ context.Context, _ json.RawMessage, _ string) toolResult {
	return textResult(`crest-spec — declarative code generation MCP server (state engine only).

This server plans, tracks, validates, and records. It does not run LLMs.
Claude Code orchestrates generation natively. Call spec_begin and follow
the returned Instructions, or read the server instructions from initialize.`)
}
```

- [ ] **Step: covered by Tasks 1 and 3** — verify with `go test ./internal/spec/ ./internal/mcp/` that the strings compile, then ensure committed in Task 5's commit.

---

### Task 7: Workflow script — `.claude/workflows/spec-generate.js`

**Files:**
- Create: `.claude/workflows/spec-generate.js`

- [ ] **Step 1: Write the workflow script**

```js
export const meta = {
  name: 'spec-generate',
  description: 'Drive a crest-spec generation session: waves of sub-agents generate, commit, and retry against server-side validations',
  whenToUse: 'After spec_begin has produced a session. Pass {sessionId, model?, maxRetries?} as args.',
  phases: [
    { title: 'Wave', detail: 'one generator agent per resource, retry loop inside the agent' },
    { title: 'Triage', detail: 'resolve or skip resources still failing after retries' },
  ],
}

// args: { sessionId: string, model?: string, maxRetries?: number }
const sessionId = args.sessionId
if (!sessionId) throw new Error('spec-generate requires args.sessionId (run spec_begin first)')
const model = args.model || 'sonnet'           // NEVER haiku
const maxRetries = args.maxRetries ?? 3

const WAVE_SCHEMA = {
  type: 'object',
  properties: {
    done: { type: 'boolean' },
    wave_index: { type: 'number' },
    resources: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          resource_id: { type: 'string' },
          attempts: { type: 'number' },
          last_error: { type: 'string' },
        },
        required: ['resource_id'],
      },
    },
  },
  required: ['done'],
}

const OUTCOME_SCHEMA = {
  type: 'object',
  properties: {
    resource_id: { type: 'string' },
    outcome: { type: 'string', enum: ['committed', 'rejected', 'skipped', 'error'] },
    attempts: { type: 'number' },
    error: { type: 'string' },
    files: { type: 'array', items: { type: 'string' } },
  },
  required: ['resource_id', 'outcome'],
}

function generatorPrompt(resourceId, waveIndex) {
  return `You are a crest-spec generation sub-agent for resource "${resourceId}" (session ${sessionId}, wave ${waveIndex}).

Load the crest-spec MCP tools first:
ToolSearch "select:mcp__crest-spec__spec_context,mcp__crest-spec__spec_commit"

Then run this loop (at most ${maxRetries + 1} attempts):
1. Call spec_context with {session_id: "${sessionId}", resource_id: "${resourceId}"}.
   It returns SystemPrompt, Prompt, and Invariants. Treat SystemPrompt as your
   role and follow Prompt exactly — it contains the resource declaration,
   dependencies, existing files (UPDATE mode), and any prior failure context.
2. Author the files the prompt asks for (full file contents, correct paths
   relative to the project root). Follow the prompt's folder structure and
   style rules. Do NOT create files the prompt doesn't call for.
3. Judge EACH invariant from the context against your files: {invariant,
   passed, summary}. Be honest — a wrong "passed" will fail wave validation
   later and cost another round trip.
4. Call spec_commit with {session_id, resource_id, files: [{path, content}],
   model: "${model}", notes: <one-line design note>, invariant_checks: [...]}.
5. If the result has Committed=true → stop, report outcome "committed".
   If Committed=false → read result.Validations for the failure, go back to
   step 1 (the new context includes the failure) and fix the actual problem.
6. If still rejected after ${maxRetries + 1} attempts, report outcome
   "rejected" with the final error message. Do not call spec_skip yourself.

Your final message is parsed as data: report resource_id, outcome, attempts,
error (last validation message, if any), and the file paths you committed.`
}

const triaged = []
let waveCount = 0

while (true) {
  const wave = await agent(
    `Load the crest-spec MCP tools (ToolSearch "select:mcp__crest-spec__spec_next"), call spec_next with {session_id: "${sessionId}"}, and return its result: done, wave_index, and resources (resource_id, attempts, last_error).`,
    { label: 'spec_next', phase: 'Wave', schema: WAVE_SCHEMA },
  )
  if (!wave || wave.done) break
  waveCount++
  const resources = (wave.resources || []).filter(Boolean)
  if (resources.length === 0) break
  log(`Wave ${wave.wave_index}: ${resources.length} resource(s)`)

  const outcomes = await parallel(resources.map(r => () =>
    agent(generatorPrompt(r.resource_id, wave.wave_index), {
      label: `gen:${r.resource_id}`,
      phase: 'Wave',
      model,
      schema: OUTCOME_SCHEMA,
    })
  ))

  const failed = outcomes.filter(Boolean).filter(o => o.outcome !== 'committed')
  for (const f of failed) {
    // One triage agent per failure: decide resolve-with-guidance vs skip.
    const verdict = await agent(
      `Resource "${f.resource_id}" in crest-spec session ${sessionId} failed generation after ${f.attempts ?? '?'} attempts. Last error:\n${f.error || '(none reported)'}\n\nLoad tools: ToolSearch "select:mcp__crest-spec__spec_resolve,mcp__crest-spec__spec_skip,mcp__crest-spec__spec_history"\n\nInspect spec_history for the resource if helpful. If the failure looks fixable with concrete guidance (a specific API misuse, a missing import pattern, a misread of the spec), call spec_resolve with {session_id: "${sessionId}", resource_id: "${f.resource_id}", guidance: <specific, actionable guidance>} — this resets the resource to pending so the next wave pass retries it. If it looks structurally impossible (contradictory spec, missing dependency), call spec_skip with a reason. Report which you chose and why.`,
      { label: `triage:${f.resource_id}`, phase: 'Triage' },
    )
    triaged.push({ resource_id: f.resource_id, action: verdict })
  }
  // Loop continues: spec_next re-serves resolved (pending) resources in the
  // same wave, or advances when the wave is terminal.
}

return {
  waves_processed: waveCount,
  triaged,
  next_steps: 'Call spec_finish (main session). If FinishResult.reflection_prompt is non-empty, run it with a sonnet agent and submit the output via spec_record_learnings.',
}
```

- [ ] **Step 2: Sanity-check the script**

No `Date.now()`/`Math.random()`/TypeScript syntax; `meta` is a pure literal; phases match `phase:` strings (`Wave`, `Triage`). Read it once more against those rules.

- [ ] **Step 3: Commit**

```bash
git add .claude/workflows/spec-generate.js
git commit -m "feat(workflows): native spec-generate workflow replaces server-side wave dispatch"
```

---

### Task 8: Skill — `.claude/skills/spec-generate/SKILL.md`

**Files:**
- Create: `.claude/skills/spec-generate/SKILL.md`

- [ ] **Step 1: Write the skill**

```markdown
---
name: spec-generate
description: Use when the user asks to run/apply/generate a crest-spec session ("run the spec", "generate phase N", "apply the spec") — drives the full native generation pipeline via the spec-generate workflow with sonnet sub-agents
---

# crest-spec native generation

You are the orchestrator. The crest-spec MCP server is a pure state engine —
it never runs LLMs. Generation happens in YOUR sub-agents via the
spec-generate workflow. Default model: sonnet. Never haiku.

## Pipeline

1. `spec_plan` — review what will change. If empty, report "up to date" and stop.
2. `spec_begin` — returns session_id, plan, waves, PendingDestroys.
3. If PendingDestroys is non-empty: show the list to the user and call
   `spec_confirm_destroys` only for resources the user confirms (or all, if
   the user pre-authorized destructive applies).
4. Invoke the Workflow tool:
   `Workflow({scriptPath: ".claude/workflows/spec-generate.js", args: {sessionId: "<session_id>"}})`
   (Use `args.model` to override the generation model; complex resources can
   justify opus — never haiku.)
5. When the workflow completes, review its `triaged` list and surface skips
   to the user.
6. `spec_finish` — if the result's `reflection_prompt` is non-empty, run it
   with one sonnet sub-agent (Agent tool, general-purpose) and pass the raw
   output to `spec_record_learnings`.
7. Report: committed/skipped/errored counts, triage decisions, learnings added.

## Failure handling

- The workflow retries each resource internally (server injects failure
  context into the regenerated prompt) and triages persistent failures with
  spec_resolve/spec_skip.
- If the whole workflow dies, `spec_status`/`spec_wave_status` show where it
  stopped; re-invoking the workflow with the same sessionId resumes (spec_next
  re-serves non-terminal resources).
- A stale lock from a crashed session: `spec_unlock`, then `spec_begin` again.

## Rules

- Never write generated-resource code in the main session — every file comes
  from a sub-agent via spec_commit.
- Waves are sequential; resources within a wave run in parallel (the workflow
  handles this).
- Validation failures are signal, not noise: read them before retrying scope.
```

- [ ] **Step 2: Commit**

```bash
git add .claude/skills/spec-generate/SKILL.md
git commit -m "feat(skills): spec-generate skill drives native orchestration"
```

---

### Task 9: Update `scripts/run-phased-agent.sh` to launch Claude Code directly

**Files:**
- Modify: `scripts/run-phased-agent.sh`

- [ ] **Step 1: Read the script, replace the launch line**

The script assembles per-phase spec dirs (KEEP all of that — the phase-copy behavior is a test harness, do not change spec content) and then launches `crest-spec run --spec-dir <dir> --remote-control --session-name "..."`. Replace that launch with a direct `claude` invocation from the workspace dir (claude as the human entry point is fine — it's the MCP server that must not shell out):

```bash
claude --permission-mode bypassPermissions \
  "Use the spec-generate skill to run a full crest-spec generation session for the spec in ${SPEC_DIR}. Work through every wave; do not stop for confirmation on destroys (this is a fixture run)."
```

Preserve any `--remote-control`/session-name plumbing if present by passing the same flags to `claude` itself. Ensure `.mcp.json` in the fixture workspace points at the freshly built `crest-spec` binary (the script or fixture already does this — verify, and add a `go build -o` step into the script if it relied on `crest-spec run` to self-locate).

- [ ] **Step 2: Shellcheck + commit**

Run: `bash -n scripts/run-phased-agent.sh`
Expected: no syntax errors.

```bash
git add scripts/run-phased-agent.sh
git commit -m "feat(scripts): phased e2e launches claude directly with the spec-generate skill"
```

---

### Task 10: Documentation — SPEC.md + README

**Files:**
- Modify: `SPEC.md`, `README.md`

- [ ] **Step 1: SPEC.md targeted rewrites** (keep surrounding sections intact)

- **Overview + §2 Architecture:** state the new shape: server = state engine + mechanical validation; orchestration = Claude Code workflows; server never calls LLMs or spawns processes. Remove agent/engine from the component map and startup sequence (§2.4: `store → spec → mcp.New(spec, stdin, stdout, log, cfg)`).
- **§3 Configuration:** drop the engine config block; document the trimmed Config.
- **§4 "The Agent Wrapper & Engine Layer":** replace the whole section with a short "§4 Orchestration Boundary" describing the prompt-out/verdict-in contract: spec_context returns prompts + invariants; spec_commit accepts files + invariant_checks + model and runs mechanical validations; spec_evolve returns a reflection prompt; spec_record_learnings ingests output; spec_finish returns reflection_prompt.
- **§5.2 Apply:** spec/apply is removed; describe the workflow-driven loop instead (reference `.claude/workflows/spec-generate.js`).
- **§7.1 Jobs:** removed — note jobs table is retained in schema but unused.
- **§8 The Plan/Apply/Dispatch/Retry Loop:** rewrite 8.1/8.2/8.4 to describe the loop as orchestrator-driven: generate (workflow sub-agent) → spec_commit (server validations + invariant verdicts) → rejected → spec_context (failure injected) → regenerate. 8.5/8.6 (state machine, resolution paths) survive with run_wave/dispatch references replaced by the workflow.
- **§9.3 MCP Tools:** delete entries for removed tools; add spec/record_learnings; update spec/commit and spec/evolve entries.
- **README.md:** update the quickstart (`crest-spec run` → "open Claude Code in your project and ask it to run the spec (spec-generate skill)"), tool list, and architecture blurb.

- [ ] **Step 2: Verify no stale references**

Run: `grep -n "run_prompt\|poll_result\|spec/dispatch\|spec/run_wave\|spec/apply\|spec/deep_review\|crest-spec run\|bugbot\|code_review" SPEC.md README.md`
Expected: no hits (except a historical-note mention if you added one deliberately).

- [ ] **Step 3: Commit**

```bash
git add SPEC.md README.md
git commit -m "docs: rewrite dispatch/jobs/orchestration sections for native workflow architecture"
```

---

### Task 11: Final verification — full suite + real end-to-end run

- [ ] **Step 1: Full local verification**

Run: `go build ./... && go vet ./... && go test ./... && gofmt -l . | (! grep .)`
Expected: all PASS, no unformatted files. Also: `go build -o crest-spec ./cmd/crest-spec` (refresh the repo-root binary used by `.mcp.json`).

- [ ] **Step 2: REAL end-to-end (no simulation — user requirement)**

From the crest-synth fixture, run phase 1 through the new path with real LLM calls and real file generation:

```bash
./scripts/run-phased-agent.sh 1   # or the script's documented phase-selection invocation
```

Watch for: session begins, waves dispatch via workflow sub-agents, spec_commit validations run (cargo fmt/build per fixture validations), files land in the fixture workspace, spec_finish reports counts, learnings recorded if reflection fired. Failure here is signal — debug with `spec_status`/`spec_wave_status`/`.crest-spec/state.db` via `crest-spec sql`, fix, re-run. Do NOT mark this plan complete on a failed e2e.

- [ ] **Step 3: Store the outcome in ICM and report**

```bash
icm store -t context-claude-mcp-server -c "Workflow-pivot implementation complete: subprocess dispatch removed, native spec-generate workflow + skill landed, phase-1 crest-synth e2e <result>" -i high
```

---

## Self-Review Notes

- **Spec coverage:** user asks = (1) stop shelling out via MCP → Tasks 1–5; (2) use built-in workflows with smaller models → Tasks 7–8 (sonnet default, haiku forbidden); (3) core validation loop preserved → commit-time `RunValidations` + `checkForFailure` untouched, retry feedback via `spec_context` untouched, invariant gate preserved via verdict ingestion (Task 1), wave verification (`VerifyWave`, TypeCheck/Test/project validations) untouched.
- **Known judgment calls encoded here:** jobs DB schema stays (no migration churn); `GenerateModel` config survives as hash/state label; parse.go deleted (nothing parses fences server-side anymore); amendment VERIFIED/FAILED marking survives (mechanical); ProposeAmendments deleted (orchestrator drafts amendments itself and uses spec/apply_amendments).
- **Type consistency:** `InvariantCheckInput` (spec) ↔ `specmod.InvariantCheckInput` (mcp) ↔ `invariant_checks` JSON; `Commit(ctx, sid, rid, files, notes, invariantChecks, model)` everywhere; `EvolvePrompt`/`RecordLearnings` on both `Spec` and `specHandler`; `FinishResult.ReflectionPrompt` ↔ skill step 6.
