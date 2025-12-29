package middleware

import (
	"context"
	"lyrics-api-go/logcolors"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"
)

// APIKeyMiddleware creates middleware that handles API key authentication for protected paths.
// When API key is required for a protected path but not provided, it sets a context flag
// instead of blocking immediately - this allows handlers to serve cached responses without API key.
//
// Behavior:
// - If required is false, all requests pass through
// - If required is true but apiKey is empty, logs warning and allows (misconfiguration)
// - If path is protected and no API key provided: sets requiredContextKey=true and proceeds (cache-first)
// - If path is protected and wrong API key provided: sets invalidContextKey=true and proceeds (cache-first, but marked invalid)
// - If path is protected and valid API key provided: sets authenticatedContextKey=true and proceeds
func APIKeyMiddleware(apiKey string, required bool, protectedPaths []string, requiredContextKey interface{}, authenticatedContextKey interface{}, invalidContextKey interface{}) func(http.Handler) http.Handler {
	// Build a map for O(1) lookup of protected paths
	protectedPathMap := make(map[string]bool)
	for _, path := range protectedPaths {
		protectedPathMap[path] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If API key auth is not required, allow all requests
			if !required {
				next.ServeHTTP(w, r)
				return
			}

			// If required but no API key configured, warn and allow (misconfiguration)
			if apiKey == "" {
				log.Warnf("%s API key required but not configured, allowing request", logcolors.LogAPIKey)
				next.ServeHTTP(w, r)
				return
			}

			// Check if path is protected (exact match or prefix match for paths ending with *)
			path := r.URL.Path
			isProtected := protectedPathMap[path]
			if !isProtected {
				for protectedPath := range protectedPathMap {
					if strings.HasSuffix(protectedPath, "*") {
						prefix := strings.TrimSuffix(protectedPath, "*")
						if strings.HasPrefix(path, prefix) {
							isProtected = true
							break
						}
					}
				}
			}

			// If path is not protected, allow without API key
			if !isProtected {
				next.ServeHTTP(w, r)
				return
			}

			// Path is protected, check X-API-Key header
			providedKey := r.Header.Get("X-API-Key")

			// No API key provided - set context flag and proceed (handler will check cache first)
			if providedKey == "" {
				log.Debugf("%s No API key from %s for %s, setting cache-first mode", logcolors.LogAPIKey, r.RemoteAddr, path)
				ctx := context.WithValue(r.Context(), requiredContextKey, true)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// API key provided but invalid - set context flag and proceed (handler will check cache first)
			if providedKey != apiKey {
				log.Debugf("%s Invalid API key from %s for %s, setting cache-first mode", logcolors.LogAPIKey, r.RemoteAddr, path)
				ctx := context.WithValue(r.Context(), invalidContextKey, true)
				ctx = context.WithValue(ctx, requiredContextKey, true)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Valid API key, proceed with authenticated flag
			ctx := context.WithValue(r.Context(), authenticatedContextKey, true)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
