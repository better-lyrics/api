//go:build conformance

package contracttest

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"testing"
	"time"

	"lyrics-api-go/internal/store"

	"github.com/jackc/pgx/v5"
)

type queuedLink struct{ videoID, appleTrackID string }

func resetBiniLinkQueue(t *testing.T) {
	t.Helper()
	dsn, _ := ensurePostgres(t)
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), `DELETE FROM binilink_links`); err != nil {
		t.Fatal(err)
	}
}

func queuedLinks(t *testing.T) []queuedLink {
	t.Helper()
	_, st := ensurePostgres(t)
	pending, err := st.ClaimBiniLinkPending(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]queuedLink, 0, len(pending))
	for _, p := range pending {
		if p.Attempts != 0 {
			t.Errorf("%s queued with %d attempts, want 0", p.VideoID, p.Attempts)
		}
		out = append(out, queuedLink{p.VideoID, p.AppleTrackID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].videoID < out[j].videoID })
	return out
}

func waitQueued(t *testing.T, want queuedLink) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, got := range queuedLinks(t) {
			if got == want {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%+v was never queued; queue holds %+v", want, queuedLinks(t))
}

func waitVideoMapped(t *testing.T, videoID string) {
	t.Helper()
	_, st := ensurePostgres(t)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		keys, err := st.GetCacheKeysByVideoID(context.Background(), videoID)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s was never recorded against a cache key", videoID)
}

func requestLyrics(t *testing.T, base string, query url.Values, wantCache string) {
	t.Helper()
	resp, err := http.Get(base + "/getLyrics?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%v: status %d, want 200", query, resp.StatusCode)
	}
	if got := resp.Header.Get("X-Cache-Status"); got != wantCache {
		t.Fatalf("%v: X-Cache-Status %s, want %s", query, got, wantCache)
	}
}

func lyricsQuery(song, artist, duration, videoID string) url.Values {
	q := url.Values{"s": {song}, "a": {artist}}
	if duration != "" {
		q.Set("d", duration)
	}
	if videoID != "" {
		q.Set("videoId", videoID)
	}
	return q
}

func TestConformanceNewBiniLinkQueue(t *testing.T) {
	stub := upstreamStub(t)
	seed := append([]SeedLyrics{
		{Song: "Linked Song", Artist: "Link Tester", Duration: "200", TTML: "<tt>LINKED</tt>"},
		{Song: "Near Link", Artist: "Link Tester", Duration: "200", TTML: "<tt>NEAR</tt>"},
		{Song: "Bare Song", Artist: "Link Tester", TTML: "<tt>BARE</tt>"},
		{Song: "No Apple", Artist: "Link Tester", TTML: "<tt>NO APPLE</tt>"},
	}, seedLyricsData...)
	base := newServerBase(t, stubEnv(stub.URL), seed, seedNegativeData)
	resetBiniLinkQueue(t)

	_, st := ensurePostgres(t)
	ctx := context.Background()
	for _, m := range []store.SongMetadata{
		{CacheKey: normalizedCacheKey("Linked Song", "Link Tester", "", "200"), AppleTrackID: "1500000001", ISRC: "USLINK000001", TrackName: "Linked Song", ArtistName: "Link Tester"},
		{CacheKey: normalizedCacheKey("Near Link", "Link Tester", "", "200"), AppleTrackID: "1500000002", ISRC: "USLINK000002", TrackName: "Near Link", ArtistName: "Link Tester"},
		{CacheKey: normalizedCacheKey("No Apple", "Link Tester", "", ""), ISRC: "USLINK000003", TrackName: "No Apple", ArtistName: "Link Tester"},
	} {
		if err := st.SetSongMetadata(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}
	legacyKey := "ttml_lyrics:Legacy Song Link Tester  200s"
	if err := st.SetLyrics(ctx, legacyKey, store.DeriveKey(legacyKey), store.CachedLyrics{TTML: "<tt>LEGACY</tt>"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSongMetadata(ctx, &store.SongMetadata{CacheKey: legacyKey, AppleTrackID: "1500000004", ISRC: "USLINK000004", TrackName: "Legacy Song", ArtistName: "Link Tester"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.GetSongMetadata(ctx, normalizedCacheKey("Bare Song", "Link Tester", "", "")); err != nil || ok {
		t.Fatalf("Bare Song must have no metadata: ok=%v err=%v", ok, err)
	}

	t.Run("fuzzy duration hit records the video but does not queue it", func(t *testing.T) {
		requestLyrics(t, base, lyricsQuery("Near Link", "Link Tester", "202", "kJQP7kiw5Fk"), "HIT")
		waitVideoMapped(t, "kJQP7kiw5Fk")
	})

	t.Run("exact hit on a key without metadata queues nothing", func(t *testing.T) {
		requestLyrics(t, base, lyricsQuery("Bare Song", "Link Tester", "", "9bZkp7q1909"), "HIT")
		waitVideoMapped(t, "9bZkp7q1909")
	})

	t.Run("exact hit on metadata without an Apple ID queues nothing", func(t *testing.T) {
		requestLyrics(t, base, lyricsQuery("No Apple", "Link Tester", "", "JGwWNGJdvx8"), "HIT")
		waitVideoMapped(t, "JGwWNGJdvx8")
	})

	t.Run("requests without a videoId queue nothing", func(t *testing.T) {
		requestLyrics(t, base, lyricsQuery("Linked Song", "Link Tester", "200", ""), "HIT")
		requestLyrics(t, base, lyricsQuery("Cached Song", "Cached Artist", "", ""), "MISS")
	})

	t.Run("exact hit queues the cached Apple track", func(t *testing.T) {
		requestLyrics(t, base, lyricsQuery("Linked Song", "Link Tester", "200", "dQw4w9WgXcQ"), "HIT")
		waitQueued(t, queuedLink{"dQw4w9WgXcQ", "1500000001"})
	})

	t.Run("legacy key hit is exact and queues the cached Apple track", func(t *testing.T) {
		requestLyrics(t, base, lyricsQuery("Legacy Song", "Link Tester", "200", "hT_nvWreIhg"), "HIT")
		waitQueued(t, queuedLink{"hT_nvWreIhg", "1500000004"})
	})

	t.Run("fresh fetch queues the matched Apple track", func(t *testing.T) {
		requestLyrics(t, base, lyricsQuery("Fresh Hit", "Fresh Artist", "", "OPf0YbXqDm0"), "MISS")
		waitQueued(t, queuedLink{"OPf0YbXqDm0", "1000001"})
	})

	t.Run("invariants", func(t *testing.T) {
		requestLyrics(t, base, lyricsQuery("Linked Song", "Link Tester", "200", "dQw4w9WgXcQ"), "HIT")
		time.Sleep(200 * time.Millisecond)
		want := []queuedLink{{"OPf0YbXqDm0", "1000001"}, {"dQw4w9WgXcQ", "1500000001"}, {"hT_nvWreIhg", "1500000004"}}
		got := queuedLinks(t)
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Fatalf("queue = %+v, want exactly %+v", got, want)
		}
	})
}
