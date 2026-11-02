package binilink

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func isPositiveDecimal(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return math.MaxUint64, true
	}
	return n, n > 0
}

func FuzzRetryAfter(f *testing.F) {
	for _, seed := range []string{
		"", "0", "1", "120", "007", "86399", "86400", "86401", "-1", "-0", "+5", " 5", "5 ",
		"1.5", "1e3", "0x10", "abc", "Wed, 21 Oct 2015 07:28:00 GMT", "９", "\x00",
		"9223372036854775807", "9223372036854775808", "99999999999999999999999",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, header string) {
		got := retryAfter(header)
		if got < 0 || got > maxRetryAfter {
			t.Fatalf("retryAfter(%q) = %v, outside [0, 24h]", header, got)
		}
		n, positive := isPositiveDecimal(header)
		if !positive {
			if got != 0 {
				t.Fatalf("retryAfter(%q) = %v, want 0 for a value that is not a positive decimal integer", header, got)
			}
			return
		}
		if n <= uint64(math.MaxInt64) {
			want := time.Duration(min(n, uint64(maxRetryAfter/time.Second))) * time.Second
			if got != want {
				t.Fatalf("retryAfter(%q) = %v, want %v", header, got, want)
			}
		}
	})
}

func scheduleStep(attempts int) time.Duration {
	switch {
	case attempts <= 1:
		return time.Minute
	case attempts >= len(backoffSchedule):
		return backoffSchedule[len(backoffSchedule)-1]
	}
	return backoffSchedule[attempts-1]
}

func FuzzBackoff(f *testing.F) {
	for _, seed := range []struct {
		attempts   int
		retryAfter int64
	}{
		{0, 0}, {1, 0}, {2, 0}, {5, 0}, {6, 0}, {10, 0}, {-1, 0},
		{math.MinInt, 0}, {math.MaxInt, 0}, {math.MinInt + 1, int64(time.Hour)},
		{1, int64(time.Second)}, {1, int64(90 * time.Second)}, {3, int64(time.Hour)},
		{2, int64(48 * time.Hour)}, {1, -1}, {1, math.MinInt64}, {1, math.MaxInt64},
	} {
		f.Add(seed.attempts, seed.retryAfter)
	}
	f.Fuzz(func(t *testing.T, attempts int, retryAfterNs int64) {
		retryAfter := time.Duration(retryAfterNs)
		got := backoff(attempts, retryAfter)
		if got < time.Minute || got > maxRetryAfter {
			t.Fatalf("backoff(%d, %v) = %v, outside [1m, 24h]", attempts, retryAfter, got)
		}
		if step := scheduleStep(attempts); got < step {
			t.Fatalf("backoff(%d, %v) = %v, below the schedule step %v", attempts, retryAfter, got, step)
		}
		if floor := min(retryAfter, maxRetryAfter); got < floor {
			t.Fatalf("backoff(%d, %v) = %v, below min(Retry-After, 24h) %v", attempts, retryAfter, got, floor)
		}
	})
}

func FuzzNormalizeBaseURL(f *testing.F) {
	for _, seed := range []string{
		"https://link.binimum.org", "https://link.binimum.org/", "https://link.binimum.org//",
		"http://127.0.0.1:8080", "https://example.com/proxy/", "HTTPS://Example.COM/a/b/",
		"http://[::1]:9/x", "https://user:secret@link.binimum.org", "https://user@link.binimum.org/",
		"https://link.binimum.org?x=1", "https://link.binimum.org#f", "https://", "ftp://x", "//host/path",
		"https://host/%2F", "https://host/%zz", "https://host/a b", "https://host/./", "https://host/..",
		"https://host\\@evil", "https:host", "http://host:port", "", "https://h/\x7f", "https://h/é/",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got, err := normalizeBaseURL(raw)
		input, inputErr := url.Parse(raw)
		if err != nil {
			if inputErr == nil && input.User != nil && raw != "" && strings.Contains(err.Error(), raw) {
				t.Fatalf("error %q echoes an input with credentials", err)
			}
			return
		}
		if inputErr != nil {
			t.Fatalf("normalizeBaseURL(%q) accepted an input url.Parse rejects: %v", raw, inputErr)
		}
		if strings.HasSuffix(got, "/") {
			t.Fatalf("normalizeBaseURL(%q) = %q has a trailing slash", raw, got)
		}
		if strings.ContainsAny(got, "?#") {
			t.Fatalf("normalizeBaseURL(%q) = %q contains a query or fragment marker", raw, got)
		}
		result, err := url.Parse(got)
		if err != nil {
			t.Fatalf("normalizeBaseURL(%q) = %q does not parse: %v", raw, got, err)
		}
		if (result.Scheme != "http" && result.Scheme != "https") || result.Host == "" || result.User != nil {
			t.Fatalf("normalizeBaseURL(%q) = %q: scheme %q host %q user %v", raw, got, result.Scheme, result.Host, result.User)
		}
		endpoint, err := url.Parse(got + ingressPath)
		if err != nil {
			t.Fatalf("endpoint for %q does not parse: %v", raw, err)
		}
		if endpoint.Scheme != input.Scheme || endpoint.Host != input.Host {
			t.Fatalf("endpoint for %q is %s://%s, want %s://%s", raw, endpoint.Scheme, endpoint.Host, input.Scheme, input.Host)
		}
		if !strings.HasSuffix(endpoint.Path, ingressPath) || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			t.Fatalf("endpoint for %q has path %q query %q fragment %q", raw, endpoint.Path, endpoint.RawQuery, endpoint.Fragment)
		}
	})
}

type capturedPost struct {
	method, path, key string
	body              []byte
}

func plainHeaderValue(s string) bool {
	if s == "" || s[0] == ' ' || s[len(s)-1] == ' ' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func FuzzPostBody(f *testing.F) {
	for _, seed := range []struct{ videoID, appleTrackID, key string }{
		{"qrO4YZeyl0I", "1440815557", "secret"},
		{"", "", "k"},
		{`a"b\c`, "\u2028\u2029", "key with spaces"},
		{"<script>&amp;", "\x00\x01\x1f", "k"},
		{"日本語", "😀", "k"},
		{"\xff\xfe", "1", "k"},
		{"x", "y", "bad\nkey"},
		{"x", "y", " padded "},
	} {
		f.Add(seed.videoID, seed.appleTrackID, seed.key)
	}

	var mu sync.Mutex
	var last *capturedPost
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		last = &capturedPost{method: r.Method, path: r.URL.Path, key: r.Header.Get("X-Ingress-Key"), body: body}
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	client := srv.Client()

	f.Fuzz(func(t *testing.T, videoID, appleTrackID, key string) {
		mu.Lock()
		last = nil
		mu.Unlock()

		outcome, _, _ := Post(client, srv.URL, key, "ua", videoID, appleTrackID)

		mu.Lock()
		got := last
		mu.Unlock()
		if !plainHeaderValue(key) {
			if got != nil && got.key != strings.Trim(key, " \t") {
				t.Fatalf("key %q arrived as %q", key, got.key)
			}
			return
		}
		if got == nil || outcome != OutcomeLinked {
			t.Fatalf("request with key %q never reached the server (outcome %v)", key, outcome)
		}
		if got.method != http.MethodPost || got.path != ingressPath {
			t.Fatalf("method %q path %q", got.method, got.path)
		}
		if got.key != key {
			t.Fatalf("X-Ingress-Key = %q, want %q", got.key, key)
		}
		var decoded map[string]any
		if err := json.Unmarshal(got.body, &decoded); err != nil {
			t.Fatalf("body %q is not JSON: %v", got.body, err)
		}
		if len(decoded) != 2 {
			t.Fatalf("body %q has %d keys, want 2", got.body, len(decoded))
		}
		gotVideo, ok1 := decoded["youtube_id"].(string)
		gotApple, ok2 := decoded["apple_music_id"].(string)
		if !ok1 || !ok2 {
			t.Fatalf("body %q: youtube_id and apple_music_id must be strings", got.body)
		}
		if utf8.ValidString(videoID) && gotVideo != videoID {
			t.Fatalf("youtube_id = %q, want %q", gotVideo, videoID)
		}
		if utf8.ValidString(appleTrackID) && gotApple != appleTrackID {
			t.Fatalf("apple_music_id = %q, want %q", gotApple, appleTrackID)
		}
	})
}
