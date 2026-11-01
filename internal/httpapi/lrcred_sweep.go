package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
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

type checkedKeys struct {
	mu  sync.Mutex
	m   map[string]struct{}
	max int
}

func (c *checkedKeys) seen(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.m[key]
	return ok
}

func (c *checkedKeys) claim(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[key]; ok {
		return false
	}
	if c.m == nil || len(c.m) >= c.max {
		c.m = make(map[string]struct{})
	}
	c.m[key] = struct{}{}
	return true
}

func (c *checkedKeys) release(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

// lrcRedCheckBlocked also refuses in cache-only mode, where a deleted row could never be fetched again.
func (s *Server) lrcRedCheckBlocked() error {
	if s.cfg.FeatureFlags.CacheOnlyMode {
		return fmt.Errorf("FF_CACHE_ONLY_MODE is on; a deleted row could not be fetched again")
	}
	return ttml.AISyncCheckReady()
}

// lrcRedCheckLimiter is shared by the sweep and the cache-hit check, so together they read lrc.red at 2/s.
var lrcRedCheckLimiter = rate.NewLimiter(2, 1)

var lrcRedChecked = &checkedKeys{max: lrcRedCheckedMax}

// lrcRedCopyIsModelSynced compares bytes because byModel describes lrc.red's file today, not a copy cached before an AI sync.
func (s *Server) lrcRedCopyIsModelSynced(ctx context.Context, cacheKey, isrc string) (aiTTML string, changed bool, err error) {
	byModel, err := ttml.LRCRedByModel(ctx, isrc)
	if err != nil || !byModel {
		return "", false, err
	}
	cached, ok, err := s.store.GetLyricsExact(ctx, cacheKey)
	if err != nil || !ok {
		return "", false, err
	}
	current, found, err := ttml.LRCRedTTML(ctx, isrc)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, fmt.Errorf("lrc.red flags %s as model-synced but has no .ttml", isrc)
	}
	if current != cached.TTML {
		return "", true, nil
	}
	return current, false, nil
}

func (s *Server) dropLRCRedRow(ctx context.Context, cacheKey, isrc, aiTTML string) (bool, error) {
	ok, err := s.store.DeleteLyricsIfUnchanged(ctx, cacheKey, providers.SourceLRCRed, aiTTML)
	if err != nil || !ok {
		return false, err
	}
	cdn.Purge(store.KeyCacheTag(cacheKey), store.KeySongCacheTag(cacheKey))
	log.Infof("%s Deleted model-synced lrc.red lyrics for ISRC %s: %s", logcolors.LogCacheClear, isrc, cacheKey)
	return true, nil
}

// checkLRCRedCopy checks each key once per process: a cached copy never changes, so a second check learns nothing.
func (s *Server) checkLRCRedCopy(cacheKey string, cached store.CachedLyrics) {
	if cached.Source != providers.SourceLRCRed || lrcRedChecked.seen(cacheKey) || s.lrcRedCheckBlocked() != nil {
		return
	}
	if !lrcRedChecked.claim(cacheKey) {
		return
	}
	if !lrcRedCheckLimiter.Allow() {
		lrcRedChecked.release(cacheKey)
		return
	}
	// A failed check is not retried in this process, so one hot failing key cannot take every token; the sweep covers it.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		meta, ok, err := s.store.GetSongMetadata(ctx, cacheKey)
		if err != nil {
			log.Warnf("%s lrc.red check could not read metadata for %s: %v", logcolors.LogCacheClear, cacheKey, err)
			return
		}
		if !ok || utils.NormalizeISRC(meta.ISRC) == "" {
			return
		}
		isrc := utils.NormalizeISRC(meta.ISRC)
		aiTTML, _, err := s.lrcRedCopyIsModelSynced(ctx, cacheKey, isrc)
		if err != nil {
			log.Warnf("%s lrc.red check failed for ISRC %s: %v", logcolors.LogCacheClear, isrc, err)
			return
		}
		if aiTTML == "" {
			return
		}
		if _, err := s.dropLRCRedRow(ctx, cacheKey, isrc, aiTTML); err != nil {
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
	if err := s.lrcRedCheckBlocked(); err != nil {
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
		rows, next, err := s.store.ScanLyricsBySource(ctx, providers.SourceLRCRed, cursor, lrcRedSweepPageSize)
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
			aiTTML, changed, err := s.lrcRedCopyIsModelSynced(ctx, row.CacheKey, isrc)
			if err != nil {
				failedKey = row.CacheKey
				sweepErrors = append(sweepErrors, fmt.Sprintf("%s (%s): %v", isrc, row.CacheKey, err))
				break scan
			}
			if changed {
				keptChanged = append(keptChanged, isrc)
			}
			if aiTTML != "" && dryRun {
				modelSynced = append(modelSynced, isrc)
			}
			if aiTTML != "" && !dryRun {
				ok, err := s.dropLRCRedRow(ctx, row.CacheKey, isrc, aiTTML)
				if err != nil {
					failedKey = row.CacheKey
					sweepErrors = append(sweepErrors, fmt.Sprintf("%s (%s): delete failed: %v", isrc, row.CacheKey, err))
					break scan
				}
				if ok {
					deleted++
					modelSynced = append(modelSynced, isrc)
				} else {
					keptChanged = append(keptChanged, isrc)
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
