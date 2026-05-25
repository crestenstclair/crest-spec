import { describe, test, expect, beforeEach } from "bun:test";
import { HashComputer } from "../../src/planner/hash-computer";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";

describe("HashComputer", () => {
  let registry: ResourceRegistry;
  let hashComputer: HashComputer;

  beforeEach(() => {
    registry = new ResourceRegistry();
    hashComputer = new HashComputer("claude-sonnet-4-6");
  });

  test("computes a hash for a simple resource", () => {
    registry.register(makeResource({ id: "vo.Comp.Ticks", kind: "valueObject" }));
    const hashes = hashComputer.computeAll(registry);
    expect(hashes.get("vo.Comp.Ticks")).toBeDefined();
    expect(typeof hashes.get("vo.Comp.Ticks")).toBe("string");
    expect(hashes.get("vo.Comp.Ticks")!.length).toBe(64);
  });

  test("same declaration produces same hash (deterministic)", () => {
    registry.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        declaration: { from: "number" },
      }),
    );
    const hashes1 = hashComputer.computeAll(registry);

    const registry2 = new ResourceRegistry();
    registry2.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        declaration: { from: "number" },
      }),
    );
    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("vo.Comp.Ticks")).toBe(hashes2.get("vo.Comp.Ticks"));
  });

  test("different declarations produce different hashes", () => {
    registry.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        declaration: { from: "number" },
      }),
    );
    const hashes1 = hashComputer.computeAll(registry);

    const registry2 = new ResourceRegistry();
    registry2.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        declaration: { from: "string" },
      }),
    );
    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("vo.Comp.Ticks")).not.toBe(hashes2.get("vo.Comp.Ticks"));
  });

  test("changing meta changes the hash", () => {
    registry.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        meta: { style: "functional" },
      }),
    );
    const hashes1 = hashComputer.computeAll(registry);

    const registry2 = new ResourceRegistry();
    registry2.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        meta: { style: "OOP" },
      }),
    );
    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("vo.Comp.Ticks")).not.toBe(hashes2.get("vo.Comp.Ticks"));
  });

  test("dependency hash is folded into dependent hash", () => {
    registry.register(makeResource({ id: "port.Render", kind: "port", declaration: { contract: { render: "() => void" } } }));
    registry.register(
      makeResource({
        id: "agg.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Render", kind: "implements" }],
      }),
    );
    const hashes1 = hashComputer.computeAll(registry);

    const registry2 = new ResourceRegistry();
    registry2.register(makeResource({ id: "port.Render", kind: "port", declaration: { contract: { render: "() => NoteEvent[]" } } }));
    registry2.register(
      makeResource({
        id: "agg.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Render", kind: "implements" }],
      }),
    );
    const hashes2 = hashComputer.computeAll(registry2);

    expect(hashes1.get("agg.Linear")).not.toBe(hashes2.get("agg.Linear"));
  });

  test("changing model identifier changes all hashes", () => {
    registry.register(makeResource({ id: "vo.Ticks", kind: "valueObject" }));
    const hashes1 = hashComputer.computeAll(registry);

    const hashComputer2 = new HashComputer("claude-opus-4-6");
    const hashes2 = hashComputer2.computeAll(registry);

    expect(hashes1.get("vo.Ticks")).not.toBe(hashes2.get("vo.Ticks"));
  });
});
