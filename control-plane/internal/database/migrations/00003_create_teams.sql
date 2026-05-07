-- +goose Up
-- Schema for the teams feature: a Team groups instances and users; users
-- belong to teams via team_members with a per-team role; team_providers
-- whitelists which global LLM providers a team's instances may use.
--
-- The companion column instances.team_id is added by 00004_seed_teams.go
-- via GORM AutoMigrate, which is idempotent across fresh installs (where
-- the baseline already added it from the current Instance model) and
-- upgrades (where the baseline is stamped and the column is missing).
-- Data backfill (seeding the Default team and mirroring legacy
-- UserInstance grants) also lives in 00004_seed_teams.go.
--
-- Targeted at SQLite (primary deployment driver). Postgres/MySQL would
-- need dialect-specific autoincrement/boolean syntax — add a sibling
-- migration file when those drivers ship.

CREATE TABLE IF NOT EXISTS teams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(100) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_teams_name ON teams(name);

CREATE TABLE IF NOT EXISTS team_members (
    team_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'user',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE IF NOT EXISTS team_providers (
    team_id INTEGER NOT NULL,
    provider_id INTEGER NOT NULL,
    PRIMARY KEY (team_id, provider_id)
);

-- +goose Down
DROP TABLE IF EXISTS team_providers;
DROP TABLE IF EXISTS team_members;
DROP INDEX IF EXISTS idx_teams_name;
DROP TABLE IF EXISTS teams;
