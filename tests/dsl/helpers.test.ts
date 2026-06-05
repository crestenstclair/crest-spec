import { describe, test, expect } from "bun:test";
import { command, event, operation, invariant, relationship, layer } from "../../src/dsl/helpers";

describe("command()", () => {
  test("creates a command descriptor with name and payload", () => {
    const cmd = command("RenameSong", { name: "string" });
    expect(cmd).toEqual({ name: "RenameSong", payload: { name: "string" } });
  });

  test("creates a command descriptor with empty payload", () => {
    const cmd = command("Play");
    expect(cmd).toEqual({ name: "Play", payload: {} });
  });
});

describe("event()", () => {
  test("creates an event descriptor with name and payload", () => {
    const evt = event("SongRenamed", { id: "SongId", name: "string" });
    expect(evt).toEqual({
      name: "SongRenamed",
      payload: { id: "SongId", name: "string" },
    });
  });
});

describe("operation()", () => {
  test("creates an operation descriptor", () => {
    const op = operation("renameSong", { input: { id: "SongId", name: "string" } });
    expect(op).toEqual({
      name: "renameSong",
      input: { id: "SongId", name: "string" },
    });
  });
});

describe("invariant()", () => {
  test("creates an invariant descriptor from a string", () => {
    const inv = invariant("tempo is between 20 and 999 BPM");
    expect(inv).toEqual({ text: "tempo is between 20 and 999 BPM" });
  });

  test("creates an invariant descriptor with meta", () => {
    const inv = invariant("all mutations go through ApplicationServices", {
      meta: { rationale: "single audit point" },
    });
    expect(inv).toEqual({
      text: "all mutations go through ApplicationServices",
      meta: { rationale: "single audit point" },
    });
  });
});

describe("relationship()", () => {
  test("creates a context relationship", () => {
    const rel = relationship("Playback", "Composition", {
      kind: "customer-supplier",
      direction: "downstream",
    });
    expect(rel).toEqual({
      from: "Playback",
      to: "Composition",
      kind: "customer-supplier",
      direction: "downstream",
    });
  });
});

describe("layer()", () => {
  test("creates a layer rule", () => {
    const rule = layer("domain").dependsOn([]);
    expect(rule).toEqual({ layer: "domain", dependsOn: [] });
  });

  test("creates a layer rule with dependencies", () => {
    const rule = layer("application").dependsOn(["domain"]);
    expect(rule).toEqual({ layer: "application", dependsOn: ["domain"] });
  });
});
