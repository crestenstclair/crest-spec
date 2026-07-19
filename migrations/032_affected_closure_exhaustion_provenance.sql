-- Link the exact affected-closure failure that exhausted repair routing to the
-- session-resource projection.  The link is written atomically by new routing
-- code and is the only integration_error provenance eligible for handoffless
-- candidate reinstatement.
ALTER TABLE session_resources
    ADD COLUMN exhaustion_failure_id TEXT REFERENCES failure_classifications(id);

CREATE INDEX idx_session_resources_exhaustion_failure
    ON session_resources(exhaustion_failure_id);

-- Compatibility for active sessions created before this projection existed.
-- Backfill only the narrow state left by the old exhaustion + spec/resolve
-- workflow.  In particular, the current failure must be the sole unresolved
-- affected-closure failure, belong to the latest accepted attempt, have no
-- handoff, and have no newer executed/candidate attempt.  Ambiguous histories
-- intentionally retain NULL and remain blocked.
UPDATE session_resources AS resource
SET exhaustion_failure_id = (
    SELECT failure.id
    FROM failure_classifications AS failure
    JOIN generation_attempts AS attempt ON attempt.id = failure.attempt_id
    JOIN execution_manifests AS execution ON execution.attempt_id = attempt.id
    JOIN candidate_sets AS candidate ON candidate.attempt_id = attempt.id
    WHERE attempt.session_id = resource.session_id
      AND attempt.resource_id = resource.resource_id
      AND attempt.status = 'accepted'
      AND execution.status = 'accepted'
      AND candidate.status = 'accepted'
      AND failure.resource_id = resource.resource_id
      AND failure.category = 'integration_error'
      AND failure.origin = 'engine'
      AND failure.evidence_source = 'affected_closure_validation'
      AND failure.resolved_at IS NULL
      AND NOT EXISTS (
          SELECT 1
          FROM attempt_handoffs AS handoff
          WHERE handoff.failure_id = failure.id
      )
      AND attempt.retry_number = (
          SELECT MAX(accepted.retry_number)
          FROM generation_attempts AS accepted
          WHERE accepted.session_id = resource.session_id
            AND accepted.resource_id = resource.resource_id
            AND accepted.status = 'accepted'
      )
      AND NOT EXISTS (
          SELECT 1
          FROM failure_classifications AS later_failure
          JOIN generation_attempts AS later_failure_attempt
            ON later_failure_attempt.id = later_failure.attempt_id
          WHERE later_failure_attempt.session_id = attempt.session_id
            AND later_failure_attempt.resource_id = attempt.resource_id
            AND later_failure.resource_id = failure.resource_id
            AND later_failure.origin = 'engine'
            AND later_failure.evidence_source = 'affected_closure_validation'
            AND (
                later_failure.created_at > failure.created_at
                OR (
                    later_failure.created_at = failure.created_at
                    AND later_failure.id > failure.id
                )
            )
      )
      AND NOT EXISTS (
          SELECT 1
          FROM generation_attempts AS newer
          WHERE newer.session_id = attempt.session_id
            AND newer.resource_id = attempt.resource_id
            AND newer.retry_number > attempt.retry_number
            AND (
                newer.status != 'abandoned'
                OR EXISTS (
                    SELECT 1 FROM execution_manifests AS newer_execution
                    WHERE newer_execution.attempt_id = newer.id
                )
                OR EXISTS (
                    SELECT 1 FROM candidate_sets AS newer_candidate
                    WHERE newer_candidate.attempt_id = newer.id
                )
            )
      )
      AND NOT EXISTS (
          SELECT 1
          FROM failure_classifications AS competing_failure
          JOIN generation_attempts AS competing_attempt
            ON competing_attempt.id = competing_failure.attempt_id
          WHERE competing_attempt.session_id = attempt.session_id
            AND competing_attempt.resource_id = attempt.resource_id
            AND competing_failure.resource_id = failure.resource_id
            AND competing_failure.category = 'integration_error'
            AND competing_failure.origin = 'engine'
            AND competing_failure.evidence_source = 'affected_closure_validation'
            AND competing_failure.resolved_at IS NULL
            AND competing_failure.id != failure.id
      )
    ORDER BY failure.created_at DESC, failure.id DESC
    LIMIT 1
)
WHERE resource.exhaustion_failure_id IS NULL
  AND resource.phase = 'integration_failed'
  AND resource.execution_strategy = 'generate'
  AND EXISTS (
      SELECT 1 FROM agent_sessions AS session
      WHERE session.id = resource.session_id AND session.status = 'active'
  )
  AND (
      (
          resource.state = 'errored'
          AND COALESCE(resource.last_error, '') LIKE 'affected-closure repair retries exhausted:%'
      )
      OR (
          resource.state = 'pending'
          AND COALESCE(resource.last_error, '') = ''
          AND resource.attempts >= resource.max_retries
      )
  )
  -- Legacy routing exhausted only after its affected-closure history exceeded
  -- the resource cap. Count every engine-owned affected-closure
  -- classification here, including the legacy invalid-validation cycle and
  -- cycles resolved by accepted retries; COUNT == max_retries is not enough.
  AND (
      SELECT COUNT(*)
      FROM failure_classifications AS cycle_failure
      JOIN generation_attempts AS cycle_attempt ON cycle_attempt.id = cycle_failure.attempt_id
      WHERE cycle_attempt.session_id = resource.session_id
        AND cycle_attempt.resource_id = resource.resource_id
        AND cycle_failure.resource_id = resource.resource_id
        AND cycle_failure.origin = 'engine'
        AND cycle_failure.evidence_source = 'affected_closure_validation'
  ) > resource.max_retries;
