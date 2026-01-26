package kugou

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestParseLRC_BasicFormat(t *testing.T) {
	tests := []struct {
		name          string
		lrc           string
		expectedCount int
		firstWords    string
	}{
		{
			name: "Simple two-digit milliseconds",
			lrc: `[00:01.50]Hello world
[00:03.00]Second line`,
			expectedCount: 2,
			firstWords:    "Hello world",
		},
		{
			name: "Three-digit milliseconds",
			lrc: `[00:01.500]Hello world
[00:03.000]Second line`,
			expectedCount: 2,
			firstWords:    "Hello world",
		},
		{
			name: "Mixed formats",
			lrc: `[00:01.50]Line one
[00:03.123]Line two
[00:05.00]Line three`,
			expectedCount: 3,
			firstWords:    "Line one",
		},
		{
			name:          "Single line",
			lrc:           `[00:05.00]Only one line`,
			expectedCount: 1,
			firstWords:    "Only one line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines, _, err := ParseLRC(tt.lrc)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(lines) != tt.expectedCount {
				t.Errorf("Expected %d lines, got %d", tt.expectedCount, len(lines))
			}

			if len(lines) > 0 && lines[0].Words != tt.firstWords {
				t.Errorf("Expected first line %q, got %q", tt.firstWords, lines[0].Words)
			}
		})
	}
}

func TestParseLRC_Timestamps(t *testing.T) {
	lrc := `[01:30.50]One minute thirty seconds
[02:00.00]Two minutes`

	lines, _, err := ParseLRC(lrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 2 {
		t.Fatalf("Expected 2 lines, got %d", len(lines))
	}

	// 1:30.50 = 90500ms
	if lines[0].StartTimeMs != "90500" {
		t.Errorf("Expected StartTimeMs '90500', got %q", lines[0].StartTimeMs)
	}

	// 2:00.00 = 120000ms
	if lines[1].StartTimeMs != "120000" {
		t.Errorf("Expected StartTimeMs '120000', got %q", lines[1].StartTimeMs)
	}

	// First line ends when second line starts
	if lines[0].EndTimeMs != "120000" {
		t.Errorf("Expected EndTimeMs '120000', got %q", lines[0].EndTimeMs)
	}

	// Duration should be 120000 - 90500 = 29500
	if lines[0].DurationMs != "29500" {
		t.Errorf("Expected DurationMs '29500', got %q", lines[0].DurationMs)
	}
}

func TestParseLRC_MultipleTimestamps(t *testing.T) {
	// Karaoke-style: same lyrics appear at multiple times
	lrc := `[00:05.00][00:30.00]Repeated chorus line`

	lines, _, err := ParseLRC(lrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 2 {
		t.Fatalf("Expected 2 lines (one per timestamp), got %d", len(lines))
	}

	// Both should have the same text
	for _, line := range lines {
		if line.Words != "Repeated chorus line" {
			t.Errorf("Expected 'Repeated chorus line', got %q", line.Words)
		}
	}

	// Should be sorted by start time
	if lines[0].StartTimeMs != "5000" {
		t.Errorf("First line should start at 5000ms, got %s", lines[0].StartTimeMs)
	}
	if lines[1].StartTimeMs != "30000" {
		t.Errorf("Second line should start at 30000ms, got %s", lines[1].StartTimeMs)
	}
}

func TestParseLRC_MetadataTags(t *testing.T) {
	lrc := `[ar:Test Artist]
[ti:Test Song]
[al:Test Album]
[by:LRC Creator]
[offset:500]
[00:05.00]First lyrics line`

	lines, metadata, err := ParseLRC(lrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should only have 1 lyric line, not metadata
	if len(lines) != 1 {
		t.Errorf("Expected 1 line, got %d", len(lines))
	}

	// Check metadata extraction
	expectedMetadata := map[string]string{
		"artist":  "Test Artist",
		"title":   "Test Song",
		"album":   "Test Album",
		"creator": "LRC Creator",
		"offset":  "500",
	}

	for key, expected := range expectedMetadata {
		if metadata[key] != expected {
			t.Errorf("metadata[%q] = %q, expected %q", key, metadata[key], expected)
		}
	}
}

func TestParseLRC_InternalTagsSkipped(t *testing.T) {
	lrc := `[id:123456]
[hash:abcdef]
[sign:xyz]
[qq:123]
[total:180000]
[00:05.00]Lyrics`

	lines, metadata, err := ParseLRC(lrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 1 {
		t.Errorf("Expected 1 line, got %d", len(lines))
	}

	// Internal tags should not be in metadata
	internalTags := []string{"id", "hash", "sign", "qq", "total"}
	for _, tag := range internalTags {
		if _, ok := metadata[tag]; ok {
			t.Errorf("Internal tag %q should not be in metadata", tag)
		}
	}
}

func TestParseLRC_EmptyLines(t *testing.T) {
	lrc := `[00:05.00]First line

[00:10.00]Second line

[00:15.00]Third line`

	lines, _, err := ParseLRC(lrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 3 {
		t.Errorf("Expected 3 lines (empty lines skipped), got %d", len(lines))
	}
}

func TestParseLRC_TimestampOnlyLines(t *testing.T) {
	lrc := `[00:05.00]
[00:10.00]Real lyrics here
[00:15.00]
[00:20.00]More lyrics`

	lines, _, err := ParseLRC(lrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Lines with only timestamps (no text) should be skipped
	if len(lines) != 2 {
		t.Errorf("Expected 2 lines (empty text skipped), got %d", len(lines))
	}

	if lines[0].Words != "Real lyrics here" {
		t.Errorf("Expected 'Real lyrics here', got %q", lines[0].Words)
	}
}

func TestParseLRC_SyllableGeneration(t *testing.T) {
	lrc := `[00:05.00]Hello world again
[00:10.00]Next line`

	lines, _, err := ParseLRC(lrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) < 1 {
		t.Fatalf("Expected at least 1 line")
	}

	// "Hello world again" = 3 words
	if len(lines[0].Syllables) != 3 {
		t.Errorf("Expected 3 syllables, got %d", len(lines[0].Syllables))
	}

	expectedWords := []string{"Hello", "world", "again"}
	for i, expected := range expectedWords {
		if lines[0].Syllables[i].Text != expected {
			t.Errorf("Syllable %d: expected %q, got %q", i, expected, lines[0].Syllables[i].Text)
		}
	}
}

func TestParseLRC_ColonFormat(t *testing.T) {
	// Some LRC files use colon instead of dot for milliseconds
	lrc := `[00:05:50]Colon format line`

	lines, _, err := ParseLRC(lrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 1 {
		t.Errorf("Expected 1 line, got %d", len(lines))
	}

	// 5.50 seconds = 5500ms
	if lines[0].StartTimeMs != "5500" {
		t.Errorf("Expected StartTimeMs '5500', got %q", lines[0].StartTimeMs)
	}
}

func TestStripLRCMetadata(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "Remove metadata tags",
			input: `[ar:Artist]
[ti:Title]
[00:05.00]First line
[00:10.00]Second line`,
			expected: `[00:05.00]First line
[00:10.00]Second line`,
		},
		{
			name: "Keep only timed lines",
			input: `[id:123]
[hash:abc]
[00:05.00]Lyrics`,
			expected: `[00:05.00]Lyrics`,
		},
		{
			name:     "All metadata, no lyrics",
			input:    `[ar:Artist]`,
			expected: ``,
		},
		{
			name:     "Empty input",
			input:    ``,
			expected: ``,
		},
		{
			name: "Skip blank lines",
			input: `[00:05.00]Line one

[00:10.00]Line two`,
			expected: `[00:05.00]Line one
[00:10.00]Line two`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripLRCMetadata(tt.input)
			if result != tt.expected {
				t.Errorf("StripLRCMetadata() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestNormalizeLyrics_CreditRemoval(t *testing.T) {
	t.Run("Remove head credits", func(t *testing.T) {
		// Credit lines have pattern: [timestamp]text：text
		lrc := `[00:00.00]作词：Lyricist
[00:01.00]作曲：Composer
[00:05.00]Real lyrics start here
[00:10.00]More lyrics`

		result := NormalizeLyrics(lrc)

		if strings.Contains(result, "作词") {
			t.Error("Should remove head credit lines")
		}
		if !strings.Contains(result, "Real lyrics start here") {
			t.Error("Should keep real lyrics")
		}
	})

	t.Run("Remove tail credits", func(t *testing.T) {
		// Need more than MaxHeadTailLines (30) lines so tail credit isn't in head range
		var lines []string
		for i := 0; i < 35; i++ {
			lines = append(lines, "[00:"+fmt.Sprintf("%02d", i)+".00]Lyrics line "+fmt.Sprintf("%d", i+1))
		}
		lines = append(lines, "[03:00.00]制作：Producer")
		lrc := strings.Join(lines, "\n")

		result := NormalizeLyrics(lrc)

		if strings.Contains(result, "制作") {
			t.Error("Should remove tail credit lines")
		}
		if !strings.Contains(result, "Lyrics line 1") {
			t.Error("Should keep real lyrics")
		}
	})
}

func TestNormalizeLyrics_PureMusic(t *testing.T) {
	lrc := `[00:00.00]纯音乐，请欣赏`

	result := NormalizeLyrics(lrc)

	if !strings.Contains(result, "[Instrumental Only]") {
		t.Errorf("Expected '[Instrumental Only]', got %q", result)
	}
	if strings.Contains(result, "纯音乐") {
		t.Error("Should replace pure music placeholder")
	}
}

func TestNormalizeLyrics_HTMLEntities(t *testing.T) {
	lrc := `[00:05.00]Don&apos;t stop believing`

	result := NormalizeLyrics(lrc)

	if !strings.Contains(result, "Don't") {
		t.Errorf("Should replace &apos; with apostrophe, got %q", result)
	}
}

func TestNormalizeLyrics_PreservesNormalLyrics(t *testing.T) {
	lrc := `[00:05.00]First line of lyrics
[00:10.00]Second line of lyrics
[00:15.00]Third line of lyrics`

	result := NormalizeLyrics(lrc)

	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(lines))
	}
}

func TestNormalizeLyrics_EmptyInput(t *testing.T) {
	result := NormalizeLyrics("")
	if result != "" {
		t.Errorf("Expected empty string, got %q", result)
	}
}

func TestDetectLanguage_Metadata(t *testing.T) {
	metadata := map[string]string{
		"language": "Chinese",
	}

	result := DetectLanguage(metadata, "some content")
	if result != "zh" {
		t.Errorf("Expected 'zh', got %q", result)
	}
}

func TestDetectLanguage_ContentHeuristics(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "Chinese characters",
			content:  "你好世界",
			expected: "zh",
		},
		{
			name:     "Japanese hiragana",
			content:  "こんにちは",
			expected: "ja",
		},
		{
			name:     "Japanese katakana",
			content:  "コンニチハ",
			expected: "ja",
		},
		{
			name:     "Korean",
			content:  "안녕하세요",
			expected: "ko",
		},
		{
			name:     "English only",
			content:  "Hello world",
			expected: "en",
		},
		{
			name:     "Mixed with Chinese first",
			content:  "你好 hello",
			expected: "zh",
		},
		{
			name:     "Empty content",
			content:  "",
			expected: "en",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectLanguage(nil, tt.content)
			if result != tt.expected {
				t.Errorf("DetectLanguage() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestNormalizeLanguageCode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Chinese variants
		{"Chinese (Chinese name)", "中文", "zh"},
		{"Chinese (English)", "chinese", "zh"},
		{"Chinese (abbrev)", "chi", "zh"},
		{"Mandarin", "普通话", "zh"},
		{"Cantonese", "粤语", "zh"},
		{"Mandarin alt", "国语", "zh"},

		// Japanese
		{"Japanese (Chinese name)", "日语", "ja"},
		{"Japanese (English)", "japanese", "ja"},
		{"Japanese (abbrev)", "jpn", "ja"},

		// Korean
		{"Korean (Chinese name)", "韩语", "ko"},
		{"Korean (English)", "korean", "ko"},
		{"Korean (abbrev)", "kor", "ko"},

		// English
		{"English (Chinese name)", "英语", "en"},
		{"English (English)", "english", "en"},
		{"English (abbrev)", "eng", "en"},

		// Spanish
		{"Spanish (Chinese name)", "西班牙语", "es"},
		{"Spanish (English)", "spanish", "es"},

		// French
		{"French (Chinese name)", "法语", "fr"},
		{"French (English)", "french", "fr"},

		// German
		{"German (Chinese name)", "德语", "de"},
		{"German (English)", "german", "de"},

		// Edge cases
		{"Already ISO code", "en", "en"},
		{"Short code", "zh", "zh"},
		{"Unknown long name", "Klingon", "en"}, // defaults to en
		{"Whitespace", "  english  ", "en"},
		{"Case insensitive", "ENGLISH", "en"},
		{"Empty", "", ""}, // Empty string returns as-is (len <= 3)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeLanguageCode(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeLanguageCode(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestDecodeBase64Content(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{
			name:     "Basic ASCII",
			input:    base64.StdEncoding.EncodeToString([]byte("[00:05.00]Hello")),
			expected: "[00:05.00]Hello",
		},
		{
			name:     "UTF-8 content",
			input:    base64.StdEncoding.EncodeToString([]byte("[00:05.00]你好")),
			expected: "[00:05.00]你好",
		},
		{
			name:     "With BOM",
			input:    base64.StdEncoding.EncodeToString([]byte("\ufeff[00:05.00]Content")),
			expected: "[00:05.00]Content",
		},
		{
			name:        "Invalid base64",
			input:       "not-valid-base64!!!",
			expectError: true,
		},
		{
			name:     "Empty string after decode",
			input:    base64.StdEncoding.EncodeToString([]byte("")),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := DecodeBase64Content(tt.input)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("DecodeBase64Content() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestSortLinesByStartTime(t *testing.T) {
	lrc := `[00:30.00]Third
[00:10.00]First
[00:20.00]Second`

	lines, _, err := ParseLRC(lrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 3 {
		t.Fatalf("Expected 3 lines, got %d", len(lines))
	}

	// Lines should be sorted by start time
	expectedOrder := []string{"First", "Second", "Third"}
	for i, expected := range expectedOrder {
		if lines[i].Words != expected {
			t.Errorf("Line %d: expected %q, got %q", i, expected, lines[i].Words)
		}
	}
}

func TestParseLRC_RealWorldSample(t *testing.T) {
	// Sample resembling actual Kugou LRC format
	lrc := `[ar:周杰伦]
[ti:晴天]
[al:叶惠美]
[by:Kugou]
[hash:abc123]
[00:00.00]晴天 - 周杰伦
[00:05.50]词：周杰伦
[00:08.00]曲：周杰伦
[00:15.00]故事的小黄花
[00:18.50]从出生那年就飘着
[00:22.00]童年的荡秋千
[00:25.50]随记忆一直晃到现在`

	lines, metadata, err := ParseLRC(lrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check metadata
	if metadata["artist"] != "周杰伦" {
		t.Errorf("Expected artist '周杰伦', got %q", metadata["artist"])
	}
	if metadata["title"] != "晴天" {
		t.Errorf("Expected title '晴天', got %q", metadata["title"])
	}
	if metadata["album"] != "叶惠美" {
		t.Errorf("Expected album '叶惠美', got %q", metadata["album"])
	}

	// Should have lyrics lines (excluding metadata-only tags)
	if len(lines) < 4 {
		t.Errorf("Expected at least 4 lyric lines, got %d", len(lines))
	}

	// Check language detection on content
	allContent := ""
	for _, line := range lines {
		allContent += line.Words
	}
	lang := DetectLanguage(nil, allContent)
	if lang != "zh" {
		t.Errorf("Expected language 'zh', got %q", lang)
	}
}
