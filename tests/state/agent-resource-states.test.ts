import { describe, test, expect, beforeEach } from "bun:test";
import { StateDatabase } from "../../src/state/state-database";

describe("agent_resource_states", () => {
  let state: StateDatabase;
  let applyId: number;

  beforeEach(() => {
    state = new StateDatabase(":memory:");
    const apply = state.beginApply("test-hash");
    applyId = apply.id;
  });

  test("setResourceState inserts a new state", () => {
    state.setResourceState(applyId, "vo.K.A", "pending");
    const rec = state.getResourceState(applyId, "vo.K.A");
    expect(rec).not.toBeNull();
    expect(rec!.state).toBe("pending");
    expect(rec!.context_json).toBeNull();
  });

  test("setResourceState upserts on conflict", () => {
    state.setResourceState(applyId, "vo.K.A", "pending");
    state.setResourceState(applyId, "vo.K.A", "dispatched");
    const rec = state.getResourceState(applyId, "vo.K.A");
    expect(rec!.state).toBe("dispatched");
  });

  test("setResourceState stores context_json", () => {
    const ctx = JSON.stringify({ reason: "need decision", retryable: true });
    state.setResourceState(applyId, "vo.K.A", "blocked", ctx);
    const rec = state.getResourceState(applyId, "vo.K.A");
    expect(rec!.state).toBe("blocked");
    expect(rec!.context_json).toBe(ctx);
  });

  test("getResourceState returns null for unknown resource", () => {
    const rec = state.getResourceState(applyId, "vo.K.Missing");
    expect(rec).toBeNull();
  });

  test("getResourceStatesForApply returns all states for an apply", () => {
    state.setResourceState(applyId, "vo.K.A", "pending");
    state.setResourceState(applyId, "vo.K.B", "dispatched");
    state.setResourceState(applyId, "vo.K.C", "committed");

    const states = state.getResourceStatesForApply(applyId);
    expect(states).toHaveLength(3);
    expect(states.map((s) => s.resource_id).sort()).toEqual(["vo.K.A", "vo.K.B", "vo.K.C"]);
  });

  test("states are scoped to apply_id", () => {
    state.setResourceState(applyId, "vo.K.A", "pending");

    const apply2 = state.beginApply("test-hash-2");
    state.setResourceState(apply2.id, "vo.K.A", "committed");

    const s1 = state.getResourceState(applyId, "vo.K.A");
    const s2 = state.getResourceState(apply2.id, "vo.K.A");

    expect(s1!.state).toBe("pending");
    expect(s2!.state).toBe("committed");
  });

  test("state transition sequence works", () => {
    state.setResourceState(applyId, "vo.K.A", "pending");
    state.setResourceState(applyId, "vo.K.A", "dispatched");
    state.setResourceState(applyId, "vo.K.A", "completed");
    state.setResourceState(applyId, "vo.K.A", "committed");

    const rec = state.getResourceState(applyId, "vo.K.A");
    expect(rec!.state).toBe("committed");
  });
});
