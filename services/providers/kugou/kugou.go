package kugou

import (
	"context"
	"fmt"
	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/providers"

	log "github.com/sirupsen/logrus"
)

const (
	// ProviderName is the identifier for the Kugou provider
	ProviderName = "kugou"

	// CachePrefix is the cache key prefix for Kugou lyrics
	CachePrefix = "kugou_lyrics"
)

// KugouProvider implements the providers.Provider interface for Kugou lyrics
type KugouProvider struct{}

// NewProvider creates a new Kugou provider instance
func NewProvider() *KugouProvider {
	return &KugouProvider{}
}

// Name returns the provider identifier
func (p *KugouProvider) Name() string {
	return ProviderName
}

// CacheKeyPrefix returns the cache key prefix for this provider
func (p *KugouProvider) CacheKeyPrefix() string {
	return CachePrefix
}

// FetchLyrics fetches lyrics from Kugou API
func (p *KugouProvider) FetchLyrics(ctx context.Context, song, artist, album string, durationMs int) (*providers.LyricsResult, error) {
	conf := config.Get()

	if song == "" && artist == "" {
		return nil, providers.NewProviderError(ProviderName, "song name and artist name cannot both be empty", nil)
	}

	log.Infof("%s [Kugou] Searching: %s - %s", logcolors.LogSearch, song, artist)

	// First, search for songs to get the hash (required for lyrics search)
	songs, err := SearchSongs(song, artist, 10)
	if err != nil {
		return nil, providers.NewProviderError(ProviderName, "song search failed", err)
	}

	if len(songs) == 0 {
		return nil, providers.NewProviderError(ProviderName, fmt.Sprintf("no songs found for: %s - %s", song, artist), nil)
	}

	// Apply duration filter if duration is provided (same logic as TTML)
	filteredSongs := songs
	if durationMs > 0 {
		deltaMs := conf.Configuration.DurationMatchDeltaMs
		filteredSongs = filterSongsByDuration(songs, durationMs, deltaMs)
		if len(filteredSongs) == 0 {
			return nil, providers.NewProviderError(ProviderName,
				fmt.Sprintf("no songs within %dms of duration %dms", deltaMs, durationMs), nil)
		}
		log.Infof("%s [Kugou] %d/%d songs passed duration filter (delta: %dms)",
			logcolors.LogDurationFilter, len(filteredSongs), len(songs), deltaMs)
	}

	// Select the best matching song to get its hash
	bestSong, songScore := SelectBestSong(filteredSongs, song, artist, durationMs)
	if bestSong == nil {
		return nil, providers.NewProviderError(ProviderName, "no suitable song match found", nil)
	}

	// Check minimum similarity threshold (same as TTML)
	minScore := conf.Configuration.MinSimilarityScore
	if songScore < minScore {
		return nil, providers.NewProviderError(ProviderName,
			fmt.Sprintf("best match score %.2f below threshold %.2f for: %s - %s",
				songScore, minScore, song, artist), nil)
	}

	hashPreview := bestSong.Hash
	if len(hashPreview) > 16 {
		hashPreview = hashPreview[:16]
	}
	log.Infof("%s [Kugou] Found song: %s - %s (score: %.2f, hash: %s...)",
		logcolors.LogMatch, bestSong.SongName, bestSong.SingerName, songScore, hashPreview)

	// Search for lyrics using the song hash
	candidates, err := SearchLyrics(song, artist, durationMs, bestSong.Hash)
	if err != nil {
		return nil, providers.NewProviderError(ProviderName, "lyrics search failed", err)
	}

	if len(candidates) == 0 {
		return nil, providers.NewProviderError(ProviderName, fmt.Sprintf("no lyrics found for: %s - %s", song, artist), nil)
	}

	// Select the best candidate
	best, matchScore := SelectBestCandidate(candidates, song, artist, durationMs)
	if best == nil {
		return nil, providers.NewProviderError(ProviderName, "no suitable lyrics candidate found", nil)
	}

	log.Infof("%s [Kugou] Best lyrics match: %s - %s (score: %.2f, type: %d)",
		logcolors.LogMatch, best.Song, best.Singer, matchScore, best.KRCType)

	// Download the lyrics
	lrcContent, err := DownloadLyrics(best.ID, best.AccessKey)
	if err != nil {
		return nil, providers.NewProviderError(ProviderName, "failed to download lyrics", err)
	}

	// Normalize lyrics (remove credit lines, handle pure music placeholder)
	lrcContent = NormalizeLyrics(lrcContent)

	// Parse LRC content and extract metadata
	lines, metadata, parseErr := ParseLRC(lrcContent)
	if parseErr != nil {
		log.Warnf("%s [Kugou] Failed to parse LRC: %v", logcolors.LogWarning, parseErr)
	}

	// Strip metadata from raw LRC content for clean output
	cleanLRC := StripLRCMetadata(lrcContent)

	// Detect language
	language := best.Language
	if language == "" {
		language = DetectLanguage(metadata, lrcContent)
	}

	log.Infof("%s [Kugou] Fetched lyrics for: %s - %s (%d bytes, %d lines)",
		logcolors.LogSuccess, best.Song, best.Singer, len(cleanLRC), len(lines))

	result := &providers.LyricsResult{
		RawLyrics:       cleanLRC,
		Lines:           lines,
		TrackDurationMs: best.Duration,
		Score:           matchScore,
		Provider:        ProviderName,
		Language:        normalizeLanguageCode(language),
		IsRTL:           providers.IsRTLLanguage(normalizeLanguageCode(language)),
	}

	return result, nil
}

// init registers the Kugou provider with the global registry
func init() {
	providers.Register(NewProvider())
}
