package main

import (
	"strings"
	"testing"
)

func TestGzipStringRoundTrip(t *testing.T) {
	inputs := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"ascii", "legacy bolt value"},
		{"unicode", "歌詞 / lyrics / كلمات"},
		{"large repetitive", strings.Repeat("row ", 20_000)},
	}

	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			blob, err := gzipString(tt.in)
			if err != nil {
				t.Fatalf("gzipString: %v", err)
			}
			out, err := gunzipString(blob)
			if err != nil {
				t.Fatalf("gunzipString: %v", err)
			}
			if out != tt.in {
				t.Fatalf("round trip mismatch: got %d bytes, want %d bytes", len(out), len(tt.in))
			}
		})
	}
}

func TestGunzipStringRejectsNonGzip(t *testing.T) {
	if _, err := gunzipString([]byte{0x00, 0x01, 0x02, 0x03}); err == nil {
		t.Fatal("expected error decoding non-gzip input, got nil")
	}
}
