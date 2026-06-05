# Generic Asset Type Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a generic `asset` resource type and `assetKind` definitions to the crest-spec DSL, enabling non-DDD artifacts (Godot test scenes, configs, shaders) to be declared and generated through the same engine as tactical DDD resources.

**Architecture:** Two new resource kinds (`assetKind` and `asset`) registered through the existing `ResourceRegistry`. Assets depend on their kind definition and optional target resources. The `PromptBuilder` merges kind context + asset specifics + target declarations when generating prompts.

**Tech Stack:** TypeScript, Bun test framework, existing crest-spec DSL/engine

---

## File Structure

| Action | Path | Responsibility |
|--------|------|---------------|
| Modify | `src/types.ts` | Add `AssetKindConfig`, `AssetDeclaration` interfaces; extend `ResourceKind` union |
| Modify | `src/dsl/project-builder.ts` | Add `assetKind()` and `asset()` methods |
| Modify | `src/dsl/context-builder.ts` | Add `asset()` method |
| Modify | `src/dsl/aggregate-builder.ts` | Add `asset()` method |
| Modify | `src/dsl/index.ts` | Re-export new types |
| Modify | `src/index.ts` | Re-export new types |
| Modify | `src/engine/prompt-builder.ts` | Asset-specific prompt construction |
| Create | `tests/dsl/asset.test.ts` | DSL registration tests at all three levels |
| Create | `tests/engine/prompt-builder-asset.test.ts` | Asset prompt merging tests |

---

### Task 1: Add types for AssetKind and Asset

**Files:**
- Modify: `src/types.ts`

- [ ] **Step 1: Add `"assetKind"` and `"asset"` to the `ResourceKind` union**

In `src/types.ts`, change the `ResourceKind` type:

```ts
export type ResourceKind =
  | "project"
  | "context"
  | "aggregate"
  | "entity"
  | "valueObject"
  | "command"
  | "event"
  | "port"
  | "adapter"
  | "repository"
  | "applicationService"
  | "domainService"
  | "factory"
  | "assetKind"
  | "asset";
```

- [ ] **Step 2: Add `AssetKindConfig` interface**

Append after `DomainServiceConfig`:

```ts
export interface AssetKindConfig {
  description: string;
  prompts?: string[];
  references?: string[];
  filePattern?: string;
  meta?: Meta;
}
```

- [ ] **Step 3: Add `AssetDeclaration` interface**

Append after `AssetKindConfig`:

```ts
export interface ResourceRef {
  id: string;
}

export interface AssetDeclaration {
  kind: string;
  description?: string;
  targets?: ResourceRef[];
  prompts?: string[];
  references?: string[];
  meta?: Meta;
}
```

- [ ] **Step 4: Run type check**

Run: `bunx tsc --noEmit`
Expected: PASS (no errors — these are additive type changes)

- [ ] **Step 5: Commit**

```bash
git add src/types.ts
git commit -m "feat: add AssetKind and Asset types to ResourceKind union"
```

---

### Task 2: Add `assetKind()` and `asset()` to ProjectBuilder

**Files:**
- Modify: `src/dsl/project-builder.ts`
- Create: `tests/dsl/asset.test.ts`

- [ ] **Step 1: Write the failing test for `assetKind()`**

Create `tests/dsl/asset.test.ts`:

```ts
import { describe, test, expect, beforeEach } from "bun:test";
import { ProjectBuilder } from "../../src/dsl/project-builder";
import { ResourceRegistry } from "../../src/registry/resource-registry";

describe("assetKind", () => {
  let app: ProjectBuilder;

  beforeEach(() => {
    app = new ProjectBuilder("test-project");
  });

  test("registers an assetKind resource in the registry", () => {
    app.assetKind("godot-test-scene", {
      description: "A Godot 4 test scene that auto-pilots scenarios headlessly.",
      prompts: ["Use EventBus.emit() for commands"],
      references: ["./src/autoloads/event_bus.gd"],
      filePattern: "tests/scenes/{context}/{name}",
    });

    const registry = app.getRegistry();
    const resource = registry.getById("assetKind.godot-test-scene");
    expect(resource).not.toBeNull();
    expect(resource!.kind).toBe("assetKind");
    expect(resource!.name).toBe("godot-test-scene");
    expect(resource!.context).toBeNull();
    expect(resource!.layer).toBeNull();
    expect(resource!.declaration).toEqual({
      description: "A Godot 4 test scene that auto-pilots scenarios headlessly.",
      filePattern: "tests/scenes/{context}/{name}",
    });
    expect(resource!.meta.prompts).toEqual(["Use EventBus.emit() for commands"]);
    expect(resource!.meta.references).toEqual(["./src/autoloads/event_bus.gd"]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test tests/dsl/asset.test.ts`
Expected: FAIL — `app.assetKind is not a function`

- [ ] **Step 3: Implement `assetKind()` on ProjectBuilder**

In `src/dsl/project-builder.ts`, add the import for `AssetKindConfig` and add the method:

```ts
import type {
  ProjectConfig,
  ContextConfig,
  AdapterConfig,
  AssetKindConfig,
  AssetDeclaration,
  ContextRelationship,
  InvariantDescriptor,
  ResourceDescriptor,
  Meta,
} from "../types.js";
```

Add method to `ProjectBuilder` class:

```ts
assetKind(name: string, config: AssetKindConfig): void {
  const id = `assetKind.${name}`;
  const descriptor: ResourceDescriptor = {
    id,
    kind: "assetKind",
    name,
    context: null,
    layer: null,
    declaration: {
      description: config.description,
      filePattern: config.filePattern,
    },
    meta: {
      prompts: config.prompts,
      references: config.references,
      ...config.meta,
    },
    dependencies: [],
  };
  this.registry.register(descriptor);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test tests/dsl/asset.test.ts`
Expected: PASS

- [ ] **Step 5: Write the failing test for project-level `asset()`**

Append to `tests/dsl/asset.test.ts`:

```ts
describe("project-level asset", () => {
  let app: ProjectBuilder;

  beforeEach(() => {
    app = new ProjectBuilder("test-project");
    app.assetKind("godot-test-scene", {
      description: "A Godot 4 test scene.",
    });
  });

  test("registers an asset resource with dependency on its assetKind", () => {
    app.asset("editor-integration", {
      kind: "godot-test-scene",
      description: "Tests editor-to-playback flow.",
      prompts: ["Instantiate both context nodes"],
    });

    const registry = app.getRegistry();
    const resource = registry.getById("asset.editor-integration");
    expect(resource).not.toBeNull();
    expect(resource!.kind).toBe("asset");
    expect(resource!.name).toBe("editor-integration");
    expect(resource!.context).toBeNull();
    expect(resource!.layer).toBeNull();
    expect(resource!.declaration).toEqual({
      assetKind: "godot-test-scene",
      description: "Tests editor-to-playback flow.",
    });
    expect(resource!.meta.prompts).toEqual(["Instantiate both context nodes"]);
    expect(resource!.dependencies).toContainEqual({
      targetId: "assetKind.godot-test-scene",
      kind: "uses",
    });
  });

  test("registers target dependencies", () => {
    app.asset("cross-context-test", {
      kind: "godot-test-scene",
      description: "Tests cross-context flow.",
      targets: [{ id: "aggregate.Editor.SongEditor" }, { id: "aggregate.Playback.Engine" }],
    });

    const registry = app.getRegistry();
    const resource = registry.getById("asset.cross-context-test");
    expect(resource!.dependencies).toContainEqual({
      targetId: "assetKind.godot-test-scene",
      kind: "uses",
    });
    expect(resource!.dependencies).toContainEqual({
      targetId: "aggregate.Editor.SongEditor",
      kind: "uses",
    });
    expect(resource!.dependencies).toContainEqual({
      targetId: "aggregate.Playback.Engine",
      kind: "uses",
    });
  });
});
```

- [ ] **Step 6: Run test to verify it fails**

Run: `bun test tests/dsl/asset.test.ts`
Expected: FAIL — `app.asset is not a function`

- [ ] **Step 7: Implement `asset()` on ProjectBuilder**

Add method to `ProjectBuilder` class in `src/dsl/project-builder.ts`:

```ts
asset(name: string, config: AssetDeclaration): void {
  const id = `asset.${name}`;
  const dependencies = [
    { targetId: `assetKind.${config.kind}`, kind: "uses" as const },
    ...(config.targets ?? []).map((t) => ({ targetId: t.id, kind: "uses" as const })),
  ];
  const descriptor: ResourceDescriptor = {
    id,
    kind: "asset",
    name,
    context: null,
    layer: null,
    declaration: {
      assetKind: config.kind,
      description: config.description,
    },
    meta: {
      prompts: config.prompts,
      references: config.references,
      ...config.meta,
    },
    dependencies,
  };
  this.registry.register(descriptor);
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `bun test tests/dsl/asset.test.ts`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add src/dsl/project-builder.ts tests/dsl/asset.test.ts
git commit -m "feat: add assetKind() and asset() to ProjectBuilder"
```

---

### Task 3: Add `asset()` to ContextBuilder

**Files:**
- Modify: `src/dsl/context-builder.ts`
- Modify: `tests/dsl/asset.test.ts`

- [ ] **Step 1: Write the failing test for context-level `asset()`**

Append to `tests/dsl/asset.test.ts`:

```ts
import { ContextBuilder } from "../../src/dsl/context-builder";

describe("context-level asset", () => {
  let app: ProjectBuilder;
  let composition: ContextBuilder;

  beforeEach(() => {
    app = new ProjectBuilder("test-project");
    app.assetKind("godot-test-scene", {
      description: "A Godot 4 test scene.",
    });
    composition = app.context("Composition", { purpose: "structural model" });
  });

  test("registers an asset with context inferred", () => {
    composition.asset("phrase-flow", {
      kind: "godot-test-scene",
      description: "Tests phrase editing commands.",
    });

    const registry = app.getRegistry();
    const resource = registry.getById("asset.Composition.phrase-flow");
    expect(resource).not.toBeNull();
    expect(resource!.kind).toBe("asset");
    expect(resource!.context).toBe("Composition");
    expect(resource!.declaration).toEqual({
      assetKind: "godot-test-scene",
      description: "Tests phrase editing commands.",
    });
    expect(resource!.dependencies).toContainEqual({
      targetId: "assetKind.godot-test-scene",
      kind: "uses",
    });
  });

  test("registers target dependencies from context-level asset", () => {
    composition.asset("multi-target", {
      kind: "godot-test-scene",
      description: "Tests multiple aggregates.",
      targets: [{ id: "aggregate.Composition.Song" }],
    });

    const registry = app.getRegistry();
    const resource = registry.getById("asset.Composition.multi-target");
    expect(resource!.dependencies).toContainEqual({
      targetId: "aggregate.Composition.Song",
      kind: "uses",
    });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test tests/dsl/asset.test.ts`
Expected: FAIL — `composition.asset is not a function`

- [ ] **Step 3: Implement `asset()` on ContextBuilder**

In `src/dsl/context-builder.ts`, add the import for `AssetDeclaration`:

```ts
import type {
  AggregateConfig,
  ValueObjectConfig,
  PortConfig,
  RepositoryConfig,
  ApplicationServiceConfig,
  DomainServiceConfig,
  AdapterConfig,
  EntityConfig,
  AssetDeclaration,
  ResourceDescriptor,
  PortRef,
  AggregateRef,
  Meta,
} from "../types.js";
```

Add method to `ContextBuilder` class:

```ts
asset(name: string, config: AssetDeclaration): void {
  const id = `asset.${this.name}.${name}`;
  const dependencies = [
    { targetId: `assetKind.${config.kind}`, kind: "uses" as const },
    ...(config.targets ?? []).map((t) => ({ targetId: t.id, kind: "uses" as const })),
  ];
  const descriptor: ResourceDescriptor = {
    id,
    kind: "asset",
    name,
    context: this.name,
    layer: null,
    declaration: {
      assetKind: config.kind,
      description: config.description,
    },
    meta: {
      prompts: config.prompts,
      references: config.references,
      ...config.meta,
    },
    dependencies,
  };
  this.registry.register(descriptor);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test tests/dsl/asset.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/dsl/context-builder.ts tests/dsl/asset.test.ts
git commit -m "feat: add asset() to ContextBuilder"
```

---

### Task 4: Add `asset()` to AggregateBuilder

**Files:**
- Modify: `src/dsl/aggregate-builder.ts`
- Modify: `tests/dsl/asset.test.ts`

- [ ] **Step 1: Write the failing test for aggregate-level `asset()`**

Append to `tests/dsl/asset.test.ts`:

```ts
describe("aggregate-level asset", () => {
  let app: ProjectBuilder;

  beforeEach(() => {
    app = new ProjectBuilder("test-project");
    app.assetKind("godot-test-scene", {
      description: "A Godot 4 test scene.",
    });
  });

  test("registers an asset with context inferred and aggregate as implicit target", () => {
    const composition = app.context("Composition", { purpose: "structural model" });
    const song = composition.aggregate("Song", {
      root: true,
      state: { id: "SongId", name: "string" },
    });

    song.asset("rename-flow", {
      kind: "godot-test-scene",
      description: "Emits RenameSong, asserts name updated.",
    });

    const registry = app.getRegistry();
    const resource = registry.getById("asset.Composition.Song.rename-flow");
    expect(resource).not.toBeNull();
    expect(resource!.kind).toBe("asset");
    expect(resource!.context).toBe("Composition");
    expect(resource!.declaration).toEqual({
      assetKind: "godot-test-scene",
      description: "Emits RenameSong, asserts name updated.",
    });
    // Implicit dependency on the aggregate
    expect(resource!.dependencies).toContainEqual({
      targetId: "aggregate.Composition.Song",
      kind: "uses",
    });
    // Dependency on the assetKind
    expect(resource!.dependencies).toContainEqual({
      targetId: "assetKind.godot-test-scene",
      kind: "uses",
    });
  });

  test("aggregate-level asset merges explicit targets with implicit aggregate target", () => {
    const composition = app.context("Composition", { purpose: "structural model" });
    const song = composition.aggregate("Song", { root: true, state: { id: "SongId" } });

    song.asset("with-extra-targets", {
      kind: "godot-test-scene",
      description: "Tests song with chain.",
      targets: [{ id: "aggregate.Composition.Chain" }],
    });

    const registry = app.getRegistry();
    const resource = registry.getById("asset.Composition.Song.with-extra-targets");
    expect(resource!.dependencies).toContainEqual({
      targetId: "aggregate.Composition.Song",
      kind: "uses",
    });
    expect(resource!.dependencies).toContainEqual({
      targetId: "aggregate.Composition.Chain",
      kind: "uses",
    });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test tests/dsl/asset.test.ts`
Expected: FAIL — `song.asset is not a function`

- [ ] **Step 3: Implement `asset()` on AggregateBuilder**

In `src/dsl/aggregate-builder.ts`, add the import for `AssetDeclaration`:

```ts
import type { EntityConfig, AssetDeclaration, ResourceDescriptor, Meta } from "../types.js";
```

Add method to `AggregateBuilder` class:

```ts
asset(name: string, config: AssetDeclaration): void {
  const id = `asset.${this.contextName}.${this.name}.${name}`;
  const dependencies = [
    { targetId: `assetKind.${config.kind}`, kind: "uses" as const },
    { targetId: this.id, kind: "uses" as const },
    ...(config.targets ?? []).map((t) => ({ targetId: t.id, kind: "uses" as const })),
  ];
  const descriptor: ResourceDescriptor = {
    id,
    kind: "asset",
    name,
    context: this.contextName,
    layer: null,
    declaration: {
      assetKind: config.kind,
      description: config.description,
    },
    meta: {
      prompts: config.prompts,
      references: config.references,
      ...config.meta,
    },
    dependencies,
  };
  this.registry.register(descriptor);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test tests/dsl/asset.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/dsl/aggregate-builder.ts tests/dsl/asset.test.ts
git commit -m "feat: add asset() to AggregateBuilder"
```

---

### Task 5: Asset-specific prompt construction in PromptBuilder

**Files:**
- Modify: `src/engine/prompt-builder.ts`
- Create: `tests/engine/prompt-builder-asset.test.ts`

- [ ] **Step 1: Write the failing test for asset prompt building**

Create `tests/engine/prompt-builder-asset.test.ts`:

```ts
import { describe, test, expect } from "bun:test";
import { PromptBuilder } from "../../src/engine/prompt-builder";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";

describe("PromptBuilder - asset resources", () => {
  test("includes assetKind description and prompts in the asset prompt", () => {
    const registry = new ResourceRegistry();

    registry.register(
      makeResource({
        id: "assetKind.godot-test-scene",
        kind: "assetKind",
        name: "godot-test-scene",
        context: null,
        declaration: {
          description: "A Godot 4 scene that auto-pilots tests headlessly.",
          filePattern: "tests/scenes/{context}/{name}",
        },
        meta: {
          prompts: ["Use EventBus.emit() for commands", "Print TAP output"],
          references: ["./src/autoloads/event_bus.gd"],
        },
      }),
    );

    const asset = makeResource({
      id: "asset.Composition.rename-flow",
      kind: "asset",
      name: "rename-flow",
      context: "Composition",
      declaration: {
        assetKind: "godot-test-scene",
        description: "Emits RenameSong, asserts name updated.",
      },
      meta: {
        prompts: ["Also check the signal fires"],
      },
      dependencies: [
        { targetId: "assetKind.godot-test-scene", kind: "uses" },
      ],
    });
    registry.register(asset);

    const builder = new PromptBuilder();
    const prompt = builder.build(asset, registry);

    expect(prompt).toContain("A Godot 4 scene that auto-pilots tests headlessly.");
    expect(prompt).toContain("tests/scenes/{context}/{name}");
    expect(prompt).toContain("Use EventBus.emit() for commands");
    expect(prompt).toContain("Print TAP output");
    expect(prompt).toContain("Emits RenameSong, asserts name updated.");
    expect(prompt).toContain("Also check the signal fires");
  });

  test("includes target resource declarations in the asset prompt", () => {
    const registry = new ResourceRegistry();

    registry.register(
      makeResource({
        id: "assetKind.godot-test-scene",
        kind: "assetKind",
        name: "godot-test-scene",
        context: null,
        declaration: {
          description: "A Godot 4 test scene.",
        },
        meta: {},
      }),
    );

    registry.register(
      makeResource({
        id: "aggregate.Composition.Song",
        kind: "aggregate",
        name: "Song",
        context: "Composition",
        declaration: { root: true, state: { id: "SongId", name: "string", tempo: "BPM" } },
        commands: [{ name: "RenameSong", payload: { name: "string" } }],
        events: [{ name: "SongRenamed", payload: { name: "string" } }],
        invariants: ["tempo is between 20 and 999"],
      }),
    );

    const asset = makeResource({
      id: "asset.Composition.rename-flow",
      kind: "asset",
      name: "rename-flow",
      context: "Composition",
      declaration: {
        assetKind: "godot-test-scene",
        description: "Tests the RenameSong command.",
      },
      meta: {},
      dependencies: [
        { targetId: "assetKind.godot-test-scene", kind: "uses" },
        { targetId: "aggregate.Composition.Song", kind: "uses" },
      ],
    });
    registry.register(asset);

    const builder = new PromptBuilder();
    const prompt = builder.build(asset, registry);

    expect(prompt).toContain("Song");
    expect(prompt).toContain("RenameSong");
    expect(prompt).toContain("SongRenamed");
    expect(prompt).toContain("tempo is between 20 and 999");
  });

  test("non-asset resources still build normally", () => {
    const registry = new ResourceRegistry();
    const aggregate = makeResource({
      id: "aggregate.Composition.Song",
      kind: "aggregate",
      name: "Song",
      context: "Composition",
      layer: "domain",
      declaration: { root: true, state: { id: "SongId" } },
    });
    registry.register(aggregate);

    const builder = new PromptBuilder();
    const prompt = builder.build(aggregate, registry);

    expect(prompt).toContain('Resource: aggregate "Song"');
    expect(prompt).not.toContain("Asset Kind");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun test tests/engine/prompt-builder-asset.test.ts`
Expected: FAIL — prompt doesn't contain the assetKind description (current `build()` method is generic and doesn't have asset-specific logic)

- [ ] **Step 3: Implement asset-specific prompt construction**

In `src/engine/prompt-builder.ts`, modify the `build()` method to detect asset resources and construct a specialized prompt. Add this logic after the existing `build()` method body, or restructure as a conditional:

```ts
build(resource: ResourceDescriptor, registry: IResourceRegistry): string {
  if (resource.kind === "asset") {
    return this.buildAssetPrompt(resource, registry);
  }

  const sections: string[] = [];
  // ... existing generic build logic unchanged ...
  return sections.join("\n");
}

private buildAssetPrompt(resource: ResourceDescriptor, registry: IResourceRegistry): string {
  const sections: string[] = [];
  const assetKindName = resource.declaration.assetKind as string;

  sections.push(`## Asset: "${resource.name}" (${resource.id})`);
  sections.push(`Context: ${resource.context ?? "project-level"}`);
  sections.push(`Asset Kind: ${assetKindName}`);

  // Include the assetKind definition
  const kindResource = registry.getById(`assetKind.${assetKindName}`);
  if (kindResource) {
    sections.push("\n## Asset Kind Definition");
    if (kindResource.declaration.description) {
      sections.push(kindResource.declaration.description as string);
    }
    if (kindResource.declaration.filePattern) {
      sections.push(`\nFile pattern: ${kindResource.declaration.filePattern}`);
    }
    if (kindResource.meta.prompts && (kindResource.meta.prompts as string[]).length > 0) {
      sections.push("\n### Kind Prompts");
      for (const p of kindResource.meta.prompts as string[]) {
        sections.push(`- ${p}`);
      }
    }
    if (kindResource.meta.references && (kindResource.meta.references as string[]).length > 0) {
      sections.push("\n### Kind References");
      for (const ref of kindResource.meta.references as string[]) {
        sections.push(`- ${ref}`);
      }
    }
  }

  // Include the asset's own description and prompts
  sections.push("\n## Asset Description");
  if (resource.declaration.description) {
    sections.push(resource.declaration.description as string);
  }

  if (resource.meta.prompts && (resource.meta.prompts as string[]).length > 0) {
    sections.push("\n### Asset Prompts");
    for (const p of resource.meta.prompts as string[]) {
      sections.push(`- ${p}`);
    }
  }

  if (resource.meta.references && (resource.meta.references as string[]).length > 0) {
    sections.push("\n### Asset References");
    for (const ref of resource.meta.references as string[]) {
      sections.push(`- ${ref}`);
    }
  }

  // Include target resource declarations
  const targets = resource.dependencies.filter(
    (d) => d.kind === "uses" && !d.targetId.startsWith("assetKind."),
  );
  if (targets.length > 0) {
    sections.push("\n## Target Resources");
    for (const dep of targets) {
      const target = registry.getById(dep.targetId);
      if (!target) continue;
      sections.push(`\n### ${target.kind}: ${target.name} (${target.id})`);
      sections.push("```json");
      sections.push(JSON.stringify(target.declaration, null, 2));
      sections.push("```");
      if (target.commands && target.commands.length > 0) {
        sections.push("Commands:");
        for (const cmd of target.commands) {
          sections.push(`- **${cmd.name}**: ${JSON.stringify(cmd.payload)}`);
        }
      }
      if (target.events && target.events.length > 0) {
        sections.push("Events:");
        for (const evt of target.events) {
          sections.push(`- **${evt.name}**: ${JSON.stringify(evt.payload)}`);
        }
      }
      if (target.invariants && target.invariants.length > 0) {
        sections.push("Invariants:");
        for (const inv of target.invariants) {
          sections.push(`- ${inv}`);
        }
      }
    }
  }

  return sections.join("\n");
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun test tests/engine/prompt-builder-asset.test.ts`
Expected: PASS

- [ ] **Step 5: Run all tests to check for regressions**

Run: `bun test`
Expected: All tests PASS

- [ ] **Step 6: Commit**

```bash
git add src/engine/prompt-builder.ts tests/engine/prompt-builder-asset.test.ts
git commit -m "feat: asset-specific prompt construction in PromptBuilder"
```

---

### Task 6: Update exports

**Files:**
- Modify: `src/dsl/index.ts`
- Modify: `src/index.ts`

- [ ] **Step 1: Export new types from `src/dsl/index.ts`**

No changes needed to `src/dsl/index.ts` — the builders are already exported and the new methods are instance methods, not standalone exports.

- [ ] **Step 2: Export new type interfaces from `src/index.ts`**

In `src/index.ts`, the `export * from "./types.js"` already re-exports everything from types.ts, so `AssetKindConfig`, `AssetDeclaration`, and `ResourceRef` are already available.

Verify by running: `bunx tsc --noEmit`
Expected: PASS

- [ ] **Step 3: Commit (if any changes were needed)**

If no file changes are needed, skip this commit.

---

### Task 7: Integration test — full DSL usage

**Files:**
- Modify: `tests/dsl/asset.test.ts`

- [ ] **Step 1: Write an integration test that exercises the full flow**

Append to `tests/dsl/asset.test.ts`:

```ts
describe("asset integration: hash cascade", () => {
  test("asset effective hash changes when assetKind changes", () => {
    const { HashComputer } = await import("../../src/planner/hash-computer");

    const registry1 = new ResourceRegistry();
    const app1 = new ProjectBuilder("test", undefined, registry1);
    app1.assetKind("godot-test-scene", {
      description: "Version 1 of the test scene pattern.",
    });
    const ctx1 = app1.context("Comp", { purpose: "test" });
    ctx1.asset("rename-flow", {
      kind: "godot-test-scene",
      description: "Tests rename.",
    });

    const hashComputer = new HashComputer("claude-sonnet-4-6");
    const hashes1 = hashComputer.computeAll(app1.getRegistry());

    const registry2 = new ResourceRegistry();
    const app2 = new ProjectBuilder("test", undefined, registry2);
    app2.assetKind("godot-test-scene", {
      description: "Version 2 — CHANGED description.",
    });
    const ctx2 = app2.context("Comp", { purpose: "test" });
    ctx2.asset("rename-flow", {
      kind: "godot-test-scene",
      description: "Tests rename.",
    });

    const hashes2 = hashComputer.computeAll(app2.getRegistry());

    const assetHash1 = hashes1.get("asset.Comp.rename-flow");
    const assetHash2 = hashes2.get("asset.Comp.rename-flow");
    expect(assetHash1).not.toBe(assetHash2);
  });

  test("asset effective hash changes when target resource changes", () => {
    const { HashComputer } = await import("../../src/planner/hash-computer");
    const hashComputer = new HashComputer("claude-sonnet-4-6");

    const registry1 = new ResourceRegistry();
    registry1.register(makeResource({
      id: "assetKind.godot-test-scene",
      kind: "assetKind",
      name: "godot-test-scene",
      context: null,
      declaration: { description: "Test scene." },
    }));
    registry1.register(makeResource({
      id: "aggregate.Comp.Song",
      kind: "aggregate",
      name: "Song",
      context: "Comp",
      declaration: { state: { name: "string" } },
    }));
    registry1.register(makeResource({
      id: "asset.Comp.rename-flow",
      kind: "asset",
      name: "rename-flow",
      context: "Comp",
      declaration: { assetKind: "godot-test-scene", description: "Tests rename." },
      dependencies: [
        { targetId: "assetKind.godot-test-scene", kind: "uses" },
        { targetId: "aggregate.Comp.Song", kind: "uses" },
      ],
    }));

    const hashes1 = hashComputer.computeAll(registry1);

    const registry2 = new ResourceRegistry();
    registry2.register(makeResource({
      id: "assetKind.godot-test-scene",
      kind: "assetKind",
      name: "godot-test-scene",
      context: null,
      declaration: { description: "Test scene." },
    }));
    registry2.register(makeResource({
      id: "aggregate.Comp.Song",
      kind: "aggregate",
      name: "Song",
      context: "Comp",
      declaration: { state: { name: "string", tempo: "BPM" } },
    }));
    registry2.register(makeResource({
      id: "asset.Comp.rename-flow",
      kind: "asset",
      name: "rename-flow",
      context: "Comp",
      declaration: { assetKind: "godot-test-scene", description: "Tests rename." },
      dependencies: [
        { targetId: "assetKind.godot-test-scene", kind: "uses" },
        { targetId: "aggregate.Comp.Song", kind: "uses" },
      ],
    }));

    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("asset.Comp.rename-flow")).not.toBe(hashes2.get("asset.Comp.rename-flow"));
  });
});
```

- [ ] **Step 2: Run the integration test**

Run: `bun test tests/dsl/asset.test.ts`
Expected: PASS — the hash cascade works through existing `HashComputer` logic since assets declare dependencies via the standard `dependencies` array.

- [ ] **Step 3: Run full test suite**

Run: `bun test`
Expected: All tests PASS

- [ ] **Step 4: Commit**

```bash
git add tests/dsl/asset.test.ts
git commit -m "test: integration tests for asset hash cascade"
```

---

### Task 8: Final verification

- [ ] **Step 1: Type check the entire project**

Run: `bunx tsc --noEmit`
Expected: PASS

- [ ] **Step 2: Run full test suite**

Run: `bun test`
Expected: All tests PASS

- [ ] **Step 3: Verify the DSL reads naturally with a smoke test**

Create a temporary file and run it to verify the API works end-to-end:

```bash
bun -e "
import { project } from './src/index.js';

const app = project('smoke-test');
app.assetKind('godot-test-scene', {
  description: 'A Godot 4 test scene.',
  prompts: ['Use EventBus'],
});

const ctx = app.context('Comp', { purpose: 'test' });
const song = ctx.aggregate('Song', { root: true, state: { id: 'SongId' } });

song.asset('rename-flow', {
  kind: 'godot-test-scene',
  description: 'Tests RenameSong command.',
});

ctx.asset('cross-agg', {
  kind: 'godot-test-scene',
  description: 'Tests cross-aggregate flow.',
  targets: [song],
});

app.asset('integration', {
  kind: 'godot-test-scene',
  description: 'Cross-context integration.',
});

const reg = app.getRegistry();
console.log('Registered:', reg.getAll().map(r => r.id));
console.log('Assets:', reg.getByKind('asset').map(r => r.id));
console.log('AssetKinds:', reg.getByKind('assetKind').map(r => r.id));
"
```

Expected output:
```
Registered: [project.smoke-test, assetKind.godot-test-scene, context.Comp, aggregate.Comp.Song, asset.Comp.Song.rename-flow, asset.Comp.cross-agg, asset.integration]
Assets: [asset.Comp.Song.rename-flow, asset.Comp.cross-agg, asset.integration]
AssetKinds: [assetKind.godot-test-scene]
```

- [ ] **Step 4: Final commit (if any cleanup needed)**

No new changes expected. The feature is complete.
