-- +goose Up
CREATE TABLE binilink_links (
    video_id       TEXT PRIMARY KEY,
    apple_track_id TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending',
    attempts       INTEGER NOT NULL DEFAULT 0,
    unresolved_retried BOOLEAN NOT NULL DEFAULT FALSE,
    next_attempt   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX binilink_links_pending_idx ON binilink_links (next_attempt) WHERE status = 'pending';

-- +goose Down
DROP TABLE binilink_links;
