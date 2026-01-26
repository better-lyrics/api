package main

import (
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
	setupRoutes(router)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"https://music.youtube.com", "http://localhost:3000","http://localhost:4321","https://lyrics-api-docs.boidu.dev"},
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

	// API key middleware - if API_KEY_REQUIRED is true, protected paths require API key
	// for cache misses. Cache hits are served without API key (cache-first approach).
	apiKeyHandler := middleware.APIKeyMiddleware(
		conf.Configuration.APIKey,
		conf.Configuration.APIKeyRequired,
		config.APIKeyProtectedPaths,
		apiKeyRequiredForFreshKey,
		apiKeyAuthenticatedKey,
		apiKeyInvalidKey,
	)(corsHandler)

	handler := limitMiddleware(apiKeyHandler, limiter)

	// Get account info for startup notification
	activeAccounts, _ := conf.GetTTMLAccounts()
	allAccounts, _ := conf.GetAllTTMLAccounts()

	// Collect out-of-service account names
	var outOfServiceNames []string
	for _, acc := range allAccounts {
		if acc.OutOfService {
			outOfServiceNames = append(outOfServiceNames, acc.Name)
		}
	}

	// Log API key status
	if conf.Configuration.APIKeyRequired {
		if conf.Configuration.APIKey != "" {
			log.Infof("%s API key required for cache misses on paths: %v", logcolors.LogAPIKey, config.APIKeyProtectedPaths)
		} else {
			log.Warnf("%s API key required but not configured!", logcolors.LogAPIKey)
		}
	} else if conf.Configuration.APIKey != "" {
		log.Infof("%s API key configured for rate limit bypass only", logcolors.LogAPIKey)
	}

	// Log cache-only mode status
	if conf.FeatureFlags.CacheOnlyMode {
		log.Warnf("%s FF_CACHE_ONLY_MODE is enabled - all upstream requests are disabled, serving from cache only", logcolors.LogWarning)
	}

	log.Infof("%s Listening on port %s", logcolors.LogServer, port)

	// Publish server started event
	notifier.PublishServerStarted(port, len(activeAccounts), outOfServiceNames)

	log.Fatal(http.ListenAndServe(":"+port, handler))
}
