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

describe("AgentSession", () => {
  let state: StateDatabase;
  let registry: ResourceRegistry;
  let hashComputer: HashComputer;
  let planner: Planner;
  let waveComputer: WaveComputer;
  let promptBuilder: PromptBuilder;
  let validator: ResourceValidator;

  beforeEach(() => {
    state = new StateDatabase(":memory:");
    registry = new ResourceRegistry();
    hashComputer = new HashComputer("test-model");
    planner = new Planner(hashComputer);
    waveComputer = new WaveComputer();
    promptBuilder = new PromptBuilder({ language: "typescript" });
    validator = new ResourceValidator(new InvariantChecker([]), {});
  });

  function makeSession(): AgentSession {
    return new AgentSession(state, planner, waveComputer, hashComputer, promptBuilder, validator);
  }

  test("begin creates session and returns plan", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(makeResource({ id: "vo.K.B", kind: "valueObject" }));

    const session = makeSession();
    const result = session.begin(registry);

    expect(result.applyId).toBeGreaterThan(0);
    expect(result.plan).toHaveLength(2);
    expect(result.waves).toHaveLength(1);
    expect(result.totalResources).toBe(2);
  });

  test("begin fails when session already active", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));

    const session = makeSession();
    session.begin(registry);

    expect(() => session.begin(registry)).toThrow(/locked/i);
  });

  test("next returns resources from current wave", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));

    const session = makeSession();
    session.begin(registry);

    const next = session.next();
    expect(next.done).toBe(false);
    expect(next.wave).toBe(0);
    expect(next.resources).toHaveLength(1);
    expect(next.resources[0].resourceId).toBe("vo.K.A");
  });

  test("next returns done after all resources committed", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));

    const session = makeSession();
    const beginResult = session.begin(registry);

    state.recordAction(beginResult.applyId, "vo.K.A", "create", "success");

    const next = session.next();
    expect(next.done).toBe(true);
    expect(next.resources).toHaveLength(0);
  });

  test("next advances waves when current wave is fully committed", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(
      makeResource({
        id: "agg.K.B",
        kind: "aggregate",
        dependencies: [{ targetId: "vo.K.A", kind: "uses" }],
      }),
    );

    const session = makeSession();
    const beginResult = session.begin(registry);

    // Wave 0: vo.K.A
    const wave0 = session.next();
    expect(wave0.wave).toBe(0);
    expect(wave0.resources[0].resourceId).toBe("vo.K.A");

    // Commit vo.K.A
    state.recordAction(beginResult.applyId, "vo.K.A", "create", "success");

    // Wave 1: agg.K.B
    const wave1 = session.next();
    expect(wave1.wave).toBe(1);
    expect(wave1.resources[0].resourceId).toBe("agg.K.B");
  });

  test("context returns system prompt and resource prompt", () => {
    registry.register(
      makeResource({
        id: "vo.K.A",
        kind: "valueObject",
        declaration: { from: "number" },
      }),
    );

    const session = makeSession();
    session.begin(registry);

    const ctx = session.context("vo.K.A", registry);
    expect(ctx.resourceId).toBe("vo.K.A");
    expect(ctx.systemPrompt).toContain("code generator");
    expect(ctx.prompt).toContain("vo.K.A");
    expect(ctx.dependencyNotes).toEqual({});
  });

  test("context includes dependency notes", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(
      makeResource({
        id: "agg.K.B",
        kind: "aggregate",
        dependencies: [{ targetId: "vo.K.A", kind: "uses" }],
      }),
    );

    const session = makeSession();
    const beginResult = session.begin(registry);

    state.addAgentNote({
      resource_id: "vo.K.A",
      apply_id: beginResult.applyId,
      content: "Used newtype pattern",
      created_at: new Date().toISOString(),
    });

    const ctx = session.context("agg.K.B", registry);
    expect(ctx.dependencyNotes["vo.K.A"]).toEqual(["Used newtype pattern"]);
    expect(ctx.prompt).toContain("Used newtype pattern");
  });

  test("note saves to database", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));

    const session = makeSession();
    const beginResult = session.begin(registry);

    const result = session.note("vo.K.A", "Design decision: used newtype");
    expect(result.saved).toBe(true);
    expect(result.noteId).toBeGreaterThan(0);

    const notes = state.getAgentNotesForApply("vo.K.A", beginResult.applyId);
    expect(notes).toHaveLength(1);
    expect(notes[0].content).toBe("Design decision: used newtype");
  });

  test("finish finalizes apply and cleans up session", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));

    const session = makeSession();
    const beginResult = session.begin(registry);
    state.recordAction(beginResult.applyId, "vo.K.A", "create", "success");

    const result = session.finish();
    expect(result.status).toBe("ok");
    expect(result.created).toBe(1);

    // Session cleaned up
    expect(state.getAgentSession(beginResult.applyId)).toBeNull();
    // Lock released
    expect(state.getLock()).toBeNull();
  });

  test("finish reports failed status when resources are uncommitted", () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));

    const session = makeSession();
    session.begin(registry);

    const result = session.finish();
    expect(result.status).toBe("failed");
    expect(result.failed).toBe(1);
  });
});
