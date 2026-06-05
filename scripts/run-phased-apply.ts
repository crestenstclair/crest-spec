#!/usr/bin/env bun
/**
 * Live phased apply: iterates through crest-synth phases 1-10,
 * loading each spec and running the full engine pipeline with a real LLM.
 * State carries over between phases so the planner detects diffs.
 *
 * Usage: bun scripts/run-phased-apply.ts [--model <model-id>] [--start <phase>] [--output <dir>]
 */

import { join } from "path";
import { mkdtemp, mkdir } from "fs/promises";
import { tmpdir } from "os";
import { Planner } from "../src/planner/planner";
import { HashComputer } from "../src/planner/hash-computer";
import { StateDatabase } from "../src/state/state-database";
import { InvariantChecker } from "../src/invariants/invariant-checker";
import { PromptBuilder } from "../src/engine/prompt-builder";
import { ConstraintLoop } from "../src/engine/constraint-loop";
import { ApplyEngine } from "../src/engine/apply-engine";
import { ClaudeCliClient } from "../src/engine/llm-client";
import { WaveComputer } from "../src/engine/wave-computer";
import { WaveVerifier } from "../src/engine/wave-verifier";
import { getActiveProject, resetSingleton } from "../src/dsl/singleton";

const PHASE_DIR = join(import.meta.dir, "../fixtures/crest-synth/phases");
const STRUCTURAL_KINDS = new Set(["project", "context", "assetKind"]);

function parseArgs() {
  const args = process.argv.slice(2);
  let model = "claude-sonnet-4-6";
  let startPhase = 1;
  let endPhase = 10;
  let outputDir = "";

  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--model" && args[i + 1]) model = args[++i];
    else if (args[i] === "--start" && args[i + 1]) startPhase = parseInt(args[++i]);
    else if (args[i] === "--end" && args[i + 1]) endPhase = parseInt(args[++i]);
    else if (args[i] === "--output" && args[i + 1]) outputDir = args[++i];
  }

  return { model, startPhase, endPhase, outputDir };
}

async function main() {
  const { model, startPhase, endPhase, outputDir: userOutputDir } = parseArgs();

  const outputDir = userOutputDir || await mkdtemp(join(tmpdir(), "crest-synth-live-"));
  await mkdir(outputDir, { recursive: true });
  const dbPath = join(outputDir, "crest-spec.db");

  console.log("╔══════════════════════════════════════════════════════════════╗");
  console.log("║  crest-synth: live phased apply (phases 1-10)              ║");
  console.log("╚══════════════════════════════════════════════════════════════╝");
  console.log(`  Model:   ${model}`);
  console.log(`  Output:  ${outputDir}`);
  console.log(`  DB:      ${dbPath}`);
  console.log(`  Start:   phase ${startPhase}`);
  console.log(`  End:     phase ${endPhase}`);
  console.log();

  const state = new StateDatabase(dbPath);
  const llm = new ClaudeCliClient(model);

  const phaseResults: Array<{
    phase: number;
    totalResources: number;
    generatable: number;
    planned: number;
    created: number;
    modified: number;
    destroyed: number;
    failed: number;
    elapsed: number;
  }> = [];

  const globalStart = performance.now();

  for (let phase = startPhase; phase <= endPhase; phase++) {
    const phaseStart = performance.now();

    console.log(`\n${"█".repeat(70)}`);
    console.log(`  PHASE ${phase}/10`);
    console.log(`${"█".repeat(70)}`);

    resetSingleton();
    const specPath = join(PHASE_DIR, `crest-spec-phase-${phase}.ts`);
    await import(`${specPath}?live=${phase}`);
    const project = getActiveProject();
    if (!project) {
      console.error(`  ERROR: No project loaded from phase ${phase}`);
      process.exit(1);
    }

    const registry = project.getRegistry();
    const allResources = registry.getAll();
    const generatable = allResources.filter((r) => !STRUCTURAL_KINDS.has(r.kind)).length;

    const meta = project.getMeta();
    const hashComputer = new HashComputer(model);
    const planner = new Planner(hashComputer);
    const plan = planner.plan(registry, state);

    const creates = plan.actions.filter((a) => a.action === "create");
    const modifies = plan.actions.filter((a) => a.action === "modify");
    const destroys = plan.actions.filter((a) => a.action === "destroy");

    console.log(`  Resources: ${allResources.length} total, ${generatable} generatable`);
    console.log(`  Plan: +${creates.length} create, ~${modifies.length} modify, -${destroys.length} destroy`);

    if (plan.actions.length === 0) {
      console.log(`  → Nothing to do, skipping.`);
      phaseResults.push({
        phase,
        totalResources: allResources.length,
        generatable,
        planned: 0,
        created: 0,
        modified: 0,
        destroyed: 0,
        failed: 0,
        elapsed: 0,
      });
      continue;
    }

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
    const waveVerifier = new WaveVerifier();

    const engine = new ApplyEngine(
      planner,
      promptBuilder,
      constraintLoop,
      hashComputer,
      waveComputer,
      waveVerifier,
    );

    const result = await engine.apply(registry, state, llm, {
      outputDir,
      waveVerifyCommand: ["cargo", "test"],
      concurrency: 3,
    });

    const elapsed = (performance.now() - phaseStart) / 1000;

    console.log(`\n  ── Phase ${phase} result ──`);
    console.log(`  Created:   ${result.created}`);
    console.log(`  Modified:  ${result.modified}`);
    console.log(`  Destroyed: ${result.destroyed}`);
    console.log(`  Failed:    ${result.failed}`);
    console.log(`  Time:      ${elapsed.toFixed(1)}s`);

    if (result.errors.length > 0) {
      console.log(`  Errors:`);
      for (const err of result.errors) {
        console.log(`    - ${err}`);
      }
    }

    phaseResults.push({
      phase,
      totalResources: allResources.length,
      generatable,
      planned: plan.actions.length,
      created: result.created,
      modified: result.modified,
      destroyed: result.destroyed,
      failed: result.failed,
      elapsed,
    });
  }

  const totalElapsed = (performance.now() - globalStart) / 1000;

  console.log(`\n${"═".repeat(70)}`);
  console.log("SUMMARY");
  console.log("═".repeat(70));
  console.log(
    `${"Phase".padEnd(7)} ${"Total".padEnd(7)} ${"Gen".padEnd(6)} ${"Plan".padEnd(6)} ${"+Cr".padEnd(6)} ${"~Mod".padEnd(6)} ${"-Del".padEnd(6)} ${"Fail".padEnd(6)} ${"Time".padEnd(8)}`,
  );
  console.log("─".repeat(58));
  for (const r of phaseResults) {
    console.log(
      `${String(r.phase).padEnd(7)} ${String(r.totalResources).padEnd(7)} ${String(r.generatable).padEnd(6)} ${String(r.planned).padEnd(6)} ${String(r.created).padEnd(6)} ${String(r.modified).padEnd(6)} ${String(r.destroyed).padEnd(6)} ${String(r.failed).padEnd(6)} ${r.elapsed.toFixed(1).padStart(6)}s`,
    );
  }
  console.log("─".repeat(58));

  const totals = phaseResults.reduce(
    (acc, r) => ({
      created: acc.created + r.created,
      modified: acc.modified + r.modified,
      destroyed: acc.destroyed + r.destroyed,
      failed: acc.failed + r.failed,
    }),
    { created: 0, modified: 0, destroyed: 0, failed: 0 },
  );

  console.log(
    `${"TOTAL".padEnd(7)} ${"".padEnd(7)} ${"".padEnd(6)} ${"".padEnd(6)} ${String(totals.created).padEnd(6)} ${String(totals.modified).padEnd(6)} ${String(totals.destroyed).padEnd(6)} ${String(totals.failed).padEnd(6)} ${totalElapsed.toFixed(1).padStart(6)}s`,
  );

  console.log(`\nOutput directory: ${outputDir}`);

  if (totals.failed > 0) {
    console.log(`\n⚠ ${totals.failed} resources failed to generate.`);
    process.exit(1);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
