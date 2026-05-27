import { describe, test, expect, beforeEach } from "bun:test";
import { ProjectBuilder } from "../../src/dsl/project-builder";

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
