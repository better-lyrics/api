//go:build conformance

package contracttest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"lyrics-api-go/internal/store"
)

type sweepResult struct {
	DryRun        bool     `json:"dry_run"`
	Checked       int      `json:"checked"`
	ModelSynced   []string `json:"model_synced"`
	Deleted       int      `json:"deleted"`
	SkippedNoISRC int      `json:"skipped_no_isrc"`
	Errors        []string `json:"errors"`
	NextAfter     string   `json:"next_after"`
	Done          bool     `json:"done"`
}

func seedSweepRows(t *testing.T) {
	t.Helper()
	_, st := ensurePostgres(t)
	ctx := context.Background()
	for _, r := range []struct{ song, artist, source, isrc string }{
		{"AI Synced", "AI Artist", "lrc.red", "USAISYNC0001"},
		{"Apple Song", "Tester", "apple", "USAISYNC0001"},
		{"Cached Song", "Cached Artist", "lrc.red", "USLRCRED0001"},
		{"Flaky Flag", "Flaky Artist", "lrc.red", "USFLAKY00001"},
		{"No Meta", "Tester", "lrc.red", ""},
	} {
		key := normalizedCacheKey(r.song, r.artist, "", "")
		if err := st.SetLyrics(ctx, key, store.DeriveKey(key), store.CachedLyrics{TTML: "<tt>SEEDED</tt>", Format: "ttml", Source: r.source}); err != nil {
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
			Path:        "/cache/lrcred-sweep?dry_run=true",
			Headers:     map[string]string{"Authorization": "test-admin-token"},
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"Content-Type": "application/json"},
			WantBody: jbody(map[string]interface{}{
				"dry_run":         true,
				"checked":         3,
				"model_synced":    []string{"USAISYNC0001"},
				"deleted":         0,
				"skipped_no_isrc": 1,
				"errors":          []string{"USFLAKY00001: lrc.red song.json returned status 500"},
				"next_after":      "",
				"done":            true,
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
	if ttml, ok := cachedTTML(t, "AI Synced", "AI Artist"); !ok || ttml != "<tt>SEEDED</tt>" {
		t.Fatal("dry run changed the cache")
	}

	var modelSynced []string
	deleted, calls, after := 0, 0, ""
	start := time.Now()
	for {
		res := postSweep(t, base, "limit=1&after="+url.QueryEscape(after))
		calls++
		if res.DryRun || res.Checked > 1 {
			t.Fatalf("call %d: %+v exceeded limit=1 or ran dry", calls, res)
		}
		modelSynced = append(modelSynced, res.ModelSynced...)
		deleted += res.Deleted
		if res.Done {
			break
		}
		if calls > 10 || res.NextAfter == "" {
			t.Fatalf("sweep did not finish: %+v", res)
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
	for _, kept := range [][2]string{{"Apple Song", "Tester"}, {"Cached Song", "Cached Artist"}, {"Flaky Flag", "Flaky Artist"}, {"No Meta", "Tester"}} {
		if _, ok := cachedTTML(t, kept[0], kept[1]); !ok {
			t.Errorf("%s was deleted", kept[0])
		}
	}
	waitForPurge(t, rec, store.KeyCacheTag(normalizedCacheKey("AI Synced", "AI Artist", "", "")))

	RunSpec(t, base, []Scenario{
		{
			Name:        "getlyrics_after_sweep_takes_apple_path",
			Path:        "/getLyrics?s=AI%20Synced&a=AI%20Artist",
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"X-Cache-Status": "MISS"},
			WantBody:    jbody(map[string]interface{}{"score": 0.875, "ttml": "<tt>FRESH</tt>"}),
		},
	})

	if again := postSweep(t, base, ""); again.Deleted != 0 || len(again.ModelSynced) != 0 || !again.Done {
		t.Errorf("re-run is not idempotent: %+v", again)
	}
}
