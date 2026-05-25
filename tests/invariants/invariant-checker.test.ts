import { describe, test, expect } from "bun:test";
import { InvariantChecker } from "../../src/invariants/invariant-checker";
import { AggregateHasRepository } from "../../src/invariants/rules/aggregate-has-repository";
import { DomainNoInfraImports } from "../../src/invariants/rules/domain-no-infra-imports";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";

describe("InvariantChecker", () => {
  test("checkStructural runs all applicable rules", () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", declaration: { root: true } }));

    const checker = new InvariantChecker([new AggregateHasRepository()]);
    const results = checker.checkStructural(registry);
    expect(results).toHaveLength(1);
    expect(results[0].status).toBe("violated");
  });

  test("checkGenerated runs code-level rules", () => {
    const registry = new ResourceRegistry();
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", layer: "domain" }));

    const checker = new InvariantChecker([new DomainNoInfraImports()]);
    const files = new Map([["src/song.ts", 'import { x } from "../infrastructure/db";\n']]);
    const results = checker.checkGenerated("agg.Comp.Song", files, registry);
    expect(results).toHaveLength(1);
    expect(results[0].status).toBe("violated");
  });
});
