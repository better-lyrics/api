package main

import (
	"encoding/json"
	"fmt"
	"lyrics-api-go/cache"
	"lyrics-api-go/logcolors"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Basic cache operations

func getCache(key string) (string, bool) {
	return persistentCache.Get(key)
}

func setCache(key, value string) {
	if err := persistentCache.Set(key, value); err != nil {
		log.Errorf("%s Error setting cache value: %v", logcolors.LogCache, err)
	}
}

// Lyrics cache operations

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
		log.Errorf("%s Error marshaling cached lyrics: %v", logcolors.LogCacheLyrics, err)
		return
	}
	if err := persistentCache.Set(key, string(data)); err != nil {
		log.Errorf("%s Error setting cache value: %v", logcolors.LogCacheLyrics, err)
	}
}

// Negative cache operations

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
		log.Errorf("%s Error marshaling negative cache entry: %v", logcolors.LogCacheNegative, err)
		return
	}
	if err := persistentCache.Set(negativeKey, string(data)); err != nil {
		log.Errorf("%s Error setting negative cache: %v", logcolors.LogCacheNegative, err)
	}
	log.Infof("%s Cached 'no lyrics' for key: %s (reason: %s)", logcolors.LogCacheNegative, key, reason)
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

// Cache key builders

// buildNormalizedCacheKey creates a consistent, normalized cache key.
// This ensures cache hits regardless of input casing or whitespace variations.
func buildNormalizedCacheKey(songName, artistName, albumName, durationStr string) string {
	// Normalize: trim whitespace and convert to lowercase
	song := strings.ToLower(strings.TrimSpace(songName))
	artist := strings.ToLower(strings.TrimSpace(artistName))
	album := strings.ToLower(strings.TrimSpace(albumName))

	// Build query without trailing spaces for empty values
	query := song + " " + artist
	if album != "" {
		query += " " + album
	}
	if durationStr != "" {
		query += " " + durationStr + "s"
	}

	return fmt.Sprintf("ttml_lyrics:%s", query)
}

// buildLegacyCacheKey creates a cache key using the old format (for backwards compatibility).
// Old format: "ttml_lyrics:{song} {artist} {album}" with trailing space when album is empty.
func buildLegacyCacheKey(songName, artistName, albumName, durationStr string) string {
	query := songName + " " + artistName + " " + albumName
	if durationStr != "" {
		query = query + " " + durationStr + "s"
	}
	return fmt.Sprintf("ttml_lyrics:%s", query)
}

// buildFallbackCacheKeys returns a list of cache keys to try when the backend fails.
// Keys are ordered from most specific to least specific, excluding the original key.
// When duration is provided, fallback keys still include duration to maintain strict matching.
func buildFallbackCacheKeys(songName, artistName, albumName, durationStr, originalKey string) []string {
	var keys []string

	// Fallback: without album (if album was provided)
	// Duration is preserved if it was in the original request
	// Uses normalized format (no trailing spaces, lowercase)
	if albumName != "" {
		normalizedNoAlbum := buildNormalizedCacheKey(songName, artistName, "", durationStr)
		if normalizedNoAlbum != originalKey {
			keys = append(keys, normalizedNoAlbum)
		}
	}

	return keys
}

// Migration handler

// migrateCache migrates legacy cache keys to the new normalized format and re-compresses data.
// Legacy format: "ttml_lyrics:{song} {artist} {album}" with trailing space when album is empty
// New format: "ttml_lyrics:{song} {artist}" (lowercase, trimmed, no trailing spaces)
//
// Query params:
//   - recompress=true: Also re-compress entries that don't need key migration (optimizes storage)
//   - dry_run=true: Preview changes without applying them
func migrateCache(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	recompress := r.URL.Query().Get("recompress") == "true"
	dryRun := r.URL.Query().Get("dry_run") == "true"

	log.Infof("%s Starting cache migration (recompress=%v, dry_run=%v)...", logcolors.LogCache, recompress, dryRun)

	var migrated, recompressed, skipped, failed int
	var totalSavings int64
	var migratedKeys []string
	keysToDelete := make(map[string]bool)
	keysToMigrate := make(map[string]string) // normalizedKey -> legacyKey
	keysToRecompress := []string{}

	// First pass: identify keys that need migration or recompression
	persistentCache.Range(func(key string, entry cache.CacheEntry) bool {
		// Only process lyrics cache keys
		if !strings.HasPrefix(key, "ttml_lyrics:") {
			skipped++
			return true
		}

		// Check if key has legacy format characteristics
		query := strings.TrimPrefix(key, "ttml_lyrics:")
		normalizedQuery := strings.ToLower(strings.TrimSpace(query))

		// Collapse multiple spaces to single space
		for strings.Contains(normalizedQuery, "  ") {
			normalizedQuery = strings.ReplaceAll(normalizedQuery, "  ", " ")
		}

		normalizedKey := "ttml_lyrics:" + normalizedQuery

		// If the normalized key is different, this is a legacy key
		if normalizedKey != key {
			// Check if normalized key already exists
			if _, exists := persistentCache.Get(normalizedKey); !exists {
				keysToMigrate[normalizedKey] = key
			}
			keysToDelete[key] = true
		} else if recompress {
			// Key is already normalized, but may need re-compression
			keysToRecompress = append(keysToRecompress, key)
		}

		return true
	})

	if dryRun {
		// Return preview without making changes
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message":            "Dry run - no changes made",
			"dry_run":            true,
			"keys_to_migrate":    len(keysToMigrate),
			"keys_to_delete":     len(keysToDelete),
			"keys_to_recompress": len(keysToRecompress),
			"skipped":            skipped,
		})
		return
	}

	// Second pass: migrate keys (re-compresses during Set)
	for normalizedKey, legacyKey := range keysToMigrate {
		if value, ok := persistentCache.Get(legacyKey); ok {
			if err := persistentCache.Set(normalizedKey, value); err != nil {
				log.Warnf("%s Failed to migrate key %s -> %s: %v", logcolors.LogCache, legacyKey, normalizedKey, err)
				failed++
				continue
			}
			migratedKeys = append(migratedKeys, fmt.Sprintf("%s -> %s", legacyKey, normalizedKey))
			migrated++
		}
	}

	// Third pass: re-compress entries that don't need key migration
	if recompress {
		for _, key := range keysToRecompress {
			if value, ok := persistentCache.Get(key); ok {
				// Get original size from raw entry
				originalSize := 0
				persistentCache.Range(func(k string, entry cache.CacheEntry) bool {
					if k == key {
						originalSize = len(entry.Value)
						return false
					}
					return true
				})

				// Re-set with optimized compression
				if err := persistentCache.Set(key, value); err != nil {
					log.Warnf("%s Failed to recompress key %s: %v", logcolors.LogCache, key, err)
					failed++
					continue
				}

				// Calculate savings
				newSize := 0
				persistentCache.Range(func(k string, entry cache.CacheEntry) bool {
					if k == key {
						newSize = len(entry.Value)
						return false
					}
					return true
				})

				savings := originalSize - newSize
				if savings > 0 {
					totalSavings += int64(savings)
					recompressed++
				}
			}
		}
	}

	// Fourth pass: delete legacy keys
	deleted := 0
	for legacyKey := range keysToDelete {
		if err := persistentCache.Delete(legacyKey); err != nil {
			log.Warnf("%s Failed to delete legacy key %s: %v", logcolors.LogCache, legacyKey, err)
		} else {
			deleted++
		}
	}

	log.Infof("%s Cache migration complete: %d migrated, %d recompressed, %d deleted, %d skipped, %d failed, %d bytes saved",
		logcolors.LogCache, migrated, recompressed, deleted, skipped, failed, totalSavings)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":       "Cache migration complete",
		"migrated":      migrated,
		"recompressed":  recompressed,
		"deleted":       deleted,
		"skipped":       skipped,
		"failed":        failed,
		"bytes_saved":   totalSavings,
		"migrated_keys": migratedKeys,
	})
}
