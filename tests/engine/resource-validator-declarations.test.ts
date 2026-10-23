import { describe, test, expect, beforeEach } from "bun:test";
import { ResourceValidator } from "../../src/engine/resource-validator";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";
import type { ValidationDeclaration } from "../../src/types";
import { mkdtemp, writeFile, rm } from "fs/promises";
import { join } from "path";
import { tmpdir } from "os";

describe("ResourceValidator declared validations", () => {
  let registry: ResourceRegistry;
  let tmpDir: string;

  beforeEach(async () => {
    registry = new ResourceRegistry();
    tmpDir = await mkdtemp(join(tmpdir(), "rv-test-"));
  });

  test("compiles validation passes when command exits 0", async () => {
    const validations: ValidationDeclaration[] = [
      { kind: "compiles", command: ["true"] },
    ];
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject", validations }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });
    const result = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result.passed).toBe(true);
  });

  test("compiles validation fails when command exits nonzero", async () => {
    const validations: ValidationDeclaration[] = [
      { kind: "compiles", command: ["false"] },
    ];
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject", validations }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });
    const result = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result.passed).toBe(false);
    expect(result.errors.length).toBeGreaterThan(0);
  });

  test("test validation uses description in error message", async () => {
    const validations: ValidationDeclaration[] = [
      { kind: "test", command: ["false"], description: "unit tests pass" },
    ];
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject", validations }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });
    const result = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result.passed).toBe(false);
    expect(result.errors[0]).toContain("unit tests pass");
  });

  test("integration validation checks exit_code assertion", async () => {
    const validations: ValidationDeclaration[] = [
      {
        kind: "integration",
        command: ["sh", "-c", "exit 42"],
        assertions: [{ kind: "exit_code", expected: 0 }],
      },
    ];
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject", validations }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });
    const result = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result.passed).toBe(false);
    expect(result.errors[0]).toContain("expected exit code 0, got 42");
  });

  test("integration validation checks stdout_contains assertion", async () => {
    const validations: ValidationDeclaration[] = [
      {
        kind: "integration",
        command: ["echo", "hello world"],
        assertions: [{ kind: "stdout_contains", pattern: "hello world" }],
      },
    ];
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject", validations }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });
    const result = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result.passed).toBe(true);
  });

  test("integration validation checks file_exists assertion", async () => {
    const validations: ValidationDeclaration[] = [
      {
        kind: "integration",
        command: ["true"],
        assertions: [{ kind: "file_exists", path: "output.txt" }],
      },
    ];
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject", validations }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });

    const result1 = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result1.passed).toBe(false);
    expect(result1.errors[0]).toContain("output.txt");

    await writeFile(join(tmpDir, "output.txt"), "content");
    const result2 = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result2.passed).toBe(true);
  });

  test("integration validation checks file_not_empty assertion", async () => {
    await writeFile(join(tmpDir, "empty.txt"), "");
    const validations: ValidationDeclaration[] = [
      {
        kind: "integration",
        command: ["true"],
        assertions: [{ kind: "file_not_empty", path: "empty.txt" }],
      },
    ];
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject", validations }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });
    const result = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result.passed).toBe(false);
    expect(result.errors[0]).toContain("non-empty");
  });

  test("integration validation checks file_matches assertion", async () => {
    await writeFile(join(tmpDir, "data.txt"), "version=1.2.3\nname=test");
    const validations: ValidationDeclaration[] = [
      {
        kind: "integration",
        command: ["true"],
        assertions: [{ kind: "file_matches", path: "data.txt", regex: "version=\\d+\\.\\d+\\.\\d+" }],
      },
    ];
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject", validations }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });
    const result = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result.passed).toBe(true);
  });

  test("custom validation passes on exit 0", async () => {
    const validations: ValidationDeclaration[] = [
      { kind: "custom", command: ["true"], description: "custom check" },
    ];
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject", validations }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });
    const result = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result.passed).toBe(true);
  });

  test("multiple validations accumulate errors", async () => {
    const validations: ValidationDeclaration[] = [
      { kind: "compiles", command: ["false"], description: "build" },
      { kind: "test", command: ["false"], description: "tests" },
    ];
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject", validations }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });
    const result = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result.passed).toBe(false);
    expect(result.errors).toHaveLength(2);
  });

  test("resource without validations still passes", async () => {
    registry.register(makeResource({ id: "vo.K.A", kind: "valueObject" }));

    const validator = new ResourceValidator(new InvariantChecker([]), { projectRoot: tmpDir });
    const result = await validator.validateAsync("vo.K.A", new Map(), registry);
    expect(result.passed).toBe(true);
  });
});
