-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS lyrics_song_artist_idx ON lyrics ((base_key COLLATE "C")) WHERE provider IN ('qq', 'kugou');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS lyrics_song_artist_idx;
