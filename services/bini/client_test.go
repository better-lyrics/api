package bini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const sampleTTML = `<tt xmlns="http://www.w3.org/ns/ttml"><body><div><p begin="1.0" end="2.0">hi</p></div></body></tt>`
const sampleAttrs = `{"name":"Blinding Lights","artistName":"The Weeknd","isrc":"USUG11904206"}`

// withServer runs fn against a recording httptest server, with the ingress key
// and base URL overridden. Returns how many times the server was hit.
func withServer(t *testing.T, key string, handler http.HandlerFunc, fn func()) int32 {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler(w, r)
	}))
	defer srv.Close()

	oldKey, oldURL := ingressKey, ingressBaseURL
	ingressKey = func() string { return key }
	ingressBaseURL = srv.URL + "/ingress"
	defer func() { ingressKey, ingressBaseURL = oldKey, oldURL }()

	fn()
	return atomic.LoadInt32(&hits)
}

func TestPostLyrics_NoOpWhenKeyEmpty(t *testing.T) {
	hits := withServer(t, "", func(w http.ResponseWriter, r *http.Request) {
		t.Error("server must not be called when ingress key is empty")
	}, func() {
		PostLyrics("Blinding Lights", "The Weeknd", "After Hours", 200, sampleTTML, "USUG11904206")
	})
	if hits != 0 {
		t.Errorf("hits = %d, want 0", hits)
	}
}

func TestPostLyrics_SkipsBadInput(t *testing.T) {
	cases := []struct {
		name string
		isrc string
		ttml string
	}{
		{"missing isrc", "", sampleTTML},
		{"malformed isrc", "US-BAD", sampleTTML},
		{"empty ttml", "USUG11904206", ""},
		{"oversized ttml", "USUG11904206", strings.Repeat("a", maxLyricBytes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits := withServer(t, "secret", func(w http.ResponseWriter, r *http.Request) {
				t.Error("server must not be called for bad input")
			}, func() {
				PostLyrics("t", "a", "al", 200, tc.ttml, tc.isrc)
			})
			if hits != 0 {
				t.Errorf("hits = %d, want 0", hits)
			}
		})
	}
}

func TestPostLyrics_SuccessSendsRawTTML(t *testing.T) {
	var gotPath, gotQuery, gotKey, gotUA, gotBody string
	hits := withServer(t, "secret-key", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("isrc")
		gotKey = r.Header.Get("X-Ingress-Key")
		gotUA = r.Header.Get("User-Agent")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"live","action":"insert","sync":"word"}`))
	}, func() {
		// lowercase isrc must be normalized to uppercase in the query string.
		PostLyrics("Blinding Lights", "The Weeknd", "After Hours", 200, sampleTTML, "usug11904206")
	})

	if hits != 1 {
		t.Fatalf("hits = %d, want 1 (lyric only, no metadata)", hits)
	}
	if gotPath != "/ingress/lyrics" {
		t.Errorf("path = %q, want /ingress/lyrics", gotPath)
	}
	if gotQuery != "USUG11904206" {
		t.Errorf("isrc query = %q, want USUG11904206", gotQuery)
	}
	if gotKey != "secret-key" {
		t.Errorf("X-Ingress-Key = %q, want secret-key", gotKey)
	}
	if gotUA == "" {
		t.Error("no User-Agent sent to ingress")
	}
	if gotBody != sampleTTML {
		t.Errorf("body = %q, want raw TTML", gotBody)
	}
}

func TestPostLyrics_ErrorStatusesNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 422, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			hits := withServer(t, "secret-key", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"nope"}`))
			}, func() {
				PostLyrics("t", "a", "al", 200, sampleTTML, "USUG11904206")
			})
			if hits != 1 {
				t.Errorf("status %d: hits = %d, want 1 (no retry)", status, hits)
			}
		})
	}
}

func TestContribute_PostsMetadataThenLyric(t *testing.T) {
	var mu sync.Mutex
	var order []string
	bodies := map[string]string{}
	hits := withServer(t, "secret-key", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		order = append(order, r.URL.Path)
		bodies[r.URL.Path] = string(b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"live","action":"insert"}`))
	}, func() {
		Contribute("Blinding Lights", "The Weeknd", "USUG11904206", sampleAttrs, sampleTTML)
	})

	if hits != 2 {
		t.Fatalf("hits = %d, want 2 (metadata + lyric)", hits)
	}
	if len(order) != 2 || order[0] != "/ingress/metadata" || order[1] != "/ingress/lyrics" {
		t.Errorf("call order = %v, want [/ingress/metadata /ingress/lyrics]", order)
	}
	if bodies["/ingress/metadata"] != sampleAttrs {
		t.Errorf("metadata body = %q, want raw attributes", bodies["/ingress/metadata"])
	}
	if bodies["/ingress/lyrics"] != sampleTTML {
		t.Errorf("lyric body = %q, want raw TTML", bodies["/ingress/lyrics"])
	}
}

func TestContribute_MetadataOnly(t *testing.T) {
	var gotPath string
	hits := withServer(t, "secret-key", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"already_in_catalog","action":"skip"}`))
	}, func() {
		Contribute("t", "a", "USUG11904206", sampleAttrs, "")
	})
	if hits != 1 || gotPath != "/ingress/metadata" {
		t.Errorf("hits=%d path=%q, want 1 hit on /ingress/metadata", hits, gotPath)
	}
}

func TestContribute_SkipsOversizedMetadataStillPostsLyric(t *testing.T) {
	var gotPath string
	hits := withServer(t, "secret-key", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"live","action":"insert"}`))
	}, func() {
		Contribute("t", "a", "USUG11904206", strings.Repeat("x", maxMetadataBytes+1), sampleTTML)
	})
	if hits != 1 || gotPath != "/ingress/lyrics" {
		t.Errorf("hits=%d path=%q, want oversized metadata skipped and only lyric posted", hits, gotPath)
	}
}

func TestBuildIngressRequest(t *testing.T) {
	req, err := buildIngressRequest("k", "/metadata", "USUG11904206", sampleAttrs, "application/json")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.Method)
	}
	if req.URL.Path != "/ingress/metadata" {
		t.Errorf("path = %q, want /ingress/metadata", req.URL.Path)
	}
	if req.URL.Query().Get("isrc") != "USUG11904206" {
		t.Errorf("isrc query = %q", req.URL.Query().Get("isrc"))
	}
	if req.Header.Get("X-Ingress-Key") != "k" {
		t.Errorf("key header = %q", req.Header.Get("X-Ingress-Key"))
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", req.Header.Get("Content-Type"))
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != sampleAttrs {
		t.Errorf("body = %q, want raw attributes", string(body))
	}
}
