package ttml

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func htsPtr(b bool) *bool { return &b }

func trackWithISRC(isrc string, hts *bool) *Track {
	var tr Track
	tr.ID = "1000001"
	tr.Attributes.Name = "Song"
	tr.Attributes.ArtistName = "Artist"
	tr.Attributes.ISRC = isrc
	tr.Attributes.HasTimeSyncedLyrics = hts
	return &tr
}

// TestResolveLyrics covers the resolution order: lrc.red is tried before Apple's
// MUT-gated lyrics endpoint, Apple is reached only on a lrc.red miss, and the
// returned provenance is what gates the contribution loop (only SourceApple is
// contributed back).
func TestResolveLyrics(t *testing.T) {
	t.Run("lrc.red hit returns lrc.red source and skips Apple", func(t *testing.T) {
		appleCalled := false
		apple := func() (string, error) { appleCalled = true; return "APPLE", nil }
		lrc := func(string) (string, bool, error) { return "LRCRED", true, nil }

		ttml, source, err := resolveLyrics(trackWithISRC("USFRESH00001", htsPtr(true)), false, lrc, apple)
		if err != nil || ttml != "LRCRED" || source != SourceLRCRed {
			t.Fatalf("got (%q, %q, %v), want (LRCRED, lrc.red, nil)", ttml, source, err)
		}
		if appleCalled {
			t.Error("Apple lyrics endpoint must not be called on a lrc.red hit")
		}
	})

	t.Run("lrc.red 404 falls through to Apple", func(t *testing.T) {
		apple := func() (string, error) { return "APPLE", nil }
		lrc := func(string) (string, bool, error) { return "", false, nil }

		ttml, source, err := resolveLyrics(trackWithISRC("USFRESH00001", htsPtr(true)), false, lrc, apple)
		if err != nil || ttml != "APPLE" || source != SourceApple {
			t.Fatalf("got (%q, %q, %v), want (APPLE, apple, nil)", ttml, source, err)
		}
	})

	t.Run("lrc.red error falls through to Apple", func(t *testing.T) {
		apple := func() (string, error) { return "APPLE", nil }
		lrc := func(string) (string, bool, error) { return "", false, errors.New("boom") }

		ttml, source, err := resolveLyrics(trackWithISRC("USFRESH00001", nil), false, lrc, apple)
		if err != nil || ttml != "APPLE" || source != SourceApple {
			t.Fatalf("got (%q, %q, %v), want (APPLE, apple, nil)", ttml, source, err)
		}
	})

	t.Run("hasTimeSyncedLyrics=false skips Apple on lrc.red miss", func(t *testing.T) {
		appleCalled := false
		apple := func() (string, error) { appleCalled = true; return "APPLE", nil }
		lrc := func(string) (string, bool, error) { return "", false, nil }

		ttml, source, err := resolveLyrics(trackWithISRC("USFRESH00001", htsPtr(false)), false, lrc, apple)
		if err == nil || ttml != "" || source != SourceApple {
			t.Fatalf("got (%q, %q, %v), want (empty, apple, error)", ttml, source, err)
		}
		if appleCalled {
			t.Error("Apple must not be called when hasTimeSyncedLyrics=false")
		}
	})

	t.Run("Apple error propagates with apple provenance", func(t *testing.T) {
		apple := func() (string, error) { return "", errors.New("failed to fetch TTML: boom") }
		lrc := func(string) (string, bool, error) { return "", false, nil }

		_, source, err := resolveLyrics(trackWithISRC("USFRESH00001", htsPtr(true)), false, lrc, apple)
		if err == nil || source != SourceApple {
			t.Fatalf("want apple error, got source=%q err=%v", source, err)
		}
	})

	t.Run("appleFirst prefers Apple and skips lrc.red when Apple has timed lyrics", func(t *testing.T) {
		lrcCalled := false
		apple := func() (string, error) { return "APPLE", nil }
		lrc := func(string) (string, bool, error) { lrcCalled = true; return "LRCRED", true, nil }

		ttml, source, err := resolveLyrics(trackWithISRC("USFRESH00001", htsPtr(true)), true, lrc, apple)
		if err != nil || ttml != "APPLE" || source != SourceApple {
			t.Fatalf("got (%q, %q, %v), want (APPLE, apple, nil)", ttml, source, err)
		}
		if lrcCalled {
			t.Error("lrc.red must not be queried when Apple already returned timed lyrics")
		}
	})

	t.Run("appleFirst falls back to lrc.red when hasTimeSyncedLyrics=false", func(t *testing.T) {
		appleCalled := false
		apple := func() (string, error) { appleCalled = true; return "APPLE", nil }
		lrc := func(string) (string, bool, error) { return "LRCRED", true, nil }

		ttml, source, err := resolveLyrics(trackWithISRC("USFRESH00001", htsPtr(false)), true, lrc, apple)
		if err != nil || ttml != "LRCRED" || source != SourceLRCRed {
			t.Fatalf("got (%q, %q, %v), want (LRCRED, lrc.red, nil)", ttml, source, err)
		}
		if appleCalled {
			t.Error("Apple lyrics endpoint must not be called when hasTimeSyncedLyrics=false")
		}
	})

	t.Run("appleFirst falls back to lrc.red when Apple fetch fails", func(t *testing.T) {
		apple := func() (string, error) { return "", errors.New("failed to fetch TTML: boom") }
		lrc := func(string) (string, bool, error) { return "LRCRED", true, nil }

		ttml, source, err := resolveLyrics(trackWithISRC("USFRESH00001", htsPtr(true)), true, lrc, apple)
		if err != nil || ttml != "LRCRED" || source != SourceLRCRed {
			t.Fatalf("got (%q, %q, %v), want (LRCRED, lrc.red, nil)", ttml, source, err)
		}
	})

	t.Run("appleFirst returns Apple error when both sources miss", func(t *testing.T) {
		apple := func() (string, error) { return "", errors.New("failed to fetch TTML: boom") }
		lrc := func(string) (string, bool, error) { return "", false, nil }

		ttml, source, err := resolveLyrics(trackWithISRC("USFRESH00001", htsPtr(true)), true, lrc, apple)
		if err == nil || ttml != "" || source != SourceApple {
			t.Fatalf("got (%q, %q, %v), want (empty, apple, error)", ttml, source, err)
		}
	})

	t.Run("appleFirst with hasTimeSyncedLyrics=false and lrc.red miss returns Apple error without fetching Apple", func(t *testing.T) {
		appleCalled := false
		apple := func() (string, error) { appleCalled = true; return "APPLE", nil }
		lrc := func(string) (string, bool, error) { return "", false, nil }

		ttml, source, err := resolveLyrics(trackWithISRC("USFRESH00001", htsPtr(false)), true, lrc, apple)
		if err == nil || ttml != "" || source != SourceApple {
			t.Fatalf("got (%q, %q, %v), want (empty, apple, error)", ttml, source, err)
		}
		if appleCalled {
			t.Error("Apple lyrics endpoint must not be called when hasTimeSyncedLyrics=false")
		}
	})
}

// TestLRCRedConcurrencyBounded proves outbound lrc.red reads are capped at 8
// concurrent, so a runaway cannot flood the partner.
func TestLRCRedConcurrencyBounded(t *testing.T) {
	var cur, maxSeen int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&cur, 1)
		for {
			m := atomic.LoadInt32(&maxSeen)
			if n <= m || atomic.CompareAndSwapInt32(&maxSeen, m, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt32(&cur, -1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<tt/>"))
	}))
	defer srv.Close()

	old := lrcRedReadBase
	lrcRedReadBase = srv.URL
	defer func() { lrcRedReadBase = old }()

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, _ = fetchLRCRedByISRC(fmt.Sprintf("USTEST0000%02d", i))
		}(i)
	}
	wg.Wait()

	if maxSeen == 0 {
		t.Fatal("no requests reached the server")
	}
	if maxSeen > 8 {
		t.Errorf("max concurrent lrc.red requests = %d, want <= 8", maxSeen)
	}
}
