import { describe, test, expect } from "bun:test";
import { WaveComputer } from "../../src/engine/wave-computer";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";
import type { PlannedAction } from "../../src/planner/plan";

function action(resourceId: string): PlannedAction {
  return { resourceId, action: "create", reason: "new", affectedFiles: [] };
}

describe("WaveComputer", () => {
  const computer = new WaveComputer();

  test("empty plan returns empty waves", () => {
    const registry = new ResourceRegistry();
    expect(computer.compute([], registry)).toEqual([]);
  });

  test("single action returns one wave", () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "vo.K.Ticks", kind: "valueObject" }));

    const waves = computer.compute([action("vo.K.Ticks")], registry);
    expect(waves).toHaveLength(1);
    expect(waves[0]).toHaveLength(1);
    expect(waves[0][0].resourceId).toBe("vo.K.Ticks");
  });

  test("independent resources land in the same wave", () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(makeResource({ id: "vo.K.B", kind: "valueObject" }));
    registry.register(makeResource({ id: "vo.K.C", kind: "valueObject" }));

    const waves = computer.compute(
      [action("vo.K.A"), action("vo.K.B"), action("vo.K.C")],
      registry,
    );
    expect(waves).toHaveLength(1);
    expect(waves[0]).toHaveLength(3);
  });

  test("linear chain produces one resource per wave", () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "vo.K.Base", kind: "valueObject" }));
    registry.register(
      makeResource({
        id: "agg.C.Mid",
        kind: "aggregate",
        dependencies: [{ targetId: "vo.K.Base", kind: "uses" }],
      }),
    );
    registry.register(
      makeResource({
        id: "svc.C.Top",
        kind: "applicationService",
        dependencies: [{ targetId: "agg.C.Mid", kind: "uses" }],
      }),
    );

    const waves = computer.compute(
      [action("vo.K.Base"), action("agg.C.Mid"), action("svc.C.Top")],
      registry,
    );
    expect(waves).toHaveLength(3);
    expect(waves[0][0].resourceId).toBe("vo.K.Base");
    expect(waves[1][0].resourceId).toBe("agg.C.Mid");
    expect(waves[2][0].resourceId).toBe("svc.C.Top");
  });

  test("diamond dependency produces three waves", () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(
      makeResource({
        id: "agg.C.B",
        kind: "aggregate",
        dependencies: [{ targetId: "vo.K.A", kind: "uses" }],
      }),
    );
    registry.register(
      makeResource({
        id: "agg.C.C",
        kind: "aggregate",
        dependencies: [{ targetId: "vo.K.A", kind: "uses" }],
      }),
    );
    registry.register(
      makeResource({
        id: "svc.C.D",
        kind: "applicationService",
        dependencies: [
          { targetId: "agg.C.B", kind: "uses" },
          { targetId: "agg.C.C", kind: "uses" },
        ],
      }),
    );

    const waves = computer.compute(
      [action("vo.K.A"), action("agg.C.B"), action("agg.C.C"), action("svc.C.D")],
      registry,
    );
    expect(waves).toHaveLength(3);
    expect(waves[0].map((a) => a.resourceId)).toEqual(["vo.K.A"]);
    expect(waves[1].map((a) => a.resourceId).sort()).toEqual(["agg.C.B", "agg.C.C"]);
    expect(waves[2].map((a) => a.resourceId)).toEqual(["svc.C.D"]);
  });

  test("dependencies on settled resources (not in action set) are ignored", () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "vo.K.Settled", kind: "valueObject" }));
    registry.register(
      makeResource({
        id: "agg.C.New",
        kind: "aggregate",
        dependencies: [{ targetId: "vo.K.Settled", kind: "uses" }],
      }),
    );
    registry.register(
      makeResource({
        id: "svc.C.Also",
        kind: "applicationService",
        dependencies: [{ targetId: "vo.K.Settled", kind: "uses" }],
      }),
    );

    // Only generating agg.C.New and svc.C.Also — vo.K.Settled is already on disk
    const waves = computer.compute(
      [action("agg.C.New"), action("svc.C.Also")],
      registry,
    );
    expect(waves).toHaveLength(1);
    expect(waves[0]).toHaveLength(2);
  });

  test("parallel chains interleave into shared waves", () => {
    const registry = new ResourceRegistry();
    // Chain 1: A -> B
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(
      makeResource({
        id: "agg.X.B",
        kind: "aggregate",
        dependencies: [{ targetId: "vo.K.A", kind: "uses" }],
      }),
    );
    // Chain 2: C -> D
    registry.register(makeResource({ id: "vo.K.C", kind: "valueObject" }));
    registry.register(
      makeResource({
        id: "agg.Y.D",
        kind: "aggregate",
        dependencies: [{ targetId: "vo.K.C", kind: "uses" }],
      }),
    );

    const waves = computer.compute(
      [action("vo.K.A"), action("vo.K.C"), action("agg.X.B"), action("agg.Y.D")],
      registry,
    );
    expect(waves).toHaveLength(2);
    expect(waves[0].map((a) => a.resourceId).sort()).toEqual(["vo.K.A", "vo.K.C"]);
    expect(waves[1].map((a) => a.resourceId).sort()).toEqual(["agg.X.B", "agg.Y.D"]);
  });
});
