import type { IInvariantChecker } from "../invariants/invariant-checker.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";
import type { ValidationDeclaration, ValidationAssertion } from "../types.js";

export interface ResourceValidatorOptions {
  projectRoot?: string;
  typeCheckCommand?: string[];
  testCommand?: string[];
  skipTypeCheck?: boolean;
  skipTests?: boolean;
}

export interface ValidationResult {
  passed: boolean;
  errors: string[];
}

export interface IResourceValidator {
  validate(
    resourceId: string,
    files: Map<string, string>,
    registry: IResourceRegistry,
  ): ValidationResult;
  validateAsync(
    resourceId: string,
    files: Map<string, string>,
    registry: IResourceRegistry,
  ): Promise<ValidationResult>;
}

export class ResourceValidator implements IResourceValidator {
  constructor(
    private readonly invariantChecker: IInvariantChecker,
    private readonly options: ResourceValidatorOptions,
  ) {}

  validate(
    resourceId: string,
    files: Map<string, string>,
    registry: IResourceRegistry,
  ): ValidationResult {
    const resource = registry.getById(resourceId);
    if (!resource) {
      return { passed: false, errors: [`Resource ${resourceId} not found in registry`] };
    }

    const errors: string[] = [];

    const invariantResults = this.invariantChecker.checkGenerated(resourceId, files, registry);
    const violations = invariantResults.filter((r) => r.status === "violated");
    for (const v of violations) {
      errors.push(`Invariant violated: ${v.invariant}${v.detail ? ` - ${v.detail}` : ""}`);
    }

    return { passed: errors.length === 0, errors };
  }

  async validateAsync(
    resourceId: string,
    files: Map<string, string>,
    registry: IResourceRegistry,
  ): Promise<ValidationResult> {
    const syncResult = this.validate(resourceId, files, registry);
    if (!syncResult.passed) return syncResult;

    const errors: string[] = [];

    if (this.options.typeCheckCommand && !this.options.skipTypeCheck) {
      const typeError = await this.runCommand(this.options.typeCheckCommand, "Type check");
      if (typeError) errors.push(typeError);
    }

    if (this.options.testCommand && !this.options.skipTests) {
      const testError = await this.runCommand(this.options.testCommand, "Tests");
      if (testError) errors.push(testError);
    }

    const resource = registry.getById(resourceId);
    if (resource?.validations) {
      const declaredErrors = await this.runDeclaredValidations(resource.validations);
      errors.push(...declaredErrors);
    }

    return { passed: errors.length === 0, errors };
  }

  async runDeclaredValidations(validations: ValidationDeclaration[]): Promise<string[]> {
    const errors: string[] = [];

    for (const v of validations) {
      const label = v.description ?? `${v.kind}: ${v.command.join(" ")}`;

      if (v.kind === "compiles" || v.kind === "test" || v.kind === "custom") {
        const error = await this.runCommand(v.command, label);
        if (error) errors.push(error);
        continue;
      }

      if (v.kind === "integration") {
        const result = await this.runIntegrationValidation(v, label);
        errors.push(...result);
      }
    }

    return errors;
  }

  private async runIntegrationValidation(
    v: ValidationDeclaration,
    label: string,
  ): Promise<string[]> {
    if (!this.options.projectRoot) return [];

    const errors: string[] = [];

    try {
      const proc = Bun.spawn(v.command, {
        cwd: this.options.projectRoot,
        stdout: "pipe",
        stderr: "pipe",
      });
      const exitCode = await proc.exited;
      const stdout = await new Response(proc.stdout).text();
      const stderr = await new Response(proc.stderr).text();

      if (!v.assertions || v.assertions.length === 0) {
        if (exitCode !== 0) {
          errors.push(`${label} failed (exit ${exitCode}):\n${stderr}`);
        }
        return errors;
      }

      for (const assertion of v.assertions) {
        const err = this.checkAssertion(assertion, exitCode, stdout, stderr);
        if (err) errors.push(`${label}: ${err}`);
      }
    } catch (e) {
      errors.push(`${label} error: ${e}`);
    }

    return errors;
  }

  private checkAssertion(
    assertion: ValidationAssertion,
    exitCode: number,
    stdout: string,
    stderr: string,
  ): string | null {
    switch (assertion.kind) {
      case "exit_code":
        if (exitCode !== (assertion.expected ?? 0)) {
          return `expected exit code ${assertion.expected ?? 0}, got ${exitCode}`;
        }
        return null;

      case "stdout_contains":
        if (assertion.pattern && !stdout.includes(assertion.pattern)) {
          return `stdout does not contain "${assertion.pattern}"`;
        }
        return null;

      case "stderr_empty":
        if (stderr.trim().length > 0) {
          return `expected empty stderr, got: ${stderr.slice(0, 200)}`;
        }
        return null;

      case "file_exists":
        if (assertion.path) {
          const fullPath = this.options.projectRoot
            ? `${this.options.projectRoot}/${assertion.path}`
            : assertion.path;
          if (!Bun.file(fullPath).size) {
            return `expected file ${assertion.path} to exist`;
          }
        }
        return null;

      case "file_not_empty":
        if (assertion.path) {
          const fullPath = this.options.projectRoot
            ? `${this.options.projectRoot}/${assertion.path}`
            : assertion.path;
          const size = Bun.file(fullPath).size;
          if (!size || size === 0) {
            return `expected file ${assertion.path} to be non-empty`;
          }
        }
        return null;

      case "file_matches":
        if (assertion.path && assertion.regex) {
          try {
            const fullPath = this.options.projectRoot
              ? `${this.options.projectRoot}/${assertion.path}`
              : assertion.path;
            const content = require("fs").readFileSync(fullPath, "utf-8");
            if (!new RegExp(assertion.regex).test(content)) {
              return `file ${assertion.path} does not match regex: ${assertion.regex}`;
            }
          } catch (e) {
            return `file ${assertion.path} could not be read: ${e}`;
          }
        }
        return null;

      default:
        return `unknown assertion kind: ${(assertion as ValidationAssertion).kind}`;
    }
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
}
