//go:build conformance

package contracttest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
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
	requestLyricsWithKey(t, base, query, wantCache, "test-api-key")
}

func requestLyricsWithKey(t *testing.T, base string, query url.Values, wantCache, apiKey string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/getLyrics?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
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
	env := stubEnv(stub.URL)
	env["FF_BINILINK_INGRESS"] = "true"
	env["API_KEY_REQUIRED"] = "true"
	base := newServerBase(t, env, seed, seedNegativeData)
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

	t.Run("exact hits without a valid API key queue nothing", func(t *testing.T) {
		requestLyricsWithKey(t, base, lyricsQuery("Linked Song", "Link Tester", "200", "noKey000001"), "HIT", "")
		waitVideoMapped(t, "noKey000001")
		requestLyricsWithKey(t, base, lyricsQuery("Linked Song", "Link Tester", "200", "badKey00001"), "HIT", "wrong-key")
		waitVideoMapped(t, "badKey00001")
	})

	t.Run("malformed videoIds queue nothing", func(t *testing.T) {
		q := lyricsQuery("Linked Song", "Link Tester", "200", "dQw4w9WgXcQ")
		q.Set("v", "dQw4w9WgXcQ")
		requestLyrics(t, base, q, "HIT")
		waitVideoMapped(t, "dQw4w9WgXcQdQw4w9WgXcQ")
		requestLyrics(t, base, lyricsQuery("Linked Song", "Link Tester", "200", "bad id!"), "HIT")
		waitVideoMapped(t, "bad id!")
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

func backfillPage(t *testing.T, base, after string, limit int) (seeded int, next string, done bool) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/binilink/backfill?"+url.Values{"after": {after}, "limit": {strconv.Itoa(limit)}}.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "test-admin-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backfill status %d", resp.StatusCode)
	}
	var body struct {
		Seeded    int    `json:"seeded"`
		NextAfter string `json:"next_after"`
		Done      bool   `json:"done"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Seeded, body.NextAfter, body.Done
}

func TestConformanceNewBiniLinkBackfill(t *testing.T) {
	base := newServerBase(t, generalProfileEnv(), seedLyricsData, seedNegativeData)
	resetBiniLinkQueue(t)

	_, st := ensurePostgres(t)
	ctx := context.Background()
	for _, m := range []store.SongMetadata{
		{CacheKey: "ttml_lyrics:backfill one", AppleTrackID: "1600000001", ISRC: "USBACK000001", VideoIDs: []string{"bf000000001"}},
		{CacheKey: "ttml_lyrics:backfill two", AppleTrackID: "1600000002", ISRC: "USBACK000002", VideoIDs: []string{"bf000000002"}},
		{CacheKey: "ttml_lyrics:backfill split a", AppleTrackID: "1600000003", ISRC: "USBACK000003", VideoIDs: []string{"bf000000003"}},
		{CacheKey: "ttml_lyrics:backfill split b", AppleTrackID: "1600000004", ISRC: "USBACK000004", VideoIDs: []string{"bf000000003"}},
		{CacheKey: "ttml_lyrics:backfill no apple", ISRC: "USBACK000005", VideoIDs: []string{"bf000000005"}},
	} {
		if err := st.SetSongMetadata(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}

	total, cursor, pages := 0, "", 0
	for {
		seeded, next, done := backfillPage(t, base, cursor, 1)
		total += seeded
		pages++
		if done != (next == "") {
			t.Fatalf("done=%v but next_after=%q", done, next)
		}
		if done {
			break
		}
		if next <= cursor {
			t.Fatalf("cursor did not advance: %q after %q", next, cursor)
		}
		cursor = next
		if pages > 1000 {
			t.Fatal("backfill never finished")
		}
	}

	got := map[string]string{}
	for _, l := range queuedLinks(t) {
		got[l.videoID] = l.appleTrackID
	}
	if got["bf000000001"] != "1600000001" || got["bf000000002"] != "1600000002" {
		t.Fatalf("unambiguous videos missing: %+v", got)
	}
	for _, skipped := range []string{"bf000000003", "bf000000005"} {
		if _, ok := got[skipped]; ok {
			t.Fatalf("%s must be skipped", skipped)
		}
	}
	if total != len(got) {
		t.Fatalf("seeded %d but queue holds %d", total, len(got))
	}

	if seeded, _, done := backfillPage(t, base, "", 10000); seeded != 0 || !done {
		t.Fatalf("second full pass seeded %d done=%v, want 0 true", seeded, done)
	}
}

func TestConformanceNewBiniLinkQueueOffByDefault(t *testing.T) {
	stub := upstreamStub(t)
	seed := append([]SeedLyrics{
		{Song: "Flag Off", Artist: "Link Tester", Duration: "200", TTML: "<tt>OFF</tt>"},
	}, seedLyricsData...)
	env := stubEnv(stub.URL)
	env["API_KEY_REQUIRED"] = "true"
	base := newServerBase(t, env, seed, seedNegativeData)
	resetBiniLinkQueue(t)

	_, st := ensurePostgres(t)
	if err := st.SetSongMetadata(context.Background(), &store.SongMetadata{
		CacheKey: normalizedCacheKey("Flag Off", "Link Tester", "", "200"), AppleTrackID: "1700000001", ISRC: "USFLAG000001",
	}); err != nil {
		t.Fatal(err)
	}

	requestLyrics(t, base, lyricsQuery("Flag Off", "Link Tester", "200", "flagOff0001"), "HIT")
	requestLyrics(t, base, lyricsQuery("Fresh Hit", "Fresh Artist", "", "flagOff0002"), "MISS")
	waitVideoMapped(t, "flagOff0001")
	waitVideoMapped(t, "flagOff0002")
	time.Sleep(200 * time.Millisecond)
	if got := queuedLinks(t); len(got) != 0 {
		t.Fatalf("flag off must queue nothing, got %+v", got)
	}
}
