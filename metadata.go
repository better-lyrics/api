package main

import (
	"encoding/json"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/utils"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	metadataBucket = "metadata"
	indexesBucket  = "indexes"
)

// initMetadataBuckets creates the metadata and indexes buckets if they don't exist.
// Called during server startup after persistentCache is initialized.
func initMetadataBuckets() {
	if err := persistentCache.CreateBucket(metadataBucket); err != nil {
		log.Errorf("%s Failed to create metadata bucket: %v", logcolors.LogCache, err)
		return
	}
	if err := persistentCache.CreateBucket(indexesBucket); err != nil {
		log.Errorf("%s Failed to create indexes bucket: %v", logcolors.LogCache, err)
		return
	}
	log.Infof("%s Metadata and indexes buckets initialized", logcolors.LogCache)
}

// metadataGet retrieves a value from a bucket, decompressing if needed.
func metadataGet(bucket, key string) (string, bool) {
	data, ok := persistentCache.GetFromBucket(bucket, key)
	if !ok {
		return "", false
	}

	// Decompress
	decompressed, err := utils.DecompressString(string(data))
	if err != nil {
		// Might be uncompressed (old data or plain text)
		return string(data), true
	}
	return decompressed, true
}

// metadataSet stores a value in a bucket, compressing it.
func metadataSet(bucket, key, value string) error {
	compressed, err := utils.CompressString(value)
	if err != nil {
		return err
	}
	return persistentCache.SetInBucket(bucket, key, []byte(compressed))
}

// getSongMetadata retrieves metadata for a cache key.
func getSongMetadata(cacheKey string) (*SongMetadata, bool) {
	raw, ok := metadataGet(metadataBucket, cacheKey)
	if !ok {
		return nil, false
	}

	var meta SongMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return nil, false
	}
	return &meta, true
}

// setSongMetadata stores/updates metadata, maintaining all reverse indexes.
func setSongMetadata(meta *SongMetadata) {
	if meta.CacheKey == "" {
		return
	}

	now := time.Now().Unix()

	// Check if existing metadata exists to preserve FirstSeen and merge VideoIDs
	if existing, ok := getSongMetadata(meta.CacheKey); ok {
		meta.FirstSeen = existing.FirstSeen
		// Merge video IDs
		meta.VideoIDs = mergeStringSlice(existing.VideoIDs, meta.VideoIDs)
	} else {
		meta.FirstSeen = now
	}
	meta.LastUpdated = now

	data, err := json.Marshal(meta)
	if err != nil {
		log.Errorf("%s Error marshaling metadata: %v", logcolors.LogCache, err)
		return
	}

	if err := metadataSet(metadataBucket, meta.CacheKey, string(data)); err != nil {
		log.Errorf("%s Error setting metadata: %v", logcolors.LogCache, err)
		return
	}

	// Update reverse indexes
	if meta.ISRC != "" {
		addToIndex("isrc:"+meta.ISRC, meta.CacheKey)
	}

	songKey := buildSongIndexKey(meta.TrackName, meta.ArtistName)
	if songKey != "" {
		addToIndex("song:"+songKey, meta.CacheKey)
	}

	for _, vid := range meta.VideoIDs {
		if vid != "" {
			addToIndex("video:"+vid, meta.CacheKey)
		}
	}
}

// addVideoID adds a videoId to the metadata for a cache key, creating metadata if needed.
func addVideoID(cacheKey, videoID string) {
	if cacheKey == "" || videoID == "" {
		return
	}

	meta, ok := getSongMetadata(cacheKey)
	if !ok {
		meta = &SongMetadata{
			CacheKey:  cacheKey,
			FirstSeen: time.Now().Unix(),
		}
	}

	// Check if videoId already exists
	for _, v := range meta.VideoIDs {
		if v == videoID {
			return // Already tracked
		}
	}

	meta.VideoIDs = append(meta.VideoIDs, videoID)
	meta.LastUpdated = time.Now().Unix()

	data, err := json.Marshal(meta)
	if err != nil {
		log.Errorf("%s Error marshaling metadata for addVideoID: %v", logcolors.LogCache, err)
		return
	}

	if err := metadataSet(metadataBucket, cacheKey, string(data)); err != nil {
		log.Errorf("%s Error setting metadata for addVideoID: %v", logcolors.LogCache, err)
		return
	}

	// Update video reverse index
	addToIndex("video:"+videoID, cacheKey)
}

// getVideoIDs returns all videoIds associated with a cache key.
func getVideoIDs(cacheKey string) []string {
	meta, ok := getSongMetadata(cacheKey)
	if !ok {
		return nil
	}
	return meta.VideoIDs
}

// getAllVideoIDsForSong returns videoIds across ALL duration variants of a song.
// Uses the song index to find all cache keys for song+artist, then collects videoIds.
func getAllVideoIDsForSong(songName, artistName string) []string {
	songKey := buildSongIndexKey(songName, artistName)
	if songKey == "" {
		return nil
	}

	cacheKeys := getIndex("song:" + songKey)
	if len(cacheKeys) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var allVideoIDs []string
	for _, ck := range cacheKeys {
		for _, vid := range getVideoIDs(ck) {
			if !seen[vid] {
				seen[vid] = true
				allVideoIDs = append(allVideoIDs, vid)
			}
		}
	}
	return allVideoIDs
}

// getCacheKeysByVideoID reverse-lookups cache keys from a videoId.
func getCacheKeysByVideoID(videoID string) []string {
	return getIndex("video:" + videoID)
}

// addToIndex appends a value to a JSON array stored at the given index key (deduped).
func addToIndex(indexKey, value string) {
	existing := getIndex(indexKey)
	for _, v := range existing {
		if v == value {
			return // Already in index
		}
	}

	existing = append(existing, value)
	data, err := json.Marshal(existing)
	if err != nil {
		return
	}

	if err := metadataSet(indexesBucket, indexKey, string(data)); err != nil {
		log.Errorf("%s Error updating index %s: %v", logcolors.LogCache, indexKey, err)
	}
}

// getIndex retrieves a JSON array of strings from the indexes bucket.
func getIndex(indexKey string) []string {
	raw, ok := metadataGet(indexesBucket, indexKey)
	if !ok {
		return nil
	}

	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

// buildSongIndexKey creates a normalized key for the song+artist index.
func buildSongIndexKey(songName, artistName string) string {
	song := strings.ToLower(strings.TrimSpace(songName))
	artist := strings.ToLower(strings.TrimSpace(artistName))
	if song == "" && artist == "" {
		return ""
	}
	return song + " " + artist
}

// mergeStringSlice merges two string slices, deduplicating entries.
func mergeStringSlice(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, v := range a {
		seen[v] = true
	}
	merged := make([]string, len(a))
	copy(merged, a)
	for _, v := range b {
		if !seen[v] {
			merged = append(merged, v)
			seen[v] = true
		}
	}
	return merged
}
