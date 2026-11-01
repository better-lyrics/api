//go:build conformance

package contracttest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"lyrics-api-go/internal/store"
)

const flakyKey = "ttml_lyrics:flaky flag flaky artist"

type sweepResult struct {
	DryRun        bool     `json:"dry_run"`
	Checked       int      `json:"checked"`
	ModelSynced   []string `json:"model_synced"`
	KeptChanged   []string `json:"kept_changed"`
	FailedKey     string   `json:"failed_key"`
	Deleted       int      `json:"deleted"`
	SkippedNoISRC []string `json:"skipped_no_isrc"`
	Errors        []string `json:"errors"`
	NextAfter     string   `json:"next_after"`
	Done          bool     `json:"done"`
}

func seedSweepRows(t *testing.T) {
	t.Helper()
	_, st := ensurePostgres(t)
	ctx := context.Background()
	for _, r := range []struct{ song, artist, source, isrc, ttml string }{
		// The upstream stub serves <tt>LRCRED</tt> as lrc.red's current file for every ISRC below.
		{"AI Synced", "AI Artist", "lrc.red", "USAISYNC0001", "<tt>LRCRED</tt>"},
		{"Apple Song", "Tester", "apple", "USAISYNC0001", "<tt>LRCRED</tt>"},
		{"Cached Song", "Cached Artist", "lrc.red", "USLRCRED0001", "<tt>LRCRED</tt>"},
		{"Flaky Flag", "Flaky Artist", "lrc.red", "USFLAKY00001", "<tt>LRCRED</tt>"},
		{"No Meta", "Tester", "lrc.red", "", "<tt>LRCRED</tt>"},
		{"No Meta Two", "Tester", "lrc.red", "", "<tt>LRCRED</tt>"},
		{"Old Human", "Tester", "lrc.red", "USAISYNC0001", "<tt>OLD HUMAN</tt>"},
	} {
		key := normalizedCacheKey(r.song, r.artist, "", "")
		if err := st.SetLyrics(ctx, key, store.DeriveKey(key), store.CachedLyrics{TTML: r.ttml, Format: "ttml", Source: r.source}); err != nil {
			t.Fatal(err)
		}
		if r.isrc != "" {
			if err := st.SetSongMetadata(ctx, &store.SongMetadata{CacheKey: key, ISRC: r.isrc, TrackName: r.song, ArtistName: r.artist}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func postSweep(t *testing.T, base, query string) sweepResult {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/cache/lrcred-sweep?"+query, nil)
	req.Header.Set("Authorization", "test-admin-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sweep %q: status %d", query, resp.StatusCode)
	}
	var out sweepResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func cachedTTML(t *testing.T, song, artist string) (string, bool) {
	t.Helper()
	_, st := ensurePostgres(t)
	l, ok, err := st.GetLyricsExact(context.Background(), normalizedCacheKey(song, artist, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	return l.TTML, ok
}

func TestConformanceNewLRCRedSweepRefusedWhenFlagOff(t *testing.T) {
	env := stubEnv(upstreamStub(t).URL)
	env["FF_LRC_RED_SKIP_AI_SYNCED"] = "false"
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)
	seedSweepRows(t)

	RunSpec(t, base, []Scenario{
		{
			Name:          "lrcred_sweep_unauthorized_401",
			Method:        http.MethodPost,
			Path:          "/cache/lrcred-sweep?dry_run=true",
			WantStatus:    http.StatusUnauthorized,
			WantBody:      "Unauthorized\n",
			AbsentHeaders: []string{"X-Cache-Status"},
		},
		{
			Name:       "lrcred_sweep_flag_off_409",
			Method:     http.MethodPost,
			Path:       "/cache/lrcred-sweep",
			Headers:    map[string]string{"Authorization": "test-admin-token"},
			WantStatus: http.StatusConflict,
			WantBody: jbody(map[string]interface{}{
				"error": "FF_LRC_RED_SKIP_AI_SYNCED is off; the sweep only runs while model-synced lrc.red files are skipped",
			}),
		},
	})
	if _, ok := cachedTTML(t, "AI Synced", "AI Artist"); !ok {
		t.Fatal("refused sweep deleted a row")
	}
}

func TestConformanceNewLRCRedSweepRefusedWithoutBaseURL(t *testing.T) {
	env := stubEnv(upstreamStub(t).URL)
	env["LRC_RED_BASE_URL"] = ""
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)
	seedSweepRows(t)

	RunSpec(t, base, []Scenario{
		{
			Name:       "lrcred_sweep_no_base_url_409",
			Method:     http.MethodPost,
			Path:       "/cache/lrcred-sweep",
			Headers:    map[string]string{"Authorization": "test-admin-token"},
			WantStatus: http.StatusConflict,
			WantBody: jbody(map[string]interface{}{
				"error": "LRC_RED_BASE_URL is not set; the sweep has nothing to check against",
			}),
		},
	})
	if _, ok := cachedTTML(t, "AI Synced", "AI Artist"); !ok {
		t.Fatal("refused sweep deleted a row")
	}
}

func TestConformanceNewLRCRedSweep(t *testing.T) {
	upstream := upstreamStub(t)
	cf, rec := cloudflareStub(t)
	env := stubEnv(upstream.URL)
	env["CLOUDFLARE_ZONE_ID"] = "test-zone"
	env["CLOUDFLARE_API_TOKEN"] = "test-cf-token"
	env["CLOUDFLARE_API_BASE_URL"] = cf.URL
	env["CLOUDFLARE_PURGE_INTERVAL_SECONDS"] = "1"
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)
	seedSweepRows(t)

	RunSpec(t, base, []Scenario{
		{
			Name:        "lrcred_sweep_dry_run_reports_without_deleting",
			Method:      http.MethodPost,
			Path:        "/cache/lrcred-sweep?dry_run=1",
			Headers:     map[string]string{"Authorization": "test-admin-token"},
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"Content-Type": "application/json"},
			WantBody: jbody(map[string]interface{}{
				"dry_run":         true,
				"checked":         3,
				"model_synced":    []string{"USAISYNC0001"},
				"kept_changed":    []string{},
				"failed_key":      flakyKey,
				"deleted":         0,
				"skipped_no_isrc": []string{},
				"errors":          []string{"USFLAKY00001 (" + flakyKey + "): lrc.red song.json returned status 500"},
				"next_after":      "ttml_lyrics:cached song cached artist",
				"done":            false,
			}),
		},
		{
			Name:        "lrcred_sweep_rejects_bad_limit",
			Method:      http.MethodPost,
			Path:        "/cache/lrcred-sweep?limit=0",
			Headers:     map[string]string{"Authorization": "test-admin-token"},
			WantStatus:  http.StatusBadRequest,
			WantHeaders: map[string]string{"Content-Type": "application/json"},
			WantBody:    jbody(map[string]interface{}{"error": "limit must be between 1 and 50"}),
		},
	})
	if ttml, ok := cachedTTML(t, "AI Synced", "AI Artist"); !ok || ttml != "<tt>LRCRED</tt>" {
		t.Fatal("dry run changed the cache")
	}

	var modelSynced, keptChanged, skippedNoISRC []string
	deleted, calls, after := 0, 0, ""
	start := time.Now()
	for {
		res := postSweep(t, base, "limit=1&after="+url.QueryEscape(after))
		calls++
		if res.DryRun || res.Checked+len(res.SkippedNoISRC) > 1 {
			t.Fatalf("call %d: %+v exceeded limit=1 or ran dry", calls, res)
		}
		modelSynced = append(modelSynced, res.ModelSynced...)
		skippedNoISRC = append(skippedNoISRC, res.SkippedNoISRC...)
		keptChanged = append(keptChanged, res.KeptChanged...)
		deleted += res.Deleted
		if res.Done {
			break
		}
		if calls > 12 || res.NextAfter == "" {
			t.Fatalf("sweep did not finish: %+v", res)
		}
		if res.FailedKey != "" {
			if res.FailedKey != flakyKey || res.NextAfter != after || len(res.Errors) != 1 {
				t.Fatalf("failed row not reported or cursor moved past it: %+v", res)
			}
			after = res.FailedKey
			continue
		}
		after = res.NextAfter
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("3 lrc.red checks took %v, want them rate limited to about 2/s", elapsed)
	}
	if len(modelSynced) != 1 || modelSynced[0] != "USAISYNC0001" || deleted != 1 {
		t.Fatalf("model_synced=%v deleted=%d, want only USAISYNC0001 deleted", modelSynced, deleted)
	}

	if _, ok := cachedTTML(t, "AI Synced", "AI Artist"); ok {
		t.Error("model-synced row survived the sweep")
	}
	if want := []string{normalizedCacheKey("No Meta", "Tester", "", ""), normalizedCacheKey("No Meta Two", "Tester", "", "")}; len(skippedNoISRC) != 2 || skippedNoISRC[0] != want[0] || skippedNoISRC[1] != want[1] {
		t.Errorf("skipped_no_isrc = %v, want %v", skippedNoISRC, want)
	}
	if len(keptChanged) != 1 || keptChanged[0] != "USAISYNC0001" {
		t.Errorf("kept_changed = %v, want the row cached before lrc.red's AI sync", keptChanged)
	}
	for _, kept := range [][2]string{{"Apple Song", "Tester"}, {"Cached Song", "Cached Artist"}, {"Flaky Flag", "Flaky Artist"}, {"No Meta", "Tester"}, {"Old Human", "Tester"}} {
		if _, ok := cachedTTML(t, kept[0], kept[1]); !ok {
			t.Errorf("%s was deleted", kept[0])
		}
	}
	waitForPurge(t, rec, store.KeyCacheTag(normalizedCacheKey("AI Synced", "AI Artist", "", "")))
	waitForPurge(t, rec, store.SongCacheTag("AI Synced", "AI Artist"))

	RunSpec(t, base, []Scenario{
		{
			Name:        "getlyrics_after_sweep_takes_apple_path",
			Path:        "/getLyrics?s=AI%20Synced&a=AI%20Artist",
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"X-Cache-Status": "MISS"},
			WantBody:    jbody(map[string]interface{}{"score": 0.875, "ttml": "<tt>FRESH</tt>"}),
		},
	})

	if again := postSweep(t, base, ""); again.Deleted != 0 || len(again.ModelSynced) != 0 || again.NextAfter != "ttml_lyrics:cached song cached artist" {
		t.Errorf("re-run is not idempotent: %+v", again)
	}
}

func getLyricsTTML(t *testing.T, base, song, artist string) (string, string) {
	t.Helper()
	resp, err := http.Get(base + "/getLyrics?s=" + url.QueryEscape(song) + "&a=" + url.QueryEscape(artist))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		TTML string `json:"ttml"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.TTML, resp.Header.Get("X-Cache-Status")
}

func waitUncached(t *testing.T, song, artist string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := cachedTTML(t, song, artist); !ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s was never removed from the cache", song)
}

func TestConformanceNewLRCRedCheckOnCacheHit(t *testing.T) {
	cf, rec := cloudflareStub(t)
	env := stubEnv(upstreamStub(t).URL)
	env["CLOUDFLARE_ZONE_ID"] = "test-zone"
	env["CLOUDFLARE_API_TOKEN"] = "test-cf-token"
	env["CLOUDFLARE_API_BASE_URL"] = cf.URL
	env["CLOUDFLARE_PURGE_INTERVAL_SECONDS"] = "1"
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)
	seedSweepRows(t)

	if _, status := getLyricsTTML(t, base, "No Meta", "Tester"); status != "HIT" {
		t.Fatalf("No Meta: status %s, want HIT", status)
	}
	// A key with no ISRC spends one limiter token, once per process; wait for the refill.
	time.Sleep(600 * time.Millisecond)
	if ttml, status := getLyricsTTML(t, base, "AI Synced", "AI Artist"); status != "HIT" || ttml != "<tt>LRCRED</tt>" {
		t.Fatalf("first request got (%q, %s), want the cached copy served without delay", ttml, status)
	}
	waitUncached(t, "AI Synced", "AI Artist")
	waitForPurge(t, rec, store.KeyCacheTag(normalizedCacheKey("AI Synced", "AI Artist", "", "")))
	if ttml, status := getLyricsTTML(t, base, "AI Synced", "AI Artist"); status != "MISS" || ttml != "<tt>FRESH</tt>" {
		t.Fatalf("after the check got (%q, %s), want the Apple path", ttml, status)
	}

	for _, song := range [][2]string{{"Old Human", "Tester"}, {"Cached Song", "Cached Artist"}} {
		time.Sleep(600 * time.Millisecond)
		if _, status := getLyricsTTML(t, base, song[0], song[1]); status != "HIT" {
			t.Fatalf("%s: status %s, want HIT", song[0], status)
		}
	}
	time.Sleep(1500 * time.Millisecond)
	for _, song := range [][2]string{{"Old Human", "Tester"}, {"Cached Song", "Cached Artist"}} {
		if _, ok := cachedTTML(t, song[0], song[1]); !ok {
			t.Errorf("%s was deleted, but its cached copy is not lrc.red's AI file", song[0])
		}
	}
}

func TestConformanceNewLRCRedCheckOnCacheHitFlagOff(t *testing.T) {
	env := stubEnv(upstreamStub(t).URL)
	env["FF_LRC_RED_SKIP_AI_SYNCED"] = "false"
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)
	seedSweepRows(t)

	if _, status := getLyricsTTML(t, base, "AI Synced", "AI Artist"); status != "HIT" {
		t.Fatalf("status %s, want HIT", status)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, ok := cachedTTML(t, "AI Synced", "AI Artist"); !ok {
		t.Fatal("flag off deleted a cached row")
	}
}

func TestConformanceNewLRCRedCheckSkipsStaleFallback(t *testing.T) {
	stub := upstreamStub(t)
	env := stubEnv(stub.URL)
	env["TTML_BASE_URL"] = "http://127.0.0.1:1"
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)
	seedSweepRows(t)

	resp, err := http.Get(base + "/getLyrics?s=" + url.QueryEscape("AI Synced") + "&a=" + url.QueryEscape("AI Artist") + "&al=Other%20Album")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if status := resp.Header.Get("X-Cache-Status"); status != "STALE" {
		t.Fatalf("status %s, want STALE while Apple is down", status)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, ok := cachedTTML(t, "AI Synced", "AI Artist"); !ok {
		t.Fatal("a stale fallback deleted the only cached copy while Apple was down")
	}
}

func TestConformanceNewLRCRedDeleteRevalidatesProxy(t *testing.T) {
	var mu sync.Mutex
	var videoIDs []string
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		videoIDs = append(videoIDs, r.URL.Query().Get("videoId"))
		mu.Unlock()
	}))
	t.Cleanup(proxySrv.Close)
	env := stubEnv(upstreamStub(t).URL)
	env["PROXY_REVALIDATE_URL"] = proxySrv.URL + "/revalidate"
	env["PROXY_API_KEY"] = "test-proxy-key"
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)
	seedSweepRows(t)
	_, st := ensurePostgres(t)
	for key, vid := range map[string]string{
		normalizedCacheKey("AI Synced", "AI Artist", "", ""): "vidAISynced",
		normalizedCacheKey("Old Human", "Tester", "", ""):    "vidOldHuman",
	} {
		if err := st.AddVideoID(context.Background(), key, vid); err != nil {
			t.Fatal(err)
		}
	}

	if _, status := getLyricsTTML(t, base, "AI Synced", "AI Artist"); status != "HIT" {
		t.Fatalf("status %s, want HIT", status)
	}
	waitUncached(t, "AI Synced", "AI Artist")
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := append([]string(nil), videoIDs...)
		mu.Unlock()
		if len(got) > 0 {
			if len(got) != 1 || got[0] != "vidAISynced" {
				t.Fatalf("proxy revalidated %v, want only the deleted song's video", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the proxy was never told about the deleted row")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func adminGet(t *testing.T, url string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "test-admin-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d", url, resp.StatusCode)
	}
}

func TestConformanceNewLRCRedKeepsRowsWhileAppleIsDown(t *testing.T) {
	env := stubEnv(upstreamStub(t).URL)
	env["CIRCUIT_BREAKER_THRESHOLD"] = "1"
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)
	seedSweepRows(t)
	adminGet(t, base+"/circuit-breaker/simulate-failure")

	if _, status := getLyricsTTML(t, base, "AI Synced", "AI Artist"); status != "HIT" {
		t.Fatalf("status %s, want HIT", status)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, ok := cachedTTML(t, "AI Synced", "AI Artist"); !ok {
		t.Fatal("deleted the cached copy while the Apple circuit breaker was open")
	}
	if res := postSweep(t, base, "limit=50"); res.Deleted != 0 || len(res.Errors) == 0 {
		t.Fatalf("sweep while Apple is down: deleted %d, errors %v; want no deletes and an error", res.Deleted, res.Errors)
	}

	adminGet(t, base+"/circuit-breaker/reset")
	time.Sleep(600 * time.Millisecond)
	if _, status := getLyricsTTML(t, base, "AI Synced", "AI Artist"); status != "HIT" {
		t.Fatalf("status %s, want HIT", status)
	}
	waitUncached(t, "AI Synced", "AI Artist")
}
