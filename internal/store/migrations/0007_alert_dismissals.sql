-- +goose Up
-- Overview alerts an operator dismissed: an alert stays hidden until it happens again after dismissed_at.
CREATE TABLE alert_dismissals (
    id           TEXT PRIMARY KEY,
    dismissed_at INTEGER NOT NULL
);
-- When an environment failed: updated_at moves on while a failed environment is destroyed.
ALTER TABLE environments ADD COLUMN failed_at INTEGER NOT NULL DEFAULT 0;
UPDATE environments SET failed_at = updated_at WHERE failure_stage != '';
CREATE INDEX environments_failed_at ON environments (failed_at) WHERE failed_at > 0;

-- +goose Down
DROP INDEX environments_failed_at;
ALTER TABLE environments DROP COLUMN failed_at;
DROP TABLE alert_dismissals;
