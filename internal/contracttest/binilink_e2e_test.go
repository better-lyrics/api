//go:build conformance

package contracttest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"lyrics-api-go/internal/store"
)

type fakeIngressRequest struct {
	at          time.Time
	method      string
	path        string
	key         string
	contentType string
	body        map[string]any
	rawBody     string
}

type fakeBiniLink struct {
	*httptest.Server
	busy map[string]bool

	mu          sync.Mutex
	requests    []fakeIngressRequest
	inFlight    int
	maxInFlight int
}

func newFakeBiniLink(t *testing.T, busy ...string) *fakeBiniLink {
	t.Helper()
	f := &fakeBiniLink{busy: map[string]bool{}}
	for _, v := range busy {
		f.busy[v] = true
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.inFlight++
		f.maxInFlight = max(f.maxInFlight, f.inFlight)
		f.mu.Unlock()
		defer func() {
			f.mu.Lock()
			f.inFlight--
			f.mu.Unlock()
		}()

		raw, _ := io.ReadAll(r.Body)
		req := fakeIngressRequest{
			at: time.Now(), method: r.Method, path: r.URL.Path, key: r.Header.Get("X-Ingress-Key"),
			contentType: r.Header.Get("Content-Type"), rawBody: string(raw),
		}
		_ = json.Unmarshal(raw, &req.body)
		videoID, _ := req.body["youtube_id"].(string)

		time.Sleep(5 * time.Millisecond)
		f.mu.Lock()
		seen := false
		for _, prev := range f.requests {
			if prev.body["youtube_id"] == videoID {
				seen = true
			}
		}
		f.requests = append(f.requests, req)
		f.mu.Unlock()

		switch {
		case f.busy[videoID]:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
		case seen:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeBiniLink) snapshot() ([]fakeIngressRequest, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeIngressRequest(nil), f.requests...), f.maxInFlight
}

func (f *fakeBiniLink) byVideo() map[string][]fakeIngressRequest {
	reqs, _ := f.snapshot()
	out := map[string][]fakeIngressRequest{}
	for _, r := range reqs {
		v, _ := r.body["youtube_id"].(string)
		out[v] = append(out[v], r)
	}
	return out
}

func resetBiniLinkTables(t *testing.T) {
	t.Helper()
	_, st := ensurePostgres(t)
	if _, err := st.Pool().Exec(context.Background(), `TRUNCATE binilink_links, video_map, song_metadata`); err != nil {
		t.Fatal(err)
	}
}

func linkStatus(t *testing.T, videoID string) (status string, attempts int) {
	t.Helper()
	_, st := ensurePostgres(t)
	if err := st.Pool().QueryRow(context.Background(),
		`SELECT status, attempts FROM binilink_links WHERE video_id = $1`, videoID).Scan(&status, &attempts); err != nil {
		t.Fatalf("row for %s: %v", videoID, err)
	}
	return status, attempts
}

func runBackfill(t *testing.T, base string) {
	t.Helper()
	cursor := ""
	for pages := 0; ; pages++ {
		_, next, done := backfillPage(t, base, cursor, 2)
		if done {
			return
		}
		if next <= cursor || pages > 1000 {
			t.Fatalf("backfill cursor stuck at %q after %q", next, cursor)
		}
		cursor = next
	}
}

func TestConformanceNewBiniLinkEndToEnd(t *testing.T) {
	const (
		freshVideo  = "e2eFresh001"
		exactVideo  = "e2eExact001"
		busyVideo   = "e2eBusy0001"
		backOne     = "e2eBackOne1"
		backTwo     = "e2eBackTwo2"
		backSplit   = "e2eBackSplt"
		backNoApple = "e2eBackNoAp"
		backNoISRC  = "e2eBackNoIs"
		keyless     = "e2eNoKey001"
		wrongKey    = "e2eBadKey01"
		fuzzy       = "e2eFuzzy001"
	)
	want := map[string]string{
		freshVideo: "1000001",
		exactVideo: "1800000001",
		busyVideo:  "1800000002",
		backOne:    "1900000001",
		backTwo:    "1900000002",
	}

	ensurePostgres(t)
	resetBiniLinkTables(t)
	stub := upstreamStub(t)
	fake := newFakeBiniLink(t, busyVideo)
	env := stubEnv(stub.URL)
	env["FF_BINILINK_INGRESS"] = "true"
	env["BINILINK_INGRESS_KEY"] = "e2e-key"
	env["BINILINK_BASE_URL"] = fake.URL
	env["BINILINK_INGRESS_INTERVAL_MS"] = "10"
	env["API_KEY_REQUIRED"] = "true"
	seed := append([]SeedLyrics{
		{Song: "E2E Hit", Artist: "E2E Tester", Duration: "200", TTML: "<tt>E2E</tt>"},
		{Song: "E2E Busy", Artist: "E2E Tester", Duration: "200", TTML: "<tt>BUSY</tt>"},
	}, seedLyricsData...)
	base := newServerBase(t, env, seed, seedNegativeData)
	start := time.Now()

	_, st := ensurePostgres(t)
	ctx := context.Background()
	for _, m := range []store.SongMetadata{
		{CacheKey: normalizedCacheKey("E2E Hit", "E2E Tester", "", "200"), AppleTrackID: "1800000001", ISRC: "USE2E0000001"},
		{CacheKey: normalizedCacheKey("E2E Busy", "E2E Tester", "", "200"), AppleTrackID: "1800000002", ISRC: "USE2E0000002"},
		{CacheKey: "ttml_lyrics:e2e back one", AppleTrackID: "1900000001", ISRC: "USE2EB000001", VideoIDs: []string{backOne}},
		{CacheKey: "ttml_lyrics:e2e back two", AppleTrackID: "1900000002", ISRC: "USE2EB000002", VideoIDs: []string{backTwo}},
		{CacheKey: "ttml_lyrics:e2e back split a", AppleTrackID: "1900000003", ISRC: "USE2EB000003", VideoIDs: []string{backSplit}},
		{CacheKey: "ttml_lyrics:e2e back split b", AppleTrackID: "1900000004", ISRC: "USE2EB000004", VideoIDs: []string{backSplit}},
		{CacheKey: "ttml_lyrics:e2e back no apple", ISRC: "USE2EB000005", VideoIDs: []string{backNoApple}},
		{CacheKey: "ttml_lyrics:e2e back no isrc", AppleTrackID: "1900000006", VideoIDs: []string{backNoISRC}},
	} {
		if err := st.SetSongMetadata(ctx, &m); err != nil {
			t.Fatal(err)
		}
	}

	runBackfill(t, base)

	requestLyrics(t, base, lyricsQuery("Fresh Hit", "Fresh Artist", "", freshVideo), "MISS")
	requestLyrics(t, base, lyricsQuery("E2E Hit", "E2E Tester", "200", exactVideo), "HIT")
	requestLyrics(t, base, lyricsQuery("E2E Hit", "E2E Tester", "200", exactVideo), "HIT")
	requestLyrics(t, base, lyricsQuery("E2E Busy", "E2E Tester", "200", busyVideo), "HIT")
	requestLyricsWithKey(t, base, lyricsQuery("E2E Hit", "E2E Tester", "200", keyless), "HIT", "")
	requestLyricsWithKey(t, base, lyricsQuery("E2E Hit", "E2E Tester", "200", wrongKey), "HIT", "wrong-key")
	requestLyrics(t, base, lyricsQuery("E2E Hit", "E2E Tester", "202", fuzzy), "HIT")
	requestLyrics(t, base, lyricsQuery("E2E Hit", "E2E Tester", "200", "bad id!"), "HIT")
	for _, v := range []string{keyless, wrongKey, fuzzy} {
		waitVideoMapped(t, v)
	}

	deadline := time.Now().Add(70 * time.Second)
	for {
		got := fake.byVideo()
		complete := true
		for v := range want {
			if len(got[v]) == 0 {
				complete = false
			}
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %v the fake received %d videos, want %v", time.Since(start), len(got), want)
		}
		time.Sleep(100 * time.Millisecond)
	}
	busyFirst := fake.byVideo()[busyVideo][0].at
	if wait := time.Until(busyFirst.Add(20 * time.Second)); wait > 0 {
		time.Sleep(wait)
	}

	t.Run("every queued pair arrives once, well formed", func(t *testing.T) {
		for v, apple := range want {
			reqs := fake.byVideo()[v]
			if v != busyVideo && len(reqs) != 1 {
				t.Errorf("%s received %d times, want 1", v, len(reqs))
			}
			for _, r := range reqs {
				if r.method != http.MethodPost || r.path != "/api/v1/ingress/youtube" {
					t.Errorf("%s: %s %s", v, r.method, r.path)
				}
				if r.key != "e2e-key" || r.contentType != "application/json" {
					t.Errorf("%s: key %q content type %q", v, r.key, r.contentType)
				}
				if len(r.body) != 2 || r.body["youtube_id"] != v || r.body["apple_music_id"] != apple {
					t.Errorf("%s: body %s, want youtube_id %s apple_music_id %s only", v, r.rawBody, v, apple)
				}
			}
		}
	})

	t.Run("accepted rows end linked", func(t *testing.T) {
		for v := range want {
			if v == busyVideo {
				continue
			}
			if status, attempts := linkStatus(t, v); status != "linked" || attempts != 1 {
				t.Errorf("%s: status %s attempts %d, want linked 1", v, status, attempts)
			}
		}
	})

	t.Run("keyless, wrong key, fuzzy and skipped backfill videos never reach BiniLink", func(t *testing.T) {
		got := fake.byVideo()
		var extra []string
		for v := range got {
			if _, ok := want[v]; !ok {
				extra = append(extra, v)
			}
		}
		sort.Strings(extra)
		if len(extra) > 0 {
			t.Fatalf("unexpected videos posted: %v", extra)
		}
	})

	t.Run("a busy video is not hammered", func(t *testing.T) {
		reqs := fake.byVideo()[busyVideo]
		for i := 1; i < len(reqs); i++ {
			if gap := reqs[i].at.Sub(reqs[i-1].at); gap < 20*time.Second {
				t.Fatalf("busy video retried after %v, want at least 20s", gap)
			}
		}
		if status, attempts := linkStatus(t, busyVideo); status != "pending" || attempts != len(reqs) {
			t.Fatalf("busy video: status %s attempts %d after %d requests", status, attempts, len(reqs))
		}
	})

	t.Run("one request in flight at a time", func(t *testing.T) {
		if _, peak := fake.snapshot(); peak != 1 {
			t.Fatalf("max in flight = %d, want 1", peak)
		}
	})
	t.Logf("end to end finished in %v", time.Since(start))
}
