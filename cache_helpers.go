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

// generateJobID creates a unique job ID
func generateJobID() string {
	return fmt.Sprintf("mig_%d", time.Now().UnixNano())
}

// migrateCache migrates legacy cache keys to the new normalized format and re-compresses data.
// Legacy format: "ttml_lyrics:{song} {artist} {album}" with trailing space when album is empty
// New format: "ttml_lyrics:{song} {artist}" (lowercase, trimmed, no trailing spaces)
//
// Query params:
//   - recompress=true: Also re-compress entries that don't need key migration (optimizes storage)
//   - dry_run=true: Preview changes without applying them (runs synchronously)
//
// Returns immediately with a job ID. Use /cache/migrate/status?job_id=xxx to check progress.
func migrateCache(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	recompress := r.URL.Query().Get("recompress") == "true"
	dryRun := r.URL.Query().Get("dry_run") == "true"

	// Dry run is synchronous (fast, just counts keys)
	if dryRun {
		runMigrationDryRun(w)
		return
	}

	// Check if a migration is already running
	migrationJobs.RLock()
	for _, job := range migrationJobs.jobs {
		if job.Status == JobStatusRunning || job.Status == JobStatusPending {
			migrationJobs.RUnlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":  "A migration is already in progress",
				"job_id": job.ID,
			})
			return
		}
	}
	migrationJobs.RUnlock()

	// Create new job
	job := &MigrationJob{
		ID:         generateJobID(),
		Status:     JobStatusPending,
		StartedAt:  time.Now().Unix(),
		Recompress: recompress,
		Progress:   MigrationProgress{},
	}

	// Store job
	migrationJobs.Lock()
	migrationJobs.jobs[job.ID] = job
	migrationJobs.Unlock()

	// Start migration in background
	go runMigrationAsync(job)

	log.Infof("%s Started async cache migration job %s (recompress=%v)", logcolors.LogCache, job.ID, recompress)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "Migration started",
		"job_id":     job.ID,
		"status_url": fmt.Sprintf("/cache/migrate/status?job_id=%s", job.ID),
	})
}

// runMigrationDryRun performs a dry run synchronously
func runMigrationDryRun(w http.ResponseWriter) {
	var skipped int
	keysToDelete := make(map[string]bool)
	keysToMigrate := make(map[string]string)
	keysToRecompress := []string{}

	persistentCache.Range(func(key string, entry cache.CacheEntry) bool {
		if !strings.HasPrefix(key, "ttml_lyrics:") {
			skipped++
			return true
		}

		query := strings.TrimPrefix(key, "ttml_lyrics:")
		normalizedQuery := strings.ToLower(strings.TrimSpace(query))
		for strings.Contains(normalizedQuery, "  ") {
			normalizedQuery = strings.ReplaceAll(normalizedQuery, "  ", " ")
		}
		normalizedKey := "ttml_lyrics:" + normalizedQuery

		if normalizedKey != key {
			if _, exists := persistentCache.Get(normalizedKey); !exists {
				keysToMigrate[normalizedKey] = key
			}
			keysToDelete[key] = true
		} else {
			keysToRecompress = append(keysToRecompress, key)
		}
		return true
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":            "Dry run - no changes made",
		"dry_run":            true,
		"keys_to_migrate":    len(keysToMigrate),
		"keys_to_delete":     len(keysToDelete),
		"keys_to_recompress": len(keysToRecompress),
		"skipped":            skipped,
	})
}

// runMigrationAsync performs the actual migration in the background
func runMigrationAsync(job *MigrationJob) {
	// Update status to running
	migrationJobs.Lock()
	job.Status = JobStatusRunning
	migrationJobs.Unlock()

	defer func() {
		if r := recover(); r != nil {
			migrationJobs.Lock()
			job.Status = JobStatusFailed
			job.Error = fmt.Sprintf("panic: %v", r)
			job.CompletedAt = time.Now().Unix()
			migrationJobs.Unlock()
			log.Errorf("%s Migration job %s panicked: %v", logcolors.LogCache, job.ID, r)
		}
	}()

	var migrated, recompressed, skipped, failed int
	var totalSavings int64
	var migratedKeys []string
	keysToDelete := make(map[string]bool)
	keysToMigrate := make(map[string]string)
	keysToRecompress := []string{}

	// First pass: identify keys
	var totalKeys int
	persistentCache.Range(func(key string, entry cache.CacheEntry) bool {
		totalKeys++
		if !strings.HasPrefix(key, "ttml_lyrics:") {
			skipped++
			return true
		}

		query := strings.TrimPrefix(key, "ttml_lyrics:")
		normalizedQuery := strings.ToLower(strings.TrimSpace(query))
		for strings.Contains(normalizedQuery, "  ") {
			normalizedQuery = strings.ReplaceAll(normalizedQuery, "  ", " ")
		}
		normalizedKey := "ttml_lyrics:" + normalizedQuery

		if normalizedKey != key {
			if _, exists := persistentCache.Get(normalizedKey); !exists {
				keysToMigrate[normalizedKey] = key
			}
			keysToDelete[key] = true
		} else if job.Recompress {
			keysToRecompress = append(keysToRecompress, key)
		}
		return true
	})

	// Calculate total work
	totalWork := len(keysToMigrate) + len(keysToRecompress) + len(keysToDelete)
	processedWork := 0

	updateProgress := func() {
		migrationJobs.Lock()
		job.Progress.TotalKeys = totalWork
		job.Progress.ProcessedKeys = processedWork
		if totalWork > 0 {
			job.Progress.Percent = (processedWork * 100) / totalWork
		}
		migrationJobs.Unlock()
	}

	updateProgress()

	// Second pass: migrate keys
	for normalizedKey, legacyKey := range keysToMigrate {
		if value, ok := persistentCache.Get(legacyKey); ok {
			if err := persistentCache.Set(normalizedKey, value); err != nil {
				log.Warnf("%s Failed to migrate key %s -> %s: %v", logcolors.LogCache, legacyKey, normalizedKey, err)
				failed++
			} else {
				migratedKeys = append(migratedKeys, fmt.Sprintf("%s -> %s", legacyKey, normalizedKey))
				migrated++
			}
		}
		processedWork++
		updateProgress()
	}

	// Third pass: re-compress
	if job.Recompress {
		for _, key := range keysToRecompress {
			if value, ok := persistentCache.Get(key); ok {
				originalSize := 0
				persistentCache.Range(func(k string, entry cache.CacheEntry) bool {
					if k == key {
						originalSize = len(entry.Value)
						return false
					}
					return true
				})

				if err := persistentCache.Set(key, value); err != nil {
					log.Warnf("%s Failed to recompress key %s: %v", logcolors.LogCache, key, err)
					failed++
				} else {
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
			processedWork++
			updateProgress()
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
		processedWork++
		updateProgress()
	}

	// Store results
	migrationJobs.Lock()
	job.Status = JobStatusCompleted
	job.CompletedAt = time.Now().Unix()
	job.Result = &MigrationResult{
		Migrated:     migrated,
		Recompressed: recompressed,
		Deleted:      deleted,
		Skipped:      skipped,
		Failed:       failed,
		BytesSaved:   totalSavings,
		MigratedKeys: migratedKeys,
	}
	migrationJobs.Unlock()

	log.Infof("%s Migration job %s complete: %d migrated, %d recompressed, %d deleted, %d skipped, %d failed, %d bytes saved",
		logcolors.LogCache, job.ID, migrated, recompressed, deleted, skipped, failed, totalSavings)
}

// getMigrationStatus returns the status of a migration job
func getMigrationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	jobID := r.URL.Query().Get("job_id")
	if jobID == "" {
		// Return all jobs
		migrationJobs.RLock()
		jobs := make([]*MigrationJob, 0, len(migrationJobs.jobs))
		for _, job := range migrationJobs.jobs {
			jobs = append(jobs, job)
		}
		migrationJobs.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jobs": jobs,
		})
		return
	}

	migrationJobs.RLock()
	job, exists := migrationJobs.jobs[jobID]
	migrationJobs.RUnlock()

	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Job not found",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}
