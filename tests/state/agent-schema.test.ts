import { describe, test, expect } from "bun:test";
import { StateDatabase } from "../../src/state/state-database";

describe("agent schema", () => {
  test("agent_sessions table exists after migration", () => {
    const state = new StateDatabase(":memory:");
    const row = state.beginApply("test-hash");
    // Should not throw — table exists
    const db = (state as any).db;
    db.query(
      `INSERT INTO agent_sessions (apply_id, plan_json, waves_json, hashes_json, created_at)
       VALUES (?, '[]', '[]', '{}', '2026-01-01T00:00:00Z')`
    ).run(row.id);
    const session = db.query("SELECT * FROM agent_sessions WHERE apply_id = ?").get(row.id);
    expect(session).not.toBeNull();
    expect(session.plan_json).toBe("[]");
    state.finishApply(row.id, "ok");
  });

  test("agent_notes table exists after migration", () => {
    const state = new StateDatabase(":memory:");
    const row = state.beginApply("test-hash-2");
    const db = (state as any).db;
    db.query(
      `INSERT INTO agent_notes (resource_id, apply_id, content, created_at)
       VALUES ('vo.Test.X', ?, 'test note', '2026-01-01T00:00:00Z')`
    ).run(row.id);
    const note = db.query("SELECT * FROM agent_notes WHERE resource_id = ?").get("vo.Test.X");
    expect(note).not.toBeNull();
    expect(note.content).toBe("test note");
    state.finishApply(row.id, "ok");
  });
});
