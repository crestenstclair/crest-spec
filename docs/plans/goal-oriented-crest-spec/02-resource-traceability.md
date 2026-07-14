# Workstream 2: DDD Traceability and Boundary/Artifact Profiles

Status: **Draft**  
Depends on: WS1  
Enables: WS3–WS8  
Original scope: Workstreams 2 and 10

## Objective

Connect project functionality to the existing DDD architecture that implements it. Extend ports, adapters, and assets just enough to express delivery and operational concerns without introducing a second, overlapping resource model.

The complete proposed composition is illustrated in the [goal-oriented crest-synth target sample](examples/README.md).

The required trace is:

```text
goal -> capability -> acceptance scenario -> resource -> generated file
     -> validation/evidence -> current completion consequence
```

Every edge must be inspectable in both directions.

## Requirements

- Every generatable resource may declare one or more capability contributions and explain its responsibility.
- A goal view lists capabilities, contributing resources, files, validations, evidence, missing contributions, and blockers.
- A resource view lists the capabilities and goals it supports.
- Failed validations identify affected capabilities and goals.
- Changed resources identify completed goals that may require re-verification.
- Resource settlement never implies capability or goal completion.
- Existing DDD resource kinds retain their current IDs, semantics, dependency behavior, and prompt support.
- Goals and capabilities never generate files directly and never duplicate aggregate/service/port contracts.
- Workflows and pipelines use application services; state machines use aggregates; event/message schemas use value objects and port contracts.
- Endpoints, screens, scheduled jobs, message handlers, devices, and external integrations use boundary profiles on adapters implementing ports.
- Configuration, migrations, infrastructure, observability, security deployment policy, documentation, and deployment use structured asset profiles.
- User journeys are ordered acceptance scenarios, not resources.

## Public CUE interface

All existing resource declarations gain a common optional contribution field:

```cue
contributesTo: [{
    capability: "capability.create_account"
    contribution: "persists the newly registered account atomically"
}]
```

Boundary concerns remain adapters. A profile adds delivery-specific context without changing dependency identity:

```cue
project: contexts: Accounts: ports: RegisterAccount: {
    direction: "inbound"
    contract: {
        register: "(request: RegisterAccountRequest) -> result<Account, RegistrationError>"
    }
}

project: adapters: RegistrationHttpEndpoint: {
    implements: "port.Accounts.RegisterAccount"
    layer:      "infrastructure"
    contributesTo: [{
        capability:   "capability.create_account"
        contribution: "accepts and returns the public registration contract"
    }]
    profile: {
        kind:     "http"
        method:   "POST"
        path:     "/accounts"
    }
}
```

Recurring project artifacts remain assets:

```cue
project: assets: AccountSchemaMigration: {
    kind: "sql-migration"
    profile: {
        kind:          "database_migration"
        predecessor:   "migration.006"
        compatibility: "expand-contract; old and new binaries overlap safely"
        rollback:      "forward repair only after contract phase"
    }
    targets: ["repository.Accounts.AccountRepository"]
    contributesTo: [{
        capability:   "capability.create_account"
        contribution: "creates durable account storage"
    }]
}
```

## Three-layer schema relationship

```text
Intent layer
  goal -> capability -> acceptance/evidence
                         |
                         | contributesTo
                         v
Implementation layer
  aggregate/entity/value object/domain service/application service
  repository/port/adapter
                         |
                         | targets/uses/implements
                         v
Artifact layer
  manifest/config/migration/infrastructure/observability/deployment/docs
```

The implementation layer remains the center of the resource graph. The intent graph groups and evaluates implementation resources; the artifact layer supports or verifies them.

## Canonical mapping rules

| Requested concern | Representation |
|---|---|
| UI screen | Inbound presentation port plus UI adapter profile; view state/actions are contract value objects |
| User journey | Acceptance scenario steps spanning capabilities and actors |
| HTTP/RPC endpoint | Inbound port plus HTTP/RPC adapter profile |
| Scheduled job | Inbound application port plus scheduler adapter profile |
| Workflow | Application service coordinating aggregates and ports |
| Stateful workflow/state machine | Aggregate with commands, events, states, and invariants |
| Data pipeline | Application service orchestration with source/sink ports and adapters |
| External integration | Outbound port plus external-system adapter profile |
| Event/message schema | Value object referenced by publishing/consuming port contracts |
| Persistence | Repository contract plus outbound adapter; schema migration is an asset |
| Security | Domain/application invariant or authorization service; deploy-time IAM/network policy is an asset |
| Configuration/observability/infrastructure/deployment/docs | Structured asset profile |

Boundary profile kinds are a closed enum such as `http`, `rpc`, `ui`, `cli`, `scheduler`, `message_consumer`, `message_publisher`, `persistence`, `external_system`, `device_input`, `device_output`, and `in_process`. Profiles describe delivery mechanics only; the implemented port owns the behavioral contract.

Asset profile kinds are a closed enum such as `build_manifest`, `configuration`, `database_migration`, `infrastructure`, `observability`, `security_policy`, `deployment`, `documentation`, and `verification_harness`. The existing user-defined `assetKinds` mechanism remains available for project-specific file shapes.

## Registry and graph design

- Add a common `Contribution` representation to registry resources.
- Keep resource dependency edges distinct from contribution edges: capability links affect grouping, impact, and context but do not automatically impose code-generation ordering.
- Add capability and acceptance nodes to a higher-level trace graph while retaining the existing DDD resource graph for topological execution.
- Derive goal links through capability-goal relationships instead of duplicating goal IDs on every resource.
- Validate resource contribution targets during registry construction.
- Record unmapped required capabilities as completion gaps.

## Persistence ownership

WS2 owns current and historical concepts for:

- resource-capability contributions and contribution descriptions
- port direction, adapter boundary profiles, and asset profiles
- traceability query/read models
- links from generated files to capability/goal projections through resource ownership

The CUE declaration remains authoritative. SQLite contribution rows are reconciled with the same specification snapshot transaction introduced by WS1.

## Tasks

### WS2-01 — Extend existing DDD boundaries and assets

- Add explicit inbound/outbound direction to ports.
- Add validated boundary profiles to adapters without duplicating the port contract.
- Add validated recurring profiles to assets while retaining `assetKinds`.
- Extend structural/declaration hashing for the new fields without changing existing resource IDs.

Acceptance: delivery and operational concerns are expressible through existing resource families, and no generic parallel resource registry exists.

### WS2-02 — Add capability contributions to all resources

- Add a reusable contribution type to DDD declarations, adapters, and assets.
- Validate capability IDs and non-empty contribution descriptions.
- Define merge/inheritance behavior explicitly: contributions belong to the concrete resource and are not inherited from project/context metadata.

Acceptance: all generatable resource kinds can map to capabilities and dangling mappings fail validation.

### WS2-03 — Build the traceability graph

- Construct goal/capability/acceptance/contribution/resource traversal indexes.
- Provide forward and reverse traversal methods with deterministic ordering.
- Keep contribution traversal separate from resource topological dependencies.
- Surface missing-resource and missing-acceptance gaps.

Acceptance: queries traverse both directions without scanning unstructured JSON.

### WS2-04 — Persist traceability

- Add focused migrations and SQL queries for contribution relationships.
- Reconcile contributions transactionally with WS1 intent.
- Add queries for goal, capability, resource, file, and current validation/evidence relationships.
- Preserve historical contribution identity after declaration changes.

Acceptance: persisted traversal matches the in-memory registry for the same spec hash.

### WS2-05 — Integrate generation and inspection

- Render capability contributions, port direction, boundary mechanics, and asset profiles in prompts.
- Expose traceability through Go, MCP, CLI, and typed dashboard APIs.
- Extend resource and goal explorers with contribution details, generated files, and missing links.
- Include supported capabilities in resource inspection and context candidates.

Acceptance: developers can navigate goal-to-file and file/resource-to-goal paths without raw SQL.

### WS2-06 — Add representative systems

- Add a goal-oriented crest-synth sample mapping goals onto aggregates, services, ports, adapters, and assets.
- Cover UI, journey, device input/output, real-time boundary, workflow coordination, verification harness, configuration, and documentation without a generic resource union.
- Demonstrate a locally valid aggregate whose capability remains incomplete because its adapter or end-to-end evidence is missing.

Acceptance: examples prove that resource settlement and project completion are distinct.

## Test plan

- Boundary/asset profile tests for required fields, invalid enums, dependency resolution, hashes, and prompt rendering.
- Contribution validation tests for dangling capability IDs, empty descriptions, multiple capabilities, and shared capabilities.
- Graph tests for forward/reverse traversal, deterministic ordering, cycles limited to the correct graph, and missing contributions.
- Store tests for reconciliation, historical preservation, and query parity with registry state.
- Planner regression tests showing contribution edges do not accidentally impose resource ordering.
- Dashboard/API tests for goals spanning DDD resources, boundary adapters, and artifacts.
- Existing DDD fixture and graph suites must remain unchanged in behavior except for required goal additions.

## Compatibility and rollout

Existing DDD syntax remains valid after the WS1 goal migration. Capability contributions are optional for an individual resource, but a required capability without sufficient contributions remains incomplete and visible. Existing adapters and assets without profiles remain valid; profiles add structured delivery context. Generic assets remain available for genuinely project-specific artifacts and are not removed or silently converted.

## Exit gate

- All existing DDD resource families and assets can declare validated capability contributions.
- Boundary and asset profiles work across registry, graph, hashing, prompts, persistence, API, and dashboard.
- Bidirectional traceability is deterministic and SQLite-backed.
- The crest-synth target sample demonstrates complete and incomplete vertical functionality without making the DDD schemas redundant.
