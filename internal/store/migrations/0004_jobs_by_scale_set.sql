-- +goose Up
-- The repositories view groups jobs by scale set, newest first, on every refresh.
CREATE INDEX jobs_scale_set_updated ON jobs (scale_set, updated_at);

-- +goose Down
DROP INDEX jobs_scale_set_updated;
