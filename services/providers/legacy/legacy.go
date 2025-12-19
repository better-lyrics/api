package legacy

import (
	"context"
	"encoding/json"
	"fmt"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/providers"

	log "github.com/sirupsen/logrus"
)

const (
	// ProviderName is the identifier for the legacy provider
	ProviderName = "legacy"

	// CachePrefix is the cache key prefix for legacy lyrics
	CachePrefix = "legacy_lyrics"
)

// LegacyProvider implements the providers.Provider interface using the Spotify-based legacy method
type LegacyProvider struct{}

// NewProvider creates a new legacy provider instance
func NewProvider() *LegacyProvider {
	return &LegacyProvider{}
}

// Name returns the provider identifier
func (p *LegacyProvider) Name() string {
	return ProviderName
}

// CacheKeyPrefix returns the cache key prefix for this provider
func (p *LegacyProvider) CacheKeyPrefix() string {
	return CachePrefix
}

// FetchLyrics fetches lyrics using the legacy Spotify-based method
func (p *LegacyProvider) FetchLyrics(ctx context.Context, song, artist, album string, durationMs int) (*providers.LyricsResult, error) {
	if song == "" && artist == "" {
		return nil, providers.NewProviderError(ProviderName, "song name and artist name cannot both be empty", nil)
	}

	query := song
	if artist != "" {
		query = song + " " + artist
	}

	log.Infof("%s [Legacy] Searching: %s", logcolors.LogSearch, query)

	// Search for track
	track, err := SearchTrack(query)
	if err != nil {
		return nil, providers.NewProviderError(ProviderName, "track search failed", err)
	}

	if track == nil {
		return nil, providers.NewProviderError(ProviderName, fmt.Sprintf("no track found for: %s", query), nil)
	}

	log.Infof("%s [Legacy] Found track: %s (ID: %s)", logcolors.LogMatch, track.Name, track.ID)

	// Fetch lyrics
	lyricsData, err := FetchLyrics(track.ID)
	if err != nil {
		return nil, providers.NewProviderError(ProviderName, "failed to fetch lyrics", err)
	}

	if lyricsData == nil || len(lyricsData.Lines) == 0 {
		return nil, providers.NewProviderError(ProviderName, "no lyrics available for track", nil)
	}

	// Convert legacy lines to provider lines
	lines := make([]providers.Line, len(lyricsData.Lines))
	for i, ll := range lyricsData.Lines {
		// Convert []string syllables to []Syllable
		syllables := make([]providers.Syllable, len(ll.Syllables))
		for j, s := range ll.Syllables {
			syllables[j] = providers.Syllable{
				Text: s,
			}
		}

		lines[i] = providers.Line{
			StartTimeMs: ll.StartTimeMs,
			DurationMs:  ll.DurationMs,
			Words:       ll.Words,
			Syllables:   syllables,
			EndTimeMs:   ll.EndTimeMs,
		}
	}

	// Create raw lyrics JSON for caching
	rawLyrics, _ := json.Marshal(map[string]interface{}{
		"syncType": lyricsData.SyncType,
		"lines":    lyricsData.Lines,
		"language": lyricsData.Language,
		"isRTL":    lyricsData.IsRtlLanguage,
	})

	log.Infof("%s [Legacy] Fetched lyrics for: %s (%d lines)",
		logcolors.LogSuccess, track.Name, len(lines))

	result := &providers.LyricsResult{
		RawLyrics:       string(rawLyrics),
		Lines:           lines,
		TrackDurationMs: track.DurationMs,
		Score:           1.0, // Legacy doesn't have a score, assume perfect match
		Provider:        ProviderName,
		Language:        lyricsData.Language,
		IsRTL:           lyricsData.IsRtlLanguage,
	}

	return result, nil
}

// init registers the legacy provider with the global registry
func init() {
	providers.Register(NewProvider())
}
