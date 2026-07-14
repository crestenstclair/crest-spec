-- name: UpsertProjectState :exec
INSERT INTO project_state (
    project_name, mission, spec_hash, completion_status, active, created_at, updated_at
) VALUES (?, ?, ?, 'declared', 1, ?, ?)
ON CONFLICT(project_name) DO UPDATE SET
    mission = excluded.mission,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: GetProjectState :one
SELECT * FROM project_state WHERE project_name = ?;

-- name: DeactivateProjectActors :exec
UPDATE project_actors SET active = 0, updated_at = ? WHERE project_name = ?;

-- name: UpsertProjectActor :exec
INSERT INTO project_actors (id, project_name, description, spec_hash, active, updated_at)
VALUES (?, ?, ?, ?, 1, ?)
ON CONFLICT(id) DO UPDATE SET
    project_name = excluded.project_name,
    description = excluded.description,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: ListProjectActors :many
SELECT * FROM project_actors
WHERE project_name = ? AND active = 1 ORDER BY id;

-- name: DeactivateProjectGoals :exec
UPDATE project_goals SET active = 0, updated_at = ? WHERE project_name = ?;

-- name: UpsertProjectGoal :exec
INSERT INTO project_goals (
    id, project_name, description, priority, status, status_reason,
    spec_hash, active, created_at, updated_at
) VALUES (?, ?, ?, ?, 'declared',
    'goal is declared but no active implementation plan targets it',
    ?, 1, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    project_name = excluded.project_name,
    description = excluded.description,
    priority = excluded.priority,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: GetProjectGoal :one
SELECT * FROM project_goals WHERE id = ?;

-- name: UpdateProjectGoalStatus :exec
UPDATE project_goals
SET status = ?, status_reason = ?, updated_at = ?
WHERE id = ? AND active = 1;

-- name: InsertGoalStatusHistory :exec
INSERT INTO goal_status_history (
    id, goal_id, from_status, to_status, reason,
    source_type, source_id, session_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListGoalStatusHistory :many
SELECT * FROM goal_status_history WHERE goal_id = ? ORDER BY created_at, id;

-- name: ListProjectGoals :many
SELECT * FROM project_goals
WHERE project_name = ? AND active = 1
ORDER BY CASE priority WHEN 'required' THEN 0 ELSE 1 END, id;

-- name: DeactivateProjectCapabilities :exec
UPDATE project_capabilities SET active = 0, updated_at = ? WHERE project_name = ?;

-- name: UpsertProjectCapability :exec
INSERT INTO project_capabilities (id, project_name, description, spec_hash, active, updated_at)
VALUES (?, ?, ?, ?, 1, ?)
ON CONFLICT(id) DO UPDATE SET
    project_name = excluded.project_name,
    description = excluded.description,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: ListProjectCapabilities :many
SELECT * FROM project_capabilities
WHERE project_name = ? AND active = 1 ORDER BY id;

-- name: DeactivateProjectRequirements :exec
UPDATE project_requirements SET active = 0, updated_at = ? WHERE project_name = ?;

-- name: UpsertProjectRequirement :exec
INSERT INTO project_requirements (id, project_name, kind, description, spec_hash, active, updated_at)
VALUES (?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(id) DO UPDATE SET
    project_name = excluded.project_name,
    kind = excluded.kind,
    description = excluded.description,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: ListProjectRequirements :many
SELECT * FROM project_requirements
WHERE project_name = ? AND active = 1 ORDER BY kind, id;

-- name: DeactivateAcceptanceScenarios :exec
UPDATE acceptance_scenarios SET active = 0, updated_at = ?
WHERE capability_id IN (
    SELECT id FROM project_capabilities WHERE project_name = ?
);

-- name: UpsertAcceptanceScenario :exec
INSERT INTO acceptance_scenarios (
    id, capability_id, actor_id, description, ordinal, spec_hash, active, updated_at
) VALUES (?, ?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(id) DO UPDATE SET
    capability_id = excluded.capability_id,
    actor_id = excluded.actor_id,
    description = excluded.description,
    ordinal = excluded.ordinal,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: ListAcceptanceScenarios :many
SELECT acceptance_scenarios.* FROM acceptance_scenarios
JOIN project_capabilities ON project_capabilities.id = acceptance_scenarios.capability_id
WHERE project_capabilities.project_name = ? AND acceptance_scenarios.active = 1
ORDER BY acceptance_scenarios.capability_id, acceptance_scenarios.ordinal, acceptance_scenarios.id;

-- name: DeactivateEvidenceRequirements :exec
UPDATE evidence_requirements SET active = 0, updated_at = ? WHERE project_name = ?;

-- name: UpsertEvidenceRequirement :exec
INSERT INTO evidence_requirements (id, project_name, kind, description, spec_hash, active, updated_at)
VALUES (?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(id) DO UPDATE SET
    project_name = excluded.project_name,
    kind = excluded.kind,
    description = excluded.description,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: ListEvidenceRequirements :many
SELECT * FROM evidence_requirements
WHERE project_name = ? AND active = 1 ORDER BY id;

-- name: DeactivateProjectNonGoals :exec
UPDATE project_non_goals SET active = 0, updated_at = ? WHERE project_name = ?;

-- name: UpsertProjectNonGoal :exec
INSERT INTO project_non_goals (id, project_name, description, spec_hash, active, updated_at)
VALUES (?, ?, ?, ?, 1, ?)
ON CONFLICT(id) DO UPDATE SET
    project_name = excluded.project_name,
    description = excluded.description,
    spec_hash = excluded.spec_hash,
    active = 1,
    updated_at = excluded.updated_at;

-- name: ListProjectNonGoals :many
SELECT * FROM project_non_goals
WHERE project_name = ? AND active = 1 ORDER BY id;

-- name: ClearGoalIntentRelationships :exec
DELETE FROM goal_actors WHERE goal_id IN (
    SELECT id FROM project_goals WHERE project_name = ?
);

-- name: ClearGoalDependencies :exec
DELETE FROM goal_dependencies WHERE goal_id IN (
    SELECT id FROM project_goals WHERE project_name = ?
);

-- name: ClearCapabilityGoals :exec
DELETE FROM capability_goals WHERE capability_id IN (
    SELECT id FROM project_capabilities WHERE project_name = ?
);

-- name: ClearRequirementGoals :exec
DELETE FROM requirement_goals WHERE requirement_id IN (
    SELECT id FROM project_requirements WHERE project_name = ?
);

-- name: ClearRequirementCapabilities :exec
DELETE FROM requirement_capabilities WHERE requirement_id IN (
    SELECT id FROM project_requirements WHERE project_name = ?
);

-- name: ClearAcceptanceSteps :exec
DELETE FROM acceptance_steps WHERE scenario_id IN (
    SELECT acceptance_scenarios.id FROM acceptance_scenarios
    JOIN project_capabilities ON project_capabilities.id = acceptance_scenarios.capability_id
    WHERE project_capabilities.project_name = ?
);

-- name: ClearAcceptanceEvidence :exec
DELETE FROM acceptance_evidence WHERE scenario_id IN (
    SELECT acceptance_scenarios.id FROM acceptance_scenarios
    JOIN project_capabilities ON project_capabilities.id = acceptance_scenarios.capability_id
    WHERE project_capabilities.project_name = ?
);

-- name: ClearProjectRequiredGoals :exec
DELETE FROM project_required_goals WHERE project_name = ?;

-- name: InsertGoalActor :exec
INSERT INTO goal_actors (goal_id, actor_id) VALUES (?, ?);

-- name: InsertGoalDependency :exec
INSERT INTO goal_dependencies (goal_id, dependency_goal_id) VALUES (?, ?);

-- name: InsertCapabilityGoal :exec
INSERT INTO capability_goals (capability_id, goal_id) VALUES (?, ?);

-- name: InsertRequirementGoal :exec
INSERT INTO requirement_goals (requirement_id, goal_id) VALUES (?, ?);

-- name: InsertRequirementCapability :exec
INSERT INTO requirement_capabilities (requirement_id, capability_id) VALUES (?, ?);

-- name: InsertAcceptanceStep :exec
INSERT INTO acceptance_steps (scenario_id, ordinal, action, observes) VALUES (?, ?, ?, ?);

-- name: InsertAcceptanceEvidence :exec
INSERT INTO acceptance_evidence (scenario_id, evidence_id) VALUES (?, ?);

-- name: InsertProjectRequiredGoal :exec
INSERT INTO project_required_goals (project_name, goal_id, ordinal) VALUES (?, ?, ?);

-- name: ListGoalDependencies :many
SELECT goal_dependencies.* FROM goal_dependencies
JOIN project_goals ON project_goals.id = goal_dependencies.goal_id
WHERE project_goals.project_name = ?
ORDER BY goal_dependencies.goal_id, goal_dependencies.dependency_goal_id;

-- name: ListCapabilityGoals :many
SELECT capability_goals.* FROM capability_goals
JOIN project_capabilities ON project_capabilities.id = capability_goals.capability_id
WHERE project_capabilities.project_name = ?
ORDER BY capability_goals.capability_id, capability_goals.goal_id;

-- name: ListProjectRequiredGoals :many
SELECT * FROM project_required_goals WHERE project_name = ? ORDER BY ordinal, goal_id;

-- name: UpdateProjectCompletionStatus :exec
UPDATE project_state
SET completion_status = ?, completion_reason = ?, updated_at = ?
WHERE project_name = ? AND active = 1;

-- name: InsertProjectStatusHistory :exec
INSERT INTO project_status_history (
    id, project_name, from_status, to_status, reason,
    source_type, source_id, session_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListProjectStatusHistory :many
SELECT * FROM project_status_history WHERE project_name = ? ORDER BY created_at, id;

-- name: ListCompletionBlockers :many
SELECT * FROM completion_blockers
WHERE project_name = ? AND resolved = 0
ORDER BY goal_id, category, created_at, id;

-- name: ListGoalActors :many
SELECT goal_actors.* FROM goal_actors
JOIN project_goals ON project_goals.id = goal_actors.goal_id
WHERE project_goals.project_name = ?
ORDER BY goal_actors.goal_id, goal_actors.actor_id;

-- name: ListRequirementGoals :many
SELECT requirement_goals.* FROM requirement_goals
JOIN project_requirements ON project_requirements.id = requirement_goals.requirement_id
WHERE project_requirements.project_name = ?
ORDER BY requirement_goals.requirement_id, requirement_goals.goal_id;

-- name: ListRequirementCapabilities :many
SELECT requirement_capabilities.* FROM requirement_capabilities
JOIN project_requirements ON project_requirements.id = requirement_capabilities.requirement_id
WHERE project_requirements.project_name = ?
ORDER BY requirement_capabilities.requirement_id, requirement_capabilities.capability_id;

-- name: ListAcceptanceSteps :many
SELECT acceptance_steps.* FROM acceptance_steps
JOIN acceptance_scenarios ON acceptance_scenarios.id = acceptance_steps.scenario_id
JOIN project_capabilities ON project_capabilities.id = acceptance_scenarios.capability_id
WHERE project_capabilities.project_name = ?
ORDER BY acceptance_steps.scenario_id, acceptance_steps.ordinal;

-- name: ListAcceptanceEvidence :many
SELECT acceptance_evidence.* FROM acceptance_evidence
JOIN acceptance_scenarios ON acceptance_scenarios.id = acceptance_evidence.scenario_id
JOIN project_capabilities ON project_capabilities.id = acceptance_scenarios.capability_id
WHERE project_capabilities.project_name = ?
ORDER BY acceptance_evidence.scenario_id, acceptance_evidence.evidence_id;
