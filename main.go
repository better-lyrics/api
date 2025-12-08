package main

import (
	"encoding/json"
	"lyrics-api-go/cache"
	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/middleware"
	"lyrics-api-go/services/notifier"
	"lyrics-api-go/stats"
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

var conf = config.Get()

var (
	persistentCache *cache.PersistentCache
	statsStore      *stats.Store
	inFlightReqs    sync.Map
)

func init() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)

	err := godotenv.Load()
	if err != nil {
		log.Warnf("%s Error loading .env file, using environment variables", logcolors.LogConfig)
	}
}

func main() {
	// Initialize persistent cache
	var err error
	cachePath := getEnvOrDefault("CACHE_DB_PATH", "./cache.db")
	backupPath := getEnvOrDefault("CACHE_BACKUP_PATH", "./backups")
	persistentCache, err = cache.NewPersistentCache(cachePath, backupPath, conf.FeatureFlags.CacheCompression)
	if err != nil {
		notifier.PublishServerStartupFailed("cache", err)
		log.Fatalf("Failed to initialize cache: %v", err)
	}
	defer persistentCache.Close()

	// Initialize stats store (separate from cache to preserve stats across cache clears)
	statsPath := getEnvOrDefault("STATS_DB_PATH", "./stats.db")
	statsStore, err = stats.NewStore(statsPath)
	if err != nil {
		notifier.PublishServerStartupFailed("stats_store", err)
		log.Fatalf("Failed to initialize stats store: %v", err)
	}
	defer statsStore.Close()

	// Load persisted stats from previous runs
	if err := statsStore.Load(); err != nil {
		log.Warnf("%s Failed to load persisted stats: %v", logcolors.LogStats, err)
	}

	// Start auto-saving stats every 5 minutes
	statsStore.StartAutoSave(5 * time.Minute)

	// Initialize alert handler for system notifications
	alertNotifiers := setupNotifiers()
	if len(alertNotifiers) > 0 {
		alertHandler := notifier.NewAlertHandler(notifier.AlertConfig{
			Notifiers:        alertNotifiers,
			CooldownDuration: 15 * time.Minute,
		})
		alertHandler.Start()
		log.Infof("%s Alert handler initialized with %d notifier(s)", logcolors.LogNotifier, len(alertNotifiers))
	}

	go startTokenMonitor()

	router := mux.NewRouter()
	router.HandleFunc("/getLyrics", getLyrics)
	router.HandleFunc("/cache", getCacheDump)
	router.HandleFunc("/cache/help", cacheHelp)
	router.HandleFunc("/cache/backup", backupCache)
	router.HandleFunc("/cache/backups", listBackups)
	router.HandleFunc("/cache/restore", restoreCache)
	router.HandleFunc("/cache/clear", clearCache)
	router.HandleFunc("/cache/migrate", migrateCache)
	router.HandleFunc("/cache/migrate/status", getMigrationStatus)
	router.HandleFunc("/cache/lookup", cacheLookup)
	router.HandleFunc("/cache/debug", cacheDebug)
	router.HandleFunc("/cache/keys", cacheKeys)
	router.HandleFunc("/health", getHealthStatus)
	router.HandleFunc("/stats", getStats)
	router.HandleFunc("/circuit-breaker", getCircuitBreakerStatus)
	router.HandleFunc("/circuit-breaker/reset", resetCircuitBreaker)
	router.HandleFunc("/circuit-breaker/simulate-failure", simulateCircuitBreakerFailure)
	router.HandleFunc("/test-notifications", testNotifications)
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"help": "Use /getLyrics to get the lyrics of a song. Provide the song name and artist name as query parameters. Example: /getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran&d=234",
			"parameters": map[string]string{
				"s, song, songName":     "Song name (required)",
				"a, artist, artistName": "Artist name (required)",
				"al, album, albumName":  "Album name (optional, improves matching)",
				"d, duration":           "Duration in seconds (optional, improves matching)",
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

	// API key middleware - if API_KEY_REQUIRED is true, require X-API-Key header
	// Public paths are accessible without API key
	publicPaths := []string{"/", "/health", "/cache/help"}
	apiKeyHandler := middleware.APIKeyMiddleware(
		conf.Configuration.APIKey,
		conf.Configuration.APIKeyRequired,
		publicPaths,
	)(corsHandler)

	handler := limitMiddleware(apiKeyHandler, limiter)

	// Get account count for startup notification
	accounts, _ := conf.GetTTMLAccounts()
	accountCount := len(accounts)

	// Log API key status
	if conf.Configuration.APIKeyRequired {
		if conf.Configuration.APIKey != "" {
			log.Infof("%s API key authentication enabled", logcolors.LogAPIKey)
		} else {
			log.Warnf("%s API key required but not configured!", logcolors.LogAPIKey)
		}
	} else if conf.Configuration.APIKey != "" {
		log.Infof("%s API key configured for rate limit bypass only", logcolors.LogAPIKey)
	}

	log.Infof("%s Listening on port %s", logcolors.LogServer, port)

	// Publish server started event
	notifier.PublishServerStarted(port, accountCount)

	log.Fatal(http.ListenAndServe(":"+port, handler))
}
