package contracttest

import (
	"net/http"
	"strings"
	"testing"
)

func fakeResponse(status int, contentType string, headers map[string]string) *http.Response {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: status, Header: h}
}

func TestValidateAgainstSpec(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		resp    *http.Response
		body    string
		wantErr string
	}{
		{
			name: "documented hit matches",
			path: "/getLyrics?s=a&a=b",
			resp: fakeResponse(200, "application/json", map[string]string{"X-Cache-Status": "HIT", "X-RateLimit-Type": "normal"}),
			body: `{"ttml":"<tt/>"}`,
		},
		{
			name: "any host matches, not only the production server",
			path: "/health",
			resp: fakeResponse(200, "application/json", nil),
			body: `{"status":"ok","accounts":1,"accounts_active":1,"accounts_out_of_service":0,"circuit_breaker":"CLOSED","cache_ready":true}`,
		},
		{
			name: "text/plain 422 matches",
			path: "/getLyrics",
			resp: fakeResponse(422, "text/plain; charset=utf-8", nil),
			body: "Song name or artist name not provided\n",
		},
		{
			name: "path parameter route matches",
			path: "/cache/clear/nope",
			resp: fakeResponse(400, "application/json", map[string]string{"X-RateLimit-Type": "normal"}),
			body: `{"error":"Unknown provider: nope","valid_providers":["ttml"]}`,
		},
		{
			name: "undocumented route is skipped",
			path: "/stats",
			resp: fakeResponse(418, "text/plain", nil),
			body: "anything",
		},
		{
			name:   "undocumented method is skipped",
			method: http.MethodPost,
			path:   "/health",
			resp:   fakeResponse(405, "", nil),
		},
		{
			name:    "renamed field fails",
			path:    "/getLyrics?s=a",
			resp:    fakeResponse(200, "application/json", map[string]string{"X-Cache-Status": "HIT", "X-RateLimit-Type": "normal"}),
			body:    `{"ttml":"<tt/>","scor":0.5}`,
			wantErr: `"scor" is unsupported`,
		},
		{
			name:    "missing required X-Cache-Status fails",
			path:    "/getLyrics?s=a",
			resp:    fakeResponse(200, "application/json", map[string]string{"X-RateLimit-Type": "normal"}),
			body:    `{"ttml":"<tt/>"}`,
			wantErr: `header "X-Cache-Status" missing`,
		},
		{
			name:    "score above 1 fails",
			path:    "/getLyrics?s=a",
			resp:    fakeResponse(200, "application/json", map[string]string{"X-Cache-Status": "MISS", "X-RateLimit-Type": "normal"}),
			body:    `{"ttml":"<tt/>","score":1.5}`,
			wantErr: `/score`,
		},
		{
			name:    "missing required X-Provider fails",
			path:    "/kugou/getLyrics?s=a",
			resp:    fakeResponse(200, "application/json", map[string]string{"X-Cache-Status": "HIT", "X-RateLimit-Type": "normal"}),
			body:    `{"lyrics":"x","provider":"kugou"}`,
			wantErr: `header "X-Provider" missing`,
		},
		{
			name:    "undeclared status fails",
			path:    "/health",
			resp:    fakeResponse(500, "application/json", nil),
			body:    `{}`,
			wantErr: "status is not supported",
		},
		{
			name:    "missing required field fails",
			path:    "/getLyrics?s=a",
			resp:    fakeResponse(200, "application/json", map[string]string{"X-Cache-Status": "HIT", "X-RateLimit-Type": "normal"}),
			body:    `{"score":1}`,
			wantErr: `property "ttml" is missing`,
		},
		{
			name:    "wrong field type fails",
			path:    "/getLyrics?s=a",
			resp:    fakeResponse(200, "application/json", map[string]string{"X-Cache-Status": "HIT", "X-RateLimit-Type": "normal"}),
			body:    `{"ttml":42}`,
			wantErr: "value must be a string",
		},
		{
			name:    "header outside enum fails",
			path:    "/getLyrics?s=a",
			resp:    fakeResponse(200, "application/json", map[string]string{"X-Cache-Status": "WARM", "X-RateLimit-Type": "normal"}),
			body:    `{"ttml":"<tt/>"}`,
			wantErr: `header "X-Cache-Status" doesn't match schema`,
		},
		{
			name:    "undeclared content type fails",
			path:    "/health",
			resp:    fakeResponse(200, "text/html", nil),
			body:    "<html>",
			wantErr: "text/html",
		},
		{
			name:    "enum violation in body fails",
			path:    "/health",
			resp:    fakeResponse(200, "application/json", nil),
			body:    `{"status":"fine","accounts":1,"accounts_active":1,"accounts_out_of_service":0,"circuit_breaker":"CLOSED","cache_ready":true}`,
			wantErr: "/status",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			req, err := http.NewRequest(method, "http://127.0.0.1:54321"+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = validateAgainstSpec(req, tt.resp, []byte(tt.body))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("want no error, got %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}
