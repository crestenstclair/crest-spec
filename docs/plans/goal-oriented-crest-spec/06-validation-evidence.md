# Workstream 6: Multi-Scope Validation and Behavioral Evidence Provenance

Status: **Draft**  
Depends on: WS1–WS5  
Enables: WS7, WS8, final completion  
Original scope: Workstreams 6 and 7

## Objective

Extend fail-closed validation from local resource checks to integrated project functionality and close the provenance gap in behavioral verification. Passing evidence must prove that the exact accepted source tree produced the observed behavior and that the same check rejects a controlled negative case.

## Requirements

- Support resource, dependency-contract, integration-wave, goal, project, regression, and behavioral validation scopes.
- Execute declared commands through crest-spec's controlled validation runner.
- Persist command, working directory, allowlisted environment description, stdout, stderr, files/artifacts, exit status, duration, parser result, classification, retry relationship, and consequences.
- Associate runs with exact declaration, source tree, executable/artifact, validation/witness, resource, capability, goal, attempt, and candidate hashes.
- Use executable acceptance scenarios wherever practical.
- Prevent local compilation from satisfying capability or goal completion.
- Re-run relevant evidence after impact invalidation.
- Retain `passed`, `failed`, `malformed`, and `theater` behavioral classifications with stronger provenance.

## Validation model

Every validation definition has a stable ID, scope, command, assertions/parser, applicability links, and optional timeout. Existing anonymous resource validation arrays remain readable during migration but are assigned deterministic snapshot-local IDs; new goal/project references require explicit named validations.

Validation runs are immutable events. Current validation status is a projection over the latest non-stale applicable run, not a mutable flag on the definition.

### Scope semantics

| Scope | Meaning |
|---|---|
| Resource | A resource candidate satisfies its local mechanical contract |
| Dependency contract | A provider and its consumers agree on interface/schema behavior |
| Integration wave | Resources accepted in a wave work in the whole project tree |
| Goal | Acceptance requirements for one goal pass |
| Project | Cross-goal completion and nonfunctional checks pass |
| Regression | Previously passing goal evidence is current after an impact |
| Behavioral | A real implementation exhibits behavior and a negative case does not |

## Behavioral witness declaration

```cue
project: witnesses: registration_round_trip: {
    scope:      "goal"
    goal:       "goal.registration"
    capability: "capability.create_account"
    command:    ["go", "test", "./e2e", "-run", "TestRegistrationWitness", "-json"]
    negativeCommand: ["go", "test", "./e2e", "-run", "TestRegistrationNegativeWitness", "-json"]
    workingDirectory: "."
    timeout: "2m"
    artifacts: ["bin/example-server"]
    observation: {
        kind:   "json_stdout"
        marker: "CREST_OBS"
        schema: {
            account_created: "bool"
            login_succeeds:  "bool"
        }
    }
    predicates: [
        {field: "account_created", op: "equals", member: true},
        {field: "login_succeeds",  op: "equals", member: true},
    ]
}
```

The negative command must use the same observation parser/schema and emit all predicate fields. It may run a declared stub/fixture, feature-disabled executable, or controlled negative input. It may not be an arbitrary caller-supplied observation.

## Controlled execution and provenance

For each witness:

1. Resolve and validate the declaration.
2. Calculate the relevant accepted candidate/source-tree hash.
3. Execute the real command with captured stdout/stderr/artifacts and timeout.
4. Hash declared executable/artifacts after execution.
5. Parse exactly one observation using the declared parser/schema.
6. Execute the negative command in an isolated temporary workspace where required.
7. Parse through the same mechanism and schema.
8. Evaluate predicates: real must pass and negative must fail.
9. Persist raw captures, observations, predicate results, hashes, and classification in one logical transaction.
10. Reconfirm the source/artifact hash has not changed during the run before making evidence current.

Temporary execution directories are ephemeral and never become project operational state. Durable evidence lives only in SQLite.

## Evidence and completion

An evidence record references its producing run and the exact hashes for which it is valid. Evidence can be `current`, `stale`, `failed`, or `superseded`. Historical evidence is never deleted when it becomes stale.

The completion projector consumes current evidence only. A relevant WS3 impact marks evidence stale and schedules the corresponding regression validation. A forced session finish cannot silently mark a goal complete; any override is a separate human audit event and leaves completion incomplete.

## Persistence ownership

WS6 owns:

- validation/witness definition snapshots
- validation runs and retry links
- command executions and bounded raw capture blobs
- source-tree, candidate, executable, artifact, and command hashes
- parsed observations and schema versions
- predicate results and classifications
- evidence records, applicability, currency, and invalidation links
- validation-to-resource/capability/goal/project consequences

Large raw output uses content-addressed blobs with stored full-content hashes. Configured display truncation never changes the retained classification or full hash.

## Tasks

### WS6-01 — Define named multi-scope validations

- Extend CUE and Go types with stable IDs, scopes, applicability, timeouts, and references.
- Validate scope-specific requirements and references.
- Migrate existing project/resource validation execution to the unified runner contract.

Acceptance: all scopes have deterministic definitions and existing fail-closed resource/wave behavior remains intact.

### WS6-02 — Define executable witnesses

- Add witness command, negative command, parser/schema, predicate, artifact, timeout, and applicability types.
- Validate command safety boundaries, parser completeness, predicate structure, and negative-case presence.
- Retain current predicate operators and malformed/theater semantics.

Acceptance: incomplete, structurally impossible, or non-falsifiable witness definitions fail before generation retry.

### WS6-03 — Implement the controlled runner

- Generalize command execution to return raw structured execution data.
- Add timeout, working-directory containment, environment allowlisting, artifact hashing, parser implementations, and source stability checks.
- Execute real and negative cases through the same parser.

Acceptance: the runner proves observation provenance or fails closed with an explicit infrastructure/definition classification.

### WS6-04 — Persist runs and evidence transactionally

- Add migrations, queries, repositories, content-blob use, and atomic run finalization.
- Link runs to attempts, candidates, plan operations, resources, capabilities, and goals.
- Preserve incomplete/crashed execution diagnostics without creating passing evidence.

Acceptance: no passing evidence exists without complete raw capture, hashes, parser output, predicates, and source linkage.

### WS6-05 — Integrate completion and regression

- Feed validation/evidence consequences into WS1 projection.
- Consume WS3 invalidations and enqueue regression checks.
- Update wave/finish gates to require applicable integration, behavioral, goal, and project evidence.
- Preserve explicit human visibility for forced finish without treating it as completion.

Acceptance: locally valid resources cannot complete a goal until integrated and behaviorally evidenced.

### WS6-06 — Replace caller-supplied behavioral verification

- Make engine-executed witnesses the canonical `spec/verify` behavior.
- Retain old observations only as legacy history; disable new caller-supplied real/stub acceptance.
- Add validation/evidence inspection and history APIs plus dashboard explorer through WS8.

Acceptance: every new Passed/Failed/Malformed/Theater verdict is tied to actual commands and exact accepted source/artifact hashes.

## Test plan

- Scope tests for resource, dependency, wave, goal, project, regression, and behavioral runs.
- Runner tests for successful command, non-zero exit, launch failure, timeout, invalid cwd, environment filtering, artifact missing/change, and source-tree mutation.
- Parser tests for missing/multiple markers, invalid JSON, schema mismatch, missing predicate fields, and large output.
- Behavioral matrix: real pass/negative fail, real fail, negative pass/theater, malformed real, malformed negative, and structurally invalid predicate.
- Store tests for atomic finalization, crash records, blob deduplication, evidence currency, and retry links.
- Completion tests proving local validity, integration, behavior, goal dependencies, project checks, and regression transitions.
- Security tests for path traversal, command definition validation, secret redaction, and bounded capture.
- Dashboard/API tests for raw evidence, provenance, classifications, consequences, and historical trends.

## Compatibility and rollout

Existing behavioral check and verification rows remain visible as legacy caller-supplied evidence but do not satisfy newly required current evidence after the canonical runner is enabled. Existing resource/project validations are translated into the unified runtime model without weakening launch-failure behavior.

## Exit gate

- Every validation scope is executable and persisted through one evidence model.
- Behavioral witnesses are engine-executed, falsification-gated, and source-provenant.
- Completion and regression consume only current evidence.
- Raw evidence and consequences are inspectable through all required surfaces.

