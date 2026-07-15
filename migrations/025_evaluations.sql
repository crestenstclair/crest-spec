-- Phase 7: host-driven evaluations and evidence-gated reusable improvement.
-- Evaluation state references the immutable context/execution/validation
-- chain and stores canonical payloads through the existing content blob table.

CREATE TABLE evaluation_case_assessments (
    source_attempt_id  TEXT PRIMARY KEY REFERENCES generation_attempts(id),
    eligible           INTEGER NOT NULL CHECK (eligible IN (0,1)),
    reason             TEXT NOT NULL,
    case_id            TEXT REFERENCES evaluation_cases(id),
    assessed_at        TEXT NOT NULL
);

CREATE TABLE evaluation_cases (
    id                         TEXT PRIMARY KEY,
    identity_hash              TEXT NOT NULL UNIQUE,
    provenance                 TEXT NOT NULL CHECK (provenance IN ('historical','curated','imported')),
    source_attempt_id          TEXT REFERENCES generation_attempts(id),
    project_name               TEXT NOT NULL,
    goal_id                    TEXT NOT NULL DEFAULT '',
    capability_id              TEXT NOT NULL DEFAULT '',
    resource_id                TEXT NOT NULL,
    spec_hash                  TEXT NOT NULL,
    repository_hash            TEXT NOT NULL,
    resource_declaration_hash  TEXT NOT NULL,
    plan_operation_id          TEXT NOT NULL DEFAULT '',
    context_manifest_id        TEXT REFERENCES context_manifests(id),
    execution_id               TEXT REFERENCES execution_manifests(id),
    candidate_id               TEXT REFERENCES candidate_sets(id),
    payload_blob               TEXT NOT NULL REFERENCES content_blobs(hash),
    expected_outcome_blob      TEXT NOT NULL REFERENCES content_blobs(hash),
    created_at                 TEXT NOT NULL,
    CHECK (provenance != 'historical' OR source_attempt_id IS NOT NULL),
    UNIQUE (source_attempt_id)
);
CREATE INDEX idx_evaluation_cases_provenance ON evaluation_cases(provenance, created_at, id);
CREATE INDEX idx_evaluation_cases_resource ON evaluation_cases(resource_id, created_at, id);

CREATE TABLE evaluation_datasets (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    identity_hash  TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL CHECK (status IN ('draft','sealed','archived')),
    created_at     TEXT NOT NULL,
    sealed_at      TEXT
);
CREATE INDEX idx_evaluation_datasets_status ON evaluation_datasets(status, created_at, id);

CREATE TABLE evaluation_dataset_cases (
    dataset_id  TEXT NOT NULL REFERENCES evaluation_datasets(id) ON DELETE CASCADE,
    case_id     TEXT NOT NULL REFERENCES evaluation_cases(id),
    split       TEXT NOT NULL CHECK (split IN ('training','development','held_out')),
    ordinal     INTEGER NOT NULL CHECK (ordinal >= 0),
    PRIMARY KEY (dataset_id, case_id),
    UNIQUE (dataset_id, ordinal)
);
CREATE INDEX idx_evaluation_dataset_cases_split ON evaluation_dataset_cases(dataset_id, split, ordinal);

CREATE TABLE evaluation_configurations (
    id                       TEXT PRIMARY KEY,
    name                     TEXT NOT NULL,
    identity_hash            TEXT NOT NULL UNIQUE,
    planner_version          TEXT NOT NULL,
    planner_policy_hash      TEXT NOT NULL,
    context_selector_version TEXT NOT NULL,
    context_budget_tokens    INTEGER NOT NULL CHECK (context_budget_tokens > 0),
    template_hashes_json     TEXT NOT NULL,
    role_policy_version      TEXT NOT NULL,
    host_name                TEXT NOT NULL,
    host_version             TEXT NOT NULL DEFAULT '',
    provider                 TEXT NOT NULL DEFAULT '',
    model                    TEXT NOT NULL,
    inference_config_json    TEXT NOT NULL,
    validation_version       TEXT NOT NULL,
    learning_ids_json        TEXT NOT NULL,
    payload_blob             TEXT NOT NULL REFERENCES content_blobs(hash),
    created_at               TEXT NOT NULL
);
CREATE INDEX idx_evaluation_configurations_model ON evaluation_configurations(host_name, model, created_at, id);

CREATE TABLE evaluation_runs (
    id                           TEXT PRIMARY KEY,
    dataset_id                   TEXT NOT NULL REFERENCES evaluation_datasets(id),
    name                         TEXT NOT NULL,
    status                       TEXT NOT NULL CHECK (status IN ('draft','running','completed','cancelled')),
    metric_policy_json           TEXT NOT NULL,
    metric_policy_hash           TEXT NOT NULL,
    minimum_sample_size          INTEGER NOT NULL CHECK (minimum_sample_size > 0),
    practical_significance       REAL NOT NULL CHECK (practical_significance >= 0),
    require_held_out             INTEGER NOT NULL CHECK (require_held_out IN (0,1)),
    conclusion                   TEXT NOT NULL CHECK (conclusion IN ('pending','inconclusive','baseline_wins','candidate_wins','no_material_change','cancelled')),
    winning_variant              TEXT NOT NULL DEFAULT '',
    conclusion_reason            TEXT NOT NULL DEFAULT '',
    created_at                   TEXT NOT NULL,
    started_at                   TEXT,
    completed_at                 TEXT
);
CREATE INDEX idx_evaluation_runs_status ON evaluation_runs(status, created_at, id);

CREATE TABLE evaluation_run_variants (
    run_id            TEXT NOT NULL REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    variant_name      TEXT NOT NULL,
    configuration_id  TEXT NOT NULL REFERENCES evaluation_configurations(id),
    is_baseline       INTEGER NOT NULL CHECK (is_baseline IN (0,1)),
    ordinal           INTEGER NOT NULL CHECK (ordinal >= 0),
    PRIMARY KEY (run_id, variant_name),
    UNIQUE (run_id, configuration_id),
    UNIQUE (run_id, ordinal)
);
CREATE UNIQUE INDEX idx_evaluation_run_one_baseline
ON evaluation_run_variants(run_id) WHERE is_baseline = 1;

CREATE TABLE evaluation_assignments (
    id                    TEXT PRIMARY KEY,
    run_id                TEXT NOT NULL REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    case_id               TEXT NOT NULL REFERENCES evaluation_cases(id),
    variant_name          TEXT NOT NULL,
    configuration_id      TEXT NOT NULL REFERENCES evaluation_configurations(id),
    split                 TEXT NOT NULL CHECK (split IN ('training','development','held_out')),
    status                TEXT NOT NULL CHECK (status IN ('pending','leased','submitted','cancelled')),
    current_lease_id      TEXT REFERENCES evaluation_assignment_leases(id),
    lease_owner           TEXT NOT NULL DEFAULT '',
    lease_expires_at      TEXT,
    attempt_id            TEXT REFERENCES generation_attempts(id),
    terminal_status       TEXT NOT NULL DEFAULT '',
    terminal_reason       TEXT NOT NULL DEFAULT '',
    submitted_at          TEXT,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    FOREIGN KEY (run_id, variant_name) REFERENCES evaluation_run_variants(run_id, variant_name),
    UNIQUE (run_id, case_id, variant_name)
);
CREATE INDEX idx_evaluation_assignments_claim ON evaluation_assignments(run_id, status, lease_expires_at, split, id);
CREATE INDEX idx_evaluation_assignments_attempt ON evaluation_assignments(attempt_id);

CREATE TABLE evaluation_assignment_leases (
    id             TEXT PRIMARY KEY,
    assignment_id  TEXT NOT NULL REFERENCES evaluation_assignments(id) ON DELETE CASCADE,
    lease_owner    TEXT NOT NULL,
    lease_token_hash TEXT NOT NULL,
    lease_number   INTEGER NOT NULL CHECK (lease_number > 0),
    status         TEXT NOT NULL CHECK (status IN ('active','released','expired','submitted')),
    claimed_at     TEXT NOT NULL,
    expires_at     TEXT NOT NULL,
    completed_at   TEXT,
    UNIQUE (assignment_id, lease_number)
);
CREATE INDEX idx_evaluation_assignment_leases_active ON evaluation_assignment_leases(status, expires_at, id);

CREATE TABLE evaluation_metric_observations (
    assignment_id  TEXT NOT NULL REFERENCES evaluation_assignments(id) ON DELETE CASCADE,
    metric_name    TEXT NOT NULL,
    value          REAL,
    missing_reason TEXT NOT NULL DEFAULT '',
    unit           TEXT NOT NULL DEFAULT '',
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL DEFAULT '',
    metadata_json  TEXT NOT NULL DEFAULT '{}',
    created_at     TEXT NOT NULL,
    PRIMARY KEY (assignment_id, metric_name),
    CHECK (value IS NOT NULL OR missing_reason != '')
);
CREATE INDEX idx_evaluation_metric_observations_name ON evaluation_metric_observations(metric_name, assignment_id);

CREATE TABLE evaluation_metric_aggregates (
    run_id          TEXT NOT NULL REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    variant_name    TEXT NOT NULL,
    split           TEXT NOT NULL CHECK (split IN ('all','training','development','held_out')),
    metric_name     TEXT NOT NULL,
    sample_count    INTEGER NOT NULL CHECK (sample_count >= 0),
    missing_count   INTEGER NOT NULL CHECK (missing_count >= 0),
    mean_value      REAL,
    minimum_value   REAL,
    maximum_value   REAL,
    created_at      TEXT NOT NULL,
    PRIMARY KEY (run_id, variant_name, split, metric_name),
    FOREIGN KEY (run_id, variant_name) REFERENCES evaluation_run_variants(run_id, variant_name)
);

CREATE TABLE evaluation_comparisons (
    run_id                 TEXT NOT NULL REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    baseline_variant       TEXT NOT NULL,
    candidate_variant      TEXT NOT NULL,
    split                  TEXT NOT NULL CHECK (split IN ('all','training','development','held_out')),
    metric_name            TEXT NOT NULL,
    baseline_sample_count  INTEGER NOT NULL CHECK (baseline_sample_count >= 0),
    candidate_sample_count INTEGER NOT NULL CHECK (candidate_sample_count >= 0),
    missing_count          INTEGER NOT NULL CHECK (missing_count >= 0),
    baseline_value         REAL,
    candidate_value        REAL,
    absolute_change        REAL,
    relative_change        REAL,
    practical_threshold    REAL NOT NULL CHECK (practical_threshold >= 0),
    conclusion             TEXT NOT NULL CHECK (conclusion IN ('inconclusive','baseline_better','candidate_better','no_material_change')),
    regression             INTEGER NOT NULL CHECK (regression IN (0,1)),
    reason                 TEXT NOT NULL,
    created_at             TEXT NOT NULL,
    PRIMARY KEY (run_id, candidate_variant, split, metric_name),
    FOREIGN KEY (run_id, baseline_variant) REFERENCES evaluation_run_variants(run_id, variant_name),
    FOREIGN KEY (run_id, candidate_variant) REFERENCES evaluation_run_variants(run_id, variant_name)
);

CREATE TABLE evaluation_promotion_proposals (
    id                    TEXT PRIMARY KEY,
    run_id                TEXT NOT NULL REFERENCES evaluation_runs(id),
    configuration_id      TEXT NOT NULL REFERENCES evaluation_configurations(id),
    variant_name          TEXT NOT NULL,
    change_kind           TEXT NOT NULL CHECK (change_kind IN ('learning','template','context_selector','planner','role_policy')),
    target_identity       TEXT NOT NULL,
    change_blob           TEXT NOT NULL REFERENCES content_blobs(hash),
    rollback_identity     TEXT NOT NULL,
    status                TEXT NOT NULL CHECK (status IN ('proposed','eligible','approved','rejected','applied','rolled_back')),
    eligibility_reason    TEXT NOT NULL,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    FOREIGN KEY (run_id, variant_name) REFERENCES evaluation_run_variants(run_id, variant_name)
);
CREATE INDEX idx_evaluation_promotions_status ON evaluation_promotion_proposals(status, created_at, id);

CREATE TABLE evaluation_promotion_decisions (
    id           TEXT PRIMARY KEY,
    proposal_id  TEXT NOT NULL REFERENCES evaluation_promotion_proposals(id) ON DELETE CASCADE,
    decision     TEXT NOT NULL CHECK (decision IN ('approved','rejected','applied','rolled_back')),
    actor        TEXT NOT NULL,
    reason       TEXT NOT NULL,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_evaluation_promotion_decisions ON evaluation_promotion_decisions(proposal_id, created_at, id);
