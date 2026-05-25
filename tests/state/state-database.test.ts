import { describe, test, expect, beforeEach } from "bun:test";
import { StateDatabase } from "../../src/state/state-database";
import type { StoredResource, StoredFile, StoredDependency } from "../../src/state/types";

describe("StateDatabase", () => {
  let db: StateDatabase;

  beforeEach(() => {
    db = new StateDatabase(":memory:");
  });

  describe("resources", () => {
    const resource: StoredResource = {
      id: "aggregate.Comp.Song",
      kind: "aggregate",
      context: "Comp",
      declaration_hash: "abc123",
      effective_hash: "def456",
      declaration_json: '{"state":{"id":"SongId"}}',
      layer: "domain",
      settled_at: null,
      last_apply_id: null,
    };

    test("upsertResource and getResource", () => {
      db.upsertResource(resource);
      const result = db.getResource("aggregate.Comp.Song");
      expect(result).not.toBeNull();
      expect(result!.id).toBe("aggregate.Comp.Song");
      expect(result!.effective_hash).toBe("def456");
    });

    test("getResource returns null for unknown ID", () => {
      expect(db.getResource("nonexistent")).toBeNull();
    });

    test("getAllResources returns all resources", () => {
      db.upsertResource(resource);
      db.upsertResource({ ...resource, id: "aggregate.Comp.Chain", declaration_hash: "x", effective_hash: "y" });
      expect(db.getAllResources()).toHaveLength(2);
    });

    test("upsertResource updates existing resource", () => {
      db.upsertResource(resource);
      db.upsertResource({ ...resource, effective_hash: "updated" });
      const result = db.getResource("aggregate.Comp.Song");
      expect(result!.effective_hash).toBe("updated");
    });

    test("deleteResource removes a resource", () => {
      db.upsertResource(resource);
      db.deleteResource("aggregate.Comp.Song");
      expect(db.getResource("aggregate.Comp.Song")).toBeNull();
    });
  });

  describe("generated files", () => {
    test("upsertGeneratedFile and getGeneratedFile", () => {
      db.upsertResource({
        id: "aggregate.Comp.Song", kind: "aggregate", context: "Comp",
        declaration_hash: "h", effective_hash: "h", declaration_json: "{}",
        layer: "domain", settled_at: null, last_apply_id: null,
      });
      const file: StoredFile = {
        path: "src/comp/song.ts",
        resource_id: "aggregate.Comp.Song",
        content_hash: "hash123",
        generator: "llm",
        model: "claude-sonnet-4-6",
        prompt_hash: "phash",
        generated_at: "2026-05-18T00:00:00Z",
      };
      db.upsertGeneratedFile(file);
      const result = db.getGeneratedFile("src/comp/song.ts");
      expect(result).not.toBeNull();
      expect(result!.content_hash).toBe("hash123");
    });

    test("getFilesForResource returns files for a resource", () => {
      db.upsertResource({
        id: "aggregate.Comp.Song", kind: "aggregate", context: "Comp",
        declaration_hash: "h", effective_hash: "h", declaration_json: "{}",
        layer: "domain", settled_at: null, last_apply_id: null,
      });
      const file: StoredFile = {
        path: "src/comp/song.ts",
        resource_id: "aggregate.Comp.Song",
        content_hash: "h1",
        generator: "llm",
        model: null,
        prompt_hash: null,
        generated_at: "2026-05-18T00:00:00Z",
      };
      db.upsertGeneratedFile(file);
      db.upsertGeneratedFile({ ...file, path: "src/comp/song.test.ts", content_hash: "h2" });
      expect(db.getFilesForResource("aggregate.Comp.Song")).toHaveLength(2);
    });
  });

  describe("dependencies", () => {
    function insertResources() {
      db.upsertResource({
        id: "agg.Linear", kind: "aggregate", context: null,
        declaration_hash: "h", effective_hash: "h", declaration_json: "{}",
        layer: null, settled_at: null, last_apply_id: null,
      });
      db.upsertResource({
        id: "port.Render", kind: "port", context: null,
        declaration_hash: "h", effective_hash: "h", declaration_json: "{}",
        layer: null, settled_at: null, last_apply_id: null,
      });
    }

    test("setDependencies and getDependencies", () => {
      insertResources();
      const deps: StoredDependency[] = [
        { from_resource: "agg.Linear", to_resource: "port.Render", kind: "implements" },
      ];
      db.setDependencies("agg.Linear", deps);
      const result = db.getDependencies("agg.Linear");
      expect(result).toHaveLength(1);
      expect(result[0].to_resource).toBe("port.Render");
    });

    test("getDependents returns reverse dependencies", () => {
      insertResources();
      const deps: StoredDependency[] = [
        { from_resource: "agg.Linear", to_resource: "port.Render", kind: "implements" },
      ];
      db.setDependencies("agg.Linear", deps);
      const result = db.getDependents("port.Render");
      expect(result).toHaveLength(1);
      expect(result[0].from_resource).toBe("agg.Linear");
    });
  });

  describe("applies", () => {
    test("beginApply creates a running apply", () => {
      const apply = db.beginApply("spechash123");
      expect(apply.id).toBeGreaterThan(0);
      expect(apply.status).toBe("running");
    });

    test("finishApply updates status", () => {
      const apply = db.beginApply("spechash123");
      db.finishApply(apply.id, "ok");
      const applies = db.getApplies();
      expect(applies[0].status).toBe("ok");
    });

    test("recordAction stores an action", () => {
      const apply = db.beginApply("spechash123");
      db.recordAction(apply.id, "aggregate.Comp.Song", "create", "success");
      const actions = db.getApplyActions(apply.id);
      expect(actions).toHaveLength(1);
      expect(actions[0].action).toBe("create");
    });
  });

  describe("generations", () => {
    test("recordGeneration and getGenerationsForResource", () => {
      const apply = db.beginApply("hash");
      db.recordGeneration({
        apply_id: apply.id,
        resource_id: "aggregate.Comp.Song",
        model: "claude-sonnet-4-6",
        prompt_hash: "ph",
        prompt_text: "generate Song",
        output_text: "export type Song = {}",
        retries: 0,
        outcome: "accepted",
        rejection_reason: null,
        created_at: "2026-05-18T00:00:00Z",
      });
      const gens = db.getGenerationsForResource("aggregate.Comp.Song");
      expect(gens).toHaveLength(1);
      expect(gens[0].outcome).toBe("accepted");
    });
  });

  describe("coordination lock", () => {
    test("acquireLock succeeds when no lock exists", () => {
      expect(db.acquireLock("worker-1")).toBe(true);
    });

    test("acquireLock fails when lock is held", () => {
      db.acquireLock("worker-1");
      expect(db.acquireLock("worker-2")).toBe(false);
    });

    test("getLock returns current lock holder", () => {
      db.acquireLock("worker-1");
      const lock = db.getLock();
      expect(lock).not.toBeNull();
      expect(lock!.holder).toBe("worker-1");
    });

    test("releaseLock clears the lock", () => {
      db.acquireLock("worker-1");
      db.releaseLock();
      expect(db.getLock()).toBeNull();
    });

    test("forceClearLock clears even without holding", () => {
      db.acquireLock("worker-1");
      db.forceClearLock();
      expect(db.getLock()).toBeNull();
    });
  });

  describe("invariant checks", () => {
    test("recordInvariantCheck stores a check", () => {
      const apply = db.beginApply("hash");
      db.recordInvariantCheck({
        apply_id: apply.id,
        invariant: "domain layer has no infrastructure imports",
        resource_id: "aggregate.Comp.Song",
        status: "ok",
        detail: null,
      });
    });
  });
});
