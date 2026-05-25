import { describe, test, expect } from "bun:test";
import { ConstraintLoop } from "../../src/engine/constraint-loop";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { makeResource } from "../helpers";
import type { ILlmClient } from "../../src/engine/llm-client";

function mockLlmClient(responses: string[]): ILlmClient {
  let callIndex = 0;
  return {
    modelId: "test-model",
    async generate(_prompt: string, _system: string): Promise<string> {
      return responses[callIndex++] ?? "";
    },
  };
}

describe("ConstraintLoop", () => {
  test("returns files on first success with no invariant violations", async () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "vo.Ticks", kind: "valueObject" }));

    const checker = new InvariantChecker([]);
    const llm = mockLlmClient([
      '```ts\n// path: src/ticks.ts\nexport type Ticks = number;\n```',
    ]);

    const loop = new ConstraintLoop(checker, {
      skipTypeCheck: true,
      skipTests: true,
    });

    const result = await loop.run({
      resource: registry.getById("vo.Ticks")!,
      registry,
      llmClient: llm,
      prompt: "generate Ticks",
      systemPrompt: "you are a code generator",
      maxRetries: 3,
    });

    expect(result.success).toBe(true);
    expect(result.files!.size).toBe(1);
    expect(result.files!.get("src/ticks.ts")).toContain("Ticks");
  });

  test("retries when invariant check fails, succeeds on second attempt", async () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "agg.Song", kind: "aggregate", layer: "domain" }));

    const { DomainNoInfraImports } = require("../../src/invariants/rules/domain-no-infra-imports");
    const checker = new InvariantChecker([new DomainNoInfraImports()]);

    const llm = mockLlmClient([
      '```ts\n// path: src/song.ts\nimport { db } from "../infrastructure/db";\nexport interface Song {}\n```',
      '```ts\n// path: src/song.ts\nexport interface Song { id: string; }\n```',
    ]);

    const loop = new ConstraintLoop(checker, {
      skipTypeCheck: true,
      skipTests: true,
    });

    const result = await loop.run({
      resource: registry.getById("agg.Song")!,
      registry,
      llmClient: llm,
      prompt: "generate Song",
      systemPrompt: "you are a code generator",
      maxRetries: 3,
    });

    expect(result.success).toBe(true);
    expect(result.retries).toBe(1);
  });

  test("fails after exhausting all retries", async () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "agg.Song", kind: "aggregate", layer: "domain" }));

    const { DomainNoInfraImports } = require("../../src/invariants/rules/domain-no-infra-imports");
    const checker = new InvariantChecker([new DomainNoInfraImports()]);

    const badResponse = '```ts\n// path: src/song.ts\nimport { db } from "../infrastructure/db";\nexport interface Song {}\n```';
    const llm = mockLlmClient([badResponse, badResponse, badResponse, badResponse]);

    const loop = new ConstraintLoop(checker, {
      skipTypeCheck: true,
      skipTests: true,
    });

    const result = await loop.run({
      resource: registry.getById("agg.Song")!,
      registry,
      llmClient: llm,
      prompt: "generate Song",
      systemPrompt: "you are a code generator",
      maxRetries: 3,
    });

    expect(result.success).toBe(false);
    expect(result.lastError).toContain("infrastructure");
  });
});
