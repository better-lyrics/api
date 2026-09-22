package syncupgrade

import (
	"context"
	"time"

	"lyrics-api-go/config"
	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/bini"
	ttml "lyrics-api-go/services/providers/ttml"
	"lyrics-api-go/services/proxy"

	log "github.com/sirupsen/logrus"
)

type condFetch func(trackID, ifNoneMatch string) (lyricsTTML string, etag string, notModified bool, err error)

type Action struct {
	Bump      bool
	Upgraded  bool
	NewTTML   string
	NewETag   string
	NewTiming string
}

func decideCandidate(c store.SyncUpgradeCandidate, fetch condFetch, timingOf func(string) string) (Action, error) {
	oldTiming := timingOf(c.TTML)
	if syncRank(oldTiming) >= syncRank("word") {
		return Action{Bump: true, NewTiming: oldTiming, NewETag: c.AppleETag}, nil
	}

	body, etag, notModified, err := fetch(c.AppleTrackID, c.AppleETag)
	if err != nil {
		return Action{Bump: true, NewTiming: oldTiming, NewETag: c.AppleETag}, err
	}
	if notModified {
		return Action{Bump: true, NewTiming: oldTiming, NewETag: c.AppleETag}, nil
	}

	newTiming := timingOf(body)
	if isUpgrade(oldTiming, newTiming) {
		return Action{Upgraded: true, NewTTML: body, NewETag: etag, NewTiming: newTiming}, nil
	}
	return Action{Bump: true, NewTiming: oldTiming, NewETag: etag}, nil
}

func syncUpgradeInterval(mins int) time.Duration {
	if mins <= 0 {
		mins = 360
	}
	return time.Duration(mins) * time.Minute
}

func StartSyncUpgradeDetector(st *store.Store, cfg config.Config) {
	conf := cfg.Configuration
	if !conf.SyncUpgradeEnabled {
		return
	}

	interval := syncUpgradeInterval(conf.SyncUpgradeIntervalMins)
	windowDays := conf.SyncUpgradeWindowDays
	batchLimit := conf.SyncUpgradeBatchLimit
	fetch := func(trackID, ifNoneMatch string) (string, string, bool, error) {
		return ttml.FetchLyricsByTrackIDConditional(trackID, false, ifNoneMatch)
	}

	run := func() { runSyncUpgradePass(st, windowDays, batchLimit, fetch) }
	go run()

	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			run()
		}
	}()
}

func runSyncUpgradePass(st *store.Store, windowDays, batchLimit int, fetch condFetch) {
	ctx := context.Background()
	now := time.Now()
	windowStart := now.AddDate(0, 0, -windowDays)

	cands, err := st.SelectSyncUpgradeCandidates(ctx, windowStart, batchLimit)
	if err != nil {
		log.Warnf("%s Sync-upgrade candidate query failed: %v", logcolors.LogLyrics, err)
		return
	}

	for _, cand := range cands {
		if !checkDue(cand.ReleaseDate, cand.LastChecked, now, windowDays) {
			continue
		}
		act, err := decideCandidate(cand, fetch, ttml.TimingType)
		if err != nil {
			log.Warnf("%s Sync-upgrade check failed for %s: %v", logcolors.LogLyrics, cand.CacheKey, err)
		}
		persistSyncUpgrade(ctx, st, cand, act)
	}
}

func persistSyncUpgrade(ctx context.Context, st *store.Store, cand store.SyncUpgradeCandidate, act Action) {
	now := time.Now().UTC()

	if !act.Upgraded {
		timing := cand.TimingType
		etag := cand.AppleETag
		if act.NewTiming != "" {
			timing = act.NewTiming
		}
		if act.NewETag != "" {
			etag = act.NewETag
		}
		if err := st.SetLyricsSyncState(ctx, cand.CacheKey, timing, etag, now); err != nil {
			log.Warnf("%s Sync-state update failed for %s: %v", logcolors.LogLyrics, cand.CacheKey, err)
		}
		return
	}

	existing, ok, err := st.GetLyricsExact(ctx, cand.CacheKey)
	if err != nil || !ok {
		log.Warnf("%s Sync-upgrade load failed for %s (ok=%v): %v", logcolors.LogLyrics, cand.CacheKey, ok, err)
		return
	}
	existing.TTML = act.NewTTML
	existing.TimingType = act.NewTiming
	existing.AppleETag = act.NewETag
	existing.Source = ttml.SourceApple
	existing.LastCheckedAt = &now
	if err := st.SetLyrics(ctx, cand.CacheKey, store.DeriveKey(cand.CacheKey), existing); err != nil {
		log.Warnf("%s Sync-upgrade write failed for %s: %v", logcolors.LogLyrics, cand.CacheKey, err)
		return
	}
	log.Infof("%s Sync-upgrade applied for %s: %s to %s", logcolors.LogSuccess, cand.CacheKey, cand.TimingType, act.NewTiming)

	go bini.Contribute(cand.Name, cand.Artist, cand.ISRC, ttml.SourceApple, "", act.NewTTML)
	videoIDs := func(song, artist string) []string {
		v, _ := st.GetAllVideoIDsForSong(ctx, song, artist)
		return v
	}
	proxy.RevalidateAllForSong(cand.Name, cand.Artist, cand.Album, cand.DurationMs/1000, videoIDs)
}
