-- +goose Up
ALTER TABLE lyrics ADD COLUMN source TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE lyrics DROP COLUMN source;
