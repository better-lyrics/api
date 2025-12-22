package kugou

import (
	"testing"
)

func TestSelectBestCandidate_ExactMatch(t *testing.T) {
	candidates := []LyricsCandidate{
		{Song: "Shape of You", Singer: "Ed Sheeran", Score: 50, KRCType: 1, Duration: 230000},
		{Song: "Perfect", Singer: "Ed Sheeran", Score: 50, KRCType: 1, Duration: 260000},
	}

	best, score := SelectBestCandidate(candidates, "Shape of You", "Ed Sheeran", 230000)

	if best == nil {
		t.Fatal("Expected a best candidate, got nil")
	}

	if best.Song != "Shape of You" {
		t.Errorf("Expected 'Shape of You', got %q", best.Song)
	}

	if score <= 0 || score > 1 {
		t.Errorf("Score should be between 0 and 1, got %f", score)
	}
}

func TestSelectBestCandidate_PartialMatch(t *testing.T) {
	candidates := []LyricsCandidate{
		{Song: "Shape of You (Remix)", Singer: "Ed Sheeran feat. DJ", Score: 50, KRCType: 1, Duration: 230000},
		{Song: "Different Song", Singer: "Different Artist", Score: 50, KRCType: 1, Duration: 230000},
	}

	best, _ := SelectBestCandidate(candidates, "Shape of You", "Ed Sheeran", 230000)

	if best == nil {
		t.Fatal("Expected a best candidate, got nil")
	}

	// Partial match should prefer the one containing the search terms
	if best.Song != "Shape of You (Remix)" {
		t.Errorf("Expected partial match 'Shape of You (Remix)', got %q", best.Song)
	}
}

func TestSelectBestCandidate_DurationBonus(t *testing.T) {
	candidates := []LyricsCandidate{
		{Song: "Test Song", Singer: "Test Artist", Score: 50, KRCType: 1, Duration: 200000}, // 30s off
		{Song: "Test Song", Singer: "Test Artist", Score: 50, KRCType: 1, Duration: 229000}, // 1s off
		{Song: "Test Song", Singer: "Test Artist", Score: 50, KRCType: 1, Duration: 250000}, // 20s off
	}

	best, _ := SelectBestCandidate(candidates, "Test Song", "Test Artist", 230000)

	if best == nil {
		t.Fatal("Expected a best candidate, got nil")
	}

	// Should prefer the one closest to 230000ms
	if best.Duration != 229000 {
		t.Errorf("Expected duration 229000 (closest match), got %d", best.Duration)
	}
}

func TestSelectBestCandidate_SyncedPreference(t *testing.T) {
	candidates := []LyricsCandidate{
		{Song: "Test", Singer: "Artist", Score: 60, KRCType: 2, Duration: 200000}, // Unsynced, higher API score
		{Song: "Test", Singer: "Artist", Score: 50, KRCType: 1, Duration: 200000}, // Synced, lower API score
	}

	best, _ := SelectBestCandidate(candidates, "Test", "Artist", 0)

	if best == nil {
		t.Fatal("Expected a best candidate, got nil")
	}

	// Synced lyrics (KRCType=1) should be preferred
	if best.KRCType != 1 {
		t.Errorf("Expected synced lyrics (KRCType=1), got KRCType=%d", best.KRCType)
	}
}

func TestSelectBestCandidate_OfficialBonus(t *testing.T) {
	candidates := []LyricsCandidate{
		{Song: "Test", Singer: "Artist", Score: 50, KRCType: 1, ProductFrom: "user", Duration: 200000},
		{Song: "Test", Singer: "Artist", Score: 50, KRCType: 1, ProductFrom: "官方歌词", Duration: 200000},
	}

	best, _ := SelectBestCandidate(candidates, "Test", "Artist", 0)

	if best == nil {
		t.Fatal("Expected a best candidate, got nil")
	}

	// Official lyrics should be preferred
	if best.ProductFrom != "官方歌词" {
		t.Errorf("Expected official lyrics, got ProductFrom=%q", best.ProductFrom)
	}
}

func TestSelectBestCandidate_EmptyList(t *testing.T) {
	best, score := SelectBestCandidate([]LyricsCandidate{}, "Test", "Artist", 0)

	if best != nil {
		t.Error("Expected nil for empty candidate list")
	}

	if score != 0 {
		t.Errorf("Expected score 0, got %f", score)
	}
}

func TestSelectBestCandidate_ScoreNormalization(t *testing.T) {
	// Test with various combinations to ensure score stays in 0-1 range
	testCases := []struct {
		name       string
		candidates []LyricsCandidate
		song       string
		artist     string
		durationMs int
	}{
		{
			name: "High score case",
			candidates: []LyricsCandidate{
				{Song: "Test Song", Singer: "Test Artist", Score: 60, KRCType: 1, ProductFrom: "官方", Duration: 200000},
			},
			song:       "Test Song",
			artist:     "Test Artist",
			durationMs: 200000,
		},
		{
			name: "Low score case",
			candidates: []LyricsCandidate{
				{Song: "Different", Singer: "Other", Score: 10, KRCType: 2, Duration: 300000},
			},
			song:       "Test Song",
			artist:     "Test Artist",
			durationMs: 200000,
		},
		{
			name: "Zero API score",
			candidates: []LyricsCandidate{
				{Song: "Test", Singer: "Artist", Score: 0, KRCType: 2, Duration: 200000},
			},
			song:       "Test",
			artist:     "Artist",
			durationMs: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, score := SelectBestCandidate(tc.candidates, tc.song, tc.artist, tc.durationMs)

			if score < 0 || score > 1 {
				t.Errorf("Score %f is out of range [0, 1]", score)
			}
		})
	}
}

func TestSelectBestCandidate_CaseInsensitive(t *testing.T) {
	candidates := []LyricsCandidate{
		{Song: "HELLO WORLD", Singer: "TEST ARTIST", Score: 50, KRCType: 1, Duration: 200000},
	}

	best, _ := SelectBestCandidate(candidates, "hello world", "test artist", 200000)

	if best == nil {
		t.Fatal("Expected a match with case-insensitive comparison")
	}
}

func TestSelectBestSong_ExactMatch(t *testing.T) {
	songs := []SongInfo{
		{SongName: "Shape of You", SingerName: "Ed Sheeran", Duration: 230, Hash: "abc123"},
		{SongName: "Perfect", SingerName: "Ed Sheeran", Duration: 260, Hash: "def456"},
	}

	best, score := SelectBestSong(songs, "Shape of You", "Ed Sheeran", 230000)

	if best == nil {
		t.Fatal("Expected a best song, got nil")
	}

	if best.SongName != "Shape of You" {
		t.Errorf("Expected 'Shape of You', got %q", best.SongName)
	}

	if score <= 0 || score > 1 {
		t.Errorf("Score should be between 0 and 1, got %f", score)
	}
}

func TestSelectBestSong_DurationBonus(t *testing.T) {
	songs := []SongInfo{
		{SongName: "Test", SingerName: "Artist", Duration: 200, Hash: "a"}, // 200s = 200000ms
		{SongName: "Test", SingerName: "Artist", Duration: 230, Hash: "b"}, // 230s = 230000ms (exact match)
		{SongName: "Test", SingerName: "Artist", Duration: 260, Hash: "c"}, // 260s = 260000ms
	}

	best, _ := SelectBestSong(songs, "Test", "Artist", 230000)

	if best == nil {
		t.Fatal("Expected a best song, got nil")
	}

	// Note: SongInfo.Duration is in seconds, durationMs is in milliseconds
	if best.Duration != 230 {
		t.Errorf("Expected duration 230 (exact match), got %d", best.Duration)
	}
}

func TestSelectBestSong_QualityBonus(t *testing.T) {
	songs := []SongInfo{
		{SongName: "Test", SingerName: "Artist", Duration: 200, Hash: "a", SQHash: "", Hash320: ""},
		{SongName: "Test", SingerName: "Artist", Duration: 200, Hash: "b", SQHash: "sq", Hash320: "320"},
	}

	best, _ := SelectBestSong(songs, "Test", "Artist", 0)

	if best == nil {
		t.Fatal("Expected a best song, got nil")
	}

	// Should prefer the one with higher quality hashes
	if best.SQHash != "sq" {
		t.Error("Expected song with SQ quality hash")
	}
}

func TestSelectBestSong_EmptyList(t *testing.T) {
	best, score := SelectBestSong([]SongInfo{}, "Test", "Artist", 0)

	if best != nil {
		t.Error("Expected nil for empty song list")
	}

	if score != 0 {
		t.Errorf("Expected score 0, got %f", score)
	}
}

func TestSelectBestSong_PartialMatch(t *testing.T) {
	songs := []SongInfo{
		{SongName: "Shape of You (Official Video)", SingerName: "Ed Sheeran & Friends", Duration: 230, Hash: "a"},
		{SongName: "Other Song", SingerName: "Other Artist", Duration: 230, Hash: "b"},
	}

	best, _ := SelectBestSong(songs, "Shape of You", "Ed Sheeran", 230000)

	if best == nil {
		t.Fatal("Expected a best song, got nil")
	}

	// Should match the one containing the search terms
	if best.SongName != "Shape of You (Official Video)" {
		t.Errorf("Expected partial match, got %q", best.SongName)
	}
}

func TestSelectBestSong_CaseInsensitive(t *testing.T) {
	songs := []SongInfo{
		{SongName: "HELLO WORLD", SingerName: "TEST ARTIST", Duration: 200, Hash: "a"},
	}

	best, _ := SelectBestSong(songs, "hello world", "test artist", 200000)

	if best == nil {
		t.Fatal("Expected a match with case-insensitive comparison")
	}
}

func TestFilterSongsByDuration(t *testing.T) {
	songs := []SongInfo{
		{SongName: "Song1", Duration: 200, Hash: "a"}, // 200s = 200000ms
		{SongName: "Song2", Duration: 230, Hash: "b"}, // 230s = 230000ms
		{SongName: "Song3", Duration: 260, Hash: "c"}, // 260s = 260000ms
		{SongName: "Song4", Duration: 300, Hash: "d"}, // 300s = 300000ms
	}

	// Filter for songs within 10000ms (10s) of 230000ms
	filtered := filterSongsByDuration(songs, 230000, 10000)

	if len(filtered) != 1 {
		t.Errorf("Expected 1 song within delta, got %d", len(filtered))
	}

	if len(filtered) > 0 && filtered[0].SongName != "Song2" {
		t.Errorf("Expected 'Song2', got %q", filtered[0].SongName)
	}
}

func TestFilterSongsByDuration_WiderDelta(t *testing.T) {
	songs := []SongInfo{
		{SongName: "Song1", Duration: 200, Hash: "a"}, // 200000ms
		{SongName: "Song2", Duration: 230, Hash: "b"}, // 230000ms
		{SongName: "Song3", Duration: 260, Hash: "c"}, // 260000ms
	}

	// Filter for songs within 50000ms (50s) of 230000ms
	filtered := filterSongsByDuration(songs, 230000, 50000)

	if len(filtered) != 3 {
		t.Errorf("Expected 3 songs within delta, got %d", len(filtered))
	}
}

func TestFilterSongsByDuration_AllFiltered(t *testing.T) {
	songs := []SongInfo{
		{SongName: "Song1", Duration: 100, Hash: "a"}, // 100000ms
		{SongName: "Song2", Duration: 120, Hash: "b"}, // 120000ms
	}

	// Filter for songs within 5000ms of 300000ms - none should match
	filtered := filterSongsByDuration(songs, 300000, 5000)

	if len(filtered) != 0 {
		t.Errorf("Expected 0 songs, got %d", len(filtered))
	}
}

func TestFilterSongsByDuration_EmptyInput(t *testing.T) {
	filtered := filterSongsByDuration([]SongInfo{}, 230000, 10000)

	if len(filtered) != 0 {
		t.Errorf("Expected empty slice, got %d items", len(filtered))
	}
}

func TestAbs(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{5, 5},
		{-5, 5},
		{0, 0},
		{-100, 100},
		{100, 100},
		{-1, 1},
	}

	for _, tt := range tests {
		result := abs(tt.input)
		if result != tt.expected {
			t.Errorf("abs(%d) = %d, expected %d", tt.input, result, tt.expected)
		}
	}
}

func TestSelectBestCandidate_NoDuration(t *testing.T) {
	candidates := []LyricsCandidate{
		{Song: "Test", Singer: "Artist", Score: 50, KRCType: 1, Duration: 200000},
	}

	// When durationMs is 0, should not use duration bonus
	best, score := SelectBestCandidate(candidates, "Test", "Artist", 0)

	if best == nil {
		t.Fatal("Expected a best candidate")
	}

	if score < 0 || score > 1 {
		t.Errorf("Score %f is out of range", score)
	}
}

func TestSelectBestSong_NoArtist(t *testing.T) {
	songs := []SongInfo{
		{SongName: "Test Song", SingerName: "Any Artist", Duration: 200, Hash: "a"},
	}

	// Empty artist should still match
	best, _ := SelectBestSong(songs, "Test Song", "", 0)

	if best == nil {
		t.Fatal("Expected a match even with empty artist")
	}
}

func TestSelectBestCandidate_SubstringMatch(t *testing.T) {
	candidates := []LyricsCandidate{
		{Song: "Hello", Singer: "Test", Score: 50, KRCType: 1, Duration: 200000},
		{Song: "Hello World", Singer: "Test", Score: 50, KRCType: 1, Duration: 200000},
	}

	// "Hello World" contains "Hello", but exact match should score higher
	best, _ := SelectBestCandidate(candidates, "Hello World", "Test", 0)

	if best == nil {
		t.Fatal("Expected a match")
	}

	if best.Song != "Hello World" {
		t.Errorf("Expected exact match 'Hello World', got %q", best.Song)
	}
}
