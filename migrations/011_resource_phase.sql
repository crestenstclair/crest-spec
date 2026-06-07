-- Add phase tracking and dispatch timestamp for wave observability.
ALTER TABLE session_resources ADD COLUMN phase TEXT NOT NULL DEFAULT '';
ALTER TABLE session_resources ADD COLUMN dispatched_at TEXT NOT NULL DEFAULT '';
