import { Database } from "bun:sqlite";
import { SCHEMA_DDL, SCHEMA_VERSION } from "./schema.js";
import type {
  StoredResource,
  StoredFile,
  StoredDependency,
  StoredContextRelationship,
  ApplyRecord,
  ActionRecord,
  GenerationRecord,
  InvariantCheckRecord,
  LockRecord,
  AgentSessionRecord,
  AgentNoteRecord,
} from "./types.js";

export interface IStateDatabase {
  getResource(id: string): StoredResource | null;
  getAllResources(): StoredResource[];
  upsertResource(resource: StoredResource): void;
  deleteResource(id: string): void;

  getGeneratedFile(path: string): StoredFile | null;
  getFilesForResource(resourceId: string): StoredFile[];
  upsertGeneratedFile(file: StoredFile): void;
  deleteGeneratedFile(path: string): void;

  setDependencies(resourceId: string, deps: StoredDependency[]): void;
  getDependencies(resourceId: string): StoredDependency[];
  getDependents(resourceId: string): StoredDependency[];
  setContextRelationships(relationships: StoredContextRelationship[]): void;

  beginApply(specHash: string): ApplyRecord;
  recordAction(applyId: number, resourceId: string, action: string, outcome: string): void;
  finishApply(applyId: number, status: string): void;

  recordGeneration(gen: GenerationRecord): void;
  getGenerationsForResource(resourceId: string): GenerationRecord[];

  recordInvariantCheck(check: InvariantCheckRecord): void;

  acquireLock(holder: string): boolean;
  releaseLock(): void;
  getLock(): LockRecord | null;
  forceClearLock(): void;

  getApplies(limit?: number): ApplyRecord[];
  getApplyActions(applyId: number): ActionRecord[];

  createAgentSession(session: AgentSessionRecord): void;
  getAgentSession(applyId: number): AgentSessionRecord | null;
  getActiveAgentSession(): AgentSessionRecord | null;
  deleteAgentSession(applyId: number): void;

  addAgentNote(note: Omit<AgentNoteRecord, "id">): number;
  getAgentNotes(resourceId: string): AgentNoteRecord[];
  getAgentNotesForApply(resourceId: string, applyId: number): AgentNoteRecord[];
  getLatestAgentNotes(resourceId: string): AgentNoteRecord[];
}

export class StateDatabase implements IStateDatabase {
  private db: Database;

  constructor(path: string) {
    this.db = new Database(path);
    this.db.run("PRAGMA journal_mode = WAL");
    this.db.run("PRAGMA foreign_keys = ON");
    this.migrate();
  }

  private migrate(): void {
    const version = this.db.query("PRAGMA user_version").get() as { user_version: number };
    if (version.user_version < SCHEMA_VERSION) {
      this.db.run(SCHEMA_DDL);
      this.db.run(`PRAGMA user_version = ${SCHEMA_VERSION}`);
    }
  }

  getResource(id: string): StoredResource | null {
    return this.db.query("SELECT * FROM resources WHERE id = ?").get(id) as StoredResource | null;
  }

  getAllResources(): StoredResource[] {
    return this.db.query("SELECT * FROM resources").all() as StoredResource[];
  }

  upsertResource(resource: StoredResource): void {
    this.db
      .query(
        `INSERT INTO resources (id, kind, context, declaration_hash, effective_hash, declaration_json, layer, settled_at, last_apply_id)
         VALUES ($id, $kind, $context, $declaration_hash, $effective_hash, $declaration_json, $layer, $settled_at, $last_apply_id)
         ON CONFLICT(id) DO UPDATE SET
           kind = $kind, context = $context, declaration_hash = $declaration_hash,
           effective_hash = $effective_hash, declaration_json = $declaration_json,
           layer = $layer, settled_at = $settled_at, last_apply_id = $last_apply_id`,
      )
      .run({
        $id: resource.id,
        $kind: resource.kind,
        $context: resource.context,
        $declaration_hash: resource.declaration_hash,
        $effective_hash: resource.effective_hash,
        $declaration_json: resource.declaration_json,
        $layer: resource.layer,
        $settled_at: resource.settled_at,
        $last_apply_id: resource.last_apply_id,
      });
  }

  deleteResource(id: string): void {
    this.db.query("DELETE FROM resources WHERE id = ?").run(id);
  }

  getGeneratedFile(path: string): StoredFile | null {
    return this.db.query("SELECT * FROM generated_files WHERE path = ?").get(path) as StoredFile | null;
  }

  getFilesForResource(resourceId: string): StoredFile[] {
    return this.db
      .query("SELECT * FROM generated_files WHERE resource_id = ?")
      .all(resourceId) as StoredFile[];
  }

  upsertGeneratedFile(file: StoredFile): void {
    this.db
      .query(
        `INSERT INTO generated_files (path, resource_id, content_hash, generator, model, prompt_hash, generated_at)
         VALUES ($path, $resource_id, $content_hash, $generator, $model, $prompt_hash, $generated_at)
         ON CONFLICT(path) DO UPDATE SET
           resource_id = $resource_id, content_hash = $content_hash,
           generator = $generator, model = $model, prompt_hash = $prompt_hash,
           generated_at = $generated_at`,
      )
      .run({
        $path: file.path,
        $resource_id: file.resource_id,
        $content_hash: file.content_hash,
        $generator: file.generator,
        $model: file.model,
        $prompt_hash: file.prompt_hash,
        $generated_at: file.generated_at,
      });
  }

  deleteGeneratedFile(path: string): void {
    this.db.query("DELETE FROM generated_files WHERE path = ?").run(path);
  }

  setDependencies(resourceId: string, deps: StoredDependency[]): void {
    this.db.query("DELETE FROM dependencies WHERE from_resource = ?").run(resourceId);
    const insert = this.db.query(
      "INSERT INTO dependencies (from_resource, to_resource, kind) VALUES ($from, $to, $kind)",
    );
    for (const dep of deps) {
      insert.run({ $from: dep.from_resource, $to: dep.to_resource, $kind: dep.kind });
    }
  }

  getDependencies(resourceId: string): StoredDependency[] {
    return this.db
      .query("SELECT * FROM dependencies WHERE from_resource = ?")
      .all(resourceId) as StoredDependency[];
  }

  getDependents(resourceId: string): StoredDependency[] {
    return this.db
      .query("SELECT * FROM dependencies WHERE to_resource = ?")
      .all(resourceId) as StoredDependency[];
  }

  setContextRelationships(relationships: StoredContextRelationship[]): void {
    this.db.run("DELETE FROM context_relationships");
    const insert = this.db.query(
      `INSERT INTO context_relationships (from_context, to_context, kind, direction)
       VALUES ($from, $to, $kind, $direction)`,
    );
    for (const rel of relationships) {
      insert.run({
        $from: rel.from_context,
        $to: rel.to_context,
        $kind: rel.kind,
        $direction: rel.direction,
      });
    }
  }

  beginApply(specHash: string): ApplyRecord {
    const now = new Date().toISOString();
    this.db
      .query(
        `INSERT INTO applies (started_at, status, spec_hash) VALUES ($started_at, 'running', $spec_hash)`,
      )
      .run({ $started_at: now, $spec_hash: specHash });
    const row = this.db.query("SELECT * FROM applies ORDER BY id DESC LIMIT 1").get() as ApplyRecord;
    return row;
  }

  recordAction(applyId: number, resourceId: string, action: string, outcome: string): void {
    this.db
      .query(
        `INSERT INTO apply_actions (apply_id, resource_id, action, outcome)
         VALUES ($apply_id, $resource_id, $action, $outcome)`,
      )
      .run({ $apply_id: applyId, $resource_id: resourceId, $action: action, $outcome: outcome });
  }

  finishApply(applyId: number, status: string): void {
    const now = new Date().toISOString();
    this.db
      .query("UPDATE applies SET finished_at = $finished_at, status = $status WHERE id = $id")
      .run({ $finished_at: now, $status: status, $id: applyId });
  }

  recordGeneration(gen: GenerationRecord): void {
    this.db
      .query(
        `INSERT INTO generations (apply_id, resource_id, model, prompt_hash, prompt_text, output_text, retries, outcome, rejection_reason, created_at)
         VALUES ($apply_id, $resource_id, $model, $prompt_hash, $prompt_text, $output_text, $retries, $outcome, $rejection_reason, $created_at)`,
      )
      .run({
        $apply_id: gen.apply_id,
        $resource_id: gen.resource_id,
        $model: gen.model,
        $prompt_hash: gen.prompt_hash,
        $prompt_text: gen.prompt_text,
        $output_text: gen.output_text,
        $retries: gen.retries,
        $outcome: gen.outcome,
        $rejection_reason: gen.rejection_reason,
        $created_at: gen.created_at,
      });
  }

  getGenerationsForResource(resourceId: string): GenerationRecord[] {
    return this.db
      .query("SELECT * FROM generations WHERE resource_id = ? ORDER BY created_at")
      .all(resourceId) as GenerationRecord[];
  }

  recordInvariantCheck(check: InvariantCheckRecord): void {
    this.db
      .query(
        `INSERT INTO invariant_checks (apply_id, invariant, resource_id, status, detail)
         VALUES ($apply_id, $invariant, $resource_id, $status, $detail)`,
      )
      .run({
        $apply_id: check.apply_id,
        $invariant: check.invariant,
        $resource_id: check.resource_id,
        $status: check.status,
        $detail: check.detail,
      });
  }

  acquireLock(holder: string): boolean {
    try {
      const now = new Date().toISOString();
      this.db
        .query("INSERT INTO lock (id, holder, acquired_at) VALUES (1, $holder, $acquired_at)")
        .run({ $holder: holder, $acquired_at: now });
      return true;
    } catch {
      return false;
    }
  }

  releaseLock(): void {
    this.db.run("DELETE FROM lock WHERE id = 1");
  }

  getLock(): LockRecord | null {
    return this.db.query("SELECT holder, acquired_at FROM lock WHERE id = 1").get() as LockRecord | null;
  }

  forceClearLock(): void {
    this.db.run("DELETE FROM lock WHERE id = 1");
  }

  getApplies(limit?: number): ApplyRecord[] {
    const sql = limit
      ? "SELECT * FROM applies ORDER BY id DESC LIMIT ?"
      : "SELECT * FROM applies ORDER BY id DESC";
    return (limit ? this.db.query(sql).all(limit) : this.db.query(sql).all()) as ApplyRecord[];
  }

  getApplyActions(applyId: number): ActionRecord[] {
    return this.db
      .query("SELECT * FROM apply_actions WHERE apply_id = ?")
      .all(applyId) as ActionRecord[];
  }

  createAgentSession(session: AgentSessionRecord): void {
    this.db
      .query(
        `INSERT INTO agent_sessions (apply_id, plan_json, waves_json, hashes_json, created_at)
         VALUES ($apply_id, $plan_json, $waves_json, $hashes_json, $created_at)`,
      )
      .run({
        $apply_id: session.apply_id,
        $plan_json: session.plan_json,
        $waves_json: session.waves_json,
        $hashes_json: session.hashes_json,
        $created_at: session.created_at,
      });
  }

  getAgentSession(applyId: number): AgentSessionRecord | null {
    return this.db
      .query("SELECT * FROM agent_sessions WHERE apply_id = ?")
      .get(applyId) as AgentSessionRecord | null;
  }

  getActiveAgentSession(): AgentSessionRecord | null {
    return this.db
      .query(
        `SELECT s.* FROM agent_sessions s
         JOIN applies a ON a.id = s.apply_id
         WHERE a.status = 'running'
         LIMIT 1`,
      )
      .get() as AgentSessionRecord | null;
  }

  deleteAgentSession(applyId: number): void {
    this.db.query("DELETE FROM agent_sessions WHERE apply_id = ?").run(applyId);
  }

  addAgentNote(note: Omit<AgentNoteRecord, "id">): number {
    this.db
      .query(
        `INSERT INTO agent_notes (resource_id, apply_id, content, created_at)
         VALUES ($resource_id, $apply_id, $content, $created_at)`,
      )
      .run({
        $resource_id: note.resource_id,
        $apply_id: note.apply_id,
        $content: note.content,
        $created_at: note.created_at,
      });
    const row = this.db.query("SELECT last_insert_rowid() as id").get() as { id: number };
    return row.id;
  }

  getAgentNotes(resourceId: string): AgentNoteRecord[] {
    return this.db
      .query("SELECT * FROM agent_notes WHERE resource_id = ? ORDER BY created_at")
      .all(resourceId) as AgentNoteRecord[];
  }

  getAgentNotesForApply(resourceId: string, applyId: number): AgentNoteRecord[] {
    return this.db
      .query(
        "SELECT * FROM agent_notes WHERE resource_id = ? AND apply_id = ? ORDER BY created_at",
      )
      .all(resourceId, applyId) as AgentNoteRecord[];
  }

  getLatestAgentNotes(resourceId: string): AgentNoteRecord[] {
    return this.db
      .query(
        `SELECT n.* FROM agent_notes n
         WHERE n.resource_id = ?
         AND n.apply_id = (
           SELECT MAX(apply_id) FROM agent_notes WHERE resource_id = ?
         )
         ORDER BY n.created_at`,
      )
      .all(resourceId, resourceId) as AgentNoteRecord[];
  }
}
