package providers

// Syllable represents a single word/syllable with timing information
type Syllable struct {
	Text         string `json:"text"`
	StartTime    string `json:"startTimeMs"`
	EndTime      string `json:"endTimeMs"`
	IsBackground bool   `json:"isBackground"`
}

// Line represents a lyrics line with timing information
type Line struct {
	StartTimeMs string     `json:"startTimeMs"`
	DurationMs  string     `json:"durationMs"`
	Words       string     `json:"words"`
	Syllables   []Syllable `json:"syllables"`
	EndTimeMs   string     `json:"endTimeMs"`
	Agent       string     `json:"agent,omitempty"`
}

// LyricsResult is the standardized result from any lyrics provider
type LyricsResult struct {
	// RawLyrics contains the raw lyrics data (TTML XML, LRC text, etc.)
	RawLyrics string `json:"rawLyrics,omitempty"`

	// Lines contains parsed lyrics lines with timing
	Lines []Line `json:"lines,omitempty"`

	// TrackDurationMs is the duration of the matched track in milliseconds
	TrackDurationMs int `json:"trackDurationMs,omitempty"`

	// Score is the match confidence (0.0 to 1.0)
	Score float64 `json:"score,omitempty"`

	// Provider is the name of the provider that returned these lyrics
	Provider string `json:"provider"`

	// Language is the detected or reported language code (e.g., "en", "zh")
	Language string `json:"language,omitempty"`

	// IsRTL indicates if the lyrics are in a right-to-left language
	IsRTL bool `json:"isRtlLanguage,omitempty"`
}

// ProviderError represents an error from a provider with additional context
type ProviderError struct {
	Provider string
	Message  string
	Err      error
}

func (e *ProviderError) Error() string {
	if e.Err != nil {
		return e.Provider + ": " + e.Message + ": " + e.Err.Error()
	}
	return e.Provider + ": " + e.Message
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// NewProviderError creates a new ProviderError
func NewProviderError(provider, message string, err error) *ProviderError {
	return &ProviderError{
		Provider: provider,
		Message:  message,
		Err:      err,
	}
}

// IsRTLLanguage checks if a language code is right-to-left
func IsRTLLanguage(langCode string) bool {
	rtlLanguages := map[string]bool{
		"ar": true, // Arabic
		"fa": true, // Persian (Farsi)
		"he": true, // Hebrew
		"ur": true, // Urdu
		"ps": true, // Pashto
		"sd": true, // Sindhi
		"ug": true, // Uyghur
		"yi": true, // Yiddish
		"ku": true, // Kurdish (some dialects)
		"dv": true, // Divehi (Maldivian)
	}
	return rtlLanguages[langCode]
}
