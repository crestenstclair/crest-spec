-- name: DeactivateResourceContributions :exec
UPDATE resource_capability_contributions SET active = 0, updated_at = ? WHERE project_name = ?;

-- name: UpsertResourceContribution :exec
INSERT INTO resource_capability_contributions (
    project_name, resource_id, resource_kind, capability_id,
    contribution, spec_hash, active, updated_at
) VALUES (?, ?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(resource_id, capability_id) DO UPDATE SET
    project_name = excluded.project_name,
    resource_kind = excluded.resource_kind,
    contribution = excluded.contribution,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: ListResourceContributions :many
SELECT * FROM resource_capability_contributions
WHERE project_name = ? AND active = 1
ORDER BY capability_id, resource_id;

-- name: ListContributionsByCapability :many
SELECT * FROM resource_capability_contributions
WHERE capability_id = ? AND active = 1 ORDER BY resource_id;

-- name: ListContributionsByResource :many
SELECT * FROM resource_capability_contributions
WHERE resource_id = ? AND active = 1 ORDER BY capability_id;

-- name: ClearBoundaryProfileItems :exec
DELETE FROM resource_boundary_profile_items WHERE resource_id IN (
    SELECT resource_id FROM resource_boundary_profiles WHERE project_name = ?
);

-- name: DeactivateBoundaryProfiles :exec
UPDATE resource_boundary_profiles SET active = 0, updated_at = ? WHERE project_name = ?;

-- name: UpsertBoundaryProfile :exec
INSERT INTO resource_boundary_profiles (
    resource_id, project_name, resource_kind, direction, profile_kind,
    method, path, protocol, topology, device, medium, system_name, topic,
    trigger_name, spec_hash, active, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(resource_id) DO UPDATE SET
    project_name = excluded.project_name,
    resource_kind = excluded.resource_kind,
    direction = excluded.direction,
    profile_kind = excluded.profile_kind,
    method = excluded.method,
    path = excluded.path,
    protocol = excluded.protocol,
    topology = excluded.topology,
    device = excluded.device,
    medium = excluded.medium,
    system_name = excluded.system_name,
    topic = excluded.topic,
    trigger_name = excluded.trigger_name,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: InsertBoundaryProfileItem :exec
INSERT INTO resource_boundary_profile_items (resource_id, item_kind, ordinal, value) VALUES (?, ?, ?, ?);

-- name: ListBoundaryProfiles :many
SELECT * FROM resource_boundary_profiles WHERE project_name = ? AND active = 1 ORDER BY resource_id;

-- name: ListBoundaryProfileItems :many
SELECT resource_boundary_profile_items.* FROM resource_boundary_profile_items
JOIN resource_boundary_profiles USING (resource_id)
WHERE resource_boundary_profiles.project_name = ? AND resource_boundary_profiles.active = 1
ORDER BY resource_boundary_profile_items.resource_id, item_kind, ordinal;

-- name: ClearAssetProfileItems :exec
DELETE FROM resource_asset_profile_items WHERE resource_id IN (
    SELECT resource_id FROM resource_asset_profiles WHERE project_name = ?
);

-- name: DeactivateAssetProfiles :exec
UPDATE resource_asset_profiles SET active = 0, updated_at = ? WHERE project_name = ?;

-- name: UpsertAssetProfile :exec
INSERT INTO resource_asset_profiles (
    resource_id, project_name, profile_kind, ecosystem, witness, source,
    secret_policy, failure_policy, constraint_text, audience, predecessor,
    compatibility, rollback, spec_hash, active, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(resource_id) DO UPDATE SET
    project_name = excluded.project_name,
    profile_kind = excluded.profile_kind,
    ecosystem = excluded.ecosystem,
    witness = excluded.witness,
    source = excluded.source,
    secret_policy = excluded.secret_policy,
    failure_policy = excluded.failure_policy,
    constraint_text = excluded.constraint_text,
    audience = excluded.audience,
    predecessor = excluded.predecessor,
    compatibility = excluded.compatibility,
    rollback = excluded.rollback,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: InsertAssetProfileItem :exec
INSERT INTO resource_asset_profile_items (resource_id, item_kind, ordinal, value) VALUES (?, ?, ?, ?);

-- name: ListAssetProfiles :many
SELECT * FROM resource_asset_profiles WHERE project_name = ? AND active = 1 ORDER BY resource_id;

-- name: ListAssetProfileItems :many
SELECT resource_asset_profile_items.* FROM resource_asset_profile_items
JOIN resource_asset_profiles USING (resource_id)
WHERE resource_asset_profiles.project_name = ? AND resource_asset_profiles.active = 1
ORDER BY resource_asset_profile_items.resource_id, item_kind, ordinal;
