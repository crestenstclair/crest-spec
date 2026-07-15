-- Complete the Phase 5 execution trace with direct expected goal/capability
-- impact and host-reported completion metadata.
ALTER TABLE execution_manifests
ADD COLUMN host_commit_ref TEXT NOT NULL DEFAULT '';

ALTER TABLE execution_manifests
ADD COLUMN goal_progress_json TEXT NOT NULL DEFAULT '{}';

ALTER TABLE execution_manifests
ADD COLUMN goal_progress_hash TEXT NOT NULL DEFAULT '';

CREATE TABLE execution_goals (
    execution_id TEXT NOT NULL REFERENCES execution_manifests(id) ON DELETE CASCADE,
    goal_id      TEXT NOT NULL REFERENCES project_goals(id),
    PRIMARY KEY (execution_id, goal_id)
);
CREATE INDEX idx_execution_goals_goal ON execution_goals(goal_id, execution_id);

CREATE TABLE execution_capabilities (
    execution_id  TEXT NOT NULL REFERENCES execution_manifests(id) ON DELETE CASCADE,
    capability_id TEXT NOT NULL REFERENCES project_capabilities(id),
    PRIMARY KEY (execution_id, capability_id)
);
CREATE INDEX idx_execution_capabilities_capability ON execution_capabilities(capability_id, execution_id);
