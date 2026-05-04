-- +goose Up
-- Placeholder: future schema deltas land here as numbered goose migrations.
-- The baseline (00001_baseline.go) is registered programmatically and uses
-- GORM AutoMigrate to materialize all tables, so this file is intentionally
-- a no-op. SELECT 1 is portable across SQLite, Postgres, and MySQL.
SELECT 1;

-- +goose Down
SELECT 1;
