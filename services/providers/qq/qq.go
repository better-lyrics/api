package qq

import (
	"context"
	"fmt"
	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/providers"

	log "github.com/sirupsen/logrus"
)

const (
	ProviderName = "qq"
	CachePrefix  = "qq_lyrics"
)

// QQProvider implements the providers.Provider interface for QQ Music lyrics
type QQProvider struct{}

// NewProvider creates a new QQ provider instance
func NewProvider() *QQProvider {
	return &QQProvider{}
}

// Name returns the provider identifier
func (p *QQProvider) Name() string {
	return ProviderName
}

// CacheKeyPrefix returns the cache key prefix for this provider
func (p *QQProvider) CacheKeyPrefix() string {
	return CachePrefix
}

// FetchLyrics fetches lyrics from QQ Music API
func (p *QQProvider) FetchLyrics(ctx context.Context, song, artist, album string, durationMs int) (*providers.LyricsResult, error) {
	conf := config.Get()

	if song == "" && artist == "" {
		return nil, providers.NewProviderError(ProviderName, "song name and artist name cannot both be empty", nil)
	}

	log.Infof("%s [QQ] Searching: %s - %s", logcolors.LogSearch, song, artist)

	songs, err := SearchSongs(song, artist, 10)
	if err != nil {
		return nil, providers.NewProviderError(ProviderName, "song search failed", err)
	}

	if len(songs) == 0 {
		return nil, providers.NewProviderError(ProviderName, fmt.Sprintf("no songs found for: %s - %s", song, artist), nil)
	}

	// Apply duration filter if duration is provided
	filteredSongs := songs
	if durationMs > 0 {
		deltaMs := conf.Configuration.DurationMatchDeltaMs
		filteredSongs = filterSongsByDuration(songs, durationMs, deltaMs)
		if len(filteredSongs) == 0 {
			return nil, providers.NewProviderError(ProviderName,
				fmt.Sprintf("no songs within %dms of duration %dms", deltaMs, durationMs), nil)
		}
		log.Infof("%s [QQ] %d/%d songs passed duration filter (delta: %dms)",
			logcolors.LogDurationFilter, len(filteredSongs), len(songs), deltaMs)
	}

	bestSong, songScore := SelectBestSong(filteredSongs, song, artist, durationMs)
	if bestSong == nil {
		return nil, providers.NewProviderError(ProviderName, "no suitable song match found", nil)
	}

	// Check minimum similarity threshold
	minScore := conf.Configuration.MinSimilarityScore
	if songScore < minScore {
		return nil, providers.NewProviderError(ProviderName,
			fmt.Sprintf("best match score %.2f below threshold %.2f for: %s - %s",
				songScore, minScore, song, artist), nil)
	}

	log.Infof("%s [QQ] Found song: %s - %s (score: %.2f, mid: %s)",
		logcolors.LogMatch, bestSong.Title, bestSong.SingerNames(), songScore, bestSong.MID)

	// Fetch and decrypt QRC lyrics
	qrcContent, err := FetchQRCLyrics(bestSong.MID)
	if err != nil {
		return nil, providers.NewProviderError(ProviderName, "failed to fetch QRC lyrics", err)
	}

	// Parse QRC content
	lines, metadata, parseErr := ParseQRC(qrcContent)
	if parseErr != nil {
		log.Warnf("%s [QQ] Failed to parse QRC: %v", logcolors.LogWarning, parseErr)
	}

	// Detect language
	language := DetectLanguage(metadata, qrcContent)

	log.Infof("%s [QQ] Fetched lyrics for: %s - %s (%d bytes, %d lines)",
		logcolors.LogSuccess, bestSong.Title, bestSong.SingerNames(), len(qrcContent), len(lines))

	result := &providers.LyricsResult{
		RawLyrics:       qrcContent,
		Lines:           lines,
		TrackDurationMs: bestSong.Interval * 1000,
		Score:           songScore,
		Provider:        ProviderName,
		Language:        normalizeLanguageCode(language),
		IsRTL:           providers.IsRTLLanguage(normalizeLanguageCode(language)),
	}

	return result, nil
}

// init registers the QQ provider with the global registry
func init() {
	providers.Register(NewProvider())
}
