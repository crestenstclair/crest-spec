import { describe, test, expect, beforeEach } from "bun:test";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { AggregateHasRepository } from "../../src/invariants/rules/aggregate-has-repository";
import { ContextBoundaries } from "../../src/invariants/rules/context-boundaries";
import { DomainNoInfraImports } from "../../src/invariants/rules/domain-no-infra-imports";
import { ContractCompliance } from "../../src/invariants/rules/contract-compliance";
import { makeResource } from "../helpers";

describe("AggregateHasRepository", () => {
  const rule = new AggregateHasRepository();
  let registry: ResourceRegistry;

  beforeEach(() => {
    registry = new ResourceRegistry();
  });

  test("passes when aggregate root has a repository", () => {
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", declaration: { root: true } }));
    registry.register(
      makeResource({
        id: "repo.Comp.SongRepo",
        kind: "repository",
        dependencies: [{ targetId: "agg.Comp.Song", kind: "of" }],
      }),
    );

    const agg = registry.getById("agg.Comp.Song")!;
    const result = rule.checkStructural!(agg, registry);
    expect(result.status).toBe("ok");
  });

  test("fails when aggregate root has no repository", () => {
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", declaration: { root: true } }));

    const agg = registry.getById("agg.Comp.Song")!;
    const result = rule.checkStructural!(agg, registry);
    expect(result.status).toBe("violated");
  });

  test("does not apply to non-root aggregates", () => {
    const resource = makeResource({ id: "agg.Comp.Song", kind: "aggregate", declaration: { root: false } });
    expect(rule.appliesTo(resource)).toBe(false);
  });
});

describe("ContextBoundaries", () => {
  const rule = new ContextBoundaries();
  let registry: ResourceRegistry;

  beforeEach(() => {
    registry = new ResourceRegistry();
  });

  test("passes when dependency is within the same context", () => {
    registry.register(makeResource({ id: "port.Comp.Render", kind: "port", context: "Comp" }));
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        kind: "aggregate",
        context: "Comp",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );

    const agg = registry.getById("agg.Comp.Linear")!;
    const result = rule.checkStructural!(agg, registry);
    expect(result.status).toBe("ok");
  });

  test("passes when cross-context dependency has a declared relationship", () => {
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", context: "Comp" }));
    registry.register(
      makeResource({
        id: "svc.Play.Seq",
        kind: "applicationService",
        context: "Playback",
        dependencies: [{ targetId: "agg.Comp.Song", kind: "uses" }],
      }),
    );
    registry.setContextMap([
      { from: "Playback", to: "Comp", kind: "customer-supplier", direction: "downstream" },
    ]);

    const svc = registry.getById("svc.Play.Seq")!;
    const result = rule.checkStructural!(svc, registry);
    expect(result.status).toBe("ok");
  });

  test("fails when cross-context dependency has no declared relationship", () => {
    registry.register(makeResource({ id: "agg.Comp.Song", kind: "aggregate", context: "Comp" }));
    registry.register(
      makeResource({
        id: "svc.Play.Seq",
        kind: "applicationService",
        context: "Playback",
        dependencies: [{ targetId: "agg.Comp.Song", kind: "uses" }],
      }),
    );

    const svc = registry.getById("svc.Play.Seq")!;
    const result = rule.checkStructural!(svc, registry);
    expect(result.status).toBe("violated");
  });
});

describe("DomainNoInfraImports", () => {
  const rule = new DomainNoInfraImports();

  test("passes when domain file has no infra imports", () => {
    const resource = makeResource({ id: "agg.Comp.Song", layer: "domain" });
    const files = new Map([
      ["src/comp/domain/song.ts", 'import { Ticks } from "../kernel/ticks";\nexport interface Song {}'],
    ]);

    const registry = new ResourceRegistry();
    registry.register(resource);
    const result = rule.checkGenerated!(resource, files, registry);
    expect(result.status).toBe("ok");
  });

  test("fails when domain file imports from infrastructure", () => {
    const resource = makeResource({ id: "agg.Comp.Song", layer: "domain" });
    const files = new Map([
      ["src/comp/domain/song.ts", 'import { db } from "../infrastructure/database";\nexport interface Song {}'],
    ]);

    const registry = new ResourceRegistry();
    registry.register(resource);
    const result = rule.checkGenerated!(resource, files, registry);
    expect(result.status).toBe("violated");
  });
});

describe("ContractCompliance", () => {
  const rule = new ContractCompliance();
  let registry: ResourceRegistry;

  beforeEach(() => {
    registry = new ResourceRegistry();
  });

  test("passes when generated code contains contract methods", () => {
    registry.register(
      makeResource({
        id: "port.Comp.Render",
        kind: "port",
        declaration: { contract: { render: "(ctx: MusicalContext) => NoteEvent[]" } },
      }),
    );
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );

    const files = new Map([
      ["src/linear.ts", "export function render(ctx: MusicalContext): NoteEvent[] { return []; }"],
    ]);
    const resource = registry.getById("agg.Comp.Linear")!;
    const result = rule.checkGenerated!(resource, files, registry);
    expect(result.status).toBe("ok");
  });

  test("fails when generated code is missing contract methods", () => {
    registry.register(
      makeResource({
        id: "port.Comp.Render",
        kind: "port",
        declaration: { contract: { render: "(ctx: MusicalContext) => NoteEvent[]" } },
      }),
    );
    registry.register(
      makeResource({
        id: "agg.Comp.Linear",
        kind: "aggregate",
        dependencies: [{ targetId: "port.Comp.Render", kind: "implements" }],
      }),
    );

    const files = new Map([["src/linear.ts", "export function doSomething() {}"]]);
    const resource = registry.getById("agg.Comp.Linear")!;
    const result = rule.checkGenerated!(resource, files, registry);
    expect(result.status).toBe("violated");
  });
});
