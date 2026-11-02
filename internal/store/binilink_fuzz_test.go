package store

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func postgresMatches(t *testing.T, s, pattern string) bool {
	t.Helper()
	var ok bool
	if err := testStore.pool.QueryRow(context.Background(), `SELECT $1 ~ $2`, s, pattern).Scan(&ok); err != nil {
		t.Fatalf("postgres regex on %q: %v", s, err)
	}
	return ok
}

func biniLinkRow(t *testing.T, videoID string) (appleTrackID string, ok bool) {
	t.Helper()
	err := testStore.pool.QueryRow(context.Background(),
		`SELECT apple_track_id FROM binilink_links WHERE video_id = $1`, videoID).Scan(&appleTrackID)
	if err != nil {
		return "", false
	}
	return appleTrackID, true
}

func FuzzIDPatternParity(f *testing.F) {
	for _, seed := range []string{
		"", "dQw4w9WgXcQ", "dQw4w9WgXc", "dQw4w9WgXcQQ", "dQw4w9WgXc!", "dQw4w9WgXc\n", "\ndQw4w9WgXcQ",
		"dQw4w9WgXcQ\n", "aaaaaaaaaa-", "___________", "-----------", "ÀÀÀÀÀÀÀÀÀÀÀ", "ａａａａａａａａａａａ",
		"1", "0", "007", "1440815557", strings.Repeat("9", 128), strings.Repeat("9", 129), "١٢٣", "１２３",
		"12a", " 12", "12 ", "-1", "+1", "1.0", "²", "1\n", "\n1", "ǅǅǅǅǅǅǅǅǅǅǅ", "KKKKKKKKKKK",
	} {
		f.Add(seed)
	}
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			t.Skip("postgres cannot store NUL bytes or invalid UTF-8")
		}
		if s == "aaaaaaaaaaa" || s == "bbbbbbbbbbb" {
			t.Skip("collides with the fixed probe videos")
		}
		goVideo := biniLinkVideoID.MatchString(s)
		goApple := biniLinkAppleTrackID.MatchString(s)
		if pg := postgresMatches(t, s, biniLinkVideoIDPattern); pg != goVideo {
			t.Fatalf("video pattern on %q: go=%v postgres=%v", s, goVideo, pg)
		}
		if pg := postgresMatches(t, s, biniLinkAppleTrackIDPattern); pg != goApple {
			t.Fatalf("apple pattern on %q: go=%v postgres=%v", s, goApple, pg)
		}

		if _, err := testStore.pool.Exec(ctx, `TRUNCATE binilink_links, video_map, song_metadata`); err != nil {
			t.Fatal(err)
		}
		if err := testStore.EnqueueBiniLink(ctx, s, "1"); err != nil {
			t.Fatalf("enqueue video %q: %v", s, err)
		}
		if _, ok := biniLinkRow(t, s); ok != goVideo {
			t.Fatalf("video %q queued=%v, go regex says %v", s, ok, goVideo)
		}

		const fixedVideo = "aaaaaaaaaaa"
		if err := testStore.EnqueueBiniLink(ctx, fixedVideo, s); err != nil {
			t.Fatalf("enqueue apple %q: %v", s, err)
		}
		if apple, ok := biniLinkRow(t, fixedVideo); ok != goApple || (ok && apple != s) {
			t.Fatalf("apple %q queued=%v as %q, go regex says %v", s, ok, apple, goApple)
		}

		const mappedVideo = "bbbbbbbbbbb"
		cacheKey := "ttml_lyrics:fuzz parity"
		if err := testStore.SetSongMetadata(ctx, &SongMetadata{CacheKey: cacheKey, AppleTrackID: s, ISRC: "USFUZZ000001"}); err != nil {
			t.Fatalf("metadata with apple %q: %v", s, err)
		}
		if err := testStore.AddVideoIDAndEnqueueBiniLink(ctx, cacheKey, mappedVideo); err != nil {
			t.Fatalf("add video with apple %q: %v", s, err)
		}
		if apple, ok := biniLinkRow(t, mappedVideo); ok != goApple || (ok && apple != s) {
			t.Fatalf("cached apple %q queued=%v as %q, go regex says %v", s, ok, apple, goApple)
		}

		if _, err := testStore.pool.Exec(ctx, `DELETE FROM binilink_links WHERE video_id = $1`, s); err != nil {
			t.Fatal(err)
		}
		validKey := "ttml_lyrics:fuzz parity valid apple"
		if err := testStore.SetSongMetadata(ctx, &SongMetadata{CacheKey: validKey, AppleTrackID: "1", ISRC: "USFUZZ000002"}); err != nil {
			t.Fatal(err)
		}
		if err := testStore.AddVideoIDAndEnqueueBiniLink(ctx, validKey, s); err != nil {
			t.Fatalf("add video %q: %v", s, err)
		}
		if _, ok := biniLinkRow(t, s); ok != goVideo {
			t.Fatalf("cached video %q queued=%v, go regex says %v", s, ok, goVideo)
		}
		keys, err := testStore.GetCacheKeysByVideoID(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		if s != "" && (len(keys) != 1 || keys[0] != validKey) {
			t.Fatalf("video %q mapped to %v, want [%s]", s, keys, validKey)
		}
	})
}
