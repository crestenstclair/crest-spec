import { describe, test, expect, beforeEach } from "bun:test";
import { StateDatabase } from "../../src/state/state-database";

describe("StateDatabase agent methods", () => {
  let state: StateDatabase;

  beforeEach(() => {
    state = new StateDatabase(":memory:");
  });

  describe("sessions", () => {
    test("createAgentSession and getAgentSession round-trip", () => {
      const apply = state.beginApply("hash-1");
      state.createAgentSession({
        apply_id: apply.id,
        plan_json: '[{"resourceId":"vo.X","action":"create"}]',
        waves_json: '[["vo.X"]]',
        hashes_json: '{"vo.X":"abc123"}',
        created_at: "2026-01-01T00:00:00Z",
      });

      const session = state.getAgentSession(apply.id);
      expect(session).not.toBeNull();
      expect(session!.plan_json).toBe('[{"resourceId":"vo.X","action":"create"}]');
      expect(session!.waves_json).toBe('[["vo.X"]]');
    });

    test("getActiveAgentSession returns session for running apply", () => {
      const apply = state.beginApply("hash-1");
      state.createAgentSession({
        apply_id: apply.id,
        plan_json: "[]",
        waves_json: "[]",
        hashes_json: "{}",
        created_at: "2026-01-01T00:00:00Z",
      });

      const session = state.getActiveAgentSession();
      expect(session).not.toBeNull();
      expect(session!.apply_id).toBe(apply.id);
    });

    test("getActiveAgentSession returns null when no running apply", () => {
      const apply = state.beginApply("hash-1");
      state.createAgentSession({
        apply_id: apply.id,
        plan_json: "[]",
        waves_json: "[]",
        hashes_json: "{}",
        created_at: "2026-01-01T00:00:00Z",
      });
      state.finishApply(apply.id, "ok");

      const session = state.getActiveAgentSession();
      expect(session).toBeNull();
    });

    test("deleteAgentSession removes session", () => {
      const apply = state.beginApply("hash-1");
      state.createAgentSession({
        apply_id: apply.id,
        plan_json: "[]",
        waves_json: "[]",
        hashes_json: "{}",
        created_at: "2026-01-01T00:00:00Z",
      });

      state.deleteAgentSession(apply.id);
      const session = state.getAgentSession(apply.id);
      expect(session).toBeNull();
    });
  });

  describe("notes", () => {
    test("addAgentNote and getAgentNotes round-trip", () => {
      const apply = state.beginApply("hash-1");
      const noteId = state.addAgentNote({
        resource_id: "vo.Test.X",
        apply_id: apply.id,
        content: "Used newtype pattern",
        created_at: "2026-01-01T00:00:00Z",
      });

      expect(noteId).toBeGreaterThan(0);

      const notes = state.getAgentNotes("vo.Test.X");
      expect(notes).toHaveLength(1);
      expect(notes[0].content).toBe("Used newtype pattern");
    });

    test("multiple notes append for same resource", () => {
      const apply = state.beginApply("hash-1");
      state.addAgentNote({
        resource_id: "vo.Test.X",
        apply_id: apply.id,
        content: "Note 1",
        created_at: "2026-01-01T00:00:00Z",
      });
      state.addAgentNote({
        resource_id: "vo.Test.X",
        apply_id: apply.id,
        content: "Note 2",
        created_at: "2026-01-01T00:00:01Z",
      });

      const notes = state.getAgentNotes("vo.Test.X");
      expect(notes).toHaveLength(2);
    });

    test("getAgentNotesForApply filters by apply_id", () => {
      const apply1 = state.beginApply("hash-1");
      state.finishApply(apply1.id, "ok");
      const apply2 = state.beginApply("hash-2");

      state.addAgentNote({
        resource_id: "vo.Test.X",
        apply_id: apply1.id,
        content: "Old note",
        created_at: "2026-01-01T00:00:00Z",
      });
      state.addAgentNote({
        resource_id: "vo.Test.X",
        apply_id: apply2.id,
        content: "New note",
        created_at: "2026-01-02T00:00:00Z",
      });

      const notes = state.getAgentNotesForApply("vo.Test.X", apply2.id);
      expect(notes).toHaveLength(1);
      expect(notes[0].content).toBe("New note");
    });

    test("getLatestAgentNotes returns notes from most recent apply only", () => {
      const apply1 = state.beginApply("hash-1");
      state.addAgentNote({
        resource_id: "vo.Test.X",
        apply_id: apply1.id,
        content: "Old note",
        created_at: "2026-01-01T00:00:00Z",
      });
      state.finishApply(apply1.id, "ok");

      const apply2 = state.beginApply("hash-2");
      state.addAgentNote({
        resource_id: "vo.Test.X",
        apply_id: apply2.id,
        content: "Fresh note",
        created_at: "2026-01-02T00:00:00Z",
      });
      state.finishApply(apply2.id, "ok");

      const notes = state.getLatestAgentNotes("vo.Test.X");
      expect(notes).toHaveLength(1);
      expect(notes[0].content).toBe("Fresh note");
    });
  });
});
