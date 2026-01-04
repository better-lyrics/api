package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"lyrics-api-go/cache"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"
	"lyrics-api-go/services/providers"
	"lyrics-api-go/stats"
	"net/http"
	"strings"
	"time"

	// Import providers to trigger their init() registration
	_ "lyrics-api-go/services/providers/kugou"
	_ "lyrics-api-go/services/providers/legacy"
	ttml "lyrics-api-go/services/providers/ttml"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

func getLyrics(w http.ResponseWriter, r *http.Request) {
	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")

	if songName == "" && artistName == "" {
		http.Error(w, "Song name or artist name not provided", http.StatusUnprocessableEntity)
		return
	}

	// Use normalized cache key for consistent cache hits regardless of input casing/whitespace
	cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
	legacyCacheKey := buildLegacyCacheKey(songName, artistName, albumName, durationStr)

	// For logging, use a clean query string
	query := strings.ToLower(strings.TrimSpace(songName)) + " " + strings.ToLower(strings.TrimSpace(artistName))

	// Check if we're in cache-only mode (rate limit tier 2)
	cacheOnlyMode, _ := r.Context().Value(cacheOnlyModeKey).(bool)

	// Check if API key was required but not provided (cache-first mode)
	apiKeyRequired, _ := r.Context().Value(apiKeyRequiredForFreshKey).(bool)
	apiKeyInvalid, _ := r.Context().Value(apiKeyInvalidKey).(bool)

	// Check cache first (normalized key, then legacy key for backwards compatibility)
	if cached, ok := getCachedLyrics(cacheKey); ok {
		stats.Get().RecordCacheHit()
		log.Infof("%s Found cached TTML", logcolors.LogCacheLyrics)
		Respond(w, r).SetCacheStatus("HIT").JSON(map[string]interface{}{
			"ttml": cached.TTML,
		})
		return
	}

	// Check legacy cache key (backwards compatibility - use /cache/migrate to convert)
	if legacyCacheKey != cacheKey {
		if cached, ok := getCachedLyrics(legacyCacheKey); ok {
			stats.Get().RecordCacheHit()
			log.Infof("%s Found cached TTML under legacy key", logcolors.LogCacheLyrics)
			Respond(w, r).SetCacheStatus("HIT").JSON(map[string]interface{}{
				"ttml": cached.TTML,
			})
			return
		}
	}

	// Check negative cache (known "no lyrics" responses)
	if reason, found := getNegativeCache(cacheKey); found {
		stats.Get().RecordNegativeCacheHit()
		log.Infof("%s Returning cached 'no lyrics' response for: %s", logcolors.LogCacheNegative, query)
		Respond(w, r).SetCacheStatus("NEGATIVE_HIT").Error(http.StatusNotFound, map[string]interface{}{
			"error": reason,
		})
		return
	}

	// Check legacy negative cache (backwards compatibility)
	if legacyCacheKey != cacheKey {
		if reason, found := getNegativeCache(legacyCacheKey); found {
			stats.Get().RecordNegativeCacheHit()
			log.Infof("%s Returning cached 'no lyrics' (legacy key) for: %s", logcolors.LogCacheNegative, query)
			Respond(w, r).SetCacheStatus("NEGATIVE_HIT").Error(http.StatusNotFound, map[string]interface{}{
				"error": reason,
			})
			return
		}
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

	// If in cache-only mode and no cache found, return 429
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

	ttmlString, trackDurationMs, score, err := ttml.FetchTTMLLyrics(songName, artistName, albumName, durationMs)

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
			setNegativeCache(cacheKey, err.Error())
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
		setNegativeCache(cacheKey, "Lyrics not available for this track")
		Respond(w, r).SetCacheStatus("MISS").Error(http.StatusNotFound, map[string]interface{}{
			"error": "Lyrics not available for this track",
		})
		return
	}

	stats.Get().RecordCacheMiss()
	log.Infof("%s Caching TTML for: %s (trackDuration: %dms)", logcolors.LogCacheLyrics, query, trackDurationMs)
	setCachedLyrics(cacheKey, ttmlString, trackDurationMs, score, "", false)

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

		// If in cache-only mode and no cache found, return 429
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
				setNegativeCache(cacheKey, err.Error())
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
			setNegativeCache(cacheKey, "Lyrics not available")
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
			"error":             fmt.Sprintf("Unknown provider: %s", providerName),
			"valid_providers":   []string{"ttml", "kugou", "legacy"},
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

	// Get account info
	accounts, accErr := conf.GetTTMLAccounts()
	accountCount := 0
	if accErr == nil {
		accountCount = len(accounts)
	}

	// Basic health response (always available)
	health := map[string]interface{}{
		"status":          "ok",
		"accounts":        accountCount,
		"circuit_breaker": cbState,
		"cache_ready":     persistentCache.IsPreloadComplete(),
	}

	// If circuit breaker is open, mark as degraded
	if cbState == "OPEN" {
		health["status"] = "degraded"
		health["circuit_breaker_retry_in"] = cbTimeUntilRetry.String()
	}

	// If no accounts configured, mark as unhealthy
	if accountCount == 0 {
		health["status"] = "unhealthy"
		health["error"] = "no TTML accounts configured"
	}

	// If authenticated, include detailed token status
	if r.Header.Get("Authorization") == conf.Configuration.CacheAccessToken && conf.Configuration.CacheAccessToken != "" {
		var tokenStatuses []map[string]interface{}
		overallHealthy := true
		warningThreshold := 7

		for _, acc := range accounts {
			tokenStatus := map[string]interface{}{
				"name": acc.Name,
			}

			expirationDate, err := notifier.GetExpirationDate(acc.BearerToken)
			if err != nil {
				tokenStatus["status"] = "error"
				tokenStatus["error"] = err.Error()
				overallHealthy = false
			} else {
				daysRemaining := int(time.Until(expirationDate).Hours() / 24)
				tokenStatus["expires"] = expirationDate.Format("2006-01-02 15:04:05")
				tokenStatus["days_remaining"] = daysRemaining

				if daysRemaining <= 0 {
					tokenStatus["status"] = "expired"
					overallHealthy = false
				} else if daysRemaining <= warningThreshold {
					tokenStatus["status"] = "expiring_soon"
				} else {
					tokenStatus["status"] = "healthy"
				}
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

	accounts, accErr := conf.GetTTMLAccounts()
	if accErr != nil || len(accounts) == 0 {
		tokenInfo = "Status:               Not configured\n" +
			"TTML_BEARER_TOKENS:   Missing from environment"
		tokenDetails = map[string]interface{}{
			"configured": false,
		}
		if accErr != nil {
			tokenDetails["error"] = accErr.Error()
		}
	} else {
		now := time.Now()
		var accountInfos []map[string]interface{}
		var infoLines []string

		for _, acc := range accounts {
			expirationDate, err := notifier.GetExpirationDate(acc.BearerToken)
			if err != nil {
				infoLines = append(infoLines, fmt.Sprintf("%s: Error - %v", acc.Name, err))
				accountInfos = append(accountInfos, map[string]interface{}{
					"name":  acc.Name,
					"error": err.Error(),
				})
			} else {
				daysUntilExpiration := int(time.Until(expirationDate).Hours() / 24)
				infoLines = append(infoLines, fmt.Sprintf("%s: %d days remaining (expires %s)",
					acc.Name, daysUntilExpiration, expirationDate.Format("2006-01-02")))
				accountInfos = append(accountInfos, map[string]interface{}{
					"name":                  acc.Name,
					"token_expires":         expirationDate.Format("2006-01-02 15:04:05"),
					"days_until_expiration": daysUntilExpiration,
				})
			}
		}

		tokenInfo = fmt.Sprintf(
			"Current date:         %s\n"+
				"Accounts configured:  %d\n\n"+
				"Account Status:\n  %s\n\n"+
				"Warning threshold:    7 days before expiration\n"+
				"Reminder frequency:   Daily until updated",
			now.Format("2006-01-02 15:04:05"),
			len(accounts),
			strings.Join(infoLines, "\n  "),
		)

		tokenDetails = map[string]interface{}{
			"current_date":        now.Format("2006-01-02 15:04:05"),
			"accounts_configured": len(accounts),
			"accounts":            accountInfos,
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

// helpHandler returns API documentation
func helpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"help": "Lyrics API with multiple provider support",
		"endpoints": map[string]string{
			"/getLyrics":        "Default provider (configurable via DEFAULT_PROVIDER env)",
			"/ttml/getLyrics":   "TTML provider (word-level timing)",
			"/kugou/getLyrics":  "Kugou provider (line-level timing)",
			"/legacy/getLyrics": "Legacy Spotify-based provider",
			"/revalidate":       "Check if cached lyrics are stale and update if needed (requires API key)",
		},
		"parameters": map[string]string{
			"s, song, songName":     "Song name (required)",
			"a, artist, artistName": "Artist name (required)",
			"al, album, albumName":  "Album name (optional, improves matching)",
			"d, duration":           "Duration in seconds (optional, improves matching)",
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

	// 5. Compute hash of cached content (empty if from negative cache)
	var oldHash [16]byte
	if !wasInNegativeCache {
		oldHash = md5.Sum([]byte(cached.TTML))
	}

	// 6. Fetch fresh content
	var durationMs int
	if durationStr != "" {
		fmt.Sscanf(durationStr, "%d", &durationMs)
		durationMs = durationMs * 1000 // Convert seconds to milliseconds
	}

	log.Infof("%s Revalidating cache for: %s %s", logcolors.LogRevalidate, songName, artistName)
	ttmlString, trackDurationMs, score, err := ttml.FetchTTMLLyrics(songName, artistName, albumName, durationMs)

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

	// 7. Compare hashes (if from negative cache, always treat as updated)
	newHash := md5.Sum([]byte(ttmlString))
	updated := wasInNegativeCache || oldHash != newHash

	if updated {
		// Delete negative cache if it existed
		if wasInNegativeCache {
			deleteNegativeCache(usedKey)
		}
		// Update cache with fresh content
		setCachedLyrics(usedKey, ttmlString, trackDurationMs, score, "", false)
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
