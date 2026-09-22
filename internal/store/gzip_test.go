package store

import (
	"strings"
	"testing"
)

func TestGzipRoundTrip(t *testing.T) {
	inputs := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"ascii", "<tt>hello world</tt>"},
		{"unicode", "秘密のメロディ / café / عربى"},
		{"large repetitive", strings.Repeat("la la la ", 10_000)},
	}

	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			blob, err := gzipBytes(tt.in)
			if err != nil {
				t.Fatalf("gzipBytes: %v", err)
			}
			out, err := gunzipBytes(blob)
			if err != nil {
				t.Fatalf("gunzipBytes: %v", err)
			}
			if out != tt.in {
				t.Fatalf("round trip mismatch: got %d bytes, want %d bytes", len(out), len(tt.in))
			}
		})
	}
}

func TestGunzipBytesRejectsNonGzip(t *testing.T) {
	if _, err := gunzipBytes([]byte("not gzip at all")); err == nil {
		t.Fatal("expected error decoding non-gzip input, got nil")
	}
}

func TestGzipBytesCompressesRepetitiveInput(t *testing.T) {
	in := strings.Repeat("compress me ", 5000)
	blob, err := gzipBytes(in)
	if err != nil {
		t.Fatalf("gzipBytes: %v", err)
	}
	if len(blob) >= len(in) {
		t.Fatalf("expected compressed output smaller than %d bytes, got %d", len(in), len(blob))
	}
}
