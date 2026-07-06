-- Real-time agent event log for tracing sub-agent behavior.
CREATE TABLE IF NOT EXISTS agent_events (
    id            TEXT PRIMARY KEY,
    generation_id TEXT,
    resource_id   TEXT NOT NULL,
    apply_id      TEXT,
    event_type    TEXT NOT NULL CHECK (event_type IN ('started','stderr','completed','failed')),
    content       TEXT,
    created_at    TEXT NOT NULL
);
CREATE INDEX idx_agent_events_generation ON agent_events(generation_id);
CREATE INDEX idx_agent_events_resource ON agent_events(resource_id);
CREATE INDEX idx_agent_events_apply ON agent_events(apply_id);
CREATE INDEX idx_agent_events_created ON agent_events(created_at);
