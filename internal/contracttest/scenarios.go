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
