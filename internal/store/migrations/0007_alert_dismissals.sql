-- +goose Up
-- Overview alerts an operator dismissed: an alert stays hidden until it happens again after dismissed_at.
CREATE TABLE alert_dismissals (
    id           TEXT PRIMARY KEY,
    dismissed_at INTEGER NOT NULL
);
CREATE INDEX environments_failed ON environments (updated_at) WHERE failure_stage != '';

-- +goose Down
DROP INDEX environments_failed;
DROP TABLE alert_dismissals;
