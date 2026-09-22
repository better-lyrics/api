-- +goose Up
ALTER TABLE lyrics ADD COLUMN timing_type TEXT NOT NULL DEFAULT '';
ALTER TABLE lyrics ADD COLUMN apple_etag TEXT NOT NULL DEFAULT '';
ALTER TABLE lyrics ADD COLUMN last_checked_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE lyrics DROP COLUMN timing_type;
ALTER TABLE lyrics DROP COLUMN apple_etag;
ALTER TABLE lyrics DROP COLUMN last_checked_at;
