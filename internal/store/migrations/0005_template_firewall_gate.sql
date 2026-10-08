-- +goose Up
-- Whether a template's agent waits for the job network's firewall, as its verification proved.
ALTER TABLE templates ADD COLUMN firewall_gate INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE templates DROP COLUMN firewall_gate;
