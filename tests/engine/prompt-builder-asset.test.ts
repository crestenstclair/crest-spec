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
