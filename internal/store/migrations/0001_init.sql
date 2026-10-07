-- +goose Up
CREATE TABLE scale_sets (
    name       TEXT PRIMARY KEY,
    github_id  INTEGER NOT NULL DEFAULT 0,
    url        TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);

CREATE TABLE environments (
    id               TEXT PRIMARY KEY,
    scale_set        TEXT NOT NULL,
    state            TEXT NOT NULL,
    runtime_ref      TEXT NOT NULL DEFAULT '',
    runner_name      TEXT NOT NULL DEFAULT '',
    runner_id        INTEGER NOT NULL DEFAULT 0,
    ip               TEXT NOT NULL DEFAULT '',
    token_hash       TEXT NOT NULL DEFAULT '',
    failure_stage    TEXT NOT NULL DEFAULT '',
    failure_reason   TEXT NOT NULL DEFAULT '',
    job_id           TEXT NOT NULL DEFAULT '',
    exit_code        INTEGER,
    memory_mb        INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    state_changed_at INTEGER NOT NULL
);
CREATE INDEX environments_state ON environments (state);
CREATE INDEX environments_token ON environments (token_hash);
CREATE INDEX environments_runner ON environments (runner_name);

CREATE TABLE jobs (
    id             TEXT PRIMARY KEY,
    scale_set      TEXT NOT NULL DEFAULT '',
    repository     TEXT NOT NULL DEFAULT '',
    owner          TEXT NOT NULL DEFAULT '',
    workflow_ref   TEXT NOT NULL DEFAULT '',
    display_name   TEXT NOT NULL DEFAULT '',
    event_name     TEXT NOT NULL DEFAULT '',
    run_id         INTEGER NOT NULL DEFAULT 0,
    runner_name    TEXT NOT NULL DEFAULT '',
    environment_id TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT '',
    result         TEXT NOT NULL DEFAULT '',
    queued_at      INTEGER NOT NULL DEFAULT 0,
    started_at     INTEGER NOT NULL DEFAULT 0,
    finished_at    INTEGER NOT NULL DEFAULT 0,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX jobs_updated ON jobs (updated_at);

CREATE TABLE events (
    seq            INTEGER PRIMARY KEY AUTOINCREMENT,
    ts             INTEGER NOT NULL,
    kind           TEXT NOT NULL,
    level          TEXT NOT NULL,
    message        TEXT NOT NULL,
    scale_set      TEXT NOT NULL DEFAULT '',
    environment_id TEXT NOT NULL DEFAULT '',
    job_id         TEXT NOT NULL DEFAULT '',
    data           TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX events_environment ON events (environment_id, seq);
CREATE INDEX events_job ON events (job_id, seq);

CREATE TABLE log_streams (
    environment_id TEXT NOT NULL,
    stream         TEXT NOT NULL,
    path           TEXT NOT NULL,
    last_seq       INTEGER NOT NULL DEFAULT 0,
    bytes          INTEGER NOT NULL DEFAULT 0,
    lines          INTEGER NOT NULL DEFAULT 0,
    first_at       INTEGER NOT NULL DEFAULT 0,
    last_at        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (environment_id, stream)
);

-- +goose Down
DROP TABLE log_streams;
DROP TABLE events;
DROP TABLE jobs;
DROP TABLE environments;
DROP TABLE scale_sets;
