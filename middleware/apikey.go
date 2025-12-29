package middleware

import (
	"lyrics-api-go/logcolors"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"
)

// APIKeyMiddleware creates middleware that requires X-API-Key header for protected paths.
// If required is false, all requests pass through without authentication.
// If required is true but apiKey is empty, logs a warning and allows all requests.
// Only paths in protectedPaths require authentication (blacklist approach).
func APIKeyMiddleware(apiKey string, required bool, protectedPaths []string) func(http.Handler) http.Handler {
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
			if providedKey == "" {
				log.Warnf("%s Missing API key from %s for %s", logcolors.LogAPIKey, r.RemoteAddr, path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"API key required","message":"Provide a valid API key via X-API-Key header"}`))
				return
			}

			if providedKey != apiKey {
				log.Warnf("%s Invalid API key from %s for %s", logcolors.LogAPIKey, r.RemoteAddr, path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"Invalid API key","message":"The provided API key is not valid"}`))
				return
			}

			// Valid API key, proceed
			next.ServeHTTP(w, r)
		})
	}
}
