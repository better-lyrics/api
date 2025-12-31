package ttml

import (
	"context"

	"lyrics-api-go/services/providers"
)

const (
	// ProviderName is the identifier for the TTML provider
	ProviderName = "ttml"

	// CachePrefix is the cache key prefix for TTML lyrics
	CachePrefix = "ttml_lyrics"
)

// TTMLProvider implements the providers.Provider interface for TTML lyrics
type TTMLProvider struct{}

// NewProvider creates a new TTML provider instance
func NewProvider() *TTMLProvider {
	return &TTMLProvider{}
}

// Name returns the provider identifier
func (p *TTMLProvider) Name() string {
	return ProviderName
}

// CacheKeyPrefix returns the cache key prefix for this provider
func (p *TTMLProvider) CacheKeyPrefix() string {
	return CachePrefix
}

// FetchLyrics fetches lyrics from TTML API
func (p *TTMLProvider) FetchLyrics(ctx context.Context, song, artist, album string, durationMs int) (*providers.LyricsResult, error) {
	// Use the existing FetchTTMLLyrics function
	rawTTML, trackDurationMs, score, err := FetchTTMLLyrics(song, artist, album, durationMs)
	if err != nil {
		return nil, providers.NewProviderError(ProviderName, "failed to fetch lyrics", err)
	}

	// Parse TTML to lines
	lines, language, parseErr := parseTTMLToLines(rawTTML)

	result := &providers.LyricsResult{
		RawLyrics:       rawTTML,
		TrackDurationMs: trackDurationMs,
		Score:           score,
		Provider:        ProviderName,
		Language:        language,
		IsRTL:           providers.IsRTLLanguage(language),
	}

	// Include parsed lines if parsing succeeded
	if parseErr == nil {
		result.Lines = lines
	}

	return result, nil
}

// init registers the TTML provider with the global registry
func init() {
	providers.Register(NewProvider())
}
