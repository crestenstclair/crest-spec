import type { ResourceDescriptor } from "../types.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { IInvariantChecker } from "../invariants/invariant-checker.js";
import type { ILlmClient } from "./llm-client.js";
import { ResponseParser, type IResponseParser } from "./response-parser.js";

export interface ConstraintLoopOptions {
  skipLlmVerify?: boolean;
  projectRoot?: string;
  language?: string;
  typeCheckCommand?: string[];
  testCommand?: string[];
}

export interface ConstraintLoopInput {
  resource: ResourceDescriptor;
  registry: IResourceRegistry;
  llmClient: ILlmClient;
  prompt: string;
  systemPrompt: string;
  maxRetries: number;
}

export interface ConstraintLoopResult {
  success: boolean;
  files: Map<string, string> | null;
  retries: number;
  lastError: string | null;
}

export interface IConstraintLoop {
  run(input: ConstraintLoopInput): Promise<ConstraintLoopResult>;
}

export class ConstraintLoop implements IConstraintLoop {
  private readonly parser: IResponseParser;

  constructor(
    private readonly invariantChecker: IInvariantChecker,
    private readonly options: ConstraintLoopOptions = {},
    parser?: IResponseParser,
  ) {
    this.parser = parser ?? new ResponseParser();
  }

  async run(input: ConstraintLoopInput): Promise<ConstraintLoopResult> {
    let lastError: string | null = null;
    let lastFiles: Map<string, string> | null = null;
    let currentPrompt = input.prompt;

    for (let attempt = 0; attempt <= input.maxRetries; attempt++) {
      if (attempt > 0) {
        console.log(`      Retry ${attempt}/${input.maxRetries}: ${lastError?.slice(0, 80)}`);
      }
      console.log(`      Generating ${input.resource.id} (${input.resource.kind}) via ${input.llmClient.modelId}...`);
      const response = await input.llmClient.generate(currentPrompt, input.systemPrompt);
      console.log(`      LLM responded (${response.length} chars)`);
      const files = this.parser.parse(response);

      if (files.size === 0) {
        lastError = "LLM returned no parseable code blocks";
        console.log(`      Parse failed: ${lastError}`);
        currentPrompt = this.buildFixPrompt(input.prompt, lastError, lastFiles);
        continue;
      }
      lastFiles = files;
      console.log(`      Parsed ${files.size} file(s): ${[...files.keys()].join(", ")}`);

      if (this.options.typeCheckCommand) {
        console.log(`      Type checking...`);
        const typeError = await this.runCommand(this.options.typeCheckCommand, "Type check");
        if (typeError) {
          lastError = typeError;
          console.log(`      Type check failed: ${typeError.slice(0, 100)}`);
          currentPrompt = this.buildFixPrompt(input.prompt, typeError, files);
          continue;
        }
        console.log(`      Type check passed`);
      }

      const invariantResults = this.invariantChecker.checkGenerated(
        input.resource.id,
        files,
        input.registry,
      );
      const violations = invariantResults.filter((r) => r.status === "violated");
      if (violations.length > 0) {
        lastError = violations.map((v) => `${v.invariant}: ${v.detail}`).join("\n");
        console.log(`      Invariant violations: ${violations.length}`);
        currentPrompt = this.buildFixPrompt(input.prompt, lastError, files);
        continue;
      }

      if (this.options.testCommand) {
        console.log(`      Running tests...`);
        const testError = await this.runCommand(this.options.testCommand, "Tests");
        if (testError) {
          lastError = testError;
          console.log(`      Tests failed: ${testError.slice(0, 100)}`);
          currentPrompt = this.buildFixPrompt(input.prompt, testError, files);
          continue;
        }
        console.log(`      Tests passed`);
      }

      if (!this.options.skipLlmVerify) {
        console.log(`      LLM verify...`);
        const verifyError = await this.llmVerify(input, files);
        if (verifyError) {
          lastError = verifyError;
          console.log(`      LLM verify failed: ${verifyError.slice(0, 100)}`);
          currentPrompt = this.buildFixPrompt(input.prompt, verifyError, files);
          continue;
        }
        console.log(`      LLM verify passed`);
      }

      return { success: true, files, retries: attempt, lastError: null };
    }

    console.log(`      Exhausted retries`);
    return { success: false, files: null, retries: input.maxRetries, lastError };
  }

  private buildFixPrompt(
    originalPrompt: string,
    error: string,
    existingFiles: Map<string, string> | null,
  ): string {
    const lang = this.options.language ?? "text";
    const sections = [originalPrompt];

    if (existingFiles && existingFiles.size > 0) {
      sections.push("\n## Your previous output\n");
      for (const [path, content] of existingFiles) {
        sections.push(`\`\`\`${lang}`);
        sections.push(`// path: ${path}`);
        sections.push(content.trim());
        sections.push("```\n");
      }
    }

    sections.push("## Error to fix\n");
    sections.push("The code above has the following error. Output the COMPLETE corrected files (all of them, not just the changed one):\n");
    sections.push(error);

    return sections.join("\n");
  }

  private async runCommand(command: string[], label: string): Promise<string | null> {
    if (!this.options.projectRoot) return null;

    try {
      const proc = Bun.spawn(command, {
        cwd: this.options.projectRoot,
        stdout: "pipe",
        stderr: "pipe",
      });
      const exitCode = await proc.exited;
      if (exitCode !== 0) {
        const stderr = await new Response(proc.stderr).text();
        return `${label} failed:\n${stderr}`;
      }
      return null;
    } catch (e) {
      return `${label} error: ${e}`;
    }
  }

  private async llmVerify(
    input: ConstraintLoopInput,
    files: Map<string, string>,
  ): Promise<string | null> {
    const lang = this.options.language ?? "code";
    const verifyPrompt = this.buildVerifyPrompt(input, files);
    const verifySystem = [
      `You are a code reviewer verifying generated ${lang} code against strict requirements.`,
      "Review the code and respond with EXACTLY one of:",
      "",
      "PASS",
      "",
      "Or if there are issues:",
      "",
      "FAIL",
      "- issue 1",
      "- issue 2",
      "",
      "Be strict. Check for these specific violations:",
      "1. DEPENDENCY INJECTION: Classes must NOT instantiate their own dependencies (no `new Dependency()` inside a class body). All collaborators via constructor.",
      "2. INTERFACES: Any class that another class depends on MUST have a corresponding interface. Check that interfaces exist for all injected dependencies.",
      "3. UNIT TESTS: There MUST be at least one test file that tests the resource through its public API using injected mocks/stubs.",
      "4. SINGLE RESPONSIBILITY: Each class should have one reason to change.",
      "5. FOLDER STRUCTURE: Files must be in src/{ContextName}/ (flat, no Domain/Application/Infrastructure sub-folders). Tests in tests/{ContextName}/.",
      "6. VALUE OBJECTS: Must be records or readonly structs, not mutable classes.",
      "",
      "Only report REAL violations. Do not nitpick naming or style beyond these rules.",
      "If the code passes all checks, respond with just: PASS",
    ].join("\n");

    try {
      const response = await input.llmClient.generate(verifyPrompt, verifySystem);
      const trimmed = response.trim();

      if (trimmed === "PASS" || trimmed.startsWith("PASS")) {
        return null;
      }

      const lines = trimmed.split("\n").filter((l) => l.trim());
      const issues = lines.filter((l) => l.startsWith("-") || l.startsWith("FAIL"));
      if (issues.length === 0) {
        return null;
      }

      return issues.join("\n");
    } catch (e) {
      console.log(`      LLM verify error (skipping): ${e instanceof Error ? e.message.slice(0, 80) : e}`);
      return null;
    }
  }

  private buildVerifyPrompt(
    input: ConstraintLoopInput,
    files: Map<string, string>,
  ): string {
    const lang = this.options.language ?? "text";
    const sections: string[] = [];
    sections.push(`## Resource being verified: ${input.resource.id} (${input.resource.kind})`);
    sections.push(`Context: ${input.resource.context ?? "project-level"}`);
    sections.push("");
    sections.push("## Generated code to review:\n");

    for (const [path, content] of files) {
      sections.push(`### ${path}`);
      sections.push(`\`\`\`${lang}`);
      sections.push(content.trim());
      sections.push("```\n");
    }

    sections.push("## Original requirements:\n");
    sections.push("```json");
    sections.push(JSON.stringify(input.resource.declaration, null, 2));
    sections.push("```");

    if (input.resource.invariants && input.resource.invariants.length > 0) {
      sections.push("\n## Invariants:");
      for (const inv of input.resource.invariants) {
        sections.push(`- ${inv}`);
      }
    }

    return sections.join("\n");
  }
}
