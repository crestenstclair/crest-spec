import { describe, test, expect, beforeEach } from "bun:test";
import { ResourceValidator } from "../../src/engine/resource-validator";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";
import type { IInvariantRule } from "../../src/invariants/invariant-checker";

describe("ResourceValidator", () => {
  let registry: ResourceRegistry;
  let validator: ResourceValidator;

  beforeEach(() => {
    registry = new ResourceRegistry();
  });

  test("passes when no rules and no commands configured", () => {
    const checker = new InvariantChecker([]);
    validator = new ResourceValidator(checker, {});

    registry.register(
      makeResource({ id: "vo.Test.X", kind: "valueObject" }),
    );

    const files = new Map([["src/Test/X/x.ts", "export type X = number;"]]);
    const result = validator.validate("vo.Test.X", files, registry);
    expect(result.passed).toBe(true);
    expect(result.errors).toHaveLength(0);
  });

  test("fails when invariant is violated", () => {
    const failingRule: IInvariantRule = {
      name: "test-rule",
      appliesTo: () => true,
      checkGenerated: (resource) => ({
        invariant: "test-rule",
        resourceId: resource.id,
        status: "violated",
        detail: "missing constructor check",
        rationale: null,
      }),
    };
    const checker = new InvariantChecker([failingRule]);
    validator = new ResourceValidator(checker, {});

    registry.register(
      makeResource({ id: "vo.Test.X", kind: "valueObject" }),
    );

    const files = new Map([["src/Test/X/x.ts", "export type X = number;"]]);
    const result = validator.validate("vo.Test.X", files, registry);
    expect(result.passed).toBe(false);
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.errors[0]).toContain("missing constructor check");
  });

  test("returns error when resource not found", () => {
    const checker = new InvariantChecker([]);
    validator = new ResourceValidator(checker, {});

    const files = new Map<string, string>();
    const result = validator.validate("vo.Test.Missing", files, registry);
    expect(result.passed).toBe(false);
    expect(result.errors[0]).toContain("not found");
  });
});
