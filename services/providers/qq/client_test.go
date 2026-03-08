package qq

import (
	"testing"
)

func TestGenerateSign(t *testing.T) {
	data := `{"comm":{"wid":"ABC","cv":13020508},"music.search.SearchCgiService.DoSearchForQQMusicMobile":{"module":"music.search.SearchCgiService","method":"DoSearchForQQMusicMobile","param":{"query":"test"}}}`

	sign := generateSign(data)

	if len(sign) < 4 || sign[:3] != "zzc" {
		t.Errorf("Sign should start with 'zzc', got %q", sign[:min(3, len(sign))])
	}

	if sign != toLower(sign) {
		t.Errorf("Sign should be lowercase, got %q", sign)
	}

	// Sign should be deterministic
	sign2 := generateSign(data)
	if sign != sign2 {
		t.Errorf("Sign should be deterministic: %q != %q", sign, sign2)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func TestGenerateSign_DifferentInputs(t *testing.T) {
	sign1 := generateSign("input one")
	sign2 := generateSign("input two")

	if sign1 == sign2 {
		t.Error("Different inputs should produce different signatures")
	}
}

func TestHexCharToInt(t *testing.T) {
	tests := []struct {
		input    byte
		expected int
		wantErr  bool
	}{
		{'0', 0, false},
		{'9', 9, false},
		{'a', 10, false},
		{'f', 15, false},
		{'A', 10, false},
		{'F', 15, false},
		{'g', 0, true},
		{'z', 0, true},
	}

	for _, tt := range tests {
		result, err := hexCharToInt(tt.input)
		if tt.wantErr && err == nil {
			t.Errorf("hexCharToInt(%q): expected error", tt.input)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("hexCharToInt(%q): unexpected error: %v", tt.input, err)
		}
		if !tt.wantErr && result != tt.expected {
			t.Errorf("hexCharToInt(%q) = %d, expected %d", tt.input, result, tt.expected)
		}
	}
}

func TestSelectBestSong_ExactMatch(t *testing.T) {
	songs := []SongItem{
		{Title: "Shape of You", Singer: []Singer{{Name: "Ed Sheeran"}}, Interval: 230, MID: "abc"},
		{Title: "Perfect", Singer: []Singer{{Name: "Ed Sheeran"}}, Interval: 260, MID: "def"},
	}

	best, score := SelectBestSong(songs, "Shape of You", "Ed Sheeran", 230000)

	if best == nil {
		t.Fatal("Expected a best song, got nil")
	}

	if best.Title != "Shape of You" {
		t.Errorf("Expected 'Shape of You', got %q", best.Title)
	}

	if score <= 0 || score > 1 {
		t.Errorf("Score should be between 0 and 1, got %f", score)
	}
}

func TestSelectBestSong_PartialMatch(t *testing.T) {
	songs := []SongItem{
		{Title: "Shape of You (Official Video)", Singer: []Singer{{Name: "Ed Sheeran"}}, Interval: 230, MID: "a"},
		{Title: "Other Song", Singer: []Singer{{Name: "Other Artist"}}, Interval: 230, MID: "b"},
	}

	best, _ := SelectBestSong(songs, "Shape of You", "Ed Sheeran", 230000)

	if best == nil {
		t.Fatal("Expected a best song, got nil")
	}

	if best.Title != "Shape of You (Official Video)" {
		t.Errorf("Expected partial match, got %q", best.Title)
	}
}

func TestSelectBestSong_DurationBonus(t *testing.T) {
	songs := []SongItem{
		{Title: "Test", Singer: []Singer{{Name: "Artist"}}, Interval: 200, MID: "a"},
		{Title: "Test", Singer: []Singer{{Name: "Artist"}}, Interval: 230, MID: "b"},
		{Title: "Test", Singer: []Singer{{Name: "Artist"}}, Interval: 260, MID: "c"},
	}

	best, _ := SelectBestSong(songs, "Test", "Artist", 230000)

	if best == nil {
		t.Fatal("Expected a best song, got nil")
	}

	if best.Interval != 230 {
		t.Errorf("Expected duration 230 (exact match), got %d", best.Interval)
	}
}

func TestSelectBestSong_EmptyList(t *testing.T) {
	best, score := SelectBestSong([]SongItem{}, "Test", "Artist", 0)

	if best != nil {
		t.Error("Expected nil for empty song list")
	}

	if score != 0 {
		t.Errorf("Expected score 0, got %f", score)
	}
}

func TestSelectBestSong_CaseInsensitive(t *testing.T) {
	songs := []SongItem{
		{Title: "HELLO WORLD", Singer: []Singer{{Name: "TEST ARTIST"}}, Interval: 200, MID: "a"},
	}

	best, _ := SelectBestSong(songs, "hello world", "test artist", 200000)

	if best == nil {
		t.Fatal("Expected a match with case-insensitive comparison")
	}
}

func TestSelectBestSong_MultipleSingers(t *testing.T) {
	songs := []SongItem{
		{
			Title:    "Test Song",
			Singer:   []Singer{{Name: "Artist A"}, {Name: "Artist B"}},
			Interval: 200,
			MID:      "a",
		},
	}

	best, _ := SelectBestSong(songs, "Test Song", "Artist A", 0)

	if best == nil {
		t.Fatal("Expected a match with partial artist")
	}
}

func TestFilterSongsByDuration(t *testing.T) {
	songs := []SongItem{
		{Title: "Song1", Interval: 200, MID: "a"},
		{Title: "Song2", Interval: 230, MID: "b"},
		{Title: "Song3", Interval: 260, MID: "c"},
		{Title: "Song4", Interval: 300, MID: "d"},
	}

	filtered := filterSongsByDuration(songs, 230000, 10000)

	if len(filtered) != 1 {
		t.Errorf("Expected 1 song within delta, got %d", len(filtered))
	}

	if len(filtered) > 0 && filtered[0].Title != "Song2" {
		t.Errorf("Expected 'Song2', got %q", filtered[0].Title)
	}
}

func TestFilterSongsByDuration_EmptyInput(t *testing.T) {
	filtered := filterSongsByDuration([]SongItem{}, 230000, 10000)

	if len(filtered) != 0 {
		t.Errorf("Expected empty slice, got %d items", len(filtered))
	}
}

func TestFilterSongsByDuration_AllFiltered(t *testing.T) {
	songs := []SongItem{
		{Title: "Song1", Interval: 100, MID: "a"},
		{Title: "Song2", Interval: 120, MID: "b"},
	}

	filtered := filterSongsByDuration(songs, 300000, 5000)

	if len(filtered) != 0 {
		t.Errorf("Expected 0 songs, got %d", len(filtered))
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
	}

	for _, tt := range tests {
		result := abs(tt.input)
		if result != tt.expected {
			t.Errorf("abs(%d) = %d, expected %d", tt.input, result, tt.expected)
		}
	}
}

func TestIsHexString(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"0123456789abcdef", true},
		{"ABCDEF", true},
		{"deadbeef", true},
		{"not hex!", false},
		{"", false},
		{"0g", false},
	}

	for _, tt := range tests {
		result := isHexString(tt.input)
		if result != tt.expected {
			t.Errorf("isHexString(%q) = %v, expected %v", tt.input, result, tt.expected)
		}
	}
}

func TestParseHexPair(t *testing.T) {
	val, err := parseHexPair('F', 'A')
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if val != 0xFA {
		t.Errorf("Expected 0xFA (250), got %d", val)
	}
}

func TestSongItem_SingerNames(t *testing.T) {
	tests := []struct {
		name     string
		singers  []Singer
		expected string
	}{
		{"Single singer", []Singer{{Name: "Coldplay"}}, "Coldplay"},
		{"Multiple singers", []Singer{{Name: "A"}, {Name: "B"}, {Name: "C"}}, "A, B, C"},
		{"No singers", []Singer{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := SongItem{Singer: tt.singers}
			result := s.SingerNames()
			if result != tt.expected {
				t.Errorf("SingerNames() = %q, expected %q", result, tt.expected)
			}
		})
	}
}
