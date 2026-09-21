package ttml

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// loadFixture reads a real document captured from lrc.red's public read API.
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// TestParseRealLRCRedDocument runs the existing TTML parser over a real lrc.red
// document (lrc:timing="Word", namespace http://lrc.red/lyric-ttml-internal) and
// asserts word-level spans parse correctly. This proves the parser handles
// lrc.red's dialect without changes, rather than assuming it.
func TestParseRealLRCRedDocument(t *testing.T) {
	doc := loadFixture(t, "lrcred_blinding_lights.ttml")

	lines, timingType, err := parseTTMLToLines(doc)
	if err != nil {
		t.Fatalf("parseTTMLToLines returned error: %v", err)
	}

	// lrc:timing="Word" must be picked up by the generic timing attr tag.
	if timingType != "word" {
		t.Errorf("timingType = %q, want %q (lrc:timing not recognized)", timingType, "word")
	}

	if len(lines) == 0 {
		t.Fatal("no lines parsed from real lrc.red document")
	}

	first := lines[0]
	if first.Words != "I been tryna call" {
		t.Errorf("first line words = %q, want %q", first.Words, "I been tryna call")
	}

	if len(first.Syllables) < 4 {
		t.Fatalf("first line has %d syllables, want at least 4 word-level spans", len(first.Syllables))
	}

	// Every syllable must carry numeric, non-negative, ordered timing.
	sawRealDuration := false
	for i, syl := range first.Syllables {
		start, err := strconv.ParseInt(syl.StartTime, 10, 64)
		if err != nil || start < 0 {
			t.Errorf("syllable %d start %q not a valid ms value", i, syl.StartTime)
		}
		end, err := strconv.ParseInt(syl.EndTime, 10, 64)
		if err != nil || end < start {
			t.Errorf("syllable %d end %q invalid (start %q)", i, syl.EndTime, syl.StartTime)
		}
		if end > start {
			sawRealDuration = true
		}
	}
	if !sawRealDuration {
		t.Error("no syllable carried a real (end>start) word duration")
	}

	// Line start time is the earliest span begin ("27.395" -> 27395ms).
	if first.StartTimeMs != "27395" {
		t.Errorf("first line startMs = %q, want %q", first.StartTimeMs, "27395")
	}

	// The document is agent-tagged (ttm:agent="v1"); the parser resolves it.
	if first.Agent == "" {
		t.Error("expected an agent on the first line")
	}
}

func TestFetchLRCRedByISRC(t *testing.T) {
	const knownISRC = "USUG11904206"
	body := `<tt xmlns="http://www.w3.org/ns/ttml"><body><div><p begin="1.0" end="2.0">hi</p></div></body></tt>`

	tests := []struct {
		name       string
		isrc       string
		status     int
		respBody   string
		wantOK     bool
		wantErr    bool
		wantHit    bool // whether the server should be reached at all
		wantResult string
	}{
		{name: "200 returns body", isrc: knownISRC, status: 200, respBody: body, wantOK: true, wantHit: true, wantResult: body},
		{name: "404 clean miss", isrc: knownISRC, status: 404, wantOK: false, wantHit: true},
		{name: "500 is an error", isrc: knownISRC, status: 500, wantErr: true, wantHit: true},
		{name: "200 empty body is a miss", isrc: knownISRC, status: 200, respBody: "", wantOK: false, wantHit: true},
		{name: "empty isrc makes no request", isrc: "", wantOK: false, wantHit: false},
		{name: "malformed isrc makes no request", isrc: "US-BAD", wantOK: false, wantHit: false},
		{name: "lowercase isrc normalized in path", isrc: "usug11904206", status: 200, respBody: body, wantOK: true, wantHit: true, wantResult: body},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hit := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hit = true
				if r.URL.Path != "/s/"+knownISRC+".ttml" {
					t.Errorf("unexpected request path %q (isrc not uppercased?)", r.URL.Path)
				}
				if ua := r.Header.Get("User-Agent"); ua == "" {
					t.Error("no User-Agent sent to lrc.red")
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.respBody))
			}))
			defer srv.Close()

			old := lrcRedReadBase
			lrcRedReadBase = srv.URL
			defer func() { lrcRedReadBase = old }()

			got, ok, err := fetchLRCRedByISRC(tt.isrc)

			if hit != tt.wantHit {
				t.Errorf("server hit = %v, want %v", hit, tt.wantHit)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantResult != "" && got != tt.wantResult {
				t.Errorf("body = %q, want %q", got, tt.wantResult)
			}
		})
	}
}
