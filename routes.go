package main

import (
	"github.com/gorilla/mux"
)

// setupRoutes configures all HTTP routes for the API
func setupRoutes(router *mux.Router) {
	// Default endpoint - backwards compatible, returns {"ttml": ...}
	router.HandleFunc("/getLyrics", getLyrics)

	// Revalidate endpoint - checks if cached lyrics are stale and updates if needed
	router.HandleFunc("/revalidate", revalidateHandler)

	// Provider-specific endpoints - return {"lyrics": ..., "provider": ...}
	router.HandleFunc("/ttml/getLyrics", getLyricsWithProvider("ttml"))
	router.HandleFunc("/kugou/getLyrics", getLyricsWithProvider("kugou"))
	router.HandleFunc("/legacy/getLyrics", getLyricsWithProvider("legacy"))

	// Cache management endpoints
	router.HandleFunc("/cache", getCacheDump)
	router.HandleFunc("/cache/help", cacheHelp)
	router.HandleFunc("/cache/backup", backupCache)
	router.HandleFunc("/cache/backups", listBackups)
	router.HandleFunc("/cache/restore", restoreCache)
	router.HandleFunc("/cache/clear", clearCache)
	router.HandleFunc("/cache/clear/{provider}", clearProviderCache)
	router.HandleFunc("/cache/migrate", migrateCache)
	router.HandleFunc("/cache/migrate/status", getMigrationStatus)
	router.HandleFunc("/cache/lookup", cacheLookup)
	router.HandleFunc("/cache/debug", cacheDebug)
	router.HandleFunc("/cache/keys", cacheKeys)

	// Health and stats endpoints
	router.HandleFunc("/health", getHealthStatus)
	router.HandleFunc("/stats", getStats)

	// Circuit breaker endpoints
	router.HandleFunc("/circuit-breaker", getCircuitBreakerStatus)
	router.HandleFunc("/circuit-breaker/reset", resetCircuitBreaker)
	router.HandleFunc("/circuit-breaker/simulate-failure", simulateCircuitBreakerFailure)

	// Test/debug endpoints
	router.HandleFunc("/test-notifications", testNotifications)

	// Help endpoint
	router.HandleFunc("/", helpHandler)
}
