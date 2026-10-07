-- +goose Up
CREATE TABLE templates (
    id              TEXT PRIMARY KEY,
    slim_release    TEXT NOT NULL DEFAULT '',
    runner_version  TEXT NOT NULL DEFAULT '',
    layer_version   TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL,
    vmid            INTEGER NOT NULL DEFAULT 0,
    runtime_ref     TEXT NOT NULL DEFAULT '',
    volume          TEXT NOT NULL DEFAULT '',
    archive_sha256  TEXT NOT NULL DEFAULT '',
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    pinned          INTEGER NOT NULL DEFAULT 0,
    trigger         TEXT NOT NULL DEFAULT '',
    build_env_id    TEXT NOT NULL DEFAULT '',
    verify_env_id   TEXT NOT NULL DEFAULT '',
    failure_stage   TEXT NOT NULL DEFAULT '',
    failure_reason  TEXT NOT NULL DEFAULT '',
    report          TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    activated_at    INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX templates_one_active ON templates (state) WHERE state = 'active';

ALTER TABLE environments ADD COLUMN kind TEXT NOT NULL DEFAULT 'job';
ALTER TABLE environments ADD COLUMN template_vmid INTEGER NOT NULL DEFAULT 0;
CREATE INDEX environments_kind ON environments (kind);

-- +goose Down
DROP INDEX environments_kind;
ALTER TABLE environments DROP COLUMN template_vmid;
ALTER TABLE environments DROP COLUMN kind;
DROP TABLE templates;
