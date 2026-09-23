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
	"https://docs.betterlyrics.org",
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

var cacheGoneAlternatives = map[string]string{
	"/stats":                    "Request, cache, and performance statistics",
	"/cache/keys":               "List cache keys (paginated)",
	"/cache/debug?key=...":      "Inspect a specific cache entry",
	"/cache/lookup?s=...&a=...": "Check if a song is cached",
}

// cacheGoneScenario takes the alternatives because the BoltDB server still offers
// file backups and dumps, while the Postgres server has retired them.
func cacheGoneScenario(alternatives map[string]string) Scenario {
	return Scenario{
		Name:       "admin_cache_dump_endpoint_gone_410",
		Path:       "/cache",
		WantStatus: http.StatusGone,
		WantBody: jbody(map[string]interface{}{
			"error":        "Endpoint removed",
			"message":      "/cache has been removed. Use the alternatives below.",
			"alternatives": alternatives,
		}),
	}
}

func boltCacheGoneScenario() Scenario {
	alternatives := map[string]string{
		"/cache/backup": "Create a timestamped backup file",
		"/cache/dump":   "Stream the raw BoltDB file as a download",
	}
	for k, v := range cacheGoneAlternatives {
		alternatives[k] = v
	}
	return cacheGoneScenario(alternatives)
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

// specScenarios exercise documented endpoints the frozen contract never covered,
// so every documented response shape is checked against openapi.yaml. They target
// the Postgres server only.
func specScenarios() []Scenario {
	admin := map[string]string{"Authorization": "test-admin-token"}
	return []Scenario{
		{
			Name:        "spec_openapi_json_served",
			Path:        "/openapi.json",
			WantStatus:  http.StatusOK,
			WantHeaders: map[string]string{"Content-Type": "application/json"},
		},
		{
			Name:       "spec_ttml_provider_hit",
			Path:       "/ttml/getLyrics?s=Conformance%20Hit&a=Tester",
			WantStatus: http.StatusOK,
			WantHeaders: map[string]string{
				"X-Cache-Status": "HIT",
				"X-Provider":     "ttml",
			},
			WantBody: jbody(map[string]interface{}{"lyrics": "<tt>HIT</tt>", "provider": "ttml"}),
		},
		{
			Name:        "spec_ttml_provider_sentinel_404",
			Path:        "/ttml/getLyrics?s=Sentinel%20Song&a=Tester",
			WantStatus:  http.StatusNotFound,
			WantHeaders: map[string]string{"X-Cache-Status": "HIT", "X-Provider": "ttml"},
		},
		{
			Name:        "spec_ttml_provider_negative_hit_404",
			Path:        "/ttml/getLyrics?s=Neg%20Song&a=Tester",
			WantStatus:  http.StatusNotFound,
			WantHeaders: map[string]string{"X-Cache-Status": "NEGATIVE_HIT", "X-Provider": "ttml"},
			WantBody: jbody(map[string]interface{}{
				"error":    "Lyrics not available for this track",
				"provider": "ttml",
			}),
		},
		{
			Name:       "spec_kugou_422_missing_song_and_artist",
			Path:       "/kugou/getLyrics",
			WantStatus: http.StatusUnprocessableEntity,
		},
		{
			Name:       "spec_qq_422_missing_song_and_artist",
			Path:       "/qq/getLyrics",
			WantStatus: http.StatusUnprocessableEntity,
		},
		{
			Name:       "spec_revalidate_without_key_401",
			Path:       "/revalidate?s=Conformance%20Hit&a=Tester",
			WantStatus: http.StatusUnauthorized,
			WantBody: jbody(map[string]interface{}{
				"error":   "API key required for revalidation",
				"message": "Provide a valid API key via X-API-Key header",
			}),
		},
		{
			Name:          "spec_health_with_admin_token",
			Path:          "/health",
			Headers:       admin,
			WantStatus:    http.StatusOK,
			WantBodyRegex: regexp.MustCompile(`"tokens":`),
		},
		{
			Name:          "spec_cache_lookup_hit",
			Path:          "/cache/lookup?s=Conformance%20Hit&a=Tester",
			Headers:       admin,
			WantStatus:    http.StatusOK,
			WantBodyRegex: regexp.MustCompile(`"found":true`),
		},
		{
			Name:          "spec_cache_lookup_negative",
			Path:          "/cache/lookup?s=Neg%20Song&a=Tester",
			Headers:       admin,
			WantStatus:    http.StatusOK,
			WantBodyRegex: regexp.MustCompile(`"negative_cache":true`),
		},
		{
			Name:       "spec_cache_lookup_missing_params_400",
			Path:       "/cache/lookup",
			Headers:    admin,
			WantStatus: http.StatusBadRequest,
		},
		{
			Name:          "spec_cache_debug_lyrics",
			Path:          "/cache/debug?key=ttml_lyrics:conformance%20hit%20tester",
			Headers:       admin,
			WantStatus:    http.StatusOK,
			WantBodyRegex: regexp.MustCompile(`"type":"lyrics"`),
		},
		{
			Name:          "spec_cache_debug_sentinel",
			Path:          "/cache/debug?key=ttml_lyrics:sentinel%20song%20tester",
			Headers:       admin,
			WantStatus:    http.StatusOK,
			WantBodyRegex: regexp.MustCompile(`"type":"no_lyrics_sentinel"`),
		},
		{
			Name:          "spec_cache_debug_negative",
			Path:          "/cache/debug?key=ttml_lyrics:neg%20song%20tester",
			Headers:       admin,
			WantStatus:    http.StatusOK,
			WantBodyRegex: regexp.MustCompile(`"type":"negative_cache"`),
		},
		{
			Name:       "spec_cache_debug_missing_key_400",
			Path:       "/cache/debug",
			Headers:    admin,
			WantStatus: http.StatusBadRequest,
		},
		{
			Name:          "spec_cache_keys",
			Path:          "/cache/keys?prefix=ttml_lyrics:",
			Headers:       admin,
			WantStatus:    http.StatusOK,
			WantBodyRegex: regexp.MustCompile(`"keys":\[\{`),
		},
		{
			Name:       "spec_cache_clear_unknown_provider_400",
			Path:       "/cache/clear/nope",
			Headers:    admin,
			WantStatus: http.StatusBadRequest,
		},
		{
			Name:       "spec_retired_endpoint_410",
			Path:       "/cache/backup",
			Headers:    admin,
			WantStatus: http.StatusGone,
		},
	}
}
