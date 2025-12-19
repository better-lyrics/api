package legacy

// TokenData represents the access token response from the token endpoint
type TokenData struct {
	AccessToken                      string `json:"accessToken"`
	AccessTokenExpirationTimestampMs int64  `json:"accessTokenExpirationTimestampMs"`
}

// OAuthTokenResponse represents the Spotify OAuth token response
type OAuthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// TrackResponse represents the Spotify track search response
type TrackResponse struct {
	Tracks struct {
		Items []TrackItem `json:"items"`
	} `json:"tracks"`
}

// TrackItem represents a track in the search results
type TrackItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	DurationMs int    `json:"duration_ms"`
	Album      struct {
		Name string `json:"name"`
	} `json:"album"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
}

// LyricsResponse represents the lyrics API response
type LyricsResponse struct {
	Lyrics LyricsData `json:"lyrics"`
}

// LyricsData contains the actual lyrics data
type LyricsData struct {
	SyncType      string       `json:"syncType"`
	Lines         []LegacyLine `json:"lines"`
	IsRtlLanguage bool         `json:"isRtlLanguage"`
	Language      string       `json:"language"`
}

// LegacyLine represents a lyrics line in legacy format
// Note: Syllables are strings, not structured objects
type LegacyLine struct {
	StartTimeMs string   `json:"startTimeMs"`
	DurationMs  string   `json:"durationMs"`
	Words       string   `json:"words"`
	Syllables   []string `json:"syllables"`
	EndTimeMs   string   `json:"endTimeMs"`
}
