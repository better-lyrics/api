-- +goose Up
ALTER TABLE lyrics ALTER COLUMN duration_sec TYPE BIGINT;
ALTER TABLE lyrics ALTER COLUMN track_duration_ms TYPE BIGINT;
ALTER TABLE negative_cache ALTER COLUMN duration_sec TYPE BIGINT;
ALTER TABLE song_metadata ALTER COLUMN duration_ms TYPE BIGINT;

-- +goose Down
ALTER TABLE song_metadata ALTER COLUMN duration_ms TYPE INTEGER;
ALTER TABLE negative_cache ALTER COLUMN duration_sec TYPE INTEGER;
ALTER TABLE lyrics ALTER COLUMN track_duration_ms TYPE INTEGER;
ALTER TABLE lyrics ALTER COLUMN duration_sec TYPE INTEGER;
