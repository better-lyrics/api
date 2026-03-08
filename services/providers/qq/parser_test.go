package qq

import (
	"testing"
)

func TestParseQRC_BasicFormat(t *testing.T) {
	qrc := `[ti:Test Song]
[ar:Test Artist]
[0,4145]Viva (0,829)la (829,829)Vida (1658,829)
[13268,3323]I (13268,191)used (13459,227)to (13686,492)rule (14178,1253)the (15431,206)world(15637,954)`

	lines, metadata, err := ParseQRC(qrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 2 {
		t.Fatalf("Expected 2 lines, got %d", len(lines))
	}

	// Check metadata
	if metadata["title"] != "Test Song" {
		t.Errorf("Expected title 'Test Song', got %q", metadata["title"])
	}
	if metadata["artist"] != "Test Artist" {
		t.Errorf("Expected artist 'Test Artist', got %q", metadata["artist"])
	}

	// Check first line timing
	if lines[0].StartTimeMs != "0" {
		t.Errorf("Expected StartTimeMs '0', got %q", lines[0].StartTimeMs)
	}
	if lines[0].EndTimeMs != "4145" {
		t.Errorf("Expected EndTimeMs '4145', got %q", lines[0].EndTimeMs)
	}
	if lines[0].DurationMs != "4145" {
		t.Errorf("Expected DurationMs '4145', got %q", lines[0].DurationMs)
	}

	// Check first line has syllables with word-level timing
	if len(lines[0].Syllables) != 3 {
		t.Fatalf("Expected 3 syllables in first line, got %d", len(lines[0].Syllables))
	}

	// Check syllable text
	expectedSyllables := []string{"Viva", "la", "Vida"}
	for i, expected := range expectedSyllables {
		if lines[0].Syllables[i].Text != expected {
			t.Errorf("Syllable %d: expected %q, got %q", i, expected, lines[0].Syllables[i].Text)
		}
	}

	// Check first syllable timing
	if lines[0].Syllables[0].StartTime != "0" {
		t.Errorf("Expected first syllable StartTime '0', got %q", lines[0].Syllables[0].StartTime)
	}
	if lines[0].Syllables[0].EndTime != "829" {
		t.Errorf("Expected first syllable EndTime '829', got %q", lines[0].Syllables[0].EndTime)
	}
}

func TestParseQRC_SecondLine(t *testing.T) {
	qrc := `[13268,3323]I (13268,191)used (13459,227)to (13686,492)rule (14178,1253)the (15431,206)world(15637,954)`

	lines, _, err := ParseQRC(qrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(lines))
	}

	// Check line timing
	if lines[0].StartTimeMs != "13268" {
		t.Errorf("Expected StartTimeMs '13268', got %q", lines[0].StartTimeMs)
	}
	if lines[0].EndTimeMs != "16591" {
		t.Errorf("Expected EndTimeMs '16591', got %q", lines[0].EndTimeMs)
	}

	// Check syllables
	if len(lines[0].Syllables) != 6 {
		t.Fatalf("Expected 6 syllables, got %d", len(lines[0].Syllables))
	}

	expectedWords := []string{"I", "used", "to", "rule", "the", "world"}
	for i, expected := range expectedWords {
		if lines[0].Syllables[i].Text != expected {
			t.Errorf("Syllable %d: expected %q, got %q", i, expected, lines[0].Syllables[i].Text)
		}
	}

	// Verify word timing for "used"
	if lines[0].Syllables[1].StartTime != "13459" {
		t.Errorf("Expected 'used' StartTime '13459', got %q", lines[0].Syllables[1].StartTime)
	}
	if lines[0].Syllables[1].EndTime != "13686" {
		t.Errorf("Expected 'used' EndTime '13686', got %q", lines[0].Syllables[1].EndTime)
	}
}

func TestParseQRC_MetadataOnly(t *testing.T) {
	qrc := `[ti:Title]
[ar:Artist]
[al:Album]
[by:Creator]
[offset:500]`

	lines, metadata, err := ParseQRC(qrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 0 {
		t.Errorf("Expected 0 lines, got %d", len(lines))
	}

	expected := map[string]string{
		"title":   "Title",
		"artist":  "Artist",
		"album":   "Album",
		"creator": "Creator",
		"offset":  "500",
	}

	for key, val := range expected {
		if metadata[key] != val {
			t.Errorf("metadata[%q] = %q, expected %q", key, metadata[key], val)
		}
	}
}

func TestParseQRC_EmptyInput(t *testing.T) {
	lines, metadata, err := ParseQRC("")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 0 {
		t.Errorf("Expected 0 lines, got %d", len(lines))
	}

	if len(metadata) != 0 {
		t.Errorf("Expected empty metadata, got %d entries", len(metadata))
	}
}

func TestParseQRC_EmptyTextLines(t *testing.T) {
	// Lines with timing but no word content should be skipped
	qrc := `[0,1000]
[1000,2000]Hello (1000,500)world(1500,500)`

	lines, _, err := ParseQRC(qrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 1 {
		t.Errorf("Expected 1 line (empty line skipped), got %d", len(lines))
	}

	if lines[0].Words != "Hello world" {
		t.Errorf("Expected 'Hello world', got %q", lines[0].Words)
	}
}

func TestParseQRC_FullLineText(t *testing.T) {
	qrc := `[0,4145]Viva (0,829)la (829,829)Vida (1658,829)`

	lines, _, err := ParseQRC(qrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(lines))
	}

	if lines[0].Words != "Viva la Vida" {
		t.Errorf("Expected 'Viva la Vida', got %q", lines[0].Words)
	}
}

func TestParseQRC_ChineseContent(t *testing.T) {
	qrc := `[ti:晴天]
[ar:周杰伦]
[0,5000]故事(0,1000)的(1000,500)小(1500,500)黄花(2000,1000)
[5000,4000]从(5000,500)出生(5500,1000)那年(6500,1000)就(7500,500)飘着(8000,1000)`

	lines, metadata, err := ParseQRC(qrc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(lines) != 2 {
		t.Fatalf("Expected 2 lines, got %d", len(lines))
	}

	if metadata["title"] != "晴天" {
		t.Errorf("Expected title '晴天', got %q", metadata["title"])
	}

	if lines[0].Words != "故事的小黄花" {
		t.Errorf("Expected '故事的小黄花', got %q", lines[0].Words)
	}

	// Verify syllable count
	if len(lines[0].Syllables) != 4 {
		t.Errorf("Expected 4 syllables, got %d", len(lines[0].Syllables))
	}
}

func TestDetectLanguage_ContentHeuristics(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"Chinese characters", "你好世界", "zh"},
		{"Japanese hiragana", "こんにちは", "ja"},
		{"Japanese katakana", "コンニチハ", "ja"},
		{"Korean", "안녕하세요", "ko"},
		{"English only", "Hello world", "en"},
		{"Empty content", "", "en"},
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

func TestDetectLanguage_Metadata(t *testing.T) {
	metadata := map[string]string{
		"language": "Chinese",
	}

	result := DetectLanguage(metadata, "some content")
	if result != "zh" {
		t.Errorf("Expected 'zh', got %q", result)
	}
}

func TestNormalizeLanguageCode(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"中文", "zh"},
		{"chinese", "zh"},
		{"english", "en"},
		{"日语", "ja"},
		{"korean", "ko"},
		{"en", "en"},
		{"zh", "zh"},
		{"  english  ", "en"},
		{"Klingon", "en"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeLanguageCode(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeLanguageCode(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}
