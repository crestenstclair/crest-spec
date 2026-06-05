import { describe, test, expect, beforeEach } from "bun:test";
import { ProjectBuilder } from "../../src/dsl/project-builder";

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
    expect(resource!.dependencies).toContainEqual({
      targetId: "aggregate.Composition.Song",
      kind: "uses",
    });
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
