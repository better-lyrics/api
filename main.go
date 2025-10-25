package main

import (
	"encoding/json"
	"fmt"
	"lyrics-api-go/config"
	"lyrics-api-go/middleware"
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
	cache sync.Map
)

// Old Spotify configuration variables and types removed - now using TTML service

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

	router := mux.NewRouter()
	router.HandleFunc("/getLyrics", getLyrics)
	router.HandleFunc("/cache", getCacheDump)
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

// Old Spotify HTTP request functions removed

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

// Old Spotify OAuth functions removed

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

	// Fetch from TTML API
	lyrics, isRtlLanguage, language, timingType, rawTTML, err := ttml.FetchTTMLLyrics(songName, artistName, albumName)
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

// Old Spotify functions removed - now using ttml.FetchTTMLLyrics from services/ttml/

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
