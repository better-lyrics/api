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

// Lyrics cache operations

// getCachedLyrics retrieves and parses cached lyrics, returns the full CachedLyrics struct and found
// Handles both old format (plain TTML string) and new format (JSON with metadata)
func getCachedLyrics(key string) (*CachedLyrics, bool) {
	cached, ok := persistentCache.Get(key)
	if !ok {
		return nil, false
	}

	// Try to parse as JSON format
	var cachedLyrics CachedLyrics
	if err := json.Unmarshal([]byte(cached), &cachedLyrics); err == nil && cachedLyrics.TTML != "" {
		return &cachedLyrics, true
	}

	// Fallback to old format (plain TTML string) - no metadata available
	return &CachedLyrics{TTML: cached}, true
}

// setCachedLyrics stores lyrics with full metadata
func setCachedLyrics(key, lyrics string, trackDurationMs int, score float64, language string, isRTL bool) {
	cachedLyrics := CachedLyrics{
		TTML:            lyrics,
		TrackDurationMs: trackDurationMs,
		Score:           score,
		Language:        language,
		IsRTL:           isRTL,
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

// Cache debug endpoints

// cacheHelp returns documentation for all cache-related endpoints
func cacheHelp(w http.ResponseWriter, r *http.Request) {
	help := map[string]interface{}{
		"description": "Cache management and debugging endpoints",
		"endpoints": []map[string]interface{}{
			{
				"path":        "/cache",
				"method":      "GET",
				"auth":        "Authorization header required",
				"description": "Get full cache dump with statistics",
				"response":    "Cache contents, key count, size in KB/MB, hit rate",
			},
			{
				"path":        "/cache/help",
				"method":      "GET",
				"auth":        "None",
				"description": "This help documentation",
			},
			{
				"path":        "/cache/lookup",
				"method":      "GET",
				"auth":        "Authorization header required",
				"description": "Check if a song is cached and get cache key info",
				"params": map[string]string{
					"s, song, songName":     "Song name",
					"a, artist, artistName": "Artist name",
					"al, album, albumName":  "Album name (optional)",
					"d, duration":           "Duration in seconds (optional)",
				},
				"response": "Shows normalized/legacy keys, whether found, TTML preview",
			},
			{
				"path":        "/cache/debug",
				"method":      "GET",
				"auth":        "Authorization header required",
				"description": "Get detailed info about a specific cache key",
				"params": map[string]string{
					"key": "The full cache key to inspect",
				},
				"response": "Raw size, compression ratio, entry type, content preview",
			},
			{
				"path":        "/cache/keys",
				"method":      "GET",
				"auth":        "Authorization header required",
				"description": "List and search cache keys",
				"params": map[string]string{
					"prefix":   "Filter keys by prefix (e.g., 'ttml_lyrics:')",
					"contains": "Filter keys containing substring (case-insensitive)",
					"limit":    "Max results to return (default: 100, max: 1000)",
				},
				"response": "List of matching keys with size and type info",
			},
			{
				"path":        "/cache/backup",
				"method":      "GET",
				"auth":        "Authorization header required",
				"description": "Create a backup of the cache database",
				"response":    "Backup file path",
			},
			{
				"path":        "/cache/backups",
				"method":      "GET",
				"auth":        "Authorization header required",
				"description": "List all available cache backups",
				"response":    "Array of backup filenames",
			},
			{
				"path":        "/cache/restore",
				"method":      "GET",
				"auth":        "Authorization header required",
				"description": "Restore cache from a backup",
				"params": map[string]string{
					"backup": "Backup filename (from /cache/backups)",
				},
			},
			{
				"path":        "/cache/clear",
				"method":      "GET",
				"auth":        "Authorization header required",
				"description": "Clear the cache (creates automatic backup first)",
				"response":    "Backup path of the cleared cache",
			},
			{
				"path":        "/cache/migrate",
				"method":      "GET",
				"auth":        "Authorization header required",
				"description": "Migrate legacy cache keys to normalized format (async)",
				"params": map[string]string{
					"dry_run":    "Preview changes without applying (default: false)",
					"recompress": "Also recompress existing entries (default: false)",
				},
				"response": "Job ID for tracking progress",
				"notes":    "Returns immediately. Use /cache/migrate/status to track progress.",
			},
			{
				"path":        "/cache/migrate/status",
				"method":      "GET",
				"auth":        "Authorization header required",
				"description": "Check migration job status",
				"params": map[string]string{
					"job_id": "Job ID from /cache/migrate (optional, lists all if omitted)",
				},
				"response": "Job status, progress percentage, results when complete",
			},
		},
		"cache_key_format": map[string]string{
			"lyrics":   "ttml_lyrics:{song} {artist} [{album}] [{duration}s]",
			"negative": "no_lyrics:ttml_lyrics:{song} {artist} ...",
		},
		"notes": []string{
			"All keys are normalized to lowercase with trimmed whitespace",
			"Lyrics cache has no TTL - entries persist until manually cleared",
			"Negative cache (no lyrics found) expires after 7 days by default",
			"Cache uses gzip compression with BestCompression level",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(help)
}

// cacheLookup checks if a song is cached and returns cache key info
func cacheLookup(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")

	if songName == "" && artistName == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Provide at least song (s) or artist (a) parameter",
		})
		return
	}

	normalizedKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
	legacyKey := buildLegacyCacheKey(songName, artistName, albumName, durationStr)

	result := map[string]interface{}{
		"query": map[string]string{
			"song":     songName,
			"artist":   artistName,
			"album":    albumName,
			"duration": durationStr,
		},
		"normalized_key": normalizedKey,
		"legacy_key":     legacyKey,
		"keys_differ":    normalizedKey != legacyKey,
	}

	// Check normalized key
	if cached, ok := getCachedLyrics(normalizedKey); ok {
		result["found"] = true
		result["found_in"] = "normalized"
		result["track_duration_ms"] = cached.TrackDurationMs
		result["score"] = cached.Score
		result["language"] = cached.Language
		result["isRTL"] = cached.IsRTL
		result["ttml_length"] = len(cached.TTML)
		result["ttml_preview"] = truncateString(cached.TTML, 200)
	} else if cached, ok := getCachedLyrics(legacyKey); ok {
		result["found"] = true
		result["found_in"] = "legacy"
		result["track_duration_ms"] = cached.TrackDurationMs
		result["score"] = cached.Score
		result["language"] = cached.Language
		result["isRTL"] = cached.IsRTL
		result["ttml_length"] = len(cached.TTML)
		result["ttml_preview"] = truncateString(cached.TTML, 200)
		result["note"] = "Found in legacy key - run /cache/migrate to normalize"
	} else {
		result["found"] = false
		// Check negative cache
		if reason, ok := getNegativeCache(normalizedKey); ok {
			result["negative_cache"] = true
			result["negative_reason"] = reason
		} else if reason, ok := getNegativeCache(legacyKey); ok {
			result["negative_cache"] = true
			result["negative_cache_in"] = "legacy"
			result["negative_reason"] = reason
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// cacheDebug returns detailed info about a specific cache key
func cacheDebug(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Provide 'key' parameter",
		})
		return
	}

	result := map[string]interface{}{
		"key": key,
	}

	// Get raw entry
	var found bool
	var rawSize int
	persistentCache.Range(func(k string, entry cache.CacheEntry) bool {
		if k == key {
			found = true
			rawSize = len(entry.Value)
			result["raw_size_bytes"] = rawSize
			result["is_compressed"] = strings.HasPrefix(entry.Value, "H4sI") // gzip base64 signature
			return false
		}
		return true
	})

	if !found {
		result["found"] = false
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
		return
	}

	result["found"] = true

	// Get decompressed value
	if value, ok := persistentCache.Get(key); ok {
		result["decompressed_size_bytes"] = len(value)
		if rawSize > 0 {
			result["compression_ratio"] = fmt.Sprintf("%.1f%%", float64(rawSize)/float64(len(value))*100)
		}

		// Try to parse as lyrics
		var cachedLyrics CachedLyrics
		if err := json.Unmarshal([]byte(value), &cachedLyrics); err == nil && cachedLyrics.TTML != "" {
			result["type"] = "lyrics"
			result["track_duration_ms"] = cachedLyrics.TrackDurationMs
			result["ttml_length"] = len(cachedLyrics.TTML)
			result["ttml_preview"] = truncateString(cachedLyrics.TTML, 300)
		} else if strings.HasPrefix(key, "no_lyrics:") {
			// Try to parse as negative cache
			var negEntry NegativeCacheEntry
			if err := json.Unmarshal([]byte(value), &negEntry); err == nil {
				result["type"] = "negative_cache"
				result["reason"] = negEntry.Reason
				result["timestamp"] = negEntry.Timestamp
				result["cached_at"] = time.Unix(negEntry.Timestamp, 0).Format(time.RFC3339)
			}
		} else {
			result["type"] = "unknown"
			result["value_preview"] = truncateString(value, 200)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// cacheKeys lists cache keys matching a pattern
func cacheKeys(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	prefix := r.URL.Query().Get("prefix")
	contains := r.URL.Query().Get("contains")
	limitStr := r.URL.Query().Get("limit")

	limit := 100
	if limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
		if limit > 1000 {
			limit = 1000
		}
	}

	var keys []map[string]interface{}
	count := 0
	total := 0

	persistentCache.Range(func(key string, entry cache.CacheEntry) bool {
		total++

		// Filter by prefix
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			return true
		}

		// Filter by contains
		if contains != "" && !strings.Contains(strings.ToLower(key), strings.ToLower(contains)) {
			return true
		}

		if count < limit {
			keys = append(keys, map[string]interface{}{
				"key":       key,
				"size":      len(entry.Value),
				"is_lyrics": strings.HasPrefix(key, "ttml_lyrics:"),
				"is_negative": strings.HasPrefix(key, "no_lyrics:"),
			})
			count++
		}

		return true
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_keys":    total,
		"matched_keys":  count,
		"limit":         limit,
		"keys":          keys,
	})
}

// truncateString truncates a string to maxLen and adds "..." if truncated
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
