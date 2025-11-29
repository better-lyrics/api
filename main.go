package main

import (
	"context"
	"encoding/json"
	"fmt"
	"lyrics-api-go/cache"
	"lyrics-api-go/config"
	"lyrics-api-go/middleware"
	"lyrics-api-go/services/notifier"
	"lyrics-api-go/services/ttml"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
	log "github.com/sirupsen/logrus"
)

type contextKey string

const (
	cacheOnlyModeKey contextKey = "cacheOnlyMode"
	rateLimitTypeKey contextKey = "rateLimitType"
)

var conf = config.Get()

var (
	persistentCache *cache.PersistentCache
	inFlightReqs    sync.Map
)

type CacheDump map[string]cache.CacheEntry

type CacheDumpResponse struct {
	NumberOfKeys int
	SizeInKB     int
	Cache        CacheDump
}

type InFlightRequest struct {
	wg     sync.WaitGroup
	result string
	score  float64
	err    error
}

// CachedLyrics stores TTML with track metadata for duration validation
type CachedLyrics struct {
	TTML            string `json:"ttml"`
	TrackDurationMs int    `json:"trackDurationMs"`
}

// NegativeCacheEntry stores info about failed lyrics lookups
type NegativeCacheEntry struct {
	Reason    string `json:"reason"`
	Timestamp int64  `json:"timestamp"`
}

func init() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)

	err := godotenv.Load()
	if err != nil {
		log.Warn("Error loading .env file, using environment variables")
	}
}

func main() {
	// Initialize persistent cache
	var err error
	cachePath := getEnvOrDefault("CACHE_DB_PATH", "./cache.db")
	backupPath := getEnvOrDefault("CACHE_BACKUP_PATH", "./backups")
	persistentCache, err = cache.NewPersistentCache(cachePath, backupPath, conf.FeatureFlags.CacheCompression)
	if err != nil {
		log.Fatalf("Failed to initialize cache: %v", err)
	}
	defer persistentCache.Close()

	go startTokenMonitor()

	router := mux.NewRouter()
	router.HandleFunc("/getLyrics", getLyrics)
	router.HandleFunc("/cache", getCacheDump)
	router.HandleFunc("/cache/backup", backupCache)
	router.HandleFunc("/cache/backups", listBackups)
	router.HandleFunc("/cache/restore", restoreCache)
	router.HandleFunc("/cache/clear", clearCache)
	router.HandleFunc("/health", getHealthStatus)
	router.HandleFunc("/circuit-breaker", getCircuitBreakerStatus)
	router.HandleFunc("/circuit-breaker/reset", resetCircuitBreaker)
	router.HandleFunc("/circuit-breaker/simulate-failure", simulateCircuitBreakerFailure)
	router.HandleFunc("/test-notifications", testNotifications)
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"help": "Use /getLyrics to get the lyrics of a song. Provide the song name and artist name as query parameters. Example: /getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran&d=234",
			"parameters": map[string]string{
				"s, song, songName":       "Song name (required)",
				"a, artist, artistName":   "Artist name (required)",
				"al, album, albumName":    "Album name (optional, improves matching)",
				"d, duration":             "Duration in seconds (optional, improves matching)",
			},
			"notes": "The API uses a weighted scoring system to find the best match based on song name, artist, album, and duration. Providing more parameters improves accuracy.",
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"https://music.youtube.com", "http://localhost:3000"},
		AllowCredentials: true,
	})

	limiter := middleware.NewIPRateLimiter(
		rate.Limit(conf.Configuration.RateLimitPerSecond),
		conf.Configuration.RateLimitBurstLimit,
		rate.Limit(conf.Configuration.CachedRateLimitPerSecond),
		conf.Configuration.CachedRateLimitBurstLimit,
	)

	loggedRouter := middleware.LoggingMiddleware(router)
	corsHandler := c.Handler(loggedRouter)
	handler := limitMiddleware(corsHandler, limiter)

	log.Infof("Server listening on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

func getCache(key string) (string, bool) {
	return persistentCache.Get(key)
}

func setCache(key, value string) {
	if err := persistentCache.Set(key, value); err != nil {
		log.Errorf("Error setting cache value: %v", err)
	}
}

// getCachedLyrics retrieves and parses cached lyrics, returns ttml, trackDurationMs, found
// Handles both old format (plain TTML string) and new format (JSON with duration)
func getCachedLyrics(key string) (string, int, bool) {
	cached, ok := persistentCache.Get(key)
	if !ok {
		return "", 0, false
	}

	// Try to parse as new JSON format
	var cachedLyrics CachedLyrics
	if err := json.Unmarshal([]byte(cached), &cachedLyrics); err == nil && cachedLyrics.TTML != "" {
		return cachedLyrics.TTML, cachedLyrics.TrackDurationMs, true
	}

	// Fallback to old format (plain TTML string) - no duration info available
	return cached, 0, true
}

// setCachedLyrics stores lyrics with track duration for validation
func setCachedLyrics(key, ttml string, trackDurationMs int) {
	cachedLyrics := CachedLyrics{
		TTML:            ttml,
		TrackDurationMs: trackDurationMs,
	}
	data, err := json.Marshal(cachedLyrics)
	if err != nil {
		log.Errorf("Error marshaling cached lyrics: %v", err)
		return
	}
	if err := persistentCache.Set(key, string(data)); err != nil {
		log.Errorf("Error setting cache value: %v", err)
	}
}

// getNegativeCache checks if a request is in the negative cache (no lyrics available)
// Returns the reason and true if found and not expired, empty string and false otherwise
func getNegativeCache(key string) (string, bool) {
	negativeKey := "no_lyrics:" + key
	cached, ok := persistentCache.Get(negativeKey)
	if !ok {
		return "", false
	}

	var entry NegativeCacheEntry
	if err := json.Unmarshal([]byte(cached), &entry); err != nil {
		return "", false
	}

	// Check if entry has expired
	ttlDays := conf.Configuration.NegativeCacheTTLInDays
	expirationTime := entry.Timestamp + int64(ttlDays*24*60*60)
	if time.Now().Unix() > expirationTime {
		// Expired - delete and return not found
		persistentCache.Delete(negativeKey)
		return "", false
	}

	return entry.Reason, true
}

// setNegativeCache stores a failed lookup in the negative cache
func setNegativeCache(key, reason string) {
	negativeKey := "no_lyrics:" + key
	entry := NegativeCacheEntry{
		Reason:    reason,
		Timestamp: time.Now().Unix(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		log.Errorf("Error marshaling negative cache entry: %v", err)
		return
	}
	if err := persistentCache.Set(negativeKey, string(data)); err != nil {
		log.Errorf("Error setting negative cache: %v", err)
	}
	log.Infof("[Cache:Negative] Cached 'no lyrics' for key: %s (reason: %s)", key, reason)
}

// shouldNegativeCache determines if an error should be stored in negative cache
// Only permanent "no lyrics" type errors should be cached, not transient failures
func shouldNegativeCache(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Permanent errors - cache these
	permanentErrors := []string{
		"no track found",
		"no tracks found within",
		"TTML content is empty",
	}
	for _, pe := range permanentErrors {
		if strings.Contains(errStr, pe) {
			return true
		}
	}
	return false
}

func getLyrics(w http.ResponseWriter, r *http.Request) {
	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")

	if songName == "" && artistName == "" {
		http.Error(w, "Song name or artist name not provided", http.StatusUnprocessableEntity)
		return
	}

	query := songName + " " + artistName + " " + albumName
	// Include duration in cache key if provided
	if durationStr != "" {
		query = query + " " + durationStr + "s"
	}
	cacheKey := fmt.Sprintf("ttml_lyrics:%s", query)

	// Check if we're in cache-only mode (rate limit tier 2)
	cacheOnlyMode, _ := r.Context().Value(cacheOnlyModeKey).(bool)
	rateLimitType, _ := r.Context().Value(rateLimitTypeKey).(string)

	// Check cache first
	if cachedTTML, _, ok := getCachedLyrics(cacheKey); ok {
		log.Info("[Cache:Lyrics] Found cached TTML")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache-Status", "HIT")
		if rateLimitType != "" {
			w.Header().Set("X-RateLimit-Type", rateLimitType)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ttml": cachedTTML,
			// Score not available for cached responses
		})
		return
	}

	// Check negative cache (known "no lyrics" responses)
	if reason, found := getNegativeCache(cacheKey); found {
		log.Infof("[Cache:Negative] Returning cached 'no lyrics' response for: %s", query)
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

	// If in cache-only mode and no cache found, return 429
	if cacheOnlyMode {
		log.Warnf("[Cache:Lyrics] Cache-only mode but no cache found for: %s", query)
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
		log.Info("[Cache:Lyrics] Waiting for in-flight request to complete")
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
		log.Errorf("Error fetching TTML: %v", err)

		// Try fallback cache keys before returning error
		fallbackKeys := buildFallbackCacheKeys(songName, artistName, albumName, durationStr, cacheKey)
		for _, fallbackKey := range fallbackKeys {
			if cachedTTML, _, ok := getCachedLyrics(fallbackKey); ok {
				log.Warnf("[Cache:Lyrics] Backend failed, serving stale cache from key: %s", fallbackKey)
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

	log.Infof("[Cache:Lyrics] Caching TTML for: %s (trackDuration: %dms)", query, trackDurationMs)
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
			log.Errorf("[Test Notifications] %s failed: %v", notifierType, err)
		} else {
			results[notifierType] = map[string]string{
				"status": "success",
			}
			successCount++
			log.Infof("[Test Notifications] %s sent successfully", notifierType)
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

func getNotifierTypeName(n notifier.Notifier) string {
	switch n.(type) {
	case *notifier.EmailNotifier:
		return "email"
	case *notifier.TelegramNotifier:
		return "telegram"
	case *notifier.NtfyNotifier:
		return "ntfy"
	default:
		return "unknown"
	}
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

	cacheDumpResponse := CacheDumpResponse{
		NumberOfKeys: numKeys,
		SizeInKB:     sizeInKB,
		Cache:        cacheDump,
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
		log.Errorf("[Cache:Backup] Failed to create backup: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Failed to create backup: %v", err),
		})
		return
	}

	log.Infof("[Cache:Backup] Backup created successfully at: %s", backupPath)
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
		log.Errorf("[Cache:Clear] Failed to backup and clear cache: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Failed to backup and clear cache: %v", err),
		})
		return
	}

	log.Infof("[Cache:Clear] Cache cleared successfully, backup at: %s", backupPath)
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
		log.Errorf("[Cache:Backups] Failed to list backups: %v", err)
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
		log.Errorf("[Cache:Restore] Failed to restore from backup %s: %v", backupFileName, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("Failed to restore from backup: %v", err),
		})
		return
	}

	// Get new cache stats after restore
	numKeys, sizeKB := persistentCache.Stats()

	log.Infof("[Cache:Restore] Cache restored from backup: %s", backupFileName)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":        "Cache restored successfully",
		"restored_from":  backupFileName,
		"keys_restored":  numKeys,
		"size_kb":        sizeKB,
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
		"status":           "ok",
		"accounts":         accountCount,
		"circuit_breaker":  cbState,
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

func limitMiddleware(next http.Handler, limiter *middleware.IPRateLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limiters := limiter.GetLimiter(r.RemoteAddr)

		// Try normal tier first
		if limiters.Normal.Allow() {
			// Normal tier allows this request
			remainingNormal := limiters.GetNormalTokens()
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.GetNormalLimit()))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remainingNormal))
			w.Header().Set("X-RateLimit-Type", "normal")
			ctx := context.WithValue(r.Context(), rateLimitTypeKey, "normal")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Normal tier exceeded, try cached tier
		if limiters.Cached.Allow() {
			// Cached tier allows, but only for cached responses
			remainingCached := limiters.GetCachedTokens()
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.GetCachedLimit()))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remainingCached))
			w.Header().Set("X-RateLimit-Type", "cached")
			log.Debugf("[RateLimit] IP %s exceeded normal tier, using cached tier", r.RemoteAddr)
			ctx := context.WithValue(r.Context(), cacheOnlyModeKey, true)
			ctx = context.WithValue(ctx, rateLimitTypeKey, "cached")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Both tiers exceeded
		log.Warnf("[RateLimit] IP %s exceeded both rate limit tiers", r.RemoteAddr)
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.GetCachedLimit()))
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Type", "exceeded")
		w.Header().Set("Retry-After", "1")
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
	})
}

func startTokenMonitor() {
	accounts, err := conf.GetTTMLAccounts()
	if err != nil {
		log.Warnf("[Token Monitor] Failed to get TTML accounts: %v", err)
		return
	}

	if len(accounts) == 0 {
		log.Warn("[Token Monitor] No TTML accounts configured, token monitoring disabled")
		return
	}

	notifiers := setupNotifiers()

	if len(notifiers) == 0 {
		log.Info("[Token Monitor] No notifiers configured, token monitoring disabled")
		log.Info("[Token Monitor] To enable notifications, configure at least one notifier (Email, Telegram, or Ntfy.sh)")
		return
	}

	// Convert accounts to TokenInfo for the monitor
	tokens := make([]notifier.TokenInfo, len(accounts))
	for i, acc := range accounts {
		tokens[i] = notifier.TokenInfo{
			Name:        acc.Name,
			BearerToken: acc.BearerToken,
		}
	}

	log.Infof("[Token Monitor] Starting with %d account(s) and %d notifier(s) configured", len(tokens), len(notifiers))

	monitor := notifier.NewTokenMonitor(notifier.MonitorConfig{
		Tokens:           tokens,
		WarningThreshold: 7,
		ReminderInterval: 24,
		StateFile:        "/tmp/ttml-pager.state",
		Notifiers:        notifiers,
	})

	monitor.Run(6 * time.Hour)
}

func setupNotifiers() []notifier.Notifier {
	var notifiers []notifier.Notifier

	if smtpHost := os.Getenv("NOTIFIER_SMTP_HOST"); smtpHost != "" {
		emailNotifier := &notifier.EmailNotifier{
			SMTPHost:     smtpHost,
			SMTPPort:     getEnvOrDefault("NOTIFIER_SMTP_PORT", "587"),
			SMTPUsername: os.Getenv("NOTIFIER_SMTP_USERNAME"),
			SMTPPassword: os.Getenv("NOTIFIER_SMTP_PASSWORD"),
			FromEmail:    os.Getenv("NOTIFIER_FROM_EMAIL"),
			ToEmail:      os.Getenv("NOTIFIER_TO_EMAIL"),
		}
		notifiers = append(notifiers, emailNotifier)
		log.Info("[Token Monitor] Email notifier enabled")
	}

	if botToken := os.Getenv("NOTIFIER_TELEGRAM_BOT_TOKEN"); botToken != "" {
		telegramNotifier := &notifier.TelegramNotifier{
			BotToken: botToken,
			ChatID:   os.Getenv("NOTIFIER_TELEGRAM_CHAT_ID"),
		}
		notifiers = append(notifiers, telegramNotifier)
		log.Info("[Token Monitor] Telegram notifier enabled")
	}

	if topic := os.Getenv("NOTIFIER_NTFY_TOPIC"); topic != "" {
		ntfyNotifier := &notifier.NtfyNotifier{
			Topic:  topic,
			Server: getEnvOrDefault("NOTIFIER_NTFY_SERVER", "https://ntfy.sh"),
		}
		notifiers = append(notifiers, ntfyNotifier)
		log.Info("[Token Monitor] Ntfy.sh notifier enabled")
	}

	return notifiers
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// buildFallbackCacheKeys returns a list of cache keys to try when the backend fails.
// Keys are ordered from most specific to least specific, excluding the original key.
// When duration is provided, fallback keys still include duration to maintain strict matching.
func buildFallbackCacheKeys(songName, artistName, albumName, durationStr, originalKey string) []string {
	var keys []string

	// Fallback: without album (if album was provided)
	// Duration is preserved if it was in the original request
	// Key format must match original: "songName + artistName + " " + albumName" where albumName is empty
	if albumName != "" {
		query := songName + " " + artistName + " " // trailing space to match empty album format
		if durationStr != "" {
			query = query + " " + durationStr + "s"
		}
		keyWithoutAlbum := fmt.Sprintf("ttml_lyrics:%s", query)
		if keyWithoutAlbum != originalKey {
			keys = append(keys, keyWithoutAlbum)
		}
	}

	return keys
}
