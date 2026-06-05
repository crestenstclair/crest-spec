export const SCHEMA_VERSION = 2;

export const SCHEMA_DDL = `
CREATE TABLE IF NOT EXISTS resources (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  context TEXT,
  declaration_hash TEXT NOT NULL,
  effective_hash TEXT NOT NULL,
  declaration_json TEXT NOT NULL,
  layer TEXT,
  settled_at TEXT,
  last_apply_id INTEGER
);

CREATE TABLE IF NOT EXISTS generated_files (
  path TEXT PRIMARY KEY,
  resource_id TEXT NOT NULL REFERENCES resources(id),
  content_hash TEXT NOT NULL,
  generator TEXT NOT NULL,
  model TEXT,
  prompt_hash TEXT,
  generated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS dependencies (
  from_resource TEXT NOT NULL REFERENCES resources(id),
  to_resource TEXT NOT NULL REFERENCES resources(id),
  kind TEXT NOT NULL,
  PRIMARY KEY (from_resource, to_resource, kind)
);

CREATE TABLE IF NOT EXISTS context_relationships (
  from_context TEXT NOT NULL,
  to_context TEXT NOT NULL,
  kind TEXT NOT NULL,
  direction TEXT NOT NULL,
  PRIMARY KEY (from_context, to_context, kind)
);

CREATE TABLE IF NOT EXISTS applies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  status TEXT NOT NULL,
  spec_hash TEXT NOT NULL,
  notes TEXT
);

CREATE TABLE IF NOT EXISTS apply_actions (
  apply_id INTEGER NOT NULL REFERENCES applies(id),
  resource_id TEXT NOT NULL,
  action TEXT NOT NULL,
  outcome TEXT NOT NULL,
  PRIMARY KEY (apply_id, resource_id)
);

CREATE TABLE IF NOT EXISTS generations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  apply_id INTEGER NOT NULL REFERENCES applies(id),
  resource_id TEXT NOT NULL,
  model TEXT NOT NULL,
  prompt_hash TEXT NOT NULL,
  prompt_text TEXT NOT NULL,
  output_text TEXT NOT NULL,
  retries INTEGER NOT NULL,
  outcome TEXT NOT NULL,
  rejection_reason TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS invariant_checks (
  apply_id INTEGER NOT NULL,
  invariant TEXT NOT NULL,
  resource_id TEXT,
  status TEXT NOT NULL,
  detail TEXT,
  PRIMARY KEY (apply_id, invariant, resource_id)
);

CREATE TABLE IF NOT EXISTS lock (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  holder TEXT NOT NULL,
  acquired_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_sessions (
  apply_id INTEGER PRIMARY KEY REFERENCES applies(id),
  plan_json TEXT NOT NULL,
  waves_json TEXT NOT NULL,
  hashes_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_notes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  resource_id TEXT NOT NULL,
  apply_id INTEGER NOT NULL REFERENCES applies(id),
  content TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_notes_resource ON agent_notes(resource_id);
`;
