//go:build conformance

package contracttest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"lyrics-api-go/internal/store"
)

type purgeRecorder struct {
	mu   sync.Mutex
	tags []string
}

func (p *purgeRecorder) has(tag string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Contains(p.tags, tag)
}

func cloudflareStub(t *testing.T) (*httptest.Server, *purgeRecorder) {
	t.Helper()
	rec := &purgeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones/test-zone/purge_cache" || r.Header.Get("Authorization") != "Bearer test-cf-token" {
			http.Error(w, "bad purge request", http.StatusBadRequest)
			return
		}
		var body struct {
			Tags []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rec.mu.Lock()
		rec.tags = append(rec.tags, body.Tags...)
		rec.mu.Unlock()
		w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func waitForPurge(t *testing.T, rec *purgeRecorder, tag string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if rec.has(tag) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("tag %s was never purged; got %v", tag, rec.tags)
}

func TestConformanceNewCachePurge(t *testing.T) {
	upstream := upstreamStub(t)
	cf, rec := cloudflareStub(t)

	env := stubEnv(upstream.URL)
	env["API_KEY_REQUIRED"] = "true"
	env["CLOUDFLARE_ZONE_ID"] = "test-zone"
	env["CLOUDFLARE_API_TOKEN"] = "test-cf-token"
	env["CLOUDFLARE_API_BASE_URL"] = cf.URL
	env["CLOUDFLARE_PURGE_INTERVAL_SECONDS"] = "1"
	seed := append([]SeedLyrics{{Song: "Fresh Hit", Artist: "Fresh Artist", TTML: "<tt>OLD</tt>"}}, seedLyricsData...)
	base := newServerBase(t, env, seed, seedNegativeData)

	RunSpec(t, base, []Scenario{
		{
			Name:        "getLyrics_carries_cache_tags",
			Path:        "/getLyrics?s=Conformance%20Hit&a=Tester",
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"Cache-Tag": strings.Join(store.SongCacheTags("Conformance Hit", "Tester", ""), ",")},
		},
		{
			Name:        "provider_endpoint_carries_cache_tags",
			Path:        "/ttml/getLyrics?s=Conformance%20Hit&a=Tester",
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"Cache-Tag": strings.Join(store.SongCacheTags("Conformance Hit", "Tester", ""), ",")},
		},
		{
			Name:        "negative_404_carries_cache_tags",
			Path:        "/getLyrics?s=Neg%20Song&a=Tester",
			WantStatus:  http.StatusNotFound,
			WantHeaders: map[string]string{"Cache-Tag": strings.Join(store.SongCacheTags("Neg Song", "Tester", ""), ",")},
		},
	})

	t.Run("override purges the song tag", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/override?no_lyrics=true&provider=qq&s=Purge%20Me&a=Tester", nil)
		req.Header.Set("X-API-Key", "test-api-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("override status %d", resp.StatusCode)
		}
		waitForPurge(t, rec, store.SongCacheTag("Purge Me", "Tester"))
	})

	t.Run("revalidate with changed content purges the song tag", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/revalidate?s=Fresh%20Hit&a=Fresh%20Artist", nil)
		req.Header.Set("X-API-Key", "test-api-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("revalidate status %d", resp.StatusCode)
		}
		waitForPurge(t, rec, store.SongCacheTag("Fresh Hit", "Fresh Artist"))
	})
}
