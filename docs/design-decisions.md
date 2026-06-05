# Design Decisions

Decisions made during critique and refinement of the crest-spec design. These supplement `crest-spec.md` and clarify intent where the spec is intentionally terse.

## Output is free-form, not templated

The LLM decides file layout during generation. There is no output template system or resource-to-file mapping declared in the spec.

Implications:
- The planner cannot predict `affectedFiles` for brand-new resources (`create` actions). File paths are discovered after generation and recorded in `generated_files`.
- On subsequent `modify` actions, affected files are known from state.
- Plan output for new resources shows files as "known after apply" (same as Terraform).

Guidance about file organization belongs in `meta.prompts` or `meta.style` — not in a rigid schema.

## Modules are self-contained; injection root comes later

Each resource generates against its declared contracts and interfaces, never against another resource's generated body. Modules don't need to see each other's implementations.

The injection root — the layer that wires interfaces to adapters and composes the system — is an ApplicationService added after initial generation. It is not a new resource kind.

This eliminates generation-ordering problems: resources can be generated in any topological order without needing access to upstream bodies.

## LLM clarification: structured questions in the database

When the LLM cannot converge on a valid body within the retry budget, the failure is recorded as a structured question in a `questions` table in the state database.

Flow:
1. `apply` fails on a resource after N retries.
2. The last failure reason is stored as a question in the database, linked to the resource.
3. `crest-spec questions` shows unresolved questions.
4. The user answers by refining the resource's `meta` (adding to `notes`, `rules`, `prompts`, etc.).
5. The meta change updates the resource's hash, which triggers re-apply on the next `crest-spec apply`.

Interactive mode (`crest-spec apply --interactive`) is opt-in: pauses on failure, presents the question inline, and records the answer as `meta.notes` on the resource.

## Partial apply like Terraform

Each resource commits independently to state on success. A failed apply leaves the system in a partial state that can be resumed.

- The `applies` table tracks the overall run.
- Individual resources are settled independently as they succeed.
- A run where some resources fail gets status `partial`.
- Re-running `apply` picks up from where it left off — already-settled resources with unchanged hashes are skipped.

## Event subscriptions via `subscribesTo`

Events flow along declared ContextMap edges only. A `subscribesTo` declaration makes wiring explicit without violating context boundaries.

```ts
const synthService = synthesis.applicationService("SynthNoteHandler", {
  subscribesTo: [
    { event: "NotesEmitted", from: playback },
  ],
})
```

Constraints enforced at plan time:
- You can only subscribe to events from a context you have a declared relationship with in the ContextMap.
- The relationship direction must be correct (downstream subscribes to upstream's events).
- Subscribing across an undeclared boundary is an invariant violation.

Events are the integration mechanism between contexts. No direct access to another context's aggregates, repositories, or services — only event subscription along declared edges.

## Opinionated by design

crest-spec is opinionated. Configurability where useful, but no accommodation for "different teams want different things" in v1. The file layout, generation style, and conventions serve the author's workflow. Generalization emerges from real use, not from speculation.
