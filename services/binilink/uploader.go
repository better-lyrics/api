package binilink

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/stats"

	log "github.com/sirupsen/logrus"
)

const (
	batchSize       = 50
	maxAttempts     = 10
	unresolvedRetry = 7 * 24 * time.Hour
	statusFailed    = "failed"
)

var backoffSchedule = []time.Duration{
	time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	24 * time.Hour,
}

type Settings struct {
	Enabled   bool
	Key       string
	BaseURL   string
	UserAgent string
	Interval  time.Duration
}

type uploader struct {
	store     *store.Store
	client    *http.Client
	baseURL   string
	key       string
	userAgent string
	interval  time.Duration
	lock      *store.BiniLinkWorkerLock
}

var errLockLost = errors.New("worker lock lost")

func Start(ctx context.Context, st *store.Store, s Settings) bool {
	if !s.Enabled {
		return false
	}
	if s.Key == "" {
		log.Warnf("%s FF_BINILINK_INGRESS is on but BINILINK_INGRESS_KEY is empty; links queue but nothing sends them", logcolors.LogBini)
		return false
	}
	baseURL, err := normalizeBaseURL(s.BaseURL)
	if err != nil {
		log.Errorf("%s BiniLink uploader not started: %v", logcolors.LogBini, err)
		return false
	}
	u := &uploader{
		store:     st,
		client:    newHTTPClient(),
		baseURL:   baseURL,
		key:       s.Key,
		userAgent: s.UserAgent,
		interval:  s.Interval,
	}
	go u.run(ctx)
	return true
}

func normalizeBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
		parsed.User != nil || strings.ContainsAny(raw, "?#") {
		return "", errors.New("BINILINK_BASE_URL must be an http(s) origin with an optional path, without credentials, query or fragment")
	}
	return strings.TrimRight(raw, "/"), nil
}

var (
	idleSleep         = 30 * time.Second
	lockRetryInterval = 30 * time.Second
)

func (u *uploader) run(ctx context.Context) {
	for {
		lock, ok := u.waitForLock(ctx)
		if !ok {
			return
		}
		finished := u.runLocked(ctx, lock)
		lock.Release()
		if finished {
			return
		}
	}
}

// runLocked drains while the lock is held. It returns false when the lock was lost and must be retaken.
func (u *uploader) runLocked(ctx context.Context, lock *store.BiniLinkWorkerLock) (finished bool) {
	u.lock = lock
	defer func() { u.lock = nil }()
	for {
		stop, pause, err := u.drain(ctx)
		if stop {
			log.Errorf("%s BiniLink ingress key rejected; uploader stopped", logcolors.LogBini)
			return true
		}
		if errors.Is(err, errLockLost) {
			if ctx.Err() != nil {
				return true
			}
			log.Warnf("%s BiniLink worker lock lost; waiting to retake it", logcolors.LogBini)
			return false
		}
		if err != nil {
			log.Warnf("%s BiniLink drain failed: %v", logcolors.LogBini, err)
		}
		select {
		case <-ctx.Done():
			return true
		case <-time.After(runWait(pause)):
		}
	}
}

func (u *uploader) waitForLock(ctx context.Context) (*store.BiniLinkWorkerLock, bool) {
	for {
		lock, ok, err := u.store.TryBiniLinkWorkerLock(ctx)
		if err != nil {
			log.Warnf("%s BiniLink worker lock failed: %v", logcolors.LogBini, err)
		} else if ok {
			return lock, true
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(lockRetryInterval):
		}
	}
}

func runWait(pause time.Duration) time.Duration {
	return max(pause, idleSleep)
}

// A retryable reply ends the pass so a busy server gets no more requests; pause is its Retry-After.
func (u *uploader) drain(ctx context.Context) (stop bool, pause time.Duration, err error) {
	posted := false
	for {
		if ctx.Err() != nil {
			return false, 0, nil
		}
		if u.lock != nil && !u.lock.Held(ctx) {
			return false, 0, errLockLost
		}
		rows, err := u.store.ClaimBiniLinkPending(ctx, batchSize)
		if err != nil {
			return false, 0, err
		}
		if len(rows) == 0 {
			return false, 0, nil
		}
		for _, row := range rows {
			if ctx.Err() != nil || (posted && !u.pause(ctx)) {
				return false, 0, nil
			}
			outcome, retryAfter := Post(u.client, u.baseURL, u.key, u.userAgent, row.VideoID, row.AppleTrackID)
			posted = true
			if outcome == OutcomeUnauthorized {
				stats.Get().RecordBiniLink(string(outcome))
				return true, 0, nil
			}
			if err := u.record(ctx, row, outcome, retryAfter); err != nil {
				return false, 0, err
			}
			if outcome == OutcomeRetry {
				log.Warnf("%s BiniLink busy or unreachable; pausing for %v", logcolors.LogBini, runWait(retryAfter))
				return false, retryAfter, nil
			}
		}
	}
}

func (u *uploader) pause(ctx context.Context) bool {
	if u.interval <= 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(u.interval):
		return true
	}
}

func (u *uploader) record(ctx context.Context, row store.BiniLinkPending, outcome Outcome, retryAfter time.Duration) error {
	stats.Get().RecordBiniLink(string(outcome))
	attempts := row.Attempts + 1
	now := time.Now()

	status, next := string(outcome), now
	switch outcome {
	case OutcomeRetry:
		if attempts >= maxAttempts {
			status = statusFailed
		} else {
			status, next = "pending", now.Add(backoff(attempts, retryAfter))
		}
	case OutcomeUnresolved:
		if !row.UnresolvedRetried {
			return u.store.MarkBiniLinkUnresolvedRetry(ctx, row.VideoID, attempts, now.Add(unresolvedRetry))
		}
	case OutcomeConflict:
		log.Warnf("%s BiniLink conflict: videoId %s already linked elsewhere (Apple ID %s)",
			logcolors.LogBini, row.VideoID, row.AppleTrackID)
	}
	return u.store.SetBiniLinkResult(ctx, row.VideoID, status, attempts, next)
}

func backoff(attempts int, retryAfter time.Duration) time.Duration {
	i := min(max(attempts-1, 0), len(backoffSchedule)-1)
	return min(max(backoffSchedule[i], retryAfter), maxRetryAfter)
}
