package main

import (
	"encoding/json"
	"fmt"
	"lyrics-api-go/cache"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"
	"lyrics-api-go/services/ttml"
	"lyrics-api-go/stats"
	"net/http"
	"strings"
	"time"

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
	rateLimitType, _ := r.Context().Value(rateLimitTypeKey).(string)

	// Check cache first (normalized key, then legacy key for backwards compatibility)
	if cachedTTML, _, ok := getCachedLyrics(cacheKey); ok {
		stats.Get().RecordCacheHit()
		log.Infof("%s Found cached TTML", logcolors.LogCacheLyrics)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache-Status", "HIT")
		if rateLimitType != "" {
			w.Header().Set("X-RateLimit-Type", rateLimitType)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ttml": cachedTTML,
		})
		return
	}

	// Check legacy cache key (backwards compatibility - use /cache/migrate to convert)
	if legacyCacheKey != cacheKey {
		if cachedTTML, _, ok := getCachedLyrics(legacyCacheKey); ok {
			stats.Get().RecordCacheHit()
			log.Infof("%s Found cached TTML under legacy key", logcolors.LogCacheLyrics)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache-Status", "HIT")
			if rateLimitType != "" {
				w.Header().Set("X-RateLimit-Type", rateLimitType)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ttml": cachedTTML,
			})
			return
		}
	}

	// Check negative cache (known "no lyrics" responses)
	if reason, found := getNegativeCache(cacheKey); found {
		stats.Get().RecordNegativeCacheHit()
		log.Infof("%s Returning cached 'no lyrics' response for: %s", logcolors.LogCacheNegative, query)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache-Status", "NEGATIVE_HIT")
		if rateLimitType != "" {
			w.Header().Set("X-RateLimit-Type", rateLimitType)
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": reason,
		})
		return
	}

	// Check legacy negative cache (backwards compatibility)
	if legacyCacheKey != cacheKey {
		if reason, found := getNegativeCache(legacyCacheKey); found {
			stats.Get().RecordNegativeCacheHit()
			log.Infof("%s Returning cached 'no lyrics' (legacy key) for: %s", logcolors.LogCacheNegative, query)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache-Status", "NEGATIVE_HIT")
			if rateLimitType != "" {
				w.Header().Set("X-RateLimit-Type", rateLimitType)
			}
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": reason,
			})
			return
		}
	}

	// If in cache-only mode and no cache found, return 429
	if cacheOnlyMode {
		stats.Get().RecordCacheMiss()
		stats.Get().RecordRateLimit("exceeded")
		log.Warnf("%s Cache-only mode but no cache found for: %s", logcolors.LogCacheLyrics, query)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache-Status", "MISS")
		w.Header().Set("X-RateLimit-Type", "cached")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
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
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache-Status", "MISS")
			if rateLimitType != "" {
				w.Header().Set("X-RateLimit-Type", rateLimitType)
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": req.err.Error(),
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache-Status", "HIT")
		if rateLimitType != "" {
			w.Header().Set("X-RateLimit-Type", rateLimitType)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
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
			if cachedTTML, _, ok := getCachedLyrics(fallbackKey); ok {
				stats.Get().RecordStaleCacheHit()
				log.Warnf("%s Backend failed, serving stale cache from key: %s", logcolors.LogCacheLyrics, fallbackKey)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Cache-Status", "STALE")
				if rateLimitType != "" {
					w.Header().Set("X-RateLimit-Type", rateLimitType)
				}
				json.NewEncoder(w).Encode(map[string]interface{}{
					"ttml": cachedTTML,
				})
				return
			}
		}

		// Cache permanent "no lyrics" errors to avoid repeated API calls
		if shouldNegativeCache(err) {
			setNegativeCache(cacheKey, err.Error())
		}

		// No fallback found (or skipped due to duration), return the error
		stats.Get().RecordCacheMiss()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache-Status", "MISS")
		if rateLimitType != "" {
			w.Header().Set("X-RateLimit-Type", rateLimitType)
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	if ttmlString == "" {
		stats.Get().RecordCacheMiss()
		log.Warnf("No TTML found for: %s", query)
		// Cache this negative result to avoid repeated API calls
		setNegativeCache(cacheKey, "Lyrics not available for this track")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache-Status", "MISS")
		if rateLimitType != "" {
			w.Header().Set("X-RateLimit-Type", rateLimitType)
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Lyrics not available for this track",
		})
		return
	}

	stats.Get().RecordCacheMiss()
	log.Infof("%s Caching TTML for: %s (trackDuration: %dms)", logcolors.LogCacheLyrics, query, trackDurationMs)
	setCachedLyrics(cacheKey, ttmlString, trackDurationMs)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache-Status", "MISS")
	if rateLimitType != "" {
		w.Header().Set("X-RateLimit-Type", rateLimitType)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ttml":  ttmlString,
		"score": score,
	})
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
