import { describe, test, expect, beforeEach } from "bun:test";
import { ProjectBuilder } from "../../src/dsl/project-builder";
import { command, event } from "../../src/dsl/helpers";
import { resetSingleton, getActiveProject } from "../../src/dsl/singleton";

beforeEach(() => {
  resetSingleton();
});

describe("ProjectBuilder", () => {
  test("project() creates a ProjectBuilder and sets it as active", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker", {
      layers: ["domain", "application", "infrastructure"],
    });
    expect(app).toBeInstanceOf(ProjectBuilder);
    expect(getActiveProject()).toBe(app);
  });

  test("stores project config in registry", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker", {
      layers: ["domain", "application"],
    });
    const registry = app.getRegistry();
    const projectResource = registry.getById("project.tracker");
    expect(projectResource).not.toBeNull();
    expect(projectResource!.kind).toBe("project");
    expect(projectResource!.name).toBe("tracker");
  });
});

describe("ContextBuilder", () => {
  test("context() creates a context resource", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", {
      purpose: "structural model of a song",
    });
    const registry = app.getRegistry();
    const ctx = registry.getById("context.Composition");
    expect(ctx).not.toBeNull();
    expect(ctx!.kind).toBe("context");
    expect(ctx!.declaration).toEqual({ purpose: "structural model of a song" });
  });
});

describe("ContextBuilder.aggregate()", () => {
  test("creates an aggregate resource with commands and events", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    comp.aggregate("Song", {
      root: true,
      state: { id: "SongId", name: "string" },
      commands: [command("RenameSong", { name: "string" })],
      events: [event("SongRenamed", { id: "SongId", name: "string" })],
    });

    const registry = app.getRegistry();
    const agg = registry.getById("aggregate.Composition.Song");
    expect(agg).not.toBeNull();
    expect(agg!.kind).toBe("aggregate");
    expect(agg!.context).toBe("Composition");
    expect(agg!.commands).toHaveLength(1);
    expect(agg!.commands![0].name).toBe("RenameSong");
    expect(agg!.events).toHaveLength(1);
  });
});

describe("ContextBuilder.valueObject()", () => {
  test("creates a value object resource", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    comp.valueObject("Ticks", { from: "number", description: "musical time" });

    const registry = app.getRegistry();
    const vo = registry.getById("valueObject.Composition.Ticks");
    expect(vo).not.toBeNull();
    expect(vo!.kind).toBe("valueObject");
  });
});

describe("ContextBuilder.port()", () => {
  test("creates a port resource and returns a PortRef", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    const portRef = comp.port("PhraseRender", {
      contract: { render: "(ctx: MusicalContext) => NoteEvent[]" },
    });

    expect(portRef.id).toBe("port.Composition.PhraseRender");
    expect(portRef.name).toBe("PhraseRender");
    expect(portRef.contract).toEqual({
      render: "(ctx: MusicalContext) => NoteEvent[]",
    });

    const registry = app.getRegistry();
    const port = registry.getById("port.Composition.PhraseRender");
    expect(port).not.toBeNull();
    expect(port!.kind).toBe("port");
  });
});

describe("ContextBuilder.repository()", () => {
  test("creates a repository with a dependency on its aggregate", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    const songAgg = comp.aggregate("Song", { root: true, state: { id: "SongId" } });
    comp.repository("SongRepository", { of: songAgg });

    const registry = app.getRegistry();
    const repo = registry.getById("repository.Composition.SongRepository");
    expect(repo).not.toBeNull();
    expect(repo!.dependencies).toEqual([
      { targetId: "aggregate.Composition.Song", kind: "of" },
    ]);
  });
});

describe("AggregateBuilder.entity()", () => {
  test("creates a child entity inside an aggregate", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    const agg = comp.aggregate("Chain", { root: true, state: { id: "ChainId" } });
    agg.entity("ChainSlot", { state: { at: "Index", phraseId: "PhraseId | null" } });

    const registry = app.getRegistry();
    const entity = registry.getById("entity.Composition.Chain.ChainSlot");
    expect(entity).not.toBeNull();
    expect(entity!.kind).toBe("entity");
  });
});

describe("aggregate with implements", () => {
  test("creates a dependency on the implemented port", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    const portRef = comp.port("PhraseRender", {
      contract: { render: "(ctx: MusicalContext) => NoteEvent[]" },
    });
    comp.aggregate("LinearPhrase", {
      root: true,
      implements: portRef,
      state: { id: "PhraseId" },
    });

    const registry = app.getRegistry();
    const agg = registry.getById("aggregate.Composition.LinearPhrase");
    expect(agg!.dependencies).toEqual([
      { targetId: "port.Composition.PhraseRender", kind: "implements" },
    ]);
  });
});

describe("meta inheritance", () => {
  test("context inherits project meta", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker", { meta: { style: "functional" } });
    const comp = app.context("Composition", { purpose: "test" });
    comp.aggregate("Song", { root: true, state: {} });

    const registry = app.getRegistry();
    const agg = registry.getById("aggregate.Composition.Song");
    expect(agg!.meta.style).toBe("functional");
  });

  test("resource meta overrides project meta for scalars", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker", { meta: { style: "functional" } });
    const comp = app.context("Composition", { purpose: "test" });
    comp.aggregate("Song", {
      root: true,
      state: {},
      meta: { style: "OOP" },
    });

    const registry = app.getRegistry();
    const agg = registry.getById("aggregate.Composition.Song");
    expect(agg!.meta.style).toBe("OOP");
  });

  test("resource meta concatenates project meta for arrays", () => {
    const { project } = require("../../src/dsl/project-builder");
    const app = project("tracker", { meta: { avoid: ["any"] } });
    const comp = app.context("Composition", {
      purpose: "test",
      meta: { avoid: ["setInterval"] },
    });

    const registry = app.getRegistry();
    const ctx = registry.getById("context.Composition");
    expect(ctx!.meta.avoid).toEqual(["any", "setInterval"]);
  });
});

describe("contextMap()", () => {
  test("stores context relationships in the registry", () => {
    const { project } = require("../../src/dsl/project-builder");
    const { relationship } = require("../../src/dsl/helpers");
    const app = project("tracker");
    app.context("Playback", { purpose: "scheduling" });
    app.context("Composition", { purpose: "model" });
    app.contextMap([
      relationship("Playback", "Composition", {
        kind: "customer-supplier",
        direction: "downstream",
      }),
    ]);

    const registry = app.getRegistry();
    const map = registry.getContextMap();
    expect(map).toHaveLength(1);
    expect(map[0].from).toBe("Playback");
    expect(map[0].to).toBe("Composition");
  });
});

describe("applicationService()", () => {
  test("creates an application service with uses dependencies", () => {
    const { project } = require("../../src/dsl/project-builder");
    const { operation } = require("../../src/dsl/helpers");
    const app = project("tracker");
    const comp = app.context("Composition", { purpose: "test" });
    const songAgg = comp.aggregate("Song", { root: true, state: { id: "SongId" } });
    comp.applicationService("SongEditor", {
      purpose: "orchestrates Song commands",
      uses: [songAgg],
      operations: [operation("renameSong", { input: { id: "SongId", name: "string" } })],
    });

    const registry = app.getRegistry();
    const svc = registry.getById("applicationService.Composition.SongEditor");
    expect(svc).not.toBeNull();
    expect(svc!.kind).toBe("applicationService");
    expect(svc!.dependencies).toEqual([
      { targetId: "aggregate.Composition.Song", kind: "uses" },
    ]);
  });
});
