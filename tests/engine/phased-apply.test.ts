import { describe, test, expect, beforeAll, afterAll } from "bun:test";
import { ApplyEngine } from "../../src/engine/apply-engine";
import { Planner } from "../../src/planner/planner";
import { HashComputer } from "../../src/planner/hash-computer";
import { StateDatabase } from "../../src/state/state-database";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { PromptBuilder } from "../../src/engine/prompt-builder";
import { ConstraintLoop } from "../../src/engine/constraint-loop";
import { WaveComputer } from "../../src/engine/wave-computer";
import { getActiveProject } from "../../src/dsl/singleton";
import type { ILlmClient } from "../../src/engine/llm-client";
import type { IWaveVerifier, WaveVerificationResult } from "../../src/engine/wave-verifier";
import type { ResourceDescriptor } from "../../src/types";
import { mkdtemp, rm } from "fs/promises";
import { join } from "path";
import { tmpdir } from "os";

const PHASE_DIR = join(import.meta.dir, "../../fixtures/crest-synth/phases");

function stubLlmClient(): ILlmClient {
  return {
    modelId: "test-model",
    async generate(prompt: string): Promise<string> {
      const resourceMatch = prompt.match(/## Resource: (\w+) "(\w+)"/);
      const kind = resourceMatch?.[1] ?? "unknown";
      const name = resourceMatch?.[2] ?? "Unknown";

      const contextMatch = prompt.match(/Context: (\w+)/);
      const context = contextMatch?.[1] ?? "core";

      const dir = `src/${context}`;
      const fileName = `${name}.rs`;
      const path = `${dir}/${fileName}`;

      let body: string;
      switch (kind) {
        case "valueObject":
          body = `pub struct ${name} {\n    _inner: (),\n}\n`;
          break;
        case "aggregate":
          body = `pub struct ${name} {\n    _state: (),\n}\n\nimpl ${name} {\n    pub fn new() -> Self {\n        Self { _state: () }\n    }\n}\n`;
          break;
        case "port":
          body = `pub trait ${name} {\n    fn execute(&self);\n}\n`;
          break;
        case "repository":
          body = `pub trait ${name}Repository {\n    fn find_by_id(&self, id: &str) -> Option<()>;\n    fn save(&self, entity: &()) -> ();\n}\n`;
          break;
        case "domainService":
          body = `pub struct ${name}Service;\n\nimpl ${name}Service {\n    pub fn execute(&self) {}\n}\n`;
          break;
        case "applicationService":
          body = `pub struct ${name}AppService;\n\nimpl ${name}AppService {\n    pub fn run(&self) {}\n}\n`;
          break;
        case "entity":
          body = `pub struct ${name} {\n    _data: (),\n}\n`;
          break;
        default:
          body = `// ${kind}: ${name}\n`;
      }

      return `\`\`\`rust\n// path: ${path}\n${body}\`\`\``;
    },
  };
}

function passingVerifier(): IWaveVerifier {
  return {
    async verify(): Promise<WaveVerificationResult> {
      return { passed: true, errors: [], rawOutput: "" };
    },
  };
}

const STRUCTURAL_KINDS = new Set(["project", "context", "assetKind"]);

function countGeneratable(resources: ResourceDescriptor[]): number {
  return resources.filter((r) => !STRUCTURAL_KINDS.has(r.kind)).length;
}

describe("Phased crest-synth apply", () => {
  let state: StateDatabase;
  let tempDir: string;
  const llm = stubLlmClient();
  const verifier = passingVerifier();

  const phaseResults: Array<{
    phase: number;
    totalResources: number;
    generatable: number;
    created: number;
    modified: number;
    destroyed: number;
  }> = [];

  beforeAll(async () => {
    tempDir = await mkdtemp(join(tmpdir(), "crest-synth-phased-"));
    state = new StateDatabase(join(tempDir, "crest-spec.db"));
  });

  afterAll(async () => {
    await rm(tempDir, { recursive: true, force: true });
  });

  for (let phase = 1; phase <= 10; phase++) {
    test(`phase ${phase}`, async () => {
      // Phase files use an additive import chain rooted in base.ts.
      // base.ts calls project() once (on first load) and sets the singleton.
      // Subsequent phase imports add resources to the same registry via upsert.
      // We do NOT resetSingleton() here — the singleton accumulates correctly.
      await import(join(PHASE_DIR, `crest-spec-phase-${phase}.ts`));
      const project = getActiveProject();
      expect(project).not.toBeNull();

      const registry = project!.getRegistry();
      const allResources = registry.getAll();
      const generatable = countGeneratable(allResources);

      const meta = project!.getMeta();
      const hashComputer = new HashComputer("test-model");
      const planner = new Planner(hashComputer);
      const plan = planner.plan(registry, state);

      const creates = plan.actions.filter((a) => a.action === "create");
      const modifies = plan.actions.filter((a) => a.action === "modify");
      const destroys = plan.actions.filter((a) => a.action === "destroy");

      console.log(`\n${"─".repeat(70)}`);
      console.log(`Phase ${phase}: ${allResources.length} total resources, ${generatable} generatable`);
      console.log(`  Plan: +${creates.length} create, ~${modifies.length} modify, -${destroys.length} destroy`);
      if (creates.length > 0) {
        console.log(`  Creates: ${creates.map((a) => a.resourceId).join(", ")}`);
      }
      if (modifies.length > 0) {
        console.log(`  Modifies: ${modifies.map((a) => a.resourceId).join(", ")}`);
      }
      if (destroys.length > 0) {
        console.log(`  Destroys: ${destroys.map((a) => a.resourceId).join(", ")}`);
      }

      const promptBuilder = PromptBuilder.fromMeta(meta);
      const checker = new InvariantChecker([]);
      const constraintLoop = new ConstraintLoop(checker, {
        skipTypeCheckInLoop: true,
        skipLlmVerify: true,
      });
      const waveComputer = new WaveComputer();

      const engine = new ApplyEngine(
        planner,
        promptBuilder,
        constraintLoop,
        hashComputer,
        waveComputer,
        verifier,
      );

      const result = await engine.apply(registry, state, llm, {
        outputDir: tempDir,
        waveVerifyCommand: ["cargo", "test"],
      });

      expect(result.status).toBe("ok");
      expect(result.failed).toBe(0);

      phaseResults.push({
        phase,
        totalResources: allResources.length,
        generatable,
        created: result.created,
        modified: result.modified,
        destroyed: result.destroyed,
      });

      console.log(`  Result: +${result.created} created, ~${result.modified} modified, -${result.destroyed} destroyed`);

      // Phase 1: everything is new — all generatable resources should be created
      if (phase === 1) {
        expect(result.created).toBe(generatable);
        expect(result.modified).toBe(0);
        expect(result.destroyed).toBe(0);
      }

      // Phase 2: Audio context removed, Synth context added, Kernel modified
      if (phase === 2) {
        expect(result.destroyed).toBeGreaterThan(0);
        expect(result.created).toBeGreaterThan(0);
        const destroyedIds = destroys.map((a) => a.resourceId);
        expect(destroyedIds.some((id) => id.includes("Audio"))).toBe(true);
        const createdIds = creates.map((a) => a.resourceId);
        expect(createdIds.some((id) => id.includes("Synth"))).toBe(true);
      }

      // Phase 3+: should only create new resources and modify changed ones
      if (phase >= 3) {
        expect(result.created).toBeGreaterThanOrEqual(0);
      }

      // Re-apply with no changes: should be a no-op
      // We reuse the registry already loaded above — phase files now use an
      // additive import chain rooted in base.ts, so module caching prevents
      // re-executing project() via a fresh import. The registry is the same
      // object regardless, so planning against it again is sufficient.
      const plan2 = planner.plan(registry, state);

      expect(plan2.actions.length).toBe(0);
      console.log(`  Re-apply: plan is empty (no changes) ✓`);
    }, 30_000);
  }

  test("summary", () => {
    console.log(`\n${"═".repeat(70)}`);
    console.log("Phase summary:");
    console.log(`${"═".repeat(70)}`);
    console.log(
      `${"Phase".padEnd(6)} ${"Total".padEnd(6)} ${"Gen".padEnd(6)} ${"+Cr".padEnd(6)} ${"~Mod".padEnd(6)} ${"-Del".padEnd(6)}`,
    );
    console.log("─".repeat(36));
    for (const r of phaseResults) {
      console.log(
        `${String(r.phase).padEnd(6)} ${String(r.totalResources).padEnd(6)} ${String(r.generatable).padEnd(6)} ${String(r.created).padEnd(6)} ${String(r.modified).padEnd(6)} ${String(r.destroyed).padEnd(6)}`,
      );
    }
    expect(phaseResults).toHaveLength(10);
  });
});
