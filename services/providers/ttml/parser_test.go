package ttml

import (
	"testing"
)

func TestParseTTMLTime(t *testing.T) {
	tests := []struct {
		name        string
		timeStr     string
		expectedMs  int64
		expectError bool
	}{
		{
			name:        "Seconds only with decimal",
			timeStr:     "12.34",
			expectedMs:  12340,
			expectError: false,
		},
		{
			name:        "Seconds only integer",
			timeStr:     "5",
			expectedMs:  5000,
			expectError: false,
		},
		{
			name:        "Minutes and seconds",
			timeStr:     "1:30.5",
			expectedMs:  90500,
			expectError: false,
		},
		{
			name:        "Minutes and seconds no decimal",
			timeStr:     "2:15",
			expectedMs:  135000,
			expectError: false,
		},
		{
			name:        "Hours minutes and seconds",
			timeStr:     "1:02:30.250",
			expectedMs:  3750250,
			expectError: false,
		},
		{
			name:        "Zero time",
			timeStr:     "0:00:00",
			expectedMs:  0,
			expectError: false,
		},
		{
			name:        "Hours minutes seconds no decimal",
			timeStr:     "0:01:15",
			expectedMs:  75000,
			expectError: false,
		},
		{
			name:        "Milliseconds precision",
			timeStr:     "0:00:00.123",
			expectedMs:  123,
			expectError: false,
		},
		{
			name:        "Large time value",
			timeStr:     "2:30:45.678",
			expectedMs:  9045678,
			expectError: false,
		},
		{
			name:        "Invalid format too many colons",
			timeStr:     "1:2:3:4",
			expectedMs:  0,
			expectError: true,
		},
		{
			name:        "Invalid format non-numeric",
			timeStr:     "abc",
			expectedMs:  0,
			expectError: true,
		},
		{
			name:        "Empty string",
			timeStr:     "",
			expectedMs:  0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTTMLTime(tt.timeStr)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for input %q, got nil", tt.timeStr)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input %q: %v", tt.timeStr, err)
				}
				if result != tt.expectedMs {
					t.Errorf("Expected %d ms for input %q, got %d ms", tt.expectedMs, tt.timeStr, result)
				}
			}
		})
	}
}

func TestParseTTMLToLines_UnsyncedLyrics(t *testing.T) {
	ttml := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml" timing="none">
	<head>
		<metadata xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
		</metadata>
	</head>
	<body>
		<div>
			<p>First line of lyrics</p>
			<p>Second line of lyrics</p>
			<p>Third line with <span>HTML tags</span></p>
		</div>
	</body>
</tt>`

	lines, timingType, err := parseTTMLToLines(ttml)
	if err != nil {
		t.Fatalf("Unexpected error parsing unsynced TTML: %v", err)
	}

	if timingType != "none" {
		t.Errorf("Expected timing type 'none', got %q", timingType)
	}

	if len(lines) != 3 {
		t.Fatalf("Expected 3 lines, got %d", len(lines))
	}

	expectedLines := []string{
		"First line of lyrics",
		"Second line of lyrics",
		"Third line with HTML tags",
	}

	for i, expectedText := range expectedLines {
		if lines[i].Words != expectedText {
			t.Errorf("Line %d: expected %q, got %q", i, expectedText, lines[i].Words)
		}
		if lines[i].StartTimeMs != "0" {
			t.Errorf("Line %d: expected StartTimeMs '0', got %q", i, lines[i].StartTimeMs)
		}
		if lines[i].EndTimeMs != "0" {
			t.Errorf("Line %d: expected EndTimeMs '0', got %q", i, lines[i].EndTimeMs)
		}
		if lines[i].DurationMs != "0" {
			t.Errorf("Line %d: expected DurationMs '0', got %q", i, lines[i].DurationMs)
		}
		if len(lines[i].Syllables) != 0 {
			t.Errorf("Line %d: expected empty syllables, got %d", i, len(lines[i].Syllables))
		}
	}
}

func TestParseTTMLToLines_WordLevel(t *testing.T) {
	// Note: Spans are on one line to avoid whitespace being treated as gap text
	ttml := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml" xmlns:itunes="http://ttml-endpoint.com/" itunes:timing="word">
	<head>
		<metadata xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
		</metadata>
	</head>
	<body>
		<div>
			<p begin="0:00:01.000" end="0:00:03.500"><span begin="0:00:01.000" end="0:00:01.500">Hello</span><span begin="0:00:01.500" end="0:00:02.000"> </span><span begin="0:00:02.000" end="0:00:03.500">world</span></p>
		</div>
	</body>
</tt>`

	lines, timingType, err := parseTTMLToLines(ttml)
	if err != nil {
		t.Fatalf("Unexpected error parsing word-level TTML: %v", err)
	}

	if timingType != "word" {
		t.Errorf("Expected timing type 'word', got %q", timingType)
	}

	if len(lines) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(lines))
	}

	line := lines[0]
	if line.Words != "Hello world" {
		t.Errorf("Expected words 'Hello world', got %q", line.Words)
	}
	if line.StartTimeMs != "1000" {
		t.Errorf("Expected StartTimeMs '1000', got %q", line.StartTimeMs)
	}
	if line.EndTimeMs != "3500" {
		t.Errorf("Expected EndTimeMs '3500', got %q", line.EndTimeMs)
	}
	if line.DurationMs != "2500" {
		t.Errorf("Expected DurationMs '2500', got %q", line.DurationMs)
	}

	// Note: The parser trims whitespace from span text, so spans with only spaces get skipped
	// The space becomes "gap text" with zero duration (start and end both at first syllable's start time)
	if len(line.Syllables) != 3 {
		t.Fatalf("Expected 3 syllables, got %d", len(line.Syllables))
	}

	expectedSyllables := []struct {
		text      string
		startTime string
		endTime   string
	}{
		{"Hello", "1000", "1500"},
		{" ", "1000", "1000"}, // Gap text: zero duration at first syllable's start time
		{"world", "2000", "3500"},
	}

	for i, expected := range expectedSyllables {
		syl := line.Syllables[i]
		if syl.Text != expected.text {
			t.Errorf("Syllable %d: expected text %q, got %q", i, expected.text, syl.Text)
		}
		if syl.StartTime != expected.startTime {
			t.Errorf("Syllable %d: expected start time %q, got %q", i, expected.startTime, syl.StartTime)
		}
		if syl.EndTime != expected.endTime {
			t.Errorf("Syllable %d: expected end time %q, got %q", i, expected.endTime, syl.EndTime)
		}
	}
}

func TestParseTTMLToLines_LineLevel(t *testing.T) {
	ttml := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml" timing="line">
	<head>
		<metadata xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
		</metadata>
	</head>
	<body>
		<div>
			<p begin="0:00:01.000" end="0:00:03.000">First line of lyrics</p>
			<p begin="0:00:03.500" end="0:00:06.000">Second line of lyrics</p>
		</div>
	</body>
</tt>`

	lines, timingType, err := parseTTMLToLines(ttml)
	if err != nil {
		t.Fatalf("Unexpected error parsing line-level TTML: %v", err)
	}

	if timingType != "line" {
		t.Errorf("Expected timing type 'line', got %q", timingType)
	}

	if len(lines) != 2 {
		t.Fatalf("Expected 2 lines, got %d", len(lines))
	}

	expectedLines := []struct {
		words       string
		startTimeMs string
		endTimeMs   string
		durationMs  string
	}{
		{"First line of lyrics", "1000", "3000", "2000"},
		{"Second line of lyrics", "3500", "6000", "2500"},
	}

	for i, expected := range expectedLines {
		line := lines[i]
		if line.Words != expected.words {
			t.Errorf("Line %d: expected words %q, got %q", i, expected.words, line.Words)
		}
		if line.StartTimeMs != expected.startTimeMs {
			t.Errorf("Line %d: expected start time %q, got %q", i, expected.startTimeMs, line.StartTimeMs)
		}
		if line.EndTimeMs != expected.endTimeMs {
			t.Errorf("Line %d: expected end time %q, got %q", i, expected.endTimeMs, line.EndTimeMs)
		}
		if line.DurationMs != expected.durationMs {
			t.Errorf("Line %d: expected duration %q, got %q", i, expected.durationMs, line.DurationMs)
		}
		if len(line.Syllables) != 0 {
			t.Errorf("Line %d: expected empty syllables for line-level timing, got %d", i, len(line.Syllables))
		}
	}
}

func TestParseTTMLToLines_BackgroundVocals(t *testing.T) {
	// Note: Spans on one line to avoid whitespace being treated as gap text
	ttml := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml" timing="word">
	<head>
		<metadata xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
		</metadata>
	</head>
	<body>
		<div>
			<p begin="0:00:01.000" end="0:00:03.000"><span begin="0:00:01.000" end="0:00:02.000">Main</span><span begin="0:00:02.000" end="0:00:03.000" role="x-bg">Background</span></p>
		</div>
	</body>
</tt>`

	lines, _, err := parseTTMLToLines(ttml)
	if err != nil {
		t.Fatalf("Unexpected error parsing TTML with background vocals: %v", err)
	}

	if len(lines) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(lines))
	}

	if len(lines[0].Syllables) != 2 {
		t.Fatalf("Expected 2 syllables, got %d", len(lines[0].Syllables))
	}

	if lines[0].Syllables[0].IsBackground != false {
		t.Errorf("Expected first syllable to not be background")
	}
	if lines[0].Syllables[1].IsBackground != true {
		t.Errorf("Expected second syllable to be background")
	}
}

func TestParseTTMLToLines_NestedBackgroundVocals(t *testing.T) {
	ttml := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml" timing="word">
	<head>
		<metadata xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
		</metadata>
	</head>
	<body>
		<div>
			<p begin="0:00:01.000" end="0:00:03.000">
				<span begin="0:00:01.000" end="0:00:02.000">Main</span>
				<span role="x-bg">
					<span begin="0:00:02.000" end="0:00:02.500">Nested</span>
					<span begin="0:00:02.500" end="0:00:03.000">Background</span>
				</span>
			</p>
		</div>
	</body>
</tt>`

	lines, _, err := parseTTMLToLines(ttml)
	if err != nil {
		t.Fatalf("Unexpected error parsing TTML with nested background vocals: %v", err)
	}

	if len(lines) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(lines))
	}

	if len(lines[0].Syllables) < 3 {
		t.Fatalf("Expected at least 3 syllables, got %d", len(lines[0].Syllables))
	}

	// Check that nested spans are marked as background
	foundNested := false
	foundBackground := false
	for _, syl := range lines[0].Syllables {
		if syl.Text == "Nested" && syl.IsBackground {
			foundNested = true
		}
		if syl.Text == "Background" && syl.IsBackground {
			foundBackground = true
		}
	}

	if !foundNested {
		t.Errorf("Expected to find nested syllable marked as background")
	}
	if !foundBackground {
		t.Errorf("Expected to find background syllable marked as background")
	}
}

func TestParseTTMLToLines_EmptyParagraphs(t *testing.T) {
	ttml := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml" timing="word">
	<head>
		<metadata xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
		</metadata>
	</head>
	<body>
		<div>
			<p begin="0:00:01.000" end="0:00:02.000"></p>
			<p begin="0:00:02.000" end="0:00:03.000">
				<span begin="0:00:02.000" end="0:00:03.000">Valid</span>
			</p>
		</div>
	</body>
</tt>`

	lines, _, err := parseTTMLToLines(ttml)
	if err != nil {
		t.Fatalf("Unexpected error parsing TTML with empty paragraphs: %v", err)
	}

	// Empty paragraphs should be skipped
	if len(lines) != 1 {
		t.Errorf("Expected 1 line (empty paragraph skipped), got %d", len(lines))
	}

	if lines[0].Words != "Valid" {
		t.Errorf("Expected 'Valid', got %q", lines[0].Words)
	}
}

func TestParseTTMLToLines_InvalidXML(t *testing.T) {
	invalidTTML := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml">
	<body>
		<div>
			<p>Unclosed paragraph
		</div>
	</body>`

	_, _, err := parseTTMLToLines(invalidTTML)
	if err == nil {
		t.Error("Expected error parsing invalid XML, got nil")
	}
}

func TestParseTTMLToLines_WithAgents(t *testing.T) {
	ttml := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml" timing="line">
	<head>
		<metadata xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
			<ttm:agent type="person" id="v1"/>
			<ttm:agent type="person" id="v2"/>
		</metadata>
	</head>
	<body>
		<div>
			<p begin="0:00:01.000" end="0:00:02.000" agent="v1">First singer</p>
			<p begin="0:00:02.000" end="0:00:03.000" agent="v2">Second singer</p>
		</div>
	</body>
</tt>`

	lines, _, err := parseTTMLToLines(ttml)
	if err != nil {
		t.Fatalf("Unexpected error parsing TTML with agents: %v", err)
	}

	if len(lines) != 2 {
		t.Fatalf("Expected 2 lines, got %d", len(lines))
	}

	if lines[0].Agent != "person:v1" {
		t.Errorf("Expected agent 'person:v1', got %q", lines[0].Agent)
	}
	if lines[1].Agent != "person:v2" {
		t.Errorf("Expected agent 'person:v2', got %q", lines[1].Agent)
	}
}

func TestParseTTMLToLines_DefaultTimingType(t *testing.T) {
	// TTML without explicit timing attribute should default to "line"
	ttml := `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns="http://www.w3.org/ns/ttml">
	<head>
		<metadata xmlns:ttm="http://www.w3.org/ns/ttml#metadata">
		</metadata>
	</head>
	<body>
		<div>
			<p begin="0:00:01.000" end="0:00:02.000">Test line</p>
		</div>
	</body>
</tt>`

	_, timingType, err := parseTTMLToLines(ttml)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if timingType != "line" {
		t.Errorf("Expected default timing type 'line', got %q", timingType)
	}
}
