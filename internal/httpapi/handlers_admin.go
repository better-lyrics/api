package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"

	ttml "lyrics-api-go/services/providers/ttml"
	"lyrics-api-go/stats"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

// adminAuthorized reports whether the request carries the configured admin token.
// An empty configured token denies all callers (closes the empty-token passthrough).
func (s *Server) adminAuthorized(r *http.Request) bool {
	t := s.cfg.Configuration.CacheAccessToken
	return t != "" && r.Header.Get("Authorization") == t
}

func (s *Server) adminOnly(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.adminAuthorized(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	})
}

// retiredEndpoint keeps the admin auth gate but returns 410 Gone: these BoltDB-file
// operations have no equivalent on the managed Postgres backend.
func (s *Server) retiredEndpoint(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	respond(w, r).Error(http.StatusGone, map[string]interface{}{
		"error":   "Endpoint removed",
		"message": "This operation is not available on the managed Postgres backend. Backups and point-in-time recovery are handled by the platform.",
	})
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	st := stats.Get()
	snapshot := st.Snapshot()

	counts, _ := s.store.Counts(r.Context())
	var total int64
	for _, n := range counts {
		total += n
	}
	sizeBytes, _ := s.store.SizeBytes(r.Context())
	snapshot["cache_storage"] = map[string]interface{}{
		"keys_total":       total,
		"keys_by_provider": counts,
		"size_bytes":       sizeBytes,
		"size_mb":          float64(sizeBytes) / (1024 * 1024),
	}

	cbState, failures, cooldownRemaining := ttml.GetCircuitBreakerStats()
	snapshot["circuit_breaker"] = map[string]interface{}{
		"state":              cbState,
		"failures":           failures,
		"cooldown_remaining": cooldownRemaining.String(),
	}

	if r.URL.Query().Get("by") == "user_agent" {
		snapshot["user_agents"] = st.UserAgentSnapshot()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshot)
}

func (s *Server) getHealthStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cbState, cbFailures, cbTimeUntilRetry := ttml.GetCircuitBreakerStats()

	allAccounts, allAccErr := s.cfg.GetAllTTMLAccounts()
	activeAccounts, _ := s.cfg.GetTTMLAccounts()

	totalAccountCount := 0
	activeAccountCount := 0
	outOfServiceCount := 0
	if allAccErr == nil {
		totalAccountCount = len(allAccounts)
		activeAccountCount = len(activeAccounts)
		outOfServiceCount = totalAccountCount - activeAccountCount
	}

	health := map[string]interface{}{
		"status":                  "ok",
		"accounts":                totalAccountCount,
		"accounts_active":         activeAccountCount,
		"accounts_out_of_service": outOfServiceCount,
		"circuit_breaker":         cbState,
		"cache_ready":             true,
	}

	if cbState == "OPEN" {
		health["status"] = "degraded"
		health["circuit_breaker_retry_in"] = cbTimeUntilRetry.String()
	}

	if activeAccountCount == 0 {
		health["status"] = "unhealthy"
		if totalAccountCount == 0 {
			health["error"] = "no TTML accounts configured"
		} else {
			health["error"] = "all TTML accounts are out of service (empty credentials)"
		}
	}

	if r.Header.Get("Authorization") == s.cfg.Configuration.CacheAccessToken && s.cfg.Configuration.CacheAccessToken != "" {
		var tokenStatuses []map[string]interface{}
		overallHealthy := true

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

		healthStatuses := ttml.GetHealthStatuses()
		for _, acc := range allAccounts {
			tokenStatus := map[string]interface{}{
				"name": acc.Name,
				"type": "mut",
			}

			if acc.OutOfService {
				tokenStatus["status"] = "out_of_service"
				tokenStatus["reason"] = "empty MUT"
				tokenStatuses = append(tokenStatuses, tokenStatus)
				continue
			}

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

		if !overallHealthy && health["status"] == "ok" {
			health["status"] = "degraded"
		}
	}

	json.NewEncoder(w).Encode(health)
}

func (s *Server) handleMUTHealth(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

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

func (s *Server) getCircuitBreakerStatus(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
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
			"threshold":    s.cfg.Configuration.CircuitBreakerThreshold,
			"cooldown_sec": s.cfg.Configuration.CircuitBreakerCooldownSecs,
		},
	})
}

func (s *Server) resetCircuitBreaker(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ttml.ResetCircuitBreaker()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Circuit breaker reset to CLOSED state",
	})
}

func (s *Server) simulateCircuitBreakerFailure(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
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

func (s *Server) helpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"help":    "Lyrics API with multiple provider support",
		"docs":    "https://docs.betterlyrics.org",
		"openapi": "/openapi.json",
		"endpoints": map[string]string{
			"/getLyrics":        "Default provider (TTML)",
			"/ttml/getLyrics":   "TTML provider (syllable-level timing)",
			"/kugou/getLyrics":  "Kugou provider (line-level timing)",
			"/qq/getLyrics":     "QQ provider",
			"/legacy/getLyrics": "Legacy provider",
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

// getCacheDump answers /cache with 410 Gone. This body is part of the frozen contract.
func (s *Server) getCacheDump(w http.ResponseWriter, r *http.Request) {
	respond(w, r).Error(http.StatusGone, map[string]interface{}{
		"error":   "Endpoint removed",
		"message": "/cache has been removed. Use the alternatives below.",
		"alternatives": map[string]string{
			"/stats":                    "Request, cache, and performance statistics",
			"/cache/keys":               "List cache keys (paginated)",
			"/cache/debug?key=...":      "Inspect a specific cache entry",
			"/cache/lookup?s=...&a=...": "Check if a song is cached",
		},
	})
}

func (s *Server) cacheHelp(w http.ResponseWriter, r *http.Request) {
	help := map[string]interface{}{
		"description": "Cache management and debugging endpoints",
		"endpoints": []map[string]interface{}{
			{"path": "/cache", "method": "GET", "auth": "None", "description": "REMOVED - returns HTTP 410 Gone."},
			{"path": "/cache/help", "method": "GET", "auth": "None", "description": "This help documentation"},
			{"path": "/cache/lookup", "method": "GET", "auth": "Authorization header required", "description": "Check if a song is cached and get cache key info"},
			{"path": "/cache/debug", "method": "GET", "auth": "Authorization header required", "description": "Get detailed info about a specific cache key"},
			{"path": "/cache/keys", "method": "GET", "auth": "Authorization header required", "description": "List and search cache keys"},
			{"path": "/cache/clear", "method": "GET", "auth": "Authorization header required", "description": "Clear all cached lyrics and negative entries"},
			{"path": "/cache/clear/{provider}", "method": "GET", "auth": "Authorization header required", "description": "Clear cached entries for one provider"},
		},
		"cache_key_format": map[string]string{
			"lyrics":   "ttml_lyrics:{song} {artist} [{album}] [{duration}s]",
			"negative": "stored per cache_key with a computed expiry",
		},
		"notes": []string{
			"All keys are normalized to lowercase with trimmed whitespace",
			"Lyrics cache has no TTL - entries persist until manually cleared",
			"Backups and point-in-time recovery are handled by the managed Postgres platform",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(help)
}

func (s *Server) clearCache(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := s.store.ClearAll(r.Context()); err != nil {
		log.Errorf("%s Failed to clear cache: %v", logcolors.LogCacheClear, err)
		respond(w, r).Error(http.StatusInternalServerError, map[string]interface{}{
			"error": fmt.Sprintf("Failed to clear cache: %v", err),
		})
		return
	}

	log.Infof("%s Cache cleared", logcolors.LogCacheClear)
	respond(w, r).JSON(map[string]interface{}{
		"message": "Cache cleared successfully",
	})
}

func (s *Server) clearProviderCache(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	providerName := mux.Vars(r)["provider"]
	validProviders := map[string]bool{"ttml": true, "kugou": true, "qq": true, "legacy": true}
	if !validProviders[providerName] {
		respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error":           fmt.Sprintf("Unknown provider: %s", providerName),
			"valid_providers": []string{"ttml", "kugou", "qq", "legacy"},
		})
		return
	}

	deleted, err := s.store.ClearProvider(r.Context(), providerName)
	if err != nil {
		log.Errorf("%s Failed to clear provider cache: %v", logcolors.LogCacheClear, err)
		respond(w, r).Error(http.StatusInternalServerError, map[string]interface{}{
			"error": fmt.Sprintf("Failed to clear provider cache: %v", err),
		})
		return
	}

	log.Infof("%s Cleared %d cache entries for provider: %s", logcolors.LogCacheClear, deleted, providerName)
	respond(w, r).JSON(map[string]interface{}{
		"message":      fmt.Sprintf("Cleared cache for provider: %s", providerName),
		"provider":     providerName,
		"keys_deleted": deleted,
	})
}

func (s *Server) cacheLookup(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")

	if songName == "" && artistName == "" {
		respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "Provide at least song (s) or artist (a) parameter",
		})
		return
	}

	ctx := r.Context()
	normalizedKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
	legacyKey := buildLegacyCacheKey(songName, artistName, albumName, durationStr)

	result := map[string]interface{}{
		"query": map[string]string{
			"song": songName, "artist": artistName, "album": albumName, "duration": durationStr,
		},
		"normalized_key": normalizedKey,
		"legacy_key":     legacyKey,
		"keys_differ":    normalizedKey != legacyKey,
	}

	if cached, ok := s.getCachedLyrics(ctx, normalizedKey); ok {
		fillLookupHit(result, "normalized", cached)
	} else if cached, ok := s.getCachedLyrics(ctx, legacyKey); ok {
		fillLookupHit(result, "legacy", cached)
		result["note"] = "Found in legacy key"
	} else {
		result["found"] = false
		if reason, ok := s.getNegativeCache(ctx, normalizedKey); ok {
			result["negative_cache"] = true
			result["negative_reason"] = reason
		} else if reason, ok := s.getNegativeCache(ctx, legacyKey); ok {
			result["negative_cache"] = true
			result["negative_cache_in"] = "legacy"
			result["negative_reason"] = reason
		}
	}

	respond(w, r).JSON(result)
}

func fillLookupHit(result map[string]interface{}, foundIn string, cached store.CachedLyrics) {
	result["found"] = true
	result["found_in"] = foundIn
	result["track_duration_ms"] = cached.TrackDurationMs
	result["score"] = cached.Score
	result["language"] = cached.Language
	result["isRTL"] = cached.IsRTL
	result["ttml_length"] = len(cached.TTML)
	result["ttml_preview"] = truncateString(cached.TTML, 200)
}

func (s *Server) cacheDebug(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "Provide 'key' parameter",
		})
		return
	}

	result := map[string]interface{}{"key": key}
	if cached, ok := s.getCachedLyrics(r.Context(), key); ok {
		result["found"] = true
		if cached.TTML == store.NoLyricsSentinel {
			result["type"] = "no_lyrics_sentinel"
		} else {
			result["type"] = "lyrics"
			result["track_duration_ms"] = cached.TrackDurationMs
			result["score"] = cached.Score
			result["language"] = cached.Language
			result["isRTL"] = cached.IsRTL
			result["ttml_length"] = len(cached.TTML)
			result["ttml_preview"] = truncateString(cached.TTML, 300)
		}
	} else if reason, ok := s.getNegativeCache(r.Context(), key); ok {
		result["found"] = true
		result["type"] = "negative_cache"
		result["reason"] = reason
	} else {
		result["found"] = false
	}

	respond(w, r).JSON(result)
}

func (s *Server) cacheKeys(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	prefix := r.URL.Query().Get("prefix")
	contains := r.URL.Query().Get("contains")
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			limit = parsed
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	infos, total, err := s.store.ListCacheKeys(r.Context(), prefix, contains, limit)
	if err != nil {
		respond(w, r).Error(http.StatusInternalServerError, map[string]interface{}{
			"error": "list keys failed: " + err.Error(),
		})
		return
	}

	keys := make([]map[string]interface{}, 0, len(infos))
	for _, ki := range infos {
		keys = append(keys, map[string]interface{}{
			"key":      ki.Key,
			"size":     ki.Size,
			"provider": ki.Provider,
		})
	}

	respond(w, r).JSON(map[string]interface{}{
		"total_keys":   total,
		"matched_keys": len(keys),
		"limit":        limit,
		"keys":         keys,
	})
}

func (s *Server) metadataStatsHandler(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ms, err := s.store.MetadataStats(r.Context())
	if err != nil {
		respond(w, r).Error(http.StatusInternalServerError, map[string]interface{}{
			"error": "metadata stats failed: " + err.Error(),
		})
		return
	}

	respond(w, r).JSON(map[string]interface{}{
		"totalEntries":      ms.TotalEntries,
		"withVideoIds":      ms.WithVideoIDs,
		"withISRC":          ms.WithISRC,
		"withRawAttributes": ms.WithRawAttrs,
		"videoMapEntries":   ms.VideoMapCount,
	})
}

func (s *Server) metadataSampleHandler(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	n := 10
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	if n > 100 {
		n = 100
	}

	metas, err := s.store.SampleMetadata(r.Context(), n)
	if err != nil {
		respond(w, r).Error(http.StatusInternalServerError, map[string]interface{}{
			"error": "sample failed: " + err.Error(),
		})
		return
	}

	entries := make([]map[string]interface{}, 0, len(metas))
	for _, m := range metas {
		entries = append(entries, s.enrichMetadata(r.Context(), m))
	}

	respond(w, r).JSON(map[string]interface{}{
		"requested": n,
		"returned":  len(entries),
		"entries":   entries,
	})
}

func (s *Server) testNotifications(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	notifiers := setupNotifiers()
	if len(notifiers) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "No notifiers configured. Please configure at least one notifier.",
		})
		return
	}

	subject := "Test: TTML Token Monitor"
	message := "Notification setup is working correctly."

	results := make(map[string]interface{})
	successCount := 0
	failCount := 0
	for _, n := range notifiers {
		notifierType := getNotifierTypeName(n)
		if err := n.Send(subject, message); err != nil {
			results[notifierType] = map[string]string{"status": "failed", "error": err.Error()}
			failCount++
			log.Errorf("%s %s failed: %v", logcolors.LogTestNotifications, notifierType, err)
		} else {
			results[notifierType] = map[string]string{"status": "success"}
			successCount++
			log.Infof("%s %s sent successfully", logcolors.LogTestNotifications, notifierType)
		}
	}

	if failCount > 0 {
		w.WriteHeader(http.StatusPartialContent)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "Test notifications sent",
		"total":      len(notifiers),
		"successful": successCount,
		"failed":     failCount,
		"results":    results,
	})
}

func truncateString(str string, maxLen int) string {
	if len(str) <= maxLen {
		return str
	}
	return str[:maxLen] + "..."
}
