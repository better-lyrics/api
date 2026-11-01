package ttml

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"lyrics-api-go/stats"
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

			old, oldFlag := lrcRedReadBase, lrcRedSkipAISynced
			lrcRedReadBase, lrcRedSkipAISynced = srv.URL, false
			defer func() { lrcRedReadBase, lrcRedSkipAISynced = old, oldFlag }()

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

// lrcRedStub serves .ttml and .song.json per ISRC and records which paths were requested.
type lrcRedStub struct {
	ttmlStatus int
	ttmlBody   string
	songStatus int
	songBody   string
	songDelay  time.Duration
	ttmlDelay  time.Duration

	mu             sync.Mutex
	paths          []string
	songCancelled  bool
	inFlight       map[string]int
	maxISRCs       int
	maxRequests    int
	activeRequests int
}

func (s *lrcRedStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	isrc := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/s/"), ".ttml"), ".song.json")
	s.mu.Lock()
	s.paths = append(s.paths, r.URL.Path)
	if s.inFlight == nil {
		s.inFlight = map[string]int{}
	}
	s.inFlight[isrc]++
	s.activeRequests++
	s.maxISRCs = max(s.maxISRCs, len(s.inFlight))
	s.maxRequests = max(s.maxRequests, s.activeRequests)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.inFlight[isrc]--; s.inFlight[isrc] == 0 {
			delete(s.inFlight, isrc)
		}
		s.activeRequests--
		s.mu.Unlock()
	}()

	status, body, delay := s.ttmlStatus, s.ttmlBody, s.ttmlDelay
	if strings.HasSuffix(r.URL.Path, ".song.json") {
		status, body, delay = s.songStatus, s.songBody, s.songDelay
	}
	select {
	case <-time.After(delay):
	case <-r.Context().Done():
		if strings.HasSuffix(r.URL.Path, ".song.json") {
			s.mu.Lock()
			s.songCancelled = true
			s.mu.Unlock()
		}
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (s *lrcRedStub) requested(suffix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.paths {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}

func useLRCRedStub(t *testing.T, stub *lrcRedStub, skipAISynced bool) {
	t.Helper()
	srv := httptest.NewServer(stub)
	oldBase, oldFlag, oldClient := lrcRedReadBase, lrcRedSkipAISynced, lrcRedClient
	lrcRedReadBase, lrcRedSkipAISynced = srv.URL, skipAISynced
	t.Cleanup(func() {
		lrcRedReadBase, lrcRedSkipAISynced, lrcRedClient = oldBase, oldFlag, oldClient
		srv.Close()
	})
}

const lrcRedTestTTML = `<tt xmlns="http://www.w3.org/ns/ttml"><body><div><p begin="1.0" end="2.0">hi</p></div></body></tt>`

func TestFetchLRCRedByISRCSkipsAISynced(t *testing.T) {
	tests := []struct {
		name        string
		songStatus  int
		songBody    string
		wantOK      bool
		wantErr     bool
		wantSkipped int64
		wantFlagErr int64
	}{
		{name: "byModel true is a clean miss", songStatus: 200, songBody: `{"name":"Revenge","byModel":true,"lines":[{"t":"x"}]}`, wantSkipped: 1},
		{name: "byModel false is a hit", songStatus: 200, songBody: `{"name":"Espresso","byModel":false}`, wantOK: true},
		{name: "byModel absent is a hit", songStatus: 200, songBody: `{"name":"Espresso","lines":[]}`, wantOK: true},
		{name: "song.json 404 fails closed", songStatus: 404, wantErr: true, wantFlagErr: 1},
		{name: "song.json 500 fails closed", songStatus: 500, wantErr: true, wantFlagErr: 1},
		{name: "malformed song.json fails closed", songStatus: 200, songBody: `{"byModel":`, wantErr: true, wantFlagErr: 1},
		{name: "non-bool byModel fails closed", songStatus: 200, songBody: `{"byModel":"yes"}`, wantErr: true, wantFlagErr: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &lrcRedStub{ttmlStatus: 200, ttmlBody: lrcRedTestTTML, songStatus: tt.songStatus, songBody: tt.songBody}
			useLRCRedStub(t, stub, true)
			skipped0, flagErr0 := stats.Get().LRCRedSkippedByModel.Load(), stats.Get().LRCRedFlagErrors.Load()

			got, ok, err := fetchLRCRedByISRC("QZES72218029")

			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK && got != lrcRedTestTTML {
				t.Errorf("body = %q, want the lrc.red TTML", got)
			}
			if !tt.wantOK && got != "" {
				t.Errorf("a miss or error must not return lyrics, got %q", got)
			}
			if !stub.requested("/s/QZES72218029.song.json") {
				t.Error("flag on must request .song.json")
			}
			if d := stats.Get().LRCRedSkippedByModel.Load() - skipped0; d != tt.wantSkipped {
				t.Errorf("LRCRedSkippedByModel delta = %d, want %d", d, tt.wantSkipped)
			}
			if d := stats.Get().LRCRedFlagErrors.Load() - flagErr0; d != tt.wantFlagErr {
				t.Errorf("LRCRedFlagErrors delta = %d, want %d", d, tt.wantFlagErr)
			}
		})
	}

	t.Run("song.json timeout fails closed", func(t *testing.T) {
		stub := &lrcRedStub{ttmlStatus: 200, ttmlBody: lrcRedTestTTML, songStatus: 200, songDelay: 2 * time.Second}
		useLRCRedStub(t, stub, true)
		lrcRedClient = &http.Client{Timeout: 100 * time.Millisecond}

		_, ok, err := fetchLRCRedByISRC("QZES72218029")
		if err == nil || ok {
			t.Fatalf("ok=%v err=%v, want a timeout error", ok, err)
		}
	})

	t.Run("flag off never requests song.json and serves AI files", func(t *testing.T) {
		stub := &lrcRedStub{ttmlStatus: 200, ttmlBody: lrcRedTestTTML, songStatus: 200, songBody: `{"byModel":true}`}
		useLRCRedStub(t, stub, false)

		got, ok, err := fetchLRCRedByISRC("QZES72218029")
		if err != nil || !ok || got != lrcRedTestTTML {
			t.Fatalf("got (%q, %v, %v), want the TTML hit", got, ok, err)
		}
		if stub.requested(".song.json") {
			t.Error("flag off requested .song.json")
		}
	})

	t.Run("empty base url makes no request", func(t *testing.T) {
		stub := &lrcRedStub{}
		useLRCRedStub(t, stub, true)
		lrcRedReadBase = ""

		if _, ok, err := fetchLRCRedByISRC("QZES72218029"); ok || err != nil {
			t.Fatalf("ok=%v err=%v, want a clean miss", ok, err)
		}
		if len(stub.paths) != 0 {
			t.Errorf("requested %v with no base url", stub.paths)
		}
	})

	for _, status := range []int{404, 500} {
		t.Run(fmt.Sprintf("ttml %d does not wait on song.json and cancels it", status), func(t *testing.T) {
			stub := &lrcRedStub{ttmlStatus: status, songStatus: 200, songBody: `{}`, songDelay: 5 * time.Second}
			useLRCRedStub(t, stub, true)
			flagErr0 := stats.Get().LRCRedFlagErrors.Load()

			start := time.Now()
			_, ok, err := fetchLRCRedByISRC("USUG12004507")
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("took %v, waited on .song.json after a .ttml %d", elapsed, status)
			}
			if ok || (err != nil) != (status == 500) {
				t.Fatalf("ok=%v err=%v for .ttml %d", ok, err, status)
			}
			if d := stats.Get().LRCRedFlagErrors.Load() - flagErr0; d != 0 {
				t.Errorf("a cancelled flag lookup counted as a flag error (%d)", d)
			}
			deadline := time.Now().Add(time.Second)
			for !stub.cancelled() && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if stub.requested(".song.json") && !stub.cancelled() {
				t.Error("in-flight .song.json was not cancelled")
			}
		})
	}
}

func (s *lrcRedStub) cancelled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.songCancelled
}

func TestFetchLRCRedByISRCConcurrency(t *testing.T) {
	t.Run("ttml and song.json run in parallel", func(t *testing.T) {
		const delay = 200 * time.Millisecond
		stub := &lrcRedStub{ttmlStatus: 200, ttmlBody: lrcRedTestTTML, ttmlDelay: delay, songStatus: 200, songBody: `{}`, songDelay: delay}
		useLRCRedStub(t, stub, true)

		start := time.Now()
		if _, ok, err := fetchLRCRedByISRC("USUM72403305"); !ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if elapsed := time.Since(start); elapsed >= 2*delay-50*time.Millisecond {
			t.Fatalf("took %v, the pair ran sequentially (each leg %v)", elapsed, delay)
		}
		if !stub.requested(".song.json") {
			t.Fatal("flag on must request .song.json")
		}
	})

	t.Run("pairs take two semaphore slots without deadlock", func(t *testing.T) {
		stub := &lrcRedStub{ttmlStatus: 200, ttmlBody: lrcRedTestTTML, ttmlDelay: 30 * time.Millisecond, songStatus: 200, songBody: `{}`, songDelay: 30 * time.Millisecond}
		useLRCRedStub(t, stub, true)

		const callers = 5 * 8
		done := make(chan error, callers)
		for i := 0; i < callers; i++ {
			go func(i int) {
				_, ok, err := fetchLRCRedByISRC(fmt.Sprintf("USAAA%07d", i))
				if err == nil && !ok {
					err = fmt.Errorf("caller %d missed", i)
				}
				done <- err
			}(i)
		}
		timeout := time.After(10 * time.Second)
		for i := 0; i < callers; i++ {
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-timeout:
				t.Fatalf("deadlock: only %d of %d callers finished", i, callers)
			}
		}

		stub.mu.Lock()
		defer stub.mu.Unlock()
		if stub.maxRequests > cap(lrcRedSem) {
			t.Errorf("%d requests in flight at once, cap is %d", stub.maxRequests, cap(lrcRedSem))
		}
		if stub.maxISRCs > cap(lrcRedSem)/2 {
			t.Errorf("%d ISRCs in flight at once, want at most %d", stub.maxISRCs, cap(lrcRedSem)/2)
		}
		if stub.maxRequests <= stub.maxISRCs {
			t.Errorf("max %d requests for %d ISRCs in flight, pairs did not overlap their legs", stub.maxRequests, stub.maxISRCs)
		}
		if len(lrcRedSem) != 0 {
			t.Errorf("%d semaphore slots still held after all callers returned", len(lrcRedSem))
		}
	})
}
