package httpapi

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/bini"
	"lyrics-api-go/services/proxy"

	ttml "lyrics-api-go/services/providers/ttml"

	log "github.com/sirupsen/logrus"
)

// findMatchingCacheKeys finds existing cache keys for a song via direct lookups
// (O(delta), never a full scan). Ported verbatim over the Postgres cache.
func (s *Server) findMatchingCacheKeys(ctx context.Context, songName, artistName, albumName, durationStr string) []string {
	seen := make(map[string]bool)
	var keys []string

	addIfExists := func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		if _, ok := s.getCachedLyrics(ctx, key); ok {
			keys = append(keys, key)
		}
	}

	addIfExists(buildNormalizedCacheKey(songName, artistName, albumName, durationStr))
	addIfExists(buildLegacyCacheKey(songName, artistName, albumName, durationStr))

	if durationStr != "" {
		addIfExists(buildNormalizedCacheKey(songName, artistName, albumName, ""))
		addIfExists(buildLegacyCacheKey(songName, artistName, albumName, ""))
	}

	if durationStr != "" {
		var durationSec int
		if _, err := fmt.Sscanf(durationStr, "%d", &durationSec); err == nil {
			deltaSec := s.cfg.Configuration.DurationMatchDeltaMs / 1000
			if deltaSec < 1 {
				deltaSec = 1
			}
			for offset := 1; offset <= deltaSec; offset++ {
				if durationSec-offset >= 0 {
					d := fmt.Sprintf("%d", durationSec-offset)
					addIfExists(buildNormalizedCacheKey(songName, artistName, albumName, d))
					addIfExists(buildLegacyCacheKey(songName, artistName, albumName, d))
				}
				d := fmt.Sprintf("%d", durationSec+offset)
				addIfExists(buildNormalizedCacheKey(songName, artistName, albumName, d))
				addIfExists(buildLegacyCacheKey(songName, artistName, albumName, d))
			}
		}
	}

	return keys
}

func (s *Server) revalidateHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	apiKeyAuthenticated, _ := r.Context().Value(apiKeyAuthenticatedKey).(bool)
	if !apiKeyAuthenticated {
		respond(w, r).Error(http.StatusUnauthorized, map[string]interface{}{
			"error":   "API key required for revalidation",
			"message": "Provide a valid API key via X-API-Key header",
		})
		return
	}

	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")

	if songName == "" || artistName == "" {
		respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "song (s) and artist (a) parameters are required",
		})
		return
	}

	cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
	legacyCacheKey := buildLegacyCacheKey(songName, artistName, albumName, durationStr)

	cached, found := s.getCachedLyrics(ctx, cacheKey)
	usedKey := cacheKey
	if !found && legacyCacheKey != cacheKey {
		cached, found = s.getCachedLyrics(ctx, legacyCacheKey)
		usedKey = legacyCacheKey
	}

	wasNoLyricsSentinel := found && cached.TTML == NoLyricsSentinel

	wasInNegativeCache := false
	if !found {
		if _, negFound := s.getNegativeCache(ctx, cacheKey); negFound {
			wasInNegativeCache = true
			found = true
			usedKey = cacheKey
		} else if legacyCacheKey != cacheKey {
			if _, negFound := s.getNegativeCache(ctx, legacyCacheKey); negFound {
				wasInNegativeCache = true
				usedKey = legacyCacheKey
				found = true
			}
		}
	}

	if !found {
		respond(w, r).Error(http.StatusNotFound, map[string]interface{}{
			"error":    "no cached lyrics found for this query",
			"cacheKey": cacheKey,
		})
		return
	}

	var oldHash [16]byte
	if !wasInNegativeCache && !wasNoLyricsSentinel {
		oldHash = md5.Sum([]byte(cached.TTML))
	}

	var durationMs int
	if durationStr != "" {
		fmt.Sscanf(durationStr, "%d", &durationMs)
		durationMs = durationMs * 1000
	}

	log.Infof("%s Revalidating cache for: %s %s", logcolors.LogRevalidate, songName, artistName)
	ttmlString, trackDurationMs, score, trackMeta, err := ttml.FetchTTMLLyrics(songName, artistName, albumName, durationMs, true, true)

	if err != nil {
		log.Warnf("%s Revalidation fetch failed: %v", logcolors.LogRevalidate, err)
		respond(w, r).JSON(map[string]interface{}{
			"error":    err.Error(),
			"updated":  false,
			"cacheKey": usedKey,
		})
		return
	}

	if ttmlString == "" {
		respond(w, r).JSON(map[string]interface{}{
			"error":    "no lyrics found from source",
			"updated":  false,
			"cacheKey": usedKey,
		})
		return
	}

	newHash := md5.Sum([]byte(ttmlString))
	updated := wasInNegativeCache || wasNoLyricsSentinel || oldHash != newHash

	if updated {
		if wasInNegativeCache {
			s.deleteNegativeCache(ctx, usedKey)
		}
		language, isRTL := ttml.DetectLanguage(ttmlString)
		source := ""
		if trackMeta != nil {
			source = trackMeta.Source
		}
		s.setCachedLyrics(ctx, usedKey, ttmlString, trackDurationMs, score, language, isRTL, source)
		if trackMeta != nil {
			go bini.Contribute(trackMeta.Name, trackMeta.ArtistName, trackMeta.ISRC, trackMeta.Source, trackMeta.RawAttributes, ttmlString)
		}
		go func() {
			bg := context.Background()
			s.setSongMetadata(bg, &store.SongMetadata{
				CacheKey:     usedKey,
				AppleTrackID: trackMeta.TrackID,
				ISRC:         trackMeta.ISRC,
				TrackName:    trackMeta.Name,
				ArtistName:   trackMeta.ArtistName,
				AlbumName:    trackMeta.AlbumName,
				DurationMs:   trackDurationMs,
				ReleaseDate:  trackMeta.ReleaseDate,
			})
			proxy.RevalidateAllForSong(trackMeta.Name, trackMeta.ArtistName, trackMeta.AlbumName, trackDurationMs/1000, s.videoIDsFunc(bg))
		}()
		log.Infof("%s Content changed, cache updated for: %s", logcolors.LogRevalidate, usedKey)
	} else {
		log.Infof("%s Content unchanged for: %s", logcolors.LogRevalidate, usedKey)
	}

	respond(w, r).JSON(map[string]interface{}{
		"updated":          updated,
		"cacheKey":         usedKey,
		"wasNegativeCache": wasInNegativeCache,
	})
}

func (s *Server) overrideHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	apiKeyAuthenticated, _ := r.Context().Value(apiKeyAuthenticatedKey).(bool)
	if !apiKeyAuthenticated {
		respond(w, r).Error(http.StatusUnauthorized, map[string]interface{}{
			"error":   "API key required for override",
			"message": "Provide a valid API key via X-API-Key header",
		})
		return
	}

	trackID := r.URL.Query().Get("id")
	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album") + r.URL.Query().Get("albumName")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")
	dryRun := r.URL.Query().Get("dry_run") == "true"
	noLyrics := r.URL.Query().Get("no_lyrics") == "true"

	if songName == "" || artistName == "" {
		respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "song (s) and artist (a) parameters are required",
		})
		return
	}

	if trackID == "" && !dryRun && !noLyrics {
		respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "id parameter is required (Apple Music track ID)",
		})
		return
	}

	if trackID != "" {
		if _, err := strconv.Atoi(trackID); err != nil {
			respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
				"error": "id must be a numeric Apple Music track ID",
			})
			return
		}
	}

	matchingKeys := s.findMatchingCacheKeys(ctx, songName, artistName, albumName, durationStr)

	if dryRun {
		query := strings.ToLower(strings.TrimSpace(songName)) + " " + strings.ToLower(strings.TrimSpace(artistName))
		log.Infof("%s Dry run: found %d matching keys for %s", logcolors.LogOverride, len(matchingKeys), query)
		respond(w, r).JSON(map[string]interface{}{
			"dry_run": true,
			"count":   len(matchingKeys),
			"keys":    matchingKeys,
		})
		return
	}

	if noLyrics {
		var updatedKeys []string
		created := false

		if len(matchingKeys) == 0 {
			cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
			s.setCachedLyrics(ctx, cacheKey, NoLyricsSentinel, 0, 0, "", false, "")
			updatedKeys = append(updatedKeys, cacheKey)
			created = true
			log.Infof("%s Created no_lyrics marker for %s", logcolors.LogOverride, cacheKey)
		} else {
			for _, key := range matchingKeys {
				cached, ok := s.getCachedLyrics(ctx, key)
				if !ok {
					continue
				}
				s.setCachedLyrics(ctx, key, NoLyricsSentinel, cached.TrackDurationMs, cached.Score, cached.Language, cached.IsRTL, "")
				updatedKeys = append(updatedKeys, key)
			}
			log.Infof("%s Set no_lyrics marker on %d cache entries", logcolors.LogOverride, len(updatedKeys))
		}

		s.deleteNegativeCache(ctx, buildNormalizedCacheKey(songName, artistName, albumName, durationStr))

		respond(w, r).JSON(map[string]interface{}{
			"updated":   len(updatedKeys),
			"created":   created,
			"keys":      updatedKeys,
			"no_lyrics": true,
		})
		return
	}

	log.Infof("%s Fetching lyrics for track ID %s to override %d cache entries", logcolors.LogOverride, trackID, len(matchingKeys))
	ttmlString, err := ttml.FetchLyricsByTrackID(trackID, true)
	if err != nil {
		log.Errorf("%s Failed to fetch lyrics for track ID %s: %v", logcolors.LogOverride, trackID, err)
		respond(w, r).Error(http.StatusInternalServerError, map[string]interface{}{
			"error":    fmt.Sprintf("failed to fetch lyrics: %v", err),
			"track_id": trackID,
		})
		return
	}

	go func() {
		meta, err := ttml.FetchTrackByID(trackID, true)
		if err != nil {
			log.Warnf("%s Skipping lrc.red backfill for track %s: %v", logcolors.LogOverride, trackID, err)
			return
		}
		bini.Contribute(meta.Name, meta.ArtistName, meta.ISRC, ttml.SourceApple, meta.RawAttributes, ttmlString)
	}()

	var updatedKeys []string
	created := false

	if len(matchingKeys) == 0 {
		cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)

		var durationMs int
		if durationStr != "" {
			fmt.Sscanf(durationStr, "%d", &durationMs)
			durationMs = durationMs * 1000
		}

		language, isRTL := ttml.DetectLanguage(ttmlString)
		s.setCachedLyrics(ctx, cacheKey, ttmlString, durationMs, 0, language, isRTL, ttml.SourceApple)
		updatedKeys = append(updatedKeys, cacheKey)
		created = true
		log.Infof("%s Created new cache entry %s with lyrics from track ID %s", logcolors.LogOverride, cacheKey, trackID)
	} else {
		for _, key := range matchingKeys {
			cached, ok := s.getCachedLyrics(ctx, key)
			if !ok {
				continue
			}
			s.setCachedLyrics(ctx, key, ttmlString, cached.TrackDurationMs, cached.Score, cached.Language, cached.IsRTL, ttml.SourceApple)
			updatedKeys = append(updatedKeys, key)
		}
		log.Infof("%s Updated %d cache entries with lyrics from track ID %s", logcolors.LogOverride, len(updatedKeys), trackID)
	}

	s.deleteNegativeCache(ctx, buildNormalizedCacheKey(songName, artistName, albumName, durationStr))

	respond(w, r).JSON(map[string]interface{}{
		"updated":  len(updatedKeys),
		"created":  created,
		"keys":     updatedKeys,
		"track_id": trackID,
	})
}

func (s *Server) videoMapImportHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	type videoMapEntry struct {
		VideoID  string `json:"videoId"`
		Song     string `json:"song"`
		Artist   string `json:"artist"`
		Album    string `json:"album,omitempty"`
		Duration string `json:"duration,omitempty"`
	}

	var entries []videoMapEntry
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&entries); err != nil {
		respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "Invalid JSON body: " + err.Error(),
		})
		return
	}

	const maxVideoMapEntries = 10_000
	if len(entries) > maxVideoMapEntries {
		respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": fmt.Sprintf("too many entries: %d (max %d)", len(entries), maxVideoMapEntries),
		})
		return
	}

	processed := 0
	for _, entry := range entries {
		if entry.VideoID == "" || (entry.Song == "" && entry.Artist == "") {
			continue
		}
		cacheKey := buildNormalizedCacheKey(entry.Song, entry.Artist, entry.Album, entry.Duration)
		s.addVideoID(ctx, cacheKey, entry.VideoID)
		processed++
	}

	respond(w, r).JSON(map[string]interface{}{
		"processed": processed,
		"total":     len(entries),
	})
}

// enrichMetadata expands stored metadata into a response object: parsed rawAttributes
// plus a lyrics sub-object describing the cached lyrics for this key.
func (s *Server) enrichMetadata(ctx context.Context, meta *store.SongMetadata) map[string]interface{} {
	if meta == nil {
		return nil
	}

	out := map[string]interface{}{
		"cacheKey":    meta.CacheKey,
		"trackName":   meta.TrackName,
		"artistName":  meta.ArtistName,
		"firstSeen":   meta.FirstSeen.Unix(),
		"lastUpdated": meta.LastUpdated.Unix(),
	}
	if len(meta.VideoIDs) > 0 {
		out["videoIds"] = meta.VideoIDs
	}
	if meta.AppleTrackID != "" {
		out["appleTrackId"] = meta.AppleTrackID
	}
	if meta.ISRC != "" {
		out["isrc"] = meta.ISRC
	}
	if meta.AlbumName != "" {
		out["albumName"] = meta.AlbumName
	}
	if meta.DurationMs != 0 {
		out["durationMs"] = meta.DurationMs
	}
	if meta.ReleaseDate != "" {
		out["releaseDate"] = meta.ReleaseDate
	}

	if meta.RawAttributes != "" {
		var attrs map[string]interface{}
		if err := json.Unmarshal([]byte(meta.RawAttributes), &attrs); err == nil {
			out["rawAttributes"] = attrs
		} else {
			out["rawAttributes"] = meta.RawAttributes
		}
	}

	lyricsInfo := map[string]interface{}{"cached": false}
	if cached, ok := s.getCachedLyrics(ctx, meta.CacheKey); ok {
		if cached.TTML == NoLyricsSentinel {
			lyricsInfo["cached"] = true
			lyricsInfo["noLyrics"] = true
		} else {
			lyricsInfo["cached"] = true
			lyricsInfo["noLyrics"] = false
			lyricsInfo["ttmlBytes"] = len(cached.TTML)
			lyricsInfo["trackDurationMs"] = cached.TrackDurationMs
			lyricsInfo["score"] = cached.Score
			lyricsInfo["language"] = cached.Language
			lyricsInfo["isRTL"] = cached.IsRTL
			out["language"] = cached.Language
		}
	}
	out["lyrics"] = lyricsInfo

	return out
}

func (s *Server) metadataLookupHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	videoID := r.URL.Query().Get("videoId")
	isrc := r.URL.Query().Get("isrc")
	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist")
	albumName := r.URL.Query().Get("al") + r.URL.Query().Get("album")
	durationStr := r.URL.Query().Get("d") + r.URL.Query().Get("duration")

	if videoID != "" {
		cacheKeys, _ := s.store.GetCacheKeysByVideoID(ctx, videoID)
		if len(cacheKeys) == 0 {
			respond(w, r).Error(http.StatusNotFound, map[string]interface{}{
				"error": "no metadata found for videoId: " + videoID,
			})
			return
		}
		results := make([]map[string]interface{}, 0, len(cacheKeys))
		for _, ck := range cacheKeys {
			if meta, ok := s.getSongMetadata(ctx, ck); ok {
				results = append(results, s.enrichMetadata(ctx, meta))
			}
		}
		respond(w, r).JSON(map[string]interface{}{
			"videoId": videoID,
			"results": results,
		})
		return
	}

	if isrc != "" {
		cacheKeys, _ := s.store.GetCacheKeysByISRC(ctx, isrc)
		if len(cacheKeys) == 0 {
			respond(w, r).Error(http.StatusNotFound, map[string]interface{}{
				"error": "no metadata found for isrc: " + isrc,
			})
			return
		}
		results := make([]map[string]interface{}, 0, len(cacheKeys))
		for _, ck := range cacheKeys {
			if meta, ok := s.getSongMetadata(ctx, ck); ok {
				results = append(results, s.enrichMetadata(ctx, meta))
			}
		}
		respond(w, r).JSON(map[string]interface{}{
			"isrc":    isrc,
			"results": results,
		})
		return
	}

	if songName == "" && artistName == "" {
		respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
			"error": "provide song+artist (s, a), videoId, or isrc",
		})
		return
	}

	cacheKey := buildNormalizedCacheKey(songName, artistName, albumName, durationStr)
	meta, ok := s.getSongMetadata(ctx, cacheKey)
	if !ok {
		cacheKeys, _ := s.store.GetCacheKeysBySongArtist(ctx, songName, artistName)
		if len(cacheKeys) == 0 {
			respond(w, r).Error(http.StatusNotFound, map[string]interface{}{
				"error":    "no metadata found",
				"cacheKey": cacheKey,
			})
			return
		}
		allVids := s.getAllVideoIDsForSong(ctx, songName, artistName)
		results := make([]map[string]interface{}, 0, len(cacheKeys))
		for _, ck := range cacheKeys {
			if m, ok := s.getSongMetadata(ctx, ck); ok {
				results = append(results, s.enrichMetadata(ctx, m))
			}
		}
		respond(w, r).JSON(map[string]interface{}{
			"cacheKey":     cacheKey,
			"allVideoIds":  allVids,
			"allCacheKeys": cacheKeys,
			"results":      results,
		})
		return
	}

	respond(w, r).JSON(map[string]interface{}{
		"cacheKey": cacheKey,
		"metadata": s.enrichMetadata(ctx, meta),
	})
}
