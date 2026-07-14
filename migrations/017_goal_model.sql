-- Phase 1: strict project intent and evidence-derived completion state.
-- The CUE specification remains authoritative for declarations. These tables
-- hold reconciled identities, lifecycle state, blockers, and history.

CREATE TABLE project_state (
    project_name      TEXT PRIMARY KEY,
    mission           TEXT NOT NULL,
    spec_hash         TEXT NOT NULL,
    completion_status TEXT NOT NULL DEFAULT 'declared'
                      CHECK (completion_status IN (
                          'declared','planned','partially_implemented',
                          'locally_validated','integrated',
                          'behaviorally_verified','complete','blocked','regressed'
                      )),
    active            INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE TABLE project_actors (
    id           TEXT PRIMARY KEY,
    project_name TEXT NOT NULL REFERENCES project_state(project_name),
    description  TEXT NOT NULL,
    spec_hash    TEXT NOT NULL,
    active       INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    updated_at   TEXT NOT NULL
);
CREATE INDEX idx_project_actors_project ON project_actors(project_name, active, id);

CREATE TABLE project_goals (
    id             TEXT PRIMARY KEY,
    project_name   TEXT NOT NULL REFERENCES project_state(project_name),
    description    TEXT NOT NULL,
    priority       TEXT NOT NULL CHECK (priority IN ('required','optional')),
    status         TEXT NOT NULL DEFAULT 'declared'
                   CHECK (status IN (
                       'declared','planned','partially_implemented',
                       'locally_validated','integrated',
                       'behaviorally_verified','complete','blocked','regressed'
                   )),
    status_reason  TEXT NOT NULL DEFAULT '',
    spec_hash      TEXT NOT NULL,
    active         INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX idx_project_goals_project ON project_goals(project_name, active, priority, status, id);

CREATE TABLE goal_actors (
    goal_id  TEXT NOT NULL REFERENCES project_goals(id),
    actor_id TEXT NOT NULL REFERENCES project_actors(id),
    PRIMARY KEY (goal_id, actor_id)
);

CREATE TABLE goal_dependencies (
    goal_id            TEXT NOT NULL REFERENCES project_goals(id),
    dependency_goal_id TEXT NOT NULL REFERENCES project_goals(id),
    PRIMARY KEY (goal_id, dependency_goal_id),
    CHECK (goal_id <> dependency_goal_id)
);

CREATE TABLE project_capabilities (
    id           TEXT PRIMARY KEY,
    project_name TEXT NOT NULL REFERENCES project_state(project_name),
    description  TEXT NOT NULL,
    spec_hash    TEXT NOT NULL,
    active       INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    updated_at   TEXT NOT NULL
);
CREATE INDEX idx_project_capabilities_project ON project_capabilities(project_name, active, id);

CREATE TABLE capability_goals (
    capability_id TEXT NOT NULL REFERENCES project_capabilities(id),
    goal_id       TEXT NOT NULL REFERENCES project_goals(id),
    PRIMARY KEY (capability_id, goal_id)
);

CREATE TABLE project_requirements (
    id           TEXT PRIMARY KEY,
    project_name TEXT NOT NULL REFERENCES project_state(project_name),
    kind         TEXT NOT NULL CHECK (kind IN ('functional','nonfunctional')),
    description  TEXT NOT NULL,
    spec_hash    TEXT NOT NULL,
    active       INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    updated_at   TEXT NOT NULL
);
CREATE INDEX idx_project_requirements_project ON project_requirements(project_name, active, kind, id);

CREATE TABLE requirement_goals (
    requirement_id TEXT NOT NULL REFERENCES project_requirements(id),
    goal_id        TEXT NOT NULL REFERENCES project_goals(id),
    PRIMARY KEY (requirement_id, goal_id)
);

CREATE TABLE requirement_capabilities (
    requirement_id  TEXT NOT NULL REFERENCES project_requirements(id),
    capability_id   TEXT NOT NULL REFERENCES project_capabilities(id),
    PRIMARY KEY (requirement_id, capability_id)
);

CREATE TABLE acceptance_scenarios (
    id            TEXT PRIMARY KEY,
    capability_id TEXT NOT NULL REFERENCES project_capabilities(id),
    actor_id       TEXT REFERENCES project_actors(id),
    description    TEXT NOT NULL,
    ordinal        INTEGER NOT NULL DEFAULT 0,
    spec_hash      TEXT NOT NULL,
    active         INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    updated_at     TEXT NOT NULL
);
CREATE INDEX idx_acceptance_capability ON acceptance_scenarios(capability_id, active, ordinal, id);

CREATE TABLE acceptance_steps (
    scenario_id TEXT NOT NULL REFERENCES acceptance_scenarios(id),
    ordinal     INTEGER NOT NULL,
    action      TEXT NOT NULL,
    observes    TEXT NOT NULL,
    PRIMARY KEY (scenario_id, ordinal)
);

CREATE TABLE evidence_requirements (
    id           TEXT PRIMARY KEY,
    project_name TEXT NOT NULL REFERENCES project_state(project_name),
    kind         TEXT NOT NULL,
    description  TEXT NOT NULL,
    spec_hash    TEXT NOT NULL,
    active       INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    updated_at   TEXT NOT NULL
);
CREATE INDEX idx_evidence_requirements_project ON evidence_requirements(project_name, active, id);

CREATE TABLE acceptance_evidence (
    scenario_id TEXT NOT NULL REFERENCES acceptance_scenarios(id),
    evidence_id TEXT NOT NULL REFERENCES evidence_requirements(id),
    PRIMARY KEY (scenario_id, evidence_id)
);

CREATE TABLE project_required_goals (
    project_name TEXT NOT NULL REFERENCES project_state(project_name),
    goal_id      TEXT NOT NULL REFERENCES project_goals(id),
    ordinal      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project_name, goal_id)
);

CREATE TABLE project_non_goals (
    id           TEXT PRIMARY KEY,
    project_name TEXT NOT NULL REFERENCES project_state(project_name),
    description  TEXT NOT NULL,
    spec_hash    TEXT NOT NULL,
    active       INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    updated_at   TEXT NOT NULL
);
CREATE INDEX idx_project_non_goals_project ON project_non_goals(project_name, active, id);

CREATE TABLE completion_blockers (
    id            TEXT PRIMARY KEY,
    project_name  TEXT NOT NULL REFERENCES project_state(project_name),
    goal_id       TEXT REFERENCES project_goals(id),
    category      TEXT NOT NULL,
    reason        TEXT NOT NULL,
    source_type   TEXT NOT NULL DEFAULT '',
    source_id     TEXT NOT NULL DEFAULT '',
    resolved      INTEGER NOT NULL DEFAULT 0 CHECK (resolved IN (0,1)),
    resolution    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    resolved_at   TEXT
);
CREATE INDEX idx_completion_blockers_current ON completion_blockers(project_name, resolved, goal_id, category);

CREATE TABLE goal_status_history (
    id          TEXT PRIMARY KEY,
    goal_id     TEXT NOT NULL REFERENCES project_goals(id),
    from_status TEXT NOT NULL,
    to_status   TEXT NOT NULL,
    reason      TEXT NOT NULL,
    source_type TEXT NOT NULL DEFAULT '',
    source_id   TEXT NOT NULL DEFAULT '',
    session_id  TEXT,
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_goal_status_history_goal ON goal_status_history(goal_id, created_at, id);

CREATE TABLE project_status_history (
    id           TEXT PRIMARY KEY,
    project_name TEXT NOT NULL REFERENCES project_state(project_name),
    from_status  TEXT NOT NULL,
    to_status    TEXT NOT NULL,
    reason       TEXT NOT NULL,
    source_type  TEXT NOT NULL DEFAULT '',
    source_id    TEXT NOT NULL DEFAULT '',
    session_id   TEXT,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_project_status_history_project ON project_status_history(project_name, created_at, id);
