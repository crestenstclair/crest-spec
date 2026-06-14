-- Expand agent_events: remove restrictive CHECK, add attempt tracking.
CREATE TABLE agent_events_new (
    id            TEXT PRIMARY KEY,
    generation_id TEXT,
    resource_id   TEXT NOT NULL,
    apply_id      TEXT,
    event_type    TEXT NOT NULL,
    attempt       INTEGER,
    content       TEXT,
    created_at    TEXT NOT NULL
);
INSERT INTO agent_events_new SELECT id, generation_id, resource_id, apply_id, event_type, NULL, content, created_at FROM agent_events;
DROP TABLE agent_events;
ALTER TABLE agent_events_new RENAME TO agent_events;
CREATE INDEX idx_agent_events_generation ON agent_events(generation_id);
CREATE INDEX idx_agent_events_resource ON agent_events(resource_id);
CREATE INDEX idx_agent_events_apply ON agent_events(apply_id);
CREATE INDEX idx_agent_events_created ON agent_events(created_at);
