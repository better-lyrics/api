package binilink

import (
	"context"
	"net/http"
	"time"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/stats"

	log "github.com/sirupsen/logrus"
)

const (
	batchSize       = 50
	maxAttempts     = 10
	idleSleep       = 30 * time.Second
	unresolvedRetry = 7 * 24 * time.Hour
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
}

func Start(ctx context.Context, st *store.Store, s Settings) bool {
	if !s.Enabled || s.Key == "" {
		return false
	}
	u := &uploader{
		store:     st,
		client:    &http.Client{Timeout: 15 * time.Second},
		baseURL:   s.BaseURL,
		key:       s.Key,
		userAgent: s.UserAgent,
		interval:  s.Interval,
	}
	go u.run(ctx)
	return true
}

func (u *uploader) run(ctx context.Context) {
	for {
		stop, err := u.drain(ctx)
		if stop {
			log.Errorf("%s BiniLink ingress key rejected; uploader stopped", logcolors.LogBini)
			return
		}
		if err != nil {
			log.Warnf("%s BiniLink drain failed: %v", logcolors.LogBini, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(idleSleep):
		}
	}
}

func (u *uploader) drain(ctx context.Context) (stop bool, err error) {
	posted := false
	for {
		if ctx.Err() != nil {
			return false, nil
		}
		rows, err := u.store.ClaimBiniLinkPending(ctx, batchSize)
		if err != nil {
			return false, err
		}
		if len(rows) == 0 {
			return false, nil
		}
		for _, row := range rows {
			if ctx.Err() != nil || (posted && !u.pause(ctx)) {
				return false, nil
			}
			outcome, retryAfter := Post(u.client, u.baseURL, u.key, u.userAgent, row.VideoID, row.AppleTrackID)
			posted = true
			if outcome == OutcomeUnauthorized {
				return true, nil
			}
			if err := u.record(ctx, row, outcome, retryAfter); err != nil {
				return false, err
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
			status = string(OutcomeUnresolved)
		} else {
			status, next = "pending", now.Add(backoff(attempts, retryAfter))
		}
	case OutcomeUnresolved:
		if row.Attempts == 0 {
			status, next = "pending", now.Add(unresolvedRetry)
		}
	case OutcomeConflict:
		log.Warnf("%s BiniLink conflict: videoId %s already linked elsewhere (Apple ID %s)",
			logcolors.LogBini, row.VideoID, row.AppleTrackID)
	}
	return u.store.SetBiniLinkResult(ctx, row.VideoID, status, attempts, next)
}

func backoff(attempts int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	i := min(max(attempts-1, 0), len(backoffSchedule)-1)
	return backoffSchedule[i]
}
