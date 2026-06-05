import { describe, test, expect, beforeEach } from "bun:test";
import { ApplyEngine } from "../../src/engine/apply-engine";
import { Planner } from "../../src/planner/planner";
import { HashComputer } from "../../src/planner/hash-computer";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { StateDatabase } from "../../src/state/state-database";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { PromptBuilder } from "../../src/engine/prompt-builder";
import { ConstraintLoop } from "../../src/engine/constraint-loop";
import { WaveComputer } from "../../src/engine/wave-computer";
import { makeResource } from "../helpers";
import type { ILlmClient } from "../../src/engine/llm-client";
import type { IWaveVerifier, WaveVerificationResult } from "../../src/engine/wave-verifier";
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

  test("wave mode processes resources in dependency order", async () => {
    const callOrder: string[] = [];

    registry.register(makeResource({ id: "vo.K.Base", kind: "valueObject", name: "Base" }));
    registry.register(
      makeResource({
        id: "agg.C.Top",
        kind: "aggregate",
        name: "Top",
        dependencies: [{ targetId: "vo.K.Base", kind: "uses" }],
      }),
    );

    const llm: ILlmClient = {
      modelId: "test-model",
      async generate(prompt: string): Promise<string> {
        if (prompt.includes('## Resource: valueObject "Base"')) {
          callOrder.push("vo.K.Base");
          return '```ts\n// path: src/base.ts\nexport type Base = number;\n```';
        }
        callOrder.push("agg.C.Top");
        return '```ts\n// path: src/top.ts\nexport class Top {}\n```';
      },
    };

    let verifyCallCount = 0;
    const verifier: IWaveVerifier = {
      async verify(): Promise<WaveVerificationResult> {
        verifyCallCount++;
        return { passed: true, errors: [], rawOutput: "" };
      },
    };

    const hashComputer = new HashComputer("test-model");
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder({ language: "typescript" });
    const checker = new InvariantChecker([]);
    const constraintLoop = new ConstraintLoop(checker, { skipTypeCheckInLoop: true, skipLlmVerify: true });
    const waveComputer = new WaveComputer();

    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer, waveComputer, verifier);
    const result = await engine.apply(registry, state, llm, {
      outputDir: tempDir,
      waveVerifyCommand: ["true"],
    });

    expect(result.status).toBe("ok");
    expect(result.created).toBe(2);
    expect(callOrder).toEqual(["vo.K.Base", "agg.C.Top"]);
    expect(verifyCallCount).toBe(2);
  });

  test("wave verification failure retries the failed resource", async () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));

    let generateCount = 0;
    const llm: ILlmClient = {
      modelId: "test-model",
      async generate(): Promise<string> {
        generateCount++;
        return '```ts\n// path: src/a.ts\nexport type A = number;\n```';
      },
    };

    let verifyCount = 0;
    const verifier: IWaveVerifier = {
      async verify(): Promise<WaveVerificationResult> {
        verifyCount++;
        if (verifyCount === 1) {
          return {
            passed: false,
            errors: [{ resourceId: "vo.K.A", filePath: "src/a.ts", errorText: "error CS1234" }],
            rawOutput: "error CS1234",
          };
        }
        return { passed: true, errors: [], rawOutput: "" };
      },
    };

    const hashComputer = new HashComputer("test-model");
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder({ language: "typescript" });
    const checker = new InvariantChecker([]);
    const constraintLoop = new ConstraintLoop(checker, { skipTypeCheckInLoop: true, skipLlmVerify: true });
    const waveComputer = new WaveComputer();

    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer, waveComputer, verifier);
    const result = await engine.apply(registry, state, llm, {
      outputDir: tempDir,
      waveVerifyCommand: ["true"],
    });

    expect(result.status).toBe("ok");
    expect(generateCount).toBe(2);
    expect(verifyCount).toBe(2);
  });

  test("wave mode stops when retries exhausted", async () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));
    registry.register(
      makeResource({
        id: "agg.C.B",
        kind: "aggregate",
        dependencies: [{ targetId: "vo.K.A", kind: "uses" }],
      }),
    );

    const llm = mockLlmClient('```ts\n// path: src/a.ts\nexport type A = number;\n```');

    const verifier: IWaveVerifier = {
      async verify(): Promise<WaveVerificationResult> {
        return {
          passed: false,
          errors: [{ resourceId: "vo.K.A", filePath: "src/a.ts", errorText: "persistent error" }],
          rawOutput: "persistent error",
        };
      },
    };

    const hashComputer = new HashComputer("test-model");
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder({ language: "typescript" });
    const checker = new InvariantChecker([]);
    const constraintLoop = new ConstraintLoop(checker, { skipTypeCheckInLoop: true, skipLlmVerify: true });
    const waveComputer = new WaveComputer();

    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer, waveComputer, verifier);
    const result = await engine.apply(registry, state, llm, {
      outputDir: tempDir,
      waveVerifyCommand: ["true"],
      waveMaxRetries: 1,
    });

    expect(result.status).toBe("failed");
    // agg.C.B should never have been attempted since wave 1 failed
    const bResource = state.getResource("agg.C.B");
    expect(bResource).toBeNull();
  });

  test("wave mode injects existing dependency files into wave 2+ prompts", async () => {
    const capturedPrompts: Map<string, string> = new Map();

    registry.register(makeResource({ id: "vo.K.Base", kind: "valueObject", name: "Base" }));
    registry.register(
      makeResource({
        id: "repo.C.Things",
        kind: "repository",
        name: "Things",
        dependencies: [{ targetId: "vo.K.Base", kind: "uses" }],
      }),
    );

    const llm: ILlmClient = {
      modelId: "test-model",
      async generate(prompt: string): Promise<string> {
        if (prompt.includes('## Resource: valueObject "Base"')) {
          capturedPrompts.set("vo.K.Base", prompt);
          return '```ts\n// path: src/Base/Base.ts\nexport type Base = { id: string };\n```';
        }
        capturedPrompts.set("repo.C.Things", prompt);
        return '```ts\n// path: src/Things/ThingsRepo.ts\nimport { Base } from "../Base/Base";\n```';
      },
    };

    const verifier: IWaveVerifier = {
      async verify(): Promise<WaveVerificationResult> {
        return { passed: true, errors: [], rawOutput: "" };
      },
    };

    const hashComputer = new HashComputer("test-model");
    const planner = new Planner(hashComputer);
    const promptBuilder = new PromptBuilder({ language: "typescript" });
    const checker = new InvariantChecker([]);
    const constraintLoop = new ConstraintLoop(checker, { skipTypeCheckInLoop: true, skipLlmVerify: true });
    const waveComputer = new WaveComputer();

    const engine = new ApplyEngine(planner, promptBuilder, constraintLoop, hashComputer, waveComputer, verifier);
    const result = await engine.apply(registry, state, llm, {
      outputDir: tempDir,
      waveVerifyCommand: ["true"],
    });

    expect(result.status).toBe("ok");
    expect(result.created).toBe(2);

    const basePrompt = capturedPrompts.get("vo.K.Base")!;
    expect(basePrompt).not.toContain("Existing Generated Files");

    const repoPrompt = capturedPrompts.get("repo.C.Things")!;
    expect(repoPrompt).toContain("Existing Generated Files");
    expect(repoPrompt).toContain("src/Base/Base.ts");
    expect(repoPrompt).toContain("export type Base = { id: string };");
    expect(repoPrompt).toContain("DO NOT regenerate");
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
