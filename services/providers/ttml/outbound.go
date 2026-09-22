package ttml

import (
	"errors"
	"math"
	"sync"
	"time"

	"lyrics-api-go/config"
)

const (
	defAccountRate, defAccountBurst = 2, 10
	defMintedRate, defMintedBurst   = 5, 20
	defScrapeRate, defScrapeBurst   = 1, 5
	defMintRate, defMintBurst       = 4, 6
	defAccountReserve               = 2
	defOutboundMaxWait              = 2 * time.Second
)

var errThrottled = errors.New("outbound throttle: rate limit exceeded")

type tokenBucket struct {
	mu           sync.Mutex
	capacity     float64
	refillPerSec float64
	tokens       float64
	lastRefill   time.Time
}

func newTokenBucket(ratePerSec, burst int, now time.Time) *tokenBucket {
	return &tokenBucket{
		capacity:     float64(burst),
		refillPerSec: float64(ratePerSec),
		tokens:       float64(burst),
		lastRefill:   now,
	}
}

func (b *tokenBucket) refill(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked(now)
}

func (b *tokenBucket) refillLocked(now time.Time) {
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens = math.Min(b.capacity, b.tokens+elapsed*b.refillPerSec)
	b.lastRefill = now
}

func (b *tokenBucket) tryConsume(now time.Time, reserve float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked(now)
	if b.tokens >= 1+reserve {
		b.tokens--
		return true
	}
	return false
}

func (b *tokenBucket) msUntilNextToken(now time.Time, reserve float64) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked(now)
	need := 1 + reserve
	if b.tokens >= need {
		return 0
	}
	ms := math.Ceil((need - b.tokens) * 1000 / b.refillPerSec)
	return time.Duration(ms) * time.Millisecond
}

type outboundLimiter struct {
	account *tokenBucket
	minted  *tokenBucket
	scrape  *tokenBucket
	mint    *tokenBucket
	reserve float64
	maxWait time.Duration
}

func clampRate(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func newOutboundLimiter(cfg config.Config, now time.Time) *outboundLimiter {
	c := cfg.Configuration
	maxWait := defOutboundMaxWait
	if c.OutboundMaxWaitMs > 0 {
		maxWait = time.Duration(c.OutboundMaxWaitMs) * time.Millisecond
	}
	reserve := c.AccountPriorityReserve
	if reserve < 0 {
		reserve = 0
	}
	return &outboundLimiter{
		account: newTokenBucket(clampRate(c.AccountRate, defAccountRate), clampRate(c.AccountBurst, defAccountBurst), now),
		minted:  newTokenBucket(clampRate(c.MintedRate, defMintedRate), clampRate(c.MintedBurst, defMintedBurst), now),
		scrape:  newTokenBucket(clampRate(c.ScrapeRate, defScrapeRate), clampRate(c.ScrapeBurst, defScrapeBurst), now),
		mint:    newTokenBucket(clampRate(c.MintRate, defMintRate), clampRate(c.MintBurst, defMintBurst), now),
		reserve: float64(reserve),
		maxWait: maxWait,
	}
}

func (o *outboundLimiter) acquire(b *tokenBucket, reserve float64) error {
	deadline := time.Now().Add(o.maxWait)
	for {
		now := time.Now()
		if b.tryConsume(now, reserve) {
			return nil
		}
		if !now.Before(deadline) {
			return errThrottled
		}
		wait := b.msUntilNextToken(now, reserve)
		if remaining := deadline.Sub(now); wait > remaining {
			wait = remaining
		}
		if wait <= 0 {
			wait = time.Millisecond
		}
		time.Sleep(wait)
	}
}

func (o *outboundLimiter) acquireAccount(priority bool) error {
	reserve := o.reserve
	if priority {
		reserve = 0
	}
	return o.acquire(o.account, reserve)
}

func (o *outboundLimiter) acquireMinted() error { return o.acquire(o.minted, 0) }
func (o *outboundLimiter) acquireScrape() error { return o.acquire(o.scrape, 0) }
func (o *outboundLimiter) acquireMint() error   { return o.acquire(o.mint, 0) }

var (
	outboundOnce sync.Once
	outboundInst *outboundLimiter
)

func getOutbound() *outboundLimiter {
	outboundOnce.Do(func() {
		outboundInst = newOutboundLimiter(config.Get(), time.Now())
	})
	return outboundInst
}
