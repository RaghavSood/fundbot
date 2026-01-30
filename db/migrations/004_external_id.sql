-- +goose Up
ALTER TABLE topups ADD COLUMN external_id TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN easily, leave as-is
