package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/cdn"
	"lyrics-api-go/services/providers"
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
	// lrcRedCheckedMax bounds the memory of checked keys; past it the set resets and keys are checked once more.
	lrcRedCheckedMax = 200_000
)

// lrcRedCheckLimiter is shared by the sweep and the cache-hit check, so together they read lrc.red at 2/s.
var lrcRedCheckLimiter = rate.NewLimiter(2, 1)

var (
	lrcRedCheckedKeys  sync.Map
	lrcRedCheckedCount atomic.Int64
)

// lrcRedCopyIsModelSynced compares bytes because byModel describes lrc.red's file today, not a copy cached before an AI sync.
func (s *Server) lrcRedCopyIsModelSynced(ctx context.Context, cacheKey, isrc string) (synced, changed bool, err error) {
	byModel, err := ttml.LRCRedByModel(ctx, isrc)
	if err != nil || !byModel {
		return false, false, err
	}
	cached, ok, err := s.store.GetLyricsExact(ctx, cacheKey)
	if err != nil || !ok {
		return false, false, err
	}
	current, found, err := ttml.LRCRedTTML(ctx, isrc)
	if err != nil {
		return false, false, err
	}
	if !found {
		return false, false, fmt.Errorf("lrc.red flags %s as model-synced but has no .ttml", isrc)
	}
	if current != cached.TTML {
		return false, true, nil
	}
	return true, false, nil
}

func (s *Server) dropLRCRedRow(ctx context.Context, cacheKey, isrc string) (bool, error) {
	ok, err := s.store.DeleteLRCRedLyrics(ctx, cacheKey)
	if err != nil || !ok {
		return false, err
	}
	cdn.Purge(store.KeyCacheTag(cacheKey), store.KeySongCacheTag(cacheKey))
	log.Infof("%s Deleted model-synced lrc.red lyrics for ISRC %s: %s", logcolors.LogCacheClear, isrc, cacheKey)
	return true, nil
}

// checkLRCRedCopy checks each key once per process: a cached copy never changes, so a second check learns nothing.
func (s *Server) checkLRCRedCopy(cacheKey string, cached store.CachedLyrics) {
	if cached.Source != providers.SourceLRCRed || ttml.AISyncCheckReady() != nil {
		return
	}
	if _, seen := lrcRedCheckedKeys.LoadOrStore(cacheKey, struct{}{}); seen {
		return
	}
	if lrcRedCheckedCount.Add(1) > lrcRedCheckedMax {
		lrcRedCheckedKeys.Clear()
		lrcRedCheckedCount.Store(0)
	}
	if !lrcRedCheckLimiter.Allow() {
		lrcRedCheckedKeys.Delete(cacheKey)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		meta, ok, err := s.store.GetSongMetadata(ctx, cacheKey)
		if err != nil {
			lrcRedCheckedKeys.Delete(cacheKey)
			log.Warnf("%s lrc.red check could not read metadata for %s: %v", logcolors.LogCacheClear, cacheKey, err)
			return
		}
		if !ok || utils.NormalizeISRC(meta.ISRC) == "" {
			return
		}
		isrc := utils.NormalizeISRC(meta.ISRC)
		synced, _, err := s.lrcRedCopyIsModelSynced(ctx, cacheKey, isrc)
		if err != nil {
			lrcRedCheckedKeys.Delete(cacheKey)
			log.Warnf("%s lrc.red check failed for ISRC %s: %v", logcolors.LogCacheClear, isrc, err)
			return
		}
		if !synced {
			return
		}
		if _, err := s.dropLRCRedRow(ctx, cacheKey, isrc); err != nil {
			lrcRedCheckedKeys.Delete(cacheKey)
			log.Warnf("%s lrc.red check could not delete %s: %v", logcolors.LogCacheClear, cacheKey, err)
		}
	}()
}

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
	modelSynced, keptChanged, skippedNoISRC, sweepErrors := []string{}, []string{}, []string{}, []string{}
	failedKey := ""

	handled := func() int { return checked + len(skippedNoISRC) }

scan:
	for handled() < limit && time.Since(start) < lrcRedSweepBudget {
		rows, next, err := s.store.ScanLRCRedLyrics(ctx, cursor, lrcRedSweepPageSize)
		if err != nil {
			log.Errorf("%s lrc.red sweep scan after %q failed: %v", logcolors.LogCacheClear, cursor, err)
			sweepErrors = append(sweepErrors, fmt.Sprintf("scan after %q failed: %v", cursor, err))
			break
		}
		for _, row := range rows {
			if handled() == limit || time.Since(start) >= lrcRedSweepBudget {
				break scan
			}
			isrc := utils.NormalizeISRC(row.ISRC)
			if isrc == "" {
				skippedNoISRC = append(skippedNoISRC, row.CacheKey)
				cursor = row.CacheKey
				continue
			}
			if err := lrcRedCheckLimiter.Wait(ctx); err != nil {
				sweepErrors = append(sweepErrors, fmt.Sprintf("stopped before %s: %v", row.CacheKey, err))
				break scan
			}
			checked++
			synced, changed, err := s.lrcRedCopyIsModelSynced(ctx, row.CacheKey, isrc)
			if err != nil {
				failedKey = row.CacheKey
				sweepErrors = append(sweepErrors, fmt.Sprintf("%s (%s): %v", isrc, row.CacheKey, err))
				break scan
			}
			if changed {
				keptChanged = append(keptChanged, isrc)
			}
			if synced {
				modelSynced = append(modelSynced, isrc)
			}
			if synced && !dryRun {
				ok, err := s.dropLRCRedRow(ctx, row.CacheKey, isrc)
				if err != nil {
					failedKey = row.CacheKey
					sweepErrors = append(sweepErrors, fmt.Sprintf("%s (%s): delete failed: %v", isrc, row.CacheKey, err))
					break scan
				}
				if ok {
					deleted++
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
		"kept_changed":    keptChanged,
		"failed_key":      failedKey,
		"deleted":         deleted,
		"skipped_no_isrc": skippedNoISRC,
		"errors":          sweepErrors,
		"next_after":      cursor,
		"done":            done,
	})
}
