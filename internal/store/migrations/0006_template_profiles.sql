-- +goose Up
-- Template profiles: what a template preinstalls and leaves out of GitHub's recipe.
CREATE TABLE template_profiles (
    name       TEXT PRIMARY KEY,
    spec       TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
-- Each template version belongs to a profile; one version is active per profile.
ALTER TABLE templates ADD COLUMN profile TEXT NOT NULL DEFAULT 'default';
-- The profile as it was when the version was built (JSON): later edits do not change it.
ALTER TABLE templates ADD COLUMN profile_spec TEXT NOT NULL DEFAULT '';
DROP INDEX templates_one_active;
CREATE UNIQUE INDEX templates_one_active ON templates (profile) WHERE state = 'active';

-- +goose Down
UPDATE templates SET state = 'ready' WHERE state = 'active' AND profile <> 'default';
DROP INDEX templates_one_active;
CREATE UNIQUE INDEX templates_one_active ON templates (state) WHERE state = 'active';
ALTER TABLE templates DROP COLUMN profile_spec;
ALTER TABLE templates DROP COLUMN profile;
DROP TABLE template_profiles;
