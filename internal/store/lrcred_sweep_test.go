package store

import (
	"context"
	"testing"
)

func seedSourcedLyrics(t *testing.T, cacheKey, source, isrc string) {
	t.Helper()
	ctx := context.Background()
	k := DeriveKey(cacheKey)
	if err := testStore.SetLyrics(ctx, cacheKey, k, CachedLyrics{TTML: "<tt/>", Format: k.Provider, Source: source}); err != nil {
		t.Fatalf("seed %s: %v", cacheKey, err)
	}
	if isrc == "" {
		return
	}
	if err := testStore.SetSongMetadata(ctx, &SongMetadata{CacheKey: cacheKey, ISRC: isrc, TrackName: "Track " + isrc, ArtistName: "Artist " + isrc}); err != nil {
		t.Fatalf("seed metadata %s: %v", cacheKey, err)
	}
}

func scanAllLRCRed(t *testing.T, pageSize int) ([]LRCRedRow, int) {
	t.Helper()
	var all []LRCRedRow
	after, pages := "", 0
	for {
		rows, next, err := testStore.ScanLRCRedLyrics(context.Background(), after, pageSize)
		if err != nil {
			t.Fatalf("scan after %q: %v", after, err)
		}
		pages++
		all = append(all, rows...)
		if next == "" {
			return all, pages
		}
		if next <= after {
			t.Fatalf("cursor did not advance: %q -> %q", after, next)
		}
		after = next
	}
}

func TestScanLRCRedLyrics(t *testing.T) {
	resetTables(t)
	seedSourcedLyrics(t, "ttml_lyrics:a song artist", "lrc.red", "QZES72218029")
	seedSourcedLyrics(t, "ttml_lyrics:b song artist", "apple", "USUM72403305")
	seedSourcedLyrics(t, "ttml_lyrics:c song artist", "", "")
	seedSourcedLyrics(t, "ttml_lyrics:d song artist [album] [200s]", "lrc.red", "USUG12004507")
	seedSourcedLyrics(t, "ttml_lyrics:e song artist", "lrc.red", "")
	seedSourcedLyrics(t, "qq_lyrics:f song artist", "", "")

	want := []LRCRedRow{
		{CacheKey: "ttml_lyrics:a song artist", ISRC: "QZES72218029", TrackName: "Track QZES72218029", ArtistName: "Artist QZES72218029"},
		{CacheKey: "ttml_lyrics:d song artist [album] [200s]", ISRC: "USUG12004507", TrackName: "Track USUG12004507", ArtistName: "Artist USUG12004507"},
		{CacheKey: "ttml_lyrics:e song artist"},
	}

	for _, pageSize := range []int{1, 2, 4, 100} {
		got, pages := scanAllLRCRed(t, pageSize)
		if len(got) != len(want) {
			t.Fatalf("page size %d: got %d rows %+v, want %d", pageSize, len(got), got, len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("page size %d row %d = %+v, want %+v", pageSize, i, got[i], want[i])
			}
		}
		if pageSize == 1 && pages < 6 {
			t.Errorf("page size 1 took %d pages over 6 rows, the scan is not bounded by page size", pages)
		}
	}

	t.Run("resumes from a cursor", func(t *testing.T) {
		rows, _, err := testStore.ScanLRCRedLyrics(context.Background(), "ttml_lyrics:b song artist", 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].CacheKey != "ttml_lyrics:d song artist [album] [200s]" {
			t.Fatalf("resume got %+v", rows)
		}
	})

	t.Run("empty table ends immediately", func(t *testing.T) {
		resetTables(t)
		rows, next, err := testStore.ScanLRCRedLyrics(context.Background(), "", 10)
		if err != nil || len(rows) != 0 || next != "" {
			t.Fatalf("got (%v, %q, %v), want no rows and an empty cursor", rows, next, err)
		}
	})
}

func TestDeleteLRCRedLyrics(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	seedSourcedLyrics(t, "ttml_lyrics:ai song artist", "lrc.red", "QZES72218029")
	seedSourcedLyrics(t, "ttml_lyrics:apple song artist", "apple", "USUM72403305")
	before := counterValue(t, "ttml")

	deleted, err := testStore.DeleteLRCRedLyrics(ctx, "ttml_lyrics:ai song artist")
	if err != nil || !deleted {
		t.Fatalf("delete lrc.red row: deleted=%v err=%v", deleted, err)
	}
	if _, ok, _ := testStore.GetLyricsExact(ctx, "ttml_lyrics:ai song artist"); ok {
		t.Error("lrc.red row still cached after delete")
	}
	if got := counterValue(t, "ttml"); got != before-1 {
		t.Errorf("ttml counter = %d, want %d", got, before-1)
	}

	t.Run("refuses a row rewritten from another source", func(t *testing.T) {
		deleted, err := testStore.DeleteLRCRedLyrics(ctx, "ttml_lyrics:apple song artist")
		if err != nil || deleted {
			t.Fatalf("deleted=%v err=%v, want the apple row kept", deleted, err)
		}
		if _, ok, _ := testStore.GetLyricsExact(ctx, "ttml_lyrics:apple song artist"); !ok {
			t.Error("apple row was deleted")
		}
	})

	t.Run("is idempotent", func(t *testing.T) {
		n := counterValue(t, "ttml")
		deleted, err := testStore.DeleteLRCRedLyrics(ctx, "ttml_lyrics:ai song artist")
		if err != nil || deleted {
			t.Fatalf("second delete: deleted=%v err=%v", deleted, err)
		}
		if counterValue(t, "ttml") != n {
			t.Error("a no-op delete changed the counter")
		}
	})
}
