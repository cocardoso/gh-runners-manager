-- +goose Up
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE secrets (name TEXT PRIMARY KEY, sealed BLOB NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  password_changed_at INTEGER NOT NULL
);
CREATE TABLE sessions (
  id_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  remote_addr TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE credentials (
  name TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE scale_set_configs (
  name TEXT PRIMARY KEY,
  spec TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE scale_set_configs;
DROP TABLE credentials;
DROP TABLE sessions;
DROP TABLE users;
DROP TABLE secrets;
DROP TABLE meta;
