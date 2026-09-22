package httpapi

import (
	"context"
	"fmt"
	"strings"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"
	ttml "lyrics-api-go/services/providers/ttml"

	log "github.com/sirupsen/logrus"
)

// Cache key builders (ported verbatim: these strings are part of the frozen contract).

func buildNormalizedCacheKey(songName, artistName, albumName, durationStr string) string {
	song := strings.ToLower(strings.TrimSpace(songName))
	artist := strings.ToLower(strings.TrimSpace(artistName))
	album := strings.ToLower(strings.TrimSpace(albumName))

	query := song + " " + artist
	if album != "" {
		query += " " + album
	}
	if durationStr != "" {
		query += " " + durationStr + "s"
	}
	return fmt.Sprintf("ttml_lyrics:%s", query)
}

func buildLegacyCacheKey(songName, artistName, albumName, durationStr string) string {
	query := songName + " " + artistName + " " + albumName
	if durationStr != "" {
		query = query + " " + durationStr + "s"
	}
	return fmt.Sprintf("ttml_lyrics:%s", query)
}

func buildProviderCacheKey(prefix, song, artist, album, duration string) string {
	key := prefix + ":" + strings.ToLower(strings.TrimSpace(song)) + " " + strings.ToLower(strings.TrimSpace(artist))
	if album != "" {
		key += " [" + strings.ToLower(strings.TrimSpace(album)) + "]"
	}
	if duration != "" {
		key += " [" + duration + "s]"
	}
	return strings.TrimSpace(key)
}

// parseDurationSec returns the parsed seconds, or nil when the string is empty or
// non-numeric (matching the old fmt.Sscanf bail-out).
func parseDurationSec(durationStr string) *int {
	if durationStr == "" {
		return nil
	}
	var d int
	if _, err := fmt.Sscanf(durationStr, "%d", &d); err != nil {
		return nil
	}
	return &d
}

func (s *Server) durationDeltaSec() int {
	return s.cfg.Configuration.DurationMatchDeltaMs / 1000
}

func (s *Server) negativeTTL() store.NegativeTTL {
	return store.NegativeTTL{
		DefaultDays:          s.cfg.Configuration.NegativeCacheTTLInDays,
		NewSongThresholdDays: s.cfg.Configuration.NewSongThresholdDays,
	}
}

// Lyrics cache operations over Postgres.

func (s *Server) getCachedLyrics(ctx context.Context, cacheKey string) (store.CachedLyrics, bool) {
	l, ok, err := s.store.GetLyricsExact(ctx, cacheKey)
	if err != nil {
		log.Errorf("%s Error reading cache: %v", logcolors.LogCacheLyrics, err)
		return store.CachedLyrics{}, false
	}
	return l, ok
}

func (s *Server) getCachedLyricsWithDurationTolerance(ctx context.Context, songName, artistName, albumName, durationStr string) (store.CachedLyrics, string, bool) {
	exactKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
	if l, ok := s.getCachedLyrics(ctx, exactKey); ok {
		return l, exactKey, true
	}

	legacyKey := buildLegacyCacheKey(songName, artistName, albumName, durationStr)
	if legacyKey != exactKey {
		if l, ok := s.getCachedLyrics(ctx, legacyKey); ok {
			return l, legacyKey, true
		}
	}

	dsec := parseDurationSec(durationStr)
	if dsec == nil {
		return store.CachedLyrics{}, exactKey, false
	}

	baseKey := buildNormalizedCacheKey(songName, artistName, albumName, "")
	l, foundKey, ok, err := s.store.GetLyricsTolerant(ctx, store.Key{BaseKey: baseKey, DurationSec: dsec}, s.durationDeltaSec())
	if err != nil {
		log.Errorf("%s Error in tolerant cache read: %v", logcolors.LogCacheLyrics, err)
		return store.CachedLyrics{}, exactKey, false
	}
	if ok {
		return l, foundKey, true
	}
	return store.CachedLyrics{}, exactKey, false
}

func (s *Server) setCachedLyrics(ctx context.Context, cacheKey, lyrics string, trackDurationMs int, score float64, language string, isRTL bool, source string) {
	k := store.DeriveKey(cacheKey)
	l := store.CachedLyrics{
		TTML:            lyrics,
		TrackDurationMs: trackDurationMs,
		Score:           score,
		Language:        language,
		IsRTL:           isRTL,
		Format:          k.Provider,
		Source:          source,
	}
	if k.Provider == "ttml" && lyrics != store.NoLyricsSentinel {
		l.TimingType = ttml.TimingType(lyrics)
	}
	if err := s.store.SetLyrics(ctx, cacheKey, k, l); err != nil {
		log.Errorf("%s Error setting cache value: %v", logcolors.LogCacheLyrics, err)
	}
}

// Negative cache operations over Postgres.

func (s *Server) getNegativeCache(ctx context.Context, cacheKey string) (string, bool) {
	reason, ok, err := s.store.GetNegative(ctx, cacheKey)
	if err != nil {
		log.Errorf("%s Error reading negative cache: %v", logcolors.LogCacheNegative, err)
		return "", false
	}
	return reason, ok
}

func (s *Server) getNegativeCacheWithDurationTolerance(ctx context.Context, songName, artistName, albumName, durationStr string) (string, string, bool) {
	exactKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
	if reason, ok := s.getNegativeCache(ctx, exactKey); ok {
		return reason, exactKey, true
	}

	legacyKey := buildLegacyCacheKey(songName, artistName, albumName, durationStr)
	if legacyKey != exactKey {
		if reason, ok := s.getNegativeCache(ctx, legacyKey); ok {
			return reason, legacyKey, true
		}
	}

	dsec := parseDurationSec(durationStr)
	if dsec == nil {
		return "", exactKey, false
	}

	baseKey := buildNormalizedCacheKey(songName, artistName, albumName, "")
	reason, foundKey, ok, err := s.store.GetNegativeTolerant(ctx, store.Key{BaseKey: baseKey, DurationSec: dsec}, s.durationDeltaSec())
	if err != nil {
		log.Errorf("%s Error in tolerant negative read: %v", logcolors.LogCacheNegative, err)
		return "", exactKey, false
	}
	if ok {
		return reason, foundKey, true
	}
	return "", exactKey, false
}

func (s *Server) setNegativeCache(ctx context.Context, cacheKey, reason, releaseDate string, hasTimeSyncedLyricsKnown bool) {
	k := store.DeriveKey(cacheKey)
	e := store.NegativeEntry{
		Reason:             reason,
		ReleaseDate:        releaseDate,
		HasTimeSyncedKnown: hasTimeSyncedLyricsKnown,
	}
	if err := s.store.SetNegative(ctx, cacheKey, k, e, s.negativeTTL()); err != nil {
		log.Errorf("%s Error setting negative cache: %v", logcolors.LogCacheNegative, err)
		return
	}
	log.Infof("%s Cached 'no lyrics' for key: %s (reason: %s)", logcolors.LogCacheNegative, cacheKey, reason)
}

func (s *Server) deleteNegativeCache(ctx context.Context, cacheKey string) {
	if err := s.store.DeleteNegative(ctx, cacheKey); err != nil {
		log.Errorf("%s Error deleting negative cache: %v", logcolors.LogCacheNegative, err)
		return
	}
	log.Infof("%s Deleted negative cache for key: %s", logcolors.LogCacheNegative, cacheKey)
}

// shouldNegativeCache reports whether an error is a permanent "no lyrics" result
// worth caching (vs a transient failure). Ported verbatim.
func shouldNegativeCache(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	permanentErrors := []string{
		"no track found",
		"no tracks found",
		"no tracks within",
		"no matching tracks found",
		"No related resources",
		"no lyrics data found",
		"TTML content is empty",
		"no songs found",
		"lyrics content is empty",
	}
	for _, pe := range permanentErrors {
		if strings.Contains(errStr, pe) {
			return true
		}
	}
	return false
}
