package binilink

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPostClassifiesStatus(t *testing.T) {
	cases := []struct {
		code int
		want Outcome
	}{
		{http.StatusCreated, OutcomeLinked},
		{http.StatusOK, OutcomeExists},
		{http.StatusConflict, OutcomeConflict},
		{http.StatusNotFound, OutcomeUnresolved},
		{http.StatusUnprocessableEntity, OutcomeNoISRC},
		{http.StatusBadRequest, OutcomeInvalid},
		{http.StatusRequestEntityTooLarge, OutcomeInvalid},
		{http.StatusUnauthorized, OutcomeUnauthorized},
		{http.StatusServiceUnavailable, OutcomeRetry},
		{http.StatusInternalServerError, OutcomeRetry},
		{http.StatusTooManyRequests, OutcomeRetry},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.code)
		}))
		got, _ := Post(srv.Client(), srv.URL, "k", "ua", "qrO4YZeyl0I", "1440815557")
		srv.Close()
		if got != c.want {
			t.Errorf("HTTP %d: got %v want %v", c.code, got, c.want)
		}
	}
}

func TestPostSendsExactRequest(t *testing.T) {
	var gotMethod, gotKey, gotPath, gotCT, gotUA string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotKey, gotCT, gotUA = r.Header.Get("X-Ingress-Key"), r.Header.Get("Content-Type"), r.Header.Get("User-Agent")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	Post(srv.Client(), srv.URL, "secret", "better-lyrics-api", "qrO4YZeyl0I", "0001440815557")

	if gotMethod != http.MethodPost || gotPath != "/api/v1/ingress/youtube" {
		t.Fatalf("method=%q path=%q", gotMethod, gotPath)
	}
	if gotKey != "secret" || gotCT != "application/json" || gotUA != "better-lyrics-api" {
		t.Fatalf("key=%q ct=%q ua=%q", gotKey, gotCT, gotUA)
	}
	if len(body) != 2 || body["youtube_id"] != "qrO4YZeyl0I" || body["apple_music_id"] != "0001440815557" {
		t.Fatalf("body %+v", body)
	}
}

func TestPostRetryAfter(t *testing.T) {
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"120", 120 * time.Second},
		{"", 0},
		{"soon", 0},
		{"-5", 0},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c.header != "" {
				w.Header().Set("Retry-After", c.header)
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		got, after := Post(srv.Client(), srv.URL, "k", "ua", "qrO4YZeyl0I", "1")
		srv.Close()
		if got != OutcomeRetry || after != c.want {
			t.Errorf("Retry-After %q: got %v %v, want retry %v", c.header, got, after, c.want)
		}
	}
}

func TestPostNetworkErrorRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	got, _ := Post(http.DefaultClient, url, "k", "ua", "qrO4YZeyl0I", "1")
	if got != OutcomeRetry {
		t.Fatalf("got %v", got)
	}
}

func TestPostTimeoutRetries(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	client := &http.Client{Timeout: 50 * time.Millisecond}
	got, _ := Post(client, srv.URL, "k", "ua", "qrO4YZeyl0I", "1")
	if got != OutcomeRetry {
		t.Fatalf("got %v", got)
	}
}
