package bini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"lyrics-api-go/services/providers"
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

func TestContribute_NoOpWhenKeyEmpty(t *testing.T) {
	hits := withServer(t, "", func(w http.ResponseWriter, r *http.Request) {
		t.Error("server must not be called when the ingress key is empty")
	}, func() {
		Contribute("Blinding Lights", "The Weeknd", "USUG11904206", providers.SourceApple, sampleAttrs, sampleTTML)
	})
	if hits != 0 {
		t.Errorf("hits = %d, want 0", hits)
	}
}

func TestContribute_SkipsNonAppleSource(t *testing.T) {
	for _, src := range []string{providers.SourceLRCRed, "", "other"} {
		t.Run(src, func(t *testing.T) {
			hits := withServer(t, "secret", func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("server must not be called for non-Apple source %q", src)
			}, func() {
				Contribute("t", "a", "USUG11904206", src, sampleAttrs, sampleTTML)
			})
			if hits != 0 {
				t.Errorf("source %q: hits = %d, want 0", src, hits)
			}
		})
	}
}

func TestContribute_SkipsBadInput(t *testing.T) {
	cases := []struct {
		name  string
		isrc  string
		attrs string
		ttml  string
	}{
		{"missing isrc blocks both legs", "", sampleAttrs, sampleTTML},
		{"malformed isrc blocks both legs", "US-BAD", sampleAttrs, sampleTTML},
		{"empty metadata and lyric", "USUG11904206", "", ""},
		{"oversized lyric, no metadata", "USUG11904206", "", strings.Repeat("a", maxLyricBytes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits := withServer(t, "secret", func(w http.ResponseWriter, r *http.Request) {
				t.Error("server must not be called for bad input")
			}, func() {
				Contribute("t", "a", tc.isrc, providers.SourceApple, tc.attrs, tc.ttml)
			})
			if hits != 0 {
				t.Errorf("hits = %d, want 0", hits)
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
		if r.URL.Query().Get("isrc") != "USUG11904206" {
			t.Errorf("isrc query = %q, want USUG11904206", r.URL.Query().Get("isrc"))
		}
		if r.Header.Get("X-Ingress-Key") != "secret-key" {
			t.Errorf("X-Ingress-Key = %q", r.Header.Get("X-Ingress-Key"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"live","action":"insert"}`))
	}, func() {
		Contribute("Blinding Lights", "The Weeknd", "usug11904206", providers.SourceApple, sampleAttrs, sampleTTML)
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
		Contribute("t", "a", "USUG11904206", providers.SourceApple, sampleAttrs, "")
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
		Contribute("t", "a", "USUG11904206", providers.SourceApple, strings.Repeat("x", maxMetadataBytes+1), sampleTTML)
	})
	if hits != 1 || gotPath != "/ingress/lyrics" {
		t.Errorf("hits=%d path=%q, want oversized metadata skipped and only lyric posted", hits, gotPath)
	}
}

func TestContribute_ErrorStatusesNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 422, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			hits := withServer(t, "secret-key", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"nope"}`))
			}, func() {
				// metadata leg only, to count a single request per status deterministically
				Contribute("t", "a", "USUG11904206", providers.SourceApple, sampleAttrs, "")
			})
			if hits != 1 {
				t.Errorf("status %d: hits = %d, want 1 (no retry)", status, hits)
			}
		})
	}
}

// TestPostLyrics_NeverContributes documents that the legacy shim, which cannot
// pass provenance, never contributes (it forwards an empty source).
func TestPostLyrics_NeverContributes(t *testing.T) {
	hits := withServer(t, "secret-key", func(w http.ResponseWriter, r *http.Request) {
		t.Error("legacy PostLyrics must not contribute (no provenance available)")
	}, func() {
		PostLyrics("Blinding Lights", "The Weeknd", "After Hours", 200, sampleTTML, "USUG11904206")
	})
	if hits != 0 {
		t.Errorf("hits = %d, want 0", hits)
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
