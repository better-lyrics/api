package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"lyrics-api-go/cache"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/bini"
	"lyrics-api-go/services/notifier"
	"lyrics-api-go/services/providers"
	"lyrics-api-go/services/proxy"
	"lyrics-api-go/stats"
	"net/http"
	"strconv"
	"strings"
	"time"

	// Import providers to trigger their init() registration
	_ "lyrics-api-go/services/providers/kugou"
	_ "lyrics-api-go/services/providers/legacy"
	_ "lyrics-api-go/services/providers/qq"
	ttml "lyrics-api-go/services/providers/ttml"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

func getLyrics(w http.ResponseWriter, r *http.Request) {
	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")
	videoID := r.URL.Query().Get("videoId") + r.URL.Query().Get("v")

	if songName == "" && artistName == "" {
		http.Error(w, "Song name or artist name not provided", http.StatusUnprocessableEntity)
		return
	}

	// Use normalized cache key for consistent cache hits regardless of input casing/whitespace
	cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)

	// For logging, use a clean query string
	query := strings.ToLower(strings.TrimSpace(songName)) + " " + strings.ToLower(strings.TrimSpace(artistName))

	// Check if we're in cache-only mode (rate limit tier 2)
	cacheOnlyMode, _ := r.Context().Value(cacheOnlyModeKey).(bool)

	// Check if API key was required but not provided (cache-first mode)
	apiKeyRequired, _ := r.Context().Value(apiKeyRequiredForFreshKey).(bool)
	apiKeyInvalid, _ := r.Context().Value(apiKeyInvalidKey).(bool)

	// Check cache first with fuzzy duration matching (handles normalized + legacy keys)
	// This allows cache hits when duration differs by up to DURATION_MATCH_DELTA_MS (default 2s)
	if cached, foundKey, ok := getCachedLyricsWithDurationTolerance(songName, artistName, albumName, durationStr); ok {
		// Check for no-lyrics sentinel — return 404 as if no lyrics exist
		if cached.TTML == NoLyricsSentinel {
			stats.Get().RecordCacheHit()
			log.Infof("%s No-lyrics marker found for: %s", logcolors.LogCacheLyrics, query)
			Respond(w, r).SetCacheStatus("HIT").Error(http.StatusNotFound, map[string]interface{}{
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
		// Associate videoId on cache hits too
		if videoID != "" {
			go addVideoID(foundKey, videoID)
		}
		Respond(w, r).SetCacheStatus("HIT").JSON(map[string]interface{}{
			"ttml": cached.TTML,
		})
		return
	}

	// Check negative cache with fuzzy duration matching
	if reason, _, found := getNegativeCacheWithDurationTolerance(songName, artistName, albumName, durationStr); found {
		stats.Get().RecordNegativeCacheHit()
		log.Infof("%s Returning cached 'no lyrics' response for: %s", logcolors.LogCacheNegative, query)
		Respond(w, r).SetCacheStatus("NEGATIVE_HIT").Error(http.StatusNotFound, map[string]interface{}{
			"error": reason,
		})
		return
	}

	// If API key is required for fresh fetch but not provided/invalid, return 401
	// This allows cache hits to be served without API key
	if apiKeyRequired {
		stats.Get().RecordCacheMiss()
		if apiKeyInvalid {
			log.Warnf("%s Invalid API key for uncached query: %s", logcolors.LogAPIKey, query)
			Respond(w, r).SetCacheStatus("MISS").Error(http.StatusUnauthorized, map[string]interface{}{
				"error":   "Invalid API key",
				"message": "The provided API key is not valid",
			})
		} else {
			log.Warnf("%s API key required for uncached query: %s", logcolors.LogAPIKey, query)
			Respond(w, r).SetCacheStatus("MISS").Error(http.StatusUnauthorized, map[string]interface{}{
				"error":   "API key required",
				"message": "Uncached queries require a valid API key via X-API-Key header",
			})
		}
		return
	}

	// If in cache-only mode (rate limit tier 2) and no cache found, return 429
	if cacheOnlyMode {
		stats.Get().RecordCacheMiss()
		stats.Get().RecordRateLimit("exceeded")
		log.Warnf("%s Cache-only mode but no cache found for: %s", logcolors.LogCacheLyrics, query)
		w.Header().Set("Retry-After", "60")
		Respond(w, r).SetCacheStatus("MISS").Error(http.StatusTooManyRequests, map[string]interface{}{
			"error":   "Rate limit exceeded. This request requires cached data, but no cache is available for this query.",
			"message": "Please try again later or reduce your request rate.",
		})
		return
	}

	// If FF_CACHE_ONLY_MODE is enabled and no cache found, return 503
	if conf.FeatureFlags.CacheOnlyMode {
		stats.Get().RecordCacheMiss()
		log.Warnf("%s FF_CACHE_ONLY_MODE enabled, no cache for: %s", logcolors.LogCacheLyrics, query)
		Respond(w, r).SetCacheStatus("MISS").Error(http.StatusServiceUnavailable, map[string]interface{}{
			"error": "Service running in cache-only mode. No cached lyrics available for this query.",
		})
		return
	}

	inFlight, loaded := inFlightReqs.LoadOrStore(cacheKey, &InFlightRequest{})
	req := inFlight.(*InFlightRequest)

	if loaded {
		log.Infof("%s Waiting for in-flight request to complete", logcolors.LogCacheLyrics)
		req.wg.Wait()

		if req.err != nil {
			Respond(w, r).SetCacheStatus("MISS").Error(http.StatusInternalServerError, map[string]interface{}{
				"error": req.err.Error(),
			})
			return
		}

		Respond(w, r).SetCacheStatus("HIT").JSON(map[string]interface{}{
			"ttml":  req.result,
			"score": req.score,
		})
		return
	}

	req.wg.Add(1)
	defer func() {
		req.wg.Done()
		time.AfterFunc(1*time.Second, func() {
			inFlightReqs.Delete(cacheKey)
		})
	}()

	// Parse duration from seconds to milliseconds
	var durationMs int
	if durationStr != "" {
		fmt.Sscanf(durationStr, "%d", &durationMs)
		durationMs = durationMs * 1000 // Convert seconds to milliseconds
	}

	ttmlString, trackDurationMs, score, trackMeta, err := ttml.FetchTTMLLyrics(songName, artistName, albumName, durationMs)

	req.err = err
	if err == nil {
		req.result = ttmlString
		req.score = score
	}

	if err != nil {
		log.Errorf("%s Error fetching TTML: %v", logcolors.LogLyrics, err)

		// Try fallback cache keys before returning error
		fallbackKeys := buildFallbackCacheKeys(songName, artistName, albumName, durationStr, cacheKey)
		for _, fallbackKey := range fallbackKeys {
			if cached, ok := getCachedLyrics(fallbackKey); ok {
				stats.Get().RecordStaleCacheHit()
				log.Warnf("%s Backend failed, serving stale cache from key: %s", logcolors.LogCacheLyrics, fallbackKey)
				Respond(w, r).SetCacheStatus("STALE").JSON(map[string]interface{}{
					"ttml": cached.TTML,
				})
				return
			}
		}

		// Cache permanent "no lyrics" errors to avoid repeated API calls
		isPermanentError := shouldNegativeCache(err)
		if isPermanentError {
			releaseDate := ""
			hasTimeSyncedLyricsKnown := false
			if trackMeta != nil {
				releaseDate = trackMeta.ReleaseDate
				hasTimeSyncedLyricsKnown = trackMeta.HasTimeSyncedLyrics != nil
			}
			setNegativeCache(cacheKey, err.Error(), releaseDate, hasTimeSyncedLyricsKnown)
		}

		// No fallback found (or skipped due to duration), return the error
		stats.Get().RecordCacheMiss()
		// Return 404 for permanent "not found" errors, 500 for transient errors
		if isPermanentError {
			Respond(w, r).SetCacheStatus("MISS").Error(http.StatusNotFound, map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			Respond(w, r).SetCacheStatus("MISS").Error(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}
		return
	}

	if ttmlString == "" {
		stats.Get().RecordCacheMiss()
		log.Warnf("No TTML found for: %s", query)
		// Cache this negative result to avoid repeated API calls
		releaseDate := ""
		hasTimeSyncedLyricsKnown := false
		if trackMeta != nil {
			releaseDate = trackMeta.ReleaseDate
			hasTimeSyncedLyricsKnown = trackMeta.HasTimeSyncedLyrics != nil
		}
		setNegativeCache(cacheKey, "Lyrics not available for this track", releaseDate, hasTimeSyncedLyricsKnown)
		Respond(w, r).SetCacheStatus("MISS").Error(http.StatusNotFound, map[string]interface{}{
			"error": "Lyrics not available for this track",
		})
		return
	}

	stats.Get().RecordCacheMiss()
	log.Infof("%s Caching TTML for: %s (trackDuration: %dms)", logcolors.LogCacheLyrics, query, trackDurationMs)
	language, isRTL := ttml.DetectLanguage(ttmlString)
	setCachedLyrics(cacheKey, ttmlString, trackDurationMs, score, language, isRTL)

	go bini.PostLyrics(trackMeta.Name, trackMeta.ArtistName, trackMeta.AlbumName, trackDurationMs, ttmlString, trackMeta.ISRC)

	// Store metadata and videoId first, then trigger proxy revalidation (which queries metadata).
	// All writes happen in the same goroutine before revalidation to avoid race conditions.
	if trackMeta != nil {
		go func() {
			meta := &SongMetadata{
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
			setSongMetadata(meta)
			proxy.RevalidateAllForSong(trackMeta.Name, trackMeta.ArtistName, trackMeta.AlbumName, trackDurationMs/1000, getAllVideoIDsForSong)
		}()
	} else if videoID != "" {
		go addVideoID(cacheKey, videoID)
	}

	Respond(w, r).SetCacheStatus("MISS").JSON(map[string]interface{}{
		"ttml":  ttmlString,
		"score": score,
	})
}

// getLyricsWithProvider returns a handler for a specific provider
func getLyricsWithProvider(providerName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
		artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
		albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
		durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")

		if songName == "" && artistName == "" {
			http.Error(w, "Song name or artist name not provided", http.StatusUnprocessableEntity)
			return
		}

		// Get the provider
		provider, err := providers.Get(providerName)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": fmt.Sprintf("Invalid provider: %s", providerName),
			})
			return
		}

		// Build cache key with provider prefix
		cacheKey := buildProviderCacheKey(provider.CacheKeyPrefix(), songName, artistName, albumName, durationStr)
		query := strings.ToLower(strings.TrimSpace(songName)) + " " + strings.ToLower(strings.TrimSpace(artistName))

		// Check rate limit context
		cacheOnlyMode, _ := r.Context().Value(cacheOnlyModeKey).(bool)
		apiKeyRequired, _ := r.Context().Value(apiKeyRequiredForFreshKey).(bool)
		apiKeyInvalid, _ := r.Context().Value(apiKeyInvalidKey).(bool)

		// Check cache first
		if cached, ok := getCachedLyrics(cacheKey); ok {
			// Check for no-lyrics sentinel — return 404 as if no lyrics exist
			if cached.TTML == NoLyricsSentinel {
				stats.Get().RecordCacheHit()
				log.Infof("%s [%s] No-lyrics marker found", logcolors.LogCacheLyrics, providerName)
				Respond(w, r).SetProvider(providerName).SetCacheStatus("HIT").Error(http.StatusNotFound, map[string]interface{}{
					"error": "No lyrics available for this track",
				})
				return
			}
			stats.Get().RecordCacheHit()
			log.Infof("%s [%s] Found cached lyrics", logcolors.LogCacheLyrics, providerName)
			Respond(w, r).SetProvider(providerName).SetCacheStatus("HIT").JSON(map[string]interface{}{
				"lyrics":   cached.TTML,
				"provider": providerName,
			})
			return
		}

		// Check negative cache (uses same key format as positive cache, getNegativeCache adds "no_lyrics:" prefix)
		if reason, found := getNegativeCache(cacheKey); found {
			stats.Get().RecordNegativeCacheHit()
			log.Infof("%s [%s] Returning cached 'no lyrics' response", logcolors.LogCacheNegative, providerName)
			Respond(w, r).SetProvider(providerName).SetCacheStatus("NEGATIVE_HIT").Error(http.StatusNotFound, map[string]interface{}{
				"error":    reason,
				"provider": providerName,
			})
			return
		}

		// If API key is required for fresh fetch but not provided/invalid, return 401
		if apiKeyRequired {
			stats.Get().RecordCacheMiss()
			if apiKeyInvalid {
				log.Warnf("%s [%s] Invalid API key for uncached query: %s", logcolors.LogAPIKey, providerName, query)
				Respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusUnauthorized, map[string]interface{}{
					"error":    "Invalid API key",
					"message":  "The provided API key is not valid",
					"provider": providerName,
				})
			} else {
				log.Warnf("%s [%s] API key required for uncached query: %s", logcolors.LogAPIKey, providerName, query)
				Respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusUnauthorized, map[string]interface{}{
					"error":    "API key required",
					"message":  "Uncached queries require a valid API key via X-API-Key header",
					"provider": providerName,
				})
			}
			return
		}

		// If in cache-only mode (rate limit tier 2) and no cache found, return 429
		if cacheOnlyMode {
			stats.Get().RecordCacheMiss()
			stats.Get().RecordRateLimit("exceeded")
			log.Warnf("%s [%s] Cache-only mode but no cache found for: %s", logcolors.LogCacheLyrics, providerName, query)
			w.Header().Set("Retry-After", "60")
			Respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusTooManyRequests, map[string]interface{}{
				"error":    "Rate limit exceeded. No cached data available.",
				"provider": providerName,
			})
			return
		}

		// If FF_CACHE_ONLY_MODE is enabled and no cache found, return 503
		if conf.FeatureFlags.CacheOnlyMode {
			stats.Get().RecordCacheMiss()
			log.Warnf("%s [%s] FF_CACHE_ONLY_MODE enabled, no cache for: %s", logcolors.LogCacheLyrics, providerName, query)
			Respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusServiceUnavailable, map[string]interface{}{
				"error":    "Service running in cache-only mode. No cached lyrics available for this query.",
				"provider": providerName,
			})
			return
		}

		// In-flight request deduplication
		inFlight, loaded := inFlightReqs.LoadOrStore(cacheKey, &InFlightRequest{})
		req := inFlight.(*InFlightRequest)

		if loaded {
			log.Infof("%s [%s] Waiting for in-flight request", logcolors.LogCacheLyrics, providerName)
			req.wg.Wait()

			if req.err != nil {
				Respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusInternalServerError, map[string]interface{}{
					"error":    req.err.Error(),
					"provider": providerName,
				})
				return
			}

			Respond(w, r).SetProvider(providerName).SetCacheStatus("HIT").JSON(map[string]interface{}{
				"lyrics":   req.result,
				"provider": providerName,
			})
			return
		}

		req.wg.Add(1)
		defer func() {
			req.wg.Done()
			time.AfterFunc(1*time.Second, func() {
				inFlightReqs.Delete(cacheKey)
			})
		}()

		// Parse duration
		var durationMs int
		if durationStr != "" {
			fmt.Sscanf(durationStr, "%d", &durationMs)
			// Duration comes in as seconds, convert to milliseconds
			durationMs = durationMs * 1000
		}

		// Fetch lyrics from provider
		ctx := context.Background()
		result, err := provider.FetchLyrics(ctx, songName, artistName, albumName, durationMs)

		req.err = err
		if err == nil && result != nil {
			req.result = result.RawLyrics
			req.score = result.Score
			req.language = result.Language
			req.isRTL = result.IsRTL
		}

		if err != nil {
			log.Errorf("%s [%s] Error fetching lyrics: %v", logcolors.LogLyrics, providerName, err)

			// Cache negative result
			isPermanentError := shouldNegativeCache(err)
			if isPermanentError {
				setNegativeCache(cacheKey, err.Error(), "", false)
			}

			stats.Get().RecordCacheMiss()
			// Return 404 for permanent "not found" errors, 500 for transient errors
			if isPermanentError {
				Respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusNotFound, map[string]interface{}{
					"error":    err.Error(),
					"provider": providerName,
				})
			} else {
				Respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusInternalServerError, map[string]interface{}{
					"error":    err.Error(),
					"provider": providerName,
				})
			}
			return
		}

		if result == nil || result.RawLyrics == "" {
			stats.Get().RecordCacheMiss()
			log.Warnf("[%s] No lyrics found for: %s", providerName, query)
			setNegativeCache(cacheKey, "Lyrics not available", "", false)
			Respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").Error(http.StatusNotFound, map[string]interface{}{
				"error":    "Lyrics not available for this track",
				"provider": providerName,
			})
			return
		}

		// Cache the result
		stats.Get().RecordCacheMiss()
		log.Infof("%s [%s] Caching lyrics for: %s", logcolors.LogCacheLyrics, providerName, query)
		setCachedLyrics(cacheKey, result.RawLyrics, result.TrackDurationMs, result.Score, result.Language, result.IsRTL)

		Respond(w, r).SetProvider(providerName).SetCacheStatus("MISS").JSON(map[string]interface{}{
			"lyrics":   result.RawLyrics,
			"provider": providerName,
		})
	}
}

// buildProviderCacheKey builds a cache key with provider prefix
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

func getStats(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	s := stats.Get()
	snapshot := s.Snapshot()

	// Add cache storage info
	numKeys, sizeInKB := persistentCache.Stats()
	snapshot["cache_storage"] = map[string]interface{}{
		"keys":    numKeys,
		"size_kb": sizeInKB,
		"size_mb": float64(sizeInKB) / 1024,
	}

	// Add circuit breaker status
	cbState, failures, cooldownRemaining := ttml.GetCircuitBreakerStats()
	snapshot["circuit_breaker"] = map[string]interface{}{
		"state":              cbState,
		"failures":           failures,
		"cooldown_remaining": cooldownRemaining.String(),
	}

	// Include user agent stats if requested via ?by=user_agent
	if r.URL.Query().Get("by") == "user_agent" {
		snapshot["user_agents"] = s.UserAgentSnapshot()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshot)
}

func getCacheDump(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	cacheDump := CacheDump{}
	persistentCache.Range(func(key string, entry cache.CacheEntry) bool {
		if key == "accessToken" {
			return true
		}
		cacheDump[key] = entry
		return true
	})

	numKeys, sizeInKB := persistentCache.Stats()
	s := stats.Get()

	cacheDumpResponse := CacheDumpResponse{
		NumberOfKeys: numKeys,
		SizeInKB:     sizeInKB,
		SizeInMB:     float64(sizeInKB) / 1024,
		Performance: CachePerformance{
			Hits:         s.CacheHits.Load(),
			Misses:       s.CacheMisses.Load(),
			NegativeHits: s.NegativeCacheHits.Load(),
			StaleHits:    s.StaleCacheHits.Load(),
			HitRate:      s.CacheHitRate(),
		},
		Cache: cacheDump,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cacheDumpResponse)
}

func backupCache(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	backupPath, err := persistentCache.Backup()
	if err != nil {
		log.Errorf("%s Failed to create backup: %v", logcolors.LogCacheBackup, err)
		notifier.PublishCacheBackupFailed(err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Failed to create backup: %v", err),
		})
		return
	}

	log.Infof("%s Backup created successfully at: %s", logcolors.LogCacheBackup, backupPath)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":     "Backup created successfully",
		"backup_path": backupPath,
	})
}

func clearCache(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	backupPath, err := persistentCache.BackupAndClear()
	if err != nil {
		log.Errorf("%s Failed to backup and clear cache: %v", logcolors.LogCacheClear, err)
		notifier.PublishCacheBackupFailed(err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Failed to backup and clear cache: %v", err),
		})
		return
	}

	log.Infof("%s Cache cleared successfully, backup at: %s", logcolors.LogCacheClear, backupPath)
	notifier.PublishCacheCleared(backupPath)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":     "Cache cleared successfully",
		"backup_path": backupPath,
	})
}

func clearProviderCache(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	vars := mux.Vars(r)
	providerName := vars["provider"]

	// Map provider name to cache prefix
	prefixMap := map[string]string{
		"ttml":   "ttml_lyrics:",
		"kugou":  "kugou_lyrics:",
		"legacy": "legacy_lyrics:",
	}

	prefix, ok := prefixMap[providerName]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":           fmt.Sprintf("Unknown provider: %s", providerName),
			"valid_providers": []string{"ttml", "kugou", "legacy"},
		})
		return
	}

	// Find and delete all keys with this prefix
	var keysDeleted int
	var keysToDelete []string

	persistentCache.Range(func(key string, entry cache.CacheEntry) bool {
		if strings.HasPrefix(key, prefix) || strings.HasPrefix(key, "no_lyrics:"+prefix) {
			keysToDelete = append(keysToDelete, key)
		}
		return true
	})

	for _, key := range keysToDelete {
		if err := persistentCache.Delete(key); err != nil {
			log.Warnf("%s Failed to delete key %s: %v", logcolors.LogCacheClear, key, err)
		} else {
			keysDeleted++
		}
	}

	log.Infof("%s Cleared %d cache entries for provider: %s", logcolors.LogCacheClear, keysDeleted, providerName)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":      fmt.Sprintf("Cleared cache for provider: %s", providerName),
		"provider":     providerName,
		"keys_deleted": keysDeleted,
	})
}

func listBackups(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	backups, err := persistentCache.ListBackups()
	if err != nil {
		log.Errorf("%s Failed to list backups: %v", logcolors.LogCacheBackups, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Failed to list backups: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"count":   len(backups),
		"backups": backups,
	})
}

func restoreCache(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Get backup filename from query parameter
	backupFileName := r.URL.Query().Get("backup")
	if backupFileName == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Missing 'backup' query parameter. Use /cache/backups to list available backups.",
		})
		return
	}

	// Restore from the specified backup
	if err := persistentCache.RestoreFromBackup(backupFileName); err != nil {
		log.Errorf("%s Failed to restore from backup %s: %v", logcolors.LogCacheRestore, backupFileName, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Failed to restore from backup: %v", err),
		})
		return
	}

	// Get new cache stats after restore
	numKeys, sizeKB := persistentCache.Stats()

	log.Infof("%s Cache restored from backup: %s", logcolors.LogCacheRestore, backupFileName)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":       "Cache restored successfully",
		"restored_from": backupFileName,
		"keys_restored": numKeys,
		"size_kb":       sizeKB,
	})
}

func getHealthStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get circuit breaker status
	cbState, cbFailures, cbTimeUntilRetry := ttml.GetCircuitBreakerStats()

	// Get account info - use GetAllTTMLAccounts for total count (backward compat)
	// and GetTTMLAccounts for active count
	allAccounts, allAccErr := conf.GetAllTTMLAccounts()
	activeAccounts, _ := conf.GetTTMLAccounts()

	totalAccountCount := 0
	activeAccountCount := 0
	outOfServiceCount := 0
	if allAccErr == nil {
		totalAccountCount = len(allAccounts)
		activeAccountCount = len(activeAccounts)
		outOfServiceCount = totalAccountCount - activeAccountCount
	}

	// Basic health response (always available)
	// accounts field UNCHANGED for backward compatibility - shows total configured
	health := map[string]interface{}{
		"status":                  "ok",
		"accounts":                totalAccountCount,  // UNCHANGED: total configured
		"accounts_active":         activeAccountCount, // NEW: working accounts
		"accounts_out_of_service": outOfServiceCount,  // NEW: accounts with empty credentials
		"circuit_breaker":         cbState,
		"cache_ready":             persistentCache.IsPreloadComplete(),
	}

	// If circuit breaker is open, mark as degraded
	if cbState == "OPEN" {
		health["status"] = "degraded"
		health["circuit_breaker_retry_in"] = cbTimeUntilRetry.String()
	}

	// If no active accounts configured, mark as unhealthy
	if activeAccountCount == 0 {
		health["status"] = "unhealthy"
		if totalAccountCount == 0 {
			health["error"] = "no TTML accounts configured"
		} else {
			health["error"] = "all TTML accounts are out of service (empty credentials)"
		}
	}

	// If authenticated, include detailed token status
	if r.Header.Get("Authorization") == conf.Configuration.CacheAccessToken && conf.Configuration.CacheAccessToken != "" {
		var tokenStatuses []map[string]interface{}
		overallHealthy := true

		// Include shared bearer token status
		bearerExpiry, bearerRemaining, bearerNeedsRefresh := ttml.GetTokenStatus()
		bearerStatus := map[string]interface{}{
			"name": "shared_bearer_token",
			"type": "bearer",
		}
		if bearerExpiry.IsZero() {
			bearerStatus["status"] = "not_initialized"
		} else {
			bearerStatus["expires"] = bearerExpiry.Format("2006-01-02 15:04:05")
			bearerStatus["remaining_minutes"] = int(bearerRemaining.Minutes())
			if bearerNeedsRefresh {
				bearerStatus["status"] = "refreshing_soon"
			} else {
				bearerStatus["status"] = "healthy"
			}
		}
		tokenStatuses = append(tokenStatuses, bearerStatus)

		// Include ALL MUT accounts - use health check status (MUTs are not JWTs)
		healthStatuses := ttml.GetHealthStatuses()
		for _, acc := range allAccounts {
			tokenStatus := map[string]interface{}{
				"name": acc.Name,
				"type": "mut",
			}

			// Handle out-of-service accounts
			if acc.OutOfService {
				tokenStatus["status"] = "out_of_service"
				tokenStatus["reason"] = "empty MUT"
				tokenStatuses = append(tokenStatuses, tokenStatus)
				continue
			}

			// Get health status from canary check instead of JWT parsing
			// MUTs are opaque Apple credentials, not JWTs - cannot parse expiry
			if status, ok := healthStatuses[acc.Name]; ok {
				tokenStatus["last_checked"] = status.LastChecked.Format(time.RFC3339)
				if status.Healthy {
					tokenStatus["status"] = "healthy"
				} else {
					tokenStatus["status"] = "unhealthy"
					tokenStatus["last_error"] = status.LastError
					overallHealthy = false
				}
			} else {
				tokenStatus["status"] = "unknown"
				tokenStatus["note"] = "health check not yet run"
			}

			tokenStatuses = append(tokenStatuses, tokenStatus)
		}

		health["tokens"] = tokenStatuses
		health["circuit_breaker_failures"] = cbFailures

		// Update overall status based on token health
		if !overallHealthy && health["status"] == "ok" {
			health["status"] = "degraded"
		}
	}

	json.NewEncoder(w).Encode(health)
}

// handleMUTHealth handles the /health/mut endpoint for MUT health status
func handleMUTHealth(w http.ResponseWriter, r *http.Request) {
	// Requires auth token
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken || conf.Configuration.CacheAccessToken == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Option to force recheck
	if r.URL.Query().Get("refresh") == "true" {
		results := ttml.CheckAllMUTHealth()
		response := make(map[string]interface{})
		for _, status := range results {
			response[status.AccountName] = map[string]interface{}{
				"healthy":      status.Healthy,
				"last_checked": status.LastChecked.Format(time.RFC3339),
				"last_error":   status.LastError,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	// Return cached health statuses
	statuses := ttml.GetHealthStatuses()
	response := make(map[string]interface{})
	for name, status := range statuses {
		response[name] = map[string]interface{}{
			"healthy":      status.Healthy,
			"last_checked": status.LastChecked.Format(time.RFC3339),
			"last_error":   status.LastError,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getCircuitBreakerStatus(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	state, failures, timeUntilRetry := ttml.GetCircuitBreakerStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"state":            state,
		"failures":         failures,
		"time_until_retry": timeUntilRetry.String(),
		"config": map[string]interface{}{
			"threshold":    conf.Configuration.CircuitBreakerThreshold,
			"cooldown_sec": conf.Configuration.CircuitBreakerCooldownSecs,
		},
	})
}

func resetCircuitBreaker(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ttml.ResetCircuitBreaker()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Circuit breaker reset to CLOSED state",
	})
}

func simulateCircuitBreakerFailure(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ttml.SimulateFailure()
	state, failures, timeUntilRetry := ttml.GetCircuitBreakerStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":          "Simulated a failure",
		"state":            state,
		"failures":         failures,
		"time_until_retry": timeUntilRetry.String(),
	})
}

func testNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	notifiers := setupNotifiers()

	if len(notifiers) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "No notifiers configured. Please configure at least one notifier in your .env file.",
			"help": map[string]string{
				"telegram": "Set NOTIFIER_TELEGRAM_BOT_TOKEN and NOTIFIER_TELEGRAM_CHAT_ID",
				"email":    "Set NOTIFIER_SMTP_HOST, NOTIFIER_SMTP_USERNAME, NOTIFIER_SMTP_PASSWORD, etc.",
				"ntfy":     "Set NOTIFIER_NTFY_TOPIC",
			},
		})
		return
	}

	var tokenInfo string
	var tokenDetails map[string]interface{}

	allAccounts, allAccErr := conf.GetAllTTMLAccounts()
	activeAccounts, _ := conf.GetTTMLAccounts()

	if allAccErr != nil || len(allAccounts) == 0 {
		tokenInfo = "Status:               Not configured\n" +
			"TTML_BEARER_TOKENS:   Missing from environment"
		tokenDetails = map[string]interface{}{
			"configured": false,
		}
		if allAccErr != nil {
			tokenDetails["error"] = allAccErr.Error()
		}
	} else {
		now := time.Now()
		var accountInfos []map[string]interface{}
		var infoLines []string

		// Get health statuses from canary checks (MUTs are not JWTs)
		healthStatuses := ttml.GetHealthStatuses()

		for _, acc := range allAccounts {
			if acc.OutOfService {
				infoLines = append(infoLines, fmt.Sprintf("%s: Out of service (empty MUT)", acc.Name))
				accountInfos = append(accountInfos, map[string]interface{}{
					"name":   acc.Name,
					"status": "out_of_service",
					"reason": "empty MUT",
				})
				continue
			}

			// Use health check status instead of JWT parsing
			// MUTs are opaque Apple credentials, not JWTs
			if status, ok := healthStatuses[acc.Name]; ok {
				statusStr := "healthy"
				if !status.Healthy {
					statusStr = fmt.Sprintf("unhealthy (%s)", status.LastError)
				}
				infoLines = append(infoLines, fmt.Sprintf("%s (MUT): %s (checked %s)",
					acc.Name, statusStr, status.LastChecked.Format("2006-01-02 15:04")))
				accountInfos = append(accountInfos, map[string]interface{}{
					"name":         acc.Name,
					"status":       statusStr,
					"healthy":      status.Healthy,
					"last_checked": status.LastChecked.Format(time.RFC3339),
					"last_error":   status.LastError,
				})
			} else {
				infoLines = append(infoLines, fmt.Sprintf("%s (MUT): health check not yet run", acc.Name))
				accountInfos = append(accountInfos, map[string]interface{}{
					"name":   acc.Name,
					"status": "unknown",
					"note":   "health check not yet run",
				})
			}
		}

		outOfServiceCount := len(allAccounts) - len(activeAccounts)
		tokenInfo = fmt.Sprintf(
			"Current date:         %s\n"+
				"Accounts configured:  %d (active: %d, out of service: %d)\n\n"+
				"Account Status:\n  %s\n\n"+
				"Note: MUT validity is checked via canary requests, not JWT expiry",
			now.Format("2006-01-02 15:04:05"),
			len(allAccounts),
			len(activeAccounts),
			outOfServiceCount,
			strings.Join(infoLines, "\n  "),
		)

		tokenDetails = map[string]interface{}{
			"current_date":            now.Format("2006-01-02 15:04:05"),
			"accounts_configured":     len(allAccounts),
			"accounts_active":         len(activeAccounts),
			"accounts_out_of_service": outOfServiceCount,
			"accounts":                accountInfos,
		}
	}

	subject := "🧪 Test: TTML Token Monitor"
	message := fmt.Sprintf(
		"🧪 TTML TOKEN MONITOR - TEST NOTIFICATION\n\n"+
			"✅ Status: Your notification setup is working correctly.\n\n"+
			"📊 Token Information:\n\n"+
			"%s\n\n"+
			"You will receive similar notifications when your\n"+
			"token is approaching expiration.",
		tokenInfo,
	)

	results := make(map[string]interface{})
	successCount := 0
	failCount := 0

	for _, n := range notifiers {
		notifierType := getNotifierTypeName(n)
		if err := n.Send(subject, message); err != nil {
			results[notifierType] = map[string]string{
				"status": "failed",
				"error":  err.Error(),
			}
			failCount++
			log.Errorf("%s %s failed: %v", logcolors.LogTestNotifications, notifierType, err)
		} else {
			results[notifierType] = map[string]string{
				"status": "success",
			}
			successCount++
			log.Infof("%s %s sent successfully", logcolors.LogTestNotifications, notifierType)
		}
	}

	response := map[string]interface{}{
		"message":    "Test notifications sent",
		"total":      len(notifiers),
		"successful": successCount,
		"failed":     failCount,
		"results":    results,
		"token_info": tokenDetails,
	}

	if failCount > 0 {
		w.WriteHeader(http.StatusPartialContent)
	}

	json.NewEncoder(w).Encode(response)
}

// overrideHandler replaces cached lyrics with content fetched by a specific Apple Music track ID.
// Finds all cache entries matching the song+artist query and updates their TTML field.
// Requires a valid API key (same pattern as revalidateHandler).
func overrideHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Require valid API key
	apiKeyAuthenticated, _ := r.Context().Value(apiKeyAuthenticatedKey).(bool)
	if !apiKeyAuthenticated {
		Respond(w, r).Error(http.StatusUnauthorized, map[string]interface{}{
			"error":   "API key required for override",
			"message": "Provide a valid API key via X-API-Key header",
		})
		return
	}

	// 2. Parse params
	trackID := r.URL.Query().Get("id")
	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")
	dryRun := r.URL.Query().Get("dry_run") == "true"
	noLyrics := r.URL.Query().Get("no_lyrics") == "true"

	// 3. Validate required params
	if songName == "" || artistName == "" {
		Respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "song (s) and artist (a) parameters are required",
		})
		return
	}

	if trackID == "" && !dryRun && !noLyrics {
		Respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "id parameter is required (Apple Music track ID)",
		})
		return
	}

	// Validate track ID is numeric (Apple Music IDs are always numeric)
	if trackID != "" {
		if _, err := strconv.Atoi(trackID); err != nil {
			Respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
				"error": "id must be a numeric Apple Music track ID",
			})
			return
		}
	}

	// 4. Find matching cache keys using direct lookups (avoids full cache scan)
	matchingKeys := findMatchingCacheKeys(songName, artistName, albumName, durationStr)

	// 5. Dry run: return matching keys without modifying anything
	if dryRun {
		query := strings.ToLower(strings.TrimSpace(songName)) + " " + strings.ToLower(strings.TrimSpace(artistName))
		log.Infof("%s Dry run: found %d matching keys for %s", logcolors.LogOverride, len(matchingKeys), query)
		Respond(w, r).JSON(map[string]interface{}{
			"dry_run": true,
			"count":   len(matchingKeys),
			"keys":    matchingKeys,
		})
		return
	}

	// 6. Handle no_lyrics mode: store sentinel to permanently mark as "no lyrics"
	if noLyrics {
		var updatedKeys []string
		created := false

		if len(matchingKeys) == 0 {
			cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
			setCachedLyrics(cacheKey, NoLyricsSentinel, 0, 0, "", false)
			updatedKeys = append(updatedKeys, cacheKey)
			created = true
			log.Infof("%s Created no_lyrics marker for %s", logcolors.LogOverride, cacheKey)
		} else {
			for _, key := range matchingKeys {
				cached, ok := getCachedLyrics(key)
				if !ok {
					continue
				}
				setCachedLyrics(key, NoLyricsSentinel, cached.TrackDurationMs, cached.Score, cached.Language, cached.IsRTL)
				updatedKeys = append(updatedKeys, key)
			}
			log.Infof("%s Set no_lyrics marker on %d cache entries", logcolors.LogOverride, len(updatedKeys))
		}

		// Clear any negative cache entries for this query
		deleteNegativeCache(buildNormalizedCacheKey(songName, artistName, albumName, durationStr))

		Respond(w, r).JSON(map[string]interface{}{
			"updated":   len(updatedKeys),
			"created":   created,
			"keys":      updatedKeys,
			"no_lyrics": true,
		})
		return
	}

	// 7. Fetch lyrics by track ID
	log.Infof("%s Fetching lyrics for track ID %s to override %d cache entries", logcolors.LogOverride, trackID, len(matchingKeys))
	ttmlString, err := ttml.FetchLyricsByTrackID(trackID)
	if err != nil {
		log.Errorf("%s Failed to fetch lyrics for track ID %s: %v", logcolors.LogOverride, trackID, err)
		Respond(w, r).Error(http.StatusInternalServerError, map[string]interface{}{
			"error":    fmt.Sprintf("failed to fetch lyrics: %v", err),
			"track_id": trackID,
		})
		return
	}

	// 8. Update matching cache entries, or create a new one if none exist
	var updatedKeys []string
	created := false

	if len(matchingKeys) == 0 {
		// No existing entries — create a new cache entry using the same key format as /getLyrics
		cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)

		var durationMs int
		if durationStr != "" {
			fmt.Sscanf(durationStr, "%d", &durationMs)
			durationMs = durationMs * 1000
		}

		language, isRTL := ttml.DetectLanguage(ttmlString)
		setCachedLyrics(cacheKey, ttmlString, durationMs, 0, language, isRTL)
		updatedKeys = append(updatedKeys, cacheKey)
		created = true
		log.Infof("%s Created new cache entry %s with lyrics from track ID %s", logcolors.LogOverride, cacheKey, trackID)
	} else {
		for _, key := range matchingKeys {
			cached, ok := getCachedLyrics(key)
			if !ok {
				continue
			}

			// Replace only the TTML content, preserve existing metadata
			setCachedLyrics(key, ttmlString, cached.TrackDurationMs, cached.Score, cached.Language, cached.IsRTL)
			updatedKeys = append(updatedKeys, key)
		}
		log.Infof("%s Updated %d cache entries with lyrics from track ID %s", logcolors.LogOverride, len(updatedKeys), trackID)
	}

	// 9. Clear any negative cache entries for this query
	deleteNegativeCache(buildNormalizedCacheKey(songName, artistName, albumName, durationStr))

	Respond(w, r).JSON(map[string]interface{}{
		"updated":  len(updatedKeys),
		"created":  created,
		"keys":     updatedKeys,
		"track_id": trackID,
	})
}

// helpHandler returns API documentation
func helpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"help": "Lyrics API with multiple provider support",
		"docs": "https://lyrics-api-docs.boidu.dev",
		"endpoints": map[string]string{
			"/getLyrics":        "Default provider (TTML)",
			"/ttml/getLyrics":   "TTML provider (word-level timing)",
			"/kugou/getLyrics":  "Kugou provider (line-level timing)",
			"/legacy/getLyrics": "Legacy Spotify-based provider",
		},
		"parameters": map[string]string{
			"s, song, songName":     "Song name (required)",
			"a, artist, artistName": "Artist name (required)",
			"al, album, albumName":  "Album name (optional, improves matching)",
			"d, duration":           "Duration in seconds (optional, improves matching)",
			"videoId, v":            "YouTube video ID (optional, associates video with song for proxy revalidation)",
		},
		"example": "/getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran",
		"notes":   "The API uses provider-specific matching algorithms. Providing more parameters improves accuracy.",
	})
}

// revalidateHandler checks if cached lyrics are stale and updates them if needed.
// Requires a valid API key and uses the same parameters as getLyrics.
func revalidateHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Require valid API key
	apiKeyAuthenticated, _ := r.Context().Value(apiKeyAuthenticatedKey).(bool)
	if !apiKeyAuthenticated {
		Respond(w, r).Error(http.StatusUnauthorized, map[string]interface{}{
			"error":   "API key required for revalidation",
			"message": "Provide a valid API key via X-API-Key header",
		})
		return
	}

	// 2. Parse params (same as getLyrics)
	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")

	if songName == "" || artistName == "" {
		Respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "song (s) and artist (a) parameters are required",
		})
		return
	}

	// 3. Build cache key (same logic as getLyrics)
	cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
	legacyCacheKey := buildLegacyCacheKey(songName, artistName, albumName, durationStr)

	// 4. Get cached content (check positive cache first, then negative cache)
	cached, found := getCachedLyrics(cacheKey)
	usedKey := cacheKey
	if !found && legacyCacheKey != cacheKey {
		// Try legacy key
		cached, found = getCachedLyrics(legacyCacheKey)
		usedKey = legacyCacheKey
	}

	// Treat no-lyrics sentinel entries like negative cache (allow revalidation to replace them)
	wasNoLyricsSentinel := found && cached.TTML == NoLyricsSentinel

	// Check if this was in negative cache (allows revalidation of "no lyrics" entries)
	wasInNegativeCache := false
	if !found {
		if _, negFound := getNegativeCache(cacheKey); negFound {
			wasInNegativeCache = true
			found = true // Allow revalidation to proceed
			usedKey = cacheKey
		} else if legacyCacheKey != cacheKey {
			if _, negFound := getNegativeCache(legacyCacheKey); negFound {
				wasInNegativeCache = true
				usedKey = legacyCacheKey
				found = true
			}
		}
	}

	if !found {
		Respond(w, r).Error(http.StatusNotFound, map[string]interface{}{
			"error":    "no cached lyrics found for this query",
			"cacheKey": cacheKey,
		})
		return
	}

	// 5. Compute hash of cached content (empty if from negative cache or no-lyrics sentinel)
	var oldHash [16]byte
	if !wasInNegativeCache && !wasNoLyricsSentinel {
		oldHash = md5.Sum([]byte(cached.TTML))
	}

	// 6. Fetch fresh content
	var durationMs int
	if durationStr != "" {
		fmt.Sscanf(durationStr, "%d", &durationMs)
		durationMs = durationMs * 1000 // Convert seconds to milliseconds
	}

	log.Infof("%s Revalidating cache for: %s %s", logcolors.LogRevalidate, songName, artistName)
	ttmlString, trackDurationMs, score, trackMeta, err := ttml.FetchTTMLLyrics(songName, artistName, albumName, durationMs)

	if err != nil {
		log.Warnf("%s Revalidation fetch failed: %v", logcolors.LogRevalidate, err)
		Respond(w, r).JSON(map[string]interface{}{
			"error":    err.Error(),
			"updated":  false,
			"cacheKey": usedKey,
		})
		return
	}

	if ttmlString == "" {
		Respond(w, r).JSON(map[string]interface{}{
			"error":    "no lyrics found from source",
			"updated":  false,
			"cacheKey": usedKey,
		})
		return
	}

	// 7. Compare hashes (if from negative cache or no-lyrics sentinel, always treat as updated)
	newHash := md5.Sum([]byte(ttmlString))
	updated := wasInNegativeCache || wasNoLyricsSentinel || oldHash != newHash

	if updated {
		// Delete negative cache if it existed
		if wasInNegativeCache {
			deleteNegativeCache(usedKey)
		}
		// Update cache with fresh content
		language, isRTL := ttml.DetectLanguage(ttmlString)
		setCachedLyrics(usedKey, ttmlString, trackDurationMs, score, language, isRTL)
		go bini.PostLyrics(trackMeta.Name, trackMeta.ArtistName, trackMeta.AlbumName, trackDurationMs, ttmlString, trackMeta.ISRC)
		go func() {
			// Update metadata before proxy revalidation (which queries metadata for videoIds)
			setSongMetadata(&SongMetadata{
				CacheKey:     usedKey,
				AppleTrackID: trackMeta.TrackID,
				ISRC:         trackMeta.ISRC,
				TrackName:    trackMeta.Name,
				ArtistName:   trackMeta.ArtistName,
				AlbumName:    trackMeta.AlbumName,
				DurationMs:   trackDurationMs,
				ReleaseDate:  trackMeta.ReleaseDate,
			})
			proxy.RevalidateAllForSong(trackMeta.Name, trackMeta.ArtistName, trackMeta.AlbumName, trackDurationMs/1000, getAllVideoIDsForSong)
		}()
		log.Infof("%s Content changed, cache updated for: %s", logcolors.LogRevalidate, usedKey)
	} else {
		log.Infof("%s Content unchanged for: %s", logcolors.LogRevalidate, usedKey)
	}

	// 8. Return result
	Respond(w, r).JSON(map[string]interface{}{
		"updated":          updated,
		"cacheKey":         usedKey,
		"wasNegativeCache": wasInNegativeCache,
	})
}

// videoMapImportHandler handles bulk import of videoId-to-song mappings.
// Protected by CACHE_ACCESS_TOKEN.
func videoMapImportHandler(w http.ResponseWriter, r *http.Request) {
	// Require auth
	if conf.Configuration.CacheAccessToken != "" {
		token := r.Header.Get("Authorization")
		if token != conf.Configuration.CacheAccessToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Cap request body size (10MB max)
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	type videoMapEntry struct {
		VideoID  string `json:"videoId"`
		Song     string `json:"song"`
		Artist   string `json:"artist"`
		Album    string `json:"album,omitempty"`
		Duration string `json:"duration,omitempty"`
	}

	var entries []videoMapEntry
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&entries); err != nil {
		Respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "Invalid JSON body: " + err.Error(),
		})
		return
	}

	const maxVideoMapEntries = 10_000
	if len(entries) > maxVideoMapEntries {
		Respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": fmt.Sprintf("too many entries: %d (max %d)", len(entries), maxVideoMapEntries),
		})
		return
	}

	processed := 0
	for _, entry := range entries {
		if entry.VideoID == "" || (entry.Song == "" && entry.Artist == "") {
			continue
		}
		cacheKey := buildNormalizedCacheKey(entry.Song, entry.Artist, entry.Album, entry.Duration)
		addVideoID(cacheKey, entry.VideoID)
		processed++
	}

	Respond(w, r).JSON(map[string]interface{}{
		"processed": processed,
		"total":     len(entries),
	})
}

// enrichMetadata expands a SongMetadata into a richer response object:
//   - Parses RawAttributes JSON string into a structured "rawAttributes" object (falls back to raw string on parse failure)
//   - Adds a "lyrics" sub-object describing whether lyrics are actually cached for this entry
func enrichMetadata(meta *SongMetadata) map[string]interface{} {
	if meta == nil {
		return nil
	}

	// Marshal-then-unmarshal to get a flat map of all SongMetadata fields (preserves json tags)
	raw, err := json.Marshal(meta)
	if err != nil {
		// Fail open: construct a minimal map so callers still get something useful
		return map[string]interface{}{"cacheKey": meta.CacheKey}
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]interface{}{"cacheKey": meta.CacheKey}
	}

	// Parse RawAttributes JSON string into a structured object when possible
	if meta.RawAttributes != "" {
		var attrs map[string]interface{}
		if err := json.Unmarshal([]byte(meta.RawAttributes), &attrs); err == nil {
			out["rawAttributes"] = attrs
		}
		// On parse failure, leave the raw string as-is (never drop data)
	}

	// Attach lyrics-cache status
	lyricsInfo := map[string]interface{}{"cached": false}
	if cached, ok := getCachedLyrics(meta.CacheKey); ok {
		if cached.TTML == NoLyricsSentinel {
			lyricsInfo["cached"] = true
			lyricsInfo["noLyrics"] = true
		} else {
			lyricsInfo["cached"] = true
			lyricsInfo["noLyrics"] = false
			lyricsInfo["ttmlBytes"] = len(cached.TTML)
			lyricsInfo["trackDurationMs"] = cached.TrackDurationMs
			lyricsInfo["score"] = cached.Score
			lyricsInfo["language"] = cached.Language
			lyricsInfo["isRTL"] = cached.IsRTL
		}
	}
	out["lyrics"] = lyricsInfo

	return out
}

// metadataLookupHandler returns stored metadata for a song.
// Supports lookup by videoId (reverse index), ISRC (reverse index), or song+artist (builds cache key).
// Each result is enriched with parsed Apple Music attributes and lyrics-cache status.
// Protected by CACHE_ACCESS_TOKEN.
func metadataLookupHandler(w http.ResponseWriter, r *http.Request) {
	if conf.Configuration.CacheAccessToken != "" {
		token := r.Header.Get("Authorization")
		if token != conf.Configuration.CacheAccessToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	videoID := r.URL.Query().Get("videoId")
	isrc := r.URL.Query().Get("isrc")
	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")

	// Lookup by videoId
	if videoID != "" {
		cacheKeys := getCacheKeysByVideoID(videoID)
		if len(cacheKeys) == 0 {
			Respond(w, r).Error(http.StatusNotFound, map[string]interface{}{
				"error": "no metadata found for videoId: " + videoID,
			})
			return
		}
		results := make([]map[string]interface{}, 0, len(cacheKeys))
		for _, ck := range cacheKeys {
			if meta, ok := getSongMetadata(ck); ok {
				results = append(results, enrichMetadata(meta))
			}
		}
		Respond(w, r).JSON(map[string]interface{}{
			"videoId": videoID,
			"results": results,
		})
		return
	}

	// Lookup by ISRC
	if isrc != "" {
		cacheKeys := getIndex("isrc:" + isrc)
		if len(cacheKeys) == 0 {
			Respond(w, r).Error(http.StatusNotFound, map[string]interface{}{
				"error": "no metadata found for isrc: " + isrc,
			})
			return
		}
		results := make([]map[string]interface{}, 0, len(cacheKeys))
		for _, ck := range cacheKeys {
			if meta, ok := getSongMetadata(ck); ok {
				results = append(results, enrichMetadata(meta))
			}
		}
		Respond(w, r).JSON(map[string]interface{}{
			"isrc":    isrc,
			"results": results,
		})
		return
	}

	// Lookup by song+artist
	if songName == "" && artistName == "" {
		Respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "provide song+artist (s, a), videoId, or isrc",
		})
		return
	}

	cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
	meta, ok := getSongMetadata(cacheKey)
	if !ok {
		// Try song index for all duration variants
		songKey := buildSongIndexKey(songName, artistName)
		cacheKeys := getIndex("song:" + songKey)
		if len(cacheKeys) == 0 {
			Respond(w, r).Error(http.StatusNotFound, map[string]interface{}{
				"error":    "no metadata found",
				"cacheKey": cacheKey,
			})
			return
		}
		allVids := getAllVideoIDsForSong(songName, artistName)
		results := make([]map[string]interface{}, 0, len(cacheKeys))
		for _, ck := range cacheKeys {
			if m, ok := getSongMetadata(ck); ok {
				results = append(results, enrichMetadata(m))
			}
		}
		Respond(w, r).JSON(map[string]interface{}{
			"cacheKey":     cacheKey,
			"allVideoIds":  allVids,
			"allCacheKeys": cacheKeys,
			"results":      results,
		})
		return
	}

	Respond(w, r).JSON(map[string]interface{}{
		"cacheKey": cacheKey,
		"metadata": enrichMetadata(meta),
	})
}
