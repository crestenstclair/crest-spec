-- Phase 1: completion explanations accompany every current project state.
-- Transition history was introduced in 017; this column makes the current
-- reason directly inspectable without replaying that history.

ALTER TABLE project_state
ADD COLUMN completion_reason TEXT NOT NULL
DEFAULT 'required goals are declared but not yet planned';

UPDATE project_goals
SET status_reason = 'goal is declared but no active implementation plan targets it'
WHERE status_reason = '';
