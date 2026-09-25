package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"time"

	"lyrics-api-go/config"
	"lyrics-api-go/internal/openapi"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/middleware"
	"lyrics-api-go/stats"

	"github.com/gorilla/mux"
	"github.com/klauspost/compress/gzhttp"
	"github.com/rs/cors"
	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

func (s *Server) setupRoutes(router *mux.Router) {
	router.HandleFunc("/getLyrics", s.getLyrics)
	router.HandleFunc("/revalidate", s.revalidateHandler)
	router.HandleFunc("/override", s.overrideHandler)

	router.HandleFunc("/ttml/getLyrics", s.getLyricsWithProvider("ttml"))
	router.HandleFunc("/kugou/getLyrics", s.getLyricsWithProvider("kugou"))
	router.HandleFunc("/qq/getLyrics", s.getLyricsWithProvider("qq"))
	router.HandleFunc("/legacy/getLyrics", s.getLyricsWithProvider("legacy"))

	router.HandleFunc("/video-map", s.videoMapImportHandler).Methods("POST")
	router.HandleFunc("/metadata", s.metadataLookupHandler).Methods("GET")
	router.HandleFunc("/metadata/stats", s.metadataStatsHandler).Methods("GET")
	router.HandleFunc("/metadata/sample", s.metadataSampleHandler).Methods("GET")

	router.HandleFunc("/cache", s.getCacheDump)
	router.HandleFunc("/cache/help", s.cacheHelp)
	router.HandleFunc("/cache/backup", s.retiredEndpoint)
	router.HandleFunc("/cache/backups", s.retiredEndpoint)
	router.HandleFunc("/cache/restore", s.retiredEndpoint)
	router.HandleFunc("/cache/clear", s.clearCache)
	router.HandleFunc("/cache/clear/{provider}", s.clearProviderCache)
	router.HandleFunc("/cache/migrate", s.retiredEndpoint)
	router.HandleFunc("/cache/migrate/status", s.retiredEndpoint)
	router.HandleFunc("/cache/lookup", s.cacheLookup)
	router.HandleFunc("/cache/debug", s.cacheDebug)
	router.HandleFunc("/cache/keys", s.cacheKeys)
	router.HandleFunc("/cache/dump", s.retiredEndpoint)

	router.HandleFunc("/health", s.getHealthStatus)
	router.HandleFunc("/health/mut", s.handleMUTHealth)
	router.HandleFunc("/stats", s.getStats)

	router.HandleFunc("/circuit-breaker", s.getCircuitBreakerStatus)
	router.HandleFunc("/circuit-breaker/reset", s.resetCircuitBreaker)
	router.HandleFunc("/circuit-breaker/simulate-failure", s.simulateCircuitBreakerFailure)

	router.HandleFunc("/test-notifications", s.testNotifications)

	router.HandleFunc("/openapi.json", openapi.Handler).Methods("GET")

	router.Handle("/debug/pprof/profile", s.adminOnly(pprof.Profile))
	router.Handle("/debug/pprof/trace", s.adminOnly(pprof.Trace))
	router.Handle("/debug/pprof/cmdline", s.adminOnly(pprof.Cmdline))
	router.Handle("/debug/pprof/symbol", s.adminOnly(pprof.Symbol))
	router.PathPrefix("/debug/pprof/").Handler(s.adminOnly(pprof.Index))

	router.HandleFunc("/", s.helpHandler)
}

// Handler builds the full middleware chain. Runtime order (outermost first):
// gzip -> rate-limit -> api-key -> CORS -> logging -> router. gorilla/mux and rs/cors
// defaults are part of the frozen contract.
func (s *Server) Handler() http.Handler {
	router := mux.NewRouter()
	s.setupRoutes(router)

	c := cors.New(cors.Options{
		AllowedOrigins: []string{
			"https://music.youtube.com",
			"http://localhost:*",
			"https://lyrics-api-docs.boidu.dev",
			"https://braccato.boidu.dev",
			"https://composer.boidu.dev",
			"https://composer.betterlyrics.org",
			"https://docs.betterlyrics.org",
		},
		AllowCredentials: true,
	})

	limiter := middleware.NewIPRateLimiter(
		rate.Limit(s.cfg.Configuration.RateLimitPerSecond),
		s.cfg.Configuration.RateLimitBurstLimit,
		rate.Limit(s.cfg.Configuration.CachedRateLimitPerSecond),
		s.cfg.Configuration.CachedRateLimitBurstLimit,
	)
	limiter.StartCleanup(5*time.Minute, 10*time.Minute)

	loggedRouter := middleware.LoggingMiddleware(router)
	corsHandler := c.Handler(loggedRouter)
	apiKeyHandler := middleware.APIKeyMiddleware(
		s.cfg.Configuration.APIKey,
		s.cfg.Configuration.APIKeyRequired,
		config.APIKeyProtectedPaths,
		apiKeyRequiredForFreshKey,
		apiKeyAuthenticatedKey,
		apiKeyInvalidKey,
	)(corsHandler)

	return gzipResponses(s.limitMiddleware(apiKeyHandler, limiter))
}

// Cloudflare re-encodes for clients, so gzip here only shrinks origin egress.
var gzipResponses = func() func(http.Handler) http.HandlerFunc {
	wrap, err := gzhttp.NewWrapper(gzhttp.EnableZstd(false))
	if err != nil {
		panic(err)
	}
	return wrap
}()

func (s *Server) limitMiddleware(next http.Handler, limiter *middleware.IPRateLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "" && s.cfg.Configuration.APIKey != "" && apiKey == s.cfg.Configuration.APIKey {
			w.Header().Set("X-RateLimit-Bypass", "true")
			ctx := context.WithValue(r.Context(), rateLimitTypeKey, "bypass")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		limiters := limiter.GetLimiter(r.RemoteAddr)

		if limiters.Normal.Allow() {
			stats.Get().RecordRateLimit("normal")
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.GetNormalLimit()))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", limiters.GetNormalTokens()))
			w.Header().Set("X-RateLimit-Type", "normal")
			ctx := context.WithValue(r.Context(), rateLimitTypeKey, "normal")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		if limiters.Cached.Allow() {
			stats.Get().RecordRateLimit("cached")
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.GetCachedLimit()))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", limiters.GetCachedTokens()))
			w.Header().Set("X-RateLimit-Type", "cached")
			log.Debugf("%s IP %s exceeded normal tier, using cached tier", logcolors.LogRateLimit, r.RemoteAddr)
			ctx := context.WithValue(r.Context(), cacheOnlyModeKey, true)
			ctx = context.WithValue(ctx, rateLimitTypeKey, "cached")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		stats.Get().RecordRateLimit("exceeded")
		log.Warnf("%s IP %s exceeded both rate limit tiers", logcolors.LogRateLimit, r.RemoteAddr)
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.GetCachedLimit()))
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Type", "exceeded")
		w.Header().Set("Retry-After", "1")
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
	})
}
