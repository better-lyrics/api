//go:build conformance

package contracttest

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// stubJWT builds a token whose header base64url-encodes to the ES256 prefix the
// scraper's regex looks for, with a far-future exp so it is not treated as stale.
func stubJWT() string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT","kid":"STUB0"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(2*time.Hour).Unix())))
	return header + "." + payload + ".c3R1YnNpZw"
}

// upstreamStub emulates the Apple Music upstream: the bearer-token scrape (browse
// page + JS bundle), storefront detection, search, and lyrics. Search returns a
// match only when the term contains "Fresh Hit"; every other term yields no songs.
func upstreamStub(t *testing.T) *httptest.Server {
	t.Helper()
	jwt := stubJWT()

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/browse"):
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<!doctype html><html><head><script type="module" src="/assets/index-stub.js"></script></head><body></body></html>`)

		case path == "/assets/index-stub.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprintf(w, `const developerToken="%s";export default developerToken;`, jwt)

		case strings.HasSuffix(path, "/me/account"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"meta":{"subscription":{"active":true,"storefront":"us"}}}`)

		case path == "/token":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"storefront_id":"143478-2,31","token":"%s","token_type":"Bearer","cache_ttl_seconds":120}`, jwt)

		case strings.HasPrefix(path, "/search/"):
			term := r.URL.Query().Get("term")
			if !strings.Contains(term, "Upstream Boom") {
				w.Header().Set("Content-Type", "application/json")
			}
			switch {
			case strings.Contains(term, "Upstream Boom"):
				http.Error(w, "upstream exploded", http.StatusInternalServerError)
				return
			case strings.Contains(term, "Fresh Hit"):
				// ISRC that lrc.red does NOT have -> falls through to the Apple lyrics stub.
				fmt.Fprint(w, `{"results":{"songs":{"data":[{"id":"1000001","attributes":{"name":"Fresh Hit","artistName":"Fresh Artist","albumName":"Fresh Album","durationInMillis":200000,"isrc":"USFRESH00001","releaseDate":"2020-01-01","hasTimeSyncedLyrics":true}}]}}}`)
			case strings.Contains(term, "Cached Song"):
				// ISRC that lrc.red DOES have -> resolved from lrc.red, Apple untouched.
				fmt.Fprint(w, `{"results":{"songs":{"data":[{"id":"1000002","attributes":{"name":"Cached Song","artistName":"Cached Artist","albumName":"Cached Album","durationInMillis":210000,"isrc":"USLRCRED0001","releaseDate":"2019-01-01","hasTimeSyncedLyrics":true}}]}}}`)
			default:
				fmt.Fprint(w, `{"results":{"songs":{"data":[]}}}`)
			}

		case strings.HasPrefix(path, "/s/") && strings.HasSuffix(path, ".ttml"):
			if path == "/s/USLRCRED0001.ttml" {
				w.Header().Set("Content-Type", "application/ttml+xml")
				fmt.Fprint(w, `<tt>LRCRED</tt>`)
			} else {
				http.NotFound(w, r)
			}

		case strings.HasPrefix(path, "/lyrics/"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"1000001","attributes":{"ttml":"<tt>FRESH</tt>"}}]}`)

		default:
			http.NotFound(w, r)
		}
	})

	t.Cleanup(srv.Close)
	return srv
}

func stubEnv(stubURL string) map[string]string {
	env := generalProfileEnv()
	env["TTML_TOKEN_SOURCE_URL"] = stubURL
	env["TTML_BASE_URL"] = stubURL
	env["TTML_SEARCH_PATH"] = "/search/%s?term=%s"
	env["TTML_LYRICS_PATH"] = "/lyrics/%s/%s"
	env["TTML_STOREFRONT"] = "us"
	env["TTML_MEDIA_USER_TOKEN"] = "stub-mut"
	env["LRC_RED_BASE_URL"] = stubURL
	env["TTML_MINT_URL"] = stubURL + "/token"
	return env
}

func missScenarios() []Scenario {
	return []Scenario{
		{
			Name:       "getlyrics_miss_fresh_fetch_returns_score_and_ttml",
			Path:       "/getLyrics?s=Fresh%20Hit&a=Fresh%20Artist",
			WantStatus: http.StatusOK,
			WantHeaders: map[string]string{
				"Content-Type":   "application/json",
				"X-Cache-Status": "MISS",
			},
			WantBody: jbody(map[string]interface{}{
				"score": 0.875,
				"ttml":  "<tt>FRESH</tt>",
			}),
		},
		{
			Name:        "getlyrics_miss_no_track_returns_404",
			Path:        "/getLyrics?s=Fresh%20Miss&a=Nobody",
			WantStatus:  http.StatusNotFound,
			WantHeaders: map[string]string{"X-Cache-Status": "MISS"},
			WantBody: jbody(map[string]interface{}{
				"error": "search failed: no tracks found for query: Fresh Miss Nobody",
			}),
		},
		{
			Name:       "getlyrics_miss_resolved_from_lrcred",
			Path:       "/getLyrics?s=Cached%20Song&a=Cached%20Artist",
			WantStatus: http.StatusOK,
			WantHeaders: map[string]string{
				"Content-Type":   "application/json",
				"X-Cache-Status": "MISS",
			},
			WantBody: jbody(map[string]interface{}{
				"score": 0.875,
				"ttml":  "<tt>LRCRED</tt>",
			}),
		},
	}
}

func cacheOnlyScenarios() []Scenario {
	return []Scenario{
		{
			Name:        "getlyrics_cache_only_mode_uncached_returns_503",
			Path:        "/getLyrics?s=Cache%20Only%20Miss&a=Nobody",
			WantStatus:  http.StatusServiceUnavailable,
			WantHeaders: map[string]string{"X-Cache-Status": "MISS"},
			WantBody: jbody(map[string]interface{}{
				"error": "Service running in cache-only mode. No cached lyrics available for this query.",
			}),
		},
	}
}

func TestConformanceCurrentMiss(t *testing.T) {
	stub := upstreamStub(t)
	bin := buildServer(t, ".")
	env := stubEnv(stub.URL)
	env["CACHE_DB_PATH"] = seededDBPath(t)
	base := startServer(t, bin, env)
	Run(t, base, missScenarios())
}

func TestConformanceNewMiss(t *testing.T) {
	stub := upstreamStub(t)
	base := newServerBase(t, stubEnv(stub.URL), seedLyricsData, seedNegativeData)
	RunSpec(t, base, missScenarios())
}

func TestConformanceCurrentCacheOnly(t *testing.T) {
	bin := buildServer(t, ".")
	env := generalProfileEnv()
	env["FF_CACHE_ONLY_MODE"] = "true"
	env["CACHE_DB_PATH"] = seededDBPath(t)
	base := startServer(t, bin, env)
	Run(t, base, cacheOnlyScenarios())
}

func TestConformanceNewCacheOnly(t *testing.T) {
	env := generalProfileEnv()
	env["FF_CACHE_ONLY_MODE"] = "true"
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)
	RunSpec(t, base, cacheOnlyScenarios())
}

func upstreamFailureScenarios() []Scenario {
	return []Scenario{
		{
			Name:          "spec_getlyrics_upstream_failure_500",
			Path:          "/getLyrics?s=Upstream%20Boom&a=Nobody",
			WantStatus:    http.StatusInternalServerError,
			WantHeaders:   map[string]string{"X-Cache-Status": "MISS"},
			WantBodyRegex: regexp.MustCompile(`^\{"error":".+"\}\n$`),
		},
		{
			Name:          "spec_ttml_provider_upstream_failure_500",
			Path:          "/ttml/getLyrics?s=Upstream%20Boom&a=Nobody",
			WantStatus:    http.StatusInternalServerError,
			WantHeaders:   map[string]string{"X-Cache-Status": "MISS", "X-Provider": "ttml"},
			WantBodyRegex: regexp.MustCompile(`"provider":"ttml"`),
		},
	}
}

func revalidateScenarios() []Scenario {
	key := map[string]string{"X-API-Key": "test-api-key"}
	authed := map[string]string{"X-Auth-Mode": "authenticated", "X-RateLimit-Type": "bypass", "X-RateLimit-Bypass": "true"}
	return []Scenario{
		{
			Name:        "spec_revalidate_changed_content_updates",
			Path:        "/revalidate?s=Fresh%20Hit&a=Fresh%20Artist",
			Headers:     key,
			WantStatus:  http.StatusOK,
			WantHeaders: authed,
			WantBody: jbody(map[string]interface{}{
				"updated":          true,
				"cacheKey":         "ttml_lyrics:fresh hit fresh artist",
				"wasNegativeCache": false,
			}),
		},
		{
			Name:          "spec_revalidate_upstream_miss_reports_error",
			Path:          "/revalidate?s=Conformance%20Hit&a=Tester",
			Headers:       key,
			WantStatus:    http.StatusOK,
			WantHeaders:   authed,
			WantBodyRegex: regexp.MustCompile(`"updated":false`),
		},
		{
			Name:        "spec_revalidate_missing_artist_400",
			Path:        "/revalidate?s=Fresh%20Hit",
			Headers:     key,
			WantStatus:  http.StatusBadRequest,
			WantHeaders: authed,
			WantBody:    jbody(map[string]interface{}{"error": "song (s) and artist (a) parameters are required"}),
		},
		{
			Name:        "spec_revalidate_not_cached_404",
			Path:        "/revalidate?s=Never%20Cached&a=Nobody",
			Headers:     key,
			WantStatus:  http.StatusNotFound,
			WantHeaders: authed,
			WantBody: jbody(map[string]interface{}{
				"error":    "no cached lyrics found for this query",
				"cacheKey": "ttml_lyrics:never cached nobody",
			}),
		},
		{
			Name:        "spec_revalidate_without_key_401",
			Path:        "/revalidate?s=Fresh%20Hit&a=Fresh%20Artist",
			WantStatus:  http.StatusUnauthorized,
			WantHeaders: map[string]string{"X-Auth-Mode": "cache", "X-RateLimit-Type": "normal"},
		},
	}
}

func providerCacheOnlyScenarios() []Scenario {
	return []Scenario{
		{
			Name:        "spec_ttml_provider_cache_only_503",
			Path:        "/ttml/getLyrics?s=Cache%20Only%20Miss&a=Nobody",
			WantStatus:  http.StatusServiceUnavailable,
			WantHeaders: map[string]string{"X-Cache-Status": "MISS", "X-Provider": "ttml"},
			WantBody: jbody(map[string]interface{}{
				"error":    "Service running in cache-only mode. No cached lyrics available for this query.",
				"provider": "ttml",
			}),
		},
	}
}

func TestConformanceNewUpstreamFailure(t *testing.T) {
	stub := upstreamStub(t)
	base := newServerBase(t, stubEnv(stub.URL), seedLyricsData, seedNegativeData)
	RunSpec(t, base, upstreamFailureScenarios())
}

func TestConformanceNewRevalidate(t *testing.T) {
	stub := upstreamStub(t)
	env := stubEnv(stub.URL)
	env["API_KEY_REQUIRED"] = "true"
	seed := append([]SeedLyrics{{Song: "Fresh Hit", Artist: "Fresh Artist", TTML: "<tt>OLD</tt>"}}, seedLyricsData...)
	base := newServerBase(t, env, seed, seedNegativeData)
	RunSpec(t, base, revalidateScenarios())
}

func TestConformanceNewProviderCacheOnly(t *testing.T) {
	env := generalProfileEnv()
	env["FF_CACHE_ONLY_MODE"] = "true"
	base := newServerBase(t, env, seedLyricsData, seedNegativeData)
	RunSpec(t, base, providerCacheOnlyScenarios())
}
