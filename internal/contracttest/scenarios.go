package contracttest

import (
	"encoding/json"
	"net/http"
	"regexp"
)

const plainUTF8 = "text/plain; charset=utf-8"

var emptyBody = regexp.MustCompile(`^$`)

func jbody(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b) + "\n"
}

var seedLyricsData = []SeedLyrics{
	{Song: "Conformance Hit", Artist: "Tester", TTML: "<tt>HIT</tt>"},
	{Song: "Sentinel Song", Artist: "Tester", TTML: "__NO_LYRICS__"},
}

var seedNegativeData = []SeedNegative{
	{Song: "Neg Song", Artist: "Tester", Reason: "Lyrics not available for this track"},
}

var corsAllowedOrigins = []string{
	"https://music.youtube.com",
	"http://localhost:3000",
	"https://lyrics-api-docs.boidu.dev",
	"https://braccato.boidu.dev",
	"https://composer.boidu.dev",
	"https://composer.betterlyrics.org",
}

func corsScenarios() []Scenario {
	var s []Scenario
	for _, origin := range corsAllowedOrigins {
		s = append(s, Scenario{
			Name:       "cors_actual_get_allowed_" + origin,
			Path:       "/health",
			Headers:    map[string]string{"Origin": origin},
			WantStatus: http.StatusOK,
			WantHeaders: map[string]string{
				"Access-Control-Allow-Origin":      origin,
				"Access-Control-Allow-Credentials": "true",
			},
			AbsentHeaders: []string{"Access-Control-Expose-Headers"},
		})
	}
	s = append(s,
		Scenario{
			Name:          "cors_actual_get_disallowed_origin",
			Path:          "/health",
			Headers:       map[string]string{"Origin": "https://evil.example"},
			WantStatus:    http.StatusOK,
			AbsentHeaders: []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials"},
		},
		Scenario{
			Name:   "cors_preflight_allowed_origin",
			Method: http.MethodOptions,
			Path:   "/getLyrics",
			Headers: map[string]string{
				"Origin":                        "https://music.youtube.com",
				"Access-Control-Request-Method": "GET",
			},
			WantStatus: http.StatusNoContent,
			WantHeaders: map[string]string{
				"Access-Control-Allow-Origin":      "https://music.youtube.com",
				"Access-Control-Allow-Credentials": "true",
				"Access-Control-Allow-Methods":     "GET",
			},
		},
		Scenario{
			Name:   "cors_preflight_disallowed_request_header_aborts",
			Method: http.MethodOptions,
			Path:   "/getLyrics",
			Headers: map[string]string{
				"Origin":                         "https://music.youtube.com",
				"Access-Control-Request-Method":  "GET",
				"Access-Control-Request-Headers": "X-API-Key",
			},
			WantStatus:    http.StatusNoContent,
			AbsentHeaders: []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods"},
		},
	)
	return s
}

func authScenarios() []Scenario {
	return []Scenario{
		{
			Name:       "auth_cache_hit_without_key_is_public",
			Path:       "/getLyrics?s=Conformance%20Hit&a=Tester",
			WantStatus: http.StatusOK,
			WantHeaders: map[string]string{
				"X-Cache-Status": "HIT",
				"X-Auth-Mode":    "cache",
			},
			WantBody: jbody(map[string]interface{}{"ttml": "<tt>HIT</tt>"}),
		},
		{
			Name:       "auth_cache_hit_with_valid_key_authenticated",
			Path:       "/getLyrics?s=Conformance%20Hit&a=Tester",
			Headers:    map[string]string{"X-API-Key": "test-api-key"},
			WantStatus: http.StatusOK,
			WantHeaders: map[string]string{
				"X-Cache-Status": "HIT",
				"X-Auth-Mode":    "authenticated",
			},
			WantBody: jbody(map[string]interface{}{"ttml": "<tt>HIT</tt>"}),
		},
		{
			Name:       "auth_uncached_no_key_401",
			Path:       "/getLyrics?s=Auth%20Miss&a=Nobody",
			WantStatus: http.StatusUnauthorized,
			WantHeaders: map[string]string{
				"X-Cache-Status": "MISS",
				"X-Auth-Mode":    "cache",
			},
			WantBody: jbody(map[string]interface{}{
				"error":   "API key required",
				"message": "Uncached queries require a valid API key via X-API-Key header",
			}),
		},
		{
			Name:       "auth_uncached_wrong_key_401",
			Path:       "/getLyrics?s=Auth%20Miss&a=Nobody",
			Headers:    map[string]string{"X-API-Key": "wrong-key"},
			WantStatus: http.StatusUnauthorized,
			WantHeaders: map[string]string{
				"X-Cache-Status": "MISS",
				"X-Auth-Mode":    "invalid",
			},
			WantBody: jbody(map[string]interface{}{
				"error":   "Invalid API key",
				"message": "The provided API key is not valid",
			}),
		},
	}
}

func rateLimitScenarios() []Scenario {
	return []Scenario{
		{
			Name:        "rl_1_normal_tier_serves_hit",
			Path:        "/getLyrics?s=Conformance%20Hit&a=Tester",
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"X-RateLimit-Type": "normal", "X-Cache-Status": "HIT"},
		},
		{
			Name:       "rl_2_cached_tier_uncached_returns_429_retry60",
			Path:       "/getLyrics?s=RL%20Miss&a=Nobody",
			WantStatus: http.StatusTooManyRequests,
			WantHeaders: map[string]string{
				"X-RateLimit-Type": "cached",
				"X-Cache-Status":   "MISS",
				"Retry-After":      "60",
			},
			WantBody: jbody(map[string]interface{}{
				"error":   "Rate limit exceeded. This request requires cached data, but no cache is available for this query.",
				"message": "Please try again later or reduce your request rate.",
			}),
		},
		{
			Name:       "rl_3_both_tiers_exhausted_returns_429_retry1",
			Path:       "/getLyrics?s=RL%20Miss2&a=Nobody",
			WantStatus: http.StatusTooManyRequests,
			WantHeaders: map[string]string{
				"X-RateLimit-Type":       "exceeded",
				"X-RateLimit-Remaining":  "0",
				"Retry-After":            "1",
				"Content-Type":           plainUTF8,
				"X-Content-Type-Options": "nosniff",
			},
			WantBody: "Too Many Requests\n",
		},
	}
}

type adminGate struct {
	method string
	path   string
}

func adminScenarios() []Scenario {
	gates := []adminGate{
		{http.MethodGet, "/stats"},
		{http.MethodGet, "/circuit-breaker"},
		{http.MethodGet, "/circuit-breaker/reset"},
		{http.MethodGet, "/circuit-breaker/simulate-failure"},
		{http.MethodGet, "/health/mut"},
		{http.MethodGet, "/metadata"},
		{http.MethodGet, "/metadata/stats"},
		{http.MethodGet, "/metadata/sample"},
		{http.MethodPost, "/video-map"},
		{http.MethodGet, "/cache/lookup"},
		{http.MethodGet, "/cache/keys"},
		{http.MethodGet, "/cache/debug"},
		{http.MethodGet, "/cache/backup"},
		{http.MethodGet, "/cache/backups"},
		{http.MethodGet, "/cache/restore"},
		{http.MethodGet, "/cache/clear"},
		{http.MethodGet, "/cache/migrate"},
		{http.MethodGet, "/cache/migrate/status"},
		{http.MethodGet, "/cache/dump"},
		{http.MethodGet, "/test-notifications"},
	}
	s := make([]Scenario, 0, len(gates)+4)
	for _, g := range gates {
		s = append(s, Scenario{
			Name:       "admin_gate_401_" + g.method + "_" + g.path,
			Method:     g.method,
			Path:       g.path,
			WantStatus: http.StatusUnauthorized,
			WantHeaders: map[string]string{
				"Content-Type":           plainUTF8,
				"X-Content-Type-Options": "nosniff",
			},
			WantBody: "Unauthorized\n",
		})
	}
	s = append(s,
		Scenario{
			Name:        "admin_cache_help_public_200",
			Path:        "/cache/help",
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"Content-Type": "application/json"},
		},
		Scenario{
			Name:       "admin_cache_dump_endpoint_gone_410",
			Path:       "/cache",
			WantStatus: http.StatusGone,
			WantBody: jbody(map[string]interface{}{
				"error":   "Endpoint removed",
				"message": "/cache has been removed. Use the alternatives below.",
				"alternatives": map[string]string{
					"/stats":                    "Request, cache, and performance statistics",
					"/cache/keys":               "List cache keys (paginated)",
					"/cache/debug?key=...":      "Inspect a specific cache entry",
					"/cache/lookup?s=...&a=...": "Check if a song is cached",
					"/cache/backup":             "Create a timestamped backup file",
					"/cache/dump":               "Stream the raw BoltDB file as a download",
				},
			}),
		},
		Scenario{
			Name:        "admin_stats_authorized_200",
			Path:        "/stats",
			Headers:     map[string]string{"Authorization": "test-admin-token"},
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"Content-Type": "application/json"},
		},
		Scenario{
			Name:        "admin_circuit_breaker_authorized_200",
			Path:        "/circuit-breaker",
			Headers:     map[string]string{"Authorization": "test-admin-token"},
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"Content-Type": "application/json"},
		},
	)
	return s
}

func seededGetLyricsScenarios() []Scenario {
	return []Scenario{
		{
			Name:       "getlyrics_hit_returns_ttml_only",
			Path:       "/getLyrics?s=Conformance%20Hit&a=Tester",
			WantStatus: http.StatusOK,
			WantHeaders: map[string]string{
				"Content-Type":   "application/json",
				"X-Cache-Status": "HIT",
			},
			WantBody: jbody(map[string]interface{}{"ttml": "<tt>HIT</tt>"}),
		},
		{
			Name:        "getlyrics_sentinel_returns_404",
			Path:        "/getLyrics?s=Sentinel%20Song&a=Tester",
			WantStatus:  http.StatusNotFound,
			WantHeaders: map[string]string{"X-Cache-Status": "HIT"},
			WantBody:    jbody(map[string]interface{}{"error": "No lyrics available for this track"}),
		},
		{
			Name:        "getlyrics_negative_hit_returns_404",
			Path:        "/getLyrics?s=Neg%20Song&a=Tester",
			WantStatus:  http.StatusNotFound,
			WantHeaders: map[string]string{"X-Cache-Status": "NEGATIVE_HIT"},
			WantBody:    jbody(map[string]interface{}{"error": "Lyrics not available for this track"}),
		},
	}
}

func smokeScenarios() []Scenario {
	return []Scenario{
		{
			Name:        "framework_404_unmatched_path",
			Path:        "/definitely-not-a-route",
			WantStatus:  http.StatusNotFound,
			WantHeaders: map[string]string{"Content-Type": plainUTF8},
			WantBody:    "404 page not found\n",
		},
		{
			Name:          "framework_405_wrong_method_on_video_map",
			Method:        http.MethodGet,
			Path:          "/video-map",
			WantStatus:    http.StatusMethodNotAllowed,
			AbsentHeaders: []string{"Content-Type"},
			WantBodyRegex: emptyBody,
		},
		{
			Name:       "getlyrics_422_missing_song_and_artist",
			Path:       "/getLyrics",
			WantStatus: http.StatusUnprocessableEntity,
			WantHeaders: map[string]string{
				"Content-Type":           plainUTF8,
				"X-Content-Type-Options": "nosniff",
			},
			WantBody: "Song name or artist name not provided\n",
		},
		{
			Name:        "help_root",
			Path:        "/",
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"Content-Type": "application/json"},
		},
		{
			Name:        "health_ok",
			Path:        "/health",
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"Content-Type": "application/json"},
		},
		{
			Name:        "stats_unauthorized_without_token",
			Path:        "/stats",
			WantStatus:  http.StatusUnauthorized,
			WantHeaders: map[string]string{"Content-Type": plainUTF8},
			WantBody:    "Unauthorized\n",
		},
		{
			Name: "cors_allowed_origin_echoed",
			Path: "/health",
			Headers: map[string]string{
				"Origin": "https://music.youtube.com",
			},
			WantStatus: http.StatusOK,
			WantHeaders: map[string]string{
				"Access-Control-Allow-Origin":      "https://music.youtube.com",
				"Access-Control-Allow-Credentials": "true",
			},
		},
		{
			Name: "cors_disallowed_origin_no_headers",
			Path: "/health",
			Headers: map[string]string{
				"Origin": "https://evil.example",
			},
			WantStatus:    http.StatusOK,
			AbsentHeaders: []string{"Access-Control-Allow-Origin"},
		},
		{
			Name:           "ratelimit_type_normal_header",
			Path:           "/health",
			WantStatus:     http.StatusOK,
			WantHeaders:    map[string]string{"X-RateLimit-Type": "normal"},
			PresentHeaders: []string{"X-RateLimit-Limit", "X-RateLimit-Remaining"},
		},
	}
}
