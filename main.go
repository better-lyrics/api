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
	router.HandleFunc("/cache/clear", clearCache)
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
	if cachedTTML, ok := getCache(cacheKey); ok {
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

	ttmlString, score, err := ttml.FetchTTMLLyrics(songName, artistName, albumName, durationMs)

	req.err = err
	if err == nil {
		req.result = ttmlString
		req.score = score
	}

	if err != nil {
		log.Errorf("Error fetching TTML: %v", err)
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

	log.Infof("[Cache:Lyrics] Caching TTML for: %s", query)
	setCache(cacheKey, ttmlString)

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

	if conf.Configuration.TTMLBearerToken != "" {
		expirationDate, err := notifier.GetExpirationDate(conf.Configuration.TTMLBearerToken)
		if err != nil {
			tokenInfo = fmt.Sprintf("Error reading token expiration: %v", err)
			tokenDetails = map[string]interface{}{
				"error": err.Error(),
			}
		} else {
			now := time.Now()
			daysUntilExpiration := int(time.Until(expirationDate).Hours() / 24)
			warningDate := expirationDate.AddDate(0, 0, -7)

			tokenInfo = fmt.Sprintf(
				"Current date:         %s\n"+
					"Token expires:        %s\n"+
					"Days remaining:       %d days\n"+
					"Warning threshold:    7 days before expiration\n"+
					"First notification:   %s\n"+
					"Reminder frequency:   Daily until updated",
				now.Format("2006-01-02 15:04:05"),
				expirationDate.Format("2006-01-02 15:04:05"),
				daysUntilExpiration,
				warningDate.Format("2006-01-02 15:04:05"),
			)

			tokenDetails = map[string]interface{}{
				"current_date":           now.Format("2006-01-02 15:04:05"),
				"token_expires":          expirationDate.Format("2006-01-02 15:04:05"),
				"days_until_expiration":  daysUntilExpiration,
				"first_notification":     warningDate.Format("2006-01-02 15:04:05"),
				"notification_frequency": "Daily",
			}
		}
	} else {
		tokenInfo = "Status:               Not configured\n" +
			"TTML_BEARER_TOKEN:    Missing from .env file"
		tokenDetails = map[string]interface{}{
			"configured": false,
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
	if conf.Configuration.TTMLBearerToken == "" {
		log.Warn("[Token Monitor] TTML_BEARER_TOKEN not set, token monitoring disabled")
		return
	}

	notifiers := setupNotifiers()

	if len(notifiers) == 0 {
		log.Info("[Token Monitor] No notifiers configured, token monitoring disabled")
		log.Info("[Token Monitor] To enable notifications, configure at least one notifier (Email, Telegram, or Ntfy.sh)")
		return
	}

	log.Infof("[Token Monitor] Starting with %d notifier(s) configured", len(notifiers))

	monitor := notifier.NewTokenMonitor(notifier.MonitorConfig{
		BearerToken:      conf.Configuration.TTMLBearerToken,
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
