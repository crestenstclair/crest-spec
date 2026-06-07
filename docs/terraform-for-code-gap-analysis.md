# Terraform for Code: Gap Analysis and Path Forward

How close is crest-spec to the "Terraform for code generation" vision? What's
missing, what's working, and what are the highest-leverage improvements?

---

## The Terraform Mental Model

Terraform works because of five interlocking properties:

1. **Declarative source of truth.** You write `.tf` files describing desired
   state. Terraform never asks "what do you want to do?" — it computes the delta
   between desired and actual state.

2. **Reliable state tracking.** `terraform.tfstate` is the record of what exists
   in the real world. Every resource has an ID, a hash of its config, and
   metadata about its current state. This makes `plan` possible.

3. **Idempotent apply.** Running `apply` twice produces the same result. If a
   resource already matches its desired state, it's skipped. Only the delta is
   executed.

4. **Dependency-ordered execution.** Resources that depend on each other are
   created/updated/destroyed in the right order. Terraform builds a DAG and
   walks it.

5. **Constraint verification.** Terraform validates before applying: type
   checking configs, verifying provider constraints, checking that referenced
   resources exist. Failures are caught early.

---

## Where crest-spec Stands Today

### What's Working (Terraform Parity)

| Terraform Concept | crest-spec Implementation | Status |
|---|---|---|
| `.tf` files (declarative spec) | CUE files with DDD vocabulary | Working well |
| `terraform.tfstate` (state tracking) | SQLite with resources, generated_files, dependencies tables | Working |
| `terraform plan` (diff) | `spec/plan` — hash-based change detection with cascading | Working |
| `terraform apply` (execute) | `spec/apply` and manual `begin/next/context/commit` pipeline | Working (both modes) |
| Resource graph (DAG) | Dependency graph with topological sort and wave grouping | Working |
| Destroy (resource removal) | Auto-executed during Begin() | Working (just added) |
| Targeting (`-target`) | `BeginOpts.Target` with ancestor filtering | Working (just added) |
| Force (`-replace`) | `BeginOpts.Force` | Working (just added) |
| State locking | Single-row lock table with PID | Working |
| Drift detection | Content hash comparison on disk vs stored | Working |
| Audit trail | applies, apply_actions, generations tables | Working |
| Import (accept existing) | `spec/drift accept` | Working |

### What's Partially Working

| Concept | Gap | Impact |
|---|---|---|
| Constraint loop | Generate → Parse → Validate → Invariant → Review chain exists, but review step is basic string matching ("FAIL"/"PASS") | Medium — false positives/negatives in review gate |
| Wave verification | TypeCheck/TestCommand run between waves, but error attribution is just substring matching on file paths | Medium — errors may be attributed to wrong resource |
| State machine | All states defined, transitions work for happy path, but `completed` state is skipped (goes directly pending → dispatched → committed or rejected) | Low — functional but doesn't match spec diagram |
| Drift revert | Only "accept" works; "revert" returns unimplemented | Low — regenerate works as alternative |
| Progress reporting | No SSE streaming; agents poll with poll_result | Medium — works but wasteful |

### What's Missing (Critical Gaps)

These are the features where the Terraform analogy breaks down:

#### 1. No `terraform import` Equivalent for Existing Codebases

**The gap:** Terraform can import existing infrastructure into state without
recreating it. crest-spec has no way to take an existing codebase and produce
CUE spec files from it.

**Why it matters:** The hardest adoption barrier. Nobody starts from zero. Every
real user has existing code they want to bring under crest-spec management. Right
now, writing the CUE spec by hand for a 50-file codebase is a wall.

**Path forward:**
- `spec/import <directory>` — scans source files, infers DDD structure (what
  looks like an aggregate vs a value object vs a service), generates a skeleton
  CUE spec
- Use an LLM to classify: "Here's `VoiceAllocator.rs`. Is this a domain
  service, a repository, or an adapter? What does it depend on?"
- Write the generated_files and resources records to SQLite so the planner
  treats imported files as baseline state
- Conservative: import as "unmanaged" first (tracked but not regenerated), let
  the user opt resources into full management one at a time

**Effort:** Large. This is a whole sub-project.

#### 2. No `terraform workspace` / Environment Management

**The gap:** Terraform workspaces let you run the same config against different
environments (dev, staging, prod). crest-spec has no concept of environments
or configuration variants.

**Why it matters:** A synthesizer project might want "debug build with extra
logging" vs "release build with optimizations." A web app might want "local
dev with mocks" vs "deployed with real services."

**Path forward:**
- CUE already supports this naturally via constraints and defaults. A `mode`
  field in project meta could toggle behavior:
  ```cue
  project: meta: mode: "debug" | "release" | *"debug"
  project: meta: if mode == "debug" { rules: [..., "add tracing to every public method"] }
  ```
- The hash computation already includes meta, so changing mode would cascade
  correctly
- Workspaces = different state databases. `CREST_SPEC_STATE_DIR=.crest-spec/debug`

**Effort:** Small-medium. CUE does most of the work.

#### 3. No `terraform providers` / Plugin System

**The gap:** Terraform's power comes from providers — plugins that know how to
manage specific resource types. crest-spec has no plugin system. All resource
types (aggregate, service, adapter, etc.) are hardcoded in the CUE schema and
prompt templates.

**Why it matters:** Different languages, frameworks, and architectures need
different resource types and prompt strategies. A React app needs Components,
Hooks, Stores. A Go service needs Handlers, Middleware, Repositories. These
shouldn't all live in one monolithic schema.

**Path forward:**
- Resource kind definitions as CUE packages that can be imported:
  ```cue
  import "crest-spec.dev/providers/rust-ddd"
  import "crest-spec.dev/providers/react-spa"
  ```
- Each provider defines: resource types, prompt templates, validation commands,
  file patterns, folder conventions
- The core engine stays generic — it just needs a resource with a prompt and
  a way to validate the output

**Effort:** Large. Requires rethinking the prompt system.

#### 4. No `terraform console` / Interactive Debugging

**The gap:** Terraform console lets you evaluate expressions interactively.
crest-spec has `spec/sql` for raw queries but no way to interactively explore
the resource graph, test prompts, or debug why a resource keeps failing.

**Why it matters:** When a resource fails the constraint loop 3 times and gets
rejected, the user needs to understand WHY. Currently they read error messages
and guess.

**Path forward:**
- `spec/inspect <resource_id>` — shows the complete prompt that would be
  built for this resource (system + resource + runtime context), the hash
  breakdown (what changed and why), and the dependency chain
- `spec/replay <generation_id>` — re-runs a specific generation from the audit
  trail with the exact same prompt, useful for debugging non-deterministic
  failures
- `spec/prompt <resource_id>` — builds and returns the prompt WITHOUT dispatching,
  so the user can review what the LLM would see
- Dashboard enhancement: visual dependency graph with color-coded state (green =
  committed, red = errored, yellow = pending)

**Effort:** Medium. Most data already exists in SQLite.

#### 5. No Modules / Composition Across Projects

**The gap:** Terraform modules let you package and reuse infrastructure patterns.
crest-spec has no way to share resource patterns across projects.

**Why it matters:** Every DDD project has similar patterns: the aggregate pattern,
the repository pattern, the adapter pattern. These should be shareable templates
with project-specific customization.

**Path forward:**
- CUE packages as modules:
  ```cue
  import "github.com/org/crest-patterns/cqrs"
  project: contexts: Orders: cqrs.#BoundedContext & {
      aggregates: Order: cqrs.#EventSourcedAggregate & {
          // project-specific fields
      }
  }
  ```
- CUE's package system handles versioning and imports natively
- The prompt system would need to support inherited prompts from module
  definitions

**Effort:** Medium. CUE does the heavy lifting.

---

## Highest-Leverage Improvements (Priority Order)

### Tier 1: Make What Exists Reliable (Next Sprint)

These don't add new features — they make the existing Terraform analogy actually
work end-to-end without surprises.

1. **Fix the `completed` state gap.** The state machine should transition through
   `completed` before `committed`. Right now Commit() goes directly from
   dispatched to committed/rejected. Add a `Complete()` method that writes files
   and transitions to `completed`, then Commit() validates and transitions to
   `committed`. This matches the Terraform model where `apply` writes, then
   verifies, then marks done.

2. **Structured review output.** The constraint loop's review step does string
   matching for "FAIL"/"PASS" in LLM output. This is fragile. Use structured
   output (JSON schema) for review results: `{passed: bool, findings: [{severity,
   description, file, line}]}`. The engine already supports this via the
   `--output-format` flag or response parsing.

3. **Proper error attribution in wave verification.** The current substring
   matching is too naive. Parse common compiler output formats:
   - Rust: `error[E0433]: failed to resolve: ... --> src/Synth/Voice.rs:42:5`
   - Go: `./internal/spec/session.go:42:5: undefined: foo`
   - TypeScript: `src/components/App.tsx(42,5): error TS2304`
   Extract file:line, map to resource via generated_files table.

4. **Fallback to TypeCheckCommand/TestCommand.** When a resource has no declared
   validations, the constraint loop should fall back to the global commands from
   config. This is in the spec but not implemented — the current code only runs
   validations declared on the resource.

### Tier 2: Close the Adoption Gap (Next Month)

5. **`spec/inspect` tool.** Show the full prompt, hash breakdown, and dependency
   chain for any resource. Zero new infrastructure — just reads from SQLite and
   the prompt builder. Huge for debugging.

6. **`spec/import` MVP.** Scan a directory, use an LLM to classify files into
   resource types, generate skeleton CUE. Doesn't need to be perfect — just good
   enough that the user can hand-correct the output. The 80/20 rule applies
   hard here.

7. **Environment/mode support.** Add a `mode` field to project meta, wire it
   into hash computation. CUE constraints handle the conditional logic. Small
   change, big usability win.

### Tier 3: Ecosystem (Next Quarter)

8. **Provider/plugin system.** Define resource types as CUE packages. This is
   the multiplier that makes crest-spec useful beyond DDD-structured Rust
   projects.

9. **Module system.** Shareable patterns via CUE packages. Depends on the
   provider system being stable.

10. **Visual dashboard.** Dependency graph visualization, real-time state
    tracking during applies, generation history browser. The API endpoints
    already exist — this is a frontend project.

---

## The Core Insight

Terraform works because it's boring. `plan` is deterministic. `apply` is
idempotent. State is reliable. The magic is in the providers, not the core.

crest-spec's core loop (load spec → diff state → build prompt → dispatch →
validate → commit) is the Terraform equivalent. The LLM is the "provider" —
it's the thing that knows how to create/modify a specific resource type.

The gap isn't in the architecture — it's in reliability and adoption:

- **Reliability:** The constraint loop needs to be airtight. Every failure mode
  needs a clear resolution path. The user should never be confused about why
  something failed or what to do about it.

- **Adoption:** Nobody wants to write 500 lines of CUE before seeing their first
  generated file. `spec/import` is the unlock. So is having pre-built providers
  for common stacks (React, Go, Rust, Python).

The Terraform analogy is the right one. The question is: how do you make
`terraform init` take 5 minutes instead of 5 hours?

---

## Concrete Next Steps

| # | Action | Files | Effort |
|---|--------|-------|--------|
| 1 | Add `completed` state to Commit flow | session.go | 1 day |
| 2 | Structured review output (JSON schema) | loop.go, engine.go | 2 days |
| 3 | Better error attribution (parse compiler output) | session.go | 1 day |
| 4 | Fallback to TypeCheck/TestCommand in constraint loop | loop.go | 0.5 day |
| 5 | `spec/inspect` tool | query.go, tools.go | 1 day |
| 6 | `spec/import` MVP | new import.go, tools.go | 1 week |
| 7 | Mode/environment support | config.go, cue types | 2 days |
| 8 | Provider system design doc | docs/ | 2 days |

---

*Written 2026-06-07 during overnight drift fix session.*
