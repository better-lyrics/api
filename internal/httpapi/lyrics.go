package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/bini"
	"lyrics-api-go/services/providers"
	"lyrics-api-go/services/proxy"
	"lyrics-api-go/stats"

	ttml "lyrics-api-go/services/providers/ttml"

	log "github.com/sirupsen/logrus"
)

func (s *Server) getLyrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")
	videoID := r.URL.Query().Get("videoId") + r.URL.Query().Get("v")

	if songName == "" && artistName == "" {
		http.Error(w, "Song name or artist name not provided", http.StatusUnprocessableEntity)
		return
	}

	cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
	query := strings.ToLower(strings.TrimSpace(songName)) + " " + strings.ToLower(strings.TrimSpace(artistName))

	cacheOnlyMode, _ := r.Context().Value(cacheOnlyModeKey).(bool)
	apiKeyRequired, _ := r.Context().Value(apiKeyRequiredForFreshKey).(bool)
	apiKeyInvalid, _ := r.Context().Value(apiKeyInvalidKey).(bool)

	if cached, foundKey, ok := s.getCachedLyricsWithDurationTolerance(ctx, songName, artistName, albumName, durationStr); ok {
		if cached.TTML == NoLyricsSentinel {
			stats.Get().RecordCacheHit()
			log.Infof("%s No-lyrics marker found for: %s", logcolors.LogCacheLyrics, query)
			respond(w, r).SetCacheStatus("HIT").Error(http.StatusNotFound, map[string]interface{}{
				"error": "No lyrics available for this track",
			})
			return
		}
		stats.Get().RecordCacheHit()
		if foundKey != cacheKey {
			log.Infof("%s Found cached TTML via fuzzy duration match: %s", logcolors.LogCacheLyrics, foundKey)
		} else {
			log.Infof("%s Found cached TTML", logcolors.LogCacheLyrics)
		}
		if videoID != "" {
			go s.addVideoID(context.Background(), foundKey, videoID)
		}
		respond(w, r).SetCacheStatus("HIT").JSON(map[string]interface{}{
			"ttml": cached.TTML,
		})
		return
	}

	if reason, _, found := s.getNegativeCacheWithDurationTolerance(ctx, songName, artistName, albumName, durationStr); found {
		stats.Get().RecordNegativeCacheHit()
		log.Infof("%s Returning cached 'no lyrics' response for: %s", logcolors.LogCacheNegative, query)
		respond(w, r).SetCacheStatus("NEGATIVE_HIT").Error(http.StatusNotFound, map[string]interface{}{
			"error": reason,
		})
		return
	}

	if apiKeyRequired {
		stats.Get().RecordCacheMiss()
		if apiKeyInvalid {
			log.Warnf("%s Invalid API key for uncached query: %s", logcolors.LogAPIKey, query)
			respond(w, r).SetCacheStatus("MISS").Error(http.StatusUnauthorized, map[string]interface{}{
				"error":   "Invalid API key",
				"message": "The provided API key is not valid",
			})
		} else {
			log.Warnf("%s API key required for uncached query: %s", logcolors.LogAPIKey, query)
			respond(w, r).SetCacheStatus("MISS").Error(http.StatusUnauthorized, map[string]interface{}{
				"error":   "API key required",
				"message": "Uncached queries require a valid API key via X-API-Key header",
			})
		}
		return
	}

	if cacheOnlyMode {
		stats.Get().RecordCacheMiss()
		stats.Get().RecordRateLimit("exceeded")
		log.Warnf("%s Cache-only mode but no cache found for: %s", logcolors.LogCacheLyrics, query)
		w.Header().Set("Retry-After", "60")
		respond(w, r).SetCacheStatus("MISS").Error(http.StatusTooManyRequests, map[string]interface{}{
			"error":   "Rate limit exceeded. This request requires cached data, but no cache is available for this query.",
			"message": "Please try again later or reduce your request rate.",
		})
		return
	}

	if s.cfg.FeatureFlags.CacheOnlyMode {
		stats.Get().RecordCacheMiss()
		log.Warnf("%s FF_CACHE_ONLY_MODE enabled, no cache for: %s", logcolors.LogCacheLyrics, query)
		respond(w, r).SetCacheStatus("MISS").Error(http.StatusServiceUnavailable, map[string]interface{}{
			"error": "Service running in cache-only mode. No cached lyrics available for this query.",
		})
		return
	}

	inFlight, loaded := s.inFlight.LoadOrStore(cacheKey, &inFlightRequest{})
	req := inFlight.(*inFlightRequest)

	if loaded {
		log.Infof("%s Waiting for in-flight request to complete", logcolors.LogCacheLyrics)
		req.wg.Wait()

		if req.err != nil {
			respond(w, r).SetCacheStatus("MISS").Error(http.StatusInternalServerError, map[string]interface{}{
				"error": req.err.Error(),
			})
			return
		}

		respond(w, r).SetCacheStatus("HIT").JSON(map[string]interface{}{
			"ttml":  req.result,
			"score": req.score,
		})
		return
	}

	req.wg.Add(1)
	defer func() {
		req.wg.Done()
		time.AfterFunc(1*time.Second, func() {
			s.inFlight.Delete(cacheKey)
		})
	}()

	var durationMs int
	if durationStr != "" {
		fmt.Sscanf(durationStr, "%d", &durationMs)
		durationMs = durationMs * 1000
	}

	ttmlString, trackDurationMs, score, trackMeta, err := ttml.FetchTTMLLyrics(songName, artistName, albumName, durationMs)

	req.err = err
	if err == nil {
		req.result = ttmlString
		req.score = score
	}

	if err != nil {
		log.Errorf("%s Error fetching TTML: %v", logcolors.LogLyrics, err)

		fallbackKeys := buildFallbackCacheKeys(songName, artistName, albumName, durationStr, cacheKey)
		for _, fallbackKey := range fallbackKeys {
			if cached, ok := s.getCachedLyrics(ctx, fallbackKey); ok {
				stats.Get().RecordStaleCacheHit()
				log.Warnf("%s Backend failed, serving stale cache from key: %s", logcolors.LogCacheLyrics, fallbackKey)
				respond(w, r).SetCacheStatus("STALE").JSON(map[string]interface{}{
					"ttml": cached.TTML,
				})
				return
			}
		}

		isPermanentError := shouldNegativeCache(err)
		if isPermanentError {
			releaseDate := ""
			hasTimeSyncedLyricsKnown := false
			if trackMeta != nil {
				releaseDate = trackMeta.ReleaseDate
				hasTimeSyncedLyricsKnown = trackMeta.HasTimeSyncedLyrics != nil
			}
			s.setNegativeCache(ctx, cacheKey, err.Error(), releaseDate, hasTimeSyncedLyricsKnown)
		}

		stats.Get().RecordCacheMiss()
		if isPermanentError {
			respond(w, r).SetCacheStatus("MISS").Error(http.StatusNotFound, map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			respond(w, r).SetCacheStatus("MISS").Error(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}
		return
	}

	if ttmlString == "" {
		stats.Get().RecordCacheMiss()
		log.Warnf("No TTML found for: %s", query)
		releaseDate := ""
		hasTimeSyncedLyricsKnown := false
		if trackMeta != nil {
			releaseDate = trackMeta.ReleaseDate
			hasTimeSyncedLyricsKnown = trackMeta.HasTimeSyncedLyrics != nil
		}
		s.setNegativeCache(ctx, cacheKey, "Lyrics not available for this track", releaseDate, hasTimeSyncedLyricsKnown)
		respond(w, r).SetCacheStatus("MISS").Error(http.StatusNotFound, map[string]interface{}{
			"error": "Lyrics not available for this track",
		})
		return
	}

	stats.Get().RecordCacheMiss()
	log.Infof("%s Caching TTML for: %s (trackDuration: %dms)", logcolors.LogCacheLyrics, query, trackDurationMs)
	language, isRTL := ttml.DetectLanguage(ttmlString)
	source := ""
	if trackMeta != nil {
		source = trackMeta.Source
	}
	s.setCachedLyrics(ctx, cacheKey, ttmlString, trackDurationMs, score, language, isRTL, source)

	if trackMeta != nil {
		go bini.Contribute(trackMeta.Name, trackMeta.ArtistName, trackMeta.ISRC, trackMeta.Source, trackMeta.RawAttributes, ttmlString)
	}

	if trackMeta != nil {
		go func() {
			bg := context.Background()
			meta := &store.SongMetadata{
				CacheKey:      cacheKey,
				AppleTrackID:  trackMeta.TrackID,
				ISRC:          trackMeta.ISRC,
				TrackName:     trackMeta.Name,
				ArtistName:    trackMeta.ArtistName,
				AlbumName:     trackMeta.AlbumName,
				DurationMs:    trackDurationMs,
				ReleaseDate:   trackMeta.ReleaseDate,
				RawAttributes: trackMeta.RawAttributes,
			}
			if videoID != "" {
				meta.VideoIDs = []string{videoID}
			}
			s.setSongMetadata(bg, meta)
			proxy.RevalidateAllForSong(trackMeta.Name, trackMeta.ArtistName, trackMeta.AlbumName, trackDurationMs/1000, s.videoIDsFunc(bg))
		}()
	} else if videoID != "" {
		go s.addVideoID(context.Background(), cacheKey, videoID)
	}

	respond(w, r).SetCacheStatus("MISS").JSON(map[string]interface{}{
		"ttml":  ttmlString,
		"score": score,
	})
}

func (s *Server) getLyricsWithProvider(providerName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
		artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
		albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
		durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")

		if songName == "" && artistName == "" {
			http.Error(w, "Song name or artist name not provided", http.StatusUnprocessableEntity)
			return
		}

		provider, err := providers.Get(providerName)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": fmt.Sprintf("Invalid provider: %s", providerName),
			})
			return
		}

		cacheKey := buildProviderCacheKey(provider.CacheKeyPrefix(), songName, artistName, albumName, durationStr)
		query := strings.ToLower(strings.TrimSpace(songName)) + " " + strings.ToLower(strings.TrimSpace(artistName))

		cacheOnlyMode, _ := r.Context().Value(cacheOnlyModeKey).(bool)
		apiKeyRequired, _ := r.Context().Value(apiKeyRequiredForFreshKey).(bool)
		apiKeyInvalid, _ := r.Context().Value(apiKeyInvalidKey).(bool)

		if cached, ok := s.getCachedLyrics(ctx, cacheKey); ok {
			if cached.TTML == NoLyricsSentinel {
				stats.Get().RecordCacheHit()
				log.Infof("%s [%s] No-lyrics marker found", logcolors.LogCacheLyrics, providerName)
				respond(w, r).SetProvider(providerName).SetCacheStatus("HIT").Error(http.StatusNotFound, map[string]interface{}{
					"error": "No lyrics available for this track",
				})
				return
			}
			stats.Get().RecordCacheHit()
			log.Infof("%s [%s] Found cached lyrics", logcolors.LogCacheLyrics, providerName)
			respond(w, r).SetProvider(providerName).SetCacheStatus("HIT").JSON(map[string]interface{}{
				"lyrics":   cached.TTML,
				"provider": providerName,
			})
			return
		}

		if reason, found := s.getNegativeCache(ctx, cacheKey); found {
			stats.Get().RecordNegativeCacheHit()
			log.Infof("%s [%s] Returning cached 'no lyrics' response", logcolors.LogCacheNegative, providerName)
			respond(w, r).SetProvider(providerName).SetCacheStatus("NEGATIVE_HIT").Error(http.StatusNotFound, map[string]interface{}{
				"error":    reason,
				"provider": providerName,
			})
			return
		}

		if apiKeyRequired {
			stats.Get().RecordCacheMiss()
			if apiKeyInvalid {
				log.Warnf("%s [%s] Invalid API key for uncached query: %s", logcolors.LogAPIKey, providerName, query)
				respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusUnauthorized, map[string]interface{}{
					"error":    "Invalid API key",
					"message":  "The provided API key is not valid",
					"provider": providerName,
				})
			} else {
				log.Warnf("%s [%s] API key required for uncached query: %s", logcolors.LogAPIKey, providerName, query)
				respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusUnauthorized, map[string]interface{}{
					"error":    "API key required",
					"message":  "Uncached queries require a valid API key via X-API-Key header",
					"provider": providerName,
				})
			}
			return
		}

		if cacheOnlyMode {
			stats.Get().RecordCacheMiss()
			stats.Get().RecordRateLimit("exceeded")
			log.Warnf("%s [%s] Cache-only mode but no cache found for: %s", logcolors.LogCacheLyrics, providerName, query)
			w.Header().Set("Retry-After", "60")
			respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusTooManyRequests, map[string]interface{}{
				"error":    "Rate limit exceeded. No cached data available.",
				"provider": providerName,
			})
			return
		}

		if s.cfg.FeatureFlags.CacheOnlyMode {
			stats.Get().RecordCacheMiss()
			log.Warnf("%s [%s] FF_CACHE_ONLY_MODE enabled, no cache for: %s", logcolors.LogCacheLyrics, providerName, query)
			respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusServiceUnavailable, map[string]interface{}{
				"error":    "Service running in cache-only mode. No cached lyrics available for this query.",
				"provider": providerName,
			})
			return
		}

		inFlight, loaded := s.inFlight.LoadOrStore(cacheKey, &inFlightRequest{})
		req := inFlight.(*inFlightRequest)

		if loaded {
			log.Infof("%s [%s] Waiting for in-flight request", logcolors.LogCacheLyrics, providerName)
			req.wg.Wait()

			if req.err != nil {
				respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusInternalServerError, map[string]interface{}{
					"error":    req.err.Error(),
					"provider": providerName,
				})
				return
			}

			respond(w, r).SetProvider(providerName).SetCacheStatus("HIT").JSON(map[string]interface{}{
				"lyrics":   req.result,
				"provider": providerName,
			})
			return
		}

		req.wg.Add(1)
		defer func() {
			req.wg.Done()
			time.AfterFunc(1*time.Second, func() {
				s.inFlight.Delete(cacheKey)
			})
		}()

		var durationMs int
		if durationStr != "" {
			fmt.Sscanf(durationStr, "%d", &durationMs)
			durationMs = durationMs * 1000
		}

		result, err := provider.FetchLyrics(context.Background(), songName, artistName, albumName, durationMs)

		req.err = err
		if err == nil && result != nil {
			req.result = result.RawLyrics
			req.score = result.Score
		}

		if err != nil {
			log.Errorf("%s [%s] Error fetching lyrics: %v", logcolors.LogLyrics, providerName, err)

			isPermanentError := shouldNegativeCache(err)
			if isPermanentError {
				s.setNegativeCache(ctx, cacheKey, err.Error(), "", false)
			}

			stats.Get().RecordCacheMiss()
			if isPermanentError {
				respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusNotFound, map[string]interface{}{
					"error":    err.Error(),
					"provider": providerName,
				})
			} else {
				respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusInternalServerError, map[string]interface{}{
					"error":    err.Error(),
					"provider": providerName,
				})
			}
			return
		}

		if result == nil || result.RawLyrics == "" {
			stats.Get().RecordCacheMiss()
			log.Warnf("[%s] No lyrics found for: %s", providerName, query)
			s.setNegativeCache(ctx, cacheKey, "Lyrics not available", "", false)
			respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusNotFound, map[string]interface{}{
				"error":    "Lyrics not available for this track",
				"provider": providerName,
			})
			return
		}

		stats.Get().RecordCacheMiss()
		log.Infof("%s [%s] Caching lyrics for: %s", logcolors.LogCacheLyrics, providerName, query)
		s.setCachedLyrics(ctx, cacheKey, result.RawLyrics, result.TrackDurationMs, result.Score, result.Language, result.IsRTL, result.Source)

		respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").JSON(map[string]interface{}{
			"lyrics":   result.RawLyrics,
			"provider": providerName,
		})
	}
}

// buildFallbackCacheKeys returns keys to try when the backend fails, most specific first,
// excluding the original key. Ported verbatim.
func buildFallbackCacheKeys(songName, artistName, albumName, durationStr, originalKey string) []string {
	var keys []string
	if albumName != "" {
		normalizedNoAlbum := buildNormalizedCacheKey(songName, artistName, "", durationStr)
		if normalizedNoAlbum != originalKey {
			keys = append(keys, normalizedNoAlbum)
		}
	}
	return keys
}
