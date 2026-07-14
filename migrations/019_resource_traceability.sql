-- Phase 2: intent-to-DDD traceability and structured boundary/artifact profiles.
-- resource_id intentionally does not reference resources(id): declarations are
-- materialized before a newly planned resource has a settled implementation.

CREATE TABLE resource_capability_contributions (
    project_name  TEXT NOT NULL REFERENCES project_state(project_name),
    resource_id   TEXT NOT NULL,
    resource_kind TEXT NOT NULL,
    capability_id TEXT NOT NULL REFERENCES project_capabilities(id),
    contribution  TEXT NOT NULL,
    spec_hash     TEXT NOT NULL,
    active        INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    updated_at    TEXT NOT NULL,
    PRIMARY KEY (resource_id, capability_id)
);
CREATE INDEX idx_contributions_capability ON resource_capability_contributions(project_name, capability_id, active, resource_id);

CREATE TABLE resource_boundary_profiles (
    resource_id   TEXT PRIMARY KEY,
    project_name  TEXT NOT NULL REFERENCES project_state(project_name),
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('port','adapter')),
    direction     TEXT NOT NULL DEFAULT '' CHECK (direction IN ('','inbound','outbound')),
    profile_kind  TEXT NOT NULL DEFAULT '',
    method        TEXT NOT NULL DEFAULT '',
    path          TEXT NOT NULL DEFAULT '',
    protocol      TEXT NOT NULL DEFAULT '',
    topology      TEXT NOT NULL DEFAULT '',
    device        TEXT NOT NULL DEFAULT '',
    medium        TEXT NOT NULL DEFAULT '',
    system_name   TEXT NOT NULL DEFAULT '',
    topic         TEXT NOT NULL DEFAULT '',
    trigger_name  TEXT NOT NULL DEFAULT '',
    spec_hash     TEXT NOT NULL,
    active        INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    updated_at    TEXT NOT NULL
);
CREATE INDEX idx_boundary_profiles_project ON resource_boundary_profiles(project_name, active, resource_kind, profile_kind);

CREATE TABLE resource_boundary_profile_items (
    resource_id TEXT NOT NULL REFERENCES resource_boundary_profiles(resource_id),
    item_kind   TEXT NOT NULL CHECK (item_kind IN ('surface','accessibility')),
    ordinal     INTEGER NOT NULL,
    value       TEXT NOT NULL,
    PRIMARY KEY (resource_id, item_kind, ordinal)
);

CREATE TABLE resource_asset_profiles (
    resource_id      TEXT PRIMARY KEY,
    project_name     TEXT NOT NULL REFERENCES project_state(project_name),
    profile_kind     TEXT NOT NULL,
    ecosystem        TEXT NOT NULL DEFAULT '',
    witness          TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL DEFAULT '',
    secret_policy    TEXT NOT NULL DEFAULT '',
    failure_policy   TEXT NOT NULL DEFAULT '',
    constraint_text  TEXT NOT NULL DEFAULT '',
    audience         TEXT NOT NULL DEFAULT '',
    predecessor      TEXT NOT NULL DEFAULT '',
    compatibility    TEXT NOT NULL DEFAULT '',
    rollback         TEXT NOT NULL DEFAULT '',
    spec_hash        TEXT NOT NULL,
    active           INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    updated_at       TEXT NOT NULL
);
CREATE INDEX idx_asset_profiles_project ON resource_asset_profiles(project_name, active, profile_kind);

CREATE TABLE resource_asset_profile_items (
    resource_id TEXT NOT NULL REFERENCES resource_asset_profiles(resource_id),
    item_kind   TEXT NOT NULL CHECK (item_kind IN ('signal','required_example')),
    ordinal     INTEGER NOT NULL,
    value       TEXT NOT NULL,
    PRIMARY KEY (resource_id, item_kind, ordinal)
);
