import { describe, test, expect, beforeEach } from "bun:test";
import { Planner } from "../../src/planner/planner";
import { HashComputer } from "../../src/planner/hash-computer";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { StateDatabase } from "../../src/state/state-database";
import { makeResource } from "../helpers";

describe("Planner", () => {
  let registry: ResourceRegistry;
  let state: StateDatabase;
  let planner: Planner;

  beforeEach(() => {
    registry = new ResourceRegistry();
    state = new StateDatabase(":memory:");
    const hashComputer = new HashComputer("claude-sonnet-4-6");
    planner = new Planner(hashComputer);
  });

  test("new resource produces a create action", () => {
    registry.register(makeResource({ id: "vo.Comp.Ticks", kind: "valueObject" }));
    const plan = planner.plan(registry, state);
    expect(plan.actions).toHaveLength(1);
    expect(plan.actions[0].action).toBe("create");
    expect(plan.actions[0].resourceId).toBe("vo.Comp.Ticks");
    expect(plan.actions[0].reason).toBe("new resource");
  });

  test("unchanged resource produces no action", () => {
    const resource = makeResource({ id: "vo.Comp.Ticks", kind: "valueObject" });
    registry.register(resource);

    const hashComputer = new HashComputer("claude-sonnet-4-6");
    const hashes = hashComputer.computeAll(registry);

    state.upsertResource({
      id: "vo.Comp.Ticks",
      kind: "valueObject",
      context: "Comp",
      declaration_hash: "x",
      effective_hash: hashes.get("vo.Comp.Ticks")!,
      declaration_json: "{}",
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });

    const plan = planner.plan(registry, state);
    expect(plan.actions).toHaveLength(0);
  });

  test("changed resource produces a modify action", () => {
    registry.register(
      makeResource({
        id: "vo.Comp.Ticks",
        kind: "valueObject",
        declaration: { from: "number" },
      }),
    );

    state.upsertResource({
      id: "vo.Comp.Ticks",
      kind: "valueObject",
      context: "Comp",
      declaration_hash: "old",
      effective_hash: "old_effective",
      declaration_json: '{"from":"string"}',
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });

    const plan = planner.plan(registry, state);
    expect(plan.actions).toHaveLength(1);
    expect(plan.actions[0].action).toBe("modify");
    expect(plan.actions[0].reason).toContain("changed");
  });

  test("resource in state but not in registry produces a destroy action", () => {
    state.upsertResource({
      id: "vo.Comp.OldThing",
      kind: "valueObject",
      context: "Comp",
      declaration_hash: "x",
      effective_hash: "y",
      declaration_json: "{}",
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });

    const plan = planner.plan(registry, state);
    expect(plan.actions).toHaveLength(1);
    expect(plan.actions[0].action).toBe("destroy");
    expect(plan.actions[0].resourceId).toBe("vo.Comp.OldThing");
  });

  test("cascade: changing a port cascades to implementors", () => {
    registry.register(
      makeResource({
        id: "port.Render",
        kind: "port",
        declaration: { contract: { render: "() => NoteEvent[]" } },
      }),
    );
    registry.register(
      makeResource({
        id: "agg.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Render", kind: "implements" }],
      }),
    );

    state.upsertResource({
      id: "port.Render",
      kind: "port",
      context: null,
      declaration_hash: "old",
      effective_hash: "old_port_hash",
      declaration_json: '{"contract":{"render":"() => void"}}',
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });
    state.upsertResource({
      id: "agg.Linear",
      kind: "aggregate",
      context: null,
      declaration_hash: "old",
      effective_hash: "old_agg_hash",
      declaration_json: "{}",
      layer: null,
      settled_at: null,
      last_apply_id: null,
    });

    const plan = planner.plan(registry, state);
    const actions = plan.actions.sort((a, b) => a.resourceId.localeCompare(b.resourceId));
    expect(actions).toHaveLength(2);
    expect(actions.find((a) => a.resourceId === "port.Render")!.action).toBe("modify");
    expect(actions.find((a) => a.resourceId === "agg.Linear")!.action).toBe("modify");
    expect(actions.find((a) => a.resourceId === "agg.Linear")!.cascadedFrom).toBe("port.Render");
  });

  test("plan display shows formatted output", () => {
    registry.register(makeResource({ id: "vo.Ticks", kind: "valueObject" }));
    const plan = planner.plan(registry, state);
    const output = plan.display();
    expect(output).toContain("+ vo.Ticks");
    expect(output).toContain("reason: new resource");
  });

  test("empty plan displays no changes", () => {
    const plan = planner.plan(registry, state);
    expect(plan.display()).toBe("No changes detected.");
  });
});
