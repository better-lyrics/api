package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/cdn"
	ttml "lyrics-api-go/services/providers/ttml"
	"lyrics-api-go/utils"

	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

const (
	lrcRedSweepPageSize     = 1000
	lrcRedSweepDefaultLimit = 20
	lrcRedSweepMaxLimit     = 50
	// lrcRedSweepBudget plus the 20s grace in lrcRedSweep stays under the server's 60s WriteTimeout.
	lrcRedSweepBudget = 30 * time.Second
)

// lrcRedSweepLimiter is shared by every call, so parallel sweeps still read lrc.red at 2/s.
var lrcRedSweepLimiter = rate.NewLimiter(2, 1)

// lrcRedSweep deletes cached rows whose lrc.red file was synced by a model; a failed row stops the batch so resuming retries it.
func (s *Server) lrcRedSweep(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	q := r.URL.Query()
	limit := lrcRedSweepDefaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > lrcRedSweepMaxLimit {
			respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
				"error": fmt.Sprintf("limit must be between 1 and %d", lrcRedSweepMaxLimit),
			})
			return
		}
		limit = n
	}
	dryRun := false
	if v, set := q["dry_run"]; set {
		b, err := strconv.ParseBool(v[0])
		if err != nil {
			respond(w, r).Error(http.StatusBadRequest, map[string]interface{}{
				"error": "dry_run must be true or false",
			})
			return
		}
		dryRun = b
	}
	if err := ttml.AISyncCheckReady(); err != nil {
		respond(w, r).Error(http.StatusConflict, map[string]interface{}{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lrcRedSweepBudget+20*time.Second)
	defer cancel()
	start := time.Now()

	cursor := q.Get("after")
	checked, deleted, done := 0, 0, false
	modelSynced, skippedNoISRC, sweepErrors := []string{}, []string{}, []string{}

scan:
	for time.Since(start) < lrcRedSweepBudget {
		rows, next, err := s.store.ScanLRCRedLyrics(ctx, cursor, lrcRedSweepPageSize)
		if err != nil {
			log.Errorf("%s lrc.red sweep scan after %q failed: %v", logcolors.LogCacheClear, cursor, err)
			respond(w, r).Error(http.StatusInternalServerError, map[string]interface{}{
				"error": fmt.Sprintf("scan failed: %v", err),
			})
			return
		}
		for _, row := range rows {
			if checked == limit || time.Since(start) >= lrcRedSweepBudget {
				break scan
			}
			isrc := utils.NormalizeISRC(row.ISRC)
			if isrc == "" {
				skippedNoISRC = append(skippedNoISRC, row.CacheKey)
				cursor = row.CacheKey
				continue
			}
			if err := lrcRedSweepLimiter.Wait(ctx); err != nil {
				break scan
			}
			checked++
			byModel, err := ttml.LRCRedByModel(ctx, isrc)
			if err != nil {
				sweepErrors = append(sweepErrors, fmt.Sprintf("%s (%s): %v", isrc, row.CacheKey, err))
				break scan
			}
			if byModel {
				modelSynced = append(modelSynced, isrc)
			}
			if byModel && !dryRun {
				ok, err := s.store.DeleteLRCRedLyrics(ctx, row.CacheKey)
				if err != nil {
					sweepErrors = append(sweepErrors, fmt.Sprintf("%s (%s): delete failed: %v", isrc, row.CacheKey, err))
					break scan
				}
				if ok {
					deleted++
					cdn.Purge(store.KeyCacheTag(row.CacheKey), store.KeySongCacheTag(row.CacheKey))
					log.Infof("%s Deleted model-synced lrc.red lyrics for ISRC %s: %s", logcolors.LogCacheClear, isrc, row.CacheKey)
				}
			}
			cursor = row.CacheKey
		}
		if next == "" {
			done = true
			cursor = ""
			break
		}
		cursor = next
	}

	log.Infof("%s lrc.red sweep: dry_run=%v checked=%d model_synced=%d deleted=%d errors=%d next_after=%q",
		logcolors.LogCacheClear, dryRun, checked, len(modelSynced), deleted, len(sweepErrors), cursor)
	respond(w, r).JSON(map[string]interface{}{
		"dry_run":         dryRun,
		"checked":         checked,
		"model_synced":    modelSynced,
		"deleted":         deleted,
		"skipped_no_isrc": skippedNoISRC,
		"errors":          sweepErrors,
		"next_after":      cursor,
		"done":            done,
	})
}
