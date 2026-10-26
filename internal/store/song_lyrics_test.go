package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func seedProviderLyrics(t *testing.T, cacheKey, body string) {
	t.Helper()
	k := DeriveKey(cacheKey)
	if err := testStore.SetLyrics(context.Background(), cacheKey, k, CachedLyrics{TTML: body, Format: k.Provider}); err != nil {
		t.Fatalf("seed %s: %v", cacheKey, err)
	}
}

func hasSongLyrics(t *testing.T, provider, songArtistKey string) bool {
	t.Helper()
	ok, err := testStore.HasSongLyrics(context.Background(), provider, songArtistKey)
	if err != nil {
		t.Fatalf("HasSongLyrics(%q, %q): %v", provider, songArtistKey, err)
	}
	return ok
}

func TestHasSongLyrics(t *testing.T) {
	resetTables(t)
	seedProviderLyrics(t, "qq_lyrics:projections night tapes [projections] [261s]", "<QrcInfos/>")
	seedProviderLyrics(t, "qq_lyrics:plain song tester", "<QrcInfos/>")
	seedProviderLyrics(t, "qq_lyrics:duration only tester [200s]", "<QrcInfos/>")
	seedProviderLyrics(t, "kugou_lyrics:stick season noah kahan [stick season] [182s]", "[00:01.00]line")
	seedProviderLyrics(t, "qq_lyrics:à peu près pomme [à peu près] [207s]", "<QrcInfos/>")
	seedProviderLyrics(t, "qq_lyrics:song [live] artist b [album] [100s]", "<QrcInfos/>")

	tests := []struct {
		name     string
		provider string
		key      string
		want     bool
	}{
		{"album and duration variant", "qq", "qq_lyrics:projections night tapes", true},
		{"key without album or duration", "qq", "qq_lyrics:plain song tester", true},
		{"duration-only key", "qq", "qq_lyrics:duration only tester", true},
		{"kugou album variant", "kugou", "kugou_lyrics:stick season noah kahan", true},
		{"non-ascii song", "qq", "qq_lyrics:à peu près pomme", true},
		{"bracket in song title", "qq", "qq_lyrics:song [live] artist b", true},
		{"unknown song", "qq", "qq_lyrics:nothing here tester", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasSongLyrics(t, tt.provider, tt.key); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasSongLyrics_EdgeCases(t *testing.T) {
	resetTables(t)
	seedProviderLyrics(t, "qq_lyrics:x blink 182 [enema of the state] [180s]", "<QrcInfos/>")
	seedProviderLyrics(t, "qq_lyrics:x blinkers", "<QrcInfos/>")
	seedProviderLyrics(t, "qq_lyrics:song [live] artist b [album] [100s]", "<QrcInfos/>")
	seedProviderLyrics(t, "ttml_lyrics:viva la vida coldplay 242s", "<tt/>")

	tests := []struct {
		name     string
		provider string
		key      string
	}{
		{"longer artist sharing a prefix", "qq", "qq_lyrics:x blink"},
		{"artist sharing a prefix without a space", "qq", "qq_lyrics:x blink 18"},
		{"same song with no artist", "qq", "qq_lyrics:song "},
		{"other provider", "kugou", "kugou_lyrics:x blink 182"},
		{"provider outside qq and kugou", "ttml", "ttml_lyrics:viva la vida coldplay"},
		{"empty key", "qq", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if hasSongLyrics(t, tt.provider, tt.key) {
				t.Fatalf("matched %q for provider %q", tt.key, tt.provider)
			}
		})
	}
}

func TestHasSongLyrics_IgnoresNoLyricsMarkers(t *testing.T) {
	resetTables(t)
	seedProviderLyrics(t, "qq_lyrics:marked song tester [album] [100s]", NoLyricsSentinel)
	if hasSongLyrics(t, "qq", "qq_lyrics:marked song tester") {
		t.Fatal("a no-lyrics marker must not count as cached lyrics")
	}

	seedProviderLyrics(t, "qq_lyrics:marked song tester [other album] [101s]", "<QrcInfos/>")
	if !hasSongLyrics(t, "qq", "qq_lyrics:marked song tester") {
		t.Fatal("real lyrics next to a marker must still count")
	}
}

func TestHasSongLyrics_FindsLyricsBehindManyMarkers(t *testing.T) {
	resetTables(t)
	for i := 0; i < 15; i++ {
		seedProviderLyrics(t, fmt.Sprintf("qq_lyrics:busy song tester [album %02d] [100s]", i), NoLyricsSentinel)
	}
	seedProviderLyrics(t, "qq_lyrics:busy song tester [zz album] [100s]", "<QrcInfos/>")
	if !hasSongLyrics(t, "qq", "qq_lyrics:busy song tester") {
		t.Fatal("real lyrics behind more than ten markers must still count")
	}
}

func TestHasSongLyrics_UsesPartialIndex(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	conn, err := testStore.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, `RESET enable_seqscan`)

	rows, err := conn.Query(ctx, `EXPLAIN `+songLyricsQuery, "qq", "qq_lyrics:projections night tapes", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "lyrics_song_artist_idx") {
		t.Fatalf("query does not use lyrics_song_artist_idx:\n%s", plan.String())
	}
}

func TestHasSongLyrics_CancelledContextReturnsError(t *testing.T) {
	resetTables(t)
	seedProviderLyrics(t, "qq_lyrics:projections night tapes [projections] [261s]", "<QrcInfos/>")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ok, err := testStore.HasSongLyrics(ctx, "qq", "qq_lyrics:projections night tapes")
	if err == nil || ok {
		t.Fatalf("got ok=%v err=%v, want an error and no match", ok, err)
	}
}
