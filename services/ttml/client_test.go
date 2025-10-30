package ttml

import (
	"math"
	"testing"
)

func TestNormalizeString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Lowercase conversion",
			input:    "Hello World",
			expected: "hello world",
		},
		{
			name:     "Trim spaces",
			input:    "  test  ",
			expected: "test",
		},
		{
			name:     "Mixed case and spaces",
			input:    "  Shape Of You  ",
			expected: "shape of you",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Only spaces",
			input:    "   ",
			expected: "",
		},
		{
			name:     "Already normalized",
			input:    "already normalized",
			expected: "already normalized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeString(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestStringSimilarity(t *testing.T) {
	tests := []struct {
		name          string
		s1            string
		s2            string
		expectedMin   float64
		expectedMax   float64
		expectExact   bool
	}{
		{
			name:        "Exact match",
			s1:          "Shape of You",
			s2:          "Shape of You",
			expectExact: true,
		},
		{
			name:        "Exact match different case",
			s1:          "Shape Of You",
			s2:          "shape of you",
			expectExact: true,
		},
		{
			name:        "Contains match",
			s1:          "Bohemian Rhapsody",
			s2:          "Bohemian",
			expectedMin: 0.7,
			expectedMax: 1.0,
		},
		{
			name:        "Contains match reverse",
			s1:          "Rhapsody",
			s2:          "Bohemian Rhapsody",
			expectedMin: 0.7,
			expectedMax: 1.0,
		},
		{
			name:        "Similar strings",
			s1:          "Ed Sheeran",
			s2:          "Ed Sheeran feat. Someone",
			expectedMin: 0.4,
			expectedMax: 0.9,
		},
		{
			name:        "Character overlap",
			s1:          "The Beatles",
			s2:          "Beatles",
			expectedMin: 0.5,
			expectedMax: 0.9,
		},
		{
			name:        "Completely different",
			s1:          "Hello",
			s2:          "Goodbye",
			expectedMin: 0.0,
			expectedMax: 0.4,
		},
		{
			name:        "Empty first string",
			s1:          "",
			s2:          "test",
			expectedMin: 0.0,
			expectedMax: 0.0,
		},
		{
			name:        "Empty second string",
			s1:          "test",
			s2:          "",
			expectedMin: 0.0,
			expectedMax: 0.0,
		},
		{
			name:        "Both empty",
			s1:          "",
			s2:          "",
			expectedMin: 0.0,
			expectedMax: 0.0,
		},
		{
			name:        "Same with extra leading/trailing spaces",
			s1:          "Shape of You",
			s2:          "  Shape of You  ",
			expectExact: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stringSimilarity(tt.s1, tt.s2)

			if tt.expectExact {
				if result != 1.0 {
					t.Errorf("Expected exact match (1.0), got %.3f", result)
				}
			} else {
				if result < tt.expectedMin || result > tt.expectedMax {
					t.Errorf("Expected score between %.3f and %.3f, got %.3f",
						tt.expectedMin, tt.expectedMax, result)
				}
			}

			// Score should always be between 0 and 1
			if result < 0.0 || result > 1.0 {
				t.Errorf("Score out of range [0, 1]: %.3f", result)
			}
		})
	}
}

func TestDurationScore(t *testing.T) {
	tests := []struct {
		name             string
		trackDurationMs  int
		targetDurationMs int
		expectedMin      float64
		expectedMax      float64
	}{
		{
			name:             "Exact match",
			trackDurationMs:  234000,
			targetDurationMs: 234000,
			expectedMin:      0.99,
			expectedMax:      1.0,
		},
		{
			name:             "5 second difference",
			trackDurationMs:  234000,
			targetDurationMs: 239000,
			expectedMin:      0.75,
			expectedMax:      0.85,
		},
		{
			name:             "10 second difference",
			trackDurationMs:  234000,
			targetDurationMs: 244000,
			expectedMin:      0.55,
			expectedMax:      0.65,
		},
		{
			name:             "30 second difference",
			trackDurationMs:  234000,
			targetDurationMs: 264000,
			expectedMin:      0.15,
			expectedMax:      0.25,
		},
		{
			name:             "1 minute difference",
			trackDurationMs:  234000,
			targetDurationMs: 294000,
			expectedMin:      0.04,
			expectedMax:      0.10,
		},
		{
			name:             "No target duration",
			trackDurationMs:  234000,
			targetDurationMs: 0,
			expectedMin:      0.0,
			expectedMax:      0.0,
		},
		{
			name:             "Negative target (invalid)",
			trackDurationMs:  234000,
			targetDurationMs: -1000,
			expectedMin:      0.0,
			expectedMax:      0.0,
		},
		{
			name:             "Small difference (1 second)",
			trackDurationMs:  234000,
			targetDurationMs: 235000,
			expectedMin:      0.95,
			expectedMax:      1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := durationScore(tt.trackDurationMs, tt.targetDurationMs)

			if result < tt.expectedMin || result > tt.expectedMax {
				t.Errorf("Expected score between %.3f and %.3f, got %.3f",
					tt.expectedMin, tt.expectedMax, result)
			}

			// Score should always be between 0 and 1
			if result < 0.0 || result > 1.0 {
				t.Errorf("Score out of range [0, 1]: %.3f", result)
			}
		})
	}
}

func TestDurationScore_Symmetry(t *testing.T) {
	// Duration score should be symmetric: diff of +10s = diff of -10s
	trackDuration := 234000
	targetDuration1 := 244000 // +10s
	targetDuration2 := 224000 // -10s

	score1 := durationScore(trackDuration, targetDuration1)
	score2 := durationScore(trackDuration, targetDuration2)

	if math.Abs(score1-score2) > 0.001 {
		t.Errorf("Duration score not symmetric: +10s gave %.3f, -10s gave %.3f", score1, score2)
	}
}

func TestScoreTrack(t *testing.T) {
	track := &Track{
		ID: "test123",
		Attributes: struct {
			Name             string `json:"name"`
			ArtistName       string `json:"artistName"`
			AlbumName        string `json:"albumName"`
			DurationInMillis int    `json:"durationInMillis"`
			URL              string `json:"url"`
			ISRC             string `json:"isrc"`
			SongwriterNames  string `json:"songwriterName"`
		}{
			Name:             "Shape of You",
			ArtistName:       "Ed Sheeran",
			AlbumName:        "Divide",
			DurationInMillis: 233712,
		},
	}

	tests := []struct {
		name             string
		songName         string
		artistName       string
		albumName        string
		durationMs       int
		expectedScoreMin float64
		expectedScoreMax float64
	}{
		{
			name:             "Perfect match all fields",
			songName:         "Shape of You",
			artistName:       "Ed Sheeran",
			albumName:        "Divide",
			durationMs:       233712,
			expectedScoreMin: 0.95,
			expectedScoreMax: 1.01, // Allow slight float precision overage
		},
		{
			name:             "Perfect match without duration",
			songName:         "Shape of You",
			artistName:       "Ed Sheeran",
			albumName:        "Divide",
			durationMs:       0,
			expectedScoreMin: 0.95,
			expectedScoreMax: 1.01, // Allow slight float precision overage
		},
		{
			name:             "Case variation",
			songName:         "SHAPE OF YOU",
			artistName:       "ed sheeran",
			albumName:        "divide",
			durationMs:       233712,
			expectedScoreMin: 0.95,
			expectedScoreMax: 1.01, // Allow slight float precision overage
		},
		{
			name:             "Similar song name",
			songName:         "Shape of You (Remix)",
			artistName:       "Ed Sheeran",
			albumName:        "Divide",
			durationMs:       233712,
			expectedScoreMin: 0.85,
			expectedScoreMax: 1.0,
		},
		{
			name:             "Different album same song",
			songName:         "Shape of You",
			artistName:       "Ed Sheeran",
			albumName:        "Greatest Hits",
			durationMs:       233712,
			expectedScoreMin: 0.85,
			expectedScoreMax: 0.95,
		},
		{
			name:             "Duration mismatch",
			songName:         "Shape of You",
			artistName:       "Ed Sheeran",
			albumName:        "Divide",
			durationMs:       300000, // Very different (66 seconds off)
			expectedScoreMin: 0.75,
			expectedScoreMax: 0.85,
		},
		{
			name:             "Wrong artist",
			songName:         "Shape of You",
			artistName:       "Taylor Swift",
			albumName:        "Divide",
			durationMs:       233712,
			expectedScoreMin: 0.70,
			expectedScoreMax: 0.85,
		},
		{
			name:             "Completely wrong",
			songName:         "Bohemian Rhapsody",
			artistName:       "Queen",
			albumName:        "A Night at the Opera",
			durationMs:       354000,
			expectedScoreMin: 0.0,
			expectedScoreMax: 0.45,
		},
		{
			name:             "Empty strings",
			songName:         "",
			artistName:       "",
			albumName:        "",
			durationMs:       0,
			expectedScoreMin: 0.0,
			expectedScoreMax: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scoreTrack(track, tt.songName, tt.artistName, tt.albumName, tt.durationMs)

			if result.TotalScore < tt.expectedScoreMin || result.TotalScore > tt.expectedScoreMax {
				t.Errorf("Expected total score between %.3f and %.3f, got %.3f",
					tt.expectedScoreMin, tt.expectedScoreMax, result.TotalScore)
			}

			// Verify all component scores are in valid range [0, 1]
			const epsilon = 0.0001 // Small tolerance for floating point
			if result.NameScore < 0.0 || result.NameScore > 1.0+epsilon {
				t.Errorf("Name score out of range [0, 1]: %.3f", result.NameScore)
			}
			if result.ArtistScore < 0.0 || result.ArtistScore > 1.0+epsilon {
				t.Errorf("Artist score out of range [0, 1]: %.3f", result.ArtistScore)
			}
			if result.AlbumScore < 0.0 || result.AlbumScore > 1.0+epsilon {
				t.Errorf("Album score out of range [0, 1]: %.3f", result.AlbumScore)
			}
			if result.DurationScore < 0.0 || result.DurationScore > 1.0+epsilon {
				t.Errorf("Duration score out of range [0, 1]: %.3f", result.DurationScore)
			}
			if result.TotalScore < 0.0 || result.TotalScore > 1.0+epsilon {
				t.Errorf("Total score out of range [0, 1]: %.3f", result.TotalScore)
			}

			// Verify track reference
			if result.Track != track {
				t.Error("Track reference not preserved in score")
			}
		})
	}
}

func TestScoreTrack_WeightRedistribution(t *testing.T) {
	track := &Track{
		Attributes: struct {
			Name             string `json:"name"`
			ArtistName       string `json:"artistName"`
			AlbumName        string `json:"albumName"`
			DurationInMillis int    `json:"durationInMillis"`
			URL              string `json:"url"`
			ISRC             string `json:"isrc"`
			SongwriterNames  string `json:"songwriterName"`
		}{
			Name:             "Test Song",
			ArtistName:       "Test Artist",
			AlbumName:        "Test Album",
			DurationInMillis: 200000,
		},
	}

	// Score with duration
	scoreWithDuration := scoreTrack(track, "Test Song", "Test Artist", "Test Album", 200000)

	// Score without duration
	scoreWithoutDuration := scoreTrack(track, "Test Song", "Test Artist", "Test Album", 0)

	// Without duration, score should still be high for perfect name/artist/album match
	if scoreWithoutDuration.TotalScore < 0.95 {
		t.Errorf("Expected high score without duration for perfect match, got %.3f", scoreWithoutDuration.TotalScore)
	}

	// Duration score should be 0 when no target duration
	if scoreWithoutDuration.DurationScore != 0.0 {
		t.Errorf("Expected duration score 0 when no target duration, got %.3f", scoreWithoutDuration.DurationScore)
	}

	// With duration and perfect match, score should be very high
	if scoreWithDuration.TotalScore < 0.95 {
		t.Errorf("Expected very high score with duration for perfect match, got %.3f", scoreWithDuration.TotalScore)
	}
}

func TestScoreTrack_ComponentScores(t *testing.T) {
	track := &Track{
		Attributes: struct {
			Name             string `json:"name"`
			ArtistName       string `json:"artistName"`
			AlbumName        string `json:"albumName"`
			DurationInMillis int    `json:"durationInMillis"`
			URL              string `json:"url"`
			ISRC             string `json:"isrc"`
			SongwriterNames  string `json:"songwriterName"`
		}{
			Name:             "Test Song",
			ArtistName:       "The Beatles",
			AlbumName:        "Abbey Road",
			DurationInMillis: 200000,
		},
	}

	score := scoreTrack(track, "Test Song", "Drake", "Scorpion", 250000)

	// Name should match perfectly
	if score.NameScore < 0.99 {
		t.Errorf("Expected name score ~1.0, got %.3f", score.NameScore)
	}

	// Artist should not match (completely different)
	if score.ArtistScore > 0.4 {
		t.Errorf("Expected low artist score for 'The Beatles' vs 'Drake', got %.3f", score.ArtistScore)
	}

	// Album should not match (completely different)
	if score.AlbumScore > 0.5 {
		t.Errorf("Expected low album score for 'Abbey Road' vs 'Scorpion', got %.3f", score.AlbumScore)
	}

	// Duration is different but not too far (50 seconds)
	if score.DurationScore < 0.05 || score.DurationScore > 0.15 {
		t.Errorf("Expected moderate duration score for 50s difference, got %.3f", score.DurationScore)
	}
}

func TestStringSimilarity_RealWorldExamples(t *testing.T) {
	tests := []struct {
		name        string
		s1          string
		s2          string
		expectedMin float64
	}{
		{
			name:        "Feat variations",
			s1:          "Shape of You (feat. Someone)",
			s2:          "Shape of You",
			expectedMin: 0.7,
		},
		{
			name:        "Remix variations",
			s1:          "Blinding Lights (Remix)",
			s2:          "Blinding Lights",
			expectedMin: 0.7,
		},
		{
			name:        "Live version",
			s1:          "Bohemian Rhapsody (Live)",
			s2:          "Bohemian Rhapsody",
			expectedMin: 0.7,
		},
		{
			name:        "Remastered",
			s1:          "Let It Be - Remastered 2009",
			s2:          "Let It Be",
			expectedMin: 0.7,
		},
		{
			name:        "Single vs Album",
			s1:          "Single",
			s2:          "Album",
			expectedMin: 0.0,
		},
		{
			name:        "The prefix",
			s1:          "The Beatles",
			s2:          "Beatles",
			expectedMin: 0.6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stringSimilarity(tt.s1, tt.s2)
			if result < tt.expectedMin {
				t.Errorf("Expected score >= %.3f, got %.3f for %q vs %q",
					tt.expectedMin, result, tt.s1, tt.s2)
			}
		})
	}
}

func TestScoreTrack_Comparison(t *testing.T) {
	// Create two tracks: one is perfect match, other is not
	perfectTrack := &Track{
		ID: "perfect",
		Attributes: struct {
			Name             string `json:"name"`
			ArtistName       string `json:"artistName"`
			AlbumName        string `json:"albumName"`
			DurationInMillis int    `json:"durationInMillis"`
			URL              string `json:"url"`
			ISRC             string `json:"isrc"`
			SongwriterNames  string `json:"songwriterName"`
		}{
			Name:             "Shape of You",
			ArtistName:       "Ed Sheeran",
			AlbumName:        "Divide",
			DurationInMillis: 233000,
		},
	}

	wrongTrack := &Track{
		ID: "wrong",
		Attributes: struct {
			Name             string `json:"name"`
			ArtistName       string `json:"artistName"`
			AlbumName        string `json:"albumName"`
			DurationInMillis int    `json:"durationInMillis"`
			URL              string `json:"url"`
			ISRC             string `json:"isrc"`
			SongwriterNames  string `json:"songwriterName"`
		}{
			Name:             "Shape of My Heart",
			ArtistName:       "Sting",
			AlbumName:        "Ten Summoner's Tales",
			DurationInMillis: 270000,
		},
	}

	perfectScore := scoreTrack(perfectTrack, "Shape of You", "Ed Sheeran", "Divide", 233000)
	wrongScore := scoreTrack(wrongTrack, "Shape of You", "Ed Sheeran", "Divide", 233000)

	// Perfect match should score significantly higher
	if perfectScore.TotalScore <= wrongScore.TotalScore {
		t.Errorf("Perfect match scored %.3f, wrong match scored %.3f - perfect should be higher",
			perfectScore.TotalScore, wrongScore.TotalScore)
	}

	// Perfect match should be very high (> 0.95)
	if perfectScore.TotalScore < 0.95 {
		t.Errorf("Perfect match should score > 0.95, got %.3f", perfectScore.TotalScore)
	}

	// Wrong match should be noticeably lower
	// "Shape of My Heart" by Sting has some overlap with "Shape of You" by Ed Sheeran
	// due to "Shape of" being common, but artist/album/duration are wrong
	if wrongScore.TotalScore > 0.65 {
		t.Errorf("Wrong match should score < 0.65, got %.3f", wrongScore.TotalScore)
	}

	// Difference should be significant (at least 0.3 points)
	diff := perfectScore.TotalScore - wrongScore.TotalScore
	if diff < 0.3 {
		t.Errorf("Score difference should be >= 0.3, got %.3f", diff)
	}
}
