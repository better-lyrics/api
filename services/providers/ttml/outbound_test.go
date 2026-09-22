package ttml

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lyrics-api-go/config"
)

var t0 = time.Unix(1000, 0)

func TestTokenBucketRefill(t *testing.T) {
	t.Run("does not overflow past capacity", func(t *testing.T) {
		b := newTokenBucket(5, 10, t0)
		b.refill(t0.Add(100 * time.Second))
		if b.tokens != 10 {
			t.Fatalf("tokens = %v, want 10", b.tokens)
		}
	})

	t.Run("adds tokens proportional to elapsed time", func(t *testing.T) {
		b := newTokenBucket(5, 10, t0)
		b.tokens = 2
		b.refill(t0.Add(1 * time.Second))
		if b.tokens != 7 {
			t.Fatalf("tokens = %v, want 7", b.tokens)
		}
	})

	t.Run("updates lastRefill to provided time", func(t *testing.T) {
		b := newTokenBucket(5, 10, t0)
		at := t0.Add(4 * time.Second)
		b.refill(at)
		if !b.lastRefill.Equal(at) {
			t.Fatalf("lastRefill = %v, want %v", b.lastRefill, at)
		}
	})

	t.Run("is a no-op when elapsed is zero or negative", func(t *testing.T) {
		b := newTokenBucket(5, 10, t0)
		b.tokens = 3
		b.refill(t0)
		if b.tokens != 3 {
			t.Fatalf("tokens after zero elapsed = %v, want 3", b.tokens)
		}
		b.refill(t0.Add(-500 * time.Millisecond))
		if b.tokens != 3 {
			t.Fatalf("tokens after negative elapsed = %v, want 3", b.tokens)
		}
	})

	t.Run("handles fractional refill", func(t *testing.T) {
		b := newTokenBucket(5, 10, t0)
		b.tokens = 0
		b.refill(t0.Add(500 * time.Millisecond))
		if b.tokens != 2.5 {
			t.Fatalf("tokens = %v, want 2.5", b.tokens)
		}
	})
}

func TestTokenBucketTryConsume(t *testing.T) {
	t.Run("succeeds when tokens available and decrements by one", func(t *testing.T) {
		b := newTokenBucket(1, 5, t0)
		if !b.tryConsume(t0, 0) {
			t.Fatal("expected consume to succeed")
		}
		if b.tokens != 4 {
			t.Fatalf("tokens = %v, want 4", b.tokens)
		}
	})

	t.Run("fails when bucket is empty", func(t *testing.T) {
		b := newTokenBucket(1, 2, t0)
		if !b.tryConsume(t0, 0) || !b.tryConsume(t0, 0) {
			t.Fatal("expected first two consumes to succeed")
		}
		if b.tryConsume(t0, 0) {
			t.Fatal("expected third consume to fail")
		}
		if b.tokens != 0 {
			t.Fatalf("tokens = %v, want 0", b.tokens)
		}
	})

	t.Run("refills before checking on each call", func(t *testing.T) {
		b := newTokenBucket(1, 2, t0)
		b.tryConsume(t0, 0)
		b.tryConsume(t0, 0)
		if b.tryConsume(t0, 0) {
			t.Fatal("expected empty bucket to fail")
		}
		if !b.tryConsume(t0.Add(1*time.Second), 0) {
			t.Fatal("expected refill to allow one more consume")
		}
	})

	t.Run("burst capacity is enforced", func(t *testing.T) {
		b := newTokenBucket(10, 3, t0)
		for i := 0; i < 3; i++ {
			if !b.tryConsume(t0, 0) {
				t.Fatalf("consume %d should succeed", i)
			}
		}
		if b.tryConsume(t0, 0) {
			t.Fatal("consume beyond burst should fail")
		}
	})
}

func TestTokenBucketMsUntilNextToken(t *testing.T) {
	t.Run("returns zero when tokens are available", func(t *testing.T) {
		b := newTokenBucket(1, 5, t0)
		if got := b.msUntilNextToken(t0, 0); got != 0 {
			t.Fatalf("got %v, want 0", got)
		}
	})

	t.Run("returns exact wait for one token when empty", func(t *testing.T) {
		b := newTokenBucket(2, 2, t0)
		b.tryConsume(t0, 0)
		b.tryConsume(t0, 0)
		if got := b.msUntilNextToken(t0, 0); got != 500*time.Millisecond {
			t.Fatalf("got %v, want 500ms", got)
		}
	})

	t.Run("accounts for partial refill since last call", func(t *testing.T) {
		b := newTokenBucket(1, 5, t0)
		for i := 0; i < 5; i++ {
			b.tryConsume(t0, 0)
		}
		if got := b.msUntilNextToken(t0.Add(500*time.Millisecond), 0); got != 500*time.Millisecond {
			t.Fatalf("got %v, want 500ms", got)
		}
	})

	t.Run("accounts for reserve", func(t *testing.T) {
		b := newTokenBucket(1, 5, t0)
		b.tokens = 2
		if got := b.msUntilNextToken(t0, 0); got != 0 {
			t.Fatalf("with reserve 0 got %v, want 0", got)
		}
		if got := b.msUntilNextToken(t0, 2); got != 1000*time.Millisecond {
			t.Fatalf("with reserve 2 got %v, want 1000ms", got)
		}
	})
}

func TestTokenBucketReserve(t *testing.T) {
	t.Run("reserve zero consumes down to the last token", func(t *testing.T) {
		b := newTokenBucket(1, 3, t0)
		for i := 0; i < 3; i++ {
			if !b.tryConsume(t0, 0) {
				t.Fatalf("consume %d should succeed", i)
			}
		}
		if b.tryConsume(t0, 0) {
			t.Fatal("fourth consume should fail")
		}
	})

	t.Run("reserve leaves that many tokens unconsumed", func(t *testing.T) {
		b := newTokenBucket(1, 3, t0)
		if !b.tryConsume(t0, 2) {
			t.Fatal("first reserved consume should succeed")
		}
		if b.tryConsume(t0, 2) {
			t.Fatal("second reserved consume should fail")
		}
		if b.tokens != 2 {
			t.Fatalf("tokens = %v, want 2 held for priority", b.tokens)
		}
	})

	t.Run("priority takes the reserved tokens after standard is blocked", func(t *testing.T) {
		b := newTokenBucket(1, 3, t0)
		b.tryConsume(t0, 2)
		if b.tryConsume(t0, 2) {
			t.Fatal("standard should be blocked at the reserve floor")
		}
		if !b.tryConsume(t0, 0) || !b.tryConsume(t0, 0) {
			t.Fatal("priority should drain the reserved tokens")
		}
	})
}

func TestNewOutboundLimiterDefaults(t *testing.T) {
	var c config.Config
	o := newOutboundLimiter(c, t0)

	cases := []struct {
		name         string
		b            *tokenBucket
		wantRate     float64
		wantCapacity float64
	}{
		{"account", o.account, defAccountRate, defAccountBurst},
		{"minted", o.minted, defMintedRate, defMintedBurst},
		{"scrape", o.scrape, defScrapeRate, defScrapeBurst},
		{"mint", o.mint, defMintRate, defMintBurst},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.b.refillPerSec != tc.wantRate {
				t.Errorf("rate = %v, want %v", tc.b.refillPerSec, tc.wantRate)
			}
			if tc.b.capacity != tc.wantCapacity {
				t.Errorf("capacity = %v, want %v", tc.b.capacity, tc.wantCapacity)
			}
		})
	}

	if o.maxWait != defOutboundMaxWait {
		t.Errorf("maxWait = %v, want %v", o.maxWait, defOutboundMaxWait)
	}
}

func TestNewOutboundLimiterHonorsConfig(t *testing.T) {
	var c config.Config
	c.Configuration.AccountRate = 7
	c.Configuration.AccountBurst = 9
	c.Configuration.OutboundMaxWaitMs = 1234
	o := newOutboundLimiter(c, t0)

	if o.account.refillPerSec != 7 || o.account.capacity != 9 {
		t.Errorf("account = %v/%v, want 7/9", o.account.refillPerSec, o.account.capacity)
	}
	if o.mint.refillPerSec != defMintRate || o.mint.capacity != defMintBurst {
		t.Errorf("unset mint should fall back to defaults, got %v/%v", o.mint.refillPerSec, o.mint.capacity)
	}
	if o.maxWait != 1234*time.Millisecond {
		t.Errorf("maxWait = %v, want 1234ms", o.maxWait)
	}
}

func TestOutboundLimiterBucketsIndependent(t *testing.T) {
	var c config.Config
	o := newOutboundLimiter(c, t0)

	before := o.minted.tokens
	for o.account.tryConsume(t0, 0) {
	}
	if o.account.tokens != 0 {
		t.Fatalf("account should be drained, tokens = %v", o.account.tokens)
	}
	if o.minted.tokens != before {
		t.Fatalf("draining account changed minted: %v, want %v", o.minted.tokens, before)
	}
}

func TestAcquire(t *testing.T) {
	t.Run("returns nil under the limit", func(t *testing.T) {
		o := &outboundLimiter{maxWait: 50 * time.Millisecond}
		b := newTokenBucket(1, 5, time.Now())
		if err := o.acquire(b, 0); err != nil {
			t.Fatalf("acquire under limit: %v", err)
		}
	})

	t.Run("fast-fails with errThrottled when exhausted", func(t *testing.T) {
		o := &outboundLimiter{maxWait: 20 * time.Millisecond}
		b := newTokenBucket(1, 1, time.Now())
		if !b.tryConsume(time.Now(), 0) {
			t.Fatal("setup consume should succeed")
		}
		start := time.Now()
		err := o.acquire(b, 0)
		if err != errThrottled {
			t.Fatalf("err = %v, want errThrottled", err)
		}
		if waited := time.Since(start); waited > 500*time.Millisecond {
			t.Fatalf("fast-fail waited too long: %v", waited)
		}
	})

	t.Run("waits and succeeds when a token refills within the cap", func(t *testing.T) {
		o := &outboundLimiter{maxWait: 1 * time.Second}
		b := newTokenBucket(100, 1, time.Now())
		b.tryConsume(time.Now(), 0)
		if err := o.acquire(b, 0); err != nil {
			t.Fatalf("acquire after refill: %v", err)
		}
	})
}

func TestTokenBucketConcurrentNoOverspend(t *testing.T) {
	const burst = 100
	b := newTokenBucket(1, burst, t0)

	var wg sync.WaitGroup
	var granted int64
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.tryConsume(t0, 0) {
				atomic.AddInt64(&granted, 1)
			}
		}()
	}
	wg.Wait()

	if granted != burst {
		t.Fatalf("granted = %d, want exactly %d (no double-spend under concurrency)", granted, burst)
	}
}

func TestAcquireAccountReserve(t *testing.T) {
	o := &outboundLimiter{
		account: newTokenBucket(1, 3, time.Now()),
		reserve: 2,
		maxWait: 20 * time.Millisecond,
	}

	if err := o.acquireAccount(false); err != nil {
		t.Fatalf("first standard acquire: %v", err)
	}
	if err := o.acquireAccount(false); err != errThrottled {
		t.Fatalf("standard past reserve floor: err = %v, want errThrottled", err)
	}
	if err := o.acquireAccount(true); err != nil {
		t.Fatalf("first priority acquire: %v", err)
	}
	if err := o.acquireAccount(true); err != nil {
		t.Fatalf("second priority acquire: %v", err)
	}
}
