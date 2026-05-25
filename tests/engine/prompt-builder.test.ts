import { describe, test, expect, beforeEach } from "bun:test";
import { PromptBuilder } from "../../src/engine/prompt-builder";
import { ResourceRegistry } from "../../src/registry/resource-registry";
import { makeResource } from "../helpers";

describe("PromptBuilder", () => {
  let registry: ResourceRegistry;
  let builder: PromptBuilder;

  beforeEach(() => {
    registry = new ResourceRegistry();
    builder = new PromptBuilder();
  });

  test("includes resource declaration in prompt", () => {
    registry.register(
      makeResource({
        id: "agg.Comp.Song",
        kind: "aggregate",
        declaration: { state: { id: "SongId", name: "string" } },
      }),
    );
    const prompt = builder.build(registry.getById("agg.Comp.Song")!, registry);
    expect(prompt).toContain("SongId");
    expect(prompt).toContain("Declaration");
  });

  test("includes meta in prompt", () => {
    registry.register(
      makeResource({
        id: "agg.Comp.Song",
        kind: "aggregate",
        meta: { style: "functional", avoid: ["any"] },
      }),
    );
    const prompt = builder.build(registry.getById("agg.Comp.Song")!, registry);
    expect(prompt).toContain("functional");
    expect(prompt).toContain("any");
  });

  test("includes commands and events", () => {
    registry.register(
      makeResource({
        id: "agg.Comp.Song",
        kind: "aggregate",
        commands: [{ name: "RenameSong", payload: { name: "string" } }],
        events: [{ name: "SongRenamed", payload: { id: "SongId", name: "string" } }],
      }),
    );
    const prompt = builder.build(registry.getById("agg.Comp.Song")!, registry);
    expect(prompt).toContain("RenameSong");
    expect(prompt).toContain("SongRenamed");
  });

  test("includes invariants", () => {
    registry.register(
      makeResource({
        id: "agg.Comp.Song",
        kind: "aggregate",
        invariants: ["tempo is between 20 and 999"],
      }),
    );
    const prompt = builder.build(registry.getById("agg.Comp.Song")!, registry);
    expect(prompt).toContain("tempo is between 20 and 999");
  });

  test("includes contract from implemented port", () => {
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
    const prompt = builder.build(registry.getById("agg.Comp.Linear")!, registry);
    expect(prompt).toContain("render");
    expect(prompt).toContain("NoteEvent[]");
  });

  test("system prompt instructs structured output", () => {
    const systemPrompt = builder.systemPrompt();
    expect(systemPrompt).toContain("// path:");
    expect(systemPrompt).toContain("fenced code block");
  });
});
