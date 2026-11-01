package httpapi

import (
	"context"
	"fmt"
	"strings"
	"time"

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

func buildProviderSongArtistKey(prefix, song, artist string) string {
	return prefix + ":" + strings.ToLower(strings.TrimSpace(song)) + " " + strings.ToLower(strings.TrimSpace(artist))
}

func buildProviderCacheKey(prefix, song, artist, album, duration string) string {
	key := buildProviderSongArtistKey(prefix, song, artist)
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

	baseKey := buildNormalizedCacheKey(songName, artistName, albumName, "")
	if l, foundKey, ok := s.getCachedLyricsNearDuration(ctx, baseKey, durationStr); ok {
		return l, foundKey, true
	}
	return store.CachedLyrics{}, exactKey, false
}

func (s *Server) getProviderCachedLyrics(ctx context.Context, prefix, songName, artistName, albumName, durationStr string) (store.CachedLyrics, string, bool) {
	exactKey := buildProviderCacheKey(prefix, songName, artistName, albumName, durationStr)
	if l, ok := s.getCachedLyrics(ctx, exactKey); ok {
		return l, exactKey, true
	}
	if l, foundKey, ok := s.getCachedLyricsNearDuration(ctx, store.DeriveKey(exactKey).BaseKey, durationStr); ok {
		return l, foundKey, true
	}
	return store.CachedLyrics{}, exactKey, false
}

func (s *Server) getCachedLyricsNearDuration(ctx context.Context, baseKey, durationStr string) (store.CachedLyrics, string, bool) {
	dsec := parseDurationSec(durationStr)
	if dsec == nil {
		return store.CachedLyrics{}, "", false
	}
	l, foundKey, ok, err := s.store.GetLyricsTolerant(ctx, store.Key{BaseKey: baseKey, DurationSec: dsec}, s.durationDeltaSec())
	if err != nil {
		log.Errorf("%s Error in tolerant cache read: %v", logcolors.LogCacheLyrics, err)
		return store.CachedLyrics{}, "", false
	}
	return l, foundKey, ok
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

	baseKey := buildNormalizedCacheKey(songName, artistName, albumName, "")
	if reason, foundKey, ok := s.getNegativeCacheNearDuration(ctx, baseKey, durationStr); ok {
		return reason, foundKey, true
	}
	return "", exactKey, false
}

func (s *Server) getProviderNegativeCache(ctx context.Context, prefix, songName, artistName, albumName, durationStr string) (string, bool) {
	exactKey := buildProviderCacheKey(prefix, songName, artistName, albumName, durationStr)
	if reason, ok := s.getNegativeCache(ctx, exactKey); ok {
		return reason, true
	}
	reason, _, ok := s.getNegativeCacheNearDuration(ctx, store.DeriveKey(exactKey).BaseKey, durationStr)
	return reason, ok
}

func (s *Server) getNegativeCacheNearDuration(ctx context.Context, baseKey, durationStr string) (string, string, bool) {
	dsec := parseDurationSec(durationStr)
	if dsec == nil {
		return "", "", false
	}
	reason, foundKey, ok, err := s.store.GetNegativeTolerant(ctx, store.Key{BaseKey: baseKey, DurationSec: dsec}, s.durationDeltaSec())
	if err != nil {
		log.Errorf("%s Error in tolerant negative read: %v", logcolors.LogCacheNegative, err)
		return "", "", false
	}
	return reason, foundKey, ok
}

func (s *Server) setNegativeCache(ctx context.Context, cacheKey, reason, releaseDate string, hasTimeSyncedLyricsKnown bool) {
	s.writeNegativeCache(ctx, cacheKey, store.NegativeEntry{
		Reason:             reason,
		ReleaseDate:        releaseDate,
		HasTimeSyncedKnown: hasTimeSyncedLyricsKnown,
	})
}

// setTransientNegativeCache stores a short-lived miss, so a search that keeps coming back empty is not retried upstream on every request.
func (s *Server) setTransientNegativeCache(ctx context.Context, cacheKey, reason string) {
	s.writeNegativeCache(ctx, cacheKey, store.NegativeEntry{Reason: reason, TTL: transientEmptySearchTTL})
}

func (s *Server) writeNegativeCache(ctx context.Context, cacheKey string, e store.NegativeEntry) {
	if err := s.store.SetNegative(ctx, cacheKey, store.DeriveKey(cacheKey), e, s.negativeTTL()); err != nil {
		log.Errorf("%s Error setting negative cache: %v", logcolors.LogCacheNegative, err)
		return
	}
	log.Infof("%s Cached 'no lyrics' for key: %s (reason: %s)", logcolors.LogCacheNegative, cacheKey, e.Reason)
}

func (s *Server) deleteNegativeCache(ctx context.Context, cacheKey string) {
	if err := s.store.DeleteNegative(ctx, cacheKey); err != nil {
		log.Errorf("%s Error deleting negative cache: %v", logcolors.LogCacheNegative, err)
		return
	}
	log.Infof("%s Deleted negative cache for key: %s", logcolors.LogCacheNegative, cacheKey)
}

const (
	transientEmptySearchTTL = 6 * time.Hour
	// songLyricsLookupTimeout bounds the lookup so a missing or invalid lyrics_song_artist_idx falls back to a normal negative instead of a table scan.
	songLyricsLookupTimeout = 500 * time.Millisecond
)

// isEmptySearchError reports whether a provider search returned no songs at all. The search uses only song and artist, so album and duration play no part.
func isEmptySearchError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no songs found for: ")
}

// isTransientEmptySearch reports whether an empty provider search contradicts lyrics already cached for the same song and artist.
func (s *Server) isTransientEmptySearch(ctx context.Context, err error, cacheKey, prefix, songName, artistName string) bool {
	if !isEmptySearchError(err) {
		return false
	}
	lookupCtx, cancel := context.WithTimeout(ctx, songLyricsLookupTimeout)
	defer cancel()
	ok, qerr := s.store.HasSongLyrics(lookupCtx, store.DeriveKey(cacheKey).Provider, buildProviderSongArtistKey(prefix, songName, artistName))
	if qerr != nil {
		log.Errorf("%s Error checking cached lyrics for empty search: %v", logcolors.LogCacheNegative, qerr)
		return false
	}
	return ok
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
		"no songs within",
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
