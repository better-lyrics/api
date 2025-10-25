package main

import (
	"encoding/json"
	"fmt"
	"lyrics-api-go/config"
	"lyrics-api-go/middleware"
	"lyrics-api-go/services/notifier"
	"lyrics-api-go/services/ttml"
	"lyrics-api-go/utils"
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
	cache         sync.Map
	inFlightReqs  sync.Map // Tracks in-flight requests to prevent cache stampede
)

type CacheEntry struct {
	Value      string
	Expiration int64
}

type CacheDump map[string]CacheEntry

type CacheDumpResponse struct {
	NumberOfKeys int
	SizeInKB     int
	Cache        CacheDump
}

// InFlightRequest tracks a request that is currently being processed
type InFlightRequest struct {
	wg     sync.WaitGroup
	result *LyricsResult
	err    error
}

// LyricsResult holds the result of a lyrics fetch
type LyricsResult struct {
	Lyrics        []ttml.Line
	IsRtlLanguage bool
	Language      string
	TimingType    string
}

func init() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel) // Set to InfoLevel (change to DebugLevel for detailed logs)

	err := godotenv.Load()
	if err != nil {
		log.Warn("Error loading .env file, using environment variables")
	}
}

func main() {
	// start goroutine to invalidate cache
	go invalidateCache()

	// start token expiration monitor if configured
	go startTokenMonitor()

	router := mux.NewRouter()
	router.HandleFunc("/getLyrics", getLyrics)
	router.HandleFunc("/cache", getCacheDump)
	router.HandleFunc("/test-notifications", testNotifications)
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"help": "Use /getLyrics to get the lyrics of a song. Provide the song name and artist name as query parameters. Example: /getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran",
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

	limiter := middleware.NewIPRateLimiter(rate.Limit(conf.Configuration.RateLimitPerSecond), conf.Configuration.RateLimitBurstLimit)

	// logging middleware

	loggedRouter := middleware.LoggingMiddleware(router)
	// chain cors middleware
	corsHandler := c.Handler(loggedRouter)

	//chain rate limiter
	handler := limitMiddleware(corsHandler, limiter)

	log.Infof("Server listening on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))

}

func isRTLLanguage(langCode string) bool {
	rtlLanguages := map[string]bool{
		"ar": true, // Arabic
		"fa": true, // Persian (Farsi)
		"he": true, // Hebrew
		"ur": true, // Urdu
		"ps": true, // Pashto
		"sd": true, // Sindhi
		"ug": true, // Uyghur
		"yi": true, // Yiddish
		"ku": true, // Kurdish (some dialects)
		"dv": true, // Divehi (Maldivian)
	}
	return rtlLanguages[langCode]
}

func getCache(key string) (string, bool) {
	entry, ok := cache.Load(key)
	if !ok {
		return "", false
	}
	cacheEntry := entry.(CacheEntry)
	if time.Now().UnixNano() > cacheEntry.Expiration {
		cache.Delete(key)
		return "", false
	}
	if conf.FeatureFlags.CacheCompression {
		// Decompress the value before returning
		decompressedValue, err := utils.DecompressString(cacheEntry.Value)
		if err != nil {
			log.Errorf("Error decompressing cache value: %v", err)
			return "", false
		}
		return decompressedValue, true
	} else {
		return cacheEntry.Value, true
	}
}

func setCache(key, value string, duration time.Duration) {
	var cacheEntry CacheEntry

	if conf.FeatureFlags.CacheCompression {
		compressedValue, err := utils.CompressString(value)
		if err != nil {
			log.Errorf("Error compressing cache value: %v", err)
			return
		}
		cacheEntry = CacheEntry{
			Value:      compressedValue,
			Expiration: time.Now().Add(duration).UnixNano(),
		}
	} else {
		cacheEntry = CacheEntry{
			Value:      value,
			Expiration: time.Now().Add(duration).UnixNano(),
		}
	}

	cache.Store(key, cacheEntry)
}

func getLyrics(w http.ResponseWriter, r *http.Request) {
	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")

	if songName == "" && artistName == "" {
		http.Error(w, "Song name or artist name not provided", http.StatusUnprocessableEntity)
		return
	}

	// Check cache first
	query := songName + " " + artistName + " " + albumName
	cacheKey := fmt.Sprintf("ttml_lyrics:%s", query)

	if cachedLyrics, ok := getCache(cacheKey); ok {
		log.Info("[Cache:Lyrics] Found cached TTML lyrics")
		w.Header().Set("Content-Type", "application/json")
		var cachedData map[string]interface{}
		json.Unmarshal([]byte(cachedLyrics), &cachedData)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         nil,
			"source":        "TTML",
			"lyrics":        cachedData["lyrics"],
			"isRtlLanguage": cachedData["isRtlLanguage"],
			"language":      cachedData["language"],
			"type":          cachedData["type"],
		})
		return
	}

	// Check if there's an in-flight request for this query
	inFlight, loaded := inFlightReqs.LoadOrStore(cacheKey, &InFlightRequest{})
	req := inFlight.(*InFlightRequest)

	if loaded {
		// Another request is already fetching this, wait for it
		log.Info("[Cache:Lyrics] Waiting for in-flight request to complete")
		req.wg.Wait()

		// Use the result from the in-flight request
		if req.err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":  req.err.Error(),
				"source": "TTML",
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         nil,
			"source":        "TTML",
			"lyrics":        req.result.Lyrics,
			"isRtlLanguage": req.result.IsRtlLanguage,
			"language":      req.result.Language,
			"type":          req.result.TimingType,
		})
		return
	}

	// This is the first request, fetch the data
	req.wg.Add(1)
	defer func() {
		req.wg.Done()
		// Clean up in-flight request after a short delay to allow waiting goroutines to get the result
		time.AfterFunc(1*time.Second, func() {
			inFlightReqs.Delete(cacheKey)
		})
	}()

	// Fetch from TTML API
	lyrics, isRtlLanguage, language, timingType, rawTTML, err := ttml.FetchTTMLLyrics(songName, artistName, albumName)

	// Store result in in-flight request
	req.err = err
	if err == nil {
		req.result = &LyricsResult{
			Lyrics:        lyrics,
			IsRtlLanguage: isRtlLanguage,
			Language:      language,
			TimingType:    timingType,
		}
	}
	if err != nil {
		log.Errorf("Error fetching TTML lyrics: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)

		response := map[string]interface{}{
			"error":  err.Error(),
			"source": "TTML",
		}

		// Include raw TTML for debugging if parsing failed
		if rawTTML != "" {
			response["rawTTML"] = rawTTML
		}

		json.NewEncoder(w).Encode(response)
		return
	}

	if lyrics == nil || len(lyrics) == 0 {
		log.Warnf("No lyrics found for: %s", query)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":  "Lyrics not available for this track",
			"source": "TTML",
		})
		return
	}

	// Cache the lyrics
	log.Infof("[Cache:Lyrics] Caching TTML lyrics for: %s", query)
	cacheValue, err := json.Marshal(map[string]interface{}{
		"lyrics":        lyrics,
		"isRtlLanguage": isRtlLanguage,
		"language":      language,
		"type":          timingType,
	})
	if err != nil {
		log.Errorf("[Cache:Lyrics] Failed to marshal cache value: %v", err)
	} else {
		setCache(cacheKey, string(cacheValue), time.Duration(conf.Configuration.LyricsCacheTTLInSeconds)*time.Second)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":         nil,
		"source":        "TTML",
		"lyrics":        lyrics,
		"isRtlLanguage": isRtlLanguage,
		"language":      language,
		"type":          timingType,
	})
}

func testNotifications(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Set up notifiers
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

	// Get token expiration details
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
				"current_date":          now.Format("2006-01-02 15:04:05"),
				"token_expires":         expirationDate.Format("2006-01-02 15:04:05"),
				"days_until_expiration": daysUntilExpiration,
				"first_notification":    warningDate.Format("2006-01-02 15:04:05"),
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

	// Send test notification
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
		"message":      "Test notifications sent",
		"total":        len(notifiers),
		"successful":   successCount,
		"failed":       failCount,
		"results":      results,
		"token_info":   tokenDetails,
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
	// Check if the request is authorized by checking the access token
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	cacheDump := CacheDump{}
	cacheDumpResponse := CacheDumpResponse{}
	cache.Range(func(key, value interface{}) bool {
		if key == "accessToken" {
			return true
		}
		cacheDump[key.(string)] = value.(CacheEntry)
		return true
	})
	cacheDumpResponse.Cache = cacheDump
	cacheDumpResponse.NumberOfKeys = len(cacheDump)
	size := 0
	for key, value := range cacheDump {
		size += len(key) + len(value.Value) + 8
	}
	cacheDumpResponse.SizeInKB = size / 1024

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cacheDumpResponse)
}

func limitMiddleware(next http.Handler, limiter *middleware.IPRateLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limiter := limiter.GetLimiter(r.RemoteAddr)
		if !limiter.Allow() {
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// goroutine to invalidate cache every 1 hour based on expiration times and delete keys
func invalidateCache() {
	log.Infof("[Cache:Invalidation] Starting cache invalidation goroutine")
	for {
		time.Sleep(time.Duration(conf.Configuration.CacheInvalidationIntervalInSeconds) * time.Second)
		cache.Range(func(key, value interface{}) bool {
			cacheEntry := value.(CacheEntry)
			if time.Now().UnixNano() > cacheEntry.Expiration {
				cache.Delete(key)
				fmt.Printf("\033[31m[Cache:Invalidation] Deleted key: %s\033[0m\n", key)
			}
			return true
		})
	}
}

// startTokenMonitor starts the token expiration monitor if notifiers are configured
func startTokenMonitor() {
	// Check if bearer token is configured
	if conf.Configuration.TTMLBearerToken == "" {
		log.Warn("[Token Monitor] TTML_BEARER_TOKEN not set, token monitoring disabled")
		return
	}

	// Set up notifiers
	notifiers := setupNotifiers()

	if len(notifiers) == 0 {
		log.Info("[Token Monitor] No notifiers configured, token monitoring disabled")
		log.Info("[Token Monitor] To enable notifications, configure at least one notifier (Email, Telegram, or Ntfy.sh)")
		return
	}

	log.Infof("[Token Monitor] Starting with %d notifier(s) configured", len(notifiers))

	// Create and run monitor
	monitor := notifier.NewTokenMonitor(notifier.MonitorConfig{
		BearerToken:      conf.Configuration.TTMLBearerToken,
		WarningThreshold: 7,  // Start warning 7 days before expiration
		ReminderInterval: 24, // Remind every 24 hours
		StateFile:        "/tmp/ttml-pager.state",
		Notifiers:        notifiers,
	})

	// Run monitor (checks every 6 hours)
	monitor.Run(6 * time.Hour)
}

// setupNotifiers creates notifier instances based on environment variables
func setupNotifiers() []notifier.Notifier {
	var notifiers []notifier.Notifier

	// Email notifier
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

	// Telegram notifier
	if botToken := os.Getenv("NOTIFIER_TELEGRAM_BOT_TOKEN"); botToken != "" {
		telegramNotifier := &notifier.TelegramNotifier{
			BotToken: botToken,
			ChatID:   os.Getenv("NOTIFIER_TELEGRAM_CHAT_ID"),
		}
		notifiers = append(notifiers, telegramNotifier)
		log.Info("[Token Monitor] Telegram notifier enabled")
	}

	// Ntfy.sh notifier
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

// getEnvOrDefault returns environment variable value or default if not set
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
