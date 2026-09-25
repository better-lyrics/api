package httpapi

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lyrics-api-go/config"
)

func compressTestHandler() http.Handler {
	var cfg config.Config
	cfg.Configuration.RateLimitPerSecond = 1000
	cfg.Configuration.RateLimitBurstLimit = 1000
	cfg.Configuration.CachedRateLimitPerSecond = 1000
	cfg.Configuration.CachedRateLimitBurstLimit = 1000
	return New(nil, cfg).Handler()
}

func serve(t *testing.T, h http.Handler, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	return rec
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return out
}

func TestResponseCompression(t *testing.T) {
	h := compressTestHandler()
	plain := serve(t, h, "")

	t.Run("gzip when the client accepts it", func(t *testing.T) {
		rec := serve(t, h, "gzip, br")
		if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding: got %q want gzip", got)
		}
		if rec.Body.Len() >= plain.Body.Len() {
			t.Errorf("compressed body %d bytes is not smaller than plain %d", rec.Body.Len(), plain.Body.Len())
		}
		if !bytes.Equal(gunzip(t, rec.Body.Bytes()), plain.Body.Bytes()) {
			t.Error("decompressed body differs from the uncompressed response")
		}
	})

	t.Run("Vary tells caches the body depends on Accept-Encoding", func(t *testing.T) {
		for _, ae := range []string{"", "gzip"} {
			rec := serve(t, h, ae)
			if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
				t.Errorf("Accept-Encoding %q: Vary %q lacks Accept-Encoding", ae, rec.Header().Get("Vary"))
			}
		}
	})

	t.Run("handler headers survive compression", func(t *testing.T) {
		rec := serve(t, h, "gzip")
		if got, want := rec.Header().Get("Content-Type"), plain.Header().Get("Content-Type"); got != want {
			t.Errorf("Content-Type: got %q want %q", got, want)
		}
	})
}

func TestResponseCompressionEdgeCases(t *testing.T) {
	h := compressTestHandler()
	plain := serve(t, h, "")

	cases := []struct {
		name           string
		acceptEncoding string
	}{
		{"no Accept-Encoding", ""},
		{"identity only", "identity"},
		{"gzip explicitly refused", "gzip;q=0"},
		{"zstd only is not offered", "zstd"},
		{"unknown encoding", "compress"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := serve(t, h, c.acceptEncoding)
			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Errorf("Content-Encoding: got %q want none", got)
			}
			if !bytes.Equal(rec.Body.Bytes(), plain.Body.Bytes()) {
				t.Error("body differs from the plain response")
			}
		})
	}

	t.Run("small bodies are left uncompressed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/cache/backup", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Body.Len() >= 1024 {
			t.Skipf("fixture body is %d bytes, not small", rec.Body.Len())
		}
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding: got %q want none for a %d byte body", got, rec.Body.Len())
		}
	})
}
