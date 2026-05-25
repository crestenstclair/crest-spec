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
    let currentPrompt = input.prompt;

    for (let attempt = 0; attempt <= input.maxRetries; attempt++) {
      const response = await input.llmClient.generate(currentPrompt, input.systemPrompt);
      const files = this.parser.parse(response);

      if (files.size === 0) {
        lastError = "LLM returned no parseable code blocks";
        currentPrompt = this.appendFeedback(input.prompt, lastError);
        continue;
      }

      if (!this.options.skipTypeCheck) {
        const typeError = await this.typeCheck(files);
        if (typeError) {
          lastError = typeError;
          currentPrompt = this.appendFeedback(input.prompt, typeError);
          continue;
        }
      }

      const invariantResults = this.invariantChecker.checkGenerated(
        input.resource.id,
        files,
        input.registry,
      );
      const violations = invariantResults.filter((r) => r.status === "violated");
      if (violations.length > 0) {
        lastError = violations.map((v) => `${v.invariant}: ${v.detail}`).join("\n");
        currentPrompt = this.appendFeedback(input.prompt, lastError);
        continue;
      }

      if (!this.options.skipTests) {
        const testError = await this.runTests(files);
        if (testError) {
          lastError = testError;
          currentPrompt = this.appendFeedback(input.prompt, testError);
          continue;
        }
      }

      return { success: true, files, retries: attempt, lastError: null };
    }

    return { success: false, files: null, retries: input.maxRetries, lastError };
  }

  private appendFeedback(originalPrompt: string, error: string): string {
    return `${originalPrompt}\n\n## Previous attempt failed\n\nYour previous output had the following error. Fix it:\n\n${error}`;
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
