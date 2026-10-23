import { describe, test, expect, beforeEach } from "bun:test";
import { AgentSession } from "../../src/engine/agent-session";
import { StateDatabase } from "../../src/state/state-database";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { Planner } from "../../src/planner/planner";
import { HashComputer } from "../../src/planner/hash-computer";
import { WaveComputer } from "../../src/engine/wave-computer";
import { PromptBuilder } from "../../src/engine/prompt-builder";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { ResourceValidator } from "../../src/engine/resource-validator";
import { makeResource } from "../helpers";

describe("AgentSession states", () => {
  let state: StateDatabase;
  let registry: ResourceRegistry;
  let session: AgentSession;

  beforeEach(() => {
    state = new StateDatabase(":memory:");
    registry = new ResourceRegistry();
    const hashComputer = new HashComputer("test-model");
    session = new AgentSession(
      state,
      new Planner(hashComputer),
      new WaveComputer(),
      hashComputer,
      new PromptBuilder({ language: "typescript" }),
      new ResourceValidator(new InvariantChecker([]), {}),
    );
  });

  test("begin initializes all resources to pending", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(makeResource({ id: "vo.K.B", kind: "valueObject" }));

    const result = session.begin(registry);
    const states = state.getResourceStatesForApply(result.applyId);

    expect(states).toHaveLength(2);
    expect(states.every((s) => s.state === "pending")).toBe(true);
  });

  test("dispatch sets state to dispatched", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    const begin = session.begin(registry);

    const result = session.dispatch("vo.K.A");
    expect(result.state).toBe("dispatched");

    const rec = state.getResourceState(begin.applyId, "vo.K.A");
    expect(rec!.state).toBe("dispatched");
  });

  test("complete sets state to completed", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    const begin = session.begin(registry);

    session.dispatch("vo.K.A");
    const result = session.complete("vo.K.A");
    expect(result.state).toBe("completed");

    const rec = state.getResourceState(begin.applyId, "vo.K.A");
    expect(rec!.state).toBe("completed");
  });

  test("block sets state with context", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    const begin = session.begin(registry);

    session.dispatch("vo.K.A");
    const ctx = { reason: "need decision on API shape", retryable: true, question: "sync or async?" };
    const result = session.block("vo.K.A", ctx);
    expect(result.state).toBe("blocked");

    const rec = state.getResourceState(begin.applyId, "vo.K.A");
    expect(rec!.state).toBe("blocked");
    expect(JSON.parse(rec!.context_json!)).toEqual(ctx);
  });

  test("error sets state with context", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    const begin = session.begin(registry);

    session.dispatch("vo.K.A");
    const ctx = { errorKind: "compile" as const, message: "missing import", retryCount: 1, maxRetries: 3 };
    const result = session.error("vo.K.A", ctx);
    expect(result.state).toBe("errored");

    const rec = state.getResourceState(begin.applyId, "vo.K.A");
    expect(rec!.state).toBe("errored");
    expect(JSON.parse(rec!.context_json!)).toEqual(ctx);
  });

  test("timeout sets state", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    const begin = session.begin(registry);

    session.dispatch("vo.K.A");
    const result = session.timeout("vo.K.A");
    expect(result.state).toBe("timed_out");

    const rec = state.getResourceState(begin.applyId, "vo.K.A");
    expect(rec!.state).toBe("timed_out");
  });

  test("reject sets state with errors", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    const begin = session.begin(registry);

    session.dispatch("vo.K.A");
    session.complete("vo.K.A");
    const result = session.reject("vo.K.A", ["invariant violated: no mutex on audio thread"]);
    expect(result.state).toBe("rejected");

    const rec = state.getResourceState(begin.applyId, "vo.K.A");
    expect(rec!.state).toBe("rejected");
    expect(JSON.parse(rec!.context_json!)).toEqual({
      errors: ["invariant violated: no mutex on audio thread"],
    });
  });

  test("commit sets state to committed", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    const begin = session.begin(registry);

    session.dispatch("vo.K.A");
    session.complete("vo.K.A");
    session.commit("vo.K.A", registry, new Map(), "/tmp");

    const rec = state.getResourceState(begin.applyId, "vo.K.A");
    expect(rec!.state).toBe("committed");
  });

  test("state methods throw without active session", () => {
    expect(() => session.dispatch("vo.K.A")).toThrow(/No active agent session/);
    expect(() => session.complete("vo.K.A")).toThrow(/No active agent session/);
    expect(() => session.block("vo.K.A", { reason: "x", retryable: false })).toThrow(/No active agent session/);
    expect(() => session.error("vo.K.A", { errorKind: "unknown", message: "x", retryCount: 0, maxRetries: 3 })).toThrow(/No active agent session/);
    expect(() => session.timeout("vo.K.A")).toThrow(/No active agent session/);
    expect(() => session.status()).toThrow(/No active agent session/);
  });
});

describe("AgentSession status", () => {
  let state: StateDatabase;
  let registry: ResourceRegistry;
  let session: AgentSession;

  beforeEach(() => {
    state = new StateDatabase(":memory:");
    registry = new ResourceRegistry();
    const hashComputer = new HashComputer("test-model");
    session = new AgentSession(
      state,
      new Planner(hashComputer),
      new WaveComputer(),
      hashComputer,
      new PromptBuilder({ language: "typescript" }),
      new ResourceValidator(new InvariantChecker([]), {}),
    );
  });

  test("status returns current wave with resource states", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(makeResource({ id: "vo.K.B", kind: "valueObject" }));
    session.begin(registry);

    session.dispatch("vo.K.A");

    const result = session.status();
    expect(result.wave).toBe(0);
    expect(result.done).toBe(false);
    expect(result.resources).toHaveLength(2);

    const a = result.resources.find((r) => r.resourceId === "vo.K.A")!;
    const b = result.resources.find((r) => r.resourceId === "vo.K.B")!;
    expect(a.state).toBe("dispatched");
    expect(b.state).toBe("pending");
  });

  test("status summary counts states correctly", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(makeResource({ id: "vo.K.B", kind: "valueObject" }));
    registry.register(makeResource({ id: "vo.K.C", kind: "valueObject" }));
    session.begin(registry);

    session.dispatch("vo.K.A");
    session.dispatch("vo.K.B");
    session.error("vo.K.B", { errorKind: "compile", message: "err", retryCount: 1, maxRetries: 3 });

    const result = session.status();
    expect(result.summary.pending).toBe(1);
    expect(result.summary.dispatched).toBe(1);
    expect(result.summary.errored).toBe(1);
  });

  test("status needsAttention includes blocked, errored, timed_out, rejected", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(makeResource({ id: "vo.K.B", kind: "valueObject" }));
    registry.register(makeResource({ id: "vo.K.C", kind: "valueObject" }));
    registry.register(makeResource({ id: "vo.K.D", kind: "valueObject" }));
    session.begin(registry);

    session.dispatch("vo.K.A");
    session.block("vo.K.B", { reason: "need decision", retryable: true });
    session.error("vo.K.C", { errorKind: "compile", message: "err", retryCount: 3, maxRetries: 3 });
    session.timeout("vo.K.D");

    const result = session.status();
    expect(result.needsAttention).toHaveLength(3);
    expect(result.needsAttention.map((r) => r.resourceId).sort()).toEqual(["vo.K.B", "vo.K.C", "vo.K.D"]);
  });

  test("status returns done when all committed", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    const begin = session.begin(registry);

    session.dispatch("vo.K.A");
    session.complete("vo.K.A");
    session.commit("vo.K.A", registry, new Map(), "/tmp");

    const result = session.status();
    expect(result.done).toBe(true);
    expect(result.wave).toBe(-1);
  });

  test("status advances to next wave when current wave is all committed", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(
      makeResource({
        id: "agg.K.B",
        kind: "aggregate",
        dependencies: [{ targetId: "vo.K.A", kind: "uses" }],
      }),
    );
    session.begin(registry);

    session.dispatch("vo.K.A");
    session.complete("vo.K.A");
    session.commit("vo.K.A", registry, new Map(), "/tmp");

    const result = session.status();
    expect(result.wave).toBe(1);
    const b = result.resources.find((r) => r.resourceId === "agg.K.B")!;
    expect(b.state).toBe("pending");
  });

  test("status includes context for blocked/errored resources", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    session.begin(registry);

    const blockedCtx = { reason: "need API shape", retryable: true, question: "REST or gRPC?" };
    session.block("vo.K.A", blockedCtx);

    const result = session.status();
    const a = result.resources.find((r) => r.resourceId === "vo.K.A")!;
    expect(a.context).toEqual(blockedCtx);
  });
});
