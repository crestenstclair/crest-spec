You are improving the crest-spec project:

https://github.com/crestenstclair/crest-spec

Your task is to evolve crest-spec into a more reliable, goal-oriented, context-rich, observable, and empirically improvable framework for building complete software projects with coding agents.

Do not treat this as a documentation-only exercise. Inspect the existing architecture, specifications, implementation, tests, dashboard, SQLite persistence, MCP primitives, and agent-integration model. Propose and implement coherent changes where practical while preserving crest-spec’s core strengths:

* Declarative project specifications
* Resource dependency graphs
* Incremental reconciliation
* Deterministic validation
* Explicit lifecycle state
* SQLite-backed persistence
* Separation between generation and governance
* Model- and host-independent orchestration
* Human control over destructive changes

## Primary objective

Reorient crest-spec around successfully completing project functionality.

The system should not merely generate isolated resources that satisfy local structural checks. It should continuously understand, represent, and pursue the end goal of the project, including its required user-visible and system-level functionality.

A successful crest-spec session should answer:

1. What is the project trying to accomplish?
2. What functionality must exist before the project is considered complete?
3. Which parts of that functionality are already implemented?
4. Which parts are missing, broken, blocked, or insufficiently verified?
5. Which resource changes will make the most progress toward project completion?
6. How do we know that the completed resources work together as a functioning project?
7. What context, execution configuration, and evidence produced the current result?
8. How can this information be inspected through the crest-spec dashboard?

The project goal must remain visible throughout planning, context generation, implementation, validation, retry, verification, persistence, and dashboard reporting.

## Design principle

crest-spec should act as a declarative reconciliation and verification engine for agent-generated software.

It should govern:

* What the intended system is
* What functionality the intended system must provide
* Which resources must exist
* How those resources depend on one another
* Which files and artifacts implement them
* What has changed
* What work remains
* What evidence proves that the intended functionality works
* What information must be persisted for auditability and diagnosis
* How project status and generation activity are presented in the dashboard

The coding agent proposes implementations. crest-spec determines whether those implementations advance the project toward its declared goal and whether they are safe to accept.

## Persistence principle

Operational state, manifests, evidence, execution metadata, and observability data must be stored in SQLite using the existing SQLite infrastructure and SQLite primitives exposed through crest-spec’s MCP.

Do not introduce new flat files for runtime manifests, execution records, context records, observability state, completion state, or verification evidence.

Avoid approaches such as:

* JSON manifest files
* YAML manifest files
* Per-run metadata files
* Hidden observability directories
* Generated status documents
* Session-specific diagnostic files committed into the project
* Sidecar files placed beside generated source code

Flat files gum up the project, complicate cleanup, create unnecessary diffs, and blur the boundary between project artifacts and crest-spec operational state.

The project specification itself may remain file-based where appropriate. Runtime and operational records should live in SQLite.

All newly persisted information should be accessible through:

* Existing or extended SQLite repositories
* Existing or extended MCP SQLite primitives
* Programmatic crest-spec APIs
* CLI inspection commands where useful
* The crest-spec dashboard

## Workstream 1: Introduce explicit project goals

Add a first-class representation of project-level goals.

The specification should support concepts such as:

* Project mission
* Target users or actors
* User-visible capabilities
* System-level outcomes
* Functional requirements
* Nonfunctional requirements
* Completion criteria
* Explicit exclusions and non-goals
* End-to-end acceptance scenarios
* Dependencies between goals
* Goal priority
* Goal status
* Evidence required to declare a goal complete

A project goal should not be a decorative prose field. It must participate in planning, context construction, validation, persistence, dashboard reporting, and completion decisions.

Consider a structure conceptually similar to:

```cue
project: {
    mission: "Allow users to..."
    actors: [...]
    goals: {
        account_registration: {
            description: "..."
            priority: "required"
            dependsOn: [...]
            acceptance: [...]
            evidence: [...]
        }
    }
    completion: {
        requiredGoals: [...]
        projectChecks: [...]
    }
}
```

Do not follow this exact syntax unless it fits the existing design. Develop a representation consistent with crest-spec’s architecture and CUE conventions.

The dashboard should display:

* Project mission
* Required and optional goals
* Goal dependencies
* Goal status
* Completion evidence
* Missing functionality
* Blockers
* Regressed goals

## Workstream 2: Connect goals to resources

Each resource should declare or derive which project goals and capabilities it contributes to.

The system should be able to trace:

```text
project goal
→ capability
→ acceptance behavior
→ architectural resource
→ generated files
→ validations
→ observed evidence
```

Add bidirectional traceability where possible:

* From a goal, show all resources involved in completing it.
* From a resource, show the goals and capabilities it supports.
* From a failed validation, show which goals are blocked.
* From a changed resource, show which completed goals may need re-verification.
* From the current plan, show expected project-level progress.

Avoid treating resource completion as equivalent to project completion.

A resource can be locally valid while its associated capability remains incomplete because integrations, callers, adapters, UI, persistence, or end-to-end behavior are still missing.

Persist this traceability in SQLite rather than duplicating it into generated flat files.

Expose goal-to-resource and resource-to-goal relationships in the dashboard.

## Workstream 3: Make planning goal-directed

Improve the planner so that it does more than calculate create, modify, and destroy operations.

Planning should identify the work required to close the gap between the current implementation and the project’s declared functionality.

The plan should include:

* The target goal or capability
* Why each resource operation is needed
* What project-level behavior it enables
* Current blockers
* Missing integrations
* Required validations
* Expected completion evidence
* Dependencies and execution order
* Whether a change is structural, corrective, integrative, or verification-related

The planner should prioritize completing coherent vertical slices of functionality over producing disconnected architectural components.

For example, when a feature requires a domain object, service, port, adapter, persistence implementation, endpoint, and end-to-end validation, the plan should represent that entire path rather than declaring success after only the domain object exists.

Add a project-level completion model that distinguishes:

* Declared
* Planned
* Partially implemented
* Locally validated
* Integrated
* Behaviorally verified
* Complete
* Blocked
* Regressed

Persist planning rationale, status transitions, blockers, and expected goal impact in SQLite.

The dashboard should show:

* Active plans
* Goals targeted by each plan
* Resource operations
* Execution waves
* Blockers
* Expected progress
* Actual progress
* Plan failures
* Replanning decisions

## Workstream 4: Build substantially richer sub-agent context

Improve the sub-agent model so generators receive enough context to produce working, integrated code.

Do not optimize primarily for the smallest possible prompt. Optimize for sufficient, relevant, well-structured context.

Each sub-agent context should include, when relevant:

### Project-level context

* Project mission
* Target users and actors
* Active project goals
* Completion criteria
* Relevant nonfunctional requirements
* Explicit non-goals
* Current project status
* Current implementation gaps
* The specific capability the resource supports

### Architectural context

* Relevant bounded context
* Resource declaration
* Resource type and architectural responsibility
* Direct dependencies
* Important transitive dependencies
* Dependents and expected consumers
* Ports, contracts, schemas, and interfaces
* Cross-resource invariants
* Naming and organizational conventions
* Architectural decision records
* Applicable repository instructions

### Code context

* Existing implementation files
* Related interfaces and types
* Call sites
* Relevant tests
* Neighboring implementations that establish conventions
* Build configuration
* Package or module metadata
* Database schemas or migrations
* API schemas
* Generated-code ownership information
* Relevant recent diffs

### Execution context

* Exact task and expected outcome
* Files the agent may modify
* Files it should not modify
* Available tools
* Build and test commands
* Required validations
* Previous failed attempts
* Raw validation output
* Known blockers
* Accepted prior implementation patterns
* Constraints on scope and diff size

### Integration context

* How the generated resource will be called
* What calls it
* What data enters and leaves it
* Required side effects
* Error behavior
* Persistence behavior
* Expected end-to-end scenarios
* Integration wave and downstream consequences

The context builder should select relevant information intelligently rather than dumping the entire repository indiscriminately.

Implement explicit context budgeting and prioritization. Preserve the most important information first:

1. Project goal and task
2. Behavioral acceptance criteria
3. Resource contract
4. Direct dependencies and consumers
5. Relevant existing code
6. Failure evidence
7. Conventions and examples
8. Broader repository background

The context should explain not only what the resource is, but why it exists and what working project behavior depends on it.

## Workstream 5: Add SQLite-backed context manifests and observability

Make generated context inspectable, queryable, and reproducible.

For every generation attempt, persist a context manifest containing:

* Project goal identifiers
* Capability identifiers
* Resource identifier
* Planner rationale
* Included specification sections
* Included files and file hashes
* Included dependency contracts
* Included consumer contracts
* Included test and validation evidence
* Included previous failures
* Included project conventions
* Template version and hash
* Context-selection strategy
* Approximate token allocation by section
* Total estimated context size
* Truncated material
* Omitted material
* Reasons for inclusion
* Reasons for omission
* Host and model execution references

These manifests must be stored in SQLite using the existing SQLite persistence layer and SQLite primitives exposed through the MCP.

Do not create a context manifest as:

* A JSON file
* A YAML file
* A Markdown file
* A hidden file
* A per-resource sidecar
* A session directory
* A generated artifact in the project tree

Model the context manifest using normalized SQLite records where appropriate. Consider entities such as:

* Context manifest
* Context section
* Included source
* Omitted source
* Token allocation
* File snapshot reference
* Specification fragment reference
* Failure evidence reference
* Selection decision
* Generation attempt

Reuse existing database conventions, repositories, transaction boundaries, migration patterns, and MCP primitives.

Provide commands or APIs that allow developers and agents to inspect exactly what a sub-agent received and why it was included.

The dashboard must include a context inspector that can show:

* The complete structured context for an attempt
* Context sections and their sizes
* Included files
* Included specification fragments
* Included dependencies and consumers
* Previous failures included
* Truncated or omitted material
* Selection rationale
* Template version
* Context hash
* Related generation result
* Related validation result

The dashboard should make it easy to compare the contexts of two attempts, especially when one failed and another succeeded.

This information is necessary for diagnosing failures that are actually context failures rather than model failures.

## Workstream 6: Strengthen end-to-end validation

Preserve crest-spec’s fail-closed mechanical validation model, but extend validation beyond individual resources.

Introduce several scopes of validation:

* Resource-level validation
* Dependency-contract validation
* Integration-wave validation
* Goal-level acceptance validation
* Project-level completion validation
* Regression validation for previously completed goals

Project functionality should be verified through executable acceptance scenarios wherever possible.

A goal must not be considered complete solely because all of its resources compile independently.

Completion should require evidence that the relevant resources work together and produce the intended observable behavior.

Persist validation runs, raw results, classifications, affected goals, affected resources, and completion consequences in SQLite.

The dashboard should show:

* Validation scope
* Commands executed
* Exit status
* Evidence produced
* Goals affected
* Resources affected
* Failure classification
* Retry relationship
* Regression impact
* Historical validation trends

## Workstream 7: Close the behavioral-verification provenance gap

The current behavioral-verification model accepts caller-supplied real and stub observations. It can determine whether the supplied values satisfy the predicates and whether a degenerate stub also passes, but the engine does not independently establish that the real observations were emitted by the committed implementation.

Strengthen this evidence chain.

Where practical, behavioral witnesses should be declared in the specification and executed by crest-spec or by a tightly controlled verifier.

The system should:

1. Execute the declared witness against the committed implementation.
2. Capture raw stdout, stderr, files, exit status, and other declared observations.
3. Parse observations using a declared schema.
4. Associate the evidence with the exact source tree, executable, command, and resource hashes.
5. Execute or construct the declared degenerate or negative case.
6. Parse it through the same observation mechanism.
7. Confirm that the real implementation passes.
8. Confirm that the degenerate implementation fails.
9. Persist the raw evidence and verification metadata.

The behavioral gate should prove not merely that supplied values satisfy predicates, but that those values came from the exact implementation being accepted.

Maintain the existing classification of:

* Passed
* Failed
* Malformed
* Theater

Strengthen the provenance of each classification.

Store witness definitions, execution records, captured evidence, parsed observations, hashes, and classifications in SQLite.

Do not emit behavioral-verification evidence as flat files in the project.

The dashboard should expose:

* Witness definition
* Exact implementation hash
* Command executed
* Real observation
* Degenerate observation
* Raw evidence
* Predicate results
* Final classification
* Related project goal
* Related resource
* Historical verification runs

## Workstream 8: Add SQLite-backed reproducible execution manifests

Because crest-spec delegates model execution to an external host, two hosts may interpret the same scoped task differently.

Persist an execution manifest for every generation attempt, including:

* Agent host name and version
* Exact model identifier
* Provider
* Inference settings
* System instructions
* Tool availability
* Tool permissions
* Agent configuration
* Prompt-template hashes
* Context-manifest identifier and hash
* Retry number
* Session identifier
* Parent attempt identifier
* Start and completion timestamps
* Candidate output hashes
* Modified file set
* Validation result
* Goal progress result
* Failure classification
* Final acceptance or rejection status

Execution manifests must be stored in SQLite using the existing SQLite persistence and MCP primitives.

Do not create execution manifests as:

* JSON files
* YAML files
* Markdown reports
* Hidden metadata files
* Session folders
* Sidecar files beside source code
* Files checked into the repository

Reuse and extend existing SQLite primitives instead of building a parallel storage system.

Ensure execution-manifest records have explicit relationships to:

* Sessions
* Plans
* Goals
* Resources
* Context manifests
* Agent attempts
* Candidate file versions
* Validation runs
* Behavioral evidence
* Retry chains
* Final commits

Do not couple the crest-spec core to a particular model provider. The goal is auditability and reproducibility, not provider ownership.

The dashboard must include an execution inspector that shows:

* Host and model information
* Inference configuration
* Tools and permissions
* System instructions
* Template hashes
* Context manifest
* Candidate changes
* Validation outcomes
* Retry chain
* Goal progress
* Acceptance or rejection reason

The dashboard should support comparing execution attempts across hosts, models, templates, context policies, and retry iterations.

## Workstream 9: Introduce eval-driven improvement

crest-spec currently has a strong retry loop based on concrete validation failures. Preserve this, but distinguish it from systematic prompt, context, planner, and generator evaluation.

Create an evaluation model that can measure whether changes to templates, context-selection policies, planning strategies, or agent configurations improve results across many tasks.

Support datasets composed of historical or curated generation cases:

```text
project specification
+ project goal
+ resource declaration
+ relevant repository state
+ generated context
+ candidate changes
+ validation results
+ retry count
+ accepted implementation
```

Measure outcomes such as:

* Commit acceptance rate
* Goal-completion rate
* Integration success
* Behavioral-verification success
* Number of retries
* Regression rate
* Diff size
* Token cost
* Execution time
* Human intervention rate
* Security and static-analysis results

Separate:

* Operational memory learned from an individual failure
* Reusable guidance promoted across similar resources
* Empirically validated template improvements
* Empirically validated context-policy improvements
* Empirically validated planning improvements

A promoted learning should not automatically be assumed beneficial. It should be testable against historical or held-out cases.

Store evaluation cases, runs, configurations, metrics, comparisons, and conclusions in SQLite.

Do not materialize evaluation datasets or run records as loose files inside the project unless an explicit import or export command is requested.

The dashboard should provide:

* Evaluation runs
* Compared configurations
* Metrics
* Historical trends
* Regressions
* Winning and losing variants
* Cost and retry comparisons
* Goal-completion comparisons
* Links to the underlying context and execution manifests

## Workstream 10: Reduce excessive dependence on one domain model

DDD-oriented resources are a major strength, but avoid making all project functionality fit artificially into aggregates, entities, ports, and adapters.

Evaluate whether the resource system needs clearer first-class support for other software concerns, such as:

* User interfaces and screens
* User journeys
* HTTP and RPC endpoints
* Scheduled jobs
* Workflows and state machines
* Data pipelines
* Infrastructure
* Configuration
* Observability
* Security policies
* Database migrations
* Events and message schemas
* External-system integrations
* Documentation
* Deployment artifacts

Do not discard the DDD model. Extend the system so projects that are not primarily domain-heavy backends can still express their goals naturally.

The generic asset mechanism should remain available, but core recurring software concepts should not all require unstructured escape hatches.

The dashboard should render these resource types clearly and show how each contributes to project goals.

## Workstream 11: Preserve and improve change reconciliation

Maintain crest-spec’s existing strengths around:

* Effective hashes
* Incremental planning
* Dependency propagation
* Parallel dependency waves
* Generated-file ownership
* Human-gated deletion
* Mechanical commit validation

Extend change analysis so modifications also trigger the appropriate goal-level and project-level regression checks.

A declaration change should identify:

* Directly affected resources
* Structurally affected dependents
* Potentially affected capabilities
* Previously completed goals requiring re-verification
* Project completion status that may now be invalid

Persist change-impact analysis and regression relationships in SQLite.

The dashboard should display the complete impact chain from a changed declaration or file to affected resources, capabilities, goals, validations, and completion state.

## Workstream 12: Improve failure classification

Not every failed generation is a prompting failure.

Classify failures into categories such as:

* Missing project intent
* Missing context
* Incorrect context selection
* Stale context
* Context truncation
* Ambiguous resource contract
* Architectural inconsistency
* Implementation error
* Integration error
* Invalid validation
* Behavioral theater
* Tool failure
* Host failure
* Model failure
* Unsupported project pattern

Use these classifications to determine the next action:

* Expand or revise context
* Repair the specification
* Re-plan the dependency graph
* Retry the same generator
* Use a specialized generator
* Escalate to a human
* Improve a validation
* Reject an invalid project goal

Do not repeatedly retry a model when the root cause is an incomplete specification or insufficient context.

Persist failure classifications, evidence, corrective action, and eventual resolution in SQLite.

The dashboard should make failure causes visible and allow filtering or grouping by:

* Failure category
* Project goal
* Resource type
* Host
* Model
* Context-selection strategy
* Template version
* Validation type
* Resolution

## Workstream 13: Introduce specialized sub-agent roles

Avoid relying on a single universal coding prompt.

Support specialized roles where appropriate:

* Project-goal analyst
* Planner
* Resource implementer
* Integration implementer
* Test generator
* Behavioral-witness author
* Failure triage agent
* Architecture reviewer
* Security reviewer
* Minimal-diff repair agent
* Project-completion reviewer

Each role should receive context shaped for its responsibility.

Specialization must not fragment ownership. crest-spec remains the shared source of truth for project goals, state, evidence, and completion.

Persist agent-role assignments, attempts, handoffs, results, and failure classifications in SQLite.

The dashboard should show:

* Which role performed each attempt
* What context it received
* What output it produced
* What validations followed
* Why it succeeded or failed
* How work moved between roles

## Workstream 14: Make the dashboard a first-class operational interface

All new project-goal, planning, context, execution, validation, evaluation, evidence, and failure-classification functionality must be represented in the crest-spec dashboard.

Do not implement these capabilities solely as database tables or CLI output.

The dashboard should provide at least the following views.

### Project overview

* Mission
* Target users
* Required capabilities
* Goal status
* Overall completion state
* Current blockers
* Regressed functionality
* Recommended next work

### Goal explorer

* Goal description
* Dependencies
* Contributing resources
* Acceptance scenarios
* Validation status
* Evidence
* Blockers
* Completion history

### Resource explorer

* Resource declaration
* Supported goals
* Dependencies
* Consumers
* Generated files
* Current state
* Validation history
* Context history
* Execution history

### Plan explorer

* Goal-directed rationale
* Operations
* Dependency waves
* Expected progress
* Actual progress
* Blockers
* Replanning history

### Context inspector

* Context manifest stored in SQLite
* Included sections
* Included files
* Dependencies and consumers
* Selection rationale
* Token allocation
* Omitted or truncated material
* Template information
* Context comparison between attempts

### Execution inspector

* Execution manifest stored in SQLite
* Host and model
* Inference settings
* Tools and permissions
* System instructions
* Candidate changes
* Retry relationships
* Validation outcome
* Acceptance or rejection reason

### Validation and evidence explorer

* Resource validations
* Integration validations
* Goal validations
* Project validations
* Behavioral witnesses
* Raw evidence
* Provenance
* Theater detection
* Regression impact

### Evaluation dashboard

* Historical success rates
* Goal-completion rates
* Retry counts
* Context-policy comparisons
* Template comparisons
* Cost and duration
* Regressions
* Human intervention rates

### Failure analysis

* Failure categories
* Root-cause trends
* Context-related failures
* Specification-related failures
* Integration failures
* Host and model failures
* Corrective actions
* Resolution rates

All dashboard data should be read from the canonical SQLite-backed state. Do not implement a second dashboard-specific persistence system.

The dashboard should be capable of deep-linking between related records, such as:

```text
goal
→ plan
→ resource
→ generation attempt
→ context manifest
→ execution manifest
→ candidate files
→ validation
→ behavioral evidence
→ final completion state
```

## Required developer experience

Provide commands, APIs, MCP operations, and dashboard views that make the following easy to understand:

* What is the project’s end goal?
* Which required capabilities remain incomplete?
* What is the current completion state?
* Why is a particular resource being generated?
* What context did its agent receive?
* Why was each context item selected?
* What information was omitted or truncated?
* Which project goal does the resource support?
* Which execution environment produced the result?
* Which validation rejected it?
* Was the failure caused by implementation, specification, context, integration, host, or model behavior?
* What evidence proves that a goal is complete?
* Which previously completed goals were invalidated by a change?
* What should be worked on next?
* How can two attempts be compared?
* How can a failed attempt be traced through its context, execution configuration, output, and validation result?

Prefer explainable status over opaque numeric scoring. A completion percentage may be useful, but it must be accompanied by concrete capability and evidence status.

## SQLite and MCP implementation requirements

Before introducing persistence changes, inspect:

* Existing SQLite schema
* Existing migration system
* Existing repository abstractions
* Existing transaction boundaries
* Existing session and resource tables
* Existing MCP SQLite primitives
* Existing dashboard data-access patterns

Extend these systems rather than creating replacements.

New data should be modeled with explicit identifiers and relationships.

Consider tables or equivalent persistence concepts for:

* Project goals
* Goal dependencies
* Goal-resource relationships
* Goal status history
* Plans and plan operations
* Context manifests
* Context-manifest sections
* Context sources
* Context omissions
* Generation attempts
* Execution manifests
* Candidate outputs
* Validation runs
* Behavioral evidence
* Failure classifications
* Evaluation cases
* Evaluation runs
* Evaluation metrics
* Dashboard status projections

Use SQLite transactions where multiple records form one logical attempt or verification event.

Use the MCP’s existing SQLite primitives for agent-facing reads and writes rather than adding a separate file-based protocol.

Add migrations and compatibility handling for existing crest-spec databases.

Avoid storing unnecessarily duplicated large blobs. Use hashes, references, normalized records, and content-addressed snapshots where suitable, while retaining enough information for reliable diagnosis and reproduction.

## Implementation approach

Begin by studying:

* README and public positioning
* `SPEC.md`
* Agent integration documentation
* CUE schemas
* Planner and graph code
* Prompt and context-building code
* Session persistence
* Existing SQLite schema
* Existing SQLite repositories
* Existing MCP SQLite primitives
* Dashboard architecture and data access
* Validation execution
* Behavioral-verification implementation
* Learning and template-promotion logic
* Existing examples and generated reference systems
* Current test coverage

Then produce:

1. A concise diagnosis of the current architecture.
2. A proposed target architecture.
3. A staged implementation plan.
4. Schema changes.
5. SQLite migrations.
6. MCP primitive changes.
7. State and persistence changes.
8. Planner changes.
9. Context-builder changes.
10. Validation and evidence changes.
11. Dashboard changes.
12. CLI and API changes.
13. Tests.
14. Migration and compatibility considerations.
15. Updated documentation and examples.

Prefer incremental, reviewable changes over a broad rewrite.

## Acceptance criteria

The improvement is successful when all of the following are true:

1. A crest-spec project has an explicit, machine-readable project mission and set of completion goals.
2. Goals are traceable to capabilities, resources, files, validations, and evidence.
3. The planner can explain how each planned operation advances project functionality.
4. Sub-agents receive project intent, acceptance criteria, dependency contracts, consumers, relevant code, integration requirements, and prior failure evidence.
5. Context manifests make every sub-agent input inspectable.
6. Context manifests are stored in SQLite through the existing persistence and MCP primitives.
7. No context-manifest flat files are introduced into the project.
8. Resource validity is not confused with feature or project completion.
9. Goal-level and project-level validations can run.
10. Previously completed goals are re-verified when affected resources change.
11. Behavioral evidence is tied mechanically to the committed implementation.
12. Behavioral evidence and provenance are persisted in SQLite.
13. Execution manifests capture the host, model, tools, settings, context, candidate, and result.
14. Execution manifests are stored in SQLite through the existing persistence and MCP primitives.
15. No execution-manifest flat files are introduced into the project.
16. Failures are classified so the system can distinguish context, specification, integration, host, model, and implementation problems.
17. Historical sessions can be used as an evaluation dataset.
18. Template, planner, and context-policy changes can be compared empirically.
19. DDD remains well supported while non-DDD project functionality can be represented naturally.
20. The system can clearly report whether the intended project functionality is complete, incomplete, blocked, or regressed.
21. Existing deterministic validation, dependency planning, incremental reconciliation, and deletion safeguards remain intact.
22. All major new functionality is exposed in the crest-spec dashboard.
23. Dashboard data is read from the canonical SQLite-backed state.
24. The dashboard does not maintain a separate persistence model.
25. Developers can trace a goal through planning, generation context, execution, candidate changes, validation, evidence, and completion.
26. Existing crest-spec databases can be migrated safely.

## Non-goals

Do not:

* Turn crest-spec into an LLM provider SDK.
* Require users to train foundation models.
* Add DSPy as a dependency or integration target.
* Replace deterministic validation with model judgment.
* Declare project completion based only on generated file presence.
* Maximize prompt size without relevance filtering.
* Hide project intent inside unstructured prompt text.
* Allow generator agents to decide unilaterally whether their work is acceptable.
* Rewrite the entire project without a migration strategy.
* Introduce abstractions that cannot be validated through concrete examples and tests.
* Store context manifests as project files.
* Store execution manifests as project files.
* Store runtime observability state as project files.
* Introduce JSON, YAML, Markdown, or hidden sidecar files for operational state.
* Create a second persistence layer specifically for the dashboard.
* Bypass the existing SQLite primitives in the MCP.
* Add database records without making the corresponding operational information visible in the dashboard.

## Guiding principle

The final system should optimize for this outcome:

> Given a declared project goal, crest-spec can determine what functionality is missing, provide coding agents with enough relevant context to implement it correctly, verify that the resulting components work together, preserve trustworthy SQLite-backed evidence of how and why the result was produced, and continue reconciling the codebase until the intended project functionality is complete.

Treat prompts as one implementation detail inside a larger system of goals, specifications, context, tools, SQLite-backed state, validation, evidence, evaluation, and dashboard observability.
