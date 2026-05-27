import { describe, test, expect, beforeEach } from "bun:test";
import { ApplyEngine } from "../../src/engine/apply-engine";
import { Planner } from "../../src/planner/planner";
import { HashComputer } from "../../src/planner/hash-computer";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { StateDatabase } from "../../src/state/state-database";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { PromptBuilder } from "../../src/engine/prompt-builder";
import { ConstraintLoop } from "../../src/engine/constraint-loop";
import { makeResource } from "../helpers";
import type { ILlmClient } from "../../src/engine/llm-client";
import { mkdtemp, rm } from "fs/promises";
import { join } from "path";
import { tmpdir } from "os";

function mockLlmClient(response: string): ILlmClient {
  return {
    modelId: "test-model",
    async generate(): Promise<string> {
      return response;
    },
  };
}

describe("ApplyEngine", () => {
  let registry: ResourceRegistry;
  let state: StateDatabase;
  let tempDir: string;

  beforeEach(async () => {
    registry = new ResourceRegistry();
    state = new StateDatabase(":memory:");
    tempDir = await mkdtemp(join(tmpdir(), "crest-test-"));
  });

  test("creates new resources and writes files to disk", async () => {
    registry.register(
      makeResource({ id: "vo.Comp.Ticks", kind: "valueObject", declaration: { from: "number" } }),
    );

    const llm = mockLlmClient(
      '```ts\n// path: src/ticks.ts\nexport type Ticks = number;\n```',
    );

    const hashComputer = new HashComputer("test-model");
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder({ language: "typescript" });
    const checker = new InvariantChecker([]);
    const constraintLoop = new ConstraintLoop(checker);

    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer);
    const result = await engine.apply(registry, state, llm, { outputDir: tempDir });

    expect(result.status).toBe("ok");
    expect(result.created).toBe(1);

    const fileContent = await Bun.file(join(tempDir, "src/ticks.ts")).text();
    expect(fileContent).toContain("Ticks");

    const storedResource = state.getResource("vo.Comp.Ticks");
    expect(storedResource).not.toBeNull();
  });

  test("skips resources with unchanged hashes", async () => {
    registry.register(
      makeResource({ id: "vo.Comp.Ticks", kind: "valueObject" }),
    );

    const hashComputer = new HashComputer("test-model");
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

    const llm = mockLlmClient("");
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder({ language: "typescript" });
    const checker = new InvariantChecker([]);
    const constraintLoop = new ConstraintLoop(checker);

    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer);
    const result = await engine.apply(registry, state, llm, { outputDir: tempDir });

    expect(result.status).toBe("ok");
    expect(result.created).toBe(0);
  });

  test("records generation in state for audit trail", async () => {
    registry.register(
      makeResource({ id: "vo.Comp.Ticks", kind: "valueObject" }),
    );

    const llm = mockLlmClient(
      '```ts\n// path: src/ticks.ts\nexport type Ticks = number;\n```',
    );

    const hashComputer = new HashComputer("test-model");
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder({ language: "typescript" });
    const checker = new InvariantChecker([]);
    const constraintLoop = new ConstraintLoop(checker);

    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer);
    await engine.apply(registry, state, llm, { outputDir: tempDir });

    const gens = state.getGenerationsForResource("vo.Comp.Ticks");
    expect(gens).toHaveLength(1);
    expect(gens[0].outcome).toBe("accepted");
  });
});
