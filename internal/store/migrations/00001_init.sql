-- +goose Up
CREATE TABLE lyrics (
    cache_key         TEXT PRIMARY KEY,
    provider          TEXT NOT NULL,
    song              TEXT NOT NULL,
    artist            TEXT NOT NULL,
    album             TEXT NOT NULL DEFAULT '',
    duration_sec      INTEGER,
    raw_lyrics        BYTEA NOT NULL,
    track_duration_ms INTEGER NOT NULL DEFAULT 0,
    score             DOUBLE PRECISION NOT NULL DEFAULT 0,
    language          TEXT NOT NULL DEFAULT '',
    is_rtl            BOOLEAN NOT NULL DEFAULT FALSE,
    format            TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX lyrics_tolerance_idx ON lyrics (provider, song, artist, album, duration_sec);

CREATE TABLE negative_cache (
    cache_key              TEXT PRIMARY KEY,
    provider               TEXT NOT NULL,
    song                   TEXT NOT NULL,
    artist                 TEXT NOT NULL,
    album                  TEXT NOT NULL DEFAULT '',
    duration_sec           INTEGER,
    reason                 TEXT NOT NULL,
    release_date           TEXT NOT NULL DEFAULT '',
    has_time_synced_known  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at             TIMESTAMPTZ NOT NULL
);
CREATE INDEX negative_cache_tolerance_idx ON negative_cache (provider, song, artist, album, duration_sec);
CREATE INDEX negative_cache_expires_idx ON negative_cache (expires_at);

CREATE TABLE song_metadata (
    cache_key      TEXT PRIMARY KEY,
    apple_track_id TEXT NOT NULL DEFAULT '',
    isrc           TEXT NOT NULL DEFAULT '',
    track_name     TEXT NOT NULL DEFAULT '',
    artist_name    TEXT NOT NULL DEFAULT '',
    album_name     TEXT NOT NULL DEFAULT '',
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    release_date   TEXT NOT NULL DEFAULT '',
    raw_attributes JSONB,
    first_seen     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_updated   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX song_metadata_isrc_idx ON song_metadata (isrc) WHERE isrc <> '';
CREATE INDEX song_metadata_song_artist_idx ON song_metadata (lower(track_name), lower(artist_name));

CREATE TABLE video_map (
    video_id  TEXT NOT NULL,
    cache_key TEXT NOT NULL,
    PRIMARY KEY (video_id, cache_key)
);
CREATE INDEX video_map_cache_key_idx ON video_map (cache_key);

CREATE TABLE counters (
    prefix TEXT PRIMARY KEY,
    count  BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE stats (
    id         INTEGER PRIMARY KEY DEFAULT 1,
    data       JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT stats_singleton CHECK (id = 1)
);

CREATE TABLE storefront_cache (
    mut_hash   TEXT PRIMARY KEY,
    storefront TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE migration_progress (
    bucket   TEXT PRIMARY KEY,
    last_key BYTEA NOT NULL DEFAULT ''::bytea,
    done     BOOLEAN NOT NULL DEFAULT FALSE
);

-- +goose Down
DROP TABLE migration_progress;
DROP TABLE storefront_cache;
DROP TABLE stats;
DROP TABLE counters;
DROP TABLE video_map;
DROP TABLE song_metadata;
DROP TABLE negative_cache;
DROP TABLE lyrics;
