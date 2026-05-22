import { describe, test, expect, beforeEach } from "bun:test";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";

describe("ResourceRegistry", () => {
  let registry: ResourceRegistry;

  beforeEach(() => {
    registry = new ResourceRegistry();
  });

  test("register and getById", () => {
    const r = makeResource({ id: "aggregate.Comp.Song" });
    registry.register(r);
    expect(registry.getById("aggregate.Comp.Song")).toEqual(r);
  });

  test("register throws on duplicate ID", () => {
    const r = makeResource({ id: "aggregate.Comp.Song" });
    registry.register(r);
    expect(() => registry.register(r)).toThrow("Duplicate resource ID");
  });

  test("getById returns null for unknown ID", () => {
    expect(registry.getById("nonexistent")).toBeNull();
  });

  test("getAll returns all registered resources", () => {
    registry.register(makeResource({ id: "a.1" }));
    registry.register(makeResource({ id: "a.2" }));
    expect(registry.getAll()).toHaveLength(2);
  });

  test("getByKind filters by kind", () => {
    registry.register(makeResource({ id: "a.1", kind: "aggregate" }));
    registry.register(makeResource({ id: "p.1", kind: "port" }));
    expect(registry.getByKind("aggregate")).toHaveLength(1);
    expect(registry.getByKind("port")).toHaveLength(1);
  });

  test("getByContext filters by context", () => {
    registry.register(makeResource({ id: "a.1", context: "Comp" }));
    registry.register(makeResource({ id: "a.2", context: "Playback" }));
    expect(registry.getByContext("Comp")).toHaveLength(1);
  });

  test("getDependents returns resources that depend on the given ID", () => {
    registry.register(makeResource({ id: "port.Comp.Render", kind: "port" }));
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );
    const dependents = registry.getDependents("port.Comp.Render");
    expect(dependents).toHaveLength(1);
    expect(dependents[0].id).toBe("agg.Comp.Linear");
  });

  test("getDependencies returns resources this one depends on", () => {
    registry.register(makeResource({ id: "port.Comp.Render", kind: "port" }));
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );
    const deps = registry.getDependencies("agg.Comp.Linear");
    expect(deps).toHaveLength(1);
    expect(deps[0].id).toBe("port.Comp.Render");
  });

  test("topologicalOrder puts dependencies before dependents", () => {
    registry.register(makeResource({ id: "port.Render", kind: "port" }));
    registry.register(
      makeResource({
        id: "agg.Linear",
        dependencies: [{ targetId: "port.Render", kind: "implements" }],
      }),
    );
    registry.register(
      makeResource({
        id: "repo.LinearRepo",
        dependencies: [{ targetId: "agg.Linear", kind: "of" }],
      }),
    );

    const order = registry.topologicalOrder();
    const ids = order.map((r) => r.id);
    expect(ids.indexOf("port.Render")).toBeLessThan(ids.indexOf("agg.Linear"));
    expect(ids.indexOf("agg.Linear")).toBeLessThan(ids.indexOf("repo.LinearRepo"));
  });

  test("contextMap stores and retrieves relationships", () => {
    registry.setContextMap([
      { from: "Playback", to: "Composition", kind: "customer-supplier", direction: "downstream" },
    ]);
    const map = registry.getContextMap();
    expect(map).toHaveLength(1);
    expect(map[0].from).toBe("Playback");
  });

  test("invariants stores and retrieves project invariants", () => {
    registry.setInvariants([{ text: "no infra in domain" }]);
    expect(registry.getInvariants()).toHaveLength(1);
  });
});
