import type { IInvariantChecker } from "../invariants/invariant-checker.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";

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

    return { passed: errors.length === 0, errors };
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
