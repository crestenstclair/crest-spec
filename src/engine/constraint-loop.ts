import type { ResourceDescriptor } from "../types.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { IInvariantChecker } from "../invariants/invariant-checker.js";
import type { ILlmClient } from "./llm-client.js";
import { ResponseParser, type IResponseParser } from "./response-parser.js";

export interface ConstraintLoopOptions {
  skipTypeCheck?: boolean;
  skipTests?: boolean;
  projectRoot?: string;
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

      if (!this.options.skipTypeCheck) {
        console.log(`      Type checking...`);
        const typeError = await this.typeCheck(files);
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

      if (!this.options.skipTests) {
        console.log(`      Running tests...`);
        const testError = await this.runTests(files);
        if (testError) {
          lastError = testError;
          console.log(`      Tests failed: ${testError.slice(0, 100)}`);
          currentPrompt = this.buildFixPrompt(input.prompt, testError, files);
          continue;
        }
        console.log(`      Tests passed`);
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
    const sections = [originalPrompt];

    if (existingFiles && existingFiles.size > 0) {
      sections.push("\n## Your previous output\n");
      for (const [path, content] of existingFiles) {
        sections.push("```csharp");
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

  private async typeCheck(_files: Map<string, string>): Promise<string | null> {
    if (!this.options.projectRoot) return null;

    try {
      const proc = Bun.spawn(["tsc", "--noEmit"], {
        cwd: this.options.projectRoot,
        stdout: "pipe",
        stderr: "pipe",
      });
      const exitCode = await proc.exited;
      if (exitCode !== 0) {
        const stderr = await new Response(proc.stderr).text();
        return `TypeScript compilation failed:\n${stderr}`;
      }
      return null;
    } catch (e) {
      return `Type check error: ${e}`;
    }
  }

  private async runTests(_files: Map<string, string>): Promise<string | null> {
    if (!this.options.projectRoot) return null;

    try {
      const proc = Bun.spawn(["bun", "test"], {
        cwd: this.options.projectRoot,
        stdout: "pipe",
        stderr: "pipe",
      });
      const exitCode = await proc.exited;
      if (exitCode !== 0) {
        const stderr = await new Response(proc.stderr).text();
        return `Tests failed:\n${stderr}`;
      }
      return null;
    } catch (e) {
      return `Test execution error: ${e}`;
    }
  }
}
