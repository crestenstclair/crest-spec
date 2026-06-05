import { describe, test, expect, beforeEach, afterEach } from "bun:test";
import { mkdtemp, rm, writeFile, mkdir } from "fs/promises";
import { join } from "path";
import { tmpdir } from "os";

async function runAgent(
  projectDir: string,
  args: string[],
): Promise<{ code: number; stdout: string; stderr: string }> {
  const cliPath = join(import.meta.dir, "../../src/cli/main.ts");
  const proc = Bun.spawn(["bun", cliPath, "agent", ...args], {
    cwd: projectDir,
    stdout: "pipe",
    stderr: "pipe",
    env: { ...process.env },
  });
  const code = await proc.exited;
  const stdout = await new Response(proc.stdout).text();
  const stderr = await new Response(proc.stderr).text();
  return { code, stdout, stderr };
}

describe("agent CLI commands", () => {
  let tempDir: string;

  beforeEach(async () => {
    tempDir = await mkdtemp(join(tmpdir(), "crest-agent-"));

    const specContent = `
import { project } from "${join(import.meta.dir, "../../src/index.ts")}";

const app = project("test-project", {
  layers: ["domain"],
  rules: [],
  meta: { language: "typescript" },
});

const ctx = app.context("Test", { purpose: "test context" });
ctx.valueObject("Alpha", { from: "number", description: "test vo" });
`;
    await writeFile(join(tempDir, "crest-spec.ts"), specContent);
  });

  afterEach(async () => {
    await rm(tempDir, { recursive: true, force: true });
  });

  test("begin outputs JSON with plan and waves", async () => {
    const result = await runAgent(tempDir, ["begin"]);
    expect(result.code).toBe(0);

    const output = JSON.parse(result.stdout);
    expect(output.applyId).toBeGreaterThan(0);
    expect(output.plan).toBeInstanceOf(Array);
    expect(output.plan.length).toBeGreaterThan(0);
    expect(output.waves).toBeInstanceOf(Array);
  });

  test("next returns resources after begin", async () => {
    await runAgent(tempDir, ["begin"]);
    const result = await runAgent(tempDir, ["next"]);
    expect(result.code).toBe(0);

    const output = JSON.parse(result.stdout);
    expect(output.done).toBe(false);
    expect(output.resources.length).toBeGreaterThan(0);
  });

  test("next without begin returns error", async () => {
    const result = await runAgent(tempDir, ["next"]);
    expect(result.code).toBe(1);
  });

  test("context returns prompts for a resource", async () => {
    await runAgent(tempDir, ["begin"]);
    const result = await runAgent(tempDir, ["context", "valueObject.Test.Alpha"]);
    expect(result.code).toBe(0);

    const output = JSON.parse(result.stdout);
    expect(output.resourceId).toBe("valueObject.Test.Alpha");
    expect(output.systemPrompt).toContain("code generator");
    expect(output.prompt).toContain("Alpha");
  });

  test("note saves and returns noteId", async () => {
    await runAgent(tempDir, ["begin"]);
    const result = await runAgent(tempDir, ["note", "valueObject.Test.Alpha", "Used newtype pattern"]);
    expect(result.code).toBe(0);

    const output = JSON.parse(result.stdout);
    expect(output.saved).toBe(true);
    expect(output.noteId).toBeGreaterThan(0);
  });

  test("finish after begin returns summary", async () => {
    await runAgent(tempDir, ["begin"]);
    const result = await runAgent(tempDir, ["finish"]);
    expect(result.code).toBe(0);

    const output = JSON.parse(result.stdout);
    expect(output.status).toBeDefined();
    expect(output.applyId).toBeGreaterThan(0);
  });

  test("full lifecycle: begin -> next -> note -> commit -> finish", async () => {
    // Begin
    const beginResult = await runAgent(tempDir, ["begin"]);
    expect(beginResult.code).toBe(0);

    // Next
    const nextResult = await runAgent(tempDir, ["next"]);
    const nextOutput = JSON.parse(nextResult.stdout);
    const resourceId = nextOutput.resources[0].resourceId;

    // Write a file to disk (simulating sub-agent)
    const resourceDir = join(tempDir, "src", "Test", "Alpha");
    await mkdir(resourceDir, { recursive: true });
    await writeFile(join(resourceDir, "alpha.ts"), "export type Alpha = number;");

    // Note
    const noteResult = await runAgent(tempDir, ["note", resourceId, "Simple type alias"]);
    expect(noteResult.code).toBe(0);

    // Commit
    const commitResult = await runAgent(tempDir, ["commit", resourceId]);
    expect(commitResult.code).toBe(0);
    const commitOutput = JSON.parse(commitResult.stdout);
    expect(commitOutput.committed).toBe(true);
    expect(commitOutput.filesRecorded.length).toBeGreaterThan(0);

    // Next should be done
    const next2 = await runAgent(tempDir, ["next"]);
    const next2Output = JSON.parse(next2.stdout);
    expect(next2Output.done).toBe(true);

    // Finish
    const finishResult = await runAgent(tempDir, ["finish"]);
    expect(finishResult.code).toBe(0);
    const finishOutput = JSON.parse(finishResult.stdout);
    expect(finishOutput.status).toBe("ok");
    expect(finishOutput.created).toBe(1);
  });
});
