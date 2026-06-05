import { describe, test, expect } from "bun:test";
import { HashComputer } from "../../src/planner/hash-computer";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";

describe("asset integration: hash cascade", () => {
  const hashComputer = new HashComputer("claude-sonnet-4-6");

  test("asset effective hash changes when assetKind changes", () => {
    const registry1 = new ResourceRegistry();
    registry1.register(makeResource({
      id: "assetKind.godot-test-scene",
      kind: "assetKind",
      name: "godot-test-scene",
      context: null,
      declaration: { description: "Version 1 of the test scene pattern." },
    }));
    registry1.register(makeResource({
      id: "asset.Comp.rename-flow",
      kind: "asset",
      name: "rename-flow",
      context: "Comp",
      declaration: { assetKind: "godot-test-scene", description: "Tests rename." },
      dependencies: [
        { targetId: "assetKind.godot-test-scene", kind: "uses" },
      ],
    }));

    const hashes1 = hashComputer.computeAll(registry1);

    const registry2 = new ResourceRegistry();
    registry2.register(makeResource({
      id: "assetKind.godot-test-scene",
      kind: "assetKind",
      name: "godot-test-scene",
      context: null,
      declaration: { description: "Version 2 — CHANGED description." },
    }));
    registry2.register(makeResource({
      id: "asset.Comp.rename-flow",
      kind: "asset",
      name: "rename-flow",
      context: "Comp",
      declaration: { assetKind: "godot-test-scene", description: "Tests rename." },
      dependencies: [
        { targetId: "assetKind.godot-test-scene", kind: "uses" },
      ],
    }));

    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("asset.Comp.rename-flow")).not.toBe(hashes2.get("asset.Comp.rename-flow"));
  });

  test("asset effective hash changes when target resource changes", () => {
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

  test("asset effective hash is stable when nothing changes", () => {
    const registry1 = new ResourceRegistry();
    registry1.register(makeResource({
      id: "assetKind.godot-test-scene",
      kind: "assetKind",
      name: "godot-test-scene",
      context: null,
      declaration: { description: "Test scene." },
    }));
    registry1.register(makeResource({
      id: "asset.Comp.rename-flow",
      kind: "asset",
      name: "rename-flow",
      context: "Comp",
      declaration: { assetKind: "godot-test-scene", description: "Tests rename." },
      dependencies: [
        { targetId: "assetKind.godot-test-scene", kind: "uses" },
      ],
    }));

    const registry2 = new ResourceRegistry();
    registry2.register(makeResource({
      id: "assetKind.godot-test-scene",
      kind: "assetKind",
      name: "godot-test-scene",
      context: null,
      declaration: { description: "Test scene." },
    }));
    registry2.register(makeResource({
      id: "asset.Comp.rename-flow",
      kind: "asset",
      name: "rename-flow",
      context: "Comp",
      declaration: { assetKind: "godot-test-scene", description: "Tests rename." },
      dependencies: [
        { targetId: "assetKind.godot-test-scene", kind: "uses" },
      ],
    }));

    const hashes1 = hashComputer.computeAll(registry1);
    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("asset.Comp.rename-flow")).toBe(hashes2.get("asset.Comp.rename-flow"));
  });
});
