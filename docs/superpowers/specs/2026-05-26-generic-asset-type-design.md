# Generic Asset Type

## Problem

crest-spec's DSL covers DDD tactical patterns (aggregates, value objects, services, etc.) but has no way to declare non-DDD artifacts — test scenes, config files, shader definitions, autoloads, or anything else that doesn't fit the standard resource vocabulary. These artifacts still benefit from LLM-driven generation with architectural constraints, dependency tracking, and monotonic regeneration.

The motivating use case is Godot test scenes that use the event emitter pattern to auto-pilot scenarios headlessly and validate behavior via log output.

## Design

### Asset Kind Definitions

An `assetKind` is a project-level meta-resource that teaches the LLM what a category of asset is. It carries rich context: a detailed description of the pattern, prompts that constrain generation, references to existing code the LLM should read, and a file pattern hint.

```ts
app.assetKind("godot-test-scene", {
  description: `
    A Godot 4 scene (.tscn + .gd) that auto-pilots a test scenario
    when run headless. The scene's root script drives the test from
    _ready(), emitting domain commands through the EventBus autoload
    and connecting to signals to observe results. Each assertion is
    logged as a TAP line (ok N / not ok N). The script calls
    get_tree().quit() when finished.

    Scene tree structure:
      TestRoot (Node)
        └── [subject nodes instantiated by the script]

    The EventBus autoload (res://autoloads/event_bus.gd) provides:
      - emit_command(name: String, payload: Dictionary)
      - signal command_handled(name: String, result: Dictionary)
      - signal domain_event(name: String, payload: Dictionary)

    Timing: after emitting a command, yield one frame
    (await get_tree().process_frame) before asserting, to allow
    signal propagation.
  `,
  prompts: [
    "Never use direct method calls on aggregates — always go through EventBus",
    "Each .tscn has a single root node with the test script attached",
    "Use print() for TAP output — godot --headless captures stdout",
  ],
  references: [
    "./src/autoloads/event_bus.gd",
    "./src/autoloads/event_bus.tscn",
    "./docs/testing-patterns.md",
  ],
  filePattern: "tests/scenes/{context}/{name}",
});
```

An `assetKind` registers as a resource with kind `"assetKind"` and id `assetKind:<name>`. It does not generate files itself.

### Asset Declarations

An `asset` is a lean declaration that references a kind and describes a specific scenario. Assets can be declared at three levels:

**On an aggregate** — context and layer inferred; the aggregate is an implicit target:

```ts
song.asset("rename-flow", {
  kind: "godot-test-scene",
  description: `
    Emits RenameSong with name "My Track", waits one frame,
    asserts song.name updated and SongRenamed event fired with
    correct payload. Then emits RenameSong with empty string,
    asserts the command is rejected (name invariant).
  `,
});
```

**On a bounded context** — context inferred, no implicit target:

```ts
composition.asset("phrase-to-chain-flow", {
  kind: "godot-test-scene",
  targets: [song, linearPhrase, chain],
  description: `
    Creates a Song, adds a Chain, creates a LinearPhrase, assigns
    the phrase to chain slot 0. Verifies the full relationship:
    Song -> Chain -> PhraseRef -> LinearPhrase. Asserts all
    intermediate events fired in order.
  `,
  prompts: [
    "Instantiate all three aggregate nodes in the scene tree",
  ],
});
```

**On the project** — cross-context, no implicit scope:

```ts
app.asset("editor-playback-integration", {
  kind: "godot-test-scene",
  targets: [songEditor, playbackEngine],
  description: `
    Simulates opening a song in the editor, setting tempo to 140,
    starting playback, and verifying the PlaybackEngine receives
    the correct tempo. Covers Editor → Composition → Playback flow.
  `,
});
```

### Asset Declaration Interface

```ts
interface AssetDeclaration {
  kind: string;                    // must match a registered assetKind
  description?: string;            // scenario-specific detail for the LLM
  targets?: ResourceBuilder[];     // explicit dependencies on spec resources
  prompts?: string[];              // supplements the kind's prompts
  references?: string[];           // supplements the kind's references
}
```

## Engine Integration

### Registry

Assets register as resources with kind `"asset"` and id `asset:<name>`. They carry their asset kind as a field on the resource descriptor.

### Dependencies

- Every asset has an implicit dependency on its `assetKind` definition.
- `targets` create explicit dependencies on the referenced resources.
- On aggregate-level assets, the aggregate itself is an implicit target dependency.

### Effective Hash

An asset's effective hash includes:

- Its own declaration (description, prompts, targets, references)
- The assetKind's declaration hash
- The effective hashes of all target resources
- Contents of referenced files
- Model ID

Changes to the kind definition, target resources, or referenced files trigger regeneration.

### Prompt Construction

When generating an asset, the `PromptBuilder` merges (in order):

1. The **assetKind** definition (description, prompts, referenced file contents)
2. The **asset's own** description, prompts, references
3. The **target resources'** full declarations (commands, events, state, invariants)
4. Project-level **meta** (style, avoid, global prompts)

### Constraint Loop

- **Type checking:** Skipped for non-TypeScript output (e.g., GDScript). The constraint loop already handles this.
- **Invariant checking:** Standard structural invariants apply (layer rules, context boundaries). Asset-specific invariants are a future extension.
- **Retry:** On parse failure (no valid `// path:` annotated code blocks), retries with feedback.

### File Generation

The engine has no kind-specific codegen. The LLM produces `// path:` annotated code blocks based on the kind definition's description and file pattern hint. For a `godot-test-scene`, the LLM typically outputs a `.gd` script and a `.tscn` scene file.

Adding a new asset kind is purely a DSL-level operation — no engine changes required.

## DSL Builder Changes

### ProjectBuilder

- `assetKind(name, declaration)` — registers an assetKind meta-resource
- `asset(name, declaration)` — registers a project-level asset

### ContextBuilder

- `asset(name, declaration)` — registers a context-level asset (context inferred)

### AggregateBuilder

- `asset(name, declaration)` — registers an aggregate-level asset (context and aggregate inferred as implicit target)

## State Database

No schema changes needed. Assets use the existing `resources`, `generated_files`, and `dependencies` tables. The `kind` column stores `"asset"`, and the `declaration_json` includes the asset's `kind` field (the asset kind name) alongside description, prompts, etc.

The `assetKind` meta-resource is stored the same way with kind `"assetKind"`.
